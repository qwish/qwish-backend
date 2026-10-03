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
