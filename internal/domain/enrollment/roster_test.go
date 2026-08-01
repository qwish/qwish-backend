package enrollment

import (
	"context"
	"errors"
	"testing"
)

func TestCreateRosterEntryGeneratesClaimCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	e, err := svc.CreateRosterEntry(context.Background(), f.InstitutionID, RosterInput{
		FullName: "New Student", RollNumber: "NEW-1", Grade: "10", Section: "C",
	})
	if err != nil {
		t.Fatalf("CreateRosterEntry: %v", err)
	}
	if e.Status != "pending_claim" {
		t.Fatalf("status = %q, want pending_claim", e.Status)
	}
	if e.ClaimCode == nil || len(*e.ClaimCode) != 10 {
		t.Fatalf("claim_code = %v, want a 10-character code", e.ClaimCode)
	}
	if e.UserID != nil {
		t.Fatalf("user_id = %v, want nil before the row is claimed", e.UserID)
	}
}

func TestCreateRosterEntryRejectsDuplicateRollNumber(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var roll string
	pool.QueryRow(ctx, `SELECT roll_number FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&roll)

	_, err := svc.CreateRosterEntry(ctx, f.InstitutionID, RosterInput{FullName: "Dupe", RollNumber: roll})
	if !errors.Is(err, ErrRollNumberTaken) {
		t.Fatalf("err = %v, want ErrRollNumberTaken", err)
	}
}

// A transferred-in student's previous school's attempts must not count toward
// the new institution's numbers. This is the query shape the roster list uses.
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

// An institution may only edit its own roster rows.
func TestUpdateRosterEntryIsInstitutionScoped(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	err := svc.UpdateRosterEntry(context.Background(), f.OtherInstitutionID, f.StudentEnrollmentID,
		RosterInput{FullName: "Hijacked", Grade: "12"})
	if err == nil {
		t.Fatal("expected an update from another institution to fail")
	}
}
