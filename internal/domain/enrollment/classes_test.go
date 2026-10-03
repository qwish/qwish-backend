package enrollment

import (
	"context"
	"strings"
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

// The 086 conversion runs once against real promotion tables; temp tables of
// the same name (pg_temp shadows public) let it run on every test run.
func TestPromotionConversion(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	c9, _ := newClass(t, pool, f.InstitutionID, false)
	c10, _ := newClass(t, pool, f.InstitutionID, false)
	c11, _ := newClass(t, pool, f.InstitutionID, false)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	_, err = conn.Exec(ctx, `
 UPDATE enrollments SET joined_at=now()-interval '1 year' WHERE id='`+f.StudentEnrollmentID+`';
 CREATE TEMP TABLE promotion_batches(id uuid PRIMARY KEY DEFAULT gen_random_uuid(), target_group_id uuid, to_grade text, to_section text, reverted_at timestamptz, created_at timestamptz);
 CREATE TEMP TABLE promotion_batch_students(batch_id uuid, enrollment_id uuid, outcome text, prior_group_id uuid, prior_grade text, prior_section text, revert_outcome text);`)
	if err != nil {
		t.Fatal(err)
	}
	batch := func(target any, grade, ago string, reverted bool, prior, revertOutcome any) {
		t.Helper()
		var id string
		if err := conn.QueryRow(ctx, `INSERT INTO promotion_batches (target_group_id, to_grade, created_at, reverted_at)
			VALUES ($1::uuid, $2, now()-$3::interval, CASE WHEN $4 THEN now() END) RETURNING id`, target, grade, ago, reverted).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO promotion_batch_students VALUES ($1,$2,'promoted',$3::uuid,'x',NULL,$4)`,
			id, f.StudentEnrollmentID, prior, revertOutcome); err != nil {
			t.Fatal(err)
		}
	}
	batch(c10, "10", "200 days", false, c9, nil)       // 9 -> 10
	batch(c11, "11", "100 days", true, c10, "skipped") // reverted, but this student stayed promoted
	batch(c9, "9", "50 days", true, c11, "reverted")   // undone: no history
	batch(nil, "12", "20 days", false, f.GroupID, nil) // no target class: never left
	if _, err := conn.Exec(ctx, `SELECT convert_promotions_to_history()`); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(ctx, `SELECT group_id::text||' '||extract(day FROM now()-joined_at)||'d-'||extract(day FROM now()-left_at)||'d'
		FROM group_student_history WHERE user_id=$1 ORDER BY joined_at`, f.StudentID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var r string
		rows.Scan(&r)
		got = append(got, r)
	}
	rows.Close()
	// c9 from enrolment to the first promotion, c10 from there to the second.
	want := []string{c9 + " 365d-200d", c10 + " 200d-100d"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("history:\n got %v\nwant %v", got, want)
	}
	var grade string
	pool.QueryRow(ctx, `SELECT COALESCE(grade,'') FROM groups WHERE id=$1`, c10).Scan(&grade)
	if grade != "10" {
		t.Fatalf("promotion target class grade = %q, want 10", grade)
	}
}

// Leaving an ended class must not stretch the stay past the class's end.
func TestRemovalFromEndedClassEndsAtClassEnd(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	pool.Exec(ctx, `UPDATE groups SET archived_at=now()-interval '30 days' WHERE id=$1`, f.GroupID)
	pool.Exec(ctx, `DELETE FROM group_students WHERE group_id=$1 AND user_id=$2`, f.GroupID, f.StudentID)
	var exact bool
	pool.QueryRow(ctx, `SELECT h.left_at=g.archived_at FROM group_student_history h JOIN groups g ON g.id=h.group_id
		WHERE h.user_id=$1`, f.StudentID).Scan(&exact)
	if !exact {
		t.Fatal("history left_at must be the class end date")
	}
}

// Leaving the institute drops ended classes too, so a reopen can't bring the
// student back; history keeps the past class.
func TestLeaveDropsEndedClassMembership(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	pool.Exec(ctx, `UPDATE groups SET archived_at=now() WHERE id=$1`, f.GroupID)
	if err := NewService(pool).Leave(ctx, f.StudentID, f.StudentEnrollmentID); err != nil {
		t.Fatal(err)
	}
	var live, hist int
	pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM group_students WHERE user_id=$1),
		(SELECT count(*) FROM group_student_history WHERE user_id=$1)`, f.StudentID).Scan(&live, &hist)
	if live != 0 || hist != 1 {
		t.Fatalf("memberships=%d history=%d", live, hist)
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
