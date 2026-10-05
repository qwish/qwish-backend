package leadership_test

import (
	"context"
	"encoding/json"
	"testing"
)

// Department heads see the institute's decision dashboards for their granted
// departments only; a whole-institution role sees every class. CSE holds s1
// (overdue work); ECE holds s2 (overdue work, an overdue support plan by t2,
// and a learning signal).
func TestLeadershipDecisionDashboardsScope(t *testing.T) {
	w := build(t)
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	cse := id(t, w.call(t, 201, w.admin, "POST", "/i/departments", map[string]any{"name": "CSE"}))
	ece := id(t, w.call(t, 201, w.admin, "POST", "/i/departments", map[string]any{"name": "ECE"}))
	w.call(t, 200, w.admin, "PUT", "/i/groups/"+w.cseClass+"/department", map[string]any{"department_id": cse})
	w.call(t, 200, w.admin, "PUT", "/i/groups/"+w.eceClass+"/department", map[string]any{"department_id": ece})
	w.call(t, 201, w.admin, "POST", "/i/staff-roles", map[string]any{"user_id": w.hod, "role": "hod", "department_id": cse, "reason": "pilot"})
	w.call(t, 201, w.admin, "POST", "/i/staff-roles", map[string]any{"user_id": w.principal, "role": "principal", "reason": "pilot"})

	var quiz string
	must(w.pool.QueryRow(ctx, `INSERT INTO quizzes (institution_id, created_by, title, type, status) VALUES ($1,$2,'Q','knowledge_check','published') RETURNING id`, w.inst, w.t1).Scan(&quiz))
	for _, c := range []struct{ class, teacher, student string }{{w.cseClass, w.t1, w.s1}, {w.eceClass, w.t2, w.s2}} {
		var asg string
		must(w.pool.QueryRow(ctx, `INSERT INTO learning_assignments (institution_id, group_id, quiz_id, purpose, due_at, created_by) VALUES ($1,$2,$3,'practice',now()-interval '1 day',$4) RETURNING id`, w.inst, c.class, quiz, c.teacher).Scan(&asg))
		_, err := w.pool.Exec(ctx, `INSERT INTO learning_assignment_recipients (assignment_id, student_id) VALUES ($1,$2)`, asg, c.student)
		must(err)
	}
	_, err := w.pool.Exec(ctx, `INSERT INTO teacher_student_support (teacher_id, student_id, institution_id, status, review_on) VALUES ($1,$2,$3,'supporting',current_date-2)`, w.t2, w.s2, w.inst)
	must(err)
	conn, err := w.pool.Acquire(ctx)
	must(err)
	_, err = conn.Exec(ctx, `SET session_replication_role = replica`)
	must(err)
	var concept string
	must(conn.QueryRow(ctx, `INSERT INTO curriculum_concepts (chapter_id, code, title, position) VALUES (gen_random_uuid(),'LD'||gen_random_uuid(),'Signals',1) RETURNING id`).Scan(&concept))
	_, err = conn.Exec(ctx, `INSERT INTO learning_evidence (response_id, institution_id, user_id, attempt_id, question_id, question_revision, question_version_id, concept_id, is_correct, occurred_at)
		SELECT gen_random_uuid(), $1, $2, gen_random_uuid(), gen_random_uuid(), 1, gen_random_uuid(), $3, false, now()-interval '1 day' FROM generate_series(1,2)`, w.inst, w.s2, concept)
	must(err)
	conn.Exec(ctx, `RESET session_replication_role`)
	conn.Release()
	t.Cleanup(func() {
		w.pool.Exec(ctx, `DELETE FROM learning_evidence WHERE institution_id=$1`, w.inst)
		w.pool.Exec(ctx, `DELETE FROM curriculum_concepts WHERE id=$1`, concept)
		w.pool.Exec(ctx, `DELETE FROM teacher_student_support WHERE institution_id=$1`, w.inst)
		w.pool.Exec(ctx, `DELETE FROM learning_assignments WHERE institution_id=$1`, w.inst)
		w.pool.Exec(ctx, `DELETE FROM quizzes WHERE institution_id=$1`, w.inst)
	})

	type classes struct {
		Totals struct {
			ActiveClasses         int `json:"active_classes"`
			SupportReviewsOverdue int `json:"support_reviews_overdue"`
			StudentsMissingWork   int `json:"students_missing_work"`
			PendingApprovals      *struct {
				Count int `json:"count"`
			} `json:"pending_approvals"`
		} `json:"totals"`
		Classes []struct {
			ClassID string `json:"class_id"`
		} `json:"classes"`
		Approvals      json.RawMessage `json:"approvals"`
		SupportReviews []struct {
			StudentID string `json:"student_id"`
		} `json:"support_reviews"`
	}
	decode := func(raw json.RawMessage, v any) {
		t.Helper()
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("%v: %s", err, raw)
		}
	}

	var hod classes
	decode(w.call(t, 200, w.hod, "GET", "/l/classes/attention", nil), &hod)
	if hod.Totals.ActiveClasses != 1 || len(hod.Classes) != 1 || hod.Classes[0].ClassID != w.cseClass || hod.Totals.StudentsMissingWork != 1 {
		t.Errorf("HOD sees %+v, want only CSE", hod)
	}
	if hod.Totals.SupportReviewsOverdue != 0 || len(hod.SupportReviews) != 0 {
		t.Errorf("HOD sees ECE support reviews: %+v", hod.SupportReviews)
	}
	if hod.Totals.PendingApprovals != nil || string(hod.Approvals) != "null" {
		t.Errorf("department scope shows approvals: %s", hod.Approvals)
	}

	var principal classes
	decode(w.call(t, 200, w.principal, "GET", "/l/classes/attention", nil), &principal)
	if principal.Totals.ActiveClasses != 3 || len(principal.Classes) != 2 || principal.Totals.SupportReviewsOverdue != 1 {
		t.Errorf("principal sees %+v, want all three classes, two flagged", principal.Totals)
	}
	var narrowed classes
	decode(w.call(t, 200, w.principal, "GET", "/l/classes/attention?department_id="+ece, nil), &narrowed)
	if narrowed.Totals.ActiveClasses != 1 || len(narrowed.Classes) != 1 || narrowed.Classes[0].ClassID != w.eceClass {
		t.Errorf("principal narrowed to ECE sees %+v", narrowed)
	}

	// Drill-downs and narrowing never leave the grant.
	w.call(t, 200, w.hod, "GET", "/l/classes/"+w.cseClass+"/attention", nil)
	w.call(t, 404, w.hod, "GET", "/l/classes/"+w.eceClass+"/attention", nil)
	w.call(t, 404, w.hod, "GET", "/l/classes/"+w.bareClass+"/attention", nil)
	w.call(t, 403, w.hod, "GET", "/l/classes/attention?department_id="+ece, nil)
	w.call(t, 403, w.t1, "GET", "/l/classes/attention", nil)

	var prio struct {
		Concepts []struct {
			StudentsNeedingSupport int `json:"students_needing_support"`
		} `json:"concepts"`
	}
	decode(w.call(t, 200, w.hod, "GET", "/l/learning-priorities", nil), &prio)
	if len(prio.Concepts) != 0 {
		t.Errorf("HOD sees ECE learning signals: %+v", prio.Concepts)
	}
	decode(w.call(t, 200, w.principal, "GET", "/l/learning-priorities", nil), &prio)
	if len(prio.Concepts) != 1 || prio.Concepts[0].StudentsNeedingSupport != 1 {
		t.Errorf("principal priorities = %+v", prio.Concepts)
	}

	var support struct {
		Plans struct {
			Supporting int `json:"supporting"`
		} `json:"plans"`
		Workload []json.RawMessage `json:"workload"`
	}
	decode(w.call(t, 200, w.hod, "GET", "/l/support-summary", nil), &support)
	if support.Plans.Supporting != 0 || len(support.Workload) != 0 {
		t.Errorf("HOD support summary leaks ECE: %+v", support)
	}
	decode(w.call(t, 200, w.principal, "GET", "/l/support-summary", nil), &support)
	if support.Plans.Supporting != 1 || len(support.Workload) != 1 {
		t.Errorf("principal support summary = %+v", support)
	}
}
