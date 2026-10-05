package notice

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// notifyInto delivers like production: one user_notifications row per
// recipient, kind 'notice', reference notice:<id>.
func notifyInto(pool *pgxpool.Pool) func(context.Context, string, string, string, string) {
	return func(ctx context.Context, userID, title, body, ref string) {
		pool.Exec(ctx, `INSERT INTO user_notifications (user_id, kind, title, body, reference) VALUES ($1,'notice',$2,$3,$4)`, userID, title, body, ref)
	}
}

// A student's notices are exactly the ones delivered to them, newest first,
// with the sender, category and whether they have read it.
func TestReceivedNotices(t *testing.T) {
	pool := openTestDB(t)
	sc := seedSchool(t, pool)
	svc := NewService(pool, notifyInto(pool))
	ctx := context.Background()
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM user_notifications WHERE user_id = ANY($1)`, []string{sc.S1, sc.S2, sc.S3})
		pool.Exec(ctx, `DELETE FROM notices WHERE institution_id=$1`, sc.Inst)
	})

	classNotice, err := svc.Send(ctx, Actor{UserID: sc.TeachSciA, InstitutionID: sc.Inst}, Draft{Title: "Lab on Friday", Body: "Bring your lab coat.", Category: "event", GroupIDs: []string{sc.SciA}})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // distinct created_at for ordering
	schoolNotice, err := svc.Send(ctx, Actor{UserID: sc.Admin, InstitutionID: sc.Inst, Admin: true}, Draft{Title: "Holiday Monday", Body: "School is closed.", Category: "general", InstitutionWide: true})
	if err != nil {
		t.Fatal(err)
	}
	svc.wait()

	s1, total, err := svc.Received(ctx, sc.S1, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(s1) != 2 || s1[0].ID != schoolNotice.ID || s1[1].ID != classNotice.ID {
		t.Fatalf("S1 notices = %+v (total %d), want school then class", s1, total)
	}
	if s1[1].Category != "event" || s1[1].FromName == "" || s1[1].Read || s1[1].NotificationID == "" {
		t.Errorf("class notice = %+v", s1[1])
	}

	s2, total, _ := svc.Received(ctx, sc.S2, 20, 0)
	if total != 1 || len(s2) != 1 || s2[0].ID != schoolNotice.ID {
		t.Errorf("S2 (commerce) notices = %+v, want only the school-wide one", s2)
	}

	// Read state follows the notification.
	pool.Exec(ctx, `UPDATE user_notifications SET read_at=now() WHERE user_id=$1 AND reference=$2`, sc.S1, "notice:"+classNotice.ID)
	s1, _, _ = svc.Received(ctx, sc.S1, 20, 0)
	if !s1[1].Read || s1[0].Read {
		t.Errorf("read state = %v/%v, want class read, school unread", s1[1].Read, s1[0].Read)
	}

	page, total, _ := svc.Received(ctx, sc.S1, 1, 1)
	if total != 2 || len(page) != 1 || page[0].ID != classNotice.ID {
		t.Errorf("page 2 = %+v total %d", page, total)
	}
}
