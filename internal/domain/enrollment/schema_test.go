package enrollment

import (
	"context"
	"testing"
)

// A student may hold one live enrollment per institute, at several institutes.
func TestOneLiveEnrollmentPerInstitute(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		VALUES ($1, $2, 'second', 'active', now())`, f.OtherInstitutionID, f.StudentID); err != nil {
		t.Fatalf("a live enrollment at a second institute must be allowed: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		VALUES ($1, $2, 'dupe', 'active', now())`, f.InstitutionID, f.StudentID); err == nil {
		t.Fatal("expected a second live enrollment at the same institute to be rejected")
	}
}

// The active-institute pointer survives a second join and moves off an ended one.
func TestActiveInstitutePointer(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		VALUES ($1, $2, 'second', 'active', now())`, f.OtherInstitutionID, f.StudentID); err != nil {
		t.Fatal(err)
	}
	var active string
	pool.QueryRow(ctx, `SELECT institution_id::text FROM users WHERE id=$1`, f.StudentID).Scan(&active)
	if active != f.InstitutionID {
		t.Fatalf("second enrollment must not steal the active pointer: got %s", active)
	}
	if _, err := pool.Exec(ctx, `UPDATE enrollments SET status='left', ended_at=now() WHERE id=$1`, f.StudentEnrollmentID); err != nil {
		t.Fatal(err)
	}
	pool.QueryRow(ctx, `SELECT institution_id::text FROM users WHERE id=$1`, f.StudentID).Scan(&active)
	if active != f.OtherInstitutionID {
		t.Fatalf("ending the active enrollment must re-point to the other live one: got %s", active)
	}
}
