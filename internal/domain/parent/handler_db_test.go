package parent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// A claimed invite code cannot be re-claimed by a second parent, and only
// parents can claim.
func TestLinkClaimIsFirstParentOnly(t *testing.T) {
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
	tag := uuid.NewString()[:8]
	user := func(role string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role)
			VALUES(gen_random_uuid(),'X','X',$1,$2) RETURNING id`, tag+role+uuid.NewString()[:4]+"@p.test", role).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	student, parentA, parentB, otherStudent := user("student"), user("parent"), user("parent"), user("student")
	defer pool.Exec(ctx, `DELETE FROM parent_student_links WHERE student_id=$1`, student)

	h := NewHandler(pool)
	call := func(fn http.HandlerFunc, userID, role, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/", strings.NewReader(body))
		c := context.WithValue(r.Context(), middleware.ContextKeyUserID, userID)
		c = context.WithValue(c, middleware.ContextKeyRole, role)
		w := httptest.NewRecorder()
		fn(w, r.WithContext(c))
		return w
	}

	w := call(h.GenerateInvite, student, "student", "")
	var inv struct {
		Data struct {
			InviteCode string `json:"invite_code"`
		} `json:"data"`
	}
	json.NewDecoder(w.Body).Decode(&inv)
	code := inv.Data.InviteCode
	if w.Code != 200 || len(code) < 26 {
		t.Fatalf("invite: %d %q", w.Code, code)
	}
	body := `{"invite_code":"` + code + `"}`

	if w := call(h.Link, otherStudent, "student", body); w.Code != http.StatusForbidden {
		t.Fatalf("non-parent claim: %d", w.Code)
	}
	if w := call(h.Link, parentA, "parent", body); w.Code != 200 {
		t.Fatalf("first claim: %d %s", w.Code, w.Body)
	}
	if w := call(h.Link, parentB, "parent", body); w.Code != http.StatusNotFound {
		t.Fatalf("second claim: %d", w.Code)
	}
	var got string
	pool.QueryRow(ctx, `SELECT parent_id FROM parent_student_links WHERE invite_code=$1`, code).Scan(&got)
	if got != parentA {
		t.Fatalf("parent_id=%s, want first claimant %s", got, parentA)
	}
}
