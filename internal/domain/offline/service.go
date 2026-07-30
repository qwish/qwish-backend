// Package offline powers the app's offline mode: it bundles practice quizzes
// (with answers, so grading can happen on-device) for ahead-of-time download,
// and accepts batched practice results synced back when connectivity returns.
//
// Only non-competitive quizzes (type = 'knowledge_check') are packaged. Offline
// practice awards no points and never touches the leaderboard, so shipping the
// correct answers to the client is safe.
package offline

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

type PackQuestion struct {
	ID               string          `json:"id"`
	Position         int             `json:"position"`
	Type             string          `json:"type"`
	Prompt           string          `json:"prompt"`
	MediaURL         *string         `json:"media_url,omitempty"`
	Options          json.RawMessage `json:"options"`
	CorrectAnswer    json.RawMessage `json:"correct_answer"`
	TimeLimitSeconds int             `json:"time_limit_seconds"`
	Clues            json.RawMessage `json:"clues,omitempty"`
}

type PackQuiz struct {
	ID            string         `json:"id"`
	Title         string         `json:"title"`
	Description   *string        `json:"description,omitempty"`
	Type          string         `json:"type"`
	QuestionCount int            `json:"question_count"`
	UpdatedAt     time.Time      `json:"updated_at"`
	Questions     []PackQuestion `json:"questions"`
}

type Pack struct {
	Version   string     `json:"version"`   // RFC3339 of newest quiz in the pack
	Count     int        `json:"count"`
	Quizzes   []PackQuiz `json:"quizzes"`
}

// BuildPack returns every practice quiz visible to the user (their institution's
// published quizzes plus public ones) with full question payloads. The returned
// Version is the newest quiz updated_at; the caller may pass the client's last
// version as `since` to skip rebuilding when nothing changed (Changed=false).
func (s *Service) BuildPack(ctx context.Context, institutionID, since string) (*Pack, bool, error) {
	// Newest update across the candidate set; used as the pack version.
	var newest *time.Time
	s.db.QueryRow(ctx,
		`SELECT MAX(updated_at) FROM quizzes
		 WHERE type='knowledge_check' AND status='published' AND deleted_at IS NULL
		   AND (institution_id=$1::uuid OR visibility='public')`,
		nullable(institutionID),
	).Scan(&newest)

	version := ""
	if newest != nil {
		version = newest.UTC().Format(time.RFC3339Nano)
	}
	if since != "" && since == version {
		return &Pack{Version: version, Quizzes: []PackQuiz{}}, false, nil
	}

	rows, err := s.db.Query(ctx,
		`SELECT id, title, description, type, question_count, updated_at
		 FROM quizzes
		 WHERE type='knowledge_check' AND status='published' AND deleted_at IS NULL
		   AND (institution_id=$1::uuid OR visibility='public')
		 ORDER BY updated_at DESC`,
		nullable(institutionID))
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var quizzes []PackQuiz
	var ids []string
	for rows.Next() {
		var q PackQuiz
		if err := rows.Scan(&q.ID, &q.Title, &q.Description, &q.Type, &q.QuestionCount, &q.UpdatedAt); err != nil {
			return nil, false, err
		}
		q.Questions = []PackQuestion{}
		quizzes = append(quizzes, q)
		ids = append(ids, q.ID)
	}
	rows.Close()

	// Attach questions in one pass keyed by quiz_id.
	if len(ids) > 0 {
		byQuiz := map[string][]PackQuestion{}
		qrows, err := s.db.Query(ctx,
			`SELECT quiz_id, id, position, type, prompt, media_url, options, correct_answer, time_limit_seconds, clues
			 FROM questions WHERE quiz_id = ANY($1::uuid[]) ORDER BY quiz_id, position`, ids)
		if err != nil {
			return nil, false, err
		}
		defer qrows.Close()
		for qrows.Next() {
			var quizID string
			var q PackQuestion
			if err := qrows.Scan(&quizID, &q.ID, &q.Position, &q.Type, &q.Prompt, &q.MediaURL,
				&q.Options, &q.CorrectAnswer, &q.TimeLimitSeconds, &q.Clues); err != nil {
				return nil, false, err
			}
			byQuiz[quizID] = append(byQuiz[quizID], q)
		}
		for i := range quizzes {
			if qs := byQuiz[quizzes[i].ID]; qs != nil {
				quizzes[i].Questions = qs
			}
		}
	}

	if quizzes == nil {
		quizzes = []PackQuiz{}
	}
	return &Pack{Version: version, Count: len(quizzes), Quizzes: quizzes}, true, nil
}

// SyncResult is one practice session completed offline.
type SyncResult struct {
	ID             string          `json:"id"` // client-generated UUID (idempotency key)
	QuizID         *string         `json:"quiz_id"`
	TotalQuestions int             `json:"total_questions"`
	CorrectCount   int             `json:"correct_count"`
	ScorePct       float64         `json:"score_pct"`
	Answers        json.RawMessage `json:"answers,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at"`
}

// Sync persists a batch of offline practice sessions. Re-syncing the same id is
// a no-op (idempotent). Returns the number of newly-stored sessions.
func (s *Service) Sync(ctx context.Context, userID string, results []SyncResult) (int, error) {
	// One insert for the whole batch instead of one per session. A client coming
	// back online after a long trip can sync dozens of sessions at once, and the
	// per-row version paid a full network round trip for each.
	ids := make([]string, 0, len(results))
	quizIDs := make([]*string, 0, len(results))
	totals := make([]int, 0, len(results))
	corrects := make([]int, 0, len(results))
	scores := make([]float64, 0, len(results))
	answers := make([][]byte, 0, len(results))
	completedAts := make([]time.Time, 0, len(results))

	for _, res := range results {
		if res.ID == "" {
			continue
		}
		completed := time.Now()
		if res.CompletedAt != nil {
			completed = *res.CompletedAt
		}
		var ans []byte
		if len(res.Answers) > 0 {
			ans = res.Answers
		}
		ids = append(ids, res.ID)
		quizIDs = append(quizIDs, res.QuizID)
		totals = append(totals, res.TotalQuestions)
		corrects = append(corrects, res.CorrectCount)
		scores = append(scores, res.ScorePct)
		answers = append(answers, ans)
		completedAts = append(completedAts, completed)
	}
	if len(ids) == 0 {
		return 0, nil
	}

	ct, err := s.db.Exec(ctx,
		`INSERT INTO practice_sessions
		   (id, user_id, quiz_id, total_questions, correct_count, score_pct, answers, completed_at)
		 SELECT id, $2, quiz_id, total_questions, correct_count, score_pct, answers, completed_at
		   FROM unnest($1::uuid[], $3::uuid[], $4::int[], $5::int[], $6::float8[], $7::jsonb[], $8::timestamptz[])
		     AS t(id, quiz_id, total_questions, correct_count, score_pct, answers, completed_at)
		 ON CONFLICT (id) DO NOTHING`,
		ids, userID, quizIDs, totals, corrects, scores, answers, completedAts)
	if err != nil {
		return 0, err
	}
	return int(ct.RowsAffected()), nil
}

// nullable converts an empty string to a nil for SQL params that compare against
// a UUID column (so the OR clause degrades to just the public check).
func nullable(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
