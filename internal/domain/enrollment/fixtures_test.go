package enrollment

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fixture is two institutions plus the people needed to exercise scope rules:
//
//	InstitutionID       — holds GroupID, TeacherID, StudentID and one unclaimed roster row
//	OtherInstitutionID  — used to test cross-institution isolation
//	TeacherID           — assigned to GroupID
//	LonerTeacherID      — assigned to no group
//	StudentID           — a claimed, active student in GroupID
//	SoloStudentID       — a student with no enrollment at all
//	UnclaimedEnrollmentID / ClaimCode — a pending_claim roster row
type fixture struct {
	InstitutionID         string
	OtherInstitutionID    string
	TeacherID             string
	LonerTeacherID        string
	StudentID             string
	SoloStudentID         string
	StudentEnrollmentID   string
	UnclaimedEnrollmentID string
	ClaimCode             string
	GroupID               string
}

func seedFixture(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	ctx := context.Background()
	// A per-run suffix keeps unique constraints on email, referral and claim
	// codes satisfied when the suite runs twice against the same scratch DB.
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var f fixture

	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}

	newInst := func(label string, dest *string) {
		t.Helper()
		must(label, pool.QueryRow(ctx, `
			INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
			VALUES ($1||' '||$2, 'school', $1||'-'||$2||'@example.test', 'S'||$1||$2, 'T'||$1||$2, 'verified')
			RETURNING id`, label, tag).Scan(dest))
	}
	newInst("main", &f.InstitutionID)
	newInst("other", &f.OtherInstitutionID)

	newUser := func(role, label string, instID *string, dest *string) {
		t.Helper()
		must(label, pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			VALUES (gen_random_uuid(), $1, $1, $2, $3, $4)
			RETURNING id`,
			label+" "+tag, label+"-"+tag+"@example.test", role, instID).Scan(dest))
	}
	newUser("teacher", "assigned-teacher", &f.InstitutionID, &f.TeacherID)
	newUser("teacher", "loner-teacher", &f.InstitutionID, &f.LonerTeacherID)
	newUser("student", "group-student", &f.InstitutionID, &f.StudentID)
	newUser("student", "solo-student", nil, &f.SoloStudentID)

	must("group", pool.QueryRow(ctx, `
		INSERT INTO groups (institution_id, name, invite_code)
		VALUES ($1, 'Fixture Class', 'INV'||$2)
		RETURNING id`, f.InstitutionID, tag).Scan(&f.GroupID))

	_, err := pool.Exec(ctx,
		`INSERT INTO group_teachers (group_id, user_id) VALUES ($1, $2)`, f.GroupID, f.TeacherID)
	must("group_teachers", err)
	_, err = pool.Exec(ctx,
		`INSERT INTO group_students (group_id, user_id) VALUES ($1, $2)`, f.GroupID, f.StudentID)
	must("group_students", err)

	must("claimed enrollment", pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, roll_number, grade, section, status, joined_at)
		VALUES ($1, $2, 'group-student', 'R-'||$3, '9', 'A', 'active', now())
		RETURNING id`, f.InstitutionID, f.StudentID, tag).Scan(&f.StudentEnrollmentID))

	f.ClaimCode = "CLAIM" + tag
	must("unclaimed enrollment", pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, full_name, email, roll_number, grade, section, claim_code, status)
		VALUES ($1, 'Unclaimed Student', $2, 'U-'||$3, '9', 'B', $4, 'pending_claim')
		RETURNING id`,
		f.InstitutionID, "unclaimed-"+tag+"@example.test", tag, f.ClaimCode).Scan(&f.UnclaimedEnrollmentID))

	t.Cleanup(func() {
		ctx := context.Background()
		users := []string{f.TeacherID, f.LonerTeacherID, f.StudentID, f.SoloStudentID}
		insts := []string{f.InstitutionID, f.OtherInstitutionID}
		pool.Exec(ctx, `DELETE FROM student_edit_requests WHERE enrollment_id IN
			(SELECT id FROM enrollments WHERE institution_id = ANY($1))`, insts)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM user_profile_entries WHERE user_id = ANY($1)`, users)
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id = $1`, f.GroupID)
		pool.Exec(ctx, `DELETE FROM group_teachers WHERE group_id = $1`, f.GroupID)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id = ANY($1)`, insts)
		pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, users)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id = ANY($1)`, insts)
	})

	return f
}
