package demo

import (
	"context"
	"encoding/json"
	"time"
)

// SystemUserID owns all admin-authored demo quizzes (seeded in migration 023).
const SystemUserID = "00000000-0000-0000-0000-000000000001"

// ── Event capture ────────────────────────────────────────────────────────────
// Demo plays are pre-login and stateless, so we log lightweight events here to
// power the super-admin dashboard. Best-effort: never block or fail the play.

func (s *Service) logStart(quizID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.db.Exec(ctx, `INSERT INTO demo_events (quiz_id, event_type) VALUES ($1, 'start')`, quizID)
	}()
}

func (s *Service) logComplete(quizID string, r *ScoreResult) {
	perQ, _ := json.Marshal(r.PerQuestion)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.db.Exec(ctx,
			`INSERT INTO demo_events (quiz_id, event_type, score_pct, total_correct, total_questions, per_question)
			 VALUES ($1, 'complete', $2, $3, $4, $5)`,
			quizID, r.ScorePct, r.TotalCorrect, r.TotalQuestions, perQ)
	}()
}

// ── Admin authoring ──────────────────────────────────────────────────────────

// CreateQuestionReq is one question in a demo-quiz create request.
type CreateQuestionReq struct {
	Type             string          `json:"type"`
	Prompt           string          `json:"prompt"`
	MediaURL         *string         `json:"media_url"`
	Options          json.RawMessage `json:"options"`
	CorrectAnswer    json.RawMessage `json:"correct_answer"`
	TimeLimitSeconds int             `json:"time_limit_seconds"`
}

// CreateReq is the payload for authoring a demo quiz.
type CreateReq struct {
	Title       string              `json:"title"`
	Description *string             `json:"description"`
	Domain      *string             `json:"domain"`
	Subdomain   *string             `json:"subdomain"`
	Questions   []CreateQuestionReq `json:"questions"`
}

// Create authors a new demo quiz (is_demo=true, published) owned by the system
// user, with all its questions, in one transaction.
func (s *Service) Create(ctx context.Context, req CreateReq) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var quizID string
	err = tx.QueryRow(ctx,
		`INSERT INTO quizzes (created_by, title, description, type, visibility, status, question_count, is_demo, published_at, domain, subdomain)
		 VALUES ($1, $2, $3, 'knowledge_check', 'public', 'published', $4, true, now(), $5, $6)
		 RETURNING id`,
		SystemUserID, req.Title, req.Description, len(req.Questions), req.Domain, req.Subdomain).Scan(&quizID)
	if err != nil {
		return "", err
	}

	for i, q := range req.Questions {
		opts := q.Options
		if len(opts) == 0 {
			opts = json.RawMessage("[]")
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO questions (quiz_id, position, type, prompt, media_url, options, correct_answer, time_limit_seconds)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			quizID, i+1, q.Type, q.Prompt, q.MediaURL, opts, q.CorrectAnswer, q.TimeLimitSeconds); err != nil {
			return "", err
		}
	}

	return quizID, tx.Commit(ctx)
}

// Delete soft-deletes a demo quiz. Only affects demo quizzes.
func (s *Service) Delete(ctx context.Context, quizID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE quizzes SET deleted_at = now(), updated_at = now() WHERE id = $1 AND is_demo = true`, quizID)
	return err
}

// ── Admin analytics ──────────────────────────────────────────────────────────

// AdminDemoQuiz is a demo quiz row with headline play stats for the list view.
type AdminDemoQuiz struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Description    *string   `json:"description,omitempty"`
	Domain         *string   `json:"domain,omitempty"`
	Subdomain      *string   `json:"subdomain,omitempty"`
	QuestionCount  int       `json:"question_count"`
	Starts         int       `json:"starts"`
	Completions    int       `json:"completions"`
	CompletionRate float64   `json:"completion_rate"`
	AvgScorePct    float64   `json:"avg_score_pct"`
	CreatedAt      time.Time `json:"created_at"`
}

// ListAdmin returns every demo quiz with aggregate play stats.
func (s *Service) ListAdmin(ctx context.Context) ([]AdminDemoQuiz, error) {
	rows, err := s.db.Query(ctx,
		`SELECT q.id, q.title, q.description, q.domain, q.subdomain, q.question_count, q.created_at,
		        COUNT(*) FILTER (WHERE e.event_type = 'start')    AS starts,
		        COUNT(*) FILTER (WHERE e.event_type = 'complete') AS completions,
		        COALESCE(AVG(e.score_pct) FILTER (WHERE e.event_type = 'complete'), 0) AS avg_score
		 FROM quizzes q
		 LEFT JOIN demo_events e ON e.quiz_id = q.id
		 WHERE q.is_demo = true AND q.deleted_at IS NULL
		 GROUP BY q.id
		 ORDER BY q.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AdminDemoQuiz{}
	for rows.Next() {
		var q AdminDemoQuiz
		if err := rows.Scan(&q.ID, &q.Title, &q.Description, &q.Domain, &q.Subdomain,
			&q.QuestionCount, &q.CreatedAt, &q.Starts, &q.Completions, &q.AvgScorePct); err != nil {
			return nil, err
		}
		if q.Starts > 0 {
			q.CompletionRate = float64(q.Completions) / float64(q.Starts) * 100
		}
		out = append(out, q)
	}
	return out, nil
}

// QuestionStat is per-question difficulty for the analytics detail view.
type QuestionStat struct {
	QuestionID  string  `json:"question_id"`
	Position    int     `json:"position"`
	Prompt      string  `json:"prompt"`
	Answered    int     `json:"answered"`
	Correct     int     `json:"correct"`
	CorrectRate float64 `json:"correct_rate"`
}

// Analytics is the detailed per-quiz breakdown for improving a demo quiz.
type Analytics struct {
	Starts      int            `json:"starts"`
	Completions int            `json:"completions"`
	AvgScorePct float64        `json:"avg_score_pct"`
	Questions   []QuestionStat `json:"questions"`
}

// Analytics returns per-question correctness for a demo quiz, so admins can see
// which questions are too hard/broken and improve them.
func (s *Service) Analytics(ctx context.Context, quizID string) (*Analytics, error) {
	a := &Analytics{Questions: []QuestionStat{}}

	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FILTER (WHERE event_type = 'start'),
		        COUNT(*) FILTER (WHERE event_type = 'complete'),
		        COALESCE(AVG(score_pct) FILTER (WHERE event_type = 'complete'), 0)
		 FROM demo_events WHERE quiz_id = $1`, quizID).Scan(&a.Starts, &a.Completions, &a.AvgScorePct)
	if err != nil {
		return nil, err
	}

	// Per-question correctness: unnest each completion's per_question array and
	// join to the question rows so unplayed questions still show (0 answered).
	rows, err := s.db.Query(ctx,
		`SELECT qn.id, qn.position, qn.prompt,
		        COUNT(pq.correct) AS answered,
		        COUNT(*) FILTER (WHERE (pq.correct)::boolean) AS correct
		 FROM questions qn
		 LEFT JOIN demo_events e ON e.quiz_id = qn.quiz_id AND e.event_type = 'complete'
		 LEFT JOIN LATERAL jsonb_to_recordset(e.per_question) AS pq(question_id text, correct boolean)
		        ON pq.question_id = qn.id::text
		 WHERE qn.quiz_id = $1
		 GROUP BY qn.id, qn.position, qn.prompt
		 ORDER BY qn.position`, quizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var q QuestionStat
		if err := rows.Scan(&q.QuestionID, &q.Position, &q.Prompt, &q.Answered, &q.Correct); err != nil {
			return nil, err
		}
		if q.Answered > 0 {
			q.CorrectRate = float64(q.Correct) / float64(q.Answered) * 100
		}
		a.Questions = append(a.Questions, q)
	}
	return a, rows.Err()
}
