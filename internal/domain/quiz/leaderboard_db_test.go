package quiz

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPlayAndWinLeaderboardRanking(t *testing.T) {
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

	tag := fmt.Sprintf("qlb%d", time.Now().UnixNano())
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	user := func(name, role string) string {
		var id string
		must(name, pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
			VALUES (gen_random_uuid(), $1||$2, $1, $1||$2||'@example.test', $3) RETURNING id`, name, tag, role).Scan(&id))
		return id
	}
	teacher := user("teacher", "teacher")
	quiz := func(kind string) string {
		var id string
		must("quiz", pool.QueryRow(ctx, `INSERT INTO quizzes (created_by, title, type, visibility, status, published_at)
			VALUES ($1, $2, $3, 'public', 'published', now()) RETURNING id`, teacher, tag, kind).Scan(&id))
		return id
	}
	pw, kc := quiz("play_and_win"), quiz("knowledge_check")
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN ($1, $2)`, pw, kc)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id IN ($1, $2)`, pw, kc)
		pool.Exec(ctx, `DELETE FROM users WHERE full_name LIKE '%'||$1`, tag)
	})
	attempt := func(userID, status string, score float64, secs int) {
		_, err := pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, total_correct, total_questions, started_at, completed_at)
			VALUES ($1, $2, $3::text, $4::numeric, ($4::numeric/10)::int, 10, now() - make_interval(secs => $5::int), CASE WHEN $3::text='completed' THEN now() END)`,
			pw, userID, status, score, secs)
		must("attempt", err)
	}
	fast, slow, tieA, tieB, stray := user("fast", "student"), user("slow", "student"), user("tieA", "student"), user("tieB", "student"), user("stray", "student")
	attempt(fast, "completed", 90, 60)
	attempt(slow, "completed", 90, 120) // same score, slower: ranks below fast
	attempt(tieA, "completed", 50, 100) // tieA and tieB share a rank
	attempt(tieB, "completed", 50, 100)
	attempt(tieB, "completed", 20, 30)    // a worse duplicate attempt is ignored
	attempt(stray, "in_progress", 100, 5) // unfinished attempts never rank
	attempt(teacher, "completed", 100, 5) // non-students never rank

	svc := NewService(pool)
	board, err := svc.Leaderboard(ctx, pw, tieB)
	if err != nil {
		t.Fatalf("leaderboard: %v", err)
	}
	if board.Total != 4 || len(board.Entries) != 4 {
		t.Fatalf("total=%d entries=%d, want 4/4", board.Total, len(board.Entries))
	}
	want := []struct {
		user string
		rank int
	}{{fast, 1}, {slow, 2}, {tieA, 3}, {tieB, 3}}
	for i, w := range want {
		got := board.Entries[i]
		if got.Rank != w.rank || (i < 2 && got.UserID != w.user) {
			t.Errorf("entry %d = %s rank %d, want %s rank %d", i, got.UserID, got.Rank, w.user, w.rank)
		}
	}
	if board.Me == nil || board.Me.UserID != tieB || board.Me.ScorePct != 50 || board.Me.Rank != 3 {
		t.Fatalf("me = %+v, want tieB at rank 3 with 50%%", board.Me)
	}
	if _, err := svc.Leaderboard(ctx, kc, tieB); !errors.Is(err, ErrNotPlayAndWin) {
		t.Fatalf("knowledge check: err=%v, want ErrNotPlayAndWin", err)
	}
}
