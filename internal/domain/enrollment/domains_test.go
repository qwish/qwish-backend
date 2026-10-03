package enrollment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestNormalizeDomain(t *testing.T) {
	cases := map[string]error{
		" College.EDU ":  nil,
		"gmail.com":      ErrDomainFreeMail,
		"mailinator.com": ErrDomainFreeMail,
		"x@college.edu":  ErrDomainInvalid,
		"localhost":      ErrDomainInvalid,
	}
	for in, want := range cases {
		got, err := NormalizeDomain(in)
		if !errors.Is(err, want) {
			t.Errorf("%q: want %v, got %v (%q)", in, want, err, got)
		}
	}
	if d, _ := NormalizeDomain(" College.EDU "); d != "college.edu" {
		t.Errorf("normalized = %q", d)
	}
}

func TestVerifiedDomainBelongsToOneInstitute(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	d := fmt.Sprintf("c%d.edu", time.Now().UnixNano())
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM institution_domains WHERE domain=$1`, d) })

	if _, err := svc.AddDomain(ctx, f.InstitutionID, d, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddDomain(ctx, f.OtherInstitutionID, d, ""); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("want ErrDomainTaken, got %v", err)
	}
}
