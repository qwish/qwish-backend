package enrollment

import (
	"context"
	"testing"
)

func TestListSwitchLeave(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	g2, c2 := newClass(t, pool, f.OtherInstitutionID, true)
	if _, err := svc.ConfirmJoin(ctx, f.StudentID, c2, g2); err != nil {
		t.Fatal(err)
	}
	list, err := svc.ListMine(ctx, f.StudentID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list %+v %v", list, err)
	}
	if err := svc.SetActive(ctx, f.StudentID, f.InstitutionID); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetActive(ctx, f.SoloStudentID, f.InstitutionID); err == nil {
		t.Fatal("cannot activate an institute you are not in")
	}
}

func TestLeaveActiveRepointsToOther(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	g2, c2 := newClass(t, pool, f.OtherInstitutionID, true)
	svc.ConfirmJoin(ctx, f.StudentID, c2, g2)
	svc.SetActive(ctx, f.StudentID, f.InstitutionID)

	if err := svc.Leave(ctx, f.StudentID, f.StudentEnrollmentID); err != nil {
		t.Fatal(err)
	}
	var active, status, by string
	var members int
	pool.QueryRow(ctx, `SELECT institution_id::text FROM users WHERE id=$1`, f.StudentID).Scan(&active)
	pool.QueryRow(ctx, `SELECT status, ended_by FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&status, &by)
	pool.QueryRow(ctx, `SELECT count(*) FROM group_students WHERE group_id=$1 AND user_id=$2`, f.GroupID, f.StudentID).Scan(&members)
	if active != f.OtherInstitutionID || status != "left" || by != "student" || members != 0 {
		t.Fatalf("active=%s status=%s by=%s members=%d", active, status, by, members)
	}
}
