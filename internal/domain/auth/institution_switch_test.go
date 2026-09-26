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

// Student referral endpoints may never bypass admissions or silently transfer.
func TestUpdateUserInstitutionRequiresStudentJoinFlow(t *testing.T) {
	pool := openTestDB(t)
	f := seedSwitchFixture(t, pool)
	svc := &Service{db: pool}
	ctx := context.Background()
	for _, code := range []string{f.FromCode, f.ToCode, "T" + f.FromCode} {
		if err := svc.UpdateUserInstitution(ctx, f.StudentID, code); err == nil {
			t.Fatal("legacy student join unexpectedly allowed")
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM enrollments WHERE user_id=$1`, f.StudentID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("enrollments=%d err=%v", count, err)
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
