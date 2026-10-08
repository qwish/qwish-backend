package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestInstitutionAcademicsSnapshot(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	tag := fmt.Sprint(time.Now().UnixNano())
	var inst, other, dept, prog, cohort, year, term, group, teacher, version string
	q := func(dest *string, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(dest); err != nil {
			t.Fatal(err)
		}
	}
	for _, x := range []struct {
		dest  *string
		label string
	}{{&inst, "acad"}, {&other, "acadother"}} {
		q(x.dest, `INSERT INTO institutions (name,type,contact_email,student_referral_code,teacher_referral_code,status)
			VALUES ($1||$2,'college',$1||$2||'@example.test','S'||$1||$2,'T'||$1||$2,'verified') RETURNING id`, x.label, tag)
	}
	t.Cleanup(func() {
		for _, s := range []string{`DELETE FROM class_curricula WHERE institution_id=$1`, `DELETE FROM curriculum_versions WHERE institution_id=$1`,
			`DELETE FROM curricula WHERE institution_id=$1`, `DELETE FROM offering_groups WHERE institution_id=$1`,
			`DELETE FROM course_offerings WHERE institution_id=$1`, `DELETE FROM group_teachers WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`,
			`DELETE FROM groups WHERE institution_id=$1`, `DELETE FROM academic_terms WHERE institution_id=$1`,
			`DELETE FROM academic_years WHERE institution_id=$1`, `DELETE FROM cohorts WHERE institution_id=$1`,
			`DELETE FROM programmes WHERE institution_id=$1`, `DELETE FROM staff_role_assignments WHERE institution_id=$1`,
			`DELETE FROM departments WHERE institution_id=$1`, `DELETE FROM users WHERE institution_id=$1`, `DELETE FROM institutions WHERE id=$1`} {
			for _, id := range []string{inst, other} {
				pool.Exec(ctx, s, id)
			}
		}
	})
	q(&dept, `INSERT INTO departments (institution_id,name,code) VALUES ($1,'Computer Engineering','CE') RETURNING id`, inst)
	q(&teacher, `INSERT INTO users (supabase_uid,full_name,display_name,email,role,institution_id) VALUES (gen_random_uuid(),'Asha Rao','Asha Rao',$1,'teacher',$2) RETURNING id`, "asha"+tag+"@example.test", inst)
	q(new(string), `INSERT INTO staff_role_assignments (institution_id,user_id,role,department_id,reason) VALUES ($1,$2,'hod',$3,'test') RETURNING id`, inst, teacher, dept)
	q(&prog, `INSERT INTO programmes (institution_id,department_id,code,name,award) VALUES ($1,$2,'BTCE','B.Tech CE','B.Tech') RETURNING id`, inst, dept)
	q(&cohort, `INSERT INTO cohorts (institution_id,programme_id,admission_year,completion_year) VALUES ($1,$2,2024,2028) RETURNING id`, inst, prog)
	q(&year, `INSERT INTO academic_years (institution_id,name,starts_on,ends_on) VALUES ($1,'2026-27','2026-07-01','2027-06-30') RETURNING id`, inst)
	q(&term, `INSERT INTO academic_terms (institution_id,academic_year_id,name,sequence,starts_on,ends_on) VALUES ($1,$2,'Odd',1,'2026-07-01','2026-12-15') RETURNING id`, inst, year)
	q(&group, `INSERT INTO groups (institution_id,name,invite_code,grade,section,department_id,cohort_id,term_id) VALUES ($1,'SE A',$2,'Semester III','A',$3,$4,$5) RETURNING id`, inst, "A"+tag[len(tag)-7:], dept, cohort, term)
	if _, err := pool.Exec(ctx, `INSERT INTO group_teachers (group_id,user_id) VALUES ($1,$2)`, group, teacher); err != nil {
		t.Fatal(err)
	}
	var curriculum string
	q(&curriculum, `INSERT INTO curricula (institution_id,name) VALUES ($1,'Data Structures') RETURNING id`, inst)
	q(&version, `INSERT INTO curriculum_versions (curriculum_id,institution_id,label,subject,grade,status,published_at) VALUES ($1,$2,'2024','CS','SE','published',now()) RETURNING id`, curriculum, inst)
	q(new(string), `INSERT INTO class_curricula (institution_id,group_id,academic_year_id,curriculum_id,version_id) VALUES ($1,$2,$3,$4,$5) RETURNING id`, inst, group, year, curriculum, version)
	var offering string
	q(&offering, `INSERT INTO course_offerings (institution_id,term_id,department_id,code,title) VALUES ($1,$2,$3,'CS201','DS') RETURNING id`, inst, term, dept)
	if _, err := pool.Exec(ctx, `INSERT INTO offering_groups (offering_id,group_id,institution_id,component) VALUES ($1,$2,$3,'lab')`, offering, group, inst); err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Get("/i/{institutionId}", (&Handler{db: pool}).InstitutionAcademics)
	get := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/i/"+id, nil))
		return w
	}
	w := get(inst)
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Data struct {
			Departments []struct{ Name string } `json:"departments"`
			Roles       []struct {
				Role     string `json:"role"`
				State    string `json:"state"`
				UserName string `json:"user_name"`
			} `json:"roles"`
			Years []struct {
				Terms []struct{ Name string } `json:"terms"`
			} `json:"years"`
			Programmes []struct {
				Code    string `json:"code"`
				Cohorts []struct {
					DivisionCount int `json:"division_count"`
				} `json:"cohorts"`
			} `json:"programmes"`
			Curricula []struct {
				ClassCount int `json:"class_count"`
			} `json:"curricula"`
			Offerings []struct {
				Groups []struct{ Component string } `json:"groups"`
			} `json:"offerings"`
			Classes []struct {
				TermName  string   `json:"term_name"`
				Teachers  []string `json:"teachers"`
				Curricula []any    `json:"curricula"`
				Cohort    *struct {
					ProgrammeCode string `json:"programme_code"`
				} `json:"cohort"`
			} `json:"classes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	d := body.Data
	switch {
	case len(d.Departments) != 1 || d.Departments[0].Name != "Computer Engineering":
		t.Fatalf("departments %+v", d.Departments)
	case len(d.Roles) != 1 || d.Roles[0].Role != "hod" || d.Roles[0].State != "active" || d.Roles[0].UserName != "Asha Rao":
		t.Fatalf("roles %+v", d.Roles)
	case len(d.Years) != 1 || len(d.Years[0].Terms) != 1:
		t.Fatalf("years %+v", d.Years)
	case len(d.Programmes) != 1 || len(d.Programmes[0].Cohorts) != 1 || d.Programmes[0].Cohorts[0].DivisionCount != 1:
		t.Fatalf("programmes %+v", d.Programmes)
	case len(d.Curricula) != 1 || d.Curricula[0].ClassCount != 1:
		t.Fatalf("curricula %+v", d.Curricula)
	case len(d.Offerings) != 1 || len(d.Offerings[0].Groups) != 1 || d.Offerings[0].Groups[0].Component != "lab":
		t.Fatalf("offerings %+v", d.Offerings)
	case len(d.Classes) != 1 || d.Classes[0].TermName != "Odd" || len(d.Classes[0].Teachers) != 1 || len(d.Classes[0].Curricula) != 1 ||
		d.Classes[0].Cohort == nil || d.Classes[0].Cohort.ProgrammeCode != "BTCE":
		t.Fatalf("classes %+v", d.Classes)
	}

	// Another institute's snapshot is empty, not a leak; unknown ids are 404.
	var empty struct {
		Data map[string][]any `json:"data"`
	}
	if err := json.Unmarshal(get(other).Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	for k, v := range empty.Data {
		if len(v) != 0 {
			t.Fatalf("%s leaked into another institute: %v", k, v)
		}
	}
	if c := get("00000000-0000-4000-8000-000000000000").Code; c != 404 {
		t.Fatalf("unknown institution: %d", c)
	}
	if c := get("nope").Code; c != 400 {
		t.Fatalf("bad id: %d", c)
	}
}
