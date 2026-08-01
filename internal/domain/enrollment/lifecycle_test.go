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

	var code string
	if err := pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, full_name, claim_code, status)
		VALUES ($1, 'Transferred In', 'XFER-'||$2, 'pending_claim')
		RETURNING claim_code`, f.OtherInstitutionID, f.StudentEnrollmentID).Scan(&code); err != nil {
		t.Fatalf("seed target enrollment: %v", err)
	}

	if _, err := svc.Claim(ctx, f.StudentID, code); err != nil {
		t.Fatalf("Claim after transfer out: %v", err)
	}
}

// Suspension has to reach users.status, which is what actually blocks login.
func TestSuspendMirrorsUserStatus(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "suspended"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	var userStatus string
	pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, f.StudentID).Scan(&userStatus)
	if userStatus != "suspended" {
		t.Fatalf("users.status = %q, want suspended", userStatus)
	}

	if err := svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "active"); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	pool.QueryRow(ctx, `SELECT status FROM users WHERE id=$1`, f.StudentID).Scan(&userStatus)
	if userStatus != "active" {
		t.Fatalf("users.status = %q, want active after reactivation", userStatus)
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

func TestPromoteAdvancesMatchingEnrollmentsOnly(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	// Fixture: StudentEnrollmentID is grade 9 section A; the unclaimed row is 9/B.
	n, err := svc.Promote(ctx, f.InstitutionID, PromoteFilter{
		FromGrade: "9", FromSection: "A", ToGrade: "10", ToSection: "A",
	})
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if n != 1 {
		t.Fatalf("promoted %d rows, want 1", n)
	}

	var grade string
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&grade)
	if grade != "10" {
		t.Fatalf("grade = %q, want 10", grade)
	}
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, f.UnclaimedEnrollmentID).Scan(&grade)
	if grade != "9" {
		t.Fatalf("section B was promoted too: grade = %q, want 9", grade)
	}
}
