package institution

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// school is two institutes, A and B, with an admin at each, a class at B and a
// student enrolled at both whose active institute is A.
type school struct {
	InstA, InstB, AdminA, AdminB, ClassB, Student string
}

func seedTwoInstitutes(t *testing.T, pool *pgxpool.Pool) school {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var s school
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	inst := func(label string, dest *string) {
		must(pool.QueryRow(ctx, `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
			VALUES ($1||$2, 'school', $1||$2||'@example.test', 'S'||$1||$2, 'T'||$1||$2, 'verified') RETURNING id`, label, tag).Scan(dest))
	}
	inst("a", &s.InstA)
	inst("b", &s.InstB)
	user := func(role, label string, instID *string, dest *string) {
		must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1||$2, $1||$2, $1||$2||'@example.test', $3, $4) RETURNING id`, label, tag, role, instID).Scan(dest))
	}
	user("institution_admin", "admina", &s.InstA, &s.AdminA)
	user("institution_admin", "adminb", &s.InstB, &s.AdminB)
	user("student", "student", &s.InstA, &s.Student)
	must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id, name, invite_code) VALUES ($1, 'B class', 'B'||$2) RETURNING id`, s.InstB, tag).Scan(&s.ClassB))
	_, err := pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at) VALUES ($1,$3,'student','active',now()-interval '1 day'), ($2,$3,'student','active',now())`, s.InstA, s.InstB, s.Student)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, s.ClassB, s.Student)
	must(err)
	_, err = pool.Exec(ctx, `UPDATE users SET institution_id=$1 WHERE id=$2`, s.InstA, s.Student)
	must(err)
	t.Cleanup(func() {
		insts := []string{s.InstA, s.InstB}
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id IN (SELECT id FROM groups WHERE institution_id = ANY($1))`, insts)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM audit_log WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []string{s.AdminA, s.AdminB, s.Student})
		pool.Exec(ctx, `DELETE FROM institutions WHERE id = ANY($1)`, insts)
	})
	return s
}

func withAuth(r *http.Request, userID, role, instID string) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.ContextKeyUserID, userID)
	ctx = context.WithValue(ctx, middleware.ContextKeyRole, role)
	ctx = context.WithValue(ctx, middleware.ContextKeyInstID, instID)
	return r.WithContext(ctx)
}

func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		rctx = chi.NewRouteContext()
	}
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
