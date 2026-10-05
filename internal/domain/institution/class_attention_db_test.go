package institution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/teacher"
)

// classAttentionSeed, expected for the institution:
//
//	A (teacher T1): S1 overdue, S2 needs support, S6 suspended + overdue → flagged (work, learning)
//	B (teacher T2): S1, S3 overdue support review, S3 assessed recently    → flagged (review), listed first
//	C (teacher T2): S4 assessed recently, S5 never assessed                → not flagged
//	D archived                                                            → not counted
//	Eligible students are distinct: S1..S5 = 5, not 2+2+2 = 6. Coverage 2/5.
//	Pending approvals: Q1 stamped, Q2 pending before the migration (age unavailable).
type classAttentionSeed struct {
	Inst, Admin, T1, T2    string
	A, B, C, D             string
	S1, S2, S3, S4, S5, S6 string
	Q1, Q2                 string
}

func seedClassAttention(t *testing.T, pool *pgxpool.Pool) classAttentionSeed {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var s classAttentionSeed
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('CA '||$1,'school','ca-'||$1||'@example.test','CS'||$1,'CT'||$1,'verified') RETURNING id`, tag).Scan(&s.Inst))
	user := func(role, label string, dest *string) {
		t.Helper()
		must(pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $1||'-'||$2||'@example.test', $3, $4) RETURNING id`, label, tag, role, s.Inst).Scan(dest))
	}
	user("institution_admin", "admin", &s.Admin)
	user("teacher", "Teacher One", &s.T1)
	user("teacher", "Teacher Two", &s.T2)
	for i, d := range []*string{&s.S1, &s.S2, &s.S3, &s.S4, &s.S5, &s.S6} {
		user("student", fmt.Sprintf("S%d", i+1), d)
		status := "active"
		if d == &s.S6 {
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
	class("A", s.T1, &s.A, s.S1, s.S2, s.S6)
	class("B", s.T2, &s.B, s.S1, s.S3)
	class("C", s.T2, &s.C, s.S4, s.S5)
	class("D", s.T1, &s.D, s.S5)
	_, err := pool.Exec(ctx, `UPDATE groups SET archived_at=now() WHERE id=$1`, s.D)
	must(err)

	var quiz, asg string
	must(pool.QueryRow(ctx, `INSERT INTO quizzes (institution_id, created_by, title, type, status) VALUES ($1,$2,'Fractions','knowledge_check','published') RETURNING id`, s.Inst, s.T1).Scan(&quiz))
	must(pool.QueryRow(ctx, `INSERT INTO learning_assignments (institution_id, group_id, quiz_id, purpose, due_at, created_by)
		VALUES ($1,$2,$3,'practice',now()-interval '1 day',$4) RETURNING id`, s.Inst, s.A, quiz, s.T1).Scan(&asg))
	_, err = pool.Exec(ctx, `INSERT INTO learning_assignment_recipients (assignment_id, student_id, status) VALUES ($1,$2,'assigned'),($1,$3,'assigned')`, asg, s.S1, s.S6)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO teacher_student_support (teacher_id, student_id, institution_id, status, review_on) VALUES ($1,$2,$3,'supporting',current_date-3)`, s.T2, s.S3, s.Inst)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id, user_id, status, completed_at) VALUES ($1,$2,'completed',now()-interval '10 days'),($1,$3,'completed',now()-interval '2 days'),($1,$4,'completed',now()-interval '200 days')`, quiz, s.S3, s.S4, s.S5)
	must(err)

	must(pool.QueryRow(ctx, `INSERT INTO quizzes (institution_id, created_by, title, type, status) VALUES ($1,$2,'Pending stamped','knowledge_check','pending_approval') RETURNING id`, s.Inst, s.T1).Scan(&s.Q1))
	must(pool.QueryRow(ctx, `INSERT INTO quizzes (institution_id, created_by, title, type, status) VALUES ($1,$2,'Pending legacy','knowledge_check','pending_approval') RETURNING id`, s.Inst, s.T2).Scan(&s.Q2))
	_, err = pool.Exec(ctx, `UPDATE quizzes SET submitted_for_approval_at=NULL WHERE id=$1`, s.Q2)
	must(err)
	_, err = pool.Exec(ctx, `UPDATE quizzes SET submitted_for_approval_at=now()-interval '4 days' WHERE id=$1`, s.Q1)
	must(err)

	conn, err := pool.Acquire(ctx)
	must(err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `SET session_replication_role = replica`)
	must(err)
	var concept string
	must(conn.QueryRow(ctx, `INSERT INTO curriculum_concepts (chapter_id, code, title, position) VALUES (gen_random_uuid(),'CA'||$1,'Ratios',1) RETURNING id`, tag).Scan(&concept))
	_, err = conn.Exec(ctx, `INSERT INTO learning_evidence (response_id, institution_id, user_id, attempt_id, question_id, question_revision, question_version_id, concept_id, is_correct, occurred_at)
		SELECT gen_random_uuid(), $1, $2, gen_random_uuid(), gen_random_uuid(), 1, gen_random_uuid(), $3, false, now()-interval '1 day' FROM generate_series(1,2)`, s.Inst, s.S2, concept)
	must(err)
	_, err = conn.Exec(ctx, `RESET session_replication_role`)
	must(err)

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM learning_evidence WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM curriculum_concepts WHERE id=$1`, concept)
		pool.Exec(ctx, `DELETE FROM teacher_student_support WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM learning_assignments WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN (SELECT id FROM quizzes WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`, s.Inst)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, s.Inst)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []string{s.Admin, s.T1, s.T2, s.S1, s.S2, s.S3, s.S4, s.S5, s.S6})
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, s.Inst)
	})
	return s
}

type classAttentionBody struct {
	Data ClassAttention                   `json:"data"`
	Meta struct{ Page, Limit, Total int } `json:"meta"`
}

func getClassAttention(t *testing.T, pool *pgxpool.Pool, adminID, instID, query string) (int, classAttentionBody) {
	t.Helper()
	req := withAuth(httptest.NewRequest("GET", "/institution/classes/attention"+query, nil), adminID, "institution_admin", instID)
	w := httptest.NewRecorder()
	NewHandler(pool, nil, nil, "", "").ClassAttention(w, req)
	var body classAttentionBody
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%v: %s", err, w.Body)
		}
	}
	return w.Code, body
}

func TestClassAttentionFixtures(t *testing.T) {
	pool := openTestDB(t)
	s := seedClassAttention(t, pool)

	code, body := getClassAttention(t, pool, s.Admin, s.Inst, "")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	d := body.Data
	tot := d.Totals
	if tot.ActiveClasses != 3 || tot.ClassesNeedingAttention != 2 {
		t.Errorf("classes = %d active, %d flagged; want 3, 2", tot.ActiveClasses, tot.ClassesNeedingAttention)
	}
	if tot.Coverage.Numerator != 2 || tot.Coverage.Denominator != 5 || tot.Coverage.State != "ok" {
		t.Errorf("coverage = %+v, want 2/5 distinct students", tot.Coverage)
	}
	if tot.SupportReviewsOverdue != 1 || tot.StudentsMissingWork != 1 || tot.OverdueSubmissions != 1 || tot.StudentsNeedingSupport != 1 {
		t.Errorf("totals = %+v", tot)
	}
	if tot.PendingApprovals.Count != 2 || tot.PendingApprovals.AgeUnavailable != 1 || tot.PendingApprovals.OldestSubmittedAt == nil {
		t.Errorf("pending approvals = %+v", tot.PendingApprovals)
	}
	if d.From == "" || d.To == "" || d.Timezone != "Asia/Kolkata" || d.DefinitionVersion == "" {
		t.Errorf("envelope from=%q to=%q tz=%q", d.From, d.To, d.Timezone)
	}

	if len(d.Classes) != 2 || body.Meta.Total != 2 {
		t.Fatalf("classes = %d, meta.total = %d; want 2/2", len(d.Classes), body.Meta.Total)
	}
	b, a := d.Classes[0], d.Classes[1]
	if b.ClassID != s.B || fmt.Sprint(b.Reasons) != "[support_review_overdue]" || b.Eligible != 2 || b.Assessed != 1 {
		t.Errorf("first = %+v, want B [support_review_overdue], 1/2 assessed", b)
	}
	if len(b.Teachers) != 1 || b.Teachers[0].Name != "Teacher Two" {
		t.Errorf("B teachers = %+v", b.Teachers)
	}
	if a.ClassID != s.A || fmt.Sprint(a.Reasons) != "[overdue_work needs_support]" || a.Eligible != 2 || a.StudentsMissingWork != 1 {
		t.Errorf("second = %+v, want A [overdue_work needs_support], 2 eligible (suspended excluded)", a)
	}

	if len(d.Approvals) != 2 || d.Approvals[0].QuizID != s.Q1 || d.Approvals[1].SubmittedAt != nil {
		t.Errorf("approvals = %+v, want stamped first, legacy last with no age", d.Approvals)
	}
	if len(d.SupportReviews) != 1 || d.SupportReviews[0].StudentID != s.S3 || d.SupportReviews[0].TeacherName != "Teacher Two" {
		t.Errorf("support reviews = %+v", d.SupportReviews)
	}

	_, page := getClassAttention(t, pool, s.Admin, s.Inst, "?limit=1&page=2")
	if len(page.Data.Classes) != 1 || page.Data.Classes[0].ClassID != s.A || page.Meta.Total != 2 || page.Data.Totals.Coverage.Numerator != tot.Coverage.Numerator {
		t.Errorf("page 2 = %+v meta %+v", page.Data.Classes, page.Meta)
	}
	// The window decides coverage: S3's 10-day-old attempt falls outside 7 days.
	_, week := getClassAttention(t, pool, s.Admin, s.Inst, "?days=7")
	if week.Data.Totals.Coverage.Numerator != 1 {
		t.Errorf("7-day coverage = %+v", week.Data.Totals.Coverage)
	}
	if code, _ := getClassAttention(t, pool, s.Admin, s.Inst, "?days=12"); code != http.StatusBadRequest {
		t.Errorf("days=12: status %d, want 400", code)
	}

	// Another institution sees none of this.
	other := seedTwoInstitutes(t, pool)
	_, iso := getClassAttention(t, pool, other.AdminA, other.InstA, "")
	if iso.Data.Totals.ActiveClasses != 0 || len(iso.Data.Classes) != 0 || iso.Data.Totals.PendingApprovals.Count != 0 {
		t.Errorf("other institution sees %+v", iso.Data.Totals)
	}
}

// The class drill-down uses the teacher queue's definitions with admin scope:
// any teacher's support plan counts, and only this institution's classes open.
func TestClassStudentsAttention(t *testing.T) {
	pool := openTestDB(t)
	s := seedClassAttention(t, pool)
	h := NewHandler(pool, nil, nil, "", "")
	get := func(instID, classID string) (int, struct {
		Data struct {
			Scope    map[string]any             `json:"scope"`
			Teachers []ClassTeacher             `json:"teachers"`
			Totals   teacher.AttentionTotals    `json:"totals"`
			Students []teacher.AttentionStudent `json:"students"`
		} `json:"data"`
	}) {
		req := withURLParam(withAuth(httptest.NewRequest("GET", "/", nil), s.Admin, "institution_admin", instID), "classId", classID)
		w := httptest.NewRecorder()
		h.ClassStudentsAttention(w, req)
		var body struct {
			Data struct {
				Scope    map[string]any             `json:"scope"`
				Teachers []ClassTeacher             `json:"teachers"`
				Totals   teacher.AttentionTotals    `json:"totals"`
				Students []teacher.AttentionStudent `json:"students"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}

	_, a := get(s.Inst, s.A)
	if a.Data.Totals.EligibleStudents != 2 || len(a.Data.Students) != 2 || a.Data.Students[0].StudentID != s.S1 || a.Data.Students[1].StudentID != s.S2 {
		t.Errorf("class A = %+v", a.Data)
	}
	if len(a.Data.Teachers) != 1 || a.Data.Teachers[0].Name != "Teacher One" {
		t.Errorf("class A teachers = %+v", a.Data.Teachers)
	}
	_, b := get(s.Inst, s.B)
	if len(b.Data.Students) != 1 || b.Data.Students[0].StudentID != s.S3 || b.Data.Students[0].Reasons[0].Kind != "support_review_overdue" || b.Data.Students[0].Reasons[0].TeacherName != "Teacher Two" {
		t.Errorf("class B = %+v (another teacher's plan must count for an admin)", b.Data.Students)
	}
	if code, _ := get(s.Inst, s.D); code != http.StatusNotFound {
		t.Errorf("archived class: %d, want 404", code)
	}
	other := seedTwoInstitutes(t, pool)
	if code, _ := get(other.InstA, s.A); code != http.StatusNotFound {
		t.Errorf("another institution's class: %d, want 404", code)
	}
}
