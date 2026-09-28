package learning

import (
	"encoding/json"
	"testing"
	"time"
)

func decodeData(t *testing.T, raw []byte, v any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if err := json.Unmarshal(env.Data, v); err != nil {
		t.Fatalf("decode data %s: %v", env.Data, err)
	}
}

func TestAssignmentRecipientsRoadmap(t *testing.T) {
	db := insightTestDB(t, "")
	f := newInsightFixture(t, db)
	other := f.id(`INSERT INTO users(supabase_uid,full_name,display_name,email,role,institution_id) VALUES(gen_random_uuid(),'Zed','Zed',gen_random_uuid()||'@s.test','student',$1) RETURNING id`, f.inst)
	f.exec(`INSERT INTO group_students(group_id,user_id) VALUES($1,$2)`, f.group, other)
	outsider := f.id(`INSERT INTO users(supabase_uid,full_name,display_name,email,role,institution_id) VALUES(gen_random_uuid(),'Out','Out',gen_random_uuid()||'@s.test','student',$1) RETURNING id`, f.inst)
	quizID, _ := f.quiz(2)
	f.exec(`UPDATE quizzes SET status='published',published_at=now() WHERE id=$1`, quizID)

	// R4 — subset: an id outside the class is refused; a valid subset creates one recipient.
	w := f.call(f.h.CreateAssignment, "/", map[string]any{"group_id": f.group, "quiz_id": quizID, "purpose": "follow_up", "student_ids": []string{f.student, outsider}}, nil)
	if w.Code != 400 {
		t.Fatalf("outsider subset: %d %s", w.Code, w.Body)
	}
	opens := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	w = f.call(f.h.CreateAssignment, "/", map[string]any{"group_id": f.group, "quiz_id": quizID, "purpose": "follow_up", "student_ids": []string{f.student}, "available_at": opens}, nil)
	if w.Code != 201 {
		t.Fatalf("subset create: %d %s", w.Code, w.Body)
	}
	var created struct{ ID string }
	decodeData(t, w.Body.Bytes(), &created)
	var n int
	_ = db.QueryRow(t.Context(), `SELECT COUNT(*) FROM learning_assignment_recipients WHERE assignment_id=$1`, created.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("subset recipients = %d", n)
	}

	// R13 — list returns available_at; reschedule validates order.
	w = f.call(f.h.ListAssignments, "/?group_id="+f.group, nil, nil)
	var list []TeacherAssignment
	decodeData(t, w.Body.Bytes(), &list)
	if len(list) != 1 || list[0].AvailableAt == nil {
		t.Fatalf("list available_at: %+v", list)
	}
	w = f.call(f.h.UpdateAssignment, "/", map[string]any{"due_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, map[string]string{"assignmentId": created.ID})
	if w.Code != 400 {
		t.Fatalf("due before open must fail: %d", w.Code)
	}
	w = f.call(f.h.UpdateAssignment, "/", map[string]any{"available_at": nil, "due_at": time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}, map[string]string{"assignmentId": created.ID})
	if w.Code != 400 {
		t.Fatalf("past due must fail: %d", w.Code)
	}
	// Make it open and already overdue directly, to exercise R2/R3.
	f.exec(`UPDATE learning_assignments SET available_at=NULL, due_at=now()-interval '1 hour' WHERE id=$1`, created.ID)

	// R2 — recipients show overdue; remind is rate limited to once an hour.
	w = f.call(f.h.ListRecipients, "/", nil, map[string]string{"assignmentId": created.ID})
	var rec struct {
		Recipients []Recipient `json:"recipients"`
	}
	decodeData(t, w.Body.Bytes(), &rec)
	if len(rec.Recipients) != 1 || rec.Recipients[0].Status != "overdue" {
		t.Fatalf("recipients: %+v", rec)
	}
	w = f.call(f.h.RemindRecipients, "/", map[string]any{"student_ids": []string{f.student}}, map[string]string{"assignmentId": created.ID})
	var sent struct{ Sent int }
	decodeData(t, w.Body.Bytes(), &sent)
	if w.Code != 200 || sent.Sent != 1 {
		t.Fatalf("remind: %d %s", w.Code, w.Body)
	}
	if w = f.call(f.h.RemindRecipients, "/", map[string]any{"student_ids": []string{f.student}}, map[string]string{"assignmentId": created.ID}); w.Code != 429 {
		t.Fatalf("second remind within the hour: %d", w.Code)
	}

	// R3 — extension clears overdue for that student; excuse removes from denominator.
	w = f.call(f.h.UpdateRecipients, "/", map[string]any{"student_ids": []string{f.student}, "action": "extend", "due_at": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339), "note": "absent"}, map[string]string{"assignmentId": created.ID})
	if w.Code != 200 {
		t.Fatalf("extend: %d %s", w.Code, w.Body)
	}
	w = f.call(f.h.ListAssignments, "/?group_id="+f.group, nil, nil)
	decodeData(t, w.Body.Bytes(), &list)
	if list[0].OverdueCount != 0 {
		t.Fatalf("extended student still overdue: %+v", list[0])
	}
	f.call(f.h.UpdateRecipients, "/", map[string]any{"student_ids": []string{f.student}, "action": "excuse"}, map[string]string{"assignmentId": created.ID})
	w = f.call(f.h.ListAssignments, "/?group_id="+f.group, nil, nil)
	decodeData(t, w.Body.Bytes(), &list)
	if list[0].ExcusedCount != 1 {
		t.Fatalf("excused count: %+v", list[0])
	}
	f.call(f.h.UpdateRecipients, "/", map[string]any{"student_ids": []string{f.student}, "action": "unexcuse"}, map[string]string{"assignmentId": created.ID})
	var status string
	_ = db.QueryRow(t.Context(), `SELECT status FROM learning_assignment_recipients WHERE assignment_id=$1`, created.ID).Scan(&status)
	if status != "assigned" {
		t.Fatalf("unexcuse status = %s", status)
	}

	// Whole-class assignment still works without student_ids.
	w = f.call(f.h.CreateAssignment, "/", map[string]any{"group_id": f.group, "quiz_id": quizID, "purpose": "practice"}, nil)
	decodeData(t, w.Body.Bytes(), &created)
	_ = db.QueryRow(t.Context(), `SELECT COUNT(*) FROM learning_assignment_recipients WHERE assignment_id=$1`, created.ID).Scan(&n)
	if n != 2 {
		t.Fatalf("whole class recipients = %d", n)
	}
}
