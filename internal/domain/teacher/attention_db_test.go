package teacher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// attentionSeed is the Phase 0 fixture set (plans/teacher-and-institute-
// decision-dashboards.md §8). Expected numbers for Teacher:
//
//	S1 two classes (A, B), overdue in both          → flagged, 2 overdue submissions
//	S2 excused on one, extended on another,
//	   one question answered wrong three times      → no learning signal (repeat attempts ≠ variety),
//	                                                  but flagged for repeated wrong answers, listed last
//	S1 also: a question wrong twice, then right      → no repeated-wrong reason (latest answer correct)
//	S3 overdue support review + needs-support concept → flagged, two reasons, listed first
//	S4 overdue in A, then transferred to C          → not flagged (no longer on the roster)
//	S5 suspended, overdue in A                      → not flagged (roster exception)
//	Loner teacher, no classes                       → no-assigned-classes state, nothing listed
type attentionSeed struct {
	Inst, Teacher, Other, Loner string
	A, B, C                     string
	S1, S2, S3, S4, S5          string
	AsgA, AsgB, AsgA2           string
	Concept                     string
}

func seedAttention(t *testing.T, pool *pgxpool.Pool) attentionSeed {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var s attentionSeed
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('Attention '||$1,'school','att-'||$1||'@example.test','AS'||$1,'AT'||$1,'verified') RETURNING id`, tag).Scan(&s.Inst))
	user := func(role, label string, dest *string) {
		t.Helper()
		must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $1||'-'||$2||'@example.test', $3, $4) RETURNING id`, label, tag, role, s.Inst).Scan(dest))
	}
	user("teacher", "teacher", &s.Teacher)
	user("teacher", "other", &s.Other)
	user("teacher", "loner", &s.Loner)
	for i, d := range []*string{&s.S1, &s.S2, &s.S3, &s.S4, &s.S5} {
		user("student", fmt.Sprintf("S%d", i+1), d)
		status := "active"
		if d == &s.S5 {
			status = "suspended"
		}
		_, err := pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at) VALUES ($1,$2,'s',$3,now()-interval '60 days')`, s.Inst, *d, status)
		must(err)
	}
	class := func(name, teacher string, dest *string, members ...string) {
		t.Helper()
		must(pool.QueryRow(ctx, `INSERT INTO groups (institution_id, name, invite_code) VALUES ($1,$2,$2||$3) RETURNING id`, s.Inst, name, tag).Scan(dest))
		_, err := pool.Exec(ctx, `INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2)`, *dest, teacher)
		must(err)
		for _, m := range members {
			_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, *dest, m)
			must(err)
		}
	}
	class("A", s.Teacher, &s.A, s.S1, s.S2, s.S3, s.S4, s.S5)
	class("B", s.Teacher, &s.B, s.S1)
	class("C", s.Other, &s.C)

	var quiz string
	must(pool.QueryRow(ctx, `INSERT INTO quizzes (institution_id, created_by, title, type, status) VALUES ($1,$2,'Fractions check','knowledge_check','published') RETURNING id`, s.Inst, s.Teacher).Scan(&quiz))
	assignment := func(group string, due string, dest *string) {
		t.Helper()
		must(pool.QueryRow(ctx, `INSERT INTO learning_assignments (institution_id, group_id, quiz_id, purpose, due_at, created_by)
			VALUES ($1,$2,$3,'practice',now()+$4::interval,$5) RETURNING id`, s.Inst, group, quiz, due, s.Teacher).Scan(dest))
	}
	assignment(s.A, "-1 day", &s.AsgA)
	assignment(s.B, "-2 days", &s.AsgB)
	assignment(s.A, "-1 day", &s.AsgA2)
	recipient := func(asg, student, status string) {
		t.Helper()
		_, err := pool.Exec(ctx, `INSERT INTO learning_assignment_recipients (assignment_id, student_id, status) VALUES ($1,$2,$3)`, asg, student, status)
		must(err)
	}
	recipient(s.AsgA, s.S1, "assigned")
	recipient(s.AsgB, s.S1, "started")
	recipient(s.AsgA, s.S2, "excused")
	recipient(s.AsgA2, s.S2, "assigned")
	_, err := pool.Exec(ctx, `UPDATE learning_assignment_recipients SET due_at_override=now()+interval '2 days' WHERE assignment_id=$1 AND student_id=$2`, s.AsgA2, s.S2)
	must(err)
	recipient(s.AsgA, s.S4, "assigned")
	recipient(s.AsgA, s.S5, "assigned")

	// Transfer mid-window: S4 leaves A for the other teacher's class.
	_, err = pool.Exec(ctx, `DELETE FROM group_students WHERE group_id=$1 AND user_id=$2`, s.A, s.S4)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, s.C, s.S4)
	must(err)

	_, err = pool.Exec(ctx, `INSERT INTO teacher_student_support (teacher_id, student_id, institution_id, status, review_on)
		VALUES ($1,$2,$3,'supporting',current_date-3)`, s.Teacher, s.S3, s.Inst)
	must(err)

	conn, err := pool.Acquire(ctx)
	must(err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `SET session_replication_role = replica`)
	must(err)
	must(conn.QueryRow(ctx, `INSERT INTO curriculum_concepts (chapter_id, code, title, position) VALUES (gen_random_uuid(),'AT'||$1,'Equivalent fractions',1) RETURNING id`, tag).Scan(&s.Concept))
	// S2: one question, three wrong attempts. S3: two distinct questions wrong.
	_, err = conn.Exec(ctx, `INSERT INTO learning_evidence (response_id, institution_id, user_id, attempt_id, question_id, question_revision, question_version_id, concept_id, is_correct, occurred_at)
		SELECT gen_random_uuid(), $1, $2, gen_random_uuid(), '00000000-0000-0000-0000-0000000000a1', 1, gen_random_uuid(), $3, false, now()-interval '2 days' FROM generate_series(1,3)`, s.Inst, s.S2, s.Concept)
	must(err)
	_, err = conn.Exec(ctx, `INSERT INTO learning_evidence (response_id, institution_id, user_id, attempt_id, question_id, question_revision, question_version_id, concept_id, is_correct, occurred_at)
		SELECT gen_random_uuid(), $1, $2, gen_random_uuid(), gen_random_uuid(), 1, gen_random_uuid(), $3, false, now()-interval '1 day' FROM generate_series(1,2)`, s.Inst, s.S3, s.Concept)
	must(err)
	_, err = conn.Exec(ctx, `RESET session_replication_role`)
	must(err)

	// Quiz responses, one attempt each: S2 wrong ×3 on one question; S1 wrong, wrong, right.
	var q1, q2 string
	question := func(pos int, dest *string) {
		t.Helper()
		must(pool.QueryRow(ctx, `INSERT INTO questions (quiz_id, position, type, prompt, correct_answer)
			VALUES ($1,$2,'multiple_choice','Which fraction equals 1/2?','"2/4"') RETURNING id`, quiz, pos).Scan(dest))
	}
	question(1, &q1)
	question(2, &q2)
	answer := func(student, q string, correct bool, ago string) {
		t.Helper()
		var attempt string
		must(pool.QueryRow(ctx, `INSERT INTO quiz_attempts (quiz_id, user_id, status, started_at, completed_at)
			VALUES ($1,$2,'completed',now()-$3::interval,now()-$3::interval) RETURNING id`, quiz, student, ago).Scan(&attempt))
		_, err := pool.Exec(ctx, `INSERT INTO question_responses (attempt_id, question_id, answer, is_correct, submitted_at)
			VALUES ($1,$2,'"1/3"',$3,now()-$4::interval)`, attempt, q, correct, ago)
		must(err)
	}
	answer(s.S2, q1, false, "3 days")
	answer(s.S2, q1, false, "2 days")
	answer(s.S2, q1, false, "1 day")
	answer(s.S1, q2, false, "3 days")
	answer(s.S1, q2, false, "2 days")
	answer(s.S1, q2, true, "1 day")

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM learning_evidence WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM teacher_student_support WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM learning_assignments WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN (SELECT id FROM quizzes WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM curriculum_concepts WHERE id=$1`, s.Concept)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []string{s.Teacher, s.Other, s.Loner, s.S1, s.S2, s.S3, s.S4, s.S5})
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, s.Inst)
	})
	return s
}

type attentionBody struct {
	Data struct {
		Scope struct {
			Kind    string  `json:"kind"`
			ClassID *string `json:"class_id"`
			State   string  `json:"state"`
			Reason  string  `json:"reason"`
		} `json:"scope"`
		To                string             `json:"to"`
		Timezone          string             `json:"timezone"`
		DefinitionVersion string             `json:"definition_version"`
		Totals            AttentionTotals    `json:"totals"`
		Students          []AttentionStudent `json:"students"`
	} `json:"data"`
	Meta struct{ Page, Limit, Total int } `json:"meta"`
}

func getAttention(t *testing.T, pool *pgxpool.Pool, teacherID, instID, query string) (int, attentionBody) {
	t.Helper()
	req := teacherRequest(httptest.NewRequest("GET", "/teacher/attention"+query, nil), teacherID, instID)
	w := httptest.NewRecorder()
	NewHandler(pool).Attention(w, req)
	var body attentionBody
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%v: %s", err, w.Body)
		}
	}
	return w.Code, body
}

func reasonKinds(st AttentionStudent) []string {
	out := []string{}
	for _, r := range st.Reasons {
		out = append(out, r.Kind)
	}
	return out
}

func TestAttentionFixtures(t *testing.T) {
	pool := openTestDB(t)
	s := seedAttention(t, pool)

	code, body := getAttention(t, pool, s.Teacher, s.Inst, "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	d := body.Data
	want := AttentionTotals{EligibleStudents: 3, StudentsNeedingAttention: 3, SupportReviewsOverdue: 1,
		StudentsMissingWork: 1, OverdueSubmissions: 2, StudentsNeedingSupport: 1,
		StudentsRepeatingErrors: 1, RepeatedWrongQuestions: 1}
	if d.Totals != want {
		t.Errorf("totals = %+v, want %+v", d.Totals, want)
	}
	if d.Scope.State != "ok" || d.Timezone != "Asia/Kolkata" || d.DefinitionVersion == "" || d.To == "" {
		t.Errorf("envelope = %+v", d)
	}
	if len(d.Students) != 3 || body.Meta.Total != 3 {
		t.Fatalf("students = %d, meta.total = %d, want 3/3", len(d.Students), body.Meta.Total)
	}
	// Overdue support review ranks first; one row per student lists every reason.
	first, second, third := d.Students[0], d.Students[1], d.Students[2]
	if first.StudentID != s.S3 || fmt.Sprint(reasonKinds(first)) != "[support_review_overdue needs_support]" {
		t.Errorf("first = %s %v, want S3 [support_review_overdue needs_support]", first.StudentName, reasonKinds(first))
	}
	if second.StudentID != s.S1 || fmt.Sprint(reasonKinds(second)) != "[overdue_work]" {
		t.Errorf("second = %s %v, want S1 [overdue_work]", second.StudentName, reasonKinds(second))
	}
	if len(second.Classes) != 2 || len(second.Reasons) == 0 || len(second.Reasons[0].Items) != 2 {
		t.Errorf("S1 classes = %d, overdue items = %+v; want 2 classes, 2 items", len(second.Classes), second.Reasons)
	}
	if third.StudentID != s.S2 || fmt.Sprint(reasonKinds(third)) != "[repeated_wrong]" {
		t.Errorf("third = %s %v, want S2 [repeated_wrong]", third.StudentName, reasonKinds(third))
	} else if qs := third.Reasons[0].Questions; len(qs) != 1 || qs[0].WrongAttempts != 3 || qs[0].Attempts != 3 || qs[0].LatestAttemptID == "" || qs[0].Prompt == "" {
		t.Errorf("S2 repeated questions = %+v, want one question wrong in 3 of 3 attempts", qs)
	}

	// Pagination: totals do not move, the page does.
	_, page := getAttention(t, pool, s.Teacher, s.Inst, "?limit=1&page=2")
	if len(page.Data.Students) != 1 || page.Data.Students[0].StudentID != s.S1 || page.Meta.Total != 3 || page.Data.Totals != want {
		t.Errorf("page 2 = %+v meta %+v", page.Data.Students, page.Meta)
	}

	// A class filter narrows; it never widens.
	_, onlyB := getAttention(t, pool, s.Teacher, s.Inst, "?class_id="+s.B)
	if onlyB.Data.Totals.EligibleStudents != 1 || onlyB.Data.Totals.OverdueSubmissions != 1 || len(onlyB.Data.Students) != 1 {
		t.Errorf("class B = %+v", onlyB.Data.Totals)
	}
	if code, _ := getAttention(t, pool, s.Teacher, s.Inst, "?class_id="+s.C); code != http.StatusNotFound {
		t.Errorf("another teacher's class: status %d, want 404", code)
	}

	// Teacher with no classes: explicit state, no student-identifiable rows.
	_, loner := getAttention(t, pool, s.Loner, s.Inst, "")
	if loner.Data.Scope.State != "no_assigned_classes" || loner.Data.Scope.Reason == "" || len(loner.Data.Students) != 0 || loner.Data.Totals != (AttentionTotals{}) {
		t.Errorf("loner = %+v", loner.Data)
	}
}
