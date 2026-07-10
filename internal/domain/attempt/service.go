package attempt

import (
	"context"
	"encoding/json"
	"fmt"
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
	AttemptID string                      `json:"attempt_id"`
	QuizID    string                      `json:"quiz_id"`
	Questions []quiz.QuestionForStudent   `json:"questions"`
}

type AnswerReq struct {
	QuestionID      string          `json:"question_id"`
	Answer          json.RawMessage `json:"answer"`
	TimeTakenMs     int             `json:"time_taken_ms"`
	ConfidenceLevel string          `json:"confidence_level"`
	CluesUsed       int             `json:"clues_used"`
	ComboLevel      int             `json:"combo_level"`
}

type AnswerResp struct {
	IsCorrect     bool            `json:"is_correct"`
	CorrectAnswer json.RawMessage `json:"correct_answer"`
	PointsEarned  int64           `json:"points_earned"`
	ComboLevel    int             `json:"combo_level"`
}

type CompleteResp struct {
	AttemptID          string                   `json:"attempt_id"`
	ScorePct           float64                  `json:"score_pct"`
	PerformanceBadge   string                   `json:"performance_badge"`
	PointsDelta        int64                    `json:"points_delta"`
	TotalCorrect       int                      `json:"total_correct"`
	TotalQuestions     int                      `json:"total_questions"`
	StreakBonusAwarded int64                    `json:"streak_bonus_awarded"`
	BadgesAwarded      []string                 `json:"badges_awarded"`
	QuestionBreakdown  []QuestionBreakdownItem  `json:"question_breakdown"`
	// IsRepeatAttempt is true when the quiz is knowledge_check and the user
	// has already completed it before. Points are 0 in this case.
	IsRepeatAttempt    bool                     `json:"is_repeat_attempt"`
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
	// Validate quiz exists and is published
	q, err := s.quizSvc.GetByID(ctx, quizID)
	if err != nil || q.Status != "published" {
		return nil, fmt.Errorf("quiz not available")
	}

	// P&W: one attempt only
	if q.Type == "play_and_win" {
		var existing int
		s.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM quiz_attempts WHERE quiz_id=$1 AND user_id=$2 AND status='completed'`,
			quizID, userID,
		).Scan(&existing)
		if existing > 0 {
			return nil, fmt.Errorf("you have already attempted this quiz")
		}
	}

	// Load and snapshot point economy config
	cfg, err := scoring.LoadConfig(ctx, s.db)
	if err != nil {
		return nil, err
	}
	cfgJSON, _ := cfg.JSON()

	// Create attempt
	var attemptID string
	err = s.db.QueryRow(ctx,
		`INSERT INTO quiz_attempts (quiz_id, user_id, status, total_questions, point_config_snapshot)
		 VALUES ($1,$2,'in_progress',$3,$4)
		 RETURNING id`,
		quizID, userID, q.QuestionCount, cfgJSON,
	).Scan(&attemptID)
	if err != nil {
		return nil, err
	}

	// Update last_active_at
	s.db.Exec(ctx, `UPDATE users SET last_active_at=now() WHERE id=$1`, userID)

	questions, err := s.quizSvc.GetQuestionsForStudent(ctx, quizID)
	if err != nil {
		return nil, err
	}

	return &StartAttemptResp{AttemptID: attemptID, QuizID: quizID, Questions: questions}, nil
}

func (s *Service) SubmitAnswer(ctx context.Context, userID, attemptID string, req AnswerReq) (*AnswerResp, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Verify attempt belongs to user and is in_progress, lock FOR SHARE to prevent concurrent completion
	var quizID string
	var cfgSnapshot json.RawMessage
	var qType string
	err = tx.QueryRow(ctx,
		`SELECT quiz_id, point_config_snapshot FROM quiz_attempts WHERE id=$1 AND user_id=$2 AND status='in_progress' FOR SHARE`,
		attemptID, userID,
	).Scan(&quizID, &cfgSnapshot)
	if err != nil {
		return nil, fmt.Errorf("attempt not found or not in progress")
	}

	// Load config from snapshot
	cfg, err := scoring.ConfigFromSnapshot(cfgSnapshot)
	if err != nil {
		cfg, _ = scoring.LoadConfig(ctx, s.db) // fallback to load from db
	}

	// Get question details
	var correctAnswer json.RawMessage
	err = tx.QueryRow(ctx,
		`SELECT type, correct_answer FROM questions WHERE id=$1 AND quiz_id=$2`,
		req.QuestionID, quizID,
	).Scan(&qType, &correctAnswer)
	if err != nil {
		return nil, fmt.Errorf("question not found")
	}

	resp := scoring.QuestionResponse{
		QuestionID:      req.QuestionID,
		QuestionType:    qType,
		CorrectAnswer:   correctAnswer,
		StudentAnswer:   req.Answer,
		ConfidenceLevel: req.ConfidenceLevel,
		CluesUsed:       req.CluesUsed,
		ComboLevel:      req.ComboLevel,
	}
	isCorrect, pts := scoring.ScoreQuestion(resp, cfg)

	// Empty confidence_level must be NULL, not '' — the CHECK constraint only
	// allows NULL or the three enum values.
	var confidence *string
	if req.ConfidenceLevel != "" {
		confidence = &req.ConfidenceLevel
	}

	// Upsert response
	_, err = tx.Exec(ctx,
		`INSERT INTO question_responses (attempt_id, question_id, answer, is_correct, time_taken_ms, clues_used, confidence_level, combo_level, points_earned)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 ON CONFLICT (attempt_id, question_id) DO UPDATE
		 SET answer=$3, is_correct=$4, time_taken_ms=$5, clues_used=$6, confidence_level=$7, combo_level=$8, points_earned=$9`,
		attemptID, req.QuestionID, req.Answer, isCorrect, req.TimeTakenMs, req.CluesUsed, confidence, req.ComboLevel, pts)
	if err != nil {
		return nil, fmt.Errorf("failed to save response: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit answer: %w", err)
	}

	return &AnswerResp{
		IsCorrect:     isCorrect,
		CorrectAnswer: correctAnswer,
		PointsEarned:  pts,
		ComboLevel:    req.ComboLevel + 1,
	}, nil
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

	// For knowledge_check quizzes, check whether the user has a prior completed attempt.
	// If so this is a repeat attempt and points will be zeroed out.
	var isRepeatAttempt bool
	if quizType == "knowledge_check" {
		var priorCount int
		s.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM quiz_attempts
			 WHERE quiz_id=$1 AND user_id=$2 AND status='completed' AND id != $3`,
			quizID, userID, attemptID,
		).Scan(&priorCount)
		isRepeatAttempt = priorCount > 0
	}

	cfg, _ := scoring.ConfigFromSnapshot(cfgSnapshot)
	if cfg == nil {
		cfg, _ = scoring.LoadConfig(ctx, s.db)
	}

	// Load user's current streak
	var currentStreak int
	tx.QueryRow(ctx, `SELECT current_streak FROM streaks WHERE user_id=$1`, userID).Scan(&currentStreak)

	// Load user's activity count (completed quizzes count, adding 1 for the current one)
	var activityCount int
	tx.QueryRow(ctx, `SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed'`, userID).Scan(&activityCount)
	activityCount++

	// Load institution multiplier
	var instMultiplier float64 = 1.0
	tx.QueryRow(ctx,
		`SELECT i.point_multiplier FROM users u JOIN institutions i ON i.id=u.institution_id WHERE u.id=$1`, userID,
	).Scan(&instMultiplier)

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
			if timeTaken < 1000 {
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

	// Update attempt — store points_delta=0 for repeat attempts so history is accurate
	tx.Exec(ctx,
		`UPDATE quiz_attempts SET status='completed', score_pct=$1, points_delta=$2, total_correct=$3, total_questions=$4, completed_at=now()
		 WHERE id=$5`,
		scorePct, finalPoints, totalCorrect, totalQuestions, attemptID)

	var newBalance int64
	if !isRepeatAttempt {
		// Update user points and write ledger only for first attempt
		err = tx.QueryRow(ctx,
			`UPDATE users SET total_points = GREATEST(0, total_points + $1), updated_at=now() WHERE id=$2 RETURNING total_points`,
			finalPoints, userID,
		).Scan(&newBalance)
		if err != nil {
			return nil, err
		}

		expiresAt := time.Now().AddDate(0, int(cfg.PointsExpiryMonths), 0)
		tx.Exec(ctx,
			`INSERT INTO points_ledger (user_id, amount, reason, reference_id, balance_after, expires_at)
			 VALUES ($1,$2,'quiz_attempt',$3,$4,$5)`,
			userID, finalPoints, attemptID, newBalance, expiresAt)
	} else {
		// For repeat attempts, just read the current balance for consistency
		s.db.QueryRow(ctx, `SELECT total_points FROM users WHERE id=$1`, userID).Scan(&newBalance)
	}

	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}

	// Update streak and get bonus
	streakBonus, _ := s.streakSvc.RecordCompletion(ctx, userID, cfg)
	if streakBonus > 0 {
		var postStreakBalance int64
		err = s.db.QueryRow(ctx, `UPDATE users SET total_points = total_points + $1, updated_at=now() WHERE id=$2 RETURNING total_points`, streakBonus, userID).Scan(&postStreakBalance)
		if err == nil {
			streakExpiry := time.Now().AddDate(0, int(cfg.PointsExpiryMonths), 0)
			s.db.Exec(ctx,
				`INSERT INTO points_ledger (user_id, amount, reason, balance_after, expires_at)
				 VALUES ($1,$2,'streak_bonus',$3,$4)`,
				userID, streakBonus, postStreakBalance, streakExpiry)
		}
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
		"attempt_id":       attemptID,
		"quiz_id":          quizID,
		"status":           status,
		"score_pct":        scorePct,
		"points_delta":     pointsDelta,
		"total_correct":    totalCorrect,
		"total_questions":  totalQuestions,
		"completed_at":     completedAt,
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
	var awarded []string
	awardBadge := func(bt string) {
		_, err := s.db.Exec(ctx,
			`INSERT INTO badges (user_id, badge_type) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			userID, bt)
		if err == nil {
			awarded = append(awarded, bt)
		}
	}

	// first_quiz
	var quizCount int
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed'`, userID).Scan(&quizCount)
	if quizCount == 1 {
		awardBadge("first_quiz")
	}

	// perfect_score
	if scorePct == 100 {
		awardBadge("perfect_score")
	}

	// explorer: one of each of the 7 question types
	var typeCount int
	s.db.QueryRow(ctx,
		`SELECT COUNT(DISTINCT q.type) FROM question_responses qr
		 JOIN questions q ON q.id=qr.question_id
		 JOIN quiz_attempts qa ON qa.id=qr.attempt_id
		 WHERE qa.user_id=$1 AND qa.status='completed'`, userID,
	).Scan(&typeCount)
	if typeCount >= 7 {
		awardBadge("explorer")
	}

	// speed_demon: speed_chain with combo >=3
	var maxCombo int
	s.db.QueryRow(ctx,
		`SELECT COALESCE(MAX(combo_level),0) FROM question_responses qr
		 JOIN questions q ON q.id=qr.question_id
		 WHERE qr.attempt_id=$1 AND q.type='speed_chain'`, attemptID,
	).Scan(&maxCombo)
	if maxCombo >= 3 {
		awardBadge("speed_demon")
	}

	// sharp_mind: 100% on confidence_based, all very_confident correct
	var confTotal, confCorrect, veryConfCorrect int
	s.db.QueryRow(ctx,
		`SELECT COUNT(*), SUM(CASE WHEN qr.is_correct THEN 1 ELSE 0 END),
		        SUM(CASE WHEN qr.confidence_level='very_confident' AND qr.is_correct THEN 1 ELSE 0 END)
		 FROM question_responses qr
		 JOIN questions q ON q.id=qr.question_id
		 WHERE qr.attempt_id=$1 AND q.type='confidence_based'`, attemptID,
	).Scan(&confTotal, &confCorrect, &veryConfCorrect)
	if confTotal > 0 && confCorrect == confTotal && veryConfCorrect == confTotal {
		awardBadge("sharp_mind")
	}

	return awarded
}
