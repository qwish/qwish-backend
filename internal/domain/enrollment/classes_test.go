package enrollment

import (
	"context"
	"testing"
)

func TestJoiningGradedClassCopiesGrade(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	gid, _ := newClass(t, pool, f.InstitutionID, true)
	pool.Exec(ctx, `UPDATE groups SET grade='10', section='B' WHERE id=$1`, gid)
	if _, err := pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, gid, f.StudentID); err != nil {
		t.Fatal(err)
	}
	var grade, section string
	pool.QueryRow(ctx, `SELECT grade, section FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&grade, &section)
	if grade != "10" || section != "B" {
		t.Fatalf("grade=%q section=%q", grade, section)
	}
}

func TestRemedialGroupDoesNotSetGrade(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	gid, _ := newClass(t, pool, f.InstitutionID, false)
	pool.Exec(ctx, `UPDATE groups SET kind='remedial', grade='3' WHERE id=$1`, gid)
	pool.Exec(ctx, `INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`, gid, f.StudentID)
	var grade string
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&grade)
	if grade == "3" {
		t.Fatal("remedial group must not set the enrollment grade")
	}
}

func TestRemovalWritesHistory(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	pool.Exec(ctx, `UPDATE groups SET grade='9', section='A' WHERE id=$1`, f.GroupID)
	if _, err := pool.Exec(ctx, `DELETE FROM group_students WHERE group_id=$1 AND user_id=$2`, f.GroupID, f.StudentID); err != nil {
		t.Fatal(err)
	}
	var n int
	var grade string
	pool.QueryRow(ctx, `SELECT count(*), max(grade) FROM group_student_history WHERE group_id=$1 AND user_id=$2 AND left_at IS NOT NULL`, f.GroupID, f.StudentID).Scan(&n, &grade)
	if n != 1 || grade != "9" {
		t.Fatalf("history rows=%d grade=%q", n, grade)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM group_student_history WHERE user_id=$1`, f.StudentID) })
}
func tableExists(t *testing.T, name string) bool {
	t.Helper()
	var ok bool
	openTestDB(t).QueryRow(context.Background(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&ok)
	return ok
}

func TestRevertedPromotionNotConverted(t *testing.T) {
	if !tableExists(t, "promotion_batches") {
		t.Skip("086 already applied")
	}
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	var kept, reverted string
	pool.QueryRow(ctx, `INSERT INTO promotion_batches (institution_id, performed_by, source_group_id, to_grade, created_at) VALUES ($1,$2,$3,'10',now()-interval '30 days') RETURNING id`, f.InstitutionID, f.TeacherID, f.GroupID).Scan(&kept)
	pool.QueryRow(ctx, `INSERT INTO promotion_batches (institution_id, performed_by, source_group_id, to_grade, reverted_at) VALUES ($1,$2,$3,'10',now()) RETURNING id`, f.InstitutionID, f.TeacherID, f.GroupID).Scan(&reverted)
	pool.Exec(ctx, `INSERT INTO promotion_batch_students (batch_id, enrollment_id, outcome, prior_group_id, prior_grade, prior_section) VALUES ($1,$3,'promoted',$4,'9','A'),($2,$3,'promoted',$4,'9','A')`, kept, reverted, f.StudentEnrollmentID, f.GroupID)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM promotion_batches WHERE id IN ($1,$2)`, kept, reverted)
		pool.Exec(ctx, `DELETE FROM group_student_history WHERE user_id=$1`, f.StudentID)
	})
	if _, err := pool.Exec(ctx, `SELECT convert_promotions_to_history()`); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM group_student_history WHERE user_id=$1 AND grade='9'`, f.StudentID).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 converted row (reverted batch skipped), got %d", n)
	}
}

// Hard-deleting a user (super-admin purge) cascades their memberships; the
// history trigger must not block it by writing rows for a vanished user.
func TestUserPurgeIsNotBlockedByHistory(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	pool.Exec(ctx, `DELETE FROM enrollments WHERE user_id=$1`, f.StudentID)
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, f.StudentID); err != nil {
		t.Fatalf("purging a student with class history must succeed: %v", err)
	}
}
