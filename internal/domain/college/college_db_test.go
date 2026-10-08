package college_test

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

	"github.com/qwish/backend/internal/domain/college"
	"github.com/qwish/backend/internal/domain/curriculum"
	"github.com/qwish/backend/internal/domain/institution"
	"github.com/qwish/backend/internal/domain/leadership"
	"github.com/qwish/backend/internal/middleware"
)

type world struct {
	t                  *testing.T
	pool               *pgxpool.Pool
	router             http.Handler
	admin, otherAdmin  string
	insts              map[string]string
	dept, foreignDept  string
	year, foreignClass string
}

func build(t *testing.T) *world {
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
	tag := fmt.Sprint(time.Now().UnixNano())
	w := &world{t: t, pool: pool, insts: map[string]string{}}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	setup := func(label string, admin *string) string {
		var inst string
		must(pool.QueryRow(ctx, `INSERT INTO institutions (name,type,contact_email,student_referral_code,teacher_referral_code,status)
			VALUES ($1||$2,'college',$1||$2||'@example.test','S'||$1||$2,'T'||$1||$2,'verified') RETURNING id`, label, tag).Scan(&inst))
		must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid,full_name,display_name,email,role,institution_id)
			VALUES (gen_random_uuid(),$1,$1,$2,'institution_admin',$3) RETURNING id`, label, label+"-"+tag+"@example.test", inst).Scan(admin))
		w.insts[*admin] = inst
		return inst
	}
	inst := setup("col", &w.admin)
	other := setup("colother", &w.otherAdmin)
	must(pool.QueryRow(ctx, `INSERT INTO departments (institution_id,name,code) VALUES ($1,'Computer Engineering','CE') RETURNING id`, inst).Scan(&w.dept))
	must(pool.QueryRow(ctx, `INSERT INTO departments (institution_id,name) VALUES ($1,'Foreign') RETURNING id`, other).Scan(&w.foreignDept))
	must(pool.QueryRow(ctx, `INSERT INTO academic_years (institution_id,name,starts_on,ends_on) VALUES ($1,'2025-26','2025-07-01','2026-06-30') RETURNING id`, inst).Scan(&w.year))
	must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id,name,invite_code) VALUES ($1,'Foreign class',$2) RETURNING id`, other, "F"+tag[len(tag)-7:]).Scan(&w.foreignClass))
	t.Cleanup(func() {
		for _, in := range []string{inst, other} {
			for _, q := range []string{
				`DELETE FROM offering_groups WHERE institution_id=$1`, `DELETE FROM course_offerings WHERE institution_id=$1`,
				`DELETE FROM audit_log WHERE institution_id=$1`, `DELETE FROM groups WHERE institution_id=$1`,
				`DELETE FROM academic_terms WHERE institution_id=$1`, `DELETE FROM academic_years WHERE institution_id=$1`,
				`DELETE FROM cohorts WHERE institution_id=$1`, `DELETE FROM programmes WHERE institution_id=$1`,
				`DELETE FROM departments WHERE institution_id=$1`, `DELETE FROM users WHERE institution_id=$1`,
				`DELETE FROM institutions WHERE id=$1`,
			} {
				pool.Exec(ctx, q, in)
			}
		}
	})

	ih := institution.NewHandler(pool, nil, nil, "", "")
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			id := req.Header.Get("X-User")
			ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, id)
			ctx = context.WithValue(ctx, middleware.ContextKeyRole, "institution_admin")
			ctx = context.WithValue(ctx, middleware.ContextKeyInstID, w.insts[id])
			next.ServeHTTP(rw, req.WithContext(ctx))
		})
	})
	college.NewHandler(pool).InstitutionRoutes(r)
	leadership.NewHandler(pool).AdminRoutes(r)
	curriculum.NewHandler(curriculum.NewService(pool)).InstitutionRoutes(r)
	r.Get("/groups", ih.ListGroups)
	r.Post("/groups", ih.CreateGroup)
	w.router = r
	return w
}

func (w *world) call(want int, user, method, path string, body any) json.RawMessage {
	w.t.Helper()
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
		w.t.Fatalf("%s %s: got %d want %d: %s", method, path, rec.Code, want, rec.Body.String())
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	return env.Data
}

func (w *world) id(want int, method, path string, body any) string {
	w.t.Helper()
	var out struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.call(want, w.admin, method, path, body), &out)
	return out.ID
}

func names(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var rows []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

// Acceptance (G11): CE → B.Tech → 2024 cohort → Semester III → Division A,
// found by filters rather than by encoding the hierarchy in a class name.
func TestCollegeHierarchyAndFilters(t *testing.T) {
	w := build(t)
	btech := w.id(201, "POST", "/programmes", map[string]any{"department_id": w.dept, "code": "BTCE", "name": "B.Tech Computer Engineering", "award": "B.Tech", "duration_terms": 8})
	mtech := w.id(201, "POST", "/programmes", map[string]any{"department_id": w.dept, "code": "MTCE", "name": "M.Tech Computer Engineering"})
	w.id(409, "POST", "/programmes", map[string]any{"department_id": w.dept, "code": "btce", "name": "Duplicate code"})
	c2024 := w.id(201, "POST", "/cohorts", map[string]any{"programme_id": btech, "admission_year": 2024, "completion_year": 2028})
	w.id(201, "POST", "/cohorts", map[string]any{"programme_id": mtech, "admission_year": 2024})
	w.id(409, "POST", "/cohorts", map[string]any{"programme_id": btech, "admission_year": 2024})
	odd := w.id(201, "POST", "/academic-terms", map[string]any{"academic_year_id": w.year, "name": "Odd semester", "sequence": 1, "starts_on": "2025-07-01", "ends_on": "2025-12-15"})
	w.id(201, "POST", "/academic-terms", map[string]any{"academic_year_id": w.year, "name": "Even semester", "sequence": 2, "starts_on": "2026-01-05", "ends_on": "2026-06-30"})

	divA := w.id(201, "POST", "/groups", map[string]any{"name": "SE Computer A", "grade": "Semester III", "section": "A"})
	w.id(201, "POST", "/groups", map[string]any{"name": "Unplaced class"})
	w.id(200, "PUT", "/groups/"+divA+"/academic-context", map[string]any{"cohort_id": c2024, "term_id": odd})

	for _, q := range []string{"programme_id=" + btech, "cohort_id=" + c2024, "term_id=" + odd, "programme_id=" + btech + "&term_id=" + odd} {
		if got := names(t, w.call(200, w.admin, "GET", "/groups?"+q, nil)); len(got) != 1 || got[0] != "SE Computer A" {
			t.Fatalf("filter %s: got %v", q, got)
		}
	}
	if got := names(t, w.call(200, w.admin, "GET", "/groups?programme_id="+mtech, nil)); len(got) != 0 {
		t.Fatalf("M.Tech must stay distinct from B.Tech: %v", got)
	}
	var groups []struct {
		Name     string  `json:"name"`
		TermName *string `json:"term_name"`
		Cohort   *struct {
			ProgrammeCode string `json:"programme_code"`
			AdmissionYear int    `json:"admission_year"`
		} `json:"cohort"`
	}
	_ = json.Unmarshal(w.call(200, w.admin, "GET", "/groups?cohort_id="+c2024, nil), &groups)
	if g := groups[0]; g.Cohort == nil || g.Cohort.ProgrammeCode != "BTCE" || g.Cohort.AdmissionYear != 2024 || g.TermName == nil || *g.TermName != "Odd semester" {
		t.Fatalf("academic context not returned: %+v", g)
	}
	w.call(400, w.admin, "GET", "/groups?term_id=nope", nil)

	// Lifecycle guards: nothing archives out from under an active class.
	w.call(409, w.admin, "DELETE", "/cohorts/"+c2024, nil)
	w.call(409, w.admin, "DELETE", "/programmes/"+btech, nil)
	w.call(409, w.admin, "DELETE", "/departments/"+w.dept, nil)
	w.call(409, w.admin, "DELETE", "/academic-terms/"+odd, nil)
	w.id(200, "PUT", "/groups/"+divA+"/academic-context", map[string]any{"cohort_id": nil, "term_id": nil})
	w.call(200, w.admin, "DELETE", "/cohorts/"+c2024, nil)
	w.call(200, w.admin, "DELETE", "/academic-terms/"+odd, nil)
	// An archived cohort can't take new classes; a new 2024 cohort may be created again.
	w.id(400, "PUT", "/groups/"+divA+"/academic-context", map[string]any{"cohort_id": c2024})
	w.id(201, "POST", "/cohorts", map[string]any{"programme_id": btech, "admission_year": 2024})
}

func TestTermsStayInsideTheirYear(t *testing.T) {
	w := build(t)
	w.id(400, "POST", "/academic-terms", map[string]any{"academic_year_id": w.year, "name": "Too early", "sequence": 1, "starts_on": "2025-06-01", "ends_on": "2025-12-15"})
	odd := w.id(201, "POST", "/academic-terms", map[string]any{"academic_year_id": w.year, "name": "Odd", "sequence": 1, "starts_on": "2025-07-01", "ends_on": "2025-12-15"})
	w.id(409, "POST", "/academic-terms", map[string]any{"academic_year_id": w.year, "name": "Overlap", "sequence": 2, "starts_on": "2025-12-15", "ends_on": "2026-03-01"})
	w.id(409, "POST", "/academic-terms", map[string]any{"academic_year_id": w.year, "name": "odd", "sequence": 3, "starts_on": "2026-01-01", "ends_on": "2026-03-01"})
	// Editing a term may not overlap itself into a conflict, but may change its own dates.
	w.id(200, "PATCH", "/academic-terms/"+odd, map[string]any{"academic_year_id": w.year, "name": "Odd", "sequence": 1, "starts_on": "2025-07-15", "ends_on": "2025-12-20"})
	// Shrinking the year must not strand the term outside it.
	w.call(409, w.admin, "PATCH", "/academic-years/"+w.year, map[string]any{"name": "2025-26", "starts_on": "2025-08-01", "ends_on": "2026-06-30"})
	w.call(200, w.admin, "PATCH", "/academic-years/"+w.year, map[string]any{"name": "2025-26", "starts_on": "2025-07-10", "ends_on": "2026-06-30"})
}

func TestTenantBoundaries(t *testing.T) {
	w := build(t)
	w.id(400, "POST", "/programmes", map[string]any{"department_id": w.foreignDept, "code": "X", "name": "Foreign department"})
	prog := w.id(201, "POST", "/programmes", map[string]any{"department_id": w.dept, "code": "BSC", "name": "B.Sc"})
	cohortID := w.id(201, "POST", "/cohorts", map[string]any{"programme_id": prog, "admission_year": 2025})
	term := w.id(201, "POST", "/academic-terms", map[string]any{"academic_year_id": w.year, "name": "Annual", "sequence": 1, "starts_on": "2025-07-01", "ends_on": "2026-06-30"})

	// The other institute cannot see or use any of it.
	if got := names(t, w.call(200, w.otherAdmin, "GET", "/programmes", nil)); len(got) != 0 {
		t.Fatalf("programmes leaked: %v", got)
	}
	w.call(400, w.otherAdmin, "POST", "/cohorts", map[string]any{"programme_id": prog, "admission_year": 2025})
	w.call(400, w.otherAdmin, "POST", "/academic-terms", map[string]any{"academic_year_id": w.year, "name": "X", "sequence": 2, "starts_on": "2025-07-01", "ends_on": "2025-08-01"})
	w.call(404, w.otherAdmin, "DELETE", "/programmes/"+prog, nil)
	w.call(400, w.otherAdmin, "PUT", "/groups/"+w.foreignClass+"/academic-context", map[string]any{"cohort_id": cohortID})
	w.call(409, w.otherAdmin, "PUT", "/groups/"+w.foreignClass+"/academic-context", map[string]any{"term_id": term})
	w.call(409, w.otherAdmin, "POST", "/course-offerings", map[string]any{"term_id": term, "code": "X1", "title": "Foreign"})

	// Offerings teach through several components; foreign classes are refused.
	theory := w.id(201, "POST", "/groups", map[string]any{"name": "Div A"})
	lab := w.id(201, "POST", "/groups", map[string]any{"name": "Batch A1"})
	off := w.id(201, "POST", "/course-offerings", map[string]any{"term_id": term, "department_id": w.dept, "code": "CS201", "title": "Data Structures"})
	w.id(400, "PUT", "/course-offerings/"+off+"/groups", map[string]any{"groups": []any{map[string]any{"group_id": w.foreignClass, "component": "theory"}}})
	w.id(200, "PUT", "/course-offerings/"+off+"/groups", map[string]any{"groups": []any{
		map[string]any{"group_id": theory, "component": "theory"}, map[string]any{"group_id": lab, "component": "lab"}}})
	var offs []struct {
		Groups []struct{ Name, Component string } `json:"groups"`
	}
	_ = json.Unmarshal(w.call(200, w.admin, "GET", "/course-offerings?term_id="+term, nil), &offs)
	if len(offs) != 1 || len(offs[0].Groups) != 2 || offs[0].Groups[1].Component != "theory" {
		t.Fatalf("offering groups: %+v", offs)
	}
	// Terms with offerings are in use.
	w.call(409, w.admin, "DELETE", "/academic-terms/"+term, nil)
}
