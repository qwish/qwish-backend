package quiz

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Two workers rebuilding the same quiz must not deadlock. Worker B holds a
// pending job's row lock (mid-claim) and then waits for the quiz's advisory
// lock; worker A, holding that advisory lock, must not wait on B's row when it
// acknowledges the jobs its rebuild covered. Under load with several workers
// this surfaced as "deadlock detected (SQLSTATE 40P01)".
func TestRebuildQuizStatsSkipsJobsHeldByOtherWorkers(t *testing.T) {
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

	tag := fmt.Sprintf("stats%d", time.Now().UnixNano())
	var teacher, quizID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', 'teacher') RETURNING id`, tag).Scan(&teacher); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO quizzes (created_by, title, type, visibility, status, published_at)
		VALUES ($1, $2, 'knowledge_check', 'public', 'published', now()) RETURNING id`, teacher, tag).Scan(&quizID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM background_jobs WHERE dedupe_key LIKE $1||'%'`, tag)
		pool.Exec(ctx, `DELETE FROM quiz_read_stats WHERE quiz_id=$1`, quizID)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quizID)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, teacher)
	})
	enqueue := func(suffix string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO background_jobs (kind, dedupe_key, payload)
			VALUES ('quiz_stats', $1, jsonb_build_object('quiz_id', $2::text)) RETURNING id`, tag+suffix, quizID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	free := enqueue("a")
	held := enqueue("b")

	// Worker B: row-locks a pending job, as a claim in flight does.
	b, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Rollback(ctx)
	if _, err := b.Exec(ctx, `SELECT 1 FROM background_jobs WHERE id=$1 FOR UPDATE`, held); err != nil {
		t.Fatal(err)
	}

	// Worker A rebuilds while B holds that row.
	done := make(chan error, 1)
	go func() {
		a, err := pool.Begin(ctx)
		if err != nil {
			done <- err
			return
		}
		defer a.Rollback(ctx)
		if err := rebuildQuizStats(ctx, a, quizID); err != nil {
			done <- err
			return
		}
		done <- a.Commit(ctx)
	}()

	// B now wants the quiz's advisory lock, closing the cycle if A waits on B.
	time.Sleep(300 * time.Millisecond)
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := b.Exec(lockCtx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, quizID); err != nil {
		t.Fatalf("worker B: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker A: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker A did not finish")
	}
	if err := b.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	state := func(id string) string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT state FROM background_jobs WHERE id=$1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if got := state(free); got != "completed" {
		t.Errorf("job covered by A's rebuild: state %q, want completed", got)
	}
	if got := state(held); got != "pending" {
		t.Errorf("job held by B: state %q, want pending (B rebuilds it)", got)
	}
}
