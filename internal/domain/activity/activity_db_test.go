package activity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qwish/backend/internal/middleware"
)

// Point TEST_DATABASE_URL at a scratch database — these tests write rows.
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

// fixture: Teacher teaches Class (Student). Other is in OtherClass, which
// Teacher does not teach. Loner teaches nothing. Admin is the institution admin.
type fixture struct {
	Inst, Teacher, Loner, Admin, Student, Other, Class, OtherClass string
}

func seed(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var f fixture
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO institutions (name,type,contact_email,student_referral_code,teacher_referral_code,status)
		VALUES ('Act '||$1,'college','act-'||$1||'@example.test','S'||$1,'T'||$1,'verified') RETURNING id`, tag).Scan(&f.Inst))
	user := func(role, label string, dest *string) {
		must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid,full_name,display_name,email,role,institution_id)
			VALUES (gen_random_uuid(),$1,$1,$2,$3,$4) RETURNING id`, label, label+"-"+tag+"@example.test", role, f.Inst).Scan(dest))
		if role == "student" {
			_, err := pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at) VALUES ($1,$2,$3,'active',now())`, f.Inst, *dest, label)
			must(err)
		}
	}
	user("teacher", "teacher", &f.Teacher)
	user("teacher", "loner", &f.Loner)
	user("institution_admin", "admin", &f.Admin)
	user("student", "=student", &f.Student)
	user("student", "other", &f.Other)
	group := func(name string, dest *string) {
		must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id,name,invite_code) VALUES ($1,$2,$3) RETURNING id`,
			f.Inst, name, name+tag).Scan(dest))
	}
	group("CSE-A", &f.Class)
	group("CSE-B", &f.OtherClass)
	_, err := pool.Exec(ctx, `INSERT INTO group_teachers (group_id,user_id) VALUES ($1,$2)`, f.Class, f.Teacher)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id,user_id) VALUES ($1,$2),($3,$4)`, f.Class, f.Student, f.OtherClass, f.Other)
	must(err)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM activities WHERE institution_id=$1`, f.Inst)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, f.Inst)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, f.Inst)
		pool.Exec(ctx, `DELETE FROM users WHERE institution_id=$1`, f.Inst)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, f.Inst)
	})
	return f
}

type client struct {
	t      *testing.T
	router http.Handler
	f      fixture
}

func newClient(t *testing.T, pool *pgxpool.Pool, f fixture) *client {
	h := NewHandler(pool, nil)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, req.Header.Get("X-User"))
			ctx = context.WithValue(ctx, middleware.ContextKeyRole, req.Header.Get("X-Role"))
			ctx = context.WithValue(ctx, middleware.ContextKeyInstID, f.Inst)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Route("/o", h.OrganiserRoutes)
	r.Route("/s", h.StudentRoutes)
	return &client{t: t, router: r, f: f}
}

func (c *client) do(user, method, path string, body any) (int, map[string]any, string) {
	c.t.Helper()
	role := "teacher"
	switch user {
	case c.f.Student, c.f.Other:
		role = "student"
	case c.f.Admin:
		role = "institution_admin"
	}
	var rdr *strings.Reader
	switch b := body.(type) {
	case nil:
		rdr = strings.NewReader("")
	case string:
		rdr = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rdr = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("X-User", user)
	req.Header.Set("X-Role", role)
	rec := httptest.NewRecorder()
	c.router.ServeHTTP(rec, req)
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	var obj map[string]any
	_ = json.Unmarshal(env.Data, &obj)
	return rec.Code, obj, rec.Body.String()
}

func (c *client) want(code int, user, method, path string, body any) map[string]any {
	c.t.Helper()
	got, obj, raw := c.do(user, method, path, body)
	if got != code {
		c.t.Fatalf("%s %s as %s: got %d want %d: %s", method, path, user, got, code, raw)
	}
	return obj
}

func pollBody(groups []string) map[string]any {
	return map[string]any{"kind": "poll", "title": "Workshop topic", "group_ids": groups,
		"questions": []map[string]any{{"id": "q", "type": "single_choice", "label": "Pick", "required": true,
			"options": []map[string]string{{"id": "a", "label": "A"}, {"id": "b", "label": "B"}}}}}
}

func TestPollLifecycleAndScope(t *testing.T) {
	pool := openTestDB(t)
	f := seed(t, pool)
	c := newClient(t, pool, f)
	ctx := context.Background()

	// Authority: no class the teacher doesn't teach, no institution-wide reach.
	c.want(400, f.Teacher, "POST", "/o/activities", pollBody([]string{f.OtherClass}))
	wide := pollBody(nil)
	wide["institution_wide"] = true
	c.want(400, f.Teacher, "POST", "/o/activities", wide)

	id := c.want(201, f.Teacher, "POST", "/o/activities", pollBody([]string{f.Class}))["id"].(string)
	c.want(409, f.Teacher, "PUT", "/o/activities/"+id, func() map[string]any { b := pollBody([]string{f.Class}); b["revision"] = 7; return b }())
	// Drafts are invisible to students.
	c.want(404, f.Student, "GET", "/s/activities/"+id, nil)

	pub := c.want(200, f.Teacher, "POST", "/o/activities/"+id+"/publish", map[string]int{"revision": 1})
	if pub["reach_estimate"].(float64) != 1 {
		t.Fatalf("reach_estimate = %v, want 1", pub["reach_estimate"])
	}
	// A retried publish is not an error.
	c.want(200, f.Teacher, "POST", "/o/activities/"+id+"/publish", map[string]int{"revision": 1})

	// Frozen schema: questions are rejected, a title fix is audited.
	c.want(409, f.Teacher, "PUT", "/o/activities/"+id, func() map[string]any { b := pollBody([]string{f.Class}); b["revision"] = 2; return b }())
	c.want(200, f.Teacher, "PUT", "/o/activities/"+id, map[string]any{"revision": 2, "title": "Workshop topic (fixed)", "description": ""})
	var audits int
	pool.QueryRow(ctx, `SELECT count(*) FROM activity_audit_events WHERE activity_id=$1 AND action='descriptive_edit'`, id).Scan(&audits)
	if audits != 1 {
		t.Fatalf("descriptive_edit audits = %d", audits)
	}

	// Audience: only Student sees it, including by guessed URL.
	_, _, feed := c.do(f.Student, "GET", "/s/activities", nil)
	if !strings.Contains(feed, id) {
		t.Fatal("eligible student's feed is missing the poll")
	}
	_, _, feed = c.do(f.Other, "GET", "/s/activities", nil)
	if strings.Contains(feed, id) {
		t.Fatal("ineligible student's feed shows the poll")
	}
	c.want(404, f.Other, "GET", "/s/activities/"+id, nil)
	c.want(404, f.Other, "POST", "/s/activities/"+id+"/response", map[string]any{"answers": map[string]string{"q": "a"}})

	// Unknown option rejected; one vote; identical retry returns the same receipt; change refused.
	c.want(400, f.Student, "POST", "/s/activities/"+id+"/response", map[string]any{"answers": map[string]string{"q": "z"}})
	first := c.want(200, f.Student, "POST", "/s/activities/"+id+"/response", map[string]any{"answers": map[string]string{"q": "a"}})
	again := c.want(200, f.Student, "POST", "/s/activities/"+id+"/response", map[string]any{"answers": map[string]string{"q": "a"}})
	if first["id"] != again["id"] || first["submitted_at"] != again["submitted_at"] {
		t.Fatalf("retry produced a different receipt: %v vs %v", first, again)
	}
	c.want(409, f.Student, "POST", "/s/activities/"+id+"/response", map[string]any{"answers": map[string]string{"q": "b"}})
	c.want(400, f.Student, "PUT", "/s/activities/"+id+"/response/draft", map[string]any{"answers": map[string]string{"q": "a"}})

	// Results policy (default after_close): hidden from the student until closed.
	detail := c.want(200, f.Student, "GET", "/s/activities/"+id, nil)
	if detail["results"] != nil {
		t.Fatal("results shown before close")
	}
	org := c.want(200, f.Teacher, "GET", "/o/activities/"+id, nil)
	if org["results"].(map[string]any)["respondents"].(float64) != 1 {
		t.Fatalf("organiser results = %v", org["results"])
	}

	// Another organiser cannot see it; the institution admin can.
	c.want(404, f.Loner, "GET", "/o/activities/"+id, nil)
	c.want(404, f.Loner, "GET", "/o/activities/"+id+"/responses", nil)
	c.want(409, f.Loner, "POST", "/o/activities/"+id+"/close", nil)
	resp := c.want(200, f.Admin, "GET", "/o/activities/"+id+"/responses", nil)
	if n := len(resp["responses"].([]any)); n != 1 {
		t.Fatalf("admin sees %d responses", n)
	}

	c.want(200, f.Teacher, "POST", "/o/activities/"+id+"/close", nil)
	c.want(200, f.Teacher, "POST", "/o/activities/"+id+"/close", nil) // idempotent
	c.want(409, f.Student, "POST", "/s/activities/"+id+"/response", map[string]any{"answers": map[string]string{"q": "a"}})
	detail = c.want(200, f.Student, "GET", "/s/activities/"+id, nil)
	counts := detail["results"].(map[string]any)["questions"].([]any)[0].(map[string]any)["counts"].(map[string]any)
	if counts["a"].(float64) != 1 || counts["b"].(float64) != 0 {
		t.Fatalf("counts = %v", counts)
	}
	// Results expose aggregates only, never identities.
	if raw, _ := json.Marshal(detail["results"]); strings.Contains(string(raw), f.Student) {
		t.Fatal("results leak a student id")
	}
}

func TestFormDraftsConcurrencyExportAndAudienceExit(t *testing.T) {
	pool := openTestDB(t)
	f := seed(t, pool)
	c := newClient(t, pool, f)
	ctx := context.Background()

	body := map[string]any{"kind": "form", "title": "Proposal", "group_ids": []string{f.Class}, "allow_edit": true,
		"questions": []map[string]any{
			{"id": "title", "type": "short_text", "label": "=Title", "required": true},
			{"id": "ok", "type": "acknowledgement", "label": "Agree", "required": true},
		}}
	id := c.want(201, f.Teacher, "POST", "/o/activities", body)["id"].(string)
	c.want(200, f.Teacher, "POST", "/o/activities/"+id+"/publish", map[string]int{"revision": 1})
	path := "/s/activities/" + id + "/response"

	// A draft is partial, private and not counted.
	c.want(200, f.Student, "PUT", path+"/draft", map[string]any{"answers": map[string]any{"title": "half"}})
	c.want(400, f.Student, "POST", path, map[string]any{"answers": map[string]any{"title": "half"}})
	resp := c.want(200, f.Teacher, "GET", "/o/activities/"+id+"/responses", nil)
	if len(resp["responses"].([]any)) != 0 {
		t.Fatal("draft visible to organiser")
	}

	// Concurrent identical submits: one response, all succeed.
	answers := map[string]any{"answers": map[string]any{"title": "=HYPERLINK(\"x\")", "ok": true}}
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _, _ = c.do(f.Student, "POST", path, answers)
		}(i)
	}
	wg.Wait()
	for _, code := range codes {
		if code != 200 {
			t.Fatalf("concurrent submit codes: %v", codes)
		}
	}
	var rows int
	pool.QueryRow(ctx, `SELECT count(*) FROM activity_responses WHERE activity_id=$1`, id).Scan(&rows)
	if rows != 1 {
		t.Fatalf("%d response rows", rows)
	}

	// Editing is allowed here and keeps history.
	c.want(200, f.Student, "POST", path, map[string]any{"answers": map[string]any{"title": "v2", "ok": true}})
	var hist int
	pool.QueryRow(ctx, `SELECT count(*) FROM activity_response_history h JOIN activity_responses r ON r.id=h.response_id WHERE r.activity_id=$1`, id).Scan(&hist)
	if hist != 1 {
		t.Fatalf("history rows = %d", hist)
	}
	c.want(200, f.Student, "DELETE", path, nil)
	resp = c.want(200, f.Teacher, "GET", "/o/activities/"+id+"/responses", nil)
	if len(resp["responses"].([]any)) != 0 {
		t.Fatal("withdrawn response still listed")
	}
	c.want(200, f.Student, "POST", path, map[string]any{"answers": map[string]any{"title": "=HYPERLINK(\"x\")", "ok": true}})

	// Export escapes formulas in labels, names and answers.
	req := httptest.NewRequest("GET", "/o/activities/"+id+"/responses.csv", nil)
	req.Header.Set("X-User", f.Teacher)
	req.Header.Set("X-Role", "teacher")
	rec := httptest.NewRecorder()
	c.router.ServeHTTP(rec, req)
	csvOut := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(csvOut, `'=Title`) || !strings.Contains(csvOut, `'=student`) || !strings.Contains(csvOut, `'=HYPERLINK`) || !strings.Contains(csvOut, "CSE-A") {
		t.Fatalf("export: %d\n%s", rec.Code, csvOut)
	}
	c.want(404, f.Loner, "GET", "/o/activities/"+id+"/responses.csv", nil)

	// Leaving the audience: own receipt stays readable, new writes are refused.
	pool.Exec(ctx, `DELETE FROM group_students WHERE group_id=$1 AND user_id=$2`, f.Class, f.Student)
	detail := c.want(200, f.Student, "GET", "/s/activities/"+id, nil)
	if detail["activity"].(map[string]any)["eligible"] != false || detail["my_response"] == nil {
		t.Fatalf("receipt after leaving audience: %v", detail)
	}
	c.want(404, f.Student, "POST", path, answers)
	c.want(404, f.Student, "DELETE", path, nil)

	// Duplicate makes an editable draft with the published questions.
	dup := c.want(201, f.Teacher, "POST", "/o/activities/"+id+"/duplicate", nil)["id"].(string)
	got := c.want(200, f.Teacher, "GET", "/o/activities/"+dup, nil)["activity"].(map[string]any)
	if got["status"] != "draft" || !strings.Contains(fmt.Sprint(got["draft_questions"]), "=Title") {
		t.Fatalf("duplicate: %v", got)
	}
}

func TestDeadlineUsesServerTime(t *testing.T) {
	pool := openTestDB(t)
	f := seed(t, pool)
	c := newClient(t, pool, f)
	b := pollBody([]string{f.Class})
	b["closes_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	id := c.want(201, f.Teacher, "POST", "/o/activities", b)["id"].(string)
	c.want(200, f.Teacher, "POST", "/o/activities/"+id+"/publish", map[string]int{"revision": 1})
	// Deadline passes without anyone closing it.
	pool.Exec(context.Background(), `UPDATE activities SET closes_at=now()-interval '1 second', opens_at=NULL WHERE id=$1`, id)
	c.want(409, f.Student, "POST", "/s/activities/"+id+"/response", map[string]any{"answers": map[string]string{"q": "a"}})
	c.want(409, f.Teacher, "POST", "/o/activities/"+id+"/remind", nil)
}
