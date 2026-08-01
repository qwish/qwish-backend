package enrollment

import (
	"context"
	"testing"
)

// A student may hold only one live enrollment, but any number of unredeemed
// roster rows may point at them across institutions.
func TestOneActiveEnrollmentPerUser(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()

	_, err := pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		VALUES ($1, $2, 'dupe', 'active', now())`, f.OtherInstitutionID, f.StudentID)
	if err == nil {
		t.Fatal("expected a second active enrollment to violate enrollments_one_active_per_user")
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, full_name, claim_code, status)
		VALUES ($1, 'pre-provisioned', 'OTHERCODE-'||$2, 'pending_claim')`,
		f.OtherInstitutionID, f.StudentID)
	if err != nil {
		t.Fatalf("pending_claim rows must be exempt from the one-active index: %v", err)
	}
}

// Roll numbers collide only among live enrollments; ending one frees its number.
func TestRollNumberUniquePerLiveEnrollment(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()

	var roll string
	if err := pool.QueryRow(ctx,
		`SELECT roll_number FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&roll); err != nil {
		t.Fatalf("read roll_number: %v", err)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, full_name, roll_number, claim_code, status)
		VALUES ($1, 'collides', $2, 'C-'||$2, 'pending_claim')`, f.InstitutionID, roll)
	if err == nil {
		t.Fatal("expected duplicate roll_number in a live enrollment to be rejected")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE enrollments SET status='graduated', ended_at=now() WHERE id=$1`,
		f.StudentEnrollmentID); err != nil {
		t.Fatalf("graduate: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, full_name, roll_number, claim_code, status)
		VALUES ($1, 'reuses', $2, 'C2-'||$2, 'pending_claim')`, f.InstitutionID, roll)
	if err != nil {
		t.Fatalf("roll_number must be reusable after the enrollment ends: %v", err)
	}
}
