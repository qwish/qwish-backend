package auth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// switchFixture is two institutions and one student, used to exercise joining
// and moving between them by referral code.
type switchFixture struct {
	FromInstitutionID string
	ToInstitutionID   string
	FromCode          string
	ToCode            string
	StudentID         string
}

func seedSwitchFixture(t *testing.T, pool *pgxpool.Pool) switchFixture {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var f switchFixture

	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	f.FromCode = "FROM" + tag
	f.ToCode = "TO" + tag

	newInst := func(label, code string, dest *string) {
		t.Helper()
		must(label, pool.QueryRow(ctx, `
			INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
			VALUES ($1||' '||$2, 'school', $1||'-'||$2||'@example.test', $3, 'T'||$3, 'verified')
			RETURNING id`, label, tag, code).Scan(dest))
	}
	newInst("from", f.FromCode, &f.FromInstitutionID)
	newInst("to", f.ToCode, &f.ToInstitutionID)

	must("student", pool.QueryRow(ctx, `
		INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), 'Switcher '||$1, 'Switcher', 'switch-'||$1||'@example.test', 'student')
		RETURNING id`, tag).Scan(&f.StudentID))

	t.Cleanup(func() {
		ctx := context.Background()
		insts := []string{f.FromInstitutionID, f.ToInstitutionID}
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, f.StudentID)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id = ANY($1)`, insts)
	})

	return f
}

// Joining by referral code must create an enrollment. Setting users.institution_id
// alone leaves the student invisible to their institution's roster, which is
// built from enrollments.
func TestUpdateUserInstitutionCreatesEnrollment(t *testing.T) {
	pool := openTestDB(t)
	f := seedSwitchFixture(t, pool)
	svc := &Service{db: pool}
	ctx := context.Background()

	if err := svc.UpdateUserInstitution(ctx, f.StudentID, f.FromCode); err != nil {
		t.Fatalf("UpdateUserInstitution: %v", err)
	}

	var status string
	err := pool.QueryRow(ctx,
		`SELECT status FROM enrollments WHERE user_id=$1 AND institution_id=$2`,
		f.StudentID, f.FromInstitutionID).Scan(&status)
	if err != nil {
		t.Fatalf("no enrollment created: %v", err)
	}
	if status != "active" {
		t.Fatalf("status = %q, want active", status)
	}

	var instID *string
	pool.QueryRow(ctx, `SELECT institution_id FROM users WHERE id=$1`, f.StudentID).Scan(&instID)
	if instID == nil || *instID != f.FromInstitutionID {
		t.Fatalf("users.institution_id = %v, want %s", instID, f.FromInstitutionID)
	}
}

// Moving ends the old enrollment rather than leaving two live ones, which the
// enrollments_one_active_per_user index would reject outright.
func TestUpdateUserInstitutionEndsThePreviousEnrollment(t *testing.T) {
	pool := openTestDB(t)
	f := seedSwitchFixture(t, pool)
	svc := &Service{db: pool}
	ctx := context.Background()

	if err := svc.UpdateUserInstitution(ctx, f.StudentID, f.FromCode); err != nil {
		t.Fatalf("first join: %v", err)
	}
	if err := svc.UpdateUserInstitution(ctx, f.StudentID, f.ToCode); err != nil {
		t.Fatalf("move: %v", err)
	}

	var oldStatus string
	var oldEnded *time.Time
	pool.QueryRow(ctx,
		`SELECT status, ended_at FROM enrollments WHERE user_id=$1 AND institution_id=$2`,
		f.StudentID, f.FromInstitutionID).Scan(&oldStatus, &oldEnded)
	if oldStatus != "transferred" || oldEnded == nil {
		t.Fatalf("old enrollment status=%q ended_at=%v, want transferred with an end date", oldStatus, oldEnded)
	}

	var live int
	pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM enrollments WHERE user_id=$1 AND status IN ('active','suspended')`,
		f.StudentID).Scan(&live)
	if live != 1 {
		t.Fatalf("live enrollments = %d, want exactly 1", live)
	}

	var newInst string
	pool.QueryRow(ctx,
		`SELECT institution_id FROM enrollments
		  WHERE user_id=$1 AND status='active'`, f.StudentID).Scan(&newInst)
	if newInst != f.ToInstitutionID {
		t.Fatalf("live enrollment is at %s, want %s", newInst, f.ToInstitutionID)
	}
}

// Re-entering the code for the institution the student is already at must be a
// no-op, not a transfer out and back in that litters history with a dead row.
func TestUpdateUserInstitutionIsIdempotentForTheSameInstitution(t *testing.T) {
	pool := openTestDB(t)
	f := seedSwitchFixture(t, pool)
	svc := &Service{db: pool}
	ctx := context.Background()

	if err := svc.UpdateUserInstitution(ctx, f.StudentID, f.FromCode); err != nil {
		t.Fatalf("first join: %v", err)
	}
	if err := svc.UpdateUserInstitution(ctx, f.StudentID, f.FromCode); err != nil {
		t.Fatalf("repeat join: %v", err)
	}

	var rows int
	pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM enrollments WHERE user_id=$1`, f.StudentID).Scan(&rows)
	if rows != 1 {
		t.Fatalf("enrollment rows = %d, want 1 — the repeat should not have created another", rows)
	}
}

// A teacher joining by referral code has no enrollment: enrollments are the
// student-to-institution relationship, and a teacher's is group_teachers.
func TestUpdateUserInstitutionSkipsEnrollmentForTeachers(t *testing.T) {
	pool := openTestDB(t)
	f := seedSwitchFixture(t, pool)
	ctx := context.Background()

	var teacherID string
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), 'T '||$1, 'T', 'teach-'||$1||'@example.test', 'teacher')
		RETURNING id`, tag).Scan(&teacherID); err != nil {
		t.Fatalf("seed teacher: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, teacherID)
	})

	svc := &Service{db: pool}
	// The teacher referral code for the "from" institution is 'T'+FromCode.
	if err := svc.UpdateUserInstitution(ctx, teacherID, "T"+f.FromCode); err != nil {
		t.Fatalf("teacher join: %v", err)
	}

	var rows int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM enrollments WHERE user_id=$1`, teacherID).Scan(&rows)
	if rows != 0 {
		t.Fatalf("enrollment rows = %d, want 0 for a teacher", rows)
	}
}
