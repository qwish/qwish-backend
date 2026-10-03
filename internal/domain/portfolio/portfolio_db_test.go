package portfolio_test

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

	"github.com/qwish/backend/internal/domain/portfolio"
	"github.com/qwish/backend/internal/domain/user"
	"github.com/qwish/backend/internal/middleware"
)

type env struct {
	router                                   http.Handler
	inst, otherInst                          string
	teacher, outsider, admin, foreignTeacher string
	student                                  string
	roles, insts                             map[string]string
}

func setup(t *testing.T) (*env, *pgxpool.Pool) {
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
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	e := &env{roles: map[string]string{}, insts: map[string]string{}}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		label string
		dest  *string
	}{{"pf", &e.inst}, {"pfo", &e.otherInst}} {
		must(pool.QueryRow(ctx, `INSERT INTO institutions (name,type,contact_email,student_referral_code,teacher_referral_code,status)
			VALUES ($1||$2,'college',$1||$2||'@example.test','S'||$1||$2,'T'||$1||$2,'verified') RETURNING id`, x.label, tag).Scan(x.dest))
	}
	newUser := func(role, label, in string, dest *string) {
		must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid,full_name,display_name,email,role,institution_id)
			VALUES (gen_random_uuid(),$1,$1,$2,$3,$4) RETURNING id`,
			label, label+"-"+tag+"@example.test", role, in).Scan(dest))
		e.roles[*dest], e.insts[*dest] = role, in
	}
	newUser("teacher", "mentor", e.inst, &e.teacher)
	newUser("teacher", "outsider", e.inst, &e.outsider)
	newUser("institution_admin", "admin", e.inst, &e.admin)
	newUser("teacher", "foreign", e.otherInst, &e.foreignTeacher)
	newUser("student", "asha", e.inst, &e.student)

	var mine, other, otherStudent string
	must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id,name,invite_code) VALUES ($1,'CSE-A','A'||$2) RETURNING id`, e.inst, tag).Scan(&mine))
	must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id,name,invite_code) VALUES ($1,'CSE-B','B'||$2) RETURNING id`, e.inst, tag).Scan(&other))
	newUser("student", "other", e.inst, &otherStudent)
	_, err = pool.Exec(ctx, `INSERT INTO group_teachers (group_id,user_id) VALUES ($1,$2),($3,$4)`, mine, e.teacher, other, e.outsider)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id,user_id) VALUES ($1,$2),($3,$4)`, mine, e.student, other, otherStudent)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO enrollments (institution_id,user_id,full_name,status,joined_at) VALUES ($1,$2,'asha','active',now())`, e.inst, e.student)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO user_skills (user_id,skill_name) VALUES ($1,'Go'),($1,'SQL')`, e.student)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO user_education (user_id,institution_name,degree,is_current) VALUES ($1,'Fixture College','B.Tech',true)`, e.student)
	must(err)

	t.Cleanup(func() {
		for _, in := range []string{e.inst, e.otherInst} {
			pool.Exec(ctx, `DELETE FROM user_notifications WHERE user_id IN (SELECT id FROM users WHERE institution_id=$1)`, in)
			pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, in)
			pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, in)
			pool.Exec(ctx, `DELETE FROM users WHERE institution_id=$1`, in)
			pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, in)
		}
	})

	ph := portfolio.NewHandler(pool, nil)
	sh := user.NewProfileEntryHandler(pool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			id := req.Header.Get("X-User")
			c := context.WithValue(req.Context(), middleware.ContextKeyUserID, id)
			c = context.WithValue(c, middleware.ContextKeyRole, e.roles[id])
			c = context.WithValue(c, middleware.ContextKeyInstID, e.insts[id])
			next.ServeHTTP(w, req.WithContext(c))
		})
	})
	r.Route("/t", ph.TeacherRoutes)
	r.Route("/i", ph.InstitutionRoutes)
	r.Route("/me", func(r chi.Router) {
		r.Post("/entries", sh.Create)
		r.Patch("/entries/{entryId}", sh.Update)
		r.Post("/entries/{entryId}/submit", sh.Submit)
		r.Get("/entries/{entryId}/reviews", sh.Reviews)
	})
	e.router = r
	return e, pool
}

func (e *env) call(t *testing.T, want int, who, method, path string, body any) json.RawMessage {
	t.Helper()
	raw := ""
	if body != nil {
		b, _ := json.Marshal(body)
		raw = string(b)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(raw))
	req.Header.Set("X-User", who)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Data
}

type profileView struct {
	Student struct {
		Phone        *string `json:"phone"`
		GuardianName *string `json:"guardian_name"`
	} `json:"student"`
	Enrollment *struct {
		RollNumber *string `json:"roll_number"`
	} `json:"enrollment"`
	Classes   []string `json:"classes"`
	Skills    []string `json:"skills"`
	Education []struct {
		Degree *string `json:"degree"`
	} `json:"education"`
	Portfolio []struct {
		EntryID        string         `json:"entry_id"`
		RevisionID     string         `json:"revision_id"`
		Revision       int            `json:"revision"`
		Content        map[string]any `json:"content"`
		ReviewState    string         `json:"review_state"`
		AwaitingReview bool           `json:"awaiting_review"`
		StudentEditing bool           `json:"student_editing"`
	} `json:"portfolio"`
}

func TestStaffProfileAndReviewLoop(t *testing.T) {
	e, _ := setup(t)
	profile := func(who, prefix string) profileView {
		t.Helper()
		var p profileView
		if err := json.Unmarshal(e.call(t, 200, who, "GET", prefix+"/students/"+e.student+"/profile", nil), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// A never-submitted draft is invisible to staff.
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(e.call(t, 201, e.student, "POST", "/me/entries", map[string]any{
		"kind": "experience", "subtype": "internship", "title": "Backend intern", "org": "Acme",
		"start_date": "2026-05-01", "ongoing": true,
		"details": map[string]string{"supervisor": "Ms Rao +91 98", "work_mode": "remote", "responsibilities": "APIs"},
	}), &created)
	p := profile(e.teacher, "/t")
	if len(p.Portfolio) != 0 {
		t.Fatal("draft visible to teacher")
	}
	// Learning details only: no personal fields or roll number (spec D17).
	if p.Student.Phone != nil || p.Student.GuardianName != nil || p.Enrollment == nil || p.Enrollment.RollNumber != nil ||
		strings.Join(p.Classes, ",") != "CSE-A" || strings.Join(p.Skills, ",") != "Go,SQL" || len(p.Education) != 1 {
		t.Fatalf("profile incomplete: %+v", p)
	}

	// Scope: teacher of another class, another institution → 404. Admin → 200.
	e.call(t, 404, e.outsider, "GET", "/t/students/"+e.student+"/profile", nil)
	e.call(t, 404, e.foreignTeacher, "GET", "/t/students/"+e.student+"/profile", nil)
	e.call(t, 404, e.foreignTeacher, "GET", "/i/students/"+e.student+"/profile", nil)
	profile(e.admin, "/i")

	// Submit → queue → revision content with restricted field stripped.
	e.call(t, 200, e.student, "POST", "/me/entries/"+created.ID+"/submit", nil)
	var queue []struct {
		RevisionID string `json:"revision_id"`
		Title      string `json:"title"`
	}
	json.Unmarshal(e.call(t, 200, e.teacher, "GET", "/t/portfolio-reviews", nil), &queue)
	if len(queue) != 1 || queue[0].Title != "Backend intern" {
		t.Fatalf("queue = %+v", queue)
	}
	var outsiderQueue []any
	json.Unmarshal(e.call(t, 200, e.outsider, "GET", "/t/portfolio-reviews", nil), &outsiderQueue)
	if len(outsiderQueue) != 0 {
		t.Fatal("outsider sees the submission")
	}
	rev := queue[0].RevisionID
	p = profile(e.teacher, "/t")
	if len(p.Portfolio) != 1 || !p.Portfolio[0].AwaitingReview {
		t.Fatalf("submitted entry: %+v", p.Portfolio)
	}
	if d := p.Portfolio[0].Content["details"].(map[string]any); d["supervisor"] != nil || d["work_mode"] != "remote" {
		t.Fatalf("details = %v", d)
	}

	// Decisions: validation, scope, then changes_requested.
	e.call(t, 400, e.teacher, "POST", "/t/portfolio-reviews/"+rev+"/decision", map[string]string{"decision": "changes_requested"})
	e.call(t, 404, e.outsider, "POST", "/t/portfolio-reviews/"+rev+"/decision", map[string]string{"decision": "reviewed"})
	e.call(t, 404, e.foreignTeacher, "POST", "/t/portfolio-reviews/"+rev+"/decision", map[string]string{"decision": "reviewed"})
	ask := map[string]string{"decision": "changes_requested", "comment": "Add the completion letter"}
	e.call(t, 200, e.teacher, "POST", "/t/portfolio-reviews/"+rev+"/decision", ask)
	e.call(t, 200, e.teacher, "POST", "/t/portfolio-reviews/"+rev+"/decision", ask) // identical retry
	e.call(t, 409, e.teacher, "POST", "/t/portfolio-reviews/"+rev+"/decision", map[string]string{"decision": "reviewed"})
	if !strings.Contains(string(e.call(t, 200, e.student, "GET", "/me/entries/"+created.ID+"/reviews", nil)), "completion letter") {
		t.Fatal("student cannot see the feedback")
	}

	// Student edits: staff keep seeing revision 1 (with its review), not the private draft.
	e.call(t, 200, e.student, "PATCH", "/me/entries/"+created.ID, map[string]any{"kind": "experience", "title": "Backend intern (edited)", "org": "Acme", "start_date": "2026-05-01"})
	p = profile(e.teacher, "/t")
	if p.Portfolio[0].Content["title"] != "Backend intern" || !p.Portfolio[0].StudentEditing || p.Portfolio[0].ReviewState != "changes_requested" {
		t.Fatalf("after edit: %+v", p.Portfolio[0])
	}
	e.call(t, 409, e.teacher, "POST", "/t/portfolio-reviews/"+rev+"/decision", map[string]string{"decision": "reviewed"})

	// Resubmit → revision 2 → reviewed. The old revision can no longer be decided.
	e.call(t, 200, e.student, "POST", "/me/entries/"+created.ID+"/submit", nil)
	json.Unmarshal(e.call(t, 200, e.teacher, "GET", "/t/portfolio-reviews", nil), &queue)
	if len(queue) != 1 || queue[0].RevisionID == rev {
		t.Fatalf("resubmission queue = %+v", queue)
	}
	e.call(t, 409, e.teacher, "POST", "/t/portfolio-reviews/"+rev+"/decision", map[string]string{"decision": "reviewed"})
	e.call(t, 200, e.teacher, "POST", "/t/portfolio-reviews/"+queue[0].RevisionID+"/decision", map[string]string{"decision": "reviewed", "comment": "Looks good"})
	p = profile(e.admin, "/i")
	if p.Portfolio[0].Revision != 2 || p.Portfolio[0].ReviewState != "reviewed" || p.Portfolio[0].Content["title"] != "Backend intern (edited)" {
		t.Fatalf("institution view after review: %+v", p.Portfolio[0])
	}
}
