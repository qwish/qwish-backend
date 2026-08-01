package enrollment

import (
	"context"
	"errors"
	"testing"
)

func TestAddStudentToClassRequiresOwnership(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	// LonerTeacherID is assigned to no group.
	err := svc.AddStudentToClass(context.Background(), f.LonerTeacherID, f.GroupID, f.StudentID)
	if !errors.Is(err, ErrNotYourClass) {
		t.Fatalf("err = %v, want ErrNotYourClass", err)
	}
}

func TestAddAndRemoveStudentInOwnClass(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	// Give the solo student an enrollment at this institution first.
	if _, err := svc.Claim(ctx, f.SoloStudentID, f.ClaimCode); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := svc.AddStudentToClass(ctx, f.TeacherID, f.GroupID, f.SoloStudentID); err != nil {
		t.Fatalf("AddStudentToClass: %v", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM group_students WHERE group_id=$1 AND user_id=$2`,
		f.GroupID, f.SoloStudentID).Scan(&n)
	if n != 1 {
		t.Fatalf("membership rows = %d, want 1", n)
	}

	if err := svc.RemoveStudentFromClass(ctx, f.TeacherID, f.GroupID, f.SoloStudentID); err != nil {
		t.Fatalf("RemoveStudentFromClass: %v", err)
	}
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM group_students WHERE group_id=$1 AND user_id=$2`,
		f.GroupID, f.SoloStudentID).Scan(&n)
	if n != 0 {
		t.Fatalf("membership rows = %d, want 0 after removal", n)
	}
}

// A teacher cannot pull in a student who is not enrolled at their institution.
func TestAddStudentRejectsOutsider(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	err := svc.AddStudentToClass(context.Background(), f.TeacherID, f.GroupID, f.SoloStudentID)
	if err == nil {
		t.Fatal("expected adding an unenrolled student to fail")
	}
}
