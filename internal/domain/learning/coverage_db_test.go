package learning

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// coverageSeed: one class (teacher T) of three active students and one
// suspended. Concept K: S1 has fresh evidence (2 questions wrong → needs
// support), S2 has 200-day-old evidence (2 questions right → on track, stale),
// S3 has none (not assessed). The suspended student has fresh evidence and
// counts nowhere.
type coverageSeed struct {
	Inst, Teacher, Class, Concept string
	S1, S2, S3, Suspended         string
}

func seedCoverage(t *testing.T) (*pgxpool.Pool, coverageSeed) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var s coverageSeed
	must(pool.QueryRow(ctx, `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('Cov '||$1,'school','cov-'||$1||'@example.test','VS'||$1,'VT'||$1,'verified') RETURNING id`, tag).Scan(&s.Inst))
	user := func(role, label string, dest *string) {
		t.Helper()
		must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $1||'-'||$2||'@example.test', $3, $4) RETURNING id`, label, tag, role, s.Inst).Scan(dest))
	}
	user("teacher", "T", &s.Teacher)
	must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id, name, invite_code) VALUES ($1,'K class','K'||$2) RETURNING id`, s.Inst, tag).Scan(&s.Class))
	_, err = pool.Exec(ctx, `INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2)`, s.Class, s.Teacher)
	must(err)
	for _, st := range []struct {
		label, status string
		dest          *string
	}{{"S1", "active", &s.S1}, {"S2", "active", &s.S2}, {"S3", "active", &s.S3}, {"Sx", "suspended", &s.Suspended}} {
		user("student", st.label, st.dest)
		_, err = pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at) VALUES ($1,$2,'s',$3,now()-interval '300 days')`, s.Inst, *st.dest, st.status)
		must(err)
		_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, s.Class, *st.dest)
		must(err)
	}
	conn, err := pool.Acquire(ctx)
	must(err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `SET session_replication_role = replica`)
	must(err)
	must(conn.QueryRow(ctx, `INSERT INTO curriculum_concepts (chapter_id, code, title, position) VALUES (gen_random_uuid(),'K'||$1,'Kinetics',1) RETURNING id`, tag).Scan(&s.Concept))
	evidence := func(student string, correct bool, ago string) {
		t.Helper()
		_, err := conn.Exec(ctx, `INSERT INTO learning_evidence (response_id, institution_id, user_id, attempt_id, question_id, question_revision, question_version_id, concept_id, is_correct, occurred_at)
			SELECT gen_random_uuid(), $1, $2, gen_random_uuid(), gen_random_uuid(), 1, gen_random_uuid(), $3, $4, now()-$5::interval FROM generate_series(1,2)`, s.Inst, student, s.Concept, correct, ago)
		must(err)
	}
	evidence(s.S1, false, "1 day")
	evidence(s.S2, true, "200 days")
	evidence(s.Suspended, false, "1 day")
	_, err = conn.Exec(ctx, `RESET session_replication_role`)
	must(err)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM learning_evidence WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM curriculum_concepts WHERE id=$1`, s.Concept)
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id=$1`, s.Class)
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE group_id=$1`, s.Class)
		pool.Exec(ctx, `DELETE FROM groups WHERE id=$1`, s.Class)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM users WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, s.Inst)
	})
	return pool, s
}

func TestMatrixNotAssessedAndStale(t *testing.T) {
	pool, s := seedCoverage(t)
	h := NewHandler(pool, nil)
	call := func(query string) []map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", "/teacher/class-learning-matrix?class_id="+s.Class+query, nil)
		ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, s.Teacher)
		ctx = context.WithValue(ctx, middleware.ContextKeyInstID, s.Inst)
		w := httptest.NewRecorder()
		h.TeacherClassMatrix(w, req.WithContext(ctx))
		var rows []map[string]any
		decodeData(t, w.Body.Bytes(), &rows)
		return rows
	}
	byStudent := func(rows []map[string]any) map[string]map[string]any {
		m := map[string]map[string]any{}
		for _, r := range rows {
			m[r["student_id"].(string)] = r
		}
		return m
	}

	// Default: evidence rows only, as before, each with a stale flag.
	plain := byStudent(call(""))
	if _, ok := plain[s.S3]; ok {
		t.Error("not-assessed rows appeared without include_unassessed")
	}
	if plain[s.S1]["state"] != "needs_support" || plain[s.S1]["stale"] != false {
		t.Errorf("S1 = %v, want needs_support, fresh", plain[s.S1])
	}
	if plain[s.S2]["state"] != "on_track" || plain[s.S2]["stale"] != true {
		t.Errorf("S2 = %v, want on_track, stale after the default 90 days", plain[s.S2])
	}

	full := byStudent(call("&include_unassessed=true"))
	if full[s.S3]["state"] != "not_assessed" || full[s.S3]["concept_id"] != s.Concept || full[s.S3]["latest_evidence_at"] != nil {
		t.Errorf("S3 = %v, want a not_assessed row with no evidence date", full[s.S3])
	}
	if _, ok := full[s.Suspended]; ok {
		t.Error("suspended student listed; roster exceptions are not academic rows")
	}
	if wide := byStudent(call("&stale_after_days=365")); wide[s.S2]["stale"] != false {
		t.Errorf("stale_after_days=365: S2 = %v, want fresh", wide[s.S2])
	}
}

func TestLearningSummaryDenominators(t *testing.T) {
	pool, s := seedCoverage(t)
	h := NewHandler(pool, nil)
	call := func(query string) map[string]any {
		t.Helper()
		req := httptest.NewRequest("GET", "/teacher/learning-summary?class_id="+s.Class+query, nil)
		ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, s.Teacher)
		ctx = context.WithValue(ctx, middleware.ContextKeyInstID, s.Inst)
		w := httptest.NewRecorder()
		h.TeacherClassSummary(w, req.WithContext(ctx))
		var rows []map[string]any
		decodeData(t, w.Body.Bytes(), &rows)
		if len(rows) != 1 {
			t.Fatalf("rows = %v", rows)
		}
		return rows[0]
	}
	all := call("")
	want := map[string]float64{"eligible_students": 3, "students_assessed": 2, "students_not_assessed": 1, "students_needing_support": 1, "stale_students": 1}
	for k, v := range want {
		if all[k] != v {
			t.Errorf("%s = %v, want %v (row %v)", k, all[k], v, all)
		}
	}
	// A 30-day window keeps only S1's evidence: S2 becomes not assessed.
	recent := call("&days=30")
	if recent["students_assessed"] != 1.0 || recent["students_not_assessed"] != 2.0 {
		t.Errorf("days=30 row = %v", recent)
	}
}
