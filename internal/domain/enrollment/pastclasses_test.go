package enrollment

import (
	"context"
	"testing"
)

func TestPastClassesAndEndedClassPrompt(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var quiz string
	if err := pool.QueryRow(ctx, `INSERT INTO quizzes (institution_id, created_by, title, type, status)
		VALUES ($1,$2,'Window quiz','knowledge_check','published') RETURNING id`, f.InstitutionID, f.TeacherID).Scan(&quiz); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, quiz)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quiz)
	})
	pool.Exec(ctx, `UPDATE group_students SET joined_at=now()-interval '20 days' WHERE group_id=$1 AND user_id=$2`, f.GroupID, f.StudentID)
	pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id, user_id, status, completed_at, total_questions, total_correct, score_pct)
		VALUES ($1,$2,'completed',now()-interval '10 days',10,7,70)`, quiz, f.StudentID)
	pool.Exec(ctx, `UPDATE groups SET archived_at=now()-interval '5 days' WHERE id=$1`, f.GroupID)

	past, err := svc.PastClasses(ctx, f.StudentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 || past[0].ClassName != "Fixture Class" || past[0].Assessments != 1 || past[0].Correct != 7 {
		t.Fatalf("past classes = %+v", past)
	}
	concepts, err := svc.PastClassConcepts(ctx, f.StudentID, f.GroupID)
	if err != nil || concepts == nil {
		t.Fatalf("concepts: %v %v", concepts, err)
	}

	if other, err := svc.PastClassConcepts(ctx, f.SoloStudentID, f.GroupID); err != nil || len(other) != 0 {
		t.Fatalf("a student never in the class must get nothing: %v %v", other, err)
	}

	e, err := svc.ActiveByUser(ctx, f.StudentID)
	if err != nil || e == nil || e.EndedClassName == nil || *e.EndedClassName != "Fixture Class" || e.ClassName != nil {
		t.Fatalf("ended-class prompt: %+v %v", e, err)
	}
}
