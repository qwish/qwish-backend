package enrollment

import (
	"context"
	"testing"
	"time"
)

func TestEndInactive(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	// Student's only class archived 84 days ago → warned; 91 days → ended.
	pool.Exec(ctx, `UPDATE enrollments SET joined_at=now()-interval '200 days' WHERE id=$1`, f.StudentEnrollmentID)
	pool.Exec(ctx, `UPDATE groups SET archived_at=now()-interval '84 days' WHERE id=$1`, f.GroupID)

	warn, ended, err := svc.EndInactive(ctx, time.Now())
	if err != nil || len(ended) != 0 || !hasUser(warn, f.StudentID) {
		t.Fatalf("day 84: warn=%v ended=%v err=%v", warn, ended, err)
	}
	warn, _, _ = svc.EndInactive(ctx, time.Now())
	if hasUser(warn, f.StudentID) {
		t.Fatal("a student is warned once")
	}
	pool.Exec(ctx, `UPDATE groups SET archived_at=now()-interval '91 days' WHERE id=$1`, f.GroupID)
	_, ended, _ = svc.EndInactive(ctx, time.Now().Add(7*24*time.Hour))
	if !hasUser(ended, f.StudentID) {
		t.Fatalf("day 91 must end the enrollment: %v", ended)
	}
	var status, by string
	pool.QueryRow(ctx, `SELECT status, ended_by FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&status, &by)
	if status != "left" || by != "system" {
		t.Fatalf("status=%s by=%s", status, by)
	}
}

func hasUser(list []EndNotice, id string) bool {
	for _, n := range list {
		if n.UserID == id {
			return true
		}
	}
	return false
}

func TestJoiningClassClearsAutoEndWarning(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	pool.Exec(ctx, `UPDATE enrollments SET end_warned_at=now() WHERE id=$1`, f.StudentEnrollmentID)
	gid, code := newClass(t, pool, f.InstitutionID, true)
	if _, err := svc.ConfirmJoin(ctx, f.StudentID, code, gid); err != nil {
		t.Fatal(err)
	}
	var warned *time.Time
	pool.QueryRow(ctx, `SELECT end_warned_at FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&warned)
	if warned != nil {
		t.Fatal("joining a class must clear the auto-end warning")
	}
}

// A long-classless enrollment is warned first; it is never ended on the run
// that first notices it (no silent endings on deploy day).
func TestNeverEndWithoutPriorWarning(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	pool.Exec(ctx, `UPDATE enrollments SET joined_at=now()-interval '200 days' WHERE id=$1`, f.StudentEnrollmentID)
	pool.Exec(ctx, `DELETE FROM group_students WHERE user_id=$1`, f.StudentID)

	warn, ended, err := svc.EndInactive(ctx, time.Now())
	if err != nil || hasUser(ended, f.StudentID) || !hasUser(warn, f.StudentID) {
		t.Fatalf("first run must warn, not end: warn=%v ended=%v err=%v", warn, ended, err)
	}
	_, ended, _ = svc.EndInactive(ctx, time.Now().Add(6*24*time.Hour))
	if hasUser(ended, f.StudentID) {
		t.Fatal("must wait 7 days after the warning")
	}
	_, ended, _ = svc.EndInactive(ctx, time.Now().Add(8*24*time.Hour))
	if !hasUser(ended, f.StudentID) {
		t.Fatal("ends once the warning is 7 days old")
	}
}

// A warned student who is back in a class (added by a teacher, not by a code)
// loses the stale warning, so a later classless stretch warns again.
func TestLiveClassClearsStaleWarning(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool) // StudentID is in a live class
	svc := NewService(pool)
	ctx := context.Background()
	pool.Exec(ctx, `UPDATE enrollments SET end_warned_at=now()-interval '3 days' WHERE id=$1`, f.StudentEnrollmentID)
	if _, _, err := svc.EndInactive(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	var warned *time.Time
	pool.QueryRow(ctx, `SELECT end_warned_at FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&warned)
	if warned != nil {
		t.Fatal("a live class must clear the auto-end warning")
	}
}
