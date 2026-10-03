package enrollment

import (
	"context"
	"testing"
)

func TestActiveByUserReturnsNilForSoloStudent(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)

	got, err := svc.ActiveByUser(context.Background(), f.SoloStudentID)
	if err != nil {
		t.Fatalf("ActiveByUser: %v", err)
	}
	if got != nil {
		t.Fatalf("got %+v, want nil for a student with no enrollment", got)
	}
}

func TestSetJoiningScopedToInstitute(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	if err := svc.SetJoining(ctx, f.OtherInstitutionID, f.GroupID, false); err != ErrNotFound {
		t.Fatalf("another institute's class: want ErrNotFound, got %v", err)
	}
	if err := svc.SetJoining(ctx, f.InstitutionID, f.GroupID, false); err != nil {
		t.Fatal(err)
	}
	var on bool
	pool.QueryRow(ctx, `SELECT joining_enabled FROM groups WHERE id=$1`, f.GroupID).Scan(&on)
	if on {
		t.Fatal("joining should be off")
	}
}
