package useremail

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type outbox struct{ last map[string]string }

func (o *outbox) send(_ context.Context, to, code string) error { o.last[to] = code; return nil }

func TestAddVerifyRemove(t *testing.T) {
	pool := openTestDB(t)
	box := &outbox{last: map[string]string{}}
	svc := NewService(pool, box.send)
	ctx := context.Background()
	user := newStudent(t, pool)
	addr := fmt.Sprintf("riya-%d@college.test", time.Now().UnixNano())

	e, err := svc.Add(ctx, user, "  "+addr+" ")
	if err != nil {
		t.Fatal(err)
	}
	if e.Verified || e.Email != addr {
		t.Fatalf("got %+v", e)
	}
	if _, err := svc.Verify(ctx, user, e.ID, "000000"); !errors.Is(err, ErrBadCode) && box.last[addr] != "000000" {
		t.Fatalf("wrong code must fail, got %v", err)
	}
	v, err := svc.Verify(ctx, user, e.ID, box.last[addr])
	if err != nil || !v.Verified {
		t.Fatalf("verify: %+v %v", v, err)
	}
	if err := svc.Remove(ctx, user, e.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := svc.List(ctx, user)
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %+v", list)
	}
}

func TestVerifiedEmailIsUniqueAcrossUsers(t *testing.T) {
	pool := openTestDB(t)
	box := &outbox{last: map[string]string{}}
	svc := NewService(pool, box.send)
	ctx := context.Background()
	a, b := newStudent(t, pool), newStudent(t, pool)
	addr := fmt.Sprintf("shared-%d@college.test", time.Now().UnixNano())

	ea, _ := svc.Add(ctx, a, addr)
	if _, err := svc.Verify(ctx, a, ea.ID, box.last[addr]); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Add(ctx, b, addr); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("want ErrEmailTaken, got %v", err)
	}
}

func TestFiveWrongCodesLockTheCode(t *testing.T) {
	pool := openTestDB(t)
	box := &outbox{last: map[string]string{}}
	svc := NewService(pool, box.send)
	ctx := context.Background()
	user := newStudent(t, pool)
	addr := fmt.Sprintf("lock-%d@college.test", time.Now().UnixNano())
	e, _ := svc.Add(ctx, user, addr)
	wrong := "111111"
	if box.last[addr] == wrong {
		wrong = "222222"
	}
	for i := 0; i < 5; i++ {
		svc.Verify(ctx, user, e.ID, wrong)
	}
	if _, err := svc.Verify(ctx, user, e.ID, box.last[addr]); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("want ErrTooManyAttempts after 5 misses, got %v", err)
	}
}

func TestRejectsLoginEmailAndDisposable(t *testing.T) {
	pool := openTestDB(t)
	svc := NewService(pool, (&outbox{last: map[string]string{}}).send)
	ctx := context.Background()
	user := newStudent(t, pool)
	var login string
	pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, user).Scan(&login)
	if _, err := svc.Add(ctx, user, login); !errors.Is(err, ErrIsLoginEmail) {
		t.Fatalf("want ErrIsLoginEmail, got %v", err)
	}
	if _, err := svc.Add(ctx, user, "x@mailinator.com"); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("want ErrInvalidEmail for disposable, got %v", err)
	}
}
