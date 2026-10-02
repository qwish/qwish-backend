package leadership_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qwish/backend/internal/domain/activity"
	"github.com/qwish/backend/internal/domain/leadership"
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
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type world struct {
	pool                          *pgxpool.Pool
	router                        http.Handler
	inst, otherInst               string
	admin, hod, principal, dean   string
	t1, t2                        string // teach cse / ece classes
	s1, s2, s3, foreignStudent    string // cse, ece, no department, other institution
	cseClass, eceClass, bareClass string
	foreignDept                   string
	roles                         map[string]string // user id → users.role
	insts                         map[string]string // user id → institution
}

func build(t *testing.T) *world {
	t.Helper()
	pool := openTestDB(t)
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	w := &world{pool: pool, roles: map[string]string{}, insts: map[string]string{}}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	inst := func(label string, dest *string) {
		must(pool.QueryRow(ctx, `INSERT INTO institutions (name,type,contact_email,student_referral_code,teacher_referral_code,status)
			VALUES ($1||$2,'college',$1||$2||'@example.test','S'||$1||$2,'T'||$1||$2,'verified') RETURNING id`, label, tag).Scan(dest))
	}
	inst("lead", &w.inst)
	inst("other", &w.otherInst)
	user := func(role, label, in string, dest *string) {
		must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid,full_name,display_name,email,role,institution_id)
			VALUES (gen_random_uuid(),$1,$1,$2,$3,$4) RETURNING id`, label, label+"-"+tag+"@example.test", role, in).Scan(dest))
		w.roles[*dest], w.insts[*dest] = role, in
	}
	user("institution_admin", "admin", w.inst, &w.admin)
	user("teacher", "hod", w.inst, &w.hod)
	user("teacher", "principal", w.inst, &w.principal)
	user("teacher", "dean", w.inst, &w.dean)
	user("teacher", "t1", w.inst, &w.t1)
	user("teacher", "t2", w.inst, &w.t2)
	user("student", "s1", w.inst, &w.s1)
	user("student", "s2", w.inst, &w.s2)
	user("student", "s3", w.inst, &w.s3)
	user("student", "foreign", w.otherInst, &w.foreignStudent)
	group := func(name string, dest *string) {
		must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id,name,invite_code) VALUES ($1,$2,$2||$3) RETURNING id`, w.inst, name, tag).Scan(dest))
	}
	group("CSE-1", &w.cseClass)
	group("ECE-1", &w.eceClass)
	group("Open-1", &w.bareClass)
	_, err := pool.Exec(ctx, `INSERT INTO group_teachers (group_id,user_id) VALUES ($1,$2),($3,$4)`, w.cseClass, w.t1, w.eceClass, w.t2)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id,user_id) VALUES ($1,$2),($3,$4),($5,$6)`, w.cseClass, w.s1, w.eceClass, w.s2, w.bareClass, w.s3)
	must(err)
	must(pool.QueryRow(ctx, `INSERT INTO departments (institution_id,name) VALUES ($1,'Foreign') RETURNING id`, w.otherInst).Scan(&w.foreignDept))

	t.Cleanup(func() {
		for _, in := range []string{w.inst, w.otherInst} {
			pool.Exec(ctx, `DELETE FROM activities WHERE institution_id=$1`, in)
			pool.Exec(ctx, `DELETE FROM staff_role_assignments WHERE institution_id=$1`, in)
			pool.Exec(ctx, `DELETE FROM audit_log WHERE institution_id=$1`, in)
			pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, in)
			pool.Exec(ctx, `DELETE FROM departments WHERE institution_id=$1`, in)
			pool.Exec(ctx, `DELETE FROM users WHERE institution_id=$1`, in)
			pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, in)
		}
	})

	lh := leadership.NewHandler(pool)
	ah := activity.NewHandler(pool, nil)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			id := req.Header.Get("X-User")
			ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, id)
			ctx = context.WithValue(ctx, middleware.ContextKeyRole, w.roles[id])
			ctx = context.WithValue(ctx, middleware.ContextKeyInstID, w.insts[id])
			next.ServeHTTP(rw, req.WithContext(ctx))
		})
	})
	r.Route("/i", func(r chi.Router) { lh.AdminRoutes(r); ah.OrganiserRoutes(r) })
	r.Route("/l", lh.Routes)
	r.Route("/t", ah.OrganiserRoutes)
	w.router = r
	return w
}

func (w *world) call(t *testing.T, want int, user, method, path string, body any) json.RawMessage {
	t.Helper()
	raw := ""
	if body != nil {
		b, _ := json.Marshal(body)
		raw = string(b)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(raw))
	req.Header.Set("X-User", user)
	rec := httptest.NewRecorder()
	w.router.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return env.Data
}

func id(t *testing.T, raw json.RawMessage) string {
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &v) != nil || v.ID == "" {
		t.Fatalf("no id in %s", raw)
	}
	return v.ID
}

func names(t *testing.T, raw json.RawMessage, key string) string {
	t.Helper()
	if key != "" {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(raw, &m)
		raw = m[key]
	}
	var list []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	out := []string{}
	for _, v := range list {
		out = append(out, v.Name)
	}
	return strings.Join(out, ",")
}

func TestLeadershipScopes(t *testing.T) {
	w := build(t)
	ctx := context.Background()

	// Departments: unique per institution; classes link only to own-tenant departments.
	cse := id(t, w.call(t, 201, w.admin, "POST", "/i/departments", map[string]any{"name": "Computer Engineering", "code": "CSE"}))
	ece := id(t, w.call(t, 201, w.admin, "POST", "/i/departments", map[string]any{"name": "Electronics"}))
	w.call(t, 409, w.admin, "POST", "/i/departments", map[string]any{"name": " computer engineering "})
	w.call(t, 200, w.admin, "PUT", "/i/groups/"+w.cseClass+"/department", map[string]any{"department_id": cse})
	w.call(t, 200, w.admin, "PUT", "/i/groups/"+w.eceClass+"/department", map[string]any{"department_id": ece})
	w.call(t, 400, w.admin, "PUT", "/i/groups/"+w.bareClass+"/department", map[string]any{"department_id": w.foreignDept})

	// Assignment validation.
	grant := func(want int, user, role, dept string) json.RawMessage {
		t.Helper()
		return w.call(t, want, w.admin, "POST", "/i/staff-roles", map[string]any{"user_id": user, "role": role, "department_id": dept, "reason": "pilot"})
	}
	grant(400, w.hod, "hod", "")
	grant(400, w.principal, "principal", cse)
	grant(400, w.s1, "hod", cse)                  // students never hold roles
	grant(400, w.hod, "hod", w.foreignDept)       // other tenant's department
	grant(400, w.foreignStudent, "principal", "") // other tenant's user
	grant(400, w.hod, "registrar", "")
	hodAssignment := id(t, grant(201, w.hod, "hod", cse))
	grant(409, w.hod, "hod", cse)
	grant(201, w.principal, "principal", "")
	grant(201, w.dean, "dean", ece)

	// No assignment: no leadership access, though /access answers.
	w.call(t, 403, w.t2, "GET", "/l/students", nil)
	w.call(t, 200, w.t2, "GET", "/l/access", nil)

	// HOD: CSE only — guessed ids and filters for ECE are refused.
	if got := names(t, w.call(t, 200, w.hod, "GET", "/l/students", nil), "students"); got != "s1" {
		t.Fatalf("hod students = %q", got)
	}
	w.call(t, 404, w.hod, "GET", "/l/students?department_id="+ece, nil)
	w.call(t, 404, w.hod, "GET", "/l/departments/"+ece+"/summary", nil)
	sum := w.call(t, 200, w.hod, "GET", "/l/departments/"+cse+"/summary", nil)
	if got := names(t, sum, "classes"); got != "CSE-1" {
		t.Fatalf("hod summary classes = %q", got)
	}
	if got := names(t, w.call(t, 200, w.hod, "GET", "/l/departments", nil), ""); got != "Computer Engineering" {
		t.Fatalf("hod departments = %q", got)
	}
	if got := names(t, w.call(t, 200, w.hod, "GET", "/l/staff", nil), ""); got != "hod,t1" {
		t.Fatalf("hod staff = %q", got)
	}

	// Dean of ECE.
	if got := names(t, w.call(t, 200, w.dean, "GET", "/l/students", nil), "students"); got != "s2" {
		t.Fatalf("dean students = %q", got)
	}

	// Principal: the whole institution, including unassigned classes; never another tenant.
	if got := names(t, w.call(t, 200, w.principal, "GET", "/l/students", nil), "students"); got != "s1,s2,s3" {
		t.Fatalf("principal students = %q", got)
	}

	// Publishing: HOD → any CSE class (even one they don't teach), not ECE, not institution-wide.
	poll := func(groups []string, wide bool) map[string]any {
		return map[string]any{"kind": "poll", "title": "P", "group_ids": groups, "institution_wide": wide,
			"questions": []map[string]any{{"id": "q", "type": "single_choice", "label": "Q",
				"options": []map[string]string{{"id": "a", "label": "A"}, {"id": "b", "label": "B"}}}}}
	}
	w.call(t, 201, w.hod, "POST", "/t/activities", poll([]string{w.cseClass}, false))
	w.call(t, 400, w.hod, "POST", "/t/activities", poll([]string{w.eceClass}, false))
	w.call(t, 400, w.hod, "POST", "/t/activities", poll(nil, true))
	w.call(t, 201, w.principal, "POST", "/t/activities", poll(nil, true))
	w.call(t, 400, w.dean, "POST", "/t/activities", poll([]string{w.eceClass}, false)) // dean: no publish
	w.call(t, 400, w.t1, "POST", "/t/activities", poll([]string{w.eceClass}, false))

	// Archive refuses while classes or roles still point at the department.
	w.call(t, 409, w.admin, "DELETE", "/i/departments/"+cse, nil)

	// Revocation takes effect on the next request.
	w.call(t, 200, w.admin, "DELETE", "/i/staff-roles/"+hodAssignment, map[string]string{"reason": "term ended"})
	w.call(t, 200, w.admin, "DELETE", "/i/staff-roles/"+hodAssignment, map[string]string{"reason": "again"}) // idempotent
	w.call(t, 403, w.hod, "GET", "/l/students", nil)
	w.call(t, 400, w.hod, "POST", "/t/activities", poll([]string{w.cseClass}, false))

	// Expiry: an ended acting appointment stops working without anyone revoking it,
	// and the same role can be granted again afterwards.
	acting := id(t, grant(201, w.hod, "hod", cse))
	w.call(t, 200, w.hod, "GET", "/l/students", nil)
	w.pool.Exec(ctx, `UPDATE staff_role_assignments SET starts_at=now()-interval '2 days', ends_at=now()-interval '1 second' WHERE id=$1`, acting)
	w.call(t, 403, w.hod, "GET", "/l/students", nil)
	grant(201, w.hod, "hod", cse)

	// Scheduled assignments do not grant access early.
	w.call(t, 201, w.admin, "POST", "/i/staff-roles", map[string]any{"user_id": w.t2, "role": "vice_principal", "reason": "from next term",
		"starts_at": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)})
	w.call(t, 403, w.t2, "GET", "/l/students", nil)

	// Every grant and revoke is in the institution audit log.
	var grants, revokes int
	w.pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action_type='grant_staff_role'), count(*) FILTER (WHERE action_type='revoke_staff_role')
		FROM audit_log WHERE institution_id=$1`, w.inst).Scan(&grants, &revokes)
	if grants != 6 || revokes != 1 {
		t.Fatalf("audit grants=%d revokes=%d", grants, revokes)
	}
}
