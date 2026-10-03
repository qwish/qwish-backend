package enrollment

import (
	"context"
	"testing"
)

// Graduating ends the enrollment and returns the student to institution-less
// status, keeping their account and history.
func TestGraduateEndsEnrollmentAndClearsInstitution(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "graduated"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	var status string
	var endedAt *string
	pool.QueryRow(ctx, `SELECT status, ended_at::text FROM enrollments WHERE id=$1`,
		f.StudentEnrollmentID).Scan(&status, &endedAt)
	if status != "graduated" || endedAt == nil {
		t.Fatalf("status=%q ended_at=%v, want graduated with an end date", status, endedAt)
	}

	var instID *string
	pool.QueryRow(ctx, `SELECT institution_id FROM users WHERE id=$1`, f.StudentID).Scan(&instID)
	if instID != nil {
		t.Fatalf("users.institution_id = %v, want NULL after graduation", instID)
	}
}

// After transferring out, the student can claim a new institution's code — the
// one-active-enrollment index no longer blocks them.
func TestGraduatedStudentCanEnrollElsewhere(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "transferred"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	_, code := newClass(t, pool, f.OtherInstitutionID, true)
	if _, err := svc.JoinByClassCode(ctx, f.StudentID, code); err != nil {
		t.Fatalf("join after transfer out: %v", err)
	}
}

func TestSetStatusIsInstitutionScoped(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	err := svc.SetStatus(context.Background(), f.OtherInstitutionID, f.StudentEnrollmentID, "suspended")
	if err == nil {
		t.Fatal("expected another institution's status change to fail")
	}
}

func TestSuspensionIsPerInstitute(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	g2, c2 := newClass(t, pool, f.OtherInstitutionID, true)
	if _, err := svc.ConfirmJoin(ctx, f.StudentID, c2, g2); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "suspended"); err != nil {
		t.Fatal(err)
	}
	var userStatus string
	pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, f.StudentID).Scan(&userStatus)
	if userStatus != "active" {
		t.Fatalf("suspension at one institute must not lock the account, got %q", userStatus)
	}
	// A suspended enrollment can't be the active institute: its content is
	// paused while the other institute keeps working.
	if err := svc.SetActive(ctx, f.StudentID, f.InstitutionID); err == nil {
		t.Fatal("switching to a suspended institute must be refused")
	}
	var active string
	pool.QueryRow(ctx, `SELECT institution_id::text FROM users WHERE id=$1`, f.StudentID).Scan(&active)
	if active != f.OtherInstitutionID {
		t.Fatalf("active institute = %s, want the non-suspended one", active)
	}
	list, err := svc.ListMine(ctx, f.StudentID)
	if err != nil || len(list) != 2 {
		t.Fatalf("both enrollments stay listed: %+v %v", list, err)
	}
}
