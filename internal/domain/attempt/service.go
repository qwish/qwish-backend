package attempt

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
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
	AttemptID        string                       `json:"attempt_id"`
	QuizID           string                       `json:"quiz_id"`
	Questions        []quiz.QuestionForStudent    `json:"questions"`
	Answers          []AttemptAnswerSnapshot      `json:"answers,omitempty"`
	CurrentIndex     int                          `json:"current_index,omitempty"`
	RemainingSeconds *int                         `json:"remaining_seconds,omitempty"`
	RevealedClues    map[string][]json.RawMessage `json:"revealed_clues,omitempty"`
	Resumed          bool                         `json:"resumed,omitempty"`
}

type AttemptAnswerSnapshot struct {
	QuestionID      string          `json:"question_id"`
	Answer          json.RawMessage `json:"answer"`
	ConfidenceLevel string          `json:"confidence_level,omitempty"`
}

type AnswerReq struct {
	QuestionID      string          `json:"question_id"`
	Answer          json.RawMessage `json:"answer"`
	ConfidenceLevel string          `json:"confidence_level"`
	OptionID        *string         `json:"option_id"`

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
	// QwishScore is the skill rating after this attempt (100–900); the delta
	// is 0 when every question had been seen before.
	QwishScore      float64 `json:"qwish_score"`
	QwishScoreDelta float64 `json:"qwish_score_delta"`
}

type QuestionBreakdownItem struct {
	Position        int             `json:"position"`
	QuestionSnippet string          `json:"question_snippet"`
	StudentAnswer   json.RawMessage `json:"student_answer"`
	CorrectAnswer   json.RawMessage `json:"correct_answer"`
	IsCorrect       bool            `json:"is_correct"`
	Points          int64           `json:"points"`
}

func (s *Service) Start(ctx context.Context, userID, quizID, assignmentID string) (*StartAttemptResp, error) {
	// Assignment identity is selected by the client but authorized here. This
	// prevents one attempt from being attached to every assignment that happens
	// to reuse the same quiz.
	if assignmentID != "" {
		var status string
		var existingAttempt *string
		var attemptsStarted, attemptLimit int
		err := s.db.QueryRow(ctx, `SELECT ar.status,ar.attempt_id
			,ar.attempts_started,a.attempt_limit
			FROM learning_assignment_recipients ar
			JOIN learning_assignments a ON a.id=ar.assignment_id
			WHERE ar.assignment_id=$1 AND ar.student_id=$2 AND a.quiz_id=$3
			  AND a.status='published' AND (a.available_at IS NULL OR a.available_at<=now())
			  AND ar.status IN ('assigned','started','overdue')`,
			assignmentID, userID, quizID).Scan(&status, &existingAttempt, &attemptsStarted, &attemptLimit)
		if err != nil {
			return nil, fmt.Errorf("assignment not available")
		}
		if existingAttempt != nil && *existingAttempt != "" {
			resumed, resumeErr := s.resume(ctx, userID, quizID, *existingAttempt)
			if resumeErr == nil {
				return resumed, nil
			}
			// A stale-attempt sweep may have abandoned the attempt since the
			// assignment list was loaded. Abandoned technical sessions do not
			// consume an allowed attempt: detach the dead session atomically and
			// let the learner start again.
			var abandoned bool
			_ = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM quiz_attempts
				WHERE id=$1 AND user_id=$2 AND quiz_id=$3 AND status='abandoned')`,
				*existingAttempt, userID, quizID).Scan(&abandoned)
			if !abandoned {
				return nil, resumeErr
			}
			result, clearErr := s.db.Exec(ctx, `UPDATE learning_assignment_recipients ar
				SET attempt_id=NULL,status=CASE WHEN a.due_at IS NOT NULL AND a.due_at<=now() THEN 'overdue' ELSE 'assigned' END,
				    attempts_started=GREATEST(0,ar.attempts_started-1)
				FROM learning_assignments a
				WHERE ar.assignment_id=a.id AND ar.assignment_id=$1 AND ar.student_id=$2 AND ar.attempt_id=$3`,
				assignmentID, userID, *existingAttempt)
			if clearErr != nil || result.RowsAffected() != 1 {
				return nil, fmt.Errorf("assignment changed; refresh and try again")
			}
			attemptsStarted--
		}
		if attemptsStarted >= attemptLimit {
			return nil, fmt.Errorf("attempt limit reached")
		}
	}
	// Load delivery settings at the trust boundary. Date gates, randomisation
	// and question limits are all server-owned; a caller can never opt out by
	// modifying an app request.
	var qType string
	var questionLimit *int
	var shuffle bool
	var startsAt, endsAt *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT type, question_limit, shuffle_questions, starts_at, ends_at
		 FROM quizzes q WHERE q.id=$1 AND q.status='published' AND q.deleted_at IS NULL
		 AND ($3::bool OR q.visibility='public' OR (
		   q.institution_id=(SELECT institution_id FROM users WHERE id=$2)
		   AND (q.group_id IS NULL OR EXISTS(SELECT 1 FROM group_students gs WHERE gs.group_id=q.group_id AND gs.user_id=$2))
		 ))`, quizID, userID, assignmentID != "",
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
	query := `SELECT q.id, q.quiz_id, q.position, q.type, q.prompt, q.media_url, q.options,
		        COALESCE((SELECT jsonb_agg(jsonb_build_object('id',qo.id,'label',qo.label) ORDER BY qo.position)
		                  FROM question_options qo WHERE qo.question_id=q.id AND qo.active), '[]'::jsonb),
		        EXISTS (SELECT 1 FROM question_concepts qc WHERE qc.question_id=q.id), q.revision,
		        (SELECT qv.id FROM question_versions qv WHERE qv.question_id=q.id AND qv.revision=q.revision),
		        q.time_limit_seconds, jsonb_array_length(COALESCE(q.clues, '[]'::jsonb))
		 FROM questions q WHERE q.quiz_id=$1 ORDER BY ` + order
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
			&question.Options, &question.OptionChoices, &question.CollectConfidence, &question.Revision, &question.VersionID, &question.TimeLimitSeconds, &question.ClueCount); err != nil {
			rows.Close()
			return nil, err
		}
		options, choices, order, shuffleErr := shuffleQuestionOptions(question.Options, question.OptionChoices)
		if shuffleErr != nil {
			rows.Close()
			return nil, shuffleErr
		}
		question.Options, question.OptionChoices, question.OptionOrder = options, choices, order
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
			`INSERT INTO quiz_attempt_questions (attempt_id, question_id, position, question_revision, question_version_id, option_order) VALUES ($1,$2,$3,$4,$5,$6)`,
			attemptID, question.ID, i+1, question.Revision, question.VersionID, question.OptionOrder); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if assignmentID != "" {
		result, linkErr := s.db.Exec(ctx, `UPDATE learning_assignment_recipients
			SET status='started',attempt_id=$1,attempts_started=attempts_started+1
			WHERE assignment_id=$2 AND student_id=$3
			  AND status IN ('assigned','overdue') AND attempt_id IS NULL
			  AND attempts_started < (SELECT attempt_limit FROM learning_assignments WHERE id=$2)`,
			attemptID, assignmentID, userID)
		if linkErr != nil || result.RowsAffected() != 1 {
			// The attempt is unusable without the requested assignment link. Mark it
			// abandoned rather than letting it become an unattributed live attempt.
			s.db.Exec(ctx, `UPDATE quiz_attempts SET status='abandoned' WHERE id=$1 AND user_id=$2`, attemptID, userID)
			return nil, fmt.Errorf("assignment changed; refresh and try again")
		}
	}

	// Update last_active_at after the attempt transaction has committed.
	s.db.Exec(ctx, `UPDATE users SET last_active_at=now() WHERE id=$1`, userID)

	return &StartAttemptResp{AttemptID: attemptID, QuizID: quizID, Questions: questions}, nil
}

func (s *Service) resume(ctx context.Context, userID, quizID, attemptID string) (*StartAttemptResp, error) {
	var status string
	var lastAnswerAt time.Time
	if err := s.db.QueryRow(ctx, `SELECT status FROM quiz_attempts
		WHERE id=$1 AND user_id=$2 AND quiz_id=$3`, attemptID, userID, quizID).Scan(&status); err != nil || status != "in_progress" {
		return nil, fmt.Errorf("attempt is not available to resume")
	}
	if err := s.db.QueryRow(ctx, `SELECT COALESCE(last_answer_at,started_at) FROM quiz_attempts WHERE id=$1`, attemptID).Scan(&lastAnswerAt); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `SELECT q.id,q.quiz_id,aq.position,qv.type,qv.prompt,qv.media_url,qv.options,
		COALESCE((SELECT jsonb_agg(jsonb_build_object('id',qo.id,'label',qo.label) ORDER BY qo.position)
		 FROM question_options qo WHERE qo.question_id=q.id AND qo.active),'[]'::jsonb),
		EXISTS (SELECT 1 FROM question_concepts qc WHERE qc.question_id=q.id),aq.question_revision,
		aq.question_version_id,qv.time_limit_seconds,jsonb_array_length(COALESCE(qv.clues,'[]'::jsonb)),aq.option_order
		FROM quiz_attempt_questions aq
		JOIN questions q ON q.id=aq.question_id
		JOIN question_versions qv ON qv.id=aq.question_version_id
		WHERE aq.attempt_id=$1 ORDER BY aq.position`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	questions := []quiz.QuestionForStudent{}
	for rows.Next() {
		var question quiz.QuestionForStudent
		if err := rows.Scan(&question.ID, &question.QuizID, &question.Position, &question.Type, &question.Prompt, &question.MediaURL,
			&question.Options, &question.OptionChoices, &question.CollectConfidence, &question.Revision, &question.VersionID, &question.TimeLimitSeconds, &question.ClueCount, &question.OptionOrder); err != nil {
			return nil, err
		}
		var order []int
		if len(question.OptionOrder) > 0 && json.Unmarshal(question.OptionOrder, &order) == nil {
			options, choices, _, reorderErr := applyOptionOrder(question.Options, question.OptionChoices, order)
			if reorderErr != nil {
				return nil, reorderErr
			}
			question.Options, question.OptionChoices = options, choices
		}
		questions = append(questions, question)
	}
	answerRows, err := s.db.Query(ctx, `SELECT question_id,answer,COALESCE(confidence_level,'')
		FROM question_responses WHERE attempt_id=$1 ORDER BY submitted_at`, attemptID)
	if err != nil {
		return nil, err
	}
	defer answerRows.Close()
	answers := []AttemptAnswerSnapshot{}
	answered := map[string]bool{}
	for answerRows.Next() {
		var answer AttemptAnswerSnapshot
		if err := answerRows.Scan(&answer.QuestionID, &answer.Answer, &answer.ConfidenceLevel); err != nil {
			return nil, err
		}
		answers = append(answers, answer)
		answered[answer.QuestionID] = true
	}
	current := 0
	for current < len(questions) && answered[questions[current].ID] {
		current++
	}
	if current >= len(questions) {
		current = len(questions) - 1
	}
	if current < 0 {
		return nil, fmt.Errorf("attempt has no questions")
	}
	remaining := questions[current].TimeLimitSeconds
	if remaining > 0 {
		remaining -= int(time.Since(lastAnswerAt).Seconds())
		if remaining < 0 {
			remaining = 0
		}
	}
	revealed := map[string][]json.RawMessage{}
	clueRows, err := s.db.Query(ctx, `SELECT cr.question_id,qv.clues->cr.clue_index
		FROM clue_reveals cr
		JOIN quiz_attempt_questions aq ON aq.attempt_id=cr.attempt_id AND aq.question_id=cr.question_id
		JOIN question_versions qv ON qv.id=aq.question_version_id
		WHERE cr.attempt_id=$1 ORDER BY cr.question_id,cr.clue_index`, attemptID)
	if err != nil {
		return nil, err
	}
	defer clueRows.Close()
	for clueRows.Next() {
		var questionID string
		var clue json.RawMessage
		if err := clueRows.Scan(&questionID, &clue); err != nil {
			return nil, err
		}
		revealed[questionID] = append(revealed[questionID], clue)
	}
	return &StartAttemptResp{AttemptID: attemptID, QuizID: quizID, Questions: questions,
		Answers: answers, CurrentIndex: current, RemainingSeconds: &remaining, RevealedClues: revealed, Resumed: true}, nil
}

func (s *Service) ResumeAttempt(ctx context.Context, userID, attemptID string) (*StartAttemptResp, error) {
	var quizID string
	if err := s.db.QueryRow(ctx, `SELECT quiz_id FROM quiz_attempts WHERE id=$1 AND user_id=$2`, attemptID, userID).Scan(&quizID); err != nil {
		return nil, fmt.Errorf("attempt is not available to resume")
	}
	return s.resume(ctx, userID, quizID, attemptID)
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
	if req.ConfidenceLevel != "" && req.ConfidenceLevel != "not_sure" && req.ConfidenceLevel != "pretty_sure" && req.ConfidenceLevel != "very_confident" {
		return nil, fmt.Errorf("invalid confidence level")
	}
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
	if req.OptionID != nil {
		var label string
		if err := tx.QueryRow(ctx,
			`SELECT label FROM question_options WHERE id=$1 AND question_id=$2 AND active`,
			*req.OptionID, req.QuestionID).Scan(&label); err != nil {
			return nil, fmt.Errorf("option not found")
		}
		encoded, _ := json.Marshal(label)
		req.Answer = encoded
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
	var responseID string
	err = tx.QueryRow(ctx,
		`INSERT INTO question_responses (attempt_id, question_id, answer, is_correct, time_taken_ms, clues_used, confidence_level, combo_level, points_earned, option_id)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 ON CONFLICT (attempt_id, question_id) DO NOTHING RETURNING id`,
		attemptID, req.QuestionID, req.Answer, isCorrect, timeTakenMs, cluesUsed, confidence, comboLevel, pts, req.OptionID).Scan(&responseID)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("question already answered")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to save response: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO learning_evidence
		  (response_id,institution_id,user_id,attempt_id,question_id,question_revision,question_version_id,concept_id,option_id,misconception_id,is_correct,confidence_level,clues_used,occurred_at)
		SELECT $1,e.institution_id,$2,$3,$4,aq.question_revision,aq.question_version_id,qc.concept_id,$5,qmo.misconception_id,$6,$7,$8,now()
		FROM enrollments e
		JOIN question_concepts qc ON qc.question_id=$4
		JOIN quiz_attempt_questions aq ON aq.attempt_id=$3 AND aq.question_id=$4
		LEFT JOIN question_misconception_options qmo ON qmo.question_id=$4 AND qmo.option_id=$5
		WHERE e.user_id=$2 AND e.status IN ('active','suspended')
		ON CONFLICT (response_id,concept_id) DO NOTHING`,
		responseID, userID, attemptID, req.QuestionID, req.OptionID, isCorrect, confidence, cluesUsed)
	if err != nil {
		return nil, fmt.Errorf("failed to record learning evidence: %w", err)
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

	// One round trip for two independent scalars: the knowledge_check repeat
	// guard and the institution multiplier.
	var isRepeatAttempt bool
	var instMultiplier float64 = 1.0
	if err := tx.QueryRow(ctx,
		`SELECT
		   CASE WHEN $4 = 'knowledge_check' THEN EXISTS (
		     SELECT 1 FROM quiz_attempts
		      WHERE quiz_id=$2 AND user_id=$1 AND status='completed' AND id <> $3
		   ) ELSE false END,
		   COALESCE((SELECT i.point_multiplier FROM users u
		               JOIN institutions i ON i.id = u.institution_id
		              WHERE u.id=$1), 1.0)`,
		userID, quizID, attemptID, quizType,
	).Scan(&isRepeatAttempt, &instMultiplier); err != nil {
		return nil, err
	}

	// Load all question responses with time_taken_ms and time_limit_seconds
	rows, err := tx.Query(ctx,
		`SELECT qr.question_id, qv.type, qv.correct_answer, qr.answer, qr.confidence_level, qr.clues_used, qr.combo_level, qr.points_earned,
		        q.position, qv.prompt, qr.is_correct, q.difficulty, q.rating_b, q.rating_n,
		        `+scoring.GuessFloorSQL+`,
		        EXISTS (SELECT 1 FROM question_responses p JOIN quiz_attempts pa ON pa.id=p.attempt_id
		                 WHERE pa.user_id=$2 AND pa.status='completed' AND p.question_id=qr.question_id)
		 FROM question_responses qr
		 JOIN questions q ON q.id = qr.question_id
		 JOIN quiz_attempt_questions aq ON aq.attempt_id=qr.attempt_id AND aq.question_id=qr.question_id
		 JOIN question_versions qv ON qv.id=aq.question_version_id
		 WHERE qr.attempt_id=$1
		 ORDER BY q.position`, attemptID, userID)
	if err != nil {
		return nil, err
	}

	var rawPoints int64
	var totalCorrect int
	var answered int
	var totalDifficulty float64
	var breakdown []QuestionBreakdownItem
	var ratingObs []scoring.RatingObs

	for rows.Next() {
		var qid, qtype, confLevel *string
		var correctAns, studentAns json.RawMessage
		var cluesUsed, comboLevel, position int
		var prompt string
		var isCorrect bool
		var ptsEarned int64
		var qDifficulty, guess float64
		var ratingB *float64
		var ratingN int
		var seenBefore bool

		rows.Scan(&qid, &qtype, &correctAns, &studentAns, &confLevel, &cluesUsed, &comboLevel, &ptsEarned, &position, &prompt, &isCorrect, &qDifficulty, &ratingB, &ratingN, &guess, &seenBefore)

		rawPoints += ptsEarned
		answered++
		if isCorrect {
			totalCorrect++
		}

		// Derived per-question difficulty (refined nightly; read live — an
		// attempt lasts minutes so mid-flight drift is negligible).
		// ponytail: snapshot per-question difficulty only if that drift bites.
		totalDifficulty += qDifficulty

		// Only a first sight of a question measures ability; repeats measure
		// memory of it and would let practice on one quiz inflate the rating.
		if !seenBefore && qid != nil {
			b := scoring.SeedDifficulty(qDifficulty)
			if ratingB != nil {
				b = *ratingB
			}
			ratingObs = append(ratingObs, scoring.RatingObs{
				QuestionID: *qid, Correct: isCorrect, Guess: guess, B: b, QN: ratingN,
			})
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

	// score_pct is plain accuracy; ability lives in the rating below.
	scorePct := 0.0
	if totalQuestions > 0 {
		scorePct = float64(totalCorrect) / float64(totalQuestions) * 100
	}

	// Before the attempt UPDATE: its leaderboard trigger reads learner_ratings.
	scoreBefore, scoreAfter, err := scoring.ApplyRatings(ctx, tx, userID, ratingObs)
	if err != nil {
		return nil, err
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
		          total_correct=$3, total_questions=$4, completed_at=now(), qwish_score_after=$9
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
		userID, isRepeatAttempt, expiresAt, scoreAfter,
	).Scan(&newBalance)
	if err != nil {
		return nil, err
	}

	// An assignment is submitted only when its attempt completes. Updating this
	// after each answer made partially answered work disappear from the active
	// list and falsely recorded a submission timestamp.
	if _, err = tx.Exec(ctx,
		`UPDATE learning_assignment_recipients
		 SET status='submitted', submitted_at=now()
		 WHERE attempt_id=$1 AND student_id=$2 AND status IN ('assigned','started','overdue')`,
		attemptID, userID,
	); err != nil {
		return nil, fmt.Errorf("failed to submit assignment: %w", err)
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
		QwishScore:         scoreAfter,
		QwishScoreDelta:    scoreAfter - scoreBefore,
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
	var quizTitle string

	err := s.db.QueryRow(ctx,
		`SELECT qa.quiz_id,q.title,qa.status,COALESCE(qa.score_pct,0),COALESCE(qa.points_delta,0),COALESCE(qa.total_correct,0),COALESCE(qa.total_questions,0),qa.completed_at
		 FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id WHERE qa.id=$1 AND qa.user_id=$2`,
		attemptID, userID,
	).Scan(&quizID, &quizTitle, &status, &scorePct, &pointsDelta, &totalCorrect, &totalQuestions, &completedAt)
	if err != nil {
		return nil, err
	}
	if status != "completed" {
		return nil, fmt.Errorf("attempt is not completed")
	}
	breakdown := []QuestionBreakdownItem{}
	rows, err := s.db.Query(ctx, `SELECT q.position,qv.prompt,qr.answer,qv.correct_answer,qr.is_correct,qr.points_earned
		FROM question_responses qr
		JOIN quiz_attempt_questions aq ON aq.attempt_id=qr.attempt_id AND aq.question_id=qr.question_id
		JOIN questions q ON q.id=qr.question_id
		JOIN question_versions qv ON qv.id=aq.question_version_id
		WHERE qr.attempt_id=$1 ORDER BY q.position`, attemptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item QuestionBreakdownItem
		if err := rows.Scan(&item.Position, &item.QuestionSnippet, &item.StudentAnswer, &item.CorrectAnswer, &item.IsCorrect, &item.Points); err != nil {
			return nil, err
		}
		breakdown = append(breakdown, item)
	}
	badge := "needs_work"
	if scorePct >= 75 {
		badge = "excellent"
	} else if scorePct >= 50 {
		badge = "good"
	}

	result = map[string]interface{}{
		"attempt_id":         attemptID,
		"quiz_id":            quizID,
		"quiz_title":         quizTitle,
		"status":             status,
		"score_pct":          scorePct,
		"performance_badge":  badge,
		"points_delta":       pointsDelta,
		"total_correct":      totalCorrect,
		"total_questions":    totalQuestions,
		"completed_at":       completedAt,
		"question_breakdown": breakdown,
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
