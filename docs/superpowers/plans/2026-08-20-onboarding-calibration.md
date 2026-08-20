# Onboarding Calibration Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A first-run user picks a language and topics, plays a calibration quiz before signing up, and the account they create at the end already carries those preferences and a real Qwish Score.

**Architecture:** A server-side `onboarding_sessions` row holds the anonymous user's preferences and graded answers, keyed by an opaque session id the client stores locally. `POST /auth/create-profile` accepts that id and claims the session: preferences are copied onto the new user, and the stored answers are replayed through the existing attempt engine so scoring, points, and streak logic stay in one place. Nothing is client-reported except the raw answers and the per-question elapsed time, both re-graded server-side.

**Tech Stack:** Go 1.26, chi, pgx/v5, Postgres (Supabase) on the backend. Flutter 3.11+, go_router, flutter_bloc, get_it, dio, shared_preferences on the client.

**Spec:** `qwish-backend/docs/superpowers/specs/2026-08-20-onboarding-calibration-design.md`

## Global Constraints

- Two repositories, no root repo: `qwish-backend/` and `numpie/` are separate git repos. Commit in the repo you touched. A task touching both commits twice.
- Migrations are append-only and ordered by filename. The next free number is `041`. Never edit a shipped migration.
- Topics are the existing `domains` taxonomy from migration 020: `aptitude`, `quantitative`, `logical`, `verbal`, `computer_science`, `general`. Do not create a topic table.
- Language codes accepted by the API: `en`, `hi`, `mr`. Only `en` renders as a locale; the other two are stored preferences.
- The Qwish Score is not redefined by this work. It is `scoring.CalculateQwishScore`, written to `quiz_attempts.score_pct` by `attempt.Service.Complete`.
- The calibration player supports `multiple_choice` questions only. Recommendations must exclude quizzes containing any other question type.
- Exact CTA copy on the personalisation screen: `Start calibrating your Qwish score`.
- Public endpoints are rate limited with `mw.RateLimit`, matching the existing demo endpoints.
- Existing `/demo` routes and `DemoLauncherScreen` are not modified by any task in this plan.

---

## File Structure

**qwish-backend**

| File | Responsibility |
|---|---|
| `migrations/041_onboarding_calibration.sql` | create `onboarding_sessions`, add two `users` columns, RLS |
| `internal/domain/onboardingsession/service.go` | session lifecycle: create, update prefs, recommendations, questions, submit |
| `internal/domain/onboardingsession/claim.go` | claim at signup: copy prefs, replay answers |
| `internal/domain/onboardingsession/handler.go` | HTTP layer for the five public endpoints |
| `internal/domain/onboardingsession/validate_test.go` | pure unit tests for language/topic validation |
| `internal/domain/onboardingsession/testdb_test.go` | `openTestDB` helper (copy of the enrollment package pattern) |
| `internal/domain/onboardingsession/service_test.go` | DB-backed lifecycle tests |
| `internal/domain/onboardingsession/claim_test.go` | DB-backed claim tests |
| `internal/domain/attempt/service.go` (modify) | split `SubmitAnswer` into a shared core with an optional elapsed-time override; add `ReplayAnswer` |
| `internal/domain/auth/handler.go` (modify) | accept `onboarding_session`, call the claimer |
| `internal/scheduler/scheduler.go` (modify) | `PurgeOnboardingSessions` |
| `cmd/api/main.go` (modify) | wire service, routes, cron endpoint |
| `render.yaml` (modify) | add the purge to the nightly cron |

**numpie**

| File | Responsibility |
|---|---|
| `lib/core/l10n/locale_prefs.dart` | persisted `Locale` preference |
| `lib/core/l10n/supported_locales.dart` | the three language options and their labels |
| `lib/features/onboarding/data/onboarding_repository.dart` | the five endpoints + models |
| `lib/features/onboarding/data/onboarding_session_storage.dart` | session id persistence |
| `lib/features/onboarding/presentation/personalise_screen.dart` | language + topics picker |
| `lib/features/onboarding/presentation/calibrate_list_screen.dart` | recommended quizzes |
| `lib/features/onboarding/presentation/calibrate_play_screen.dart` | MCQ player with per-question timing |
| `lib/features/onboarding/presentation/calibrate_result_screen.dart` | review + locked score card |
| `lib/carousel/carousel_data.dart` (modify) | six slides down to three |
| `lib/core/router/route_names.dart`, `app_router.dart` (modify) | four public routes |
| `lib/features/auth/data/auth_repository.dart` (modify) | `createProfile` sends the session id |
| `lib/main.dart` (modify) | `locale` + `localizationsDelegates` |

---

### Task 1: Migration — sessions table and user preference columns

**Files:**
- Create: `qwish-backend/migrations/041_onboarding_calibration.sql`

**Interfaces:**
- Consumes: nothing.
- Produces: table `onboarding_sessions(id, language, topics, quiz_id, responses, submitted_at, claimed_by, claimed_at, expires_at, created_at)`; columns `users.preferred_language TEXT NOT NULL DEFAULT 'en'` and `users.interest_domains TEXT[] NOT NULL DEFAULT '{}'`.

- [ ] **Step 1: Write the migration**

```sql
-- Migration 041: Onboarding calibration.
--
-- A first-run user picks preferences and plays one quiz before an account
-- exists. onboarding_sessions is where that work lives until signup claims it.
-- The session id IS the credential: it is unguessable, single-use, and expires.

CREATE TABLE IF NOT EXISTS onboarding_sessions (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  language     TEXT NOT NULL DEFAULT 'en',
  topics       TEXT[] NOT NULL DEFAULT '{}',
  quiz_id      UUID REFERENCES quizzes(id),
  responses    JSONB,
  submitted_at TIMESTAMPTZ,
  claimed_by   UUID REFERENCES users(id),
  claimed_at   TIMESTAMPTZ,
  expires_at   TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The purge job's only predicate.
CREATE INDEX IF NOT EXISTS idx_onboarding_sessions_expires
  ON onboarding_sessions(expires_at) WHERE claimed_by IS NULL;

-- Reached only through the service-role pool, like the other tables covered
-- in migration 033. Enabled with no permissive policy: anon and authenticated
-- roles get nothing.
ALTER TABLE onboarding_sessions ENABLE ROW LEVEL SECURITY;

ALTER TABLE users ADD COLUMN IF NOT EXISTS preferred_language TEXT NOT NULL DEFAULT 'en';
ALTER TABLE users ADD COLUMN IF NOT EXISTS interest_domains   TEXT[] NOT NULL DEFAULT '{}';
```

- [ ] **Step 2: Verify it applies**

The migrator runs on boot and records versions in `schema_migrations`.

Run: `cd qwish-backend && go build -o bin/api ./cmd/api && ./bin/api`
Expected: startup log shows migration 041 applied, no error. Stop the server.

- [ ] **Step 3: Verify the columns exist**

Run:
```bash
psql "$DATABASE_URL" -c "\d onboarding_sessions" -c "\d users" | grep -E "preferred_language|interest_domains|expires_at"
```
Expected: all three lines present.

- [ ] **Step 4: Commit**

```bash
cd qwish-backend
git add migrations/041_onboarding_calibration.sql
git commit -m "feat(db): onboarding calibration sessions and user preferences"
```

---

### Task 2: Session package — validation and lifecycle

**Files:**
- Create: `qwish-backend/internal/domain/onboardingsession/service.go`
- Create: `qwish-backend/internal/domain/onboardingsession/validate_test.go`
- Create: `qwish-backend/internal/domain/onboardingsession/testdb_test.go`
- Create: `qwish-backend/internal/domain/onboardingsession/service_test.go`

**Interfaces:**
- Consumes: the table from Task 1.
- Produces:
  - `func NewService(db *pgxpool.Pool, quizSvc *quiz.Service) *Service`
  - `func (s *Service) Create(ctx context.Context, language string, topics []string) (string, error)`
  - `func (s *Service) UpdatePrefs(ctx context.Context, sessionID, language string, topics []string) error`
  - `func (s *Service) Prefs(ctx context.Context, sessionID string) (language string, topics []string, err error)`
  - `var ErrSession = errors.New("session not found or expired")`
  - `var ErrBadLanguage`, `var ErrBadTopic`

- [ ] **Step 1: Write the failing validation test**

`internal/domain/onboardingsession/validate_test.go`:

```go
package onboardingsession

import "testing"

func TestNormalizeLanguage(t *testing.T) {
	for _, in := range []string{"en", "hi", "mr"} {
		got, err := normalizeLanguage(in)
		if err != nil || got != in {
			t.Fatalf("normalizeLanguage(%q) = %q, %v; want %q, nil", in, got, err, in)
		}
	}

	// Empty falls back to the default rather than erroring: a user who skips
	// the picker still gets a session.
	got, err := normalizeLanguage("")
	if err != nil || got != "en" {
		t.Fatalf(`normalizeLanguage("") = %q, %v; want "en", nil`, got, err)
	}

	if _, err := normalizeLanguage("klingon"); err == nil {
		t.Fatal("normalizeLanguage(\"klingon\") returned nil error; want ErrBadLanguage")
	}
}

func TestNormalizeTopics(t *testing.T) {
	got, err := normalizeTopics([]string{"verbal", "logical", "verbal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Deduplicated and sorted, so two sessions with the same picks compare equal.
	if len(got) != 2 || got[0] != "logical" || got[1] != "verbal" {
		t.Fatalf("normalizeTopics = %v; want [logical verbal]", got)
	}

	empty, err := normalizeTopics(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("normalizeTopics(nil) = %v, %v; want empty, nil", empty, err)
	}

	if _, err := normalizeTopics([]string{"astrology"}); err == nil {
		t.Fatal("normalizeTopics([astrology]) returned nil error; want ErrBadTopic")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd qwish-backend && go test ./internal/domain/onboardingsession/`
Expected: build failure — `undefined: normalizeLanguage`.

- [ ] **Step 3: Write the service**

`internal/domain/onboardingsession/service.go`:

```go
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
```

- [ ] **Step 4: Run the validation test to verify it passes**

Run: `cd qwish-backend && go test ./internal/domain/onboardingsession/ -run 'TestNormalize' -v`
Expected: PASS for both tests.

- [ ] **Step 5: Add the DB test harness**

`internal/domain/onboardingsession/testdb_test.go`:

```go
package onboardingsession

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestDB connects to TEST_DATABASE_URL, or skips the test.
//
// Point TEST_DATABASE_URL at a scratch database — these tests write rows.
func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to TEST_DATABASE_URL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("ping TEST_DATABASE_URL: %v", err)
	}
	return pool
}
```

- [ ] **Step 6: Write the failing lifecycle test**

`internal/domain/onboardingsession/service_test.go`:

```go
package onboardingsession

import (
	"context"
	"testing"
)

func TestSessionLifecycle(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	id, err := svc.Create(ctx, "hi", []string{"verbal", "logical"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id)
	})

	lang, topics, err := svc.Prefs(ctx, id)
	if err != nil || lang != "hi" || len(topics) != 2 {
		t.Fatalf("Prefs = %q, %v, %v; want hi, 2 topics, nil", lang, topics, err)
	}

	if err := svc.UpdatePrefs(ctx, id, "en", []string{"general"}); err != nil {
		t.Fatalf("UpdatePrefs: %v", err)
	}
	lang, topics, _ = svc.Prefs(ctx, id)
	if lang != "en" || len(topics) != 1 || topics[0] != "general" {
		t.Fatalf("after update: %q %v; want en [general]", lang, topics)
	}
}

func TestExpiredSessionIsInvisible(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	id, err := svc.Create(ctx, "en", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id)
	})

	if _, err := pool.Exec(ctx,
		`UPDATE onboarding_sessions SET expires_at = now() - interval '1 minute' WHERE id=$1`, id); err != nil {
		t.Fatalf("age the session: %v", err)
	}

	if _, _, err := svc.Prefs(ctx, id); err != ErrSession {
		t.Fatalf("Prefs on expired session = %v; want ErrSession", err)
	}
	if err := svc.UpdatePrefs(ctx, id, "en", nil); err != ErrSession {
		t.Fatalf("UpdatePrefs on expired session = %v; want ErrSession", err)
	}
}
```

- [ ] **Step 7: Run the DB tests**

Run: `cd qwish-backend && TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/domain/onboardingsession/ -v`
Expected: PASS. Without `TEST_DATABASE_URL` the DB tests skip and the validation tests still pass.

- [ ] **Step 8: Commit**

```bash
cd qwish-backend
git add internal/domain/onboardingsession/
git commit -m "feat(onboarding): session lifecycle with validated language and topics"
```

---

### Task 3: Recommendations query

**Files:**
- Modify: `qwish-backend/internal/domain/onboardingsession/service.go`
- Modify: `qwish-backend/internal/domain/onboardingsession/service_test.go`

**Interfaces:**
- Consumes: `Service`, `ErrSession` from Task 2.
- Produces:
  - `type QuizSummary struct { ID, Title string; Description, Domain *string; QuestionCount int }` with JSON tags `id`, `title`, `description`, `domain`, `question_count`
  - `func (s *Service) Recommendations(ctx context.Context, sessionID string) ([]QuizSummary, error)`

- [ ] **Step 1: Write the failing test**

Append to `internal/domain/onboardingsession/service_test.go`:

```go
func TestRecommendationsExcludeNonMCQAndRespectTopics(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	// A system author to hang quizzes off. Migration 023 seeds one.
	var author string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email='system@qwish.internal'`).Scan(&author); err != nil {
		t.Skipf("system author missing: %v", err)
	}

	mk := func(title, domain, qType string) string {
		var qid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO quizzes (created_by, title, type, visibility, status, question_count, domain, published_at)
			 VALUES ($1,$2,'knowledge_check','public','published',1,$3, now()) RETURNING id`,
			author, title, domain).Scan(&qid); err != nil {
			t.Fatalf("insert quiz: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
			 VALUES ($1,1,$2,'p','["A","B"]','"A"',60)`, qid, qType); err != nil {
			t.Fatalf("insert question: %v", err)
		}
		t.Cleanup(func() {
			pool.Exec(ctx, `DELETE FROM questions WHERE quiz_id=$1`, qid)
			pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, qid)
		})
		return qid
	}

	wanted := mk("calib-mcq-verbal", "verbal", "multiple_choice")
	puzzle := mk("calib-puzzle-verbal", "verbal", "puzzle")
	offTopic := mk("calib-mcq-logical", "logical", "multiple_choice")

	id, err := svc.Create(ctx, "en", []string{"verbal"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id) })

	got, err := svc.Recommendations(ctx, id)
	if err != nil {
		t.Fatalf("Recommendations: %v", err)
	}

	ids := map[string]bool{}
	for _, q := range got {
		ids[q.ID] = true
	}
	if !ids[wanted] {
		t.Error("MCQ quiz in a picked topic was not recommended")
	}
	if ids[puzzle] {
		t.Error("quiz with a non-MCQ question was recommended; the player cannot render it")
	}
	if ids[offTopic] {
		t.Error("quiz outside the picked topics was recommended")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd qwish-backend && TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/domain/onboardingsession/ -run TestRecommendations`
Expected: build failure — `svc.Recommendations undefined`.

- [ ] **Step 3: Implement Recommendations**

Append to `internal/domain/onboardingsession/service.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd qwish-backend && TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/domain/onboardingsession/ -run TestRecommendations -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd qwish-backend
git add internal/domain/onboardingsession/
git commit -m "feat(onboarding): recommend public MCQ quizzes by picked topic"
```

---

### Task 4: Questions and submit

**Files:**
- Modify: `qwish-backend/internal/domain/onboardingsession/service.go`
- Modify: `qwish-backend/internal/domain/onboardingsession/service_test.go`

**Interfaces:**
- Consumes: `Service`, `ErrSession`, `Recommendations` from Tasks 2-3.
- Produces:
  - `type Answer struct { QuestionID string; Answer json.RawMessage; ElapsedMs int }` — JSON tags `question_id`, `answer`, `elapsed_ms`
  - `type ReviewItem struct { QuestionID string; Correct bool; CorrectAnswer json.RawMessage }` — JSON tags `question_id`, `correct`, `correct_answer`
  - `type SubmitResult struct { TotalCorrect, TotalQuestions int; Review []ReviewItem }` — JSON tags `total_correct`, `total_questions`, `review`
  - `func (s *Service) Questions(ctx context.Context, sessionID, quizID string) ([]quiz.QuestionForStudent, error)`
  - `func (s *Service) Submit(ctx context.Context, sessionID, quizID string, answers []Answer) (*SubmitResult, error)`
  - `var ErrQuizNotEligible`, `var ErrAlreadySubmitted`

- [ ] **Step 1: Write the failing test**

Append to `internal/domain/onboardingsession/service_test.go`:

```go
func TestSubmitGradesAndIsSingleUse(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	quizSvc := quiz.NewService(pool)
	svc := NewService(pool, quizSvc)

	var author string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email='system@qwish.internal'`).Scan(&author); err != nil {
		t.Skipf("system author missing: %v", err)
	}

	var quizID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO quizzes (created_by, title, type, visibility, status, question_count, domain, published_at)
		 VALUES ($1,'calib-submit','knowledge_check','public','published',2,'general', now()) RETURNING id`,
		author).Scan(&quizID); err != nil {
		t.Fatalf("insert quiz: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM questions WHERE quiz_id=$1`, quizID)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quizID)
	})

	var q1, q2 string
	pool.QueryRow(ctx,
		`INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
		 VALUES ($1,1,'multiple_choice','one','["A","B"]','"A"',60) RETURNING id`, quizID).Scan(&q1)
	pool.QueryRow(ctx,
		`INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
		 VALUES ($1,2,'multiple_choice','two','["A","B"]','"B"',60) RETURNING id`, quizID).Scan(&q2)

	sess, err := svc.Create(ctx, "en", []string{"general"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, sess) })

	// Questions must never carry the answer key.
	qs, err := svc.Questions(ctx, sess, quizID)
	if err != nil {
		t.Fatalf("Questions: %v", err)
	}
	if len(qs) != 2 {
		t.Fatalf("got %d questions, want 2", len(qs))
	}
	raw, _ := json.Marshal(qs)
	if bytes.Contains(raw, []byte("correct_answer")) {
		t.Fatal("questions payload contains correct_answer")
	}

	res, err := svc.Submit(ctx, sess, quizID, []Answer{
		{QuestionID: q1, Answer: json.RawMessage(`"A"`), ElapsedMs: 4000},
		{QuestionID: q2, Answer: json.RawMessage(`"A"`), ElapsedMs: 5000},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.TotalCorrect != 1 || res.TotalQuestions != 2 {
		t.Fatalf("got %d/%d, want 1/2", res.TotalCorrect, res.TotalQuestions)
	}
	if len(res.Review) != 2 {
		t.Fatalf("got %d review items, want 2", len(res.Review))
	}

	// A second submit would let a user grind the calibration for a better score.
	if _, err := svc.Submit(ctx, sess, quizID, nil); err != ErrAlreadySubmitted {
		t.Fatalf("second Submit = %v; want ErrAlreadySubmitted", err)
	}
}
```

Add to that file's imports: `"bytes"`, `"encoding/json"`, `"github.com/qwish/backend/internal/domain/quiz"`.

- [ ] **Step 2: Run it to verify it fails**

Run: `cd qwish-backend && TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/domain/onboardingsession/ -run TestSubmit`
Expected: build failure — `svc.Questions undefined`.

- [ ] **Step 3: Implement Questions and Submit**

Append to `internal/domain/onboardingsession/service.go` (add imports `"encoding/json"` and `"github.com/qwish/backend/internal/domain/scoring"`):

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd qwish-backend && TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/domain/onboardingsession/ -run TestSubmit -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd qwish-backend
git add internal/domain/onboardingsession/
git commit -m "feat(onboarding): serve calibration questions and grade submissions"
```

---

### Task 5: HTTP handlers and routes

**Files:**
- Create: `qwish-backend/internal/domain/onboardingsession/handler.go`
- Modify: `qwish-backend/cmd/api/main.go`

**Interfaces:**
- Consumes: every method from Tasks 2-4.
- Produces:
  - `func NewHandler(svc *Service) *Handler`
  - methods `Create`, `UpdatePrefs`, `Recommendations`, `Questions`, `Submit`
  - live routes under `/api/v1/onboarding/session`

- [ ] **Step 1: Write the handler**

`internal/domain/onboardingsession/handler.go`:

```go
package onboardingsession

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

type prefsReq struct {
	Language string   `json:"language"`
	Topics   []string `json:"topics"`
}

// respond maps the package's sentinel errors onto status codes. A missing and
// an expired session are both 404: the caller is anonymous and telling them
// apart only helps someone guessing ids.
func respond(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBadLanguage):
		middleware.BadRequest(w, "unsupported language")
	case errors.Is(err, ErrBadTopic):
		middleware.BadRequest(w, "unknown topic")
	case errors.Is(err, ErrSession):
		middleware.NotFound(w, "session")
	case errors.Is(err, ErrQuizNotEligible):
		middleware.NotFound(w, "quiz")
	case errors.Is(err, ErrAlreadySubmitted):
		middleware.BadRequest(w, "this calibration was already submitted")
	default:
		middleware.InternalError(w)
	}
}

// POST /api/v1/onboarding/session
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req prefsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	id, err := h.svc.Create(r.Context(), req.Language, req.Topics)
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"session_id": id})
}

// PATCH /api/v1/onboarding/session/{sessionId}
func (h *Handler) UpdatePrefs(w http.ResponseWriter, r *http.Request) {
	var req prefsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if err := h.svc.UpdatePrefs(r.Context(), chi.URLParam(r, "sessionId"), req.Language, req.Topics); err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/v1/onboarding/session/{sessionId}/recommendations
func (h *Handler) Recommendations(w http.ResponseWriter, r *http.Request) {
	quizzes, err := h.svc.Recommendations(r.Context(), chi.URLParam(r, "sessionId"))
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, quizzes)
}

// GET /api/v1/onboarding/session/{sessionId}/quizzes/{quizId}
func (h *Handler) Questions(w http.ResponseWriter, r *http.Request) {
	questions, err := h.svc.Questions(r.Context(), chi.URLParam(r, "sessionId"), chi.URLParam(r, "quizId"))
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, questions)
}

// POST /api/v1/onboarding/session/{sessionId}/submit
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QuizID  string   `json:"quiz_id"`
		Answers []Answer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.QuizID == "" {
		middleware.BadRequest(w, "quiz_id and answers are required")
		return
	}
	result, err := h.svc.Submit(r.Context(), chi.URLParam(r, "sessionId"), req.QuizID, req.Answers)
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}
```

- [ ] **Step 2: Wire the service and handler in main.go**

Add the import next to the other domain imports:

```go
	"github.com/qwish/backend/internal/domain/onboardingsession"
```

Next to `demoSvc := demo.NewService(pool, quizSvc)` add:

```go
	obSessionSvc := onboardingsession.NewService(pool, quizSvc)
```

Next to `demoH := demo.NewHandler(demoSvc)` add:

```go
	obSessionH := onboardingsession.NewHandler(obSessionSvc)
```

- [ ] **Step 3: Register the routes**

Inside the existing `r.Route("/onboarding", ...)` block, after the two institution routes:

```go
				// Pre-signup calibration. Public and unauthenticated: the
				// session id is the only credential, so rate-limit per IP.
				r.Route("/session", func(r chi.Router) {
					r.With(mw.RateLimit(10, 10*time.Minute)).Post("/", obSessionH.Create)
					r.Patch("/{sessionId}", obSessionH.UpdatePrefs)
					r.Get("/{sessionId}/recommendations", obSessionH.Recommendations)
					r.Get("/{sessionId}/quizzes/{quizId}", obSessionH.Questions)
					r.With(mw.RateLimit(30, 10*time.Minute)).Post("/{sessionId}/submit", obSessionH.Submit)
				})
```

- [ ] **Step 4: Build and smoke test the endpoints**

Run:
```bash
cd qwish-backend && go build -o bin/api ./cmd/api && ./bin/api &
sleep 3
SID=$(curl -fsS -X POST localhost:8080/api/v1/onboarding/session \
  -H 'Content-Type: application/json' \
  -d '{"language":"en","topics":["general"]}' | python3 -c 'import sys,json; print(json.load(sys.stdin)["data"]["session_id"])')
echo "session: $SID"
curl -fsS "localhost:8080/api/v1/onboarding/session/$SID/recommendations"
curl -s -o /dev/null -w '%{http_code}\n' "localhost:8080/api/v1/onboarding/session/00000000-0000-0000-0000-000000000000/recommendations"
```
Expected: a session id, a JSON list (possibly empty), and `404` for the bogus session. Stop the server.

- [ ] **Step 5: Verify a bad language is rejected**

Run:
```bash
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8080/api/v1/onboarding/session \
  -H 'Content-Type: application/json' -d '{"language":"klingon"}'
```
Expected: `400`.

- [ ] **Step 6: Commit**

```bash
cd qwish-backend
git add internal/domain/onboardingsession/handler.go cmd/api/main.go
git commit -m "feat(onboarding): public calibration session endpoints"
```

---

### Task 6: Replayable answers in the attempt engine

**Files:**
- Modify: `qwish-backend/internal/domain/attempt/service.go:152-258`
- Create: `qwish-backend/internal/domain/attempt/replay_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces:
  - `func (s *Service) ReplayAnswer(ctx context.Context, userID, attemptID string, req AnswerReq, elapsedMs int) (*AnswerResp, error)`
  - `func clampReplayMs(ms int) int`
  - `SubmitAnswer` keeps its exact signature and behaviour.

**Why this exists:** `SubmitAnswer` measures elapsed time from the DB clock (`now() - last_answer_at`) so a client cannot understate it. Replaying stored answers at signup would produce near-zero times, and `Complete` scores anything under 1000ms at `qSpeed = 0.1` — the replayed attempt would be penalised for being replayed. `ReplayAnswer` passes the time the client actually measured during the calibration run, clamped.

- [ ] **Step 1: Write the failing test**

`internal/domain/attempt/replay_test.go`:

```go
package attempt

import "testing"

func TestClampReplayMs(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, 0},           // nonsense floors at zero
		{0, 0},            // instant answers stay instant and take the guess penalty
		{4200, 4200},      // ordinary value passes through
		{600000, 600000},  // exactly the cap
		{9_999_999, 600000}, // a client claiming three hours per question is capped
	}
	for _, c := range cases {
		if got := clampReplayMs(c.in); got != c.want {
			t.Errorf("clampReplayMs(%d) = %d; want %d", c.in, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd qwish-backend && go test ./internal/domain/attempt/ -run TestClampReplayMs`
Expected: build failure — `undefined: clampReplayMs`.

- [ ] **Step 3: Split SubmitAnswer and add ReplayAnswer**

In `internal/domain/attempt/service.go`, rename the existing method `func (s *Service) SubmitAnswer(ctx context.Context, userID, attemptID string, req AnswerReq) (*AnswerResp, error)` to:

```go
func (s *Service) submitAnswer(ctx context.Context, userID, attemptID string, req AnswerReq, elapsedOverride *int) (*AnswerResp, error) {
```

Immediately after the block that scans `quizID, cfgSnapshot, comboLevel, timeTakenMs` and checks its error, insert:

```go
	// A replayed answer carries the time the client measured before any attempt
	// row existed; there is no DB clock to derive it from.
	if elapsedOverride != nil {
		timeTakenMs = *elapsedOverride
	}
```

Then add the two public wrappers directly above it:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd qwish-backend && go test ./internal/domain/attempt/ -v`
Expected: PASS, including the pre-existing attempt tests — `SubmitAnswer` must be unchanged for live play.

- [ ] **Step 5: Commit**

```bash
cd qwish-backend
git add internal/domain/attempt/
git commit -m "feat(attempt): replayable answers with client-measured elapsed time"
```

---

### Task 7: Claim at signup

**Files:**
- Create: `qwish-backend/internal/domain/onboardingsession/claim.go`
- Create: `qwish-backend/internal/domain/onboardingsession/claim_test.go`
- Modify: `qwish-backend/internal/domain/auth/handler.go:159-270`
- Modify: `qwish-backend/cmd/api/main.go`

**Interfaces:**
- Consumes: `Service` (Tasks 2-4), `attempt.Service.Start`, `ReplayAnswer` (Task 6), `attempt.Service.Complete`.
- Produces:
  - `func (s *Service) SetAttempts(a Attempts)` where `type Attempts interface { Start(ctx, userID, quizID string) (*attempt.StartAttemptResp, error); ReplayAnswer(ctx, userID, attemptID string, req attempt.AnswerReq, elapsedMs int) (*attempt.AnswerResp, error); Complete(ctx, userID, attemptID string) (*attempt.CompleteResp, error) }`
  - `func (s *Service) Claim(ctx context.Context, sessionID, userID string) error`
  - `func (h *auth.Handler) SetOnboardingClaimer(c auth.OnboardingClaimer)` where `type OnboardingClaimer interface { Claim(ctx context.Context, sessionID, userID string) error }`
  - `create-profile` accepts `"onboarding_session"`.

- [ ] **Step 1: Write the failing claim test**

`internal/domain/onboardingsession/claim_test.go`:

```go
package onboardingsession

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestClaimAppliesPrefsAndIsSingleUse(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		 VALUES (gen_random_uuid(), 'Calib Tester', 'Calib', $1, 'student') RETURNING id`,
		"calib+"+tag+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) })

	sess, err := svc.Create(ctx, "hi", []string{"verbal", "logical"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, sess) })

	if err := svc.Claim(ctx, sess, userID); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var lang string
	var topics []string
	if err := pool.QueryRow(ctx,
		`SELECT preferred_language, interest_domains FROM users WHERE id=$1`, userID).
		Scan(&lang, &topics); err != nil {
		t.Fatalf("read back user: %v", err)
	}
	if lang != "hi" || len(topics) != 2 {
		t.Fatalf("user prefs = %q %v; want hi and 2 topics", lang, topics)
	}

	// Claiming twice must not reapply or error the caller's signup.
	if err := svc.Claim(ctx, sess, userID); err != ErrSession {
		t.Fatalf("second Claim = %v; want ErrSession", err)
	}
}

func TestClaimOfExpiredSessionReportsErrSession(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID string
	pool.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		 VALUES (gen_random_uuid(), 'Calib Expired', 'Calib', $1, 'student') RETURNING id`,
		"calibx+"+tag+"@example.test").Scan(&userID)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) })

	sess, _ := svc.Create(ctx, "en", nil)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, sess) })
	pool.Exec(ctx, `UPDATE onboarding_sessions SET expires_at = now() - interval '1 minute' WHERE id=$1`, sess)

	if err := svc.Claim(ctx, sess, userID); err != ErrSession {
		t.Fatalf("Claim of expired session = %v; want ErrSession", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd qwish-backend && TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/domain/onboardingsession/ -run TestClaim`
Expected: build failure — `svc.Claim undefined`.

- [ ] **Step 3: Implement Claim**

`internal/domain/onboardingsession/claim.go`:

```go
package onboardingsession

import (
	"context"
	"encoding/json"
	"log"

	"github.com/qwish/backend/internal/domain/attempt"
)

// Attempts is the slice of the attempt service the claim path needs. An
// interface rather than the concrete type so the dependency stays one-way and
// testable without a full attempt service.
type Attempts interface {
	Start(ctx context.Context, userID, quizID string) (*attempt.StartAttemptResp, error)
	ReplayAnswer(ctx context.Context, userID, attemptID string, req attempt.AnswerReq, elapsedMs int) (*attempt.AnswerResp, error)
	Complete(ctx context.Context, userID, attemptID string) (*attempt.CompleteResp, error)
}

// SetAttempts wires the attempt engine. Optional: without it a claim still
// applies preferences and skips the replay.
func (s *Service) SetAttempts(a Attempts) { s.attempts = a }

// Claim hands a session's contents to a freshly created user.
//
// Preferences are applied atomically with the claim marker, so a session is
// never half-applied and never applied twice. The quiz replay runs afterwards
// and outside that transaction: it walks the ordinary attempt engine, which
// manages its own transactions per answer, and a failure there must not cost
// the user their preferences or their account.
func (s *Service) Claim(ctx context.Context, sessionID, userID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var language string
	var topics []string
	var quizID *string
	var responses []byte
	err = tx.QueryRow(ctx,
		`SELECT language, topics, quiz_id, responses
		   FROM onboarding_sessions
		  WHERE id=$1 AND claimed_by IS NULL AND expires_at > now()
		  FOR UPDATE`,
		sessionID,
	).Scan(&language, &topics, &quizID, &responses)
	if err != nil {
		return ErrSession
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET preferred_language=$2, interest_domains=$3, updated_at=now() WHERE id=$1`,
		userID, language, topics); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE onboarding_sessions SET claimed_by=$2, claimed_at=now() WHERE id=$1`,
		sessionID, userID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	if s.attempts != nil && quizID != nil && len(responses) > 0 {
		s.replay(ctx, userID, *quizID, responses)
	}
	return nil
}

// replay turns the stored calibration answers into a real completed attempt,
// so score_pct, points, streak and the ledger all come out of the same engine
// that serves logged-in play. Failures are logged, never surfaced: the user has
// just created an account and must land on home either way.
func (s *Service) replay(ctx context.Context, userID, quizID string, responses []byte) {
	var answers []Answer
	if err := json.Unmarshal(responses, &answers); err != nil {
		log.Printf("onboarding claim: decode responses for user %s: %v", userID, err)
		return
	}

	started, err := s.attempts.Start(ctx, userID, quizID)
	if err != nil {
		log.Printf("onboarding claim: start attempt for user %s: %v", userID, err)
		return
	}
	for _, a := range answers {
		if _, err := s.attempts.ReplayAnswer(ctx, userID, started.AttemptID, attempt.AnswerReq{
			QuestionID: a.QuestionID,
			Answer:     a.Answer,
		}, a.ElapsedMs); err != nil {
			log.Printf("onboarding claim: replay answer %s for user %s: %v", a.QuestionID, userID, err)
		}
	}
	if _, err := s.attempts.Complete(ctx, userID, started.AttemptID); err != nil {
		log.Printf("onboarding claim: complete attempt for user %s: %v", userID, err)
	}
}
```

In `service.go`, add the field to `Service`:

```go
type Service struct {
	db       *pgxpool.Pool
	quizSvc  *quiz.Service
	attempts Attempts
}
```

- [ ] **Step 4: Run the claim tests to verify they pass**

Run: `cd qwish-backend && TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/domain/onboardingsession/ -run TestClaim -v`
Expected: PASS for both.

- [ ] **Step 5: Write the failing replay test**

Append to `internal/domain/onboardingsession/claim_test.go` (add imports `"encoding/json"`, `"github.com/qwish/backend/internal/domain/attempt"`, `"github.com/qwish/backend/internal/domain/quiz"`, `"github.com/qwish/backend/internal/domain/streak"`):

```go
func TestClaimReplaysCalibrationIntoAScoredAttempt(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	quizSvc := quiz.NewService(pool)
	svc := NewService(pool, quizSvc)
	svc.SetAttempts(attempt.NewService(pool, quizSvc, streak.NewService(pool)))

	var author string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email='system@qwish.internal'`).Scan(&author); err != nil {
		t.Skipf("system author missing: %v", err)
	}

	var quizID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO quizzes (created_by, title, type, visibility, status, question_count, domain, published_at)
		 VALUES ($1,'calib-replay','knowledge_check','public','published',2,'general', now()) RETURNING id`,
		author).Scan(&quizID); err != nil {
		t.Fatalf("insert quiz: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM questions WHERE quiz_id=$1`, quizID)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quizID)
	})

	var q1, q2 string
	pool.QueryRow(ctx,
		`INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
		 VALUES ($1,1,'multiple_choice','one','["A","B"]','"A"',60) RETURNING id`, quizID).Scan(&q1)
	pool.QueryRow(ctx,
		`INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
		 VALUES ($1,2,'multiple_choice','two','["A","B"]','"B"',60) RETURNING id`, quizID).Scan(&q2)

	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		 VALUES (gen_random_uuid(), 'Calib Replay', 'Calib', $1, 'student') RETURNING id`,
		"calibr+"+tag+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM question_responses WHERE attempt_id IN
		                 (SELECT id FROM quiz_attempts WHERE user_id=$1)`, userID)
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE user_id=$1`, userID)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	sess, err := svc.Create(ctx, "en", []string{"general"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, sess) })

	// Both correct, and slow enough that the sub-second guess penalty does not
	// apply — this is what the replay must preserve.
	if _, err := svc.Submit(ctx, sess, quizID, []Answer{
		{QuestionID: q1, Answer: json.RawMessage(`"A"`), ElapsedMs: 6000},
		{QuestionID: q2, Answer: json.RawMessage(`"B"`), ElapsedMs: 7000},
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if err := svc.Claim(ctx, sess, userID); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var status string
	var scorePct *float64
	var totalQuestions int
	if err := pool.QueryRow(ctx,
		`SELECT status, score_pct, total_questions FROM quiz_attempts
		  WHERE user_id=$1 AND quiz_id=$2`, userID, quizID).
		Scan(&status, &scorePct, &totalQuestions); err != nil {
		t.Fatalf("no attempt was materialised: %v", err)
	}
	if status != "completed" {
		t.Fatalf("attempt status = %q; want completed", status)
	}
	if scorePct == nil || *scorePct <= 0 {
		t.Fatalf("score_pct = %v; want a positive Qwish Score", scorePct)
	}
	if totalQuestions != 2 {
		t.Fatalf("total_questions = %d; want 2", totalQuestions)
	}

	// Both answers landed, and the recorded time is the one the client measured
	// — not the near-zero the DB clock would have produced during replay.
	var answered, fastAnswers int
	pool.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE time_taken_ms < 1000)
		   FROM question_responses qr
		   JOIN quiz_attempts qa ON qa.id = qr.attempt_id
		  WHERE qa.user_id=$1`, userID).Scan(&answered, &fastAnswers)
	if answered != 2 {
		t.Fatalf("recorded %d responses; want 2", answered)
	}
	if fastAnswers != 0 {
		t.Fatalf("%d responses recorded as sub-second; the elapsed time was lost in replay", fastAnswers)
	}
}
```

- [ ] **Step 6: Run it to verify it fails, then passes**

Run: `cd qwish-backend && TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/domain/onboardingsession/ -run TestClaimReplays -v`
Expected before Task 6 and the `claim.go` replay path exist: build failure or `no attempt was materialised`. With both in place: PASS.

- [ ] **Step 7: Accept the session id at create-profile**

In `internal/domain/auth/handler.go`, add above the `Handler` type:

```go
// OnboardingClaimer applies a pre-signup calibration session to a new account.
// An interface so auth does not import the onboarding session package.
type OnboardingClaimer interface {
	Claim(ctx context.Context, sessionID, userID string) error
}

// SetOnboardingClaimer wires the claim path. Optional — unset means signup
// ignores any session id it is handed.
func (h *Handler) SetOnboardingClaimer(c OnboardingClaimer) { h.onboardingClaimer = c }
```

Add the field `onboardingClaimer OnboardingClaimer` to `auth.Handler`.

In `CreateProfile`, add to the request struct:

```go
		OnboardingSession string `json:"onboarding_session"` // pre-signup calibration
```

And after the `CreateStudentEnrollment` block, before `instName` is computed:

```go
	// Apply the pre-signup calibration: language, topics, and the quiz played
	// before the account existed. Best-effort, like the enrollment above — a
	// stale or expired session must not cost the user their account.
	if req.OnboardingSession != "" && h.onboardingClaimer != nil {
		if err := h.onboardingClaimer.Claim(r.Context(), req.OnboardingSession, newUser.ID); err != nil {
			log.Printf("CreateProfile: onboarding claim for %s: %v", newUser.ID, err)
		}
	}
```

- [ ] **Step 8: Wire both in main.go**

After `attemptSvc` and `obSessionSvc` both exist:

```go
	obSessionSvc.SetAttempts(attemptSvc)
	authH.SetOnboardingClaimer(obSessionSvc)
```

(Place these next to the existing `attemptSvc.SetNotifier(...)` line, keeping the same "wire optional collaborators after construction" grouping.)

- [ ] **Step 9: Build and verify the whole suite**

Run: `cd qwish-backend && go build ./... && TEST_DATABASE_URL="$DATABASE_URL" go test ./...`
Expected: build succeeds, all packages pass.

- [ ] **Step 10: Commit**

```bash
cd qwish-backend
git add internal/domain/onboardingsession/ internal/domain/auth/handler.go cmd/api/main.go
git commit -m "feat(auth): claim onboarding calibration session at profile creation"
```

---

### Task 8: Purge expired sessions

**Files:**
- Modify: `qwish-backend/internal/scheduler/scheduler.go`
- Modify: `qwish-backend/cmd/api/main.go`
- Modify: `qwish-backend/render.yaml:116-131`

**Interfaces:**
- Consumes: the table from Task 1.
- Produces: `func (s *Scheduler) PurgeOnboardingSessions(ctx context.Context) error`; route `POST /api/v1/internal/cron/purge-onboarding-sessions`.

- [ ] **Step 1: Add the job**

Append to `internal/scheduler/scheduler.go`, next to `AbandonStaleAttempts`:

```go
// PurgeOnboardingSessions deletes unclaimed pre-signup sessions past their
// expiry. Claimed ones are kept: they are the only record of where a user's
// first attempt came from.
func (s *Scheduler) PurgeOnboardingSessions(ctx context.Context) error {
	ct, err := s.db.Exec(ctx,
		`DELETE FROM onboarding_sessions WHERE claimed_by IS NULL AND expires_at < now()`)
	if err != nil {
		return err
	}
	log.Printf("[scheduler] purged %d expired onboarding sessions", ct.RowsAffected())
	return nil
}
```

- [ ] **Step 2: Add the cron endpoint**

In `cmd/api/main.go`, inside `r.Route("/internal/cron", ...)`, after the `abandon-stale-attempts` route:

```go
				r.Post("/purge-onboarding-sessions", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.PurgeOnboardingSessions(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
```

- [ ] **Step 3: Schedule it nightly**

In `render.yaml`, in the `qwish-cron-nightly` `dockerCommand`, add a line after `post recompute-question-difficulty;`:

```
      post purge-onboarding-sessions;
```

- [ ] **Step 4: Verify it runs**

Run:
```bash
cd qwish-backend && go build -o bin/api ./cmd/api && ./bin/api &
sleep 3
curl -fsS -X POST -H "X-Cron-Secret: $CRON_SECRET" \
  localhost:8080/api/v1/internal/cron/purge-onboarding-sessions
```
Expected: `{"data":{"message":"done"}}` and a `[scheduler] purged N expired onboarding sessions` line in the server log. Stop the server.

- [ ] **Step 5: Commit**

```bash
cd qwish-backend
git add internal/scheduler/scheduler.go cmd/api/main.go render.yaml
git commit -m "feat(scheduler): purge expired onboarding sessions nightly"
```

---

### Task 9: Flutter — locale preference and l10n scaffolding

**Files:**
- Create: `numpie/lib/core/l10n/supported_locales.dart`
- Create: `numpie/lib/core/l10n/locale_prefs.dart`
- Create: `numpie/l10n.yaml`
- Create: `numpie/lib/l10n/app_en.arb`
- Create: `numpie/test/locale_prefs_test.dart`
- Modify: `numpie/pubspec.yaml`
- Modify: `numpie/lib/core/di/service_locator.dart`
- Modify: `numpie/lib/main.dart:158-175`

**Interfaces:**
- Consumes: `SharedPreferences` from the existing service locator.
- Produces:
  - `const List<LanguageOption> kSupportedLanguages` with fields `code`, `label`, `available`
  - `class LocalePrefs { LocalePrefs({required SharedPreferences prefs}); String get code; Locale get locale; Future<void> setCode(String code); }`
  - `getIt<LocalePrefs>()`

- [ ] **Step 1: Write the failing test**

`numpie/test/locale_prefs_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:numpie/core/l10n/locale_prefs.dart';
import 'package:numpie/core/l10n/supported_locales.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('defaults to English when nothing is stored', () async {
    final prefs = await SharedPreferences.getInstance();
    final lp = LocalePrefs(prefs: prefs);
    expect(lp.code, 'en');
    expect(lp.locale.languageCode, 'en');
  });

  test('persists a stored choice', () async {
    final prefs = await SharedPreferences.getInstance();
    await LocalePrefs(prefs: prefs).setCode('hi');

    // A fresh instance reads what the previous one wrote.
    expect(LocalePrefs(prefs: prefs).code, 'hi');
  });

  test('ignores a code that is not offered', () async {
    final prefs = await SharedPreferences.getInstance();
    final lp = LocalePrefs(prefs: prefs);
    await lp.setCode('klingon');
    expect(lp.code, 'en');
  });

  test('exactly one language renders today', () {
    final available = kSupportedLanguages.where((l) => l.available).toList();
    expect(available.length, 1);
    expect(available.single.code, 'en');
  });
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd numpie && flutter test test/locale_prefs_test.dart`
Expected: FAIL — `Target of URI doesn't exist: 'package:numpie/core/l10n/locale_prefs.dart'`.

- [ ] **Step 3: Write the two files**

`numpie/lib/core/l10n/supported_locales.dart`:

```dart
/// The languages the onboarding picker offers.
///
/// [available] is whether the app actually renders in that language today.
/// Hindi and Marathi are stored preferences with no ARB file yet, so the picker
/// must label them as coming soon rather than letting them look broken.
class LanguageOption {
  final String code;
  final String label;
  final bool available;

  const LanguageOption(this.code, this.label, {this.available = false});
}

const List<LanguageOption> kSupportedLanguages = [
  LanguageOption('en', 'English', available: true),
  LanguageOption('hi', 'हिंदी'),
  LanguageOption('mr', 'मराठी'),
];

const String kDefaultLanguageCode = 'en';
```

`numpie/lib/core/l10n/locale_prefs.dart`:

```dart
import 'package:flutter/widgets.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'supported_locales.dart';

/// The user's language choice, persisted across launches.
class LocalePrefs {
  static const _key = 'preferred_language';

  final SharedPreferences _prefs;

  LocalePrefs({required SharedPreferences prefs}) : _prefs = prefs;

  String get code {
    final stored = _prefs.getString(_key);
    final known = kSupportedLanguages.any((l) => l.code == stored);
    return known ? stored! : kDefaultLanguageCode;
  }

  /// The locale to render in. A stored-but-not-yet-translated choice still
  /// renders English — the strings do not exist.
  Locale get locale {
    final chosen = kSupportedLanguages.firstWhere(
      (l) => l.code == code,
      orElse: () => kSupportedLanguages.first,
    );
    return Locale(chosen.available ? chosen.code : kDefaultLanguageCode);
  }

  Future<void> setCode(String value) async {
    if (!kSupportedLanguages.any((l) => l.code == value)) return;
    await _prefs.setString(_key, value);
  }
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd numpie && flutter test test/locale_prefs_test.dart`
Expected: PASS, 4 tests.

- [ ] **Step 5: Add the l10n scaffolding**

`numpie/pubspec.yaml` — under `dependencies:`, next to `intl`:

```yaml
  flutter_localizations:
    sdk: flutter
```

and under `flutter:`:

```yaml
  generate: true
```

`numpie/l10n.yaml`:

```yaml
arb-dir: lib/l10n
template-arb-file: app_en.arb
output-localization-file: app_localizations.dart
```

`numpie/lib/l10n/app_en.arb`:

```json
{
  "@@locale": "en",
  "personaliseTitle": "Let's make this yours",
  "@personaliseTitle": { "description": "Heading on the onboarding personalisation screen" },
  "personaliseCta": "Start calibrating your Qwish score",
  "@personaliseCta": { "description": "Primary button that begins the calibration flow" }
}
```

- [ ] **Step 6: Wire the app**

In `lib/core/di/service_locator.dart`, next to the other storage registrations:

```dart
  getIt.registerLazySingleton<LocalePrefs>(
    () => LocalePrefs(prefs: getIt<SharedPreferences>()),
  );
```

with `import '../l10n/locale_prefs.dart';` added at the top.

In `lib/main.dart`, on the `MaterialApp.router` (around line 158), add:

```dart
            locale: getIt<LocalePrefs>().locale,
            localizationsDelegates: AppLocalizations.localizationsDelegates,
            supportedLocales: AppLocalizations.supportedLocales,
```

with `import 'package:flutter_gen/gen_l10n/app_localizations.dart';` and the service locator import already present.

- [ ] **Step 7: Verify the app builds and analyzes clean**

Run: `cd numpie && flutter pub get && flutter analyze && flutter test`
Expected: `No issues found.` and the full suite passes.

- [ ] **Step 8: Commit**

```bash
cd numpie
git add pubspec.yaml pubspec.lock l10n.yaml lib/l10n/ lib/core/l10n/ lib/core/di/service_locator.dart lib/main.dart test/locale_prefs_test.dart
git commit -m "feat(l10n): persisted locale preference and localisation scaffolding"
```

---

### Task 10: Flutter — onboarding repository and session storage

**Files:**
- Create: `numpie/lib/features/onboarding/data/onboarding_repository.dart`
- Create: `numpie/lib/features/onboarding/data/onboarding_session_storage.dart`
- Create: `numpie/test/onboarding_session_storage_test.dart`
- Modify: `numpie/lib/core/di/service_locator.dart`

**Interfaces:**
- Consumes: the endpoints from Task 5; `ApiClient` from `core/network/api_client.dart`.
- Produces:
  - `class OnboardingSessionStorage { Future<void> save(String id); String? get id; Future<void> clear(); }`
  - `class CalibrationQuiz { final String id, title; final String? description, domain; final int questionCount; }`
  - `class CalibrationQuestion { final String id; final int position; final String type, prompt; final List<String> options; final int timeLimitSeconds; }`
  - `class CalibrationAnswer { final String questionId, answer; final int elapsedMs; }`
  - `class CalibrationReviewItem { final String questionId; final bool correct; final String correctAnswer; }`
  - `class CalibrationResult { final int totalCorrect, totalQuestions; final List<CalibrationReviewItem> review; }`
  - `class OnboardingRepository { Future<String> createSession({required String language, required List<String> topics}); Future<void> updatePrefs(String sessionId, {required String language, required List<String> topics}); Future<List<CalibrationQuiz>> recommendations(String sessionId); Future<List<CalibrationQuestion>> questions(String sessionId, String quizId); Future<CalibrationResult> submit(String sessionId, String quizId, List<CalibrationAnswer> answers); }`

- [ ] **Step 1: Write the failing storage test**

`numpie/test/onboarding_session_storage_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:numpie/features/onboarding/data/onboarding_session_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  test('starts empty', () async {
    final prefs = await SharedPreferences.getInstance();
    expect(OnboardingSessionStorage(prefs: prefs).id, isNull);
  });

  test('survives a restart', () async {
    final prefs = await SharedPreferences.getInstance();
    await OnboardingSessionStorage(prefs: prefs).save('abc-123');

    // A fresh instance stands in for the next app launch.
    expect(OnboardingSessionStorage(prefs: prefs).id, 'abc-123');
  });

  test('clears once claimed', () async {
    final prefs = await SharedPreferences.getInstance();
    final s = OnboardingSessionStorage(prefs: prefs);
    await s.save('abc-123');
    await s.clear();
    expect(s.id, isNull);
  });
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd numpie && flutter test test/onboarding_session_storage_test.dart`
Expected: FAIL — URI does not exist.

- [ ] **Step 3: Write the storage**

`numpie/lib/features/onboarding/data/onboarding_session_storage.dart`:

```dart
import 'package:shared_preferences/shared_preferences.dart';

/// The pre-signup calibration session id.
///
/// Persisted rather than held in memory so killing the app mid-flow does not
/// throw away a quiz the user already played. Cleared once signup claims it.
class OnboardingSessionStorage {
  static const _key = 'onboarding_session_id';

  final SharedPreferences _prefs;

  OnboardingSessionStorage({required SharedPreferences prefs}) : _prefs = prefs;

  String? get id => _prefs.getString(_key);

  Future<void> save(String value) => _prefs.setString(_key, value);

  Future<void> clear() => _prefs.remove(_key);
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd numpie && flutter test test/onboarding_session_storage_test.dart`
Expected: PASS, 3 tests.

- [ ] **Step 5: Write the repository**

`numpie/lib/features/onboarding/data/onboarding_repository.dart`:

```dart
import 'package:dio/dio.dart';

import '../../../core/network/api_client.dart';
import '../../../core/network/api_exception.dart';

class CalibrationQuiz {
  final String id;
  final String title;
  final String? description;
  final String? domain;
  final int questionCount;

  const CalibrationQuiz({
    required this.id,
    required this.title,
    this.description,
    this.domain,
    this.questionCount = 0,
  });

  factory CalibrationQuiz.fromJson(Map<String, dynamic> json) => CalibrationQuiz(
        id: json['id'] as String,
        title: json['title'] as String? ?? 'Quiz',
        description: json['description'] as String?,
        domain: json['domain'] as String?,
        questionCount: (json['question_count'] as num?)?.toInt() ?? 0,
      );
}

class CalibrationQuestion {
  final String id;
  final int position;
  final String type;
  final String prompt;
  final List<String> options;
  final int timeLimitSeconds;

  const CalibrationQuestion({
    required this.id,
    required this.position,
    required this.type,
    required this.prompt,
    required this.options,
    this.timeLimitSeconds = 60,
  });

  factory CalibrationQuestion.fromJson(Map<String, dynamic> json) => CalibrationQuestion(
        id: json['id'] as String,
        position: (json['position'] as num?)?.toInt() ?? 0,
        type: json['type'] as String? ?? 'multiple_choice',
        prompt: json['prompt'] as String? ?? '',
        options: (json['options'] as List<dynamic>? ?? const [])
            .map((e) => e.toString())
            .toList(),
        timeLimitSeconds: (json['time_limit_seconds'] as num?)?.toInt() ?? 60,
      );
}

/// One answer plus how long the user actually spent on it. The elapsed time is
/// replayed into a real attempt at signup, where the speed component of the
/// Qwish Score reads it.
class CalibrationAnswer {
  final String questionId;
  final String answer;
  final int elapsedMs;

  const CalibrationAnswer({
    required this.questionId,
    required this.answer,
    required this.elapsedMs,
  });

  Map<String, dynamic> toJson() => {
        'question_id': questionId,
        'answer': answer,
        'elapsed_ms': elapsedMs,
      };
}

class CalibrationReviewItem {
  final String questionId;
  final bool correct;
  final String correctAnswer;

  const CalibrationReviewItem({
    required this.questionId,
    required this.correct,
    required this.correctAnswer,
  });

  factory CalibrationReviewItem.fromJson(Map<String, dynamic> json) => CalibrationReviewItem(
        questionId: json['question_id'] as String,
        correct: json['correct'] as bool? ?? false,
        correctAnswer: json['correct_answer']?.toString() ?? '',
      );
}

/// The pre-signup result. It carries no score: the Qwish Score is the unlock
/// the user creates an account for.
class CalibrationResult {
  final int totalCorrect;
  final int totalQuestions;
  final List<CalibrationReviewItem> review;

  const CalibrationResult({
    required this.totalCorrect,
    required this.totalQuestions,
    this.review = const [],
  });

  factory CalibrationResult.fromJson(Map<String, dynamic> json) => CalibrationResult(
        totalCorrect: (json['total_correct'] as num?)?.toInt() ?? 0,
        totalQuestions: (json['total_questions'] as num?)?.toInt() ?? 0,
        review: (json['review'] as List<dynamic>? ?? const [])
            .map((e) => CalibrationReviewItem.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}

/// Hits the public /onboarding/session endpoints. No auth: the session id is
/// the only credential.
class OnboardingRepository {
  final ApiClient _apiClient;

  OnboardingRepository({required ApiClient apiClient}) : _apiClient = apiClient;

  Future<T> _exec<T>(Future<T> Function() fn) async {
    try {
      return await fn();
    } on DioException catch (e) {
      if (e.error is ApiException) throw e.error as ApiException;
      throw UnknownApiException(e.message ?? 'Unknown error');
    }
  }

  Future<String> createSession({
    required String language,
    required List<String> topics,
  }) =>
      _exec(() async {
        final res = await _apiClient.dio.post('/onboarding/session', data: {
          'language': language,
          'topics': topics,
        });
        return res.data['data']['session_id'] as String;
      });

  Future<void> updatePrefs(
    String sessionId, {
    required String language,
    required List<String> topics,
  }) =>
      _exec(() async {
        await _apiClient.dio.patch('/onboarding/session/$sessionId', data: {
          'language': language,
          'topics': topics,
        });
      });

  Future<List<CalibrationQuiz>> recommendations(String sessionId) => _exec(() async {
        final res = await _apiClient.dio
            .get('/onboarding/session/$sessionId/recommendations');
        final data = res.data['data'] as List<dynamic>? ?? const [];
        return data
            .map((e) => CalibrationQuiz.fromJson(e as Map<String, dynamic>))
            .toList();
      });

  Future<List<CalibrationQuestion>> questions(String sessionId, String quizId) =>
      _exec(() async {
        final res = await _apiClient.dio
            .get('/onboarding/session/$sessionId/quizzes/$quizId');
        final data = res.data['data'] as List<dynamic>? ?? const [];
        return data
            .map((e) => CalibrationQuestion.fromJson(e as Map<String, dynamic>))
            .toList()
          ..sort((a, b) => a.position.compareTo(b.position));
      });

  Future<CalibrationResult> submit(
    String sessionId,
    String quizId,
    List<CalibrationAnswer> answers,
  ) =>
      _exec(() async {
        final res = await _apiClient.dio.post(
          '/onboarding/session/$sessionId/submit',
          data: {
            'quiz_id': quizId,
            'answers': answers.map((a) => a.toJson()).toList(),
          },
        );
        return CalibrationResult.fromJson(res.data['data'] as Map<String, dynamic>);
      });
}
```

- [ ] **Step 6: Register the storage**

In `lib/core/di/service_locator.dart`, next to the `LocalePrefs` registration:

```dart
  getIt.registerLazySingleton<OnboardingSessionStorage>(
    () => OnboardingSessionStorage(prefs: getIt<SharedPreferences>()),
  );
```

with `import '../../features/onboarding/data/onboarding_session_storage.dart';`.

- [ ] **Step 7: Verify**

Run: `cd numpie && flutter analyze && flutter test`
Expected: `No issues found.` and all tests pass.

- [ ] **Step 8: Commit**

```bash
cd numpie
git add lib/features/onboarding/data/ lib/core/di/service_locator.dart test/onboarding_session_storage_test.dart
git commit -m "feat(onboarding): calibration session storage and API client"
```

---

### Task 11: Flutter — three slides, routes, personalisation screen

**Files:**
- Modify: `numpie/lib/carousel/carousel_data.dart`
- Modify: `numpie/lib/core/router/route_names.dart`
- Modify: `numpie/lib/core/router/app_router.dart:44-80`
- Modify: `numpie/lib/carousel/carousel_shell.dart`
- Create: `numpie/lib/features/onboarding/presentation/personalise_screen.dart`
- Modify: `numpie/test/carousel_test.dart`
- Create: `numpie/test/personalise_screen_test.dart`

**Interfaces:**
- Consumes: `OnboardingRepository`, `OnboardingSessionStorage` (Task 10), `LocalePrefs`, `kSupportedLanguages` (Task 9).
- Produces:
  - `RouteNames.personalise` / `personalisePath = '/personalise'`
  - `RouteNames.calibrate` / `calibratePath = '/calibrate'`
  - `RouteNames.calibratePlay` / `calibratePlayPath = '/calibrate/:id'`
  - `RouteNames.calibrateResult` / `calibrateResultPath = '/calibrate/result'`
  - `class PersonaliseScreen extends StatefulWidget`
  - `const List<TopicOption> kTopics` — the six domain slugs with labels
  - `({String language, List<String> topics}) calibrationPicks({required bool skipped, required String language, required Set<String> topics})`

- [ ] **Step 1: Check what the carousel test currently asserts**

Run: `cd numpie && grep -n "kSlides\|length" test/carousel_test.dart`
Expected: you now know which assertions hard-code six slides. Update them in Step 3.

- [ ] **Step 2: Write the failing slide-count test**

In `numpie/test/carousel_test.dart`, add:

```dart
  test('onboarding is three slides and ends on the personalisation hand-off', () {
    expect(kSlides.length, 3);
    // The last slide leads into personalisation, so it offers no skip.
    expect(kSlides.last.hasSkip, isFalse);
  });
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd numpie && flutter test test/carousel_test.dart`
Expected: FAIL — `Expected: <3> Actual: <6>`.

- [ ] **Step 4: Cut the carousel to three slides**

In `lib/carousel/carousel_data.dart`, replace the `kSlides` list with:

```dart
// ---------------------------------------------------------------------------
// All 3 slides
//
// Was six. The leaderboard and rewards slides were pitching features the user
// had not earned yet; the demo slide is now the personalisation hand-off, so
// the last slide carries no skip.
// ---------------------------------------------------------------------------

const List<CarouselSlide> kSlides = [
  // Slide 1 — The Hook
  CarouselSlide(
    title: 'Learn. Compete. Win.',
    subtitle: 'Your school. Your quizzes. Your leaderboard.',
    backgroundColor: kDarkBg,
    isDark: true,
  ),

  // Slide 2 — Quiz Types
  CarouselSlide(
    title: 'MCQs are boring.\nWe fixed that.',
    subtitle: 'Four ways to answer, so a quiz asks more of you than '
        'recognising the right row.',
    backgroundColor: kDarkBg,
    isDark: true,
  ),

  // Slide 3 — Streaks & Points
  CarouselSlide(
    title: 'One quiz a day\nkeeps the streak alive.',
    subtitle: 'Bonus points at 7, 15, and 30 days.',
    backgroundColor: kDarkBg,
    isDark: true,
    hasSkip: false,
  ),
];
```

Delete the now-unused slide widgets' entries wherever `carousel_shell.dart` maps slide index to a slide widget, keeping the three that remain (`slide_hook.dart`, `slide_quiz_types.dart`, `slide_streaks.dart`). Leave the unused slide widget files in place — nothing else imports them and deleting files is a separate decision.

- [ ] **Step 5: Point the final CTA at personalisation**

In `lib/carousel/carousel_shell.dart`, change the navigation target of the final slide's primary button and of the skip action from `RouteNames.demoPath` / `RouteNames.loginPath` to:

```dart
context.go(RouteNames.personalisePath);
```

- [ ] **Step 6: Add the routes**

In `lib/core/router/route_names.dart`, after the demo block:

```dart
  // Onboarding calibration (public, pre-login)
  static const String personalise = 'personalise';
  static const String personalisePath = '/personalise';
  static const String calibrate = 'calibrate';
  static const String calibratePath = '/calibrate';
  static const String calibratePlay = 'calibratePlay';
  static const String calibratePlayPath = '/calibrate/:id';
  static const String calibrateResult = 'calibrateResult';
  static const String calibrateResultPath = '/calibrate/result';
```

In `lib/core/router/app_router.dart`, in the global `redirect`, extend the public-route test so an anonymous user is not bounced back to `/onboarding`:

```dart
      final isCalibration = loc == RouteNames.personalisePath ||
          loc.startsWith(RouteNames.calibratePath);
```

and use it in both places `isDemo` appears:

```dart
      if (authState is AuthAuthenticated) {
        if (isAuthRoute || isSplash || isOnboarding || isCalibration) {
          return RouteNames.homePath;
        }
      } else {
        if (!isAuthRoute && !isOnboarding && !isDemo && !isCalibration) {
          return RouteNames.onboardingPath;
        }
      }
```

Register the route (the other three come in Task 12):

```dart
      // --- Onboarding calibration (public, pre-login) ---
      GoRoute(
        path: RouteNames.personalisePath,
        name: RouteNames.personalise,
        builder: (context, state) => const PersonaliseScreen(),
      ),
```

- [ ] **Step 7: Write the personalisation screen**

`numpie/lib/features/onboarding/presentation/personalise_screen.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../../core/di/service_locator.dart';
import '../../../core/l10n/locale_prefs.dart';
import '../../../core/l10n/supported_locales.dart';
import '../../../core/network/api_client.dart';
import '../../../core/network/api_exception.dart';
import '../../../core/router/route_names.dart';
import '../data/onboarding_repository.dart';
import '../data/onboarding_session_storage.dart';

/// The six domains from migration 020. Labels match the taxonomy's own.
class TopicOption {
  final String slug;
  final String label;

  const TopicOption(this.slug, this.label);
}

const List<TopicOption> kTopics = [
  TopicOption('aptitude', 'Aptitude'),
  TopicOption('quantitative', 'Quantitative'),
  TopicOption('logical', 'Logical'),
  TopicOption('verbal', 'Verbal'),
  TopicOption('computer_science', 'Computer Sci.'),
  TopicOption('general', 'General'),
];

/// The picks a session is opened with.
///
/// Skipping — or selecting nothing — means every topic, not no topics: an empty
/// pick would read to the server as "recommend me nothing". Kept a free
/// function so the defaults are testable without a widget or a network call.
({String language, List<String> topics}) calibrationPicks({
  required bool skipped,
  required String language,
  required Set<String> topics,
}) {
  final allTopics = kTopics.map((t) => t.slug).toList();
  if (skipped) {
    return (language: kDefaultLanguageCode, topics: allTopics);
  }
  return (
    language: language,
    topics: topics.isEmpty ? allTopics : topics.toList(),
  );
}

/// Step one of the pre-signup flow: language and topics, then a session.
///
/// Skipping is a first-class path — it applies the documented defaults rather
/// than blocking the user at the door.
class PersonaliseScreen extends StatefulWidget {
  const PersonaliseScreen({super.key});

  @override
  State<PersonaliseScreen> createState() => _PersonaliseScreenState();
}

class _PersonaliseScreenState extends State<PersonaliseScreen> {
  late final OnboardingRepository _repo =
      OnboardingRepository(apiClient: getIt<ApiClient>());

  late String _language = getIt<LocalePrefs>().code;
  final Set<String> _topics = {};
  bool _busy = false;

  Future<void> _continue({required bool skipped}) async {
    final picks = calibrationPicks(
      skipped: skipped,
      language: _language,
      topics: _topics,
    );
    final language = picks.language;
    final topics = picks.topics;

    setState(() => _busy = true);
    try {
      final id = await _repo.createSession(language: language, topics: topics);
      await getIt<OnboardingSessionStorage>().save(id);
      await getIt<LocalePrefs>().setCode(language);
      if (mounted) context.go(RouteNames.calibratePath);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
    } catch (_) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Could not start. Try again.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      appBar: AppBar(
        actions: [
          TextButton(
            onPressed: _busy ? null : () => _continue(skipped: true),
            child: const Text('Skip'),
          ),
        ],
      ),
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 24),
          child: ListView(
            children: [
              Text("Let's make this yours", style: theme.textTheme.headlineMedium),
              const SizedBox(height: 24),
              Text('Language', style: theme.textTheme.titleMedium),
              const SizedBox(height: 8),
              Wrap(
                spacing: 8,
                children: [
                  for (final lang in kSupportedLanguages)
                    ChoiceChip(
                      label: Text(lang.available ? lang.label : '${lang.label} (soon)'),
                      selected: _language == lang.code,
                      onSelected: (_) => setState(() => _language = lang.code),
                    ),
                ],
              ),
              const SizedBox(height: 24),
              Text('Topics', style: theme.textTheme.titleMedium),
              const SizedBox(height: 8),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: [
                  for (final topic in kTopics)
                    FilterChip(
                      label: Text(topic.label),
                      selected: _topics.contains(topic.slug),
                      onSelected: (on) => setState(() {
                        on ? _topics.add(topic.slug) : _topics.remove(topic.slug);
                      }),
                    ),
                ],
              ),
              const SizedBox(height: 32),
              FilledButton(
                onPressed: _busy ? null : () => _continue(skipped: false),
                child: _busy
                    ? const SizedBox(
                        height: 18, width: 18, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Text('Start calibrating your Qwish score'),
              ),
              const SizedBox(height: 24),
            ],
          ),
        ),
      ),
    );
  }
}
```

- [ ] **Step 8: Write the screen test**

`numpie/test/personalise_screen_test.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:numpie/features/onboarding/presentation/personalise_screen.dart';

void main() {
  testWidgets('offers the six domains and the exact CTA copy', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: PersonaliseScreen()));

    expect(kTopics.length, 6);
    for (final topic in kTopics) {
      expect(find.text(topic.label), findsOneWidget);
    }
    expect(find.text('Start calibrating your Qwish score'), findsOneWidget);
    expect(find.text('Skip'), findsOneWidget);
  });

  test('skipping yields English and every topic', () {
    final picks = calibrationPicks(skipped: true, language: 'hi', topics: {'verbal'});
    expect(picks.language, 'en');
    expect(picks.topics.length, kTopics.length);
  });

  test('selecting nothing is treated as every topic', () {
    final picks = calibrationPicks(skipped: false, language: 'hi', topics: {});
    expect(picks.language, 'hi');
    expect(picks.topics.length, kTopics.length);
  });

  test('a real selection is passed through untouched', () {
    final picks =
        calibrationPicks(skipped: false, language: 'en', topics: {'verbal', 'logical'});
    expect(picks.topics, containsAll(<String>['verbal', 'logical']));
    expect(picks.topics.length, 2);
  });

  testWidgets('topics are multi-select', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: PersonaliseScreen()));

    await tester.tap(find.text('Verbal'));
    await tester.pump();
    await tester.tap(find.text('Logical'));
    await tester.pump();

    final selected = tester
        .widgetList<FilterChip>(find.byType(FilterChip))
        .where((c) => c.selected)
        .length;
    expect(selected, 2);
  });
}
```

- [ ] **Step 9: Run the tests**

Run: `cd numpie && flutter test test/carousel_test.dart test/personalise_screen_test.dart`
Expected: PASS — 1 carousel test, 3 `calibrationPicks` tests, 2 widget tests. If the widget test throws on `getIt<ApiClient>()`, the screen is resolving dependencies during `build` — move any such lookup into the `_continue` handler, which is where this version already has it.

- [ ] **Step 10: Verify the whole suite**

Run: `cd numpie && flutter analyze && flutter test`
Expected: `No issues found.` and all tests pass.

- [ ] **Step 11: Commit**

```bash
cd numpie
git add lib/carousel/ lib/core/router/ lib/features/onboarding/presentation/personalise_screen.dart test/carousel_test.dart test/personalise_screen_test.dart
git commit -m "feat(onboarding): three-slide carousel into language and topic personalisation"
```

---

### Task 12: Flutter — calibration list, player, locked result, signup hand-off

**Files:**
- Create: `numpie/lib/features/onboarding/presentation/calibrate_list_screen.dart`
- Create: `numpie/lib/features/onboarding/presentation/calibrate_play_screen.dart`
- Create: `numpie/lib/features/onboarding/presentation/calibrate_result_screen.dart`
- Create: `numpie/test/calibrate_result_screen_test.dart`
- Modify: `numpie/lib/core/router/app_router.dart`
- Modify: `numpie/lib/features/auth/data/auth_repository.dart:27-30,121-135`
- Modify: `numpie/lib/features/auth/bloc/auth_bloc.dart`

**Interfaces:**
- Consumes: everything from Tasks 10-11; `create-profile` accepting `onboarding_session` from Task 7.
- Produces:
  - `class CalibrateListScreen extends StatefulWidget`
  - `class CalibratePlayScreen extends StatefulWidget { const CalibratePlayScreen({required String quizId, String title}); }`
  - `class CalibrateResultScreen extends StatelessWidget { const CalibrateResultScreen({required CalibrationResult result}); }`
  - `AuthRepository.createProfile({required String fullName, String? referralCode, String? onboardingSession})`

- [ ] **Step 1: Write the failing result-screen test**

`numpie/test/calibrate_result_screen_test.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:numpie/features/onboarding/data/onboarding_repository.dart';
import 'package:numpie/features/onboarding/presentation/calibrate_result_screen.dart';

void main() {
  const result = CalibrationResult(totalCorrect: 3, totalQuestions: 5);

  testWidgets('shows the raw result but never a score', (tester) async {
    await tester.pumpWidget(
      const MaterialApp(home: CalibrateResultScreen(result: result)),
    );

    expect(find.text('3 / 5'), findsOneWidget);
    // The Qwish Score is the signup unlock: no number may appear here.
    expect(find.textContaining('Qwish Score'), findsWidgets);
    expect(find.byIcon(Icons.lock_rounded), findsOneWidget);
    expect(find.text('Create your account to unlock'), findsOneWidget);
  });
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd numpie && flutter test test/calibrate_result_screen_test.dart`
Expected: FAIL — URI does not exist.

- [ ] **Step 3: Write the three screens**

`numpie/lib/features/onboarding/presentation/calibrate_result_screen.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../../core/router/route_names.dart';
import '../data/onboarding_repository.dart';

/// The pre-signup payoff. The raw result is shown in full; the Qwish Score
/// renders locked, because computing it needs an account to hang it on.
class CalibrateResultScreen extends StatelessWidget {
  final CalibrationResult result;

  const CalibrateResultScreen({super.key, required this.result});

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Scaffold(
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 24),
          child: ListView(
            children: [
              const SizedBox(height: 32),
              Text('Calibration complete', style: theme.textTheme.headlineMedium),
              const SizedBox(height: 24),
              Center(
                child: Text(
                  '${result.totalCorrect} / ${result.totalQuestions}',
                  style: theme.textTheme.displaySmall,
                ),
              ),
              const SizedBox(height: 32),
              Card(
                child: ListTile(
                  leading: const Icon(Icons.lock_rounded),
                  title: const Text('Your Qwish Score'),
                  subtitle: const Text(
                      'Accuracy, difficulty, consistency, speed, and activity — '
                      'saved to your profile when you sign up.'),
                ),
              ),
              const SizedBox(height: 24),
              FilledButton(
                onPressed: () => context.go(RouteNames.loginPath),
                child: const Text('Create your account to unlock'),
              ),
              TextButton(
                onPressed: () => context.go(RouteNames.calibratePath),
                child: const Text('Play another'),
              ),
              const SizedBox(height: 24),
              if (result.review.isNotEmpty) ...[
                Text('Review', style: theme.textTheme.titleMedium),
                const SizedBox(height: 8),
                for (var i = 0; i < result.review.length; i++)
                  ListTile(
                    leading: Icon(
                      result.review[i].correct
                          ? Icons.check_circle_rounded
                          : Icons.cancel_rounded,
                    ),
                    title: Text('Question ${i + 1}'),
                    subtitle: result.review[i].correct
                        ? null
                        : Text('Answer: ${result.review[i].correctAnswer}'),
                  ),
              ],
              const SizedBox(height: 24),
            ],
          ),
        ),
      ),
    );
  }
}
```

`numpie/lib/features/onboarding/presentation/calibrate_list_screen.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../../core/di/service_locator.dart';
import '../../../core/network/api_client.dart';
import '../../../core/network/api_exception.dart';
import '../../../core/router/route_names.dart';
import '../data/onboarding_repository.dart';
import '../data/onboarding_session_storage.dart';

/// Quizzes recommended for the topics the user just picked.
class CalibrateListScreen extends StatefulWidget {
  const CalibrateListScreen({super.key});

  @override
  State<CalibrateListScreen> createState() => _CalibrateListScreenState();
}

class _CalibrateListScreenState extends State<CalibrateListScreen> {
  late final OnboardingRepository _repo =
      OnboardingRepository(apiClient: getIt<ApiClient>());

  List<CalibrationQuiz>? _quizzes;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final sessionId = getIt<OnboardingSessionStorage>().id;
    if (sessionId == null) {
      // No session means the flow was resumed from a stale route.
      if (mounted) context.go(RouteNames.personalisePath);
      return;
    }
    setState(() => _error = null);
    try {
      final quizzes = await _repo.recommendations(sessionId);
      if (mounted) setState(() => _quizzes = quizzes);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } catch (_) {
      if (mounted) setState(() => _error = 'Could not load quizzes.');
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Pick a quiz'),
        actions: [
          TextButton(
            onPressed: () => context.go(RouteNames.loginPath),
            child: const Text('Skip'),
          ),
        ],
      ),
      body: SafeArea(child: _body(context)),
    );
  }

  Widget _body(BuildContext context) {
    if (_error != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(_error!),
            const SizedBox(height: 12),
            ElevatedButton(onPressed: _load, child: const Text('Try again')),
          ],
        ),
      );
    }
    final quizzes = _quizzes;
    if (quizzes == null) return const Center(child: CircularProgressIndicator());
    if (quizzes.isEmpty) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text('Nothing to calibrate on for those topics yet.'),
            const SizedBox(height: 12),
            TextButton(
              onPressed: () => context.go(RouteNames.personalisePath),
              child: const Text('Change topics'),
            ),
            TextButton(
              onPressed: () => context.go(RouteNames.loginPath),
              child: const Text('Create an account instead'),
            ),
          ],
        ),
      );
    }
    return ListView.builder(
      itemCount: quizzes.length,
      itemBuilder: (context, i) {
        final q = quizzes[i];
        return ListTile(
          title: Text(q.title),
          subtitle: Text('${q.questionCount} questions'),
          trailing: const Icon(Icons.chevron_right_rounded),
          onTap: () => context.pushNamed(
            RouteNames.calibratePlay,
            pathParameters: {'id': q.id},
            queryParameters: {'title': q.title},
          ),
        );
      },
    );
  }
}
```

`numpie/lib/features/onboarding/presentation/calibrate_play_screen.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../../core/di/service_locator.dart';
import '../../../core/network/api_client.dart';
import '../../../core/network/api_exception.dart';
import '../../../core/router/route_names.dart';
import '../data/onboarding_repository.dart';
import '../data/onboarding_session_storage.dart';
import 'calibrate_result_screen.dart';

/// The calibration player. Multiple-choice only — recommendations exclude
/// quizzes carrying any other question type.
///
/// Per-question elapsed time is measured here and sent with the answers: it is
/// replayed into a real attempt at signup, where the Qwish Score's speed
/// component reads it. Without it every replayed answer would look instant and
/// be scored as a guess.
class CalibratePlayScreen extends StatefulWidget {
  final String quizId;
  final String title;

  const CalibratePlayScreen({super.key, required this.quizId, this.title = 'Quiz'});

  @override
  State<CalibratePlayScreen> createState() => _CalibratePlayScreenState();
}

class _CalibratePlayScreenState extends State<CalibratePlayScreen> {
  late final OnboardingRepository _repo =
      OnboardingRepository(apiClient: getIt<ApiClient>());

  List<CalibrationQuestion>? _questions;
  String? _error;
  bool _busy = true;

  int _index = 0;
  String? _selected;
  final List<CalibrationAnswer> _answers = [];
  DateTime _questionShownAt = DateTime.now();

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final sessionId = getIt<OnboardingSessionStorage>().id;
    if (sessionId == null) {
      if (mounted) context.go(RouteNames.personalisePath);
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final questions = await _repo.questions(sessionId, widget.quizId);
      if (!mounted) return;
      if (questions.isEmpty) {
        setState(() {
          _error = 'This quiz has no questions yet.';
          _busy = false;
        });
        return;
      }
      setState(() {
        _questions = questions;
        _busy = false;
        _questionShownAt = DateTime.now();
      });
    } on ApiException catch (e) {
      if (mounted) setState(() { _error = e.message; _busy = false; });
    } catch (_) {
      if (mounted) setState(() { _error = 'Could not load the quiz.'; _busy = false; });
    }
  }

  Future<void> _next() async {
    final questions = _questions!;
    _answers.add(CalibrationAnswer(
      questionId: questions[_index].id,
      answer: _selected ?? '',
      elapsedMs: DateTime.now().difference(_questionShownAt).inMilliseconds,
    ));

    if (_index < questions.length - 1) {
      setState(() {
        _index++;
        _selected = null;
        _questionShownAt = DateTime.now();
      });
      return;
    }

    setState(() => _busy = true);
    final sessionId = getIt<OnboardingSessionStorage>().id!;
    try {
      final result = await _repo.submit(sessionId, widget.quizId, _answers);
      if (!mounted) return;
      Navigator.of(context).pushReplacement(MaterialPageRoute(
        builder: (_) => CalibrateResultScreen(result: result),
      ));
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
    } catch (_) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Something went wrong. Try again.')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.title),
        leading: IconButton(
          icon: const Icon(Icons.close_rounded),
          onPressed: () => context.go(RouteNames.calibratePath),
        ),
      ),
      body: SafeArea(child: _body(context)),
    );
  }

  Widget _body(BuildContext context) {
    if (_busy) return const Center(child: CircularProgressIndicator());
    if (_error != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(_error!),
            const SizedBox(height: 12),
            ElevatedButton(onPressed: _load, child: const Text('Try again')),
          ],
        ),
      );
    }

    final questions = _questions!;
    final q = questions[_index];
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 24),
      child: ListView(
        children: [
          const SizedBox(height: 16),
          Text('Question ${_index + 1} of ${questions.length}',
              style: Theme.of(context).textTheme.labelMedium),
          const SizedBox(height: 12),
          Text(q.prompt, style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 24),
          for (final option in q.options)
            RadioListTile<String>(
              value: option,
              groupValue: _selected,
              title: Text(option),
              onChanged: (v) => setState(() => _selected = v),
            ),
          const SizedBox(height: 24),
          FilledButton(
            onPressed: _selected == null ? null : _next,
            child: Text(_index == questions.length - 1 ? 'Finish' : 'Next'),
          ),
          const SizedBox(height: 24),
        ],
      ),
    );
  }
}
```

- [ ] **Step 4: Run the result test to verify it passes**

Run: `cd numpie && flutter test test/calibrate_result_screen_test.dart`
Expected: PASS.

- [ ] **Step 5: Register the remaining routes**

In `lib/core/router/app_router.dart`, after the `personalise` route:

```dart
      GoRoute(
        path: RouteNames.calibratePath,
        name: RouteNames.calibrate,
        builder: (context, state) => const CalibrateListScreen(),
      ),
      GoRoute(
        path: RouteNames.calibratePlayPath,
        name: RouteNames.calibratePlay,
        builder: (context, state) => CalibratePlayScreen(
          quizId: state.pathParameters['id']!,
          title: state.uri.queryParameters['title'] ?? 'Quiz',
        ),
      ),
```

with the two imports added at the top.

- [ ] **Step 6: Send the session id at signup**

In `lib/features/auth/data/auth_repository.dart`, change the abstract declaration and the implementation:

```dart
  /// Step 3 (new users only): create profile after OTP verification.
  ///
  /// [onboardingSession] carries the pre-signup calibration — language, topics,
  /// and the quiz played before the account existed.
  Future<User> createProfile({
    required String fullName,
    String? referralCode,
    String? onboardingSession,
  });
```

```dart
  Future<User> createProfile({
    required String fullName,
    String? referralCode,
    String? onboardingSession,
  }) =>
      _exec(() async {
        final res = await _apiClient.dio.post('/auth/create-profile', data: {
          'full_name': fullName,
          if (referralCode != null && referralCode.trim().isNotEmpty)
            'referral_code': referralCode.trim(),
          if (onboardingSession != null && onboardingSession.isNotEmpty)
            'onboarding_session': onboardingSession,
        });
        final data = res.data['data'] as Map<String, dynamic>;
        final user = User.fromJson(data['user'] as Map<String, dynamic>);
        await _userStorage.saveUser(user);
        return user;
      });
```

In `lib/features/auth/bloc/auth_bloc.dart`, wherever `createProfile` is called, pass the stored id and clear it once the call returns:

```dart
      final storage = getIt<OnboardingSessionStorage>();
      final user = await _authRepository.createProfile(
        fullName: event.fullName,
        referralCode: event.referralCode,
        onboardingSession: storage.id,
      );
      // Single-use server-side; holding it would only re-send a dead id.
      await storage.clear();
```

- [ ] **Step 7: Verify the whole client suite**

Run: `cd numpie && flutter analyze && flutter test`
Expected: `No issues found.` and every test passes.

- [ ] **Step 8: Walk the flow end to end**

With the backend running against a database that has at least one public, published, MCQ-only quiz:

Run: `cd numpie && flutter run`

Check, in order:
1. Three carousel slides, the third with no Skip.
2. Personalisation shows six topics and the exact CTA text.
3. The CTA lands on a list of quizzes in the picked topics.
4. Playing through ends on the result screen: `N / M` visible, score card locked.
5. Creating an account lands on Home.
6. Profile → Insights shows a Qwish Score, not zero.
7. `psql "$DATABASE_URL" -c "SELECT preferred_language, interest_domains FROM users ORDER BY created_at DESC LIMIT 1"` shows the picks.
8. `psql "$DATABASE_URL" -c "SELECT claimed_by, claimed_at FROM onboarding_sessions ORDER BY created_at DESC LIMIT 1"` shows the session claimed.

- [ ] **Step 9: Commit**

```bash
cd numpie
git add lib/features/onboarding/ lib/features/auth/ lib/core/router/app_router.dart test/calibrate_result_screen_test.dart
git commit -m "feat(onboarding): calibration quiz, locked result, and signup hand-off"
```
