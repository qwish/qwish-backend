package attempt

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

const maxBehaviorEventsPerBatch = 50

var allowedBehaviorEvents = map[string]bool{
	"question_viewed":      true,
	"answer_changed":       true,
	"timer_expired":        true,
	"question_advanced":    true,
	"focus_lost":           true,
	"focus_gained":         true,
	"exit_clicked":         true,
	"completion_requested": true,
}

type BehaviorEvent struct {
	ClientEventID   string  `json:"client_event_id"`
	EventType       string  `json:"event_type"`
	QuestionID      *string `json:"question_id"`
	ClientElapsedMs int     `json:"client_elapsed_ms"`
	ChangeCount     *int    `json:"change_count"`
	HiddenMs        *int    `json:"hidden_ms"`
}

type BehaviorBatch struct {
	Events []BehaviorEvent `json:"events"`
}

type QuestionBehavior struct {
	QuestionID    string  `json:"question_id"`
	Position      int     `json:"position"`
	Views         int64   `json:"views"`
	AnswerChanges int64   `json:"answer_changes"`
	TimerExpiries int64   `json:"timer_expiries"`
	FocusLosses   int64   `json:"focus_losses"`
	AvgResponseMs float64 `json:"avg_response_ms"`
}

type BehaviorSummary struct {
	QuizID            string             `json:"quiz_id"`
	Attempts          int64              `json:"attempts"`
	CompletedAttempts int64              `json:"completed_attempts"`
	TrackedAttempts   int64              `json:"tracked_attempts"`
	ExitClicks        int64              `json:"exit_clicks"`
	FocusLosses       int64              `json:"focus_losses"`
	TimerExpiries     int64              `json:"timer_expiries"`
	AnswerChanges     int64              `json:"answer_changes"`
	AverageHiddenMs   float64            `json:"average_hidden_ms"`
	Questions         []QuestionBehavior `json:"questions"`
}

func validateBehaviorEvent(event BehaviorEvent) error {
	if _, err := uuid.Parse(event.ClientEventID); err != nil {
		return fmt.Errorf("invalid client_event_id")
	}
	if !allowedBehaviorEvents[event.EventType] {
		return fmt.Errorf("invalid event_type")
	}
	if event.ClientElapsedMs < 0 || event.ClientElapsedMs > 86400000 {
		return fmt.Errorf("client_elapsed_ms must be between 0 and 86400000")
	}
	if event.QuestionID != nil {
		if _, err := uuid.Parse(*event.QuestionID); err != nil {
			return fmt.Errorf("invalid question_id")
		}
	}
	if event.ChangeCount != nil && (*event.ChangeCount < 1 || *event.ChangeCount > 1000) {
		return fmt.Errorf("change_count must be between 1 and 1000")
	}
	if event.HiddenMs != nil && (*event.HiddenMs < 0 || *event.HiddenMs > 86400000) {
		return fmt.Errorf("hidden_ms must be between 0 and 86400000")
	}
	if (event.EventType == "question_viewed" || event.EventType == "answer_changed" ||
		event.EventType == "timer_expired" || event.EventType == "question_advanced") && event.QuestionID == nil {
		return fmt.Errorf("question_id is required for question events")
	}
	if event.EventType == "answer_changed" && event.ChangeCount == nil {
		return fmt.Errorf("change_count is required for answer_changed")
	}
	if event.EventType != "answer_changed" && event.ChangeCount != nil {
		return fmt.Errorf("change_count is only valid for answer_changed")
	}
	if event.EventType != "focus_gained" && event.HiddenMs != nil {
		return fmt.Errorf("hidden_ms is only valid for focus_gained")
	}
	return nil
}

// RecordBehavior stores interaction telemetry for an attempt owned by userID.
// The question membership check prevents clients from attaching arbitrary
// question IDs, and the unique client ID makes batch retries idempotent.
func (s *Service) RecordBehavior(ctx context.Context, userID, attemptID string, batch BehaviorBatch) (int, error) {
	if len(batch.Events) == 0 || len(batch.Events) > maxBehaviorEventsPerBatch {
		return 0, fmt.Errorf("events must contain between 1 and %d items", maxBehaviorEventsPerBatch)
	}
	if _, err := uuid.Parse(attemptID); err != nil {
		return 0, fmt.Errorf("invalid attempt id")
	}
	for _, event := range batch.Events {
		if err := validateBehaviorEvent(event); err != nil {
			return 0, err
		}
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var owned bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM quiz_attempts
		  WHERE id=$1 AND user_id=$2
		    AND (status='in_progress' OR completed_at > now() - interval '10 minutes')
		)`, attemptID, userID).Scan(&owned); err != nil || !owned {
		return 0, fmt.Errorf("attempt not found")
	}

	inserted := 0
	for _, event := range batch.Events {
		if event.QuestionID != nil {
			var delivered bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(
				SELECT 1 FROM quiz_attempt_questions
				WHERE attempt_id=$1 AND question_id=$2
			)`, attemptID, *event.QuestionID).Scan(&delivered); err != nil || !delivered {
				return 0, fmt.Errorf("question was not delivered in this attempt")
			}
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO attempt_behavior_events
			  (client_event_id, attempt_id, user_id, question_id, event_type,
			   client_elapsed_ms, change_count, hidden_ms)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (attempt_id, client_event_id) DO NOTHING`,
			event.ClientEventID, attemptID, userID, event.QuestionID, event.EventType,
			event.ClientElapsedMs, event.ChangeCount, event.HiddenMs)
		if err != nil {
			return 0, err
		}
		inserted += int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return inserted, nil
}

func (s *Service) PurgeExpiredBehavior(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM attempt_behavior_events WHERE expires_at <= now()`)
	return tag.RowsAffected(), err
}

// BehaviorSummary combines the interaction stream with canonical attempt and
// response data. Client events describe patterns; server response times remain
// the source of truth for timing.
func (s *Service) BehaviorSummary(ctx context.Context, quizID string) (*BehaviorSummary, error) {
	if _, err := uuid.Parse(quizID); err != nil {
		return nil, fmt.Errorf("invalid quiz id")
	}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM quizzes WHERE id=$1 AND deleted_at IS NULL)`, quizID).Scan(&exists); err != nil || !exists {
		return nil, fmt.Errorf("quiz not found")
	}
	summary := &BehaviorSummary{QuizID: quizID, Questions: []QuestionBehavior{}}
	if err := s.db.QueryRow(ctx, `
		WITH a AS (
		  SELECT COUNT(*) AS attempts, COUNT(*) FILTER (WHERE status='completed') AS completed
		  FROM quiz_attempts WHERE quiz_id=$1
		), e AS (
		  SELECT COUNT(DISTINCT e.attempt_id) AS tracked,
		         COUNT(*) FILTER (WHERE e.event_type='exit_clicked') AS exits,
		         COUNT(*) FILTER (WHERE e.event_type='focus_lost') AS focus_losses,
		         COUNT(*) FILTER (WHERE e.event_type='timer_expired') AS expiries,
		         COALESCE(SUM(e.change_count) FILTER (WHERE e.event_type='answer_changed'), 0) AS changes,
		         COALESCE(AVG(e.hidden_ms) FILTER (WHERE e.event_type='focus_gained'), 0) AS hidden
		  FROM attempt_behavior_events e
		  JOIN quiz_attempts qa ON qa.id=e.attempt_id
		  WHERE qa.quiz_id=$1
		)
		SELECT a.attempts, a.completed, e.tracked, e.exits, e.focus_losses,
		       e.expiries, e.changes, e.hidden FROM a CROSS JOIN e`, quizID).Scan(
		&summary.Attempts, &summary.CompletedAttempts, &summary.TrackedAttempts,
		&summary.ExitClicks, &summary.FocusLosses, &summary.TimerExpiries,
		&summary.AnswerChanges, &summary.AverageHiddenMs); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx, `
		WITH event_totals AS (
		  SELECT question_id,
		         COUNT(*) FILTER (WHERE event_type='question_viewed') AS views,
		         COALESCE(SUM(change_count) FILTER (WHERE event_type='answer_changed'), 0) AS changes,
		         COUNT(*) FILTER (WHERE event_type='timer_expired') AS expiries,
		         COUNT(*) FILTER (WHERE event_type='focus_lost') AS focus_losses
		  FROM attempt_behavior_events
		  WHERE question_id IS NOT NULL
		  GROUP BY question_id
		), response_totals AS (
		  SELECT question_id, COALESCE(AVG(time_taken_ms), 0) AS avg_response_ms
		  FROM question_responses GROUP BY question_id
		)
		SELECT q.id, q.position,
		       COALESCE(e.views, 0), COALESCE(e.changes, 0),
		       COALESCE(e.expiries, 0), COALESCE(e.focus_losses, 0),
		       COALESCE(r.avg_response_ms, 0)
		FROM questions q
		LEFT JOIN event_totals e ON e.question_id=q.id
		LEFT JOIN response_totals r ON r.question_id=q.id
		WHERE q.quiz_id=$1
		ORDER BY q.position`, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var question QuestionBehavior
		if err := rows.Scan(&question.QuestionID, &question.Position, &question.Views,
			&question.AnswerChanges, &question.TimerExpiries, &question.FocusLosses,
			&question.AvgResponseMs); err != nil {
			return nil, err
		}
		summary.Questions = append(summary.Questions, question)
	}
	return summary, rows.Err()
}
