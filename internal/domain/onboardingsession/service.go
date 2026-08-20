// Package onboardingsession holds the work a first-run user does before an
// account exists: their language and topic picks, and the calibration quiz
// they played. A session is claimed once, at signup, and then it is inert.
//
// The institution-registration handler in internal/domain/onboarding is a
// different thing entirely and is untouched by this package.
package onboardingsession

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/scoring"
)

var (
	// ErrSession covers missing, expired, and already-claimed sessions alike.
	// The caller is anonymous, so distinguishing them only helps an attacker
	// enumerate ids.
	ErrSession     = errors.New("session not found or expired")
	ErrBadLanguage = errors.New("unsupported language")
	ErrBadTopic    = errors.New("unknown topic")
)

// supportedLanguages are the codes the API accepts. Only "en" has an ARB file
// today; the other two are stored preferences that take effect when theirs land.
var supportedLanguages = map[string]bool{"en": true, "hi": true, "mr": true}

// knownTopics mirrors the domains table seeded in migration 020. Kept in code
// rather than queried per request: the taxonomy is a fixed six rows that only
// a migration changes.
var knownTopics = map[string]bool{
	"aptitude": true, "quantitative": true, "logical": true,
	"verbal": true, "computer_science": true, "general": true,
}

type Service struct {
	db      *pgxpool.Pool
	quizSvc *quiz.Service
}

func NewService(db *pgxpool.Pool, quizSvc *quiz.Service) *Service {
	return &Service{db: db, quizSvc: quizSvc}
}

func normalizeLanguage(code string) (string, error) {
	if code == "" {
		return "en", nil
	}
	if !supportedLanguages[code] {
		return "", ErrBadLanguage
	}
	return code, nil
}

// normalizeTopics deduplicates and sorts, so an empty pick and a full pick are
// both representable and two identical picks store identically.
func normalizeTopics(topics []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(topics))
	for _, t := range topics {
		if !knownTopics[t] {
			return nil, ErrBadTopic
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// Create opens a session. The returned id is the only credential the client
// holds for it.
func (s *Service) Create(ctx context.Context, language string, topics []string) (string, error) {
	lang, err := normalizeLanguage(language)
	if err != nil {
		return "", err
	}
	tops, err := normalizeTopics(topics)
	if err != nil {
		return "", err
	}
	var id string
	err = s.db.QueryRow(ctx,
		`INSERT INTO onboarding_sessions (language, topics) VALUES ($1,$2) RETURNING id`,
		lang, tops,
	).Scan(&id)
	return id, err
}

// UpdatePrefs rewrites the picks — the user going back a screen.
func (s *Service) UpdatePrefs(ctx context.Context, sessionID, language string, topics []string) error {
	lang, err := normalizeLanguage(language)
	if err != nil {
		return err
	}
	tops, err := normalizeTopics(topics)
	if err != nil {
		return err
	}
	ct, err := s.db.Exec(ctx,
		`UPDATE onboarding_sessions SET language=$2, topics=$3
		 WHERE id=$1 AND claimed_by IS NULL AND expires_at > now()`,
		sessionID, lang, tops)
	if err != nil {
		return ErrSession // bad uuid text lands here too
	}
	if ct.RowsAffected() == 0 {
		return ErrSession
	}
	return nil
}

// Prefs reads back a live session's picks.
func (s *Service) Prefs(ctx context.Context, sessionID string) (string, []string, error) {
	var lang string
	var topics []string
	err := s.db.QueryRow(ctx,
		`SELECT language, topics FROM onboarding_sessions
		 WHERE id=$1 AND claimed_by IS NULL AND expires_at > now()`,
		sessionID,
	).Scan(&lang, &topics)
	if err != nil {
		return "", nil, ErrSession
	}
	return lang, topics, nil
}

// QuizSummary is the card shape for the pre-signup recommendation list.
type QuizSummary struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Description   *string `json:"description,omitempty"`
	Domain        *string `json:"domain,omitempty"`
	QuestionCount int     `json:"question_count"`
}

// recommendLimit caps the list. A first-run user choosing from more than this
// is being asked to browse, not to start.
const recommendLimit = 12

// Recommendations lists quizzes an anonymous user may play right now.
//
// Three filters, each load-bearing:
//   - public + published + not deleted: the same predicate the logged-in quiz
//     list uses for out-of-institution content, so nothing private leaks.
//   - every question is multiple_choice: the pre-signup player renders that
//     type only. A quiz with one puzzle question would strand the user.
//   - domain in the picked topics, when any were picked.
//
// ponytail: MCQ-only is a player limitation, not a product one. Lift the
// NOT EXISTS clause once the question-type renderers are extracted out of
// numpie's quiz_attempt_screen.dart and reused here.
func (s *Service) Recommendations(ctx context.Context, sessionID string) ([]QuizSummary, error) {
	_, topics, err := s.Prefs(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(ctx,
		`SELECT q.id, q.title, q.description, q.domain, q.question_count
		   FROM quizzes q
		  WHERE q.visibility = 'public'
		    AND q.status = 'published'
		    AND q.deleted_at IS NULL
		    AND q.question_count > 0
		    AND (cardinality($1::text[]) = 0 OR q.domain = ANY($1::text[]))
		    AND NOT EXISTS (
		          SELECT 1 FROM questions qn
		           WHERE qn.quiz_id = q.id AND qn.type <> 'multiple_choice')
		  ORDER BY q.published_at DESC NULLS LAST
		  LIMIT $2`, topics, recommendLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []QuizSummary{}
	for rows.Next() {
		var q QuizSummary
		if err := rows.Scan(&q.ID, &q.Title, &q.Description, &q.Domain, &q.QuestionCount); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

var (
	// ErrQuizNotEligible means the quiz is not something an anonymous user may
	// play: private, unpublished, deleted, or carrying a question type the
	// pre-signup player cannot render.
	ErrQuizNotEligible  = errors.New("quiz not available before signup")
	ErrAlreadySubmitted = errors.New("calibration already submitted")
)

// Answer is one submitted answer. ElapsedMs is measured by the client — there
// is no server-side clock to measure against before an attempt exists — and is
// clamped when it is replayed into a real attempt at claim time.
type Answer struct {
	QuestionID string          `json:"question_id"`
	Answer     json.RawMessage `json:"answer"`
	ElapsedMs  int             `json:"elapsed_ms"`
}

// ReviewItem is one row of the post-quiz review. The score itself is
// deliberately absent from the response: it is the signup unlock.
type ReviewItem struct {
	QuestionID    string          `json:"question_id"`
	Correct       bool            `json:"correct"`
	CorrectAnswer json.RawMessage `json:"correct_answer"`
}

type SubmitResult struct {
	TotalCorrect   int          `json:"total_correct"`
	TotalQuestions int          `json:"total_questions"`
	Review         []ReviewItem `json:"review"`
}

// assertEligible rejects any quiz an anonymous user must not be handed. Same
// predicate as Recommendations, applied per quiz so a guessed id gets nothing.
func (s *Service) assertEligible(ctx context.Context, quizID string) error {
	var ok bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1 FROM quizzes q
		    WHERE q.id = $1
		      AND q.visibility = 'public'
		      AND q.status = 'published'
		      AND q.deleted_at IS NULL
		      AND NOT EXISTS (
		            SELECT 1 FROM questions qn
		             WHERE qn.quiz_id = q.id AND qn.type <> 'multiple_choice'))`,
		quizID).Scan(&ok)
	if err != nil || !ok {
		return ErrQuizNotEligible
	}
	return nil
}

// Questions returns the quiz's questions WITHOUT correct answers.
func (s *Service) Questions(ctx context.Context, sessionID, quizID string) ([]quiz.QuestionForStudent, error) {
	if _, _, err := s.Prefs(ctx, sessionID); err != nil {
		return nil, err
	}
	if err := s.assertEligible(ctx, quizID); err != nil {
		return nil, err
	}
	return s.quizSvc.GetQuestionsForStudent(ctx, quizID)
}

// Submit grades the calibration run server-side and stores the raw answers on
// the session, where claim-time replay will find them. It returns correctness
// only — no score_pct. Skipped questions count as wrong.
func (s *Service) Submit(ctx context.Context, sessionID, quizID string, answers []Answer) (*SubmitResult, error) {
	if _, _, err := s.Prefs(ctx, sessionID); err != nil {
		return nil, err
	}
	if err := s.assertEligible(ctx, quizID); err != nil {
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

	given := make(map[string]json.RawMessage, len(answers))
	for _, a := range answers {
		given[a.QuestionID] = a.Answer
	}

	result := &SubmitResult{TotalQuestions: len(questions), Review: make([]ReviewItem, 0, len(questions))}
	for _, q := range questions {
		ok, _ := scoring.ScoreQuestion(scoring.QuestionResponse{
			QuestionType:  q.Type,
			CorrectAnswer: q.CorrectAnswer,
			StudentAnswer: given[q.ID],
		}, cfg)
		if ok {
			result.TotalCorrect++
		}
		result.Review = append(result.Review, ReviewItem{
			QuestionID: q.ID, Correct: ok, CorrectAnswer: q.CorrectAnswer,
		})
	}

	stored, err := json.Marshal(answers)
	if err != nil {
		return nil, err
	}
	// submitted_at IS NULL in the predicate is what makes this single-use.
	ct, err := s.db.Exec(ctx,
		`UPDATE onboarding_sessions
		    SET quiz_id=$2, responses=$3, submitted_at=now()
		  WHERE id=$1 AND claimed_by IS NULL AND submitted_at IS NULL AND expires_at > now()`,
		sessionID, quizID, stored)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrAlreadySubmitted
	}
	return result, nil
}
