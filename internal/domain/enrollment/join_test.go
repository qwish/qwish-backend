package enrollment

import (
	"context"
	"errors"
	"testing"
)

func TestGuidedJoinClaimAndRetry(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	p, err := svc.PreviewJoin(ctx, f.SoloStudentID, f.ClaimCode)
	if err != nil || p.Kind != "claim" || p.TargetID != f.UnclaimedEnrollmentID || p.AlreadyJoined {
		t.Fatalf("preview: %+v, %v", p, err)
	}
	result, err := svc.ConfirmJoin(ctx, f.SoloStudentID, f.ClaimCode, p.Kind, p.TargetID)
	if err != nil || result.Enrollment.ID != f.UnclaimedEnrollmentID {
		t.Fatalf("confirm: %+v, %v", result, err)
	}
	result, err = svc.ConfirmJoin(ctx, f.SoloStudentID, f.ClaimCode, p.Kind, p.TargetID)
	if err != nil || result.Enrollment.ID != f.UnclaimedEnrollmentID {
		t.Fatalf("retry: %+v, %v", result, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM enrollments WHERE user_id=$1 AND status='active'`, f.SoloStudentID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("active count = %d, %v", count, err)
	}
}

func TestGuidedJoinClassAddsExistingInstitutionStudent(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	var code string
	if err := pool.QueryRow(ctx, `SELECT invite_code FROM groups WHERE id=$1`, f.GroupID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	// A student can be enrolled at this institution without belonging to this class.
	if _, err := pool.Exec(ctx, `DELETE FROM group_students WHERE group_id=$1 AND user_id=$2`, f.GroupID, f.StudentID); err != nil {
		t.Fatal(err)
	}
	p, err := svc.PreviewJoin(ctx, f.StudentID, code)
	if err != nil || p.Kind != "class" || p.AlreadyJoined {
		t.Fatalf("preview: %+v, %v", p, err)
	}
	result, err := svc.ConfirmJoin(ctx, f.StudentID, code, p.Kind, p.TargetID)
	if err != nil || result.Enrollment.ID != f.StudentEnrollmentID {
		t.Fatalf("confirm: %+v, %v", result, err)
	}
	p, err = svc.PreviewJoin(ctx, f.StudentID, code)
	if err != nil || !p.AlreadyJoined {
		t.Fatalf("joined preview: %+v, %v", p, err)
	}
	result, err = svc.ConfirmJoin(ctx, f.StudentID, code, p.Kind, p.TargetID)
	if err != nil || result.Enrollment.ID != f.StudentEnrollmentID {
		t.Fatalf("retry: %+v, %v", result, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_students WHERE group_id=$1 AND user_id=$2`, f.GroupID, f.StudentID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("membership count = %d, %v", count, err)
	}
}

func TestGuidedJoinCannotMoveInstitution(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	p, err := svc.PreviewJoin(ctx, f.StudentID, f.ClaimCode)
	if !errors.Is(err, ErrEnrollmentExists) {
		t.Fatalf("preview: %+v, %v", p, err)
	}
	_, err = svc.ConfirmJoin(ctx, f.StudentID, f.ClaimCode, "claim", f.UnclaimedEnrollmentID)
	if !errors.Is(err, ErrEnrollmentExists) {
		t.Fatalf("confirm: %v", err)
	}
}

func TestGuidedJoinInstitutionCode(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	var code string
	if err := pool.QueryRow(ctx, `SELECT student_referral_code FROM institutions WHERE id=$1`, f.InstitutionID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	p, err := svc.PreviewJoin(ctx, f.SoloStudentID, code)
	if err != nil || p.Kind != "institution" || p.AlreadyJoined {
		t.Fatalf("preview: %+v, %v", p, err)
	}
	result, err := svc.ConfirmJoin(ctx, f.SoloStudentID, code, p.Kind, p.TargetID)
	if err != nil || result.Enrollment.InstitutionID != f.InstitutionID {
		t.Fatalf("confirm: %+v, %v", result, err)
	}
	p, err = svc.PreviewJoin(ctx, f.SoloStudentID, code)
	if err != nil || !p.AlreadyJoined {
		t.Fatalf("retry preview: %+v, %v", p, err)
	}
}

func TestGuidedJoinClassCodeCreatesEnrollmentAndMembership(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	svc := NewService(pool)
	ctx := context.Background()
	var code string
	if err := pool.QueryRow(ctx, `SELECT invite_code FROM groups WHERE id=$1`, f.GroupID).Scan(&code); err != nil {
		t.Fatal(err)
	}
	p, err := svc.PreviewJoin(ctx, f.SoloStudentID, code)
	if err != nil || p.Kind != "class" || p.AlreadyJoined {
		t.Fatalf("preview: %+v, %v", p, err)
	}
	result, err := svc.ConfirmJoin(ctx, f.SoloStudentID, code, p.Kind, p.TargetID)
	if err != nil || result.Enrollment.InstitutionID != f.InstitutionID {
		t.Fatalf("confirm: %+v, %v", result, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM group_students WHERE group_id=$1 AND user_id=$2`, f.GroupID, f.SoloStudentID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("membership count = %d, %v", count, err)
	}
}
