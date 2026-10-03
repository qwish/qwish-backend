package leaderboard

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A student enrolled at two institutes ranks on both institute leaderboards,
// not only the one their app is currently showing.
func TestInstituteLeaderboardIncludesNonActiveMembers(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var a, b, student string
	for label, dest := range map[string]*string{"a": &a, "b": &b} {
		if err := pool.QueryRow(ctx, `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
			VALUES ($1||$2,'school',$1||$2||'@example.test','S'||$1||$2,'T'||$1||$2,'verified') RETURNING id`, label, tag).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role, status)
		VALUES (gen_random_uuid(),'lb','lb','lb'||$1||'@example.test','student','active') RETURNING id`, tag).Scan(&student); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at) VALUES ($1,$3,'lb','active',now()-interval '1 day'),($2,$3,'lb','active',now())`, a, b, student)
	pool.Exec(ctx, `UPDATE users SET institution_id=$1 WHERE id=$2`, a, student)
	pool.Exec(ctx, `INSERT INTO leaderboard_scores (user_id, qwish_score, completed_quizzes) VALUES ($1, 120, 6)`, student)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM enrollments WHERE user_id=$1`, student)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, student)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id IN ($1,$2)`, a, b)
	})

	entries, total, err := NewHandler(pool).loadPage(ctx, "institution", b, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(entries) != 1 || entries[0].UserID != student {
		t.Fatalf("institute B leaderboard must include the member; total=%d entries=%+v", total, entries)
	}
}
