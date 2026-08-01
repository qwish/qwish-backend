package enrollment

import (
	"context"
	"errors"
	"testing"
)

func TestClaimAssignsEnrollmentAndInstitution(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	e, err := svc.Claim(ctx, f.SoloStudentID, f.ClaimCode)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if e.Status != "active" {
		t.Fatalf("status = %q, want active", e.Status)
	}
	if e.UserID == nil || *e.UserID != f.SoloStudentID {
		t.Fatalf("user_id = %v, want %s", e.UserID, f.SoloStudentID)
	}

	var instID *string
	pool.QueryRow(ctx, `SELECT institution_id FROM users WHERE id=$1`, f.SoloStudentID).Scan(&instID)
	if instID == nil || *instID != f.InstitutionID {
		t.Fatalf("users.institution_id = %v, want %s", instID, f.InstitutionID)
	}
}

func TestClaimRejectsUsedCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	if _, err := svc.Claim(ctx, f.SoloStudentID, f.ClaimCode); err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	_, err := svc.Claim(ctx, f.TeacherID, f.ClaimCode)
	if !errors.Is(err, ErrClaimCodeUsed) {
		t.Fatalf("err = %v, want ErrClaimCodeUsed", err)
	}
}

func TestClaimRejectsAlreadyEnrolledStudent(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	// f.StudentID already holds an active enrollment from the fixture.
	_, err := svc.Claim(context.Background(), f.StudentID, f.ClaimCode)
	if !errors.Is(err, ErrEnrollmentExists) {
		t.Fatalf("err = %v, want ErrEnrollmentExists", err)
	}
}

func TestClaimRejectsUnknownCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	_, err := svc.Claim(context.Background(), f.SoloStudentID, "NO-SUCH-CODE")
	if !errors.Is(err, ErrClaimCodeInvalid) {
		t.Fatalf("err = %v, want ErrClaimCodeInvalid", err)
	}
}

// Import-supplied personal values fill only the blanks the student left.
func TestClaimMergesImportValuesWithoutOverwriting(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		UPDATE enrollments SET import_phone='555-import', import_guardian_name='Import Guardian'
		WHERE id=$1`, f.UnclaimedEnrollmentID)
	if err != nil {
		t.Fatalf("stage import values: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE users SET phone='555-mine' WHERE id=$1`, f.SoloStudentID); err != nil {
		t.Fatalf("preset phone: %v", err)
	}

	if _, err := svc.Claim(ctx, f.SoloStudentID, f.ClaimCode); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var phone, guardian *string
	pool.QueryRow(ctx,
		`SELECT phone, guardian_name FROM users WHERE id=$1`, f.SoloStudentID).Scan(&phone, &guardian)
	if phone == nil || *phone != "555-mine" {
		t.Fatalf("phone = %v, want the student's own value to survive", phone)
	}
	if guardian == nil || *guardian != "Import Guardian" {
		t.Fatalf("guardian_name = %v, want the import value to fill the blank", guardian)
	}
}

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
