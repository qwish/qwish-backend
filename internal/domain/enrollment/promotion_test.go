package enrollment

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// promoFixture is one institution, two classes, and two students in the first.
type promoFixture struct {
	InstitutionID string
	SourceGroupID string
	TargetGroupID string
	// Enrollment ids, both starting in Grade 9 / section A and in the source class.
	MoverEnrollmentID  string
	StayerEnrollmentID string
	MoverUserID        string
	StayerUserID       string
}

func seedPromoFixture(t *testing.T, pool *pgxpool.Pool) promoFixture {
	t.Helper()
	ctx := context.Background()
	var f promoFixture

	if err := pool.QueryRow(ctx,
		`INSERT INTO institutions (name, type, timezone, student_referral_code, teacher_referral_code, status)
		 VALUES ('Promo Test','School','Asia/Kolkata',$1,$2,'verified') RETURNING id`,
		"S"+uuid.NewString()[:9], "T"+uuid.NewString()[:9],
	).Scan(&f.InstitutionID); err != nil {
		t.Fatalf("seed institution: %v", err)
	}

	for _, g := range []struct {
		name string
		dest *string
	}{{"Grade 9A", &f.SourceGroupID}, {"Grade 10A", &f.TargetGroupID}} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO groups (institution_id, name, invite_code) VALUES ($1,$2,$3) RETURNING id`,
			f.InstitutionID, g.name, uuid.NewString()[:8],
		).Scan(g.dest); err != nil {
			t.Fatalf("seed group %s: %v", g.name, err)
		}
	}

	for _, s := range []struct {
		name    string
		enrDest *string
		usrDest *string
	}{{"Mover", &f.MoverEnrollmentID, &f.MoverUserID}, {"Stayer", &f.StayerEnrollmentID, &f.StayerUserID}} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
			 VALUES ($1,$2,$2,$3,'student',$4) RETURNING id`,
			uuid.NewString(), s.name, "promo-"+uuid.NewString()[:8]+"@example.test", f.InstitutionID,
		).Scan(s.usrDest); err != nil {
			t.Fatalf("seed user %s: %v", s.name, err)
		}
		if err := pool.QueryRow(ctx,
			`INSERT INTO enrollments (institution_id, user_id, full_name, grade, section, status, joined_at)
			 VALUES ($1,$2,$3,'9','A','active',now()) RETURNING id`,
			f.InstitutionID, *s.usrDest, s.name,
		).Scan(s.enrDest); err != nil {
			t.Fatalf("seed enrollment %s: %v", s.name, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO group_students (group_id, user_id) VALUES ($1,$2)`,
			f.SourceGroupID, *s.usrDest); err != nil {
			t.Fatalf("seed membership %s: %v", s.name, err)
		}
	}

	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM promotion_batch_students WHERE enrollment_id = ANY($1)`,
			[]string{f.MoverEnrollmentID, f.StayerEnrollmentID})
		pool.Exec(ctx, `DELETE FROM promotion_batches WHERE institution_id=$1`, f.InstitutionID)
		pool.Exec(ctx, `DELETE FROM group_students WHERE user_id = ANY($1)`,
			[]string{f.MoverUserID, f.StayerUserID})
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, f.InstitutionID)
		pool.Exec(ctx, `DELETE FROM streaks WHERE user_id = ANY($1)`, []string{f.MoverUserID, f.StayerUserID})
		pool.Exec(ctx, `DELETE FROM audit_log WHERE institution_id=$1`, f.InstitutionID)
		pool.Exec(ctx, `DELETE FROM users WHERE institution_id=$1`, f.InstitutionID)
		pool.Exec(ctx, `DELETE FROM groups WHERE institution_id=$1`, f.InstitutionID)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, f.InstitutionID)
	})
	return f
}

func TestPromoteBatchMovesOnlyTheChosen(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool)
	f := seedPromoFixture(t, pool)

	res, err := svc.PromoteBatch(ctx, f.InstitutionID, f.MoverUserID, PromotionRequest{
		SourceGroupID: f.SourceGroupID,
		TargetGroupID: f.TargetGroupID,
		ToGrade:       "10",
		ToSection:     "A",
		Promote:       []string{f.MoverEnrollmentID},
		Retained: []RetainedStudent{
			{EnrollmentID: f.StayerEnrollmentID, Reason: "Average score below 40%"},
		},
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if res.Promoted != 1 || res.Retained != 1 {
		t.Fatalf("promoted=%d retained=%d, want 1 and 1", res.Promoted, res.Retained)
	}

	// The chosen student advanced and changed class.
	var grade string
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, f.MoverEnrollmentID).Scan(&grade)
	if grade != "10" {
		t.Errorf("mover grade = %q, want 10", grade)
	}
	var inTarget, inSource int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM group_students WHERE user_id=$1 AND group_id=$2`,
		f.MoverUserID, f.TargetGroupID).Scan(&inTarget)
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM group_students WHERE user_id=$1 AND group_id=$2`,
		f.MoverUserID, f.SourceGroupID).Scan(&inSource)
	if inTarget != 1 || inSource != 0 {
		t.Errorf("mover memberships: target=%d source=%d, want 1 and 0", inTarget, inSource)
	}

	// The student left behind is untouched — that is the whole point of choosing.
	var stayerGrade string
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, f.StayerEnrollmentID).Scan(&stayerGrade)
	if stayerGrade != "9" {
		t.Errorf("retained student's grade changed to %q", stayerGrade)
	}

	// And the reason they stayed survives.
	var reason string
	if err := pool.QueryRow(ctx,
		`SELECT retained_reason FROM promotion_batch_students
		  WHERE batch_id=$1 AND enrollment_id=$2 AND outcome='retained'`,
		res.BatchID, f.StayerEnrollmentID).Scan(&reason); err != nil {
		t.Fatalf("no retained record: %v", err)
	}
	if reason == "" {
		t.Error("retained student recorded with no reason")
	}
}

func TestRevertSkipsStudentsMovedSince(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool)
	f := seedPromoFixture(t, pool)

	res, err := svc.PromoteBatch(ctx, f.InstitutionID, f.MoverUserID, PromotionRequest{
		SourceGroupID: f.SourceGroupID,
		TargetGroupID: f.TargetGroupID,
		ToGrade:       "10",
		ToSection:     "A",
		Promote:       []string{f.MoverEnrollmentID, f.StayerEnrollmentID},
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	// Someone corrects one student by hand after the promotion. The revert must
	// leave that deliberate change alone.
	if _, err := pool.Exec(ctx,
		`UPDATE enrollments SET grade='11' WHERE id=$1`, f.StayerEnrollmentID); err != nil {
		t.Fatalf("manual edit: %v", err)
	}

	rev, err := svc.RevertBatch(ctx, f.InstitutionID, f.MoverUserID, res.BatchID)
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if rev.Reverted != 1 || rev.Skipped != 1 {
		t.Fatalf("reverted=%d skipped=%d, want 1 and 1", rev.Reverted, rev.Skipped)
	}

	var moverGrade, stayerGrade string
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, f.MoverEnrollmentID).Scan(&moverGrade)
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, f.StayerEnrollmentID).Scan(&stayerGrade)
	if moverGrade != "9" {
		t.Errorf("untouched student not reverted: grade = %q, want 9", moverGrade)
	}
	if stayerGrade != "11" {
		t.Errorf("revert clobbered a later manual edit: grade = %q, want 11", stayerGrade)
	}

	// The mover is back in their old class.
	var inSource int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM group_students WHERE user_id=$1 AND group_id=$2`,
		f.MoverUserID, f.SourceGroupID).Scan(&inSource)
	if inSource != 1 {
		t.Errorf("reverted student not returned to the source class")
	}

	// A batch reverts once.
	if _, err := svc.RevertBatch(ctx, f.InstitutionID, f.MoverUserID, res.BatchID); err == nil {
		t.Error("a reverted batch was allowed to revert again")
	}
}

func TestPromoteBatchRefusesAnEmptySelection(t *testing.T) {
	pool := openTestDB(t)
	svc := NewService(pool)
	f := seedPromoFixture(t, pool)

	_, err := svc.PromoteBatch(context.Background(), f.InstitutionID, f.MoverUserID, PromotionRequest{
		SourceGroupID: f.SourceGroupID,
		TargetGroupID: f.TargetGroupID,
		ToGrade:       "10",
	})
	if err == nil {
		t.Fatal("a promotion with nobody selected was accepted")
	}
}

// An enrollment belonging to another institution must not move, even when its
// id is passed directly.
func TestPromoteBatchIgnoresForeignEnrollments(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool)
	mine := seedPromoFixture(t, pool)
	theirs := seedPromoFixture(t, pool)

	res, err := svc.PromoteBatch(ctx, mine.InstitutionID, mine.MoverUserID, PromotionRequest{
		SourceGroupID: mine.SourceGroupID,
		TargetGroupID: mine.TargetGroupID,
		ToGrade:       "10",
		Promote:       []string{mine.MoverEnrollmentID, theirs.MoverEnrollmentID},
	})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if res.Promoted != 1 {
		t.Errorf("promoted=%d, want 1 — a foreign enrollment was moved", res.Promoted)
	}

	var foreignGrade string
	pool.QueryRow(ctx, `SELECT grade FROM enrollments WHERE id=$1`, theirs.MoverEnrollmentID).Scan(&foreignGrade)
	if foreignGrade != "9" {
		t.Errorf("another institution's student was promoted to %q", foreignGrade)
	}
}
