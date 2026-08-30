package attempt

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/scoring"
	"github.com/qwish/backend/internal/domain/streak"
)

type Service struct {
	db        *pgxpool.Pool
	quizSvc   *quiz.Service
	streakSvc *streak.Service
	notifSvc  *notification.Service
}

func NewService(db *pgxpool.Pool, quizSvc *quiz.Service, streakSvc *streak.Service) *Service {
	return &Service{db: db, quizSvc: quizSvc, streakSvc: streakSvc}
}

// SetNotifier wires the in-app notification emitter. Optional — if unset, emits no-op.
func (s *Service) SetNotifier(n *notification.Service) { s.notifSvc = n }

type StartAttemptResp struct {
	AttemptID string                    `json:"attempt_id"`
	QuizID    string                    `json:"quiz_id"`
	Questions []quiz.QuestionForStudent `json:"questions"`
}

type AnswerReq struct {
	QuestionID      string          `json:"question_id"`
	Answer          json.RawMessage `json:"answer"`
	ConfidenceLevel string          `json:"confidence_level"`

	// TimeTakenMs, CluesUsed and ComboLevel used to arrive from the client and
	// were fed straight into scoring — all three are now derived server-side and
	// any value sent here is ignored. Kept only so old clients still parse.
	TimeTakenMs int `json:"time_taken_ms"`
	CluesUsed   int `json:"clues_used"`
	ComboLevel  int `json:"combo_level"`
}

type AnswerResp struct {
	IsCorrect     bool            `json:"is_correct"`
	CorrectAnswer json.RawMessage `json:"correct_answer"`
	PointsEarned  int64           `json:"points_earned"`
	ComboLevel    int             `json:"combo_level"`
	TimeTakenMs   int             `json:"time_taken_ms"`
	// TimedOut is true when the answer landed past the question's time limit.
	// Such answers are recorded but always score zero.
	TimedOut bool `json:"timed_out"`
}

// answerGraceMs pads the per-question time limit to absorb network latency and
// client/server clock skew before an answer is rejected as late.
const answerGraceMs = 2000

// applyServerGates applies the two adjustments the server owns outright, after
// scoring: an answer past the time limit is void, and the combo counter advances
// only on a genuinely correct answer. Both used to ride on client-supplied
// values. timeTakenMs is measured against the DB clock by the caller.
func applyServerGates(isCorrect bool, pts int64, timeTakenMs, timeLimitSeconds, comboLevel int) (correct bool, points int64, timedOut bool, newCombo int) {
	timedOut = timeLimitSeconds > 0 && timeTakenMs > timeLimitSeconds*1000+answerGraceMs
	if timedOut {
		isCorrect, pts = false, 0
	}
	if isCorrect {
		newCombo = comboLevel + 1
	}
	return isCorrect, pts, timedOut, newCombo
}

type CompleteResp struct {
	AttemptID          string                  `json:"attempt_id"`
	ScorePct           float64                 `json:"score_pct"`
	PerformanceBadge   string                  `json:"performance_badge"`
	PointsDelta        int64                   `json:"points_delta"`
	TotalCorrect       int                     `json:"total_correct"`
	TotalQuestions     int                     `json:"total_questions"`
	StreakBonusAwarded int64                   `json:"streak_bonus_awarded"`
	BadgesAwarded      []string                `json:"badges_awarded"`
	QuestionBreakdown  []QuestionBreakdownItem `json:"question_breakdown"`
	// IsRepeatAttempt is true when the quiz is knowledge_check and the user
	// has already completed it before. Points are 0 in this case.
	IsRepeatAttempt bool `json:"is_repeat_attempt"`
}

type QuestionBreakdownItem struct {
	Position        int             `json:"position"`
	QuestionSnippet string          `json:"question_snippet"`
	StudentAnswer   json.RawMessage `json:"student_answer"`
	CorrectAnswer   json.RawMessage `json:"correct_answer"`
	IsCorrect       bool            `json:"is_correct"`
	Points          int64           `json:"points"`
}

func (s *Service) Start(ctx context.Context, userID, quizID string) (*StartAttemptResp, error) {
	// Load delivery settings at the trust boundary. Date gates, randomisation
	// and question limits are all server-owned; a caller can never opt out by
	// modifying an app request.
	var qType string
	var questionLimit *int
	var shuffle bool
	var startsAt, endsAt *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT type, question_limit, shuffle_questions, starts_at, ends_at
		 FROM quizzes WHERE id=$1 AND status='published' AND deleted_at IS NULL`, quizID,
	).Scan(&qType, &questionLimit, &shuffle, &startsAt, &endsAt)
	if err != nil || (startsAt != nil && time.Now().Before(*startsAt)) || (endsAt != nil && !time.Now().Before(*endsAt)) {
		return nil, fmt.Errorf("quiz not available")
	}

	// P&W: one attempt only
	if qType == "play_and_win" {
		var existing int
		s.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM quiz_attempts WHERE quiz_id=$1 AND user_id=$2 AND status='completed'`,
			quizID, userID,
		).Scan(&existing)
		if existing > 0 {
			return nil, fmt.Errorf("you have already attempted this quiz")
		}
	}

	// Select the delivered set before creating the attempt, then snapshot it in
	// the same transaction. That set is subsequently enforced for answers and
	// clues, so random selection cannot be bypassed by guessing another ID.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	limit := 0
	if questionLimit != nil {
		limit = *questionLimit
	}
	order := "position"
	// A delivery limit always samples a random subset. The shuffle toggle also
	// randomises the full bank when no limit is set.
	if shuffle || questionLimit != nil {
		order = "random()"
	}
	query := `SELECT id, quiz_id, position, type, prompt, media_url, options, time_limit_seconds,
		        jsonb_array_length(COALESCE(clues, '[]'::jsonb))
		 FROM questions WHERE quiz_id=$1 ORDER BY ` + order
	args := []interface{}{quizID}
	if limit > 0 {
		query += " LIMIT $2"
		args = append(args, limit)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	questions := []quiz.QuestionForStudent{}
	for rows.Next() {
		var question quiz.QuestionForStudent
		if err := rows.Scan(&question.ID, &question.QuizID, &question.Position, &question.Type, &question.Prompt, &question.MediaURL,
			&question.Options, &question.TimeLimitSeconds, &question.ClueCount); err != nil {
			rows.Close()
			return nil, err
		}
		questions = append(questions, question)
	}
	rows.Close()
	if len(questions) == 0 {
		return nil, fmt.Errorf("quiz has no questions")
	}

	// Load and snapshot point economy config
	cfg, err := scoring.LoadConfig(ctx, s.db)
	if err != nil {
		return nil, err
	}
	cfgJSON, _ := cfg.JSON()

	// Create attempt
	var attemptID string
	err = tx.QueryRow(ctx,
		`INSERT INTO quiz_attempts (quiz_id, user_id, status, total_questions, point_config_snapshot, last_answer_at)
		 VALUES ($1,$2,'in_progress',$3,$4, now())
		 RETURNING id`,
		quizID, userID, len(questions), cfgJSON,
	).Scan(&attemptID)
	if err != nil {
		return nil, err
	}

	for i, question := range questions {
		if _, err := tx.Exec(ctx,
			`INSERT INTO quiz_attempt_questions (attempt_id, question_id, position) VALUES ($1,$2,$3)`,
			attemptID, question.ID, i+1); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	// Update last_active_at after the attempt transaction has committed.
	s.db.Exec(ctx, `UPDATE users SET last_active_at=now() WHERE id=$1`, userID)

	return &StartAttemptResp{AttemptID: attemptID, QuizID: quizID, Questions: questions}, nil
}

// SubmitAnswer records an answer during live play. Elapsed time is measured
// from the DB clock, so a client cannot understate how long it took.
func (s *Service) SubmitAnswer(ctx context.Context, userID, attemptID string, req AnswerReq) (*AnswerResp, error) {
	return s.submitAnswer(ctx, userID, attemptID, req, nil)
}

// ReplayAnswer records an answer that was given before the account existed —
// the pre-signup calibration quiz. The elapsed time is client-measured and
// therefore clamped; everything else, correctness included, is graded here.
func (s *Service) ReplayAnswer(ctx context.Context, userID, attemptID string, req AnswerReq, elapsedMs int) (*AnswerResp, error) {
	ms := clampReplayMs(elapsedMs)
	return s.submitAnswer(ctx, userID, attemptID, req, &ms)
}

// replayMsCap is ten minutes: past it the value is not a measurement, and the
// per-question time limit gate in applyServerGates will reject it anyway.
const replayMsCap = 600000

func clampReplayMs(ms int) int {
	if ms < 0 {
		return 0
	}
	if ms > replayMsCap {
		return replayMsCap
	}
	return ms
}

func (s *Service) submitAnswer(ctx context.Context, userID, attemptID string, req AnswerReq, elapsedOverride *int) (*AnswerResp, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Verify attempt belongs to user and is in_progress. FOR UPDATE (not FOR
	// SHARE) because the combo counter and answer clock are written below.
	// timeTakenMs is measured from the previous answer using the DB clock, so a
	// client cannot understate how long it took.
	var quizID string
	var cfgSnapshot json.RawMessage
	var qType string
	var comboLevel, timeTakenMs int
	err = tx.QueryRow(ctx,
		`SELECT quiz_id, point_config_snapshot, combo_level,
		        GREATEST(0, EXTRACT(EPOCH FROM (now() - COALESCE(last_answer_at, started_at))) * 1000)::INT
		 FROM quiz_attempts WHERE id=$1 AND user_id=$2 AND status='in_progress' FOR UPDATE`,
		attemptID, userID,
	).Scan(&quizID, &cfgSnapshot, &comboLevel, &timeTakenMs)
	if err != nil {
		return nil, fmt.Errorf("attempt not found or not in progress")
	}

	// A replayed answer carries the time the client measured before any attempt
	// row existed; there is no DB clock to derive it from.
	if elapsedOverride != nil {
		timeTakenMs = *elapsedOverride
	}

	// Load config from snapshot
	cfg, err := scoring.ConfigFromSnapshot(cfgSnapshot)
	if err != nil {
		cfg, _ = scoring.LoadConfig(ctx, s.db) // fallback to load from db
	}

	// Get question details
	var correctAnswer json.RawMessage
	var timeLimitSeconds int
	err = tx.QueryRow(ctx,
		`SELECT q.type, q.correct_answer, q.time_limit_seconds
		 FROM questions q
		 WHERE q.id=$1 AND q.quiz_id=$2
		   AND (NOT EXISTS (SELECT 1 FROM quiz_attempt_questions WHERE attempt_id=$3)
		        OR EXISTS (SELECT 1 FROM quiz_attempt_questions WHERE attempt_id=$3 AND question_id=q.id))`,
		req.QuestionID, quizID, attemptID,
	).Scan(&qType, &correctAnswer, &timeLimitSeconds)
	if err != nil {
		return nil, fmt.Errorf("question not found")
	}

	// Clues actually handed out by the server, not what the client claims.
	var cluesUsed int
	tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM clue_reveals WHERE attempt_id=$1 AND question_id=$2`,
		attemptID, req.QuestionID,
	).Scan(&cluesUsed)

	resp := scoring.QuestionResponse{
		QuestionID:      req.QuestionID,
		QuestionType:    qType,
		CorrectAnswer:   correctAnswer,
		StudentAnswer:   req.Answer,
		ConfidenceLevel: req.ConfidenceLevel,
		CluesUsed:       cluesUsed,
		ComboLevel:      comboLevel,
	}
	isCorrect, pts := scoring.ScoreQuestion(resp, cfg)

	isCorrect, pts, timedOut, newCombo := applyServerGates(isCorrect, pts, timeTakenMs, timeLimitSeconds, comboLevel)

	// Empty confidence_level must be NULL, not '' — the CHECK constraint only
	// allows NULL or the three enum values.
	var confidence *string
	if req.ConfidenceLevel != "" {
		confidence = &req.ConfidenceLevel
	}

	// Insert, never update. The response below reveals the correct answer, so
	// allowing a second write would let a wrong answer be replaced with the
	// right one for full points.
	ct, err := tx.Exec(ctx,
		`INSERT INTO question_responses (attempt_id, question_id, answer, is_correct, time_taken_ms, clues_used, confidence_level, combo_level, points_earned)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (attempt_id, question_id) DO NOTHING`,
		attemptID, req.QuestionID, req.Answer, isCorrect, timeTakenMs, cluesUsed, confidence, comboLevel, pts)
	if err != nil {
		return nil, fmt.Errorf("failed to save response: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return nil, fmt.Errorf("question already answered")
	}

	if _, err = tx.Exec(ctx,
		`UPDATE quiz_attempts SET combo_level=$1, last_answer_at=now() WHERE id=$2`,
		newCombo, attemptID); err != nil {
		return nil, fmt.Errorf("failed to advance attempt: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit answer: %w", err)
	}

	return &AnswerResp{
		IsCorrect:     isCorrect,
		CorrectAnswer: correctAnswer,
		PointsEarned:  pts,
		ComboLevel:    newCombo,
		TimeTakenMs:   timeTakenMs,
		TimedOut:      timedOut,
	}, nil
}

// RevealClue hands out the next unrevealed clue for a question and records it,
// so the clue_reveal scoring penalty is based on what the server actually gave
// out. Clues are deliberately absent from the question payload sent at Start.
func (s *Service) RevealClue(ctx context.Context, userID, attemptID, questionID string) (*ClueResp, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var quizID string
	err = tx.QueryRow(ctx,
		`SELECT quiz_id FROM quiz_attempts WHERE id=$1 AND user_id=$2 AND status='in_progress' FOR UPDATE`,
		attemptID, userID,
	).Scan(&quizID)
	if err != nil {
		return nil, fmt.Errorf("attempt not found or not in progress")
	}

	var rawClues json.RawMessage
	err = tx.QueryRow(ctx,
		`SELECT q.clues FROM questions q
		 WHERE q.id=$1 AND q.quiz_id=$2
		   AND (NOT EXISTS (SELECT 1 FROM quiz_attempt_questions WHERE attempt_id=$3)
		        OR EXISTS (SELECT 1 FROM quiz_attempt_questions WHERE attempt_id=$3 AND question_id=q.id))`,
		questionID, quizID, attemptID,
	).Scan(&rawClues)
	if err != nil {
		return nil, fmt.Errorf("question not found")
	}

	var clues []json.RawMessage
	if len(rawClues) > 0 {
		json.Unmarshal(rawClues, &clues)
	}
	if len(clues) == 0 {
		return nil, fmt.Errorf("question has no clues")
	}

	// No clues after the answer is in — that would only game the penalty.
	var answered int
	tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM question_responses WHERE attempt_id=$1 AND question_id=$2`,
		attemptID, questionID,
	).Scan(&answered)
	if answered > 0 {
		return nil, fmt.Errorf("question already answered")
	}

	var next int
	tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM clue_reveals WHERE attempt_id=$1 AND question_id=$2`,
		attemptID, questionID,
	).Scan(&next)
	if next >= len(clues) {
		return nil, fmt.Errorf("no more clues")
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO clue_reveals (attempt_id, question_id, clue_index) VALUES ($1,$2,$3)`,
		attemptID, questionID, next); err != nil {
		return nil, fmt.Errorf("failed to record clue: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit clue reveal: %w", err)
	}

	return &ClueResp{
		Clue:      clues[next],
		Index:     next,
		Remaining: len(clues) - next - 1,
		CluesUsed: next + 1,
	}, nil
}

type ClueResp struct {
	Clue      json.RawMessage `json:"clue"`
	Index     int             `json:"index"`
	Remaining int             `json:"remaining"`
	CluesUsed int             `json:"clues_used"`
}

// Add unique constraint needed for ON CONFLICT — handled in migration.
// migrations/002_constraints.sql: ALTER TABLE question_responses ADD UNIQUE (attempt_id, question_id);

func (s *Service) Complete(ctx context.Context, userID, attemptID string) (*CompleteResp, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Load attempt with FOR UPDATE, also fetch the quiz type for repeat-attempt check
	var quizID string
	var cfgSnapshot json.RawMessage
	var totalQuestions int
	var quizType string
	err = tx.QueryRow(ctx,
		`SELECT qa.quiz_id, qa.point_config_snapshot, qa.total_questions, q.type
		 FROM quiz_attempts qa
		 JOIN quizzes q ON q.id = qa.quiz_id
		 WHERE qa.id=$1 AND qa.user_id=$2 AND qa.status='in_progress' FOR UPDATE`,
		attemptID, userID,
	).Scan(&quizID, &cfgSnapshot, &totalQuestions, &quizType)
	if err != nil {
		return nil, fmt.Errorf("attempt not found or already completed")
	}

	cfg, _ := scoring.ConfigFromSnapshot(cfgSnapshot)
	if cfg == nil {
		cfg, _ = scoring.LoadConfig(ctx, s.db)
	}

	// One round trip for four independent scalars that used to cost four:
	// the knowledge_check repeat guard, the current streak, the lifetime
	// completed count, and the institution multiplier. They share no inputs, so
	// a single SELECT of scalar subqueries is exactly equivalent and pays the
	// network latency once instead of four times.
	var isRepeatAttempt bool
	var currentStreak, activityCount int
	var instMultiplier float64 = 1.0
	if err := tx.QueryRow(ctx,
		`SELECT
		   CASE WHEN $4 = 'knowledge_check' THEN EXISTS (
		     SELECT 1 FROM quiz_attempts
		      WHERE quiz_id=$2 AND user_id=$1 AND status='completed' AND id <> $3
		   ) ELSE false END,
		   COALESCE((SELECT current_streak FROM streaks WHERE user_id=$1), 0),
		   (SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed'),
		   COALESCE((SELECT i.point_multiplier FROM users u
		               JOIN institutions i ON i.id = u.institution_id
		              WHERE u.id=$1), 1.0)`,
		userID, quizID, attemptID, quizType,
	).Scan(&isRepeatAttempt, &currentStreak, &activityCount, &instMultiplier); err != nil {
		return nil, err
	}
	activityCount++ // count the attempt being completed right now

	// Load all question responses with time_taken_ms and time_limit_seconds
	rows, err := tx.Query(ctx,
		`SELECT qr.question_id, q.type, q.correct_answer, qr.answer, qr.confidence_level, qr.clues_used, qr.combo_level, qr.points_earned,
		        q.position, q.prompt, qr.is_correct, qr.time_taken_ms, q.time_limit_seconds, q.difficulty
		 FROM question_responses qr
		 JOIN questions q ON q.id = qr.question_id
		 WHERE qr.attempt_id=$1
		 ORDER BY q.position`, attemptID)
	if err != nil {
		return nil, err
	}

	var rawPoints int64
	var totalCorrect int
	var answered int
	var speedSum float64
	var totalDifficulty float64
	var correctDifficulty float64
	var breakdown []QuestionBreakdownItem

	for rows.Next() {
		var qid, qtype, confLevel *string
		var correctAns, studentAns json.RawMessage
		var cluesUsed, comboLevel, position int
		var prompt string
		var isCorrect bool
		var ptsEarned int64
		var timeTakenMs *int
		var timeLimitSeconds int
		var qDifficulty float64

		rows.Scan(&qid, &qtype, &correctAns, &studentAns, &confLevel, &cluesUsed, &comboLevel, &ptsEarned, &position, &prompt, &isCorrect, &timeTakenMs, &timeLimitSeconds, &qDifficulty)

		rawPoints += ptsEarned
		answered++
		if isCorrect {
			totalCorrect++
		}

		// Derived per-question difficulty (refined nightly; read live — an
		// attempt lasts minutes so mid-flight drift is negligible).
		// ponytail: snapshot per-question difficulty only if that drift bites.
		qDiff := qDifficulty
		totalDifficulty += qDiff

		if isCorrect {
			correctDifficulty += qDiff

			// Calculate speed component (within reasonable time, avoiding random fast guessing)
			tTaken := 0
			if timeTakenMs != nil {
				tTaken = *timeTakenMs
			}
			timeLimitMs := float64(timeLimitSeconds * 1000)
			timeTaken := float64(tTaken)
			var qSpeed float64
			if timeLimitSeconds <= 0 {
				// Untimed questions have no meaningful speed target.
				qSpeed = 1.0
			} else if timeTaken < 1000 {
				qSpeed = 0.1 // avoid random fast guessing
			} else if timeTaken <= timeLimitMs/3.0 {
				qSpeed = 1.0 // optimal speed
			} else {
				qSpeed = (timeLimitMs - timeTaken) / (timeLimitMs - (timeLimitMs / 3.0))
				if qSpeed < 0.1 {
					qSpeed = 0.1
				}
			}
			speedSum += qSpeed
		}

		snippet := prompt
		if len(snippet) > 80 {
			snippet = snippet[:80] + "..."
		}
		breakdown = append(breakdown, QuestionBreakdownItem{
			Position:        position,
			QuestionSnippet: snippet,
			StudentAnswer:   studentAns,
			CorrectAnswer:   correctAns,
			IsCorrect:       isCorrect,
			Points:          ptsEarned,
		})
	}
	rows.Close() // Explicit close so tx is free for next statements

	// Score percentage (calculated using the Qwish Score formula)
	scorePct := 0.0
	if totalQuestions > 0 {
		factors := scoring.QwishScoreFactors{
			TotalCorrect:      totalCorrect,
			TotalQuestions:    totalQuestions,
			Streak:            currentStreak,
			ActivityCount:     activityCount,
			SpeedSum:          speedSum,
			TotalDifficulty:   totalDifficulty,
			CorrectDifficulty: correctDifficulty,
		}
		scorePct = scoring.CalculateQwishScore(factors)
	}

	avgDifficulty := 0.0
	if answered > 0 {
		avgDifficulty = totalDifficulty / float64(answered)
	}
	finalPoints := scoring.CalculateFinalScore(totalCorrect, totalQuestions, rawPoints, scorePct, cfg, instMultiplier, avgDifficulty)

	// Repeat knowledge_check attempts earn no points.
	if isRepeatAttempt {
		finalPoints = 0
	}

	// Performance badge
	badge := "needs_work"
	if scorePct >= 75 {
		badge = "excellent"
	} else if scorePct >= 50 {
		badge = "good"
	}

	// Finish the attempt, move the balance, and write the ledger row in one
	// statement. These were three sequential round trips inside the transaction
	// even though each one's input is already known here; chaining them as CTEs
	// makes the whole commit path a single exchange with the database.
	// points_delta is stored as 0 for repeat attempts so history stays accurate.
	expiresAt := time.Now().AddDate(0, int(cfg.PointsExpiryMonths), 0)
	var newBalance int64
	err = tx.QueryRow(ctx,
		`WITH att AS (
		   UPDATE quiz_attempts
		      SET status='completed', score_pct=$1, points_delta=$2,
		          total_correct=$3, total_questions=$4, completed_at=now()
		    WHERE id=$5
		 ), bal AS (
		   UPDATE users
		      SET total_points = GREATEST(0, total_points + $2), updated_at=now()
		    WHERE id=$6 AND NOT $7::bool
		   RETURNING total_points
		 ), led AS (
		   INSERT INTO points_ledger (user_id, amount, reason, reference_id, balance_after, expires_at)
		   SELECT $6, $2, 'quiz_attempt', $5, total_points, $8 FROM bal
		 )
		 SELECT COALESCE(
		   (SELECT total_points FROM bal),
		   (SELECT total_points FROM users WHERE id=$6)
		 )`,
		scorePct, finalPoints, totalCorrect, totalQuestions, attemptID,
		userID, isRepeatAttempt, expiresAt,
	).Scan(&newBalance)
	if err != nil {
		return nil, err
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}

	// Update spaced-repetition mastery and reward the recommendation arm. Both
	// are best-effort learning signals and never invalidate a completed attempt.
	s.recordAdaptiveLearning(ctx, userID, quizID, scorePct)

	// Update streak and get bonus
	streakBonus, _ := s.streakSvc.RecordCompletion(ctx, userID, cfg)
	if streakBonus > 0 {
		// Credit and ledger in one statement rather than two round trips.
		s.db.Exec(ctx,
			`WITH bal AS (
			   UPDATE users SET total_points = total_points + $1, updated_at=now()
			    WHERE id=$2 RETURNING total_points
			 )
			 INSERT INTO points_ledger (user_id, amount, reason, balance_after, expires_at)
			 SELECT $2, $1, 'streak_bonus', total_points, $3 FROM bal`,
			streakBonus, userID, time.Now().AddDate(0, int(cfg.PointsExpiryMonths), 0))
	}

	// Check and award badges
	awarded := s.checkBadges(ctx, userID, quizID, scorePct, totalCorrect, totalQuestions, attemptID)

	// ── Emit in-app notifications (best-effort) ─────────────────────────────
	if s.notifSvc != nil {
		// Badge unlocks
		for _, bt := range awarded {
			label, body := badgeCopy(bt)
			s.notifSvc.Emit(ctx, userID, "badge", label, body,
				notification.WithIcon("emoji_events"),
				notification.WithColor("warning"),
				notification.WithReference("badge:"+bt))
		}
		// Streak milestone bonus
		if streakBonus > 0 {
			s.notifSvc.Emit(ctx, userID, "streak", "Streak milestone reached!",
				fmt.Sprintf("You earned +%d bonus points for keeping your streak alive.", streakBonus),
				notification.WithIcon("local_fire_department"),
				notification.WithColor("warning"),
				notification.WithReference("streak_bonus"))
		}
		// Perfect score
		if scorePct >= 100 {
			s.notifSvc.Emit(ctx, userID, "points", "Perfect score!",
				"You aced every question on that quiz.",
				notification.WithIcon("star"),
				notification.WithColor("success"),
				notification.WithReference("attempt:"+attemptID))
		}
	}

	if breakdown == nil {
		breakdown = []QuestionBreakdownItem{}
	}
	if awarded == nil {
		awarded = []string{}
	}

	return &CompleteResp{
		AttemptID:          attemptID,
		ScorePct:           scorePct,
		PerformanceBadge:   badge,
		PointsDelta:        finalPoints,
		TotalCorrect:       totalCorrect,
		TotalQuestions:     totalQuestions,
		StreakBonusAwarded: streakBonus,
		BadgesAwarded:      awarded,
		QuestionBreakdown:  breakdown,
		IsRepeatAttempt:    isRepeatAttempt,
	}, nil
}

func (s *Service) recordAdaptiveLearning(ctx context.Context, userID, quizID string, scorePct float64) {
	_, err := s.db.Exec(ctx, `
		WITH topic AS (
		  SELECT COALESCE(NULLIF(subdomain,''), NULLIF(domain,'')) AS name
		    FROM quizzes WHERE id=$2
		), upsert_mastery AS (
		  INSERT INTO learner_topic_mastery
		    (user_id, topic, mastery, ease_factor, interval_days, review_count, next_review_at)
		  SELECT $1, name, $3::float8/100,
		         GREATEST(1.3, LEAST(3.0, 2.5 + ($3::float8-70)/100)),
		         CASE WHEN $3 < 50 THEN 1 WHEN $3 < 75 THEN 3 ELSE 7 END,
		         1,
		         now() + make_interval(days => CASE WHEN $3 < 50 THEN 1 WHEN $3 < 75 THEN 3 ELSE 7 END)
		    FROM topic WHERE name IS NOT NULL
		  ON CONFLICT (user_id, topic) DO UPDATE SET
		    mastery = 0.7*learner_topic_mastery.mastery + 0.3*EXCLUDED.mastery,
		    ease_factor = GREATEST(1.3, LEAST(3.0,
		      learner_topic_mastery.ease_factor + ($3::float8-70)/200)),
		    interval_days = CASE WHEN $3 < 50 THEN 1 ELSE GREATEST(1,
		      ROUND(learner_topic_mastery.interval_days * learner_topic_mastery.ease_factor)::int) END,
		    review_count = learner_topic_mastery.review_count + 1,
		    next_review_at = now() + make_interval(days => CASE WHEN $3 < 50 THEN 1 ELSE GREATEST(1,
		      ROUND(learner_topic_mastery.interval_days * learner_topic_mastery.ease_factor)::int) END),
		    updated_at = now()
		)
		INSERT INTO recommendation_bandit_stats (user_id, quiz_id, rewards, updated_at)
		VALUES ($1, $2, $3::float8/100, now())
		ON CONFLICT (user_id, quiz_id) DO UPDATE SET
		  rewards = recommendation_bandit_stats.rewards + EXCLUDED.rewards,
		  updated_at = now()`, userID, quizID, scorePct)
	if err != nil {
		log.Printf("adaptive learning update for attempt on quiz %s: %v", quizID, err)
	}
}

func (s *Service) GetResult(ctx context.Context, userID, attemptID string) (map[string]interface{}, error) {
	var result map[string]interface{}
	var scorePct float64
	var pointsDelta int64
	var totalCorrect, totalQuestions int
	var status string
	var completedAt *time.Time
	var quizID string

	err := s.db.QueryRow(ctx,
		`SELECT quiz_id, status, COALESCE(score_pct,0), COALESCE(points_delta,0), COALESCE(total_correct,0), COALESCE(total_questions,0), completed_at
		 FROM quiz_attempts WHERE id=$1 AND user_id=$2`,
		attemptID, userID,
	).Scan(&quizID, &status, &scorePct, &pointsDelta, &totalCorrect, &totalQuestions, &completedAt)
	if err != nil {
		return nil, err
	}

	result = map[string]interface{}{
		"attempt_id":      attemptID,
		"quiz_id":         quizID,
		"status":          status,
		"score_pct":       scorePct,
		"points_delta":    pointsDelta,
		"total_correct":   totalCorrect,
		"total_questions": totalQuestions,
		"completed_at":    completedAt,
	}
	return result, nil
}

// badgeCopy maps badge_type to a user-friendly (title, body) pair.
func badgeCopy(bt string) (string, string) {
	switch bt {
	case "first_quiz":
		return "First Sprint unlocked", "You completed your very first quiz — welcome aboard!"
	case "on_a_roll":
		return "On a Roll!", "You've maintained a 7-day streak. Keep the momentum going."
	case "unstoppable":
		return "Unstoppable", "A 30-day streak — that's championship territory."
	case "top_10":
		return "Top 10 in your institution", "You broke into the top 10 — share the win."
	case "perfect_score":
		return "Perfect Score badge", "100% on a quiz. Flawless execution."
	case "speed_demon":
		return "Speed Demon", "Lightning combo on a speed_chain question."
	case "sharp_mind":
		return "Sharp Mind", "You were both confident and right — every answer."
	case "explorer":
		return "Explorer", "You've now answered every question type on the platform."
	}
	return "New badge unlocked!", "Check your profile to see your latest achievement."
}

// checkBadges awards applicable badges after a quiz completion.
func (s *Service) checkBadges(ctx context.Context, userID, quizID string, scorePct float64, correct, total int, attemptID string) []string {
	// The four badge predicates are independent aggregates, so they collapse
	// into one SELECT instead of four sequential round trips on the completion
	// path. NULL-safe COALESCE on the SUMs because SUM over zero rows is NULL.
	var quizCount, typeCount, maxCombo int
	var confTotal, confCorrect, veryConfCorrect int
	err := s.db.QueryRow(ctx,
		`SELECT
		   (SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed'),
		   (SELECT COUNT(DISTINCT q.type) FROM question_responses qr
		      JOIN questions q ON q.id = qr.question_id
		      JOIN quiz_attempts qa ON qa.id = qr.attempt_id
		     WHERE qa.user_id=$1 AND qa.status='completed'),
		   (SELECT COALESCE(MAX(qr.combo_level), 0) FROM question_responses qr
		      JOIN questions q ON q.id = qr.question_id
		     WHERE qr.attempt_id=$2 AND q.type='speed_chain'),
		   (SELECT COUNT(*) FROM question_responses qr
		      JOIN questions q ON q.id = qr.question_id
		     WHERE qr.attempt_id=$2 AND q.type='confidence_based'),
		   (SELECT COALESCE(SUM(CASE WHEN qr.is_correct THEN 1 ELSE 0 END), 0)
		      FROM question_responses qr JOIN questions q ON q.id = qr.question_id
		     WHERE qr.attempt_id=$2 AND q.type='confidence_based'),
		   (SELECT COALESCE(SUM(CASE WHEN qr.confidence_level='very_confident' AND qr.is_correct THEN 1 ELSE 0 END), 0)
		      FROM question_responses qr JOIN questions q ON q.id = qr.question_id
		     WHERE qr.attempt_id=$2 AND q.type='confidence_based')`,
		userID, attemptID,
	).Scan(&quizCount, &typeCount, &maxCombo, &confTotal, &confCorrect, &veryConfCorrect)
	if err != nil {
		return nil
	}

	var earned []string
	if quizCount == 1 {
		earned = append(earned, "first_quiz")
	}
	if scorePct == 100 {
		earned = append(earned, "perfect_score")
	}
	if typeCount >= 7 {
		earned = append(earned, "explorer")
	}
	if maxCombo >= 3 {
		earned = append(earned, "speed_demon")
	}
	if confTotal > 0 && confCorrect == confTotal && veryConfCorrect == confTotal {
		earned = append(earned, "sharp_mind")
	}
	if len(earned) == 0 {
		return nil
	}

	// One multi-row insert instead of one per badge. RETURNING reports only the
	// rows that were actually inserted, so a badge the user already holds no
	// longer shows up as newly awarded — the old code appended on any Exec that
	// did not error, which meant ON CONFLICT DO NOTHING still counted as a win
	// and re-announced the same badge after every quiz.
	rows, err := s.db.Query(ctx,
		`INSERT INTO badges (user_id, badge_type)
		 SELECT $1, bt FROM unnest($2::text[]) AS bt
		 ON CONFLICT DO NOTHING
		 RETURNING badge_type`,
		userID, earned)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var awarded []string
	for rows.Next() {
		var bt string
		if err := rows.Scan(&bt); err != nil {
			return awarded
		}
		awarded = append(awarded, bt)
	}
	return awarded
}
