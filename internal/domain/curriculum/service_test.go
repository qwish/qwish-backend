package curriculum

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tests run the real migration in a private scratch schema. Core reference
// tables mirror the columns/keys this module uses; no application rows are read
// or altered. TEST_DATABASE_URL must point to a disposable database with the
// Supabase anon/authenticated roles provisioned.
func testService(t *testing.T) (*Service, Actor, string, string) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — PostgreSQL integration tests skipped")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "curriculum_test_" + uuid.New().String()[:8]
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		if err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `CREATE TABLE institutions(id UUID PRIMARY KEY);
		CREATE TABLE users(id UUID PRIMARY KEY,display_name TEXT);
		CREATE TABLE groups(id UUID PRIMARY KEY,institution_id UUID REFERENCES institutions(id),archived_at TIMESTAMPTZ);
		CREATE TABLE group_teachers(group_id UUID REFERENCES groups(id),user_id UUID REFERENCES users(id),PRIMARY KEY(group_id,user_id));
		CREATE TABLE audit_log(admin_id UUID NOT NULL,admin_name TEXT NOT NULL,admin_role TEXT NOT NULL,action_type TEXT NOT NULL,target_type TEXT NOT NULL,target_id UUID,institution_id UUID REFERENCES institutions(id));`)
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	sql, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../..", "migrations/058_curriculum_foundation.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("migration: %v", err)
	}
	a := Actor{InstitutionID: uuid.NewString(), ID: uuid.NewString()}
	group := uuid.NewString()
	teacher := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO institutions VALUES($1)`, a.InstitutionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO users VALUES($1,'Admin'),($2,'Teacher')`, a.ID, teacher); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO groups VALUES($1,$2,NULL)`, group, a.InstitutionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO group_teachers VALUES($1,$2)`, group, teacher); err != nil {
		t.Fatal(err)
	}
	return NewService(pool), a, group, teacher
}

func TestCurriculumLifecycleAndScope(t *testing.T) {
	s, a, group, teacher := testService(t)
	ctx := context.Background()
	year, err := s.CreateYear(ctx, a, YearInput{Name: "2026–27", StartsOn: "2026-06-01", EndsOn: "2027-05-31"})
	if err != nil {
		t.Fatal(err)
	}
	years, err := s.ListYears(ctx, a.InstitutionID)
	if err != nil || len(years) != 1 {
		t.Fatalf("years: %v %v", years, err)
	}
	id, err := s.CreateVersion(ctx, a, "", "Math", sampleVersion())
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.GetVersion(ctx, a.InstitutionID, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Chapters) != 1 || len(v.Chapters[0].Concepts) != 1 || v.Revision != 1 {
		t.Fatalf("bad version: %+v", v)
	}
	if _, err = s.GetVersion(ctx, a.InstitutionID, id, teacher); !errors.Is(err, ErrNotFound) {
		t.Fatalf("teacher saw draft: %v", err)
	}
	if _, err = s.Assign(ctx, a, group, AssignmentInput{year.ID, id}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assigned draft: %v", err)
	}
	if err = s.UpdateVersion(ctx, a, id, 99, sampleVersion()); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale save: %v", err)
	}
	updated := sampleVersion()
	updated.Chapters[0].Title = "Revised fractions"
	if err = s.UpdateVersion(ctx, a, id, 1, updated); err != nil {
		t.Fatal(err)
	}
	if err = s.PublishVersion(ctx, a, id, 1); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale publish: %v", err)
	}
	if err = s.PublishVersion(ctx, a, id, 2); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateVersion(ctx, a, id, 3, updated); !errors.Is(err, ErrPublished) {
		t.Fatalf("edited published: %v", err)
	}
	assignmentID, err := s.Assign(ctx, a, group, AssignmentInput{year.ID, id})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Assign(ctx, a, group, AssignmentInput{year.ID, id}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate assignment: %v", err)
	}
	if _, err = s.GetVersion(ctx, a.InstitutionID, id, teacher); err != nil {
		t.Fatalf("assigned teacher: %v", err)
	}
	if _, err = s.GetVersion(ctx, a.InstitutionID, id, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unassigned teacher: %v", err)
	}
	items, err := s.ListAssignments(ctx, a.InstitutionID, group, teacher)
	if err != nil || len(items) != 1 || items[0].Version.ID != id || items[0].ID != assignmentID {
		t.Fatalf("assignments: %+v %v", items, err)
	}
	if _, err = s.ListAssignments(ctx, a.InstitutionID, group, uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unassigned teacher list: %v", err)
	}
	other := Actor{InstitutionID: uuid.NewString(), ID: a.ID}
	if _, err = s.GetVersion(ctx, other.InstitutionID, id, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross institution read: %v", err)
	}
	if err = s.PublishVersion(ctx, other, id, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross institution publish: %v", err)
	}
	if _, err = s.CreateVersion(ctx, other, v.CurriculumID, "", sampleVersion()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross institution version: %v", err)
	}
	if err = s.EndAssignment(ctx, other, group, assignmentID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross institution archive: %v", err)
	}
	if err = s.EndAssignment(ctx, a, group, assignmentID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetVersion(ctx, a.InstitutionID, id, teacher); !errors.Is(err, ErrNotFound) {
		t.Fatalf("teacher retained ended access: %v", err)
	}
	var ended bool
	if err = s.db.QueryRow(ctx, `SELECT ended_at IS NOT NULL FROM class_curricula WHERE id=$1`, assignmentID).Scan(&ended); err != nil || !ended {
		t.Fatalf("history not retained: %v", err)
	}
	versions, total, err := s.ListVersions(ctx, a.InstitutionID, 2, 20)
	if err != nil || len(versions) != 0 || total != 1 {
		t.Fatalf("empty page total: %d %v", total, err)
	}
	var audits int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE institution_id=$1`, a.InstitutionID).Scan(&audits); err != nil || audits != 6 {
		t.Fatalf("audit count=%d err=%v", audits, err)
	}
}

func TestPublishedContentDatabaseGuards(t *testing.T) {
	s, a, _, _ := testService(t)
	ctx := context.Background()
	id, err := s.CreateVersion(ctx, a, "", "Math", sampleVersion())
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.GetVersion(ctx, a.InstitutionID, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PublishVersion(ctx, a, id, 1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE curriculum_versions SET grade='7' WHERE id=$1`, []any{id}},
		{`DELETE FROM curriculum_versions WHERE id=$1`, []any{id}},
		{`UPDATE curriculum_chapters SET title='changed' WHERE id=$1`, []any{v.Chapters[0].ID}},
		{`DELETE FROM curriculum_concepts WHERE id=$1`, []any{v.Chapters[0].Concepts[0].ID}},
		{`INSERT INTO curriculum_concepts(chapter_id,code,title,position) VALUES($1,'NEW','New',2)`, []any{v.Chapters[0].ID}},
		{`INSERT INTO curriculum_chapters(version_id,title,position) VALUES($1,'New',2)`, []any{id}},
	} {
		if _, err = s.db.Exec(ctx, tc.sql, tc.args...); err == nil {
			t.Errorf("guard accepted %s", tc.sql)
		}
	}
}

func TestConcurrentDraftSaveAndPublish(t *testing.T) {
	s, a, _, _ := testService(t)
	ctx := context.Background()
	id, err := s.CreateVersion(ctx, a, "", "Math", sampleVersion())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	wg.Add(2)
	go func() { defer wg.Done(); <-start; results <- s.UpdateVersion(ctx, a, id, 1, sampleVersion()) }()
	go func() { defer wg.Done(); <-start; results <- s.PublishVersion(ctx, a, id, 1) }()
	close(start)
	wg.Wait()
	close(results)
	success := 0
	conflicts := 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrRevision) || errors.Is(err, ErrPublished) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
}

func TestIncompleteVersionCannotPublish(t *testing.T) {
	s, a, _, _ := testService(t)
	ctx := context.Background()
	in := sampleVersion()
	in.Chapters[0].Concepts = nil
	id, err := s.CreateVersion(ctx, a, "", "Math", in)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PublishVersion(ctx, a, id, 1); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("got %v", err)
	}
}

func TestAssignmentRejectsCrossInstitutionReferences(t *testing.T) {
	s, a, group, _ := testService(t)
	ctx := context.Background()
	other := Actor{InstitutionID: uuid.NewString(), ID: a.ID}
	if _, err := s.db.Exec(ctx, `INSERT INTO institutions VALUES($1)`, other.InstitutionID); err != nil {
		t.Fatal(err)
	}
	year, err := s.CreateYear(ctx, other, YearInput{Name: "Other year", StartsOn: "2026-01-01", EndsOn: "2026-12-31"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateVersion(ctx, a, "", "Math", sampleVersion())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PublishVersion(ctx, a, id, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Assign(ctx, a, group, AssignmentInput{year.ID, id}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross institution year: %v", err)
	}
	v, err := s.GetVersion(ctx, a.InstitutionID, id, "")
	if err != nil {
		t.Fatal(err)
	}
	// Composite foreign keys must reject a direct invalid reference too.
	if _, err = s.db.Exec(ctx, `INSERT INTO class_curricula(institution_id,group_id,academic_year_id,curriculum_id,version_id) VALUES($1,$2,$3,$4,$5)`, a.InstitutionID, group, year.ID, v.CurriculumID, id); err == nil {
		t.Fatal("database accepted cross institution year")
	}
}

func TestHTTPCreatePublishAssignAndTeacherRead(t *testing.T) {
	s, a, group, teacher := testService(t)
	router := routes(s)
	call := func(method, path, body, role, user string, status int) json.RawMessage {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, requestAs(method, path, body, role, a.InstitutionID, user))
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	yearData := call("POST", "/institution/academic-years", `{"name":"2026","starts_on":"2026-01-01","ends_on":"2026-12-31"}`, "institution_admin", a.ID, http.StatusCreated)
	var year Year
	if err := json.Unmarshal(yearData, &year); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(CreateInput{Name: "Math", VersionInput: sampleVersion()})
	if err != nil {
		t.Fatal(err)
	}
	created := call("POST", "/institution/curricula", string(body), "institution_admin", a.ID, http.StatusCreated)
	var result struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(created, &result); err != nil {
		t.Fatal(err)
	}
	call("POST", "/institution/curriculum-versions/"+result.ID+"/publish", `{"revision":1}`, "institution_admin", a.ID, http.StatusOK)
	call("POST", "/institution/groups/"+group+"/curricula", fmt.Sprintf(`{"academic_year_id":%q,"version_id":%q}`, year.ID, result.ID), "institution_admin", a.ID, http.StatusCreated)
	data := call("GET", "/teacher/curriculum-versions/"+result.ID, "", "teacher", teacher, http.StatusOK)
	var version Version
	if err = json.Unmarshal(data, &version); err != nil {
		t.Fatal(err)
	}
	if version.Status != "published" || version.PublishedAt == nil || len(version.Chapters) != 1 {
		t.Fatalf("bad contract: %+v", version)
	}
	data = call("GET", "/teacher/classes/"+group+"/curricula", "", "teacher", teacher, http.StatusOK)
	var assignments []Assignment
	if err = json.Unmarshal(data, &assignments); err != nil {
		t.Fatal(err)
	}
	if len(assignments) != 1 || assignments[0].Version.ID != result.ID || assignments[0].ID == result.ID {
		t.Fatalf("assignment/version IDs ambiguous: %+v", assignments)
	}
}

func TestCurriculumTablesBlockDirectClientAccess(t *testing.T) {
	s, _, _, _ := testService(t)
	ctx := context.Background()
	for _, table := range []string{"academic_years", "curricula", "curriculum_versions", "curriculum_chapters", "curriculum_concepts", "class_curricula"} {
		var protected bool
		err := s.db.QueryRow(ctx, `SELECT relrowsecurity
		  AND NOT has_table_privilege('anon',oid,'SELECT,INSERT,UPDATE,DELETE')
		  AND NOT has_table_privilege('authenticated',oid,'SELECT,INSERT,UPDATE,DELETE')
		  FROM pg_class WHERE oid=to_regclass($1)`, table).Scan(&protected)
		if err != nil || !protected {
			t.Errorf("%s protected=%v err=%v", table, protected, err)
		}
	}
}
