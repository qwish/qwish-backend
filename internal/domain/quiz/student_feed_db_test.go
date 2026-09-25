package quiz

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only with TEST_DATABASE_URL pointed at a migrated scratch database;
// it writes rows and removes them afterwards.
func TestStudentFeedSortAndUnplayed(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	tag := fmt.Sprintf("feed%d", time.Now().UnixNano())
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	newUser := func(role, label string, interests []string) string {
		var id string
		must(label, pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, interest_domains)
			VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', $2, COALESCE($3, '{}'::text[])) RETURNING id`,
			label+tag, role, interests).Scan(&id))
		return id
	}
	teacher := newUser("teacher", "teacher", nil)
	student := newUser("student", "student", []string{"verbal"})
	others := []string{newUser("student", "o1", nil), newUser("student", "o2", nil), newUser("student", "o3", nil)}

	newQuiz := func(name, domain string) string {
		var id string
		must(name, pool.QueryRow(ctx, `
			INSERT INTO quizzes (created_by, title, type, visibility, status, domain, published_at)
			VALUES ($1, $2, 'knowledge_check', 'public', 'published', $3, now()) RETURNING id`,
			teacher, tag+" "+name, domain).Scan(&id))
		return id
	}
	popular := newQuiz("popular", "aptitude")
	matched := newQuiz("matched", "verbal")
	quiet := newQuiz("quiet", "logical")
	played := newQuiz("played", "verbal")

	complete := func(user, quiz string) {
		_, err := pool.Exec(ctx, `
			INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, started_at, completed_at)
			VALUES ($1, $2, 'completed', 50, now() - interval '5 minutes', now())`, quiz, user)
		must("attempt", err)
	}
	for _, o := range others {
		complete(o, popular)
	}
	complete(others[0], matched)
	complete(student, played)

	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN (SELECT id FROM quizzes WHERE title LIKE $1||'%')`, tag)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE title LIKE $1||'%'`, tag)
		pool.Exec(ctx, `DELETE FROM users WHERE full_name LIKE '%'||$1`, tag)
	})

	svc := NewService(pool)
	list := func(sort string, unplayed bool, page, limit int) []string {
		t.Helper()
		quizzes, _, err := svc.ListForStudentFilteredScope(ctx, "", "public", "", "", tag, "", "", nil, nil, student, sort, unplayed, page, limit)
		if err != nil {
			t.Fatalf("list %s: %v", sort, err)
		}
		ids := make([]string, len(quizzes))
		for i, q := range quizzes {
			ids[i] = q.ID
		}
		return ids
	}
	expect := func(name string, got, want []string) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("%s: got %v want %v", name, got, want)
		}
	}

	// Interest match first, then the most popular; the played quiz is gone.
	expect("recommended", list("recommended", true, 1, 20), []string{matched, popular, quiet})
	expect("popular", list("popular", true, 1, 20), []string{popular, matched, quiet})
	// Pages tile the same order with no overlap.
	expect("page 1", list("recommended", true, 1, 2), []string{matched, popular})
	expect("page 2", list("recommended", true, 2, 2), []string{quiet})
	// Without unplayed the completed quiz stays in the feed.
	if got := list("popular", false, 1, 20); len(got) != 4 {
		t.Fatalf("unplayed=false: got %d quizzes, want 4 (%v, played %s)", len(got), got, played)
	}
}
