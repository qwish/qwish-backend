package user

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// The student owns one name; enrollment copies follow it.
func TestUpdateMeSetsFullName(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var inst, id string
	pool.QueryRow(ctx, `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('fn'||$1,'school','fn'||$1||'@example.test','Sfn'||$1,'Tfn'||$1,'verified') RETURNING id`, tag).Scan(&inst)
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(),'Old Name','Old','fn'||$1||'@example.test','student') RETURNING id`, tag).Scan(&id); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at) VALUES ($1,$2,'Old Name','active',now())`, inst, id)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM enrollments WHERE user_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, inst)
	})
	h := NewHandler(NewService(pool))
	call := func(body string) int {
		r := httptest.NewRequest("PATCH", "/users/me", strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyUserID, id))
		w := httptest.NewRecorder()
		h.UpdateMe(w, r)
		return w.Code
	}
	if c := call(`{"full_name":"  Riya Sharma "}`); c != 200 {
		t.Fatalf("update: %d", c)
	}
	var name, enrolled string
	pool.QueryRow(ctx, `SELECT full_name FROM users WHERE id=$1`, id).Scan(&name)
	pool.QueryRow(ctx, `SELECT full_name FROM enrollments WHERE user_id=$1`, id).Scan(&enrolled)
	if name != "Riya Sharma" || enrolled != "Riya Sharma" {
		t.Fatalf("user=%q enrollment=%q", name, enrolled)
	}
	if c := call(`{"full_name":"   "}`); c != 400 {
		t.Fatalf("blank name must be rejected, got %d", c)
	}
}

// Older app builds still send personal fields; they are ignored, not a 400/500.
func TestUpdateMeIgnoresRetiredFields(t *testing.T) {
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
	var id string
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(),'Old','Old','rf'||$1||'@example.test','student') RETURNING id`, tag).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id) })
	r := httptest.NewRequest("PATCH", "/users/me", strings.NewReader(`{"display_name":"Riya","guardian_name":"X","date_of_birth":"2010-01-01"}`))
	r = r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyUserID, id))
	w := httptest.NewRecorder()
	NewHandler(NewService(pool)).UpdateMe(w, r)
	var name string
	pool.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1`, id).Scan(&name)
	if w.Code != 200 || name != "Riya" {
		t.Fatalf("code=%d display_name=%q body=%s", w.Code, name, w.Body)
	}
}
