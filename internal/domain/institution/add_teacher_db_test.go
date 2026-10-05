package institution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// An institution admin adds a teacher directly: an active teacher account in
// that institution, ready for its first email + OTP sign-in, with no invite
// link to accept and no verification step.
func TestAddTeacherDirectly(t *testing.T) {
	pool := openTestDB(t)
	s := seedTwoInstitutes(t, pool)
	h := NewHandler(pool, nil, nil, "", "")
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	post := func(body string) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		h.AddTeacher(w, withAuth(httptest.NewRequest("POST", "/institution/teachers", strings.NewReader(body)), s.AdminA, "institution_admin", s.InstA))
		var env struct{ Data map[string]any }
		json.Unmarshal(w.Body.Bytes(), &env)
		return w.Code, env.Data
	}
	email := "Teach-" + tag + "@Example.test"
	code, out := post(`{"name":"Meera Iyer","email":"` + email + `"}`)
	if code != 201 {
		t.Fatalf("add: %d %v", code, out)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE lower(email)=$1`, strings.ToLower(email)) })

	var role, inst, status, name string
	if err := pool.QueryRow(ctx, `SELECT role, institution_id, status, display_name FROM users WHERE lower(email)=$1`, strings.ToLower(email)).
		Scan(&role, &inst, &status, &name); err != nil {
		t.Fatal(err)
	}
	if role != "teacher" || inst != s.InstA || status != "active" || name != "Meera Iyer" {
		t.Errorf("teacher = %s %s %s %s", role, inst, status, name)
	}
	if out["invite_sent"] != false {
		t.Errorf("invite_sent = %v with no email service", out["invite_sent"])
	}
	if code, _ := post(`{"name":"Again","email":"` + email + `"}`); code != 409 {
		t.Errorf("duplicate email: %d, want 409", code)
	}
	for _, bad := range []string{`{"email":"x-` + tag + `@example.test"}`, `{"name":"N","email":"nope"}`} {
		if code, _ := post(bad); code != 400 {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}
}
