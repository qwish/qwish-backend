package teacher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// remedialSchool: Teacher teaches A (S1, S2, grade 9); Other teaches B (S3).
// Concept has two wrong answers from S1 an hour ago.
type remedialSchool struct{ Inst, Teacher, Other, A, B, S1, S2, S3, Concept string }

func seedRemedial(t *testing.T, pool *pgxpool.Pool) remedialSchool {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var s remedialSchool
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('rem'||$1, 'school', 'rem'||$1||'@example.test', 'SR'||$1, 'TR'||$1, 'verified') RETURNING id`, tag).Scan(&s.Inst))
	user := func(role, label string, dest *string) {
		must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $1||$2||'@example.test', $3, $4) RETURNING id`, label, tag, role, s.Inst).Scan(dest))
	}
	user("teacher", "teacher", &s.Teacher)
	user("teacher", "other", &s.Other)
	user("student", "s1", &s.S1)
	user("student", "s2", &s.S2)
	user("student", "s3", &s.S3)
	class := func(name, teacher string, dest *string, members ...string) {
		must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id, name, invite_code, grade) VALUES ($1,$2,$2||$3,'9') RETURNING id`, s.Inst, name, tag).Scan(dest))
		_, err := pool.Exec(ctx, `INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2)`, *dest, teacher)
		must(err)
		for _, m := range members {
			_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, *dest, m)
			must(err)
		}
	}
	for _, u := range []string{s.S1, s.S2, s.S3} {
		_, err := pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at) VALUES ($1,$2,'s','active',now())`, s.Inst, u)
		must(err)
	}
	class("A", s.Teacher, &s.A, s.S1, s.S2)
	class("B", s.Other, &s.B, s.S3)

	// Evidence needs a response, attempt and question; skip FK checks on this
	// connection rather than build a whole quiz.
	conn, err := pool.Acquire(ctx)
	must(err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `SET session_replication_role = replica`)
	must(err)
	must(conn.QueryRow(ctx, `INSERT INTO curriculum_concepts (chapter_id, code, title, position) VALUES (gen_random_uuid(), 'FR'||$1, 'Fractions', 1) RETURNING id`, tag).Scan(&s.Concept))
	_, err = conn.Exec(ctx, `INSERT INTO learning_evidence (response_id, institution_id, user_id, attempt_id, question_id, question_revision, question_version_id, concept_id, is_correct, occurred_at)
		SELECT gen_random_uuid(), $1, $2, gen_random_uuid(), gen_random_uuid(), 1, gen_random_uuid(), $3, false, now()-interval '1 hour' FROM generate_series(1,2)`, s.Inst, s.S1, s.Concept)
	must(err)
	_, err = conn.Exec(ctx, `RESET session_replication_role`)
	must(err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM learning_evidence WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM curriculum_concepts WHERE id=$1`, s.Concept)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []string{s.Teacher, s.Other, s.S1, s.S2, s.S3})
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, s.Inst)
	})
	return s
}

func createRemedial(h *Handler, teacherID, instID, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.CreateRemedialGroup(w, teacherRequest(httptest.NewRequest("POST", "/", strings.NewReader(body)), teacherID, instID))
	return w
}

func remedialBody(s remedialSchool, students ...string) string {
	b, _ := json.Marshal(map[string]any{"source_class_id": s.A, "concept_id": s.Concept, "name": "Fractions practice", "student_ids": students})
	return string(b)
}

func TestCreateRemedialGroup(t *testing.T) {
	pool := openTestDB(t)
	s := seedRemedial(t, pool)
	ctx := context.Background()
	pool.Exec(ctx, `UPDATE enrollments SET grade='8' WHERE user_id=$1`, s.S1)
	w := createRemedial(NewHandler(pool), s.Teacher, s.Inst, remedialBody(s, s.S1, s.S2))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var out struct {
		ID          string `json:"id"`
		MemberCount int    `json:"member_count"`
	}
	decodeData(t, w, &out)
	var kind, source string
	var joining, ungraded bool
	pool.QueryRow(ctx, `SELECT kind, joining_enabled, grade IS NULL, source_group_id::text FROM groups WHERE id=$1`, out.ID).
		Scan(&kind, &joining, &ungraded, &source)
	if kind != "remedial" || joining || !ungraded || source != s.A || out.MemberCount != 2 {
		t.Fatalf("group: kind=%s joining=%v ungraded=%v source=%s members=%d", kind, joining, ungraded, source, out.MemberCount)
	}
	var teachers, students int
	var grade string
	pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM group_teachers WHERE group_id=$1 AND user_id=$2),
		(SELECT count(*) FROM group_students WHERE group_id=$1),
		(SELECT grade FROM enrollments WHERE user_id=$3)`, out.ID, s.Teacher, s.S1).Scan(&teachers, &students, &grade)
	if teachers != 1 || students != 2 || grade != "8" {
		t.Fatalf("teachers=%d students=%d S1 grade=%q", teachers, students, grade)
	}
}

func TestRemedialMembersMustComeFromSourceClass(t *testing.T) {
	pool := openTestDB(t)
	s := seedRemedial(t, pool)
	h := NewHandler(pool)
	if w := createRemedial(h, s.Teacher, s.Inst, remedialBody(s, s.S1, s.S3)); w.Code != 400 {
		t.Fatalf("student from another class: want 400, got %d", w.Code)
	}
	if w := createRemedial(h, s.Other, s.Inst, remedialBody(s, s.S1)); w.Code != 403 {
		t.Fatalf("teacher who doesn't teach the class: want 403, got %d", w.Code)
	}
	var n int
	pool.QueryRow(context.Background(), `SELECT count(*) FROM groups WHERE institution_id=$1 AND kind='remedial'`, s.Inst).Scan(&n)
	if n != 0 {
		t.Fatalf("rejected requests created %d groups", n)
	}
}

func TestRemedialConceptMustHaveEvidence(t *testing.T) {
	pool := openTestDB(t)
	s := seedRemedial(t, pool)
	b, _ := json.Marshal(map[string]any{"source_class_id": s.A, "concept_id": s.B, "name": "x", "student_ids": []string{s.S1}})
	if w := createRemedial(NewHandler(pool), s.Teacher, s.Inst, string(b)); w.Code != 400 {
		t.Fatalf("concept with no evidence: want 400, got %d", w.Code)
	}
	b, _ = json.Marshal(map[string]any{"source_class_id": "nope", "concept_id": s.Concept, "name": "x", "student_ids": []string{"bad"}})
	if w := createRemedial(NewHandler(pool), s.Teacher, s.Inst, string(b)); w.Code != 400 {
		t.Fatalf("malformed ids: want 400, got %d", w.Code)
	}
}

func TestRemedialProgress(t *testing.T) {
	pool := openTestDB(t)
	s := seedRemedial(t, pool)
	ctx := context.Background()
	h := NewHandler(pool)
	w := createRemedial(h, s.Teacher, s.Inst, remedialBody(s, s.S1, s.S2))
	var created struct{ ID string }
	decodeData(t, w, &created)

	conn, _ := pool.Acquire(ctx)
	conn.Exec(ctx, `SET session_replication_role = replica`)
	conn.Exec(ctx, `INSERT INTO learning_evidence (response_id, institution_id, user_id, attempt_id, question_id, question_revision, question_version_id, concept_id, is_correct, occurred_at)
		VALUES (gen_random_uuid(), $1, $2, gen_random_uuid(), gen_random_uuid(), 1, gen_random_uuid(), $3, true, now()+interval '1 minute')`, s.Inst, s.S1, s.Concept)
	conn.Exec(ctx, `RESET session_replication_role`)
	conn.Release()

	get := func(teacherID string) *httptest.ResponseRecorder {
		req := teacherRequest(httptest.NewRequest("GET", "/", nil), teacherID, s.Inst)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("groupId", created.ID)
		w := httptest.NewRecorder()
		h.RemedialProgress(w, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
		return w
	}
	if w := get(s.Other); w.Code != 404 {
		t.Fatalf("other teacher: want 404, got %d", w.Code)
	}
	w = get(s.Teacher)
	var out struct {
		ConceptTitle string `json:"concept_title"`
		Students     []struct {
			ID     string `json:"student_id"`
			Before struct{ Correct, Errors int }
			After  struct{ Correct, Errors int }
		}
	}
	if w.Code != 200 {
		t.Fatalf("progress: %d %s", w.Code, w.Body)
	}
	decodeData(t, w, &out)
	for _, st := range out.Students {
		if st.ID == s.S1 && (st.Before.Errors != 2 || st.After.Correct != 1) {
			t.Fatalf("S1 before=%+v after=%+v", st.Before, st.After)
		}
	}
	if out.ConceptTitle != "Fractions" || len(out.Students) != 2 {
		t.Fatalf("progress body: %s", w.Body)
	}
}

// decodeData unwraps the {success, data} envelope.
func decodeData(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	var env struct{ Data json.RawMessage }
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		t.Fatalf("%v: %s", err, w.Body)
	}
}
