package enrollment

import (
	"context"
	"testing"
)

func TestActiveByUserReturnsNilForSoloStudent(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	got, err := svc.ActiveByUser(context.Background(), f.SoloStudentID)
	if err != nil {
		t.Fatalf("ActiveByUser: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil for a student with no enrollment", got)
	}
}

func TestSetJoiningScopedToInstitute(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	if err := svc.SetJoining(ctx, f.OtherInstitutionID, f.GroupID, false); err != ErrNotFound {
		t.Fatalf("another institute's class: want ErrNotFound, got %v", err)
	}
	if err := svc.SetJoining(ctx, f.InstitutionID, f.GroupID, false); err != nil {
		t.Fatal(err)
	}
	var on bool
	pool.QueryRow(ctx, `SELECT joining_enabled FROM groups WHERE id=$1`, f.GroupID).Scan(&on)
	if on {
		t.Fatal("joining should be off")
	}
}

func TestInstitutionStatsExcludeAttemptsBeforeJoining(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()

	var quizID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO quizzes (institution_id, created_by, title, type, status)
		VALUES ($1, $2, 'Scope Quiz', 'knowledge_check', 'published')
		RETURNING id`, f.InstitutionID, f.TeacherID).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, quizID)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quizID)
	})

	// One attempt before the student joined, one after.
	for _, offset := range []string{"-10 days", "-1 day"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, completed_at)
			VALUES ($1, $2, 'completed', 80, now() + $3::interval)`,
			quizID, f.StudentID, offset); err != nil {
			t.Fatalf("seed attempt %s: %v", offset, err)
		}
	}
	if _, err := pool.Exec(ctx,
		`UPDATE enrollments SET joined_at = now() - interval '5 days' WHERE id=$1`,
		f.StudentEnrollmentID); err != nil {
		t.Fatalf("backdate joined_at: %v", err)
	}

	var counted int
	pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM quiz_attempts qa
		  JOIN enrollments e ON e.user_id = qa.user_id
		 WHERE e.id=$1 AND qa.status='completed'
		   AND qa.completed_at >= COALESCE(e.joined_at, '-infinity'::timestamptz)`,
		f.StudentEnrollmentID).Scan(&counted)
	if counted != 1 {
		t.Fatalf("counted %d attempts, want 1 — attempts predating joined_at leaked in", counted)
	}
}
