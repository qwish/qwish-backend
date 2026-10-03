package enrollment

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestInvitesOnlyForInstituteDomain(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	d := fmt.Sprintf("i%d.edu", time.Now().UnixNano())
	pool.Exec(ctx, `INSERT INTO institution_domains (institution_id, domain, verified_at) VALUES ($1,$2,now())`, f.InstitutionID, d)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM student_invites WHERE institution_id=$1`, f.InstitutionID)
		pool.Exec(ctx, `DELETE FROM institution_domains WHERE domain=$1`, d)
	})

	b, err := svc.CreateInvites(ctx, f.InstitutionID, f.GroupID, f.TeacherID,
		[]string{"a@" + d, "b@gmail.com", "not-an-email", "A@" + d})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Created) != 1 || b.Created[0].Email != "a@"+d {
		t.Fatalf("created = %+v", b.Created)
	}
	reasons := map[string]string{}
	for _, r := range b.Rejected {
		reasons[r.Email] = r.Reason
	}
	if reasons["b@gmail.com"] != "not_institute_domain" || reasons["not-an-email"] != "invalid" {
		t.Fatalf("rejected = %+v", b.Rejected)
	}
}

func TestAcceptInviteJoinsClass(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	d := fmt.Sprintf("j%d.edu", time.Now().UnixNano())
	pool.Exec(ctx, `INSERT INTO institution_domains (institution_id, domain, verified_at) VALUES ($1,$2,now())`, f.InstitutionID, d)
	pool.Exec(ctx, `UPDATE groups SET joining_enabled=false WHERE id=$1`, f.GroupID)
	pool.Exec(ctx, `INSERT INTO user_emails (user_id, email, verified_at) VALUES ($1,$2,now())`, f.SoloStudentID, "solo@"+d)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM student_invites WHERE institution_id=$1`, f.InstitutionID)
		pool.Exec(ctx, `DELETE FROM user_emails WHERE user_id=$1`, f.SoloStudentID)
		pool.Exec(ctx, `DELETE FROM institution_domains WHERE domain=$1`, d)
	})
	b, _ := svc.CreateInvites(ctx, f.InstitutionID, f.GroupID, f.TeacherID, []string{"solo@" + d})

	mine, err := svc.MyInvites(ctx, f.SoloStudentID)
	if err != nil || len(mine) != 1 {
		t.Fatalf("my invites %+v %v", mine, err)
	}
	r, err := svc.AcceptInvite(ctx, f.SoloStudentID, b.Created[0].ID)
	if err != nil || r.Destination.Route != "invite" {
		t.Fatalf("accept %+v %v", r, err)
	}
}

// Re-inviting an address that already has a pending invite doesn't email again
// (a timed-out request retried by the client must not spam everyone).
func TestReinviteDoesNotEmailAgain(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	sent := 0
	svc.SetMailer(func(context.Context, string, string, string) error { sent++; return nil })
	ctx := context.Background()
	d := fmt.Sprintf("m%d.edu", time.Now().UnixNano())
	pool.Exec(ctx, `INSERT INTO institution_domains (institution_id, domain, verified_at) VALUES ($1,$2,now())`, f.InstitutionID, d)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM student_invites WHERE institution_id=$1`, f.InstitutionID)
		pool.Exec(ctx, `DELETE FROM institution_domains WHERE domain=$1`, d)
	})
	for i := 0; i < 2; i++ {
		b, err := svc.CreateInvites(ctx, f.InstitutionID, f.GroupID, f.TeacherID, []string{"a@" + d})
		if err != nil {
			t.Fatal(err)
		}
		svc.notifyInvites(ctx, b.Created)
	}
	if sent != 1 {
		t.Fatalf("emails sent = %d, want 1", sent)
	}
}
