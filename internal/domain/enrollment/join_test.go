package enrollment

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func newClass(t *testing.T, pool *pgxpool.Pool, instID string, joining bool) (id, code string) {
	t.Helper()
	code = fmt.Sprintf("C%d", time.Now().UnixNano())
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO groups (institution_id, name, invite_code, joining_enabled) VALUES ($1,'Class '||$2,$2,$3) RETURNING id`,
		instID, code, joining).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id, code
}

func newInstitute(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ('third '||$1, 'school', 'third-'||$1||'@example.test', 'S3'||$1, 'T3'||$1, 'verified') RETURNING id`, tag).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM group_students WHERE group_id IN (SELECT id FROM groups WHERE institution_id=$1)`, id)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, id)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, id)
	})
	return id
}

func TestJoinOpenClassByCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	gid, code := newClass(t, pool, f.OtherInstitutionID, true)

	p, err := svc.PreviewJoin(ctx, f.SoloStudentID, code)
	if err != nil || p.Route != "code" || p.TargetID != gid {
		t.Fatalf("preview %+v %v", p, err)
	}
	r, err := svc.ConfirmJoin(ctx, f.SoloStudentID, code, gid)
	if err != nil || r.Status != "joined" || r.Enrollment == nil {
		t.Fatalf("confirm %+v %v", r, err)
	}
	var route string
	pool.QueryRow(ctx, `SELECT join_route FROM enrollments WHERE id=$1`, r.Enrollment.ID).Scan(&route)
	if route != "code" {
		t.Fatalf("join_route = %q", route)
	}
}

func TestClosedClassNeedsDomainOrInvite(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	gid, code := newClass(t, pool, f.OtherInstitutionID, false)

	if _, err := svc.PreviewJoin(ctx, f.SoloStudentID, code); !errors.Is(err, ErrJoinClosed) {
		t.Fatalf("want ErrJoinClosed, got %v", err)
	}

	d := fmt.Sprintf("d%d.edu", time.Now().UnixNano())
	pool.Exec(ctx, `INSERT INTO institution_domains (institution_id, domain, verified_at) VALUES ($1,$2,now())`, f.OtherInstitutionID, d)
	pool.Exec(ctx, `INSERT INTO user_emails (user_id, email, verified_at) VALUES ($1,$2,now())`, f.SoloStudentID, "solo@"+d)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM institution_domains WHERE domain=$1`, d)
		pool.Exec(ctx, `DELETE FROM user_emails WHERE user_id=$1`, f.SoloStudentID)
	})
	r, err := svc.ConfirmJoin(ctx, f.SoloStudentID, code, gid)
	if err != nil || r.Destination.Route != "domain" {
		t.Fatalf("domain join %+v %v", r, err)
	}
}

func TestInviteAdmitsAndIsConsumed(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	gid, code := newClass(t, pool, f.OtherInstitutionID, false)
	addr := fmt.Sprintf("inv-%d@inv.edu", time.Now().UnixNano())
	pool.Exec(ctx, `INSERT INTO user_emails (user_id, email, verified_at) VALUES ($1,$2,now())`, f.SoloStudentID, addr)
	pool.Exec(ctx, `INSERT INTO student_invites (institution_id, group_id, email) VALUES ($1,$2,$3)`, f.OtherInstitutionID, gid, addr)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM student_invites WHERE email=$1`, addr)
		pool.Exec(ctx, `DELETE FROM user_emails WHERE email=$1`, addr)
	})

	r, err := svc.ConfirmJoin(ctx, f.SoloStudentID, code, gid)
	if err != nil || r.Destination.Route != "invite" {
		t.Fatalf("invite join %+v %v", r, err)
	}
	var status string
	pool.QueryRow(ctx, `SELECT status FROM student_invites WHERE email=$1`, addr).Scan(&status)
	if status != "accepted" {
		t.Fatalf("invite status = %q", status)
	}
}

func TestJoinCapIsTwo(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool) // StudentID is live at InstitutionID
	svc := NewService(pool)
	ctx := context.Background()
	g2, c2 := newClass(t, pool, f.OtherInstitutionID, true)
	if _, err := svc.ConfirmJoin(ctx, f.StudentID, c2, g2); err != nil {
		t.Fatalf("second institute: %v", err)
	}
	third := newInstitute(t, pool)
	g3, c3 := newClass(t, pool, third, true)
	if _, err := svc.ConfirmJoin(ctx, f.StudentID, c3, g3); !errors.Is(err, ErrInstituteCap) {
		t.Fatalf("want ErrInstituteCap, got %v", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM enrollments WHERE user_id=$1 AND institution_id=$2`, f.StudentID, third).Scan(&n)
	if n != 0 {
		t.Fatal("capped join must not write an enrollment")
	}
}

func TestConcurrentJoinsRespectCap(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool) // one live institute already
	svc := NewService(pool)
	ctx := context.Background()
	a, b := newInstitute(t, pool), newInstitute(t, pool)
	ga, ca := newClass(t, pool, a, true)
	gb, cb := newClass(t, pool, b, true)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, errs[0] = svc.ConfirmJoin(ctx, f.StudentID, ca, ga) }()
	go func() { defer wg.Done(); _, errs[1] = svc.ConfirmJoin(ctx, f.StudentID, cb, gb) }()
	wg.Wait()
	ok := 0
	for _, e := range errs {
		if e == nil {
			ok++
		} else if !errors.Is(e, ErrInstituteCap) {
			t.Fatalf("unexpected error %v", e)
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one concurrent join may succeed, got %d", ok)
	}
}

func TestJoiningSecondClassAtSameInstituteReusesEnrollment(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	gid, code := newClass(t, pool, f.InstitutionID, true)
	r, err := svc.ConfirmJoin(ctx, f.StudentID, code, gid)
	if err != nil || r.Enrollment.ID != f.StudentEnrollmentID {
		t.Fatalf("want existing enrollment reused, got %+v %v", r.Enrollment, err)
	}
}
