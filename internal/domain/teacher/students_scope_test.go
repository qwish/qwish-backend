package teacher

import (
	"context"
	"testing"
)

// The teacher roster is built from enrollments, so a graduated student drops
// off it. Reading from users instead would keep them listed forever.
func TestGraduatedStudentLeavesTheTeacherRoster(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	ctx := context.Background()

	var enrollmentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		VALUES ($1, $2, 'group-student', 'active', now())
		RETURNING id`, f.InstitutionID, f.StudentID).Scan(&enrollmentID); err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM enrollments WHERE id=$1`, enrollmentID)
	})

	countVisible := func() int {
		t.Helper()
		var n int
		pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM enrollments e
			  JOIN users u ON u.id = e.user_id
			 WHERE e.institution_id=$1 AND e.status IN ('active','suspended')
			   AND u.id=$2 AND u.deleted_at IS NULL`,
			f.InstitutionID, f.StudentID).Scan(&n)
		return n
	}

	if countVisible() != 1 {
		t.Fatal("an active student should be on the roster")
	}

	if _, err := pool.Exec(ctx,
		`UPDATE enrollments SET status='graduated', ended_at=now() WHERE id=$1`,
		enrollmentID); err != nil {
		t.Fatalf("graduate: %v", err)
	}

	if countVisible() != 0 {
		t.Fatal("a graduated student must not remain on the teacher roster")
	}
}

// A student who transferred in brings their account but not their previous
// school's results. Averaging over every attempt would import those numbers
// into this institution's view of them.
func TestTeacherAverageExcludesAttemptsBeforeJoining(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	ctx := context.Background()

	var enrollmentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		VALUES ($1, $2, 'group-student', 'active', now() - interval '5 days')
		RETURNING id`, f.InstitutionID, f.StudentID).Scan(&enrollmentID); err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, f.QuizID)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE id=$1`, enrollmentID)
	})

	// 40% before joining, 100% after. Only the second should count.
	for _, a := range []struct {
		offset string
		score  int
	}{{"-10 days", 40}, {"-1 day", 100}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, completed_at)
			VALUES ($1, $2, 'completed', $3, now() + $4::interval)`,
			f.QuizID, f.StudentID, a.score, a.offset); err != nil {
			t.Fatalf("seed attempt %s: %v", a.offset, err)
		}
	}

	var avg float64
	pool.QueryRow(ctx, `
		SELECT COALESCE(AVG(score_pct),0) FROM quiz_attempts qa
		  JOIN enrollments e ON e.user_id = qa.user_id
		 WHERE e.id=$1 AND qa.status='completed'
		   AND qa.completed_at >= COALESCE(e.joined_at, '-infinity'::timestamptz)`,
		enrollmentID).Scan(&avg)
	if avg != 100 {
		t.Fatalf("average = %v, want 100 — the pre-enrollment attempt leaked in", avg)
	}
}
