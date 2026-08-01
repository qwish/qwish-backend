package enrollment

import (
	"context"
	"errors"
	"testing"
)

// Joining by class code enrolls the student with academic fields left blank
// for an admin to fill in, and adds them to the class in one step.
func TestJoinByClassCodeCreatesEnrollmentAndMembership(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var invite string
	if err := pool.QueryRow(ctx,
		`SELECT invite_code FROM groups WHERE id=$1`, f.GroupID).Scan(&invite); err != nil {
		t.Fatalf("read invite_code: %v", err)
	}

	e, err := svc.JoinByClassCode(ctx, f.SoloStudentID, invite)
	if err != nil {
		t.Fatalf("JoinByClassCode: %v", err)
	}
	if e.Status != "active" || e.RollNumber != nil {
		t.Fatalf("got status=%q roll=%v, want active with a blank roll number", e.Status, e.RollNumber)
	}

	var member int
	pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM group_students WHERE group_id=$1 AND user_id=$2`,
		f.GroupID, f.SoloStudentID).Scan(&member)
	if member != 1 {
		t.Fatalf("group_students rows = %d, want 1", member)
	}
}

func TestJoinByClassCodeRejectsAlreadyEnrolled(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	var invite string
	pool.QueryRow(ctx, `SELECT invite_code FROM groups WHERE id=$1`, f.GroupID).Scan(&invite)

	_, err := svc.JoinByClassCode(ctx, f.StudentID, invite)
	if !errors.Is(err, ErrEnrollmentExists) {
		t.Fatalf("err = %v, want ErrEnrollmentExists", err)
	}
}

func TestJoinByClassCodeRejectsUnknownCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	_, err := svc.JoinByClassCode(context.Background(), f.SoloStudentID, "NOPE")
	if !errors.Is(err, ErrClassCodeInvalid) {
		t.Fatalf("err = %v, want ErrClassCodeInvalid", err)
	}
}
