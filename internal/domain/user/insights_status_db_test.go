package user

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs only with TEST_DATABASE_URL pointed at a migrated scratch database.
func TestInsightsRatingStatus(t *testing.T) {
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

	var id string
	name := fmt.Sprintf("status%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', 'student') RETURNING id`, name).Scan(&id); err != nil {
		t.Fatalf("user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id) })

	svc := NewService(pool)
	check := func(daysAgo int, want string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO learner_ratings (user_id, theta, sigma, n, score, updated_at)
			VALUES ($1, 600, 60, 40, 480, now() - make_interval(days => $2))
			ON CONFLICT (user_id) DO UPDATE SET updated_at=EXCLUDED.updated_at`, id, daysAgo); err != nil {
			t.Fatalf("rating: %v", err)
		}
		bd, err := svc.GetInsightsBreakdown(ctx, id)
		if err != nil {
			t.Fatalf("breakdown: %v", err)
		}
		if bd.SkillRating == nil || bd.SkillRating.Status != want || bd.SkillRating.LastEvidenceAt == nil {
			t.Fatalf("%d days ago: got %+v, want status %q", daysAgo, bd.SkillRating, want)
		}
	}
	check(2, "active")
	check(45, "idle")
}
