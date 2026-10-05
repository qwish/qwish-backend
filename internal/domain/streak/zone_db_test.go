package streak

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/scoring"
)

func newStreakUser(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var id string
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(),'Learner','L',$1,'student') RETURNING id`,
		fmt.Sprintf("streak-%d@example.test", time.Now().UnixNano())).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM badges WHERE user_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM streaks WHERE user_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	})
	return pool, id
}

// playAt records a completion at the given instant and returns the streak.
func playAt(t *testing.T, pool *pgxpool.Pool, id string, when time.Time) int {
	t.Helper()
	ctx := context.Background()
	clock = func() time.Time { return when }
	t.Cleanup(func() { clock = time.Now })
	if _, err := NewService(pool).RecordCompletion(ctx, id, &scoring.Config{}); err != nil {
		t.Fatal(err)
	}
	var n, users int
	pool.QueryRow(ctx, `SELECT s.current_streak, u.current_streak FROM streaks s JOIN users u ON u.id=s.user_id WHERE s.user_id=$1`, id).Scan(&n, &users)
	if n != users {
		t.Fatalf("streaks=%d but users.current_streak=%d", n, users)
	}
	return n
}

// Every completion used to fail (row lock on an outer join), so streaks
// never moved. Consecutive days must count.
func TestRecordCompletionCountsConsecutiveDays(t *testing.T) {
	pool, id := newStreakUser(t)
	noon := func(d int) time.Time { return time.Date(2026, 10, d, 12, 0, 0, 0, time.UTC) }
	if n := playAt(t, pool, id, noon(1)); n != 1 {
		t.Fatalf("first day: streak %d, want 1", n)
	}
	if n := playAt(t, pool, id, noon(2)); n != 2 {
		t.Fatalf("next day: streak %d, want 2", n)
	}
	if n := playAt(t, pool, id, noon(2).Add(time.Hour)); n != 2 {
		t.Fatalf("same day again: streak %d, want 2", n)
	}
	if n := playAt(t, pool, id, noon(3)); n != 3 {
		t.Fatalf("third day: streak %d, want 3", n)
	}
}

// A learner with no institute plays at 23:30 IST, then again at 02:00 IST the
// next day. That is two days in India, so the streak must go up — it used to
// stay put because the day was cut at UTC midnight (05:30 IST).
func TestStreakDayIsIndiaTimeWithoutInstitute(t *testing.T) {
	pool, id := newStreakUser(t)
	ist := time.FixedZone("IST", 5*3600+1800)
	if n := playAt(t, pool, id, time.Date(2026, 10, 4, 23, 30, 0, 0, ist)); n != 1 {
		t.Fatalf("first quiz: streak %d, want 1", n)
	}
	if n := playAt(t, pool, id, time.Date(2026, 10, 5, 2, 0, 0, 0, ist)); n != 2 {
		t.Fatalf("quiz at 02:00 IST the next day: streak %d, want 2", n)
	}
	if n := playAt(t, pool, id, time.Date(2026, 10, 5, 21, 0, 0, 0, ist)); n != 2 {
		t.Fatalf("second quiz the same IST day: streak %d, want 2", n)
	}
}

func TestMilestoneCreditAndClaimRollBackTogether(t *testing.T) {
	pool, id := newStreakUser(t)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM points_ledger WHERE user_id=$1`, id) })
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO streaks(user_id,current_streak,longest_streak,last_completed_date) VALUES($1,6,6,(now() AT TIME ZONE 'Asia/Kolkata')::date-1)`, id); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bonus, err := NewService(pool).RecordCompletionTx(ctx, tx, id, &scoring.Config{StreakBonus7Day: 70, PointsExpiryMonths: 6}, "")
	if err != nil || bonus != 70 {
		t.Fatalf("%d %v", bonus, err)
	}
	_ = tx.Rollback(ctx)
	var claimed bool
	var points int64
	if err = pool.QueryRow(ctx, `SELECT s.milestone_7_claimed,u.total_points FROM streaks s JOIN users u ON u.id=s.user_id WHERE s.user_id=$1`, id).Scan(&claimed, &points); err != nil {
		t.Fatal(err)
	}
	if claimed || points != 0 {
		t.Fatalf("partial reward survived rollback: %v %d", claimed, points)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = NewService(pool).RecordCompletionTx(ctx, tx, id, &scoring.Config{StreakBonus7Day: 70, PointsExpiryMonths: 6}, ""); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var credits int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM points_ledger WHERE user_id=$1 AND reason='streak_bonus' AND amount=70`, id).Scan(&credits); err != nil || credits != 1 {
		t.Fatalf("%d %v", credits, err)
	}
}
