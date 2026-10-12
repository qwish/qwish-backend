package parent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// A minor is blocked from social actions until a parent link is active; an
// adult or a learner with no date of birth (pre-age-gate) never is.
func TestRequireConsentGatesMinorUntilParentLinks(t *testing.T) {
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
	user := func(role, dob string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role,date_of_birth)
			VALUES(gen_random_uuid(),'X','X',$1,$2,NULLIF($3,'')::date) RETURNING id`,
			uuid.NewString()[:8]+"@c.test", role, dob).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	minor, adult, legacy, parent := user("student", "2015-01-01"), user("student", "1990-01-01"), user("student", ""), user("parent", "")
	defer pool.Exec(ctx, `DELETE FROM parent_student_links WHERE student_id=$1`, minor)

	gate := RequireConsent(pool)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	status := func(userID string) int {
		r := httptest.NewRequest("POST", "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), middleware.ContextKeyUserID, userID))
		w := httptest.NewRecorder()
		gate.ServeHTTP(w, r)
		return w.Code
	}

	if got := status(minor); got != http.StatusForbidden {
		t.Errorf("minor without parent: %d, want 403", got)
	}
	if got := status(adult); got != http.StatusOK {
		t.Errorf("adult: %d, want 200", got)
	}
	if got := status(legacy); got != http.StatusOK {
		t.Errorf("no date of birth: %d, want 200", got)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO parent_student_links(student_id,parent_id,invite_code,status)
		VALUES($1,$2,$3,'active')`, minor, parent, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if got := status(minor); got != http.StatusOK {
		t.Errorf("minor with active parent link: %d, want 200", got)
	}

	pool.Exec(ctx, `UPDATE parent_student_links SET status='revoked' WHERE student_id=$1`, minor)
	if got := status(minor); got != http.StatusForbidden {
		t.Errorf("minor after parent revoked: %d, want 403", got)
	}
}
