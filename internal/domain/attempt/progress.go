package attempt

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

// ProgressAttempt is one earlier completed attempt in the same domain.
type ProgressAttempt struct {
	AttemptID      string    `json:"attempt_id"`
	QuizTitle      string    `json:"quiz_title"`
	ScorePct       float64   `json:"score_pct"`
	TotalCorrect   int       `json:"total_correct"`
	TotalQuestions int       `json:"total_questions"`
	CompletedAt    time.Time `json:"completed_at"`
}

// Progress places one attempt among the learner's earlier work in the same
// domain, so a past result can say what improved. Answers are never included.
type Progress struct {
	Domain      *string `json:"domain"`
	DomainLabel *string `json:"domain_label"`
	// Earlier attempts in the domain, newest first, capped at progressLimit.
	Previous []ProgressAttempt `json:"previous"`
	// All earlier completed attempts in the domain, uncapped.
	PreviousTotal int `json:"previous_total"`
}

const progressLimit = 12

// GetProgress returns the attempt's domain and the learner's earlier completed
// attempts in it. Only the attempt's owner can read it.
func (s *Service) GetProgress(ctx context.Context, userID, attemptID string) (*Progress, error) {
	p := &Progress{Previous: []ProgressAttempt{}}
	var completedAt *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT q.domain, d.label, qa.completed_at
		 FROM quiz_attempts qa
		 JOIN quizzes q ON q.id = qa.quiz_id
		 LEFT JOIN domains d ON d.slug = q.domain
		 WHERE qa.id = $1 AND qa.user_id = $2 AND qa.status = 'completed'`,
		attemptID, userID,
	).Scan(&p.Domain, &p.DomainLabel, &completedAt)
	if err != nil {
		return nil, err
	}
	if p.Domain == nil || completedAt == nil {
		return p, nil
	}

	// Ordered by (completed_at, id) so attempts finishing in the same instant
	// still have a stable "before".
	const earlier = `FROM quiz_attempts qa
		 JOIN quizzes q ON q.id = qa.quiz_id
		 WHERE qa.user_id = $1 AND qa.status = 'completed' AND q.domain = $2
		   AND (qa.completed_at, qa.id) < ($3, $4::uuid)`
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) `+earlier,
		userID, *p.Domain, *completedAt, attemptID,
	).Scan(&p.PreviousTotal); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT qa.id, q.title, COALESCE(qa.score_pct,0), COALESCE(qa.total_correct,0),
		        COALESCE(qa.total_questions,0), qa.completed_at `+earlier+`
		 ORDER BY qa.completed_at DESC, qa.id DESC LIMIT $5`,
		userID, *p.Domain, *completedAt, attemptID, progressLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a ProgressAttempt
		if err := rows.Scan(&a.AttemptID, &a.QuizTitle, &a.ScorePct, &a.TotalCorrect, &a.TotalQuestions, &a.CompletedAt); err != nil {
			return nil, err
		}
		p.Previous = append(p.Previous, a)
	}
	return p, rows.Err()
}

// GET /api/v1/attempts/:attemptId/progress
func (h *Handler) GetProgress(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.GetProgress(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "attemptId"))
	if err != nil {
		middleware.NotFound(w, "attempt")
		return
	}
	middleware.JSON(w, http.StatusOK, p)
}
