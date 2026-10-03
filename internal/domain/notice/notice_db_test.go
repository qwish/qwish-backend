package notice

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type sink struct {
	mu   sync.Mutex
	sent map[string]int
}

func (s *sink) emit(_ context.Context, userID, _, _, _ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent[userID]++
}

func TestTeacherAudienceRules(t *testing.T) {
	pool := openTestDB(t)
	sc := seedSchool(t, pool)
	out := &sink{sent: map[string]int{}}
	svc := NewService(pool, out.emit)
	ctx := context.Background()
	teacher := Actor{UserID: sc.TeachSciA, InstitutionID: sc.Inst}
	hod := Actor{UserID: sc.HodSci, InstitutionID: sc.Inst}
	base := Draft{Title: "Test Friday", Body: "Chapter 3", Category: "test"}

	d := base
	d.GroupIDs = []string{sc.SciB}
	if _, err := svc.Send(ctx, teacher, d); !errors.Is(err, ErrForbidden) {
		t.Fatalf("teacher → class they don't teach: want ErrForbidden, got %v", err)
	}
	d = base
	d.DepartmentIDs = []string{sc.Science}
	if _, err := svc.Send(ctx, teacher, d); err != nil {
		t.Fatalf("teacher → own department: %v", err)
	}
	d = base
	d.DepartmentIDs = []string{sc.Commerce}
	if _, err := svc.Send(ctx, teacher, d); !errors.Is(err, ErrForbidden) {
		t.Fatalf("teacher → other department: want ErrForbidden, got %v", err)
	}
	d = base
	d.GroupIDs = []string{sc.SciB}
	if _, err := svc.Send(ctx, hod, d); err != nil {
		t.Fatalf("HOD → class in department: %v", err)
	}
	d = base
	d.GroupIDs = []string{sc.ComA}
	if _, err := svc.Send(ctx, hod, d); !errors.Is(err, ErrForbidden) {
		t.Fatalf("HOD → class in other department: want ErrForbidden, got %v", err)
	}
	d = base
	d.InstitutionWide = true
	if _, err := svc.Send(ctx, hod, d); !errors.Is(err, ErrForbidden) {
		t.Fatalf("HOD → whole institute: want ErrForbidden, got %v", err)
	}
	if _, err := svc.Send(ctx, Actor{UserID: sc.Admin, InstitutionID: sc.Inst, Admin: true}, d); err != nil {
		t.Fatalf("admin → whole institute: %v", err)
	}
}

func TestDepartmentNoticeDedupes(t *testing.T) {
	pool := openTestDB(t)
	sc := seedSchool(t, pool)
	out := &sink{sent: map[string]int{}}
	svc := NewService(pool, out.emit)
	n, err := svc.Send(context.Background(), Actor{UserID: sc.HodSci, InstitutionID: sc.Inst},
		Draft{Title: "Science fair", Body: "Monday", Category: "event", DepartmentIDs: []string{sc.Science}})
	if err != nil {
		t.Fatal(err)
	}
	if out.sent[sc.S1] != 1 || out.sent[sc.S2] != 0 || n.RecipientCount != 1 {
		t.Fatalf("sent=%v count=%d", out.sent, n.RecipientCount)
	}
}

func TestNoticeSkipsInactive(t *testing.T) {
	pool := openTestDB(t)
	sc := seedSchool(t, pool)
	out := &sink{sent: map[string]int{}}
	svc := NewService(pool, out.emit)
	svc.Send(context.Background(), Actor{UserID: sc.TeachSciA, InstitutionID: sc.Inst},
		Draft{Title: "Quiz", Body: "Tomorrow", Category: "test", GroupIDs: []string{sc.SciA}})
	if out.sent[sc.S3] != 0 {
		t.Fatal("suspended student must not receive notices")
	}
}

func TestDraftValidation(t *testing.T) {
	pool := openTestDB(t)
	sc := seedSchool(t, pool)
	svc := NewService(pool, (&sink{sent: map[string]int{}}).emit)
	a := Actor{UserID: sc.TeachSciA, InstitutionID: sc.Inst}
	for _, d := range []Draft{
		{Title: " ", Body: "x", Category: "test", GroupIDs: []string{sc.SciA}},
		{Title: "x", Body: "x", Category: "party", GroupIDs: []string{sc.SciA}},
		{Title: "x", Body: "x", Category: "test"}, // no audience
	} {
		if _, err := svc.Send(context.Background(), a, d); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%+v: want ErrInvalid, got %v", d, err)
		}
	}
}
