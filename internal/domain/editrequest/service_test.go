package editrequest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seeded is one institution with a teacher assigned to a class, a student
// enrolled and in that class, and a second teacher assigned to nothing.
type seeded struct {
	InstitutionID string
	TeacherID     string
	LonerID       string
	StudentID     string
	EnrollmentID  string
	GroupID       string
}

func seed(t *testing.T, pool *pgxpool.Pool) seeded {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var s seeded

	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	must("institution", pool.QueryRow(ctx, `
		INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('ER '||$1, 'school', 'er-'||$1||'@example.test', 'S'||$1, 'T'||$1, 'verified')
		RETURNING id`, tag).Scan(&s.InstitutionID))

	newUser := func(role, label string, dest *string) {
		t.Helper()
		must(label, pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $2, $3, $4)
			RETURNING id`, label+tag, label+"-"+tag+"@example.test", role, s.InstitutionID).Scan(dest))
	}
	newUser("teacher", "er-teacher", &s.TeacherID)
	newUser("teacher", "er-loner", &s.LonerID)
	newUser("student", "er-student", &s.StudentID)

	must("group", pool.QueryRow(ctx, `
		INSERT INTO groups (institution_id, name, invite_code) VALUES ($1,'ER Class','ERINV'||$2)
		RETURNING id`, s.InstitutionID, tag).Scan(&s.GroupID))
	_, err := pool.Exec(ctx, `INSERT INTO group_teachers (group_id, user_id) VALUES ($1,$2)`, s.GroupID, s.TeacherID)
	must("group_teachers", err)
	_, err = pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, s.GroupID, s.StudentID)
	must("group_students", err)

	must("enrollment", pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, roll_number, grade, section, status, joined_at)
		VALUES ($1,$2,'er-student','ER-'||$3,'9','A','active',now())
		RETURNING id`, s.InstitutionID, s.StudentID, tag).Scan(&s.EnrollmentID))

	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM student_edit_requests WHERE enrollment_id=$1`, s.EnrollmentID)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, s.InstitutionID)
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id=$1`, s.GroupID)
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE group_id=$1`, s.GroupID)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, s.InstitutionID)
		pool.Exec(ctx, `DELETE FROM users WHERE institution_id=$1`, s.InstitutionID)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, s.InstitutionID)
	})
	return s
}

func TestProposeRequiresSharedClass(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)

	_, err := svc.Propose(context.Background(), s.LonerID, s.EnrollmentID, "section", "B", "")
	if !errors.Is(err, ErrNotYourClass) {
		t.Fatalf("err = %v, want ErrNotYourClass", err)
	}
}

func TestProposeRejectsUnknownField(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)

	_, err := svc.Propose(context.Background(), s.TeacherID, s.EnrollmentID, "total_points", "999", "")
	if !errors.Is(err, ErrInvalidField) {
		t.Fatalf("err = %v, want ErrInvalidField", err)
	}
}

// Approving is what actually writes the enrollment; proposing must not.
func TestProposeDoesNotWriteUntilApproved(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	id, err := svc.Propose(ctx, s.TeacherID, s.EnrollmentID, "section", "B", "wrong section")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	var section string
	pool.QueryRow(ctx, `SELECT section FROM enrollments WHERE id=$1`, s.EnrollmentID).Scan(&section)
	if section != "A" {
		t.Fatalf("section = %q, want A while the request is pending", section)
	}

	if err := svc.Review(ctx, s.InstitutionID, s.TeacherID, id, "approved"); err != nil {
		t.Fatalf("Review: %v", err)
	}
	pool.QueryRow(ctx, `SELECT section FROM enrollments WHERE id=$1`, s.EnrollmentID).Scan(&section)
	if section != "B" {
		t.Fatalf("section = %q, want B after approval", section)
	}
}

func TestRejectLeavesEnrollmentUnchanged(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	id, _ := svc.Propose(ctx, s.TeacherID, s.EnrollmentID, "grade", "10", "")
	if err := svc.Review(ctx, s.InstitutionID, s.TeacherID, id, "rejected"); err != nil {
		t.Fatalf("Review: %v", err)
	}

	var grade string
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, s.EnrollmentID).Scan(&grade)
	if grade != "9" {
		t.Fatalf("grade = %q, want 9 after rejection", grade)
	}
}

func TestReviewTwiceFails(t *testing.T) {
	pool := openTestDB(t)
	s := seed(t, pool)
	svc := NewService(pool)
	ctx := context.Background()

	id, _ := svc.Propose(ctx, s.TeacherID, s.EnrollmentID, "grade", "10", "")
	if err := svc.Review(ctx, s.InstitutionID, s.TeacherID, id, "approved"); err != nil {
		t.Fatalf("first Review: %v", err)
	}
	err := svc.Review(ctx, s.InstitutionID, s.TeacherID, id, "rejected")
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("err = %v, want ErrAlreadyResolved", err)
	}
}
