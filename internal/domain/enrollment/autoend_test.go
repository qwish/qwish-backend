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
	_, ended, _ = svc.EndInactive(ctx, time.Now())
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
