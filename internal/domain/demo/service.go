package demo

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/scoring"
)

// ErrNotDemo is returned when a quiz exists but is not flagged as a demo quiz,
// so non-demo content never leaks through the public demo endpoints.
var ErrNotDemo = errors.New("not a demo quiz")

type Service struct {
	db      *pgxpool.Pool
	quizSvc *quiz.Service
}

func NewService(db *pgxpool.Pool, quizSvc *quiz.Service) *Service {
	return &Service{db: db, quizSvc: quizSvc}
}

// DemoQuiz is the lightweight shape for the public demo list.
type DemoQuiz struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Description   *string `json:"description,omitempty"`
	Domain        *string `json:"domain,omitempty"`
	Subdomain     *string `json:"subdomain,omitempty"`
	QuestionCount int     `json:"question_count"`
}

// Answer is one submitted answer in a demo score request.
type Answer struct {
	QuestionID string          `json:"question_id"`
	Answer     json.RawMessage `json:"answer"`
}

// ScoreResult is the stateless grade returned for a demo submission.
type ScoreResult struct {
	ScorePct       float64   `json:"score_pct"`
	TotalCorrect   int       `json:"total_correct"`
	TotalQuestions int       `json:"total_questions"`
	PerQuestion    []QResult `json:"per_question,omitempty"`
}

// QResult is per-question correctness, used for demo analytics.
type QResult struct {
	QuestionID string `json:"question_id"`
	Correct    bool   `json:"correct"`
}

// List returns the curated demo quizzes.
func (s *Service) List(ctx context.Context) ([]DemoQuiz, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, title, description, domain, subdomain, question_count
		 FROM quizzes
		 WHERE is_demo = true AND deleted_at IS NULL
		 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	quizzes := []DemoQuiz{}
	for rows.Next() {
		var q DemoQuiz
		if err := rows.Scan(&q.ID, &q.Title, &q.Description, &q.Domain, &q.Subdomain, &q.QuestionCount); err != nil {
			return nil, err
		}
		quizzes = append(quizzes, q)
	}
	return quizzes, nil
}

// Questions returns a demo quiz's questions WITHOUT correct answers.
// Returns ErrNotDemo if the quiz is missing or not flagged as demo.
func (s *Service) Questions(ctx context.Context, quizID string) ([]quiz.QuestionForStudent, error) {
	if err := s.assertDemo(ctx, quizID); err != nil {
		return nil, err
	}
	s.logStart(quizID)
	return s.quizSvc.GetQuestionsForStudent(ctx, quizID)
}

// Score grades a demo submission statelessly (no user, no persistence).
// Skipped/missing answers count as wrong: total is the quiz's full question set.
func (s *Service) Score(ctx context.Context, quizID string, answers []Answer) (*ScoreResult, error) {
	if err := s.assertDemo(ctx, quizID); err != nil {
		return nil, err
	}
	questions, err := s.quizSvc.GetQuestions(ctx, quizID) // includes correct_answer
	if err != nil {
		return nil, err
	}
	cfg, err := scoring.LoadConfig(ctx, s.db)
	if err != nil {
		return nil, err
	}

	result := grade(questions, answers, cfg)
	s.logComplete(quizID, result)
	return result, nil
}

// grade is the pure scoring core: correct/total accuracy, skipped answers
// count as wrong. Kept DB-free so it can be tested in isolation.
func grade(questions []quiz.Question, answers []Answer, cfg *scoring.Config) *ScoreResult {
	given := make(map[string]json.RawMessage, len(answers))
	for _, a := range answers {
		given[a.QuestionID] = a.Answer
	}

	correct := 0
	perQ := make([]QResult, 0, len(questions))
	for _, q := range questions {
		ok, _ := scoring.ScoreQuestion(scoring.QuestionResponse{
			QuestionType:  q.Type,
			CorrectAnswer: q.CorrectAnswer,
			StudentAnswer: given[q.ID],
		}, cfg)
		if ok {
			correct++
		}
		perQ = append(perQ, QResult{QuestionID: q.ID, Correct: ok})
	}

	total := len(questions)
	pct := 0.0
	if total > 0 {
		pct = float64(correct) / float64(total) * 100
	}
	return &ScoreResult{ScorePct: pct, TotalCorrect: correct, TotalQuestions: total, PerQuestion: perQ}
}

func (s *Service) assertDemo(ctx context.Context, quizID string) error {
	var isDemo bool
	err := s.db.QueryRow(ctx,
		`SELECT is_demo FROM quizzes WHERE id = $1 AND deleted_at IS NULL`, quizID).Scan(&isDemo)
	if err != nil {
		return ErrNotDemo // not found or bad id → treat as not-a-demo
	}
	if !isDemo {
		return ErrNotDemo
	}
	return nil
}
