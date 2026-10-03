package admin

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A student enrolled at two institutes is one account: super-admin lists and
// search show them once.
func TestTwoInstituteStudentListedOnce(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var a, b, student string
	for label, dest := range map[string]*string{"a": &a, "b": &b} {
		if err := pool.QueryRow(ctx, `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
			VALUES ($1||$2,'school',$1||$2||'@example.test','S'||$1||$2,'T'||$1||$2,'verified') RETURNING id`, label, tag).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(),'twice'||$1,'twice'||$1,'twice'||$1||'@example.test','student') RETURNING id`, tag).Scan(&student); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at) VALUES ($1,$3,'t','active',now()-interval '1 day'),($2,$3,'t','active',now())`, a, b, student)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM enrollments WHERE user_id=$1`, student)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, student)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id IN ($1,$2)`, a, b)
	})

	w := httptest.NewRecorder()
	NewHandler(pool, nil, nil).ListUsers(w, httptest.NewRequest("GET", "/?search=twice"+tag, nil))
	if n := strings.Count(w.Body.String(), student); w.Code != 200 || n != 1 {
		t.Fatalf("list users: code=%d occurrences=%d body=%s", w.Code, n, w.Body)
	}

	w = httptest.NewRecorder()
	(&StudentAdminHandler{db: pool}).Search(w, httptest.NewRequest("GET", "/?q=twice"+tag, nil))
	if n := strings.Count(w.Body.String(), student); w.Code != 200 || n != 1 {
		t.Fatalf("student search: code=%d occurrences=%d body=%s", w.Code, n, w.Body)
	}
}
