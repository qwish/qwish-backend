// Package onboardingsession holds the work a first-run user does before an
// account exists: their language and topic picks, and the calibration quiz
// they played. A session is claimed once, at signup, and then it is inert.
//
// The institution-registration handler in internal/domain/onboarding is a
// different thing entirely and is untouched by this package.
package onboardingsession

import (
	"context"
	"errors"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qwish/backend/internal/domain/quiz"
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
