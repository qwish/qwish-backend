package enrollment

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func admissionPolicy(t *testing.T, pool *pgxpool.Pool, inst string, p AdmissionPolicy) {
	t.Helper()
	if p.Match == "" {
		p.Match = "any"
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(p)
	if _, err := pool.Exec(context.Background(), `INSERT INTO admission_policies(institution_id,policy) VALUES($1,$2) ON CONFLICT(institution_id) DO UPDATE SET policy=EXCLUDED.policy`, inst, raw); err != nil {
		t.Fatal(err)
	}
}
func joinCode(t *testing.T, pool *pgxpool.Pool, group string) string {
	t.Helper()
	var code string
	if err := pool.QueryRow(context.Background(), `SELECT invite_code FROM groups WHERE id=$1`, group).Scan(&code); err != nil {
		t.Fatal(err)
	}
	return code
}
func confirmCode(t *testing.T, svc *Service, user, code string) JoinResult {
	t.Helper()
	p, err := svc.PreviewJoin(context.Background(), user, code)
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.ConfirmJoin(context.Background(), user, code, p.Kind, p.TargetID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAdmissionPendingApprovalAndMultipleClasses(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	svc := NewService(pool)
	admissionPolicy(t, pool, f.InstitutionID, AdmissionPolicy{Mode: "verify_first"})
	code := joinCode(t, pool, f.GroupID)
	r := confirmCode(t, svc, f.SoloStudentID, code)
	if r.Status != "pending" || r.Enrollment != nil {
		t.Fatalf("unexpected result %+v", r)
	}
	e, err := svc.ActiveByUser(ctx, f.SoloStudentID)
	if err != nil || e != nil {
		t.Fatalf("pending granted access: %+v %v", e, err)
	}
	again := confirmCode(t, svc, f.SoloStudentID, code)
	if again.Destination.RequestID != r.Destination.RequestID {
		t.Fatal("duplicate request")
	}
	var group string
	if err = pool.QueryRow(ctx, `INSERT INTO groups(institution_id,name,invite_code) VALUES($1,'Second class',gen_random_uuid()::text) RETURNING id`, f.InstitutionID).Scan(&group); err != nil {
		t.Fatal(err)
	}
	second := confirmCode(t, svc, f.SoloStudentID, joinCode(t, pool, group))
	if second.Destination.RequestID != r.Destination.RequestID {
		t.Fatal("classes not grouped")
	}
	// Policy changes cannot auto-approve an existing request.
	admissionPolicy(t, pool, f.InstitutionID, AdmissionPolicy{Mode: "allow_all"})
	if confirmCode(t, svc, f.SoloStudentID, code).Status != "pending" {
		t.Fatal("policy change bypassed review")
	}
	if err = svc.ActOnRequest(ctx, r.Destination.RequestID, "", f.OtherInstitutionID, f.TeacherID, "approve", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-institute review: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err = svc.ActOnRequest(ctx, r.Destination.RequestID, "", f.InstitutionID, f.TeacherID, "approve", ""); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM group_students WHERE user_id=$1`, f.SoloStudentID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("class count %d: %v", count, err)
	}
}

func TestAdmissionCustomRules(t *testing.T) {
	for _, tc := range []struct {
		name    string
		p       AdmissionPolicy
		pending bool
	}{
		{"domain", AdmissionPolicy{Mode: "custom", EmailDomains: []string{"example.test"}}, false},
		{"unmatched", AdmissionPolicy{Mode: "custom", EmailDomains: []string{"school.test"}}, true},
		{"all", AdmissionPolicy{Mode: "custom", Match: "all", EmailDomains: []string{"example.test"}, Emails: []string{"other@example.test"}}, true},
		{"any", AdmissionPolicy{Mode: "custom", Match: "any", EmailDomains: []string{"example.test"}, Emails: []string{"other@example.test"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := openTestDB(t)
			f := seedFixture(t, pool)
			admissionPolicy(t, pool, f.InstitutionID, tc.p)
			r := confirmCode(t, NewService(pool), f.SoloStudentID, joinCode(t, pool, f.GroupID))
			if (r.Status == "pending") != tc.pending {
				t.Fatalf("status %s", r.Status)
			}
		})
	}
}

func TestAdmissionTransferNeedsStudentConfirmation(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	svc := NewService(pool)
	var destGroup string
	if err := pool.QueryRow(ctx, `INSERT INTO groups(institution_id,name,invite_code) VALUES($1,'Destination',gen_random_uuid()::text) RETURNING id`, f.OtherInstitutionID).Scan(&destGroup); err != nil {
		t.Fatal(err)
	}
	r := confirmCode(t, svc, f.StudentID, joinCode(t, pool, destGroup))
	if r.Status != "pending" || !r.Destination.TransferRequired {
		t.Fatalf("transfer %+v", r)
	}
	if err := svc.ActOnRequest(ctx, r.Destination.RequestID, f.StudentID, "", "", "complete", ""); !errors.Is(err, ErrRequestClosed) {
		t.Fatalf("unapproved transfer: %v", err)
	}
	if err := svc.ActOnRequest(ctx, r.Destination.RequestID, "", f.OtherInstitutionID, f.TeacherID, "approve", ""); err != nil {
		t.Fatal(err)
	}
	e, err := svc.ActiveByUser(ctx, f.StudentID)
	if err != nil || e.InstitutionID != f.InstitutionID {
		t.Fatalf("approval switched institute: %+v %v", e, err)
	}
	for i := 0; i < 2; i++ {
		if err = svc.ActOnRequest(ctx, r.Destination.RequestID, f.StudentID, "", "", "complete", ""); err != nil {
			t.Fatal(err)
		}
	}
	e, err = svc.ActiveByUser(ctx, f.StudentID)
	if err != nil || e.InstitutionID != f.OtherInstitutionID {
		t.Fatalf("transfer not completed: %+v %v", e, err)
	}
	var oldAccess bool
	if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM group_students WHERE user_id=$1 AND group_id=$2)`, f.StudentID, f.GroupID).Scan(&oldAccess); err != nil || oldAccess {
		t.Fatalf("old access: %v %v", oldAccess, err)
	}
	// An old admin cannot clear the destination membership through a historic row.
	if err = svc.SetStatus(ctx, f.InstitutionID, f.StudentEnrollmentID, "graduated"); !errors.Is(err, ErrEnrollmentExists) {
		t.Fatalf("old lifecycle mutation: %v", err)
	}
}

func TestAdmissionInvalidatedInviteAndCancellation(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	svc := NewService(pool)
	admissionPolicy(t, pool, f.InstitutionID, AdmissionPolicy{Mode: "verify_first"})
	r := confirmCode(t, svc, f.SoloStudentID, joinCode(t, pool, f.GroupID))
	if _, err := pool.Exec(ctx, `UPDATE groups SET archived_at=now() WHERE id=$1`, f.GroupID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ActOnRequest(ctx, r.Destination.RequestID, "", f.InstitutionID, f.TeacherID, "approve", ""); !errors.Is(err, ErrJoinChanged) {
		t.Fatalf("archived class: %v", err)
	}
	if e, err := svc.ActiveByUser(ctx, f.SoloStudentID); err != nil || e != nil {
		t.Fatalf("orphan membership: %+v %v", e, err)
	}
	if err := svc.ActOnRequest(ctx, r.Destination.RequestID, f.StudentID, "", "", "cancel", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another user cancelled: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.ActOnRequest(ctx, r.Destination.RequestID, f.SoloStudentID, "", "", "cancel", ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAdmissionConcurrentJoinAcrossInstitutes(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	var otherGroup string
	if err := pool.QueryRow(ctx, `INSERT INTO groups(institution_id,name,invite_code) VALUES($1,'Other',gen_random_uuid()::text) RETURNING id`, f.OtherInstitutionID).Scan(&otherGroup); err != nil {
		t.Fatal(err)
	}
	codes := []string{joinCode(t, pool, f.GroupID), joinCode(t, pool, otherGroup)}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, code := range codes {
		p, err := svc.PreviewJoin(ctx, f.SoloStudentID, code)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(code string, p JoinPreview) {
			defer wg.Done()
			_, err := svc.ConfirmJoin(ctx, f.SoloStudentID, code, p.Kind, p.TargetID)
			errs <- err
		}(code, p)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var live int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM enrollments WHERE user_id=$1 AND status='active'`, f.SoloStudentID).Scan(&live); err != nil || live != 1 {
		t.Fatalf("live=%d err=%v", live, err)
	}
	var memberships int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_students WHERE user_id=$1`, f.SoloStudentID).Scan(&memberships); err != nil || memberships != 1 {
		t.Fatalf("memberships=%d err=%v", memberships, err)
	}
}

func TestAdmissionPolicyValidation(t *testing.T) {
	for _, p := range []AdmissionPolicy{{Mode: "unknown", Match: "any"}, {Mode: "custom", Match: "any"}, {Mode: "custom", Match: "all", EmailDomains: []string{"@school.edu"}}, {Mode: "custom", Match: "any", Emails: []string{"not-an-email"}}} {
		if p.Validate() == nil {
			t.Fatalf("accepted %+v", p)
		}
	}
}

func TestAdmissionRosterMatchClaimsExistingRecord(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	svc := NewService(pool)
	if _, err := pool.Exec(ctx, `UPDATE enrollments SET email=(SELECT email FROM users WHERE id=$1) WHERE id=$2`, f.SoloStudentID, f.UnclaimedEnrollmentID); err != nil {
		t.Fatal(err)
	}
	admissionPolicy(t, pool, f.InstitutionID, AdmissionPolicy{Mode: "custom", RosterMatch: true})
	r := confirmCode(t, svc, f.SoloStudentID, joinCode(t, pool, f.GroupID))
	if r.Status != "joined" || r.Enrollment.ID != f.UnclaimedEnrollmentID {
		t.Fatalf("roster record not claimed: %+v", r)
	}
}

func TestAdmissionPartialApprovalAndQueueScope(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	svc := NewService(pool)
	admissionPolicy(t, pool, f.InstitutionID, AdmissionPolicy{Mode: "verify_first"})
	r := confirmCode(t, svc, f.SoloStudentID, joinCode(t, pool, f.GroupID))
	var second string
	if err := pool.QueryRow(ctx, `INSERT INTO groups(institution_id,name,invite_code) VALUES($1,'Archived class',gen_random_uuid()::text) RETURNING id`, f.InstitutionID).Scan(&second); err != nil {
		t.Fatal(err)
	}
	confirmCode(t, svc, f.SoloStudentID, joinCode(t, pool, second))
	if _, err := pool.Exec(ctx, `UPDATE groups SET archived_at=now() WHERE id=$1`, second); err != nil {
		t.Fatal(err)
	}
	if err := svc.ActOnRequest(ctx, r.Destination.RequestID, "", f.InstitutionID, f.TeacherID, "approve", "Welcome"); err != nil {
		t.Fatal(err)
	}
	requests, err := svc.Requests(ctx, f.SoloStudentID, "", "", 0)
	if err != nil || len(requests) != 1 || requests[0].Status != "joined" || requests[0].Reason != "Welcome" {
		t.Fatalf("requests %+v err %v", requests, err)
	}
	unavailable := 0
	for _, target := range requests[0].Targets {
		if target.Outcome == "unavailable" {
			unavailable++
		}
	}
	if unavailable != 1 {
		t.Fatalf("unavailable=%d", unavailable)
	}
	other, err := svc.Requests(ctx, "", f.OtherInstitutionID, "open", 0)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross institute list %+v %v", other, err)
	}
}

func TestAdmissionSuspensionAndPendingElsewhere(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	svc := NewService(pool)
	admissionPolicy(t, pool, f.InstitutionID, AdmissionPolicy{Mode: "verify_first"})
	r := confirmCode(t, svc, f.SoloStudentID, joinCode(t, pool, f.GroupID))
	var otherCode string
	if err := pool.QueryRow(ctx, `SELECT student_referral_code FROM institutions WHERE id=$1`, f.OtherInstitutionID).Scan(&otherCode); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PreviewJoin(ctx, f.SoloStudentID, otherCode); !errors.Is(err, ErrPendingElsewhere) {
		t.Fatalf("another pending allowed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET status='suspended' WHERE id=$1`, f.SoloStudentID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ActOnRequest(ctx, r.Destination.RequestID, "", f.InstitutionID, f.TeacherID, "approve", ""); !errors.Is(err, ErrJoinSuspended) {
		t.Fatalf("suspended approval: %v", err)
	}
}
