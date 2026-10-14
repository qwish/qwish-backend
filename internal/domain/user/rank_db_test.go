package user

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/leaderboard"
)

// Pins GetRank to the leaderboard's eligibility rule: an active, undeleted
// student with at least five distinct completed quizzes. A unique domain keeps
// the domain rank/total confined to the rows this test creates.
func TestGetRankEligibility(t *testing.T) {
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

	tag := fmt.Sprintf("rank%d", time.Now().UnixNano())
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	newUser := func(role, label string) string {
		var id string
		must(label, pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, domain)
			VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', $2, $3) RETURNING id`,
			label+tag, role, tag).Scan(&id))
		return id
	}
	teacher := newUser("teacher", "teacher")
	quizzes := make([]string, 6)
	for i := range quizzes {
		must("quiz", pool.QueryRow(ctx, `
			INSERT INTO quizzes (created_by, title, type, visibility, status, published_at)
			VALUES ($1, $2, 'knowledge_check', 'public', 'published', now()) RETURNING id`,
			teacher, fmt.Sprintf("%s q%d", tag, i)).Scan(&quizzes[i]))
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN (SELECT id FROM quizzes WHERE title LIKE $1||'%')`, tag)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE title LIKE $1||'%'`, tag)
		pool.Exec(ctx, `DELETE FROM users WHERE full_name LIKE '%'||$1`, tag)
	})
	// complete finishes n distinct quizzes (twice each, which must not double
	// count) and then pins the rating the leaderboard ranks by.
	complete := func(user string, n int, score float64) {
		for _, q := range quizzes[:n] {
			for range 2 {
				_, err := pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, started_at, completed_at)
					VALUES ($1, $2, 'completed', 50, now() - interval '5 minutes', now())`, q, user)
				must("attempt", err)
			}
		}
		_, err := pool.Exec(ctx, `UPDATE leaderboard_scores SET qwish_score=$2 WHERE user_id=$1`, user, score)
		must("score", err)
	}
	top := newUser("student", "top")
	mid := newUser("student", "mid")
	locked := newUser("student", "locked")
	gone := newUser("student", "gone")
	complete(top, 5, 700)
	complete(mid, 6, 500)
	complete(locked, 4, 900) // highest score, but not yet eligible
	complete(gone, 5, 800)   // eligible score, but soft-deleted
	_, err = pool.Exec(ctx, `UPDATE users SET deleted_at=now() WHERE id=$1`, gone)
	must("delete", err)

	svc := NewService(pool)
	svc.SetStandings(leaderboard.NewHandler(pool))
	for _, tc := range []struct {
		name               string
		user               string
		unlocked           bool
		distinct, rank, of int
	}{
		{"top scorer", top, true, 5, 1, 2},
		{"second", mid, true, 6, 2, 2},
		{"four quizzes stays locked", locked, false, 4, 0, 0},
	} {
		got, err := svc.GetRank(ctx, tc.user, "")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.LeaderboardUnlocked != tc.unlocked || got.DistinctQuizzesCompleted != tc.distinct {
			t.Errorf("%s: unlocked=%v distinct=%d, want %v %d", tc.name, got.LeaderboardUnlocked, got.DistinctQuizzesCompleted, tc.unlocked, tc.distinct)
		}
		if !tc.unlocked {
			continue
		}
		if got.DomainRank == nil || got.DomainTotal == nil {
			t.Fatalf("%s: domain rank missing", tc.name)
		}
		if *got.DomainRank != tc.rank || *got.DomainTotal != tc.of {
			t.Errorf("%s: domain rank=%d/%d, want %d/%d", tc.name, *got.DomainRank, *got.DomainTotal, tc.rank, tc.of)
		}
	}
}
