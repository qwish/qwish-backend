package enrollment

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func columnExists(t *testing.T, table, col string) bool {
	t.Helper()
	pool := openTestDB(t)
	var ok bool
	pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name=$1 AND column_name=$2)`, table, col).Scan(&ok)
	return ok
}

func TestRosterRowsBecomeInvites(t *testing.T) {
	if !columnExists(t, "enrollments", "claim_code") {
		t.Skip("085 already applied; data step tested pre-migration")
	}
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	d := fmt.Sprintf("r%d.edu", time.Now().UnixNano())
	pool.Exec(ctx, `INSERT INTO institution_domains (institution_id, domain, verified_at) VALUES ($1,$2,now())`, f.InstitutionID, d)
	pool.Exec(ctx, `INSERT INTO enrollments (institution_id, full_name, email, status) VALUES ($1,'a',$2,'pending_claim'),($1,'b',upper($2),'pending_claim'),($1,'c','c@gmail.com','pending_claim')`, f.InstitutionID, "x@"+d)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM student_invites WHERE institution_id=$1`, f.InstitutionID)
		pool.Exec(ctx, `DELETE FROM institution_domains WHERE domain=$1`, d)
	})
	if _, err := pool.Exec(ctx, `SELECT retire_roster_rows()`); err != nil {
		t.Fatal(err)
	}
	var invites, left int
	pool.QueryRow(ctx, `SELECT count(*) FROM student_invites WHERE institution_id=$1 AND email=$2 AND group_id IS NULL`, f.InstitutionID, "x@"+d).Scan(&invites)
	pool.QueryRow(ctx, `SELECT count(*) FROM enrollments WHERE institution_id=$1 AND status='pending_claim'`, f.InstitutionID).Scan(&left)
	if invites != 1 || left != 0 {
		t.Fatalf("invites=%d remaining roster=%d", invites, left)
	}
}

func TestPromotedRosterRowIsDeleted(t *testing.T) {
	if !columnExists(t, "enrollments", "claim_code") {
		t.Skip("085 already applied")
	}
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	var batch string
	pool.QueryRow(ctx, `INSERT INTO promotion_batches (institution_id, performed_by, to_grade) VALUES ($1,$2,'10') RETURNING id`, f.InstitutionID, f.TeacherID).Scan(&batch)
	pool.Exec(ctx, `INSERT INTO promotion_batch_students (batch_id, enrollment_id, outcome) VALUES ($1,$2,'promoted')`, batch, f.UnclaimedEnrollmentID)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM promotion_batches WHERE id=$1`, batch) })
	if _, err := pool.Exec(ctx, `SELECT retire_roster_rows()`); err != nil {
		t.Fatalf("FK must not block deleting a promoted roster row: %v", err)
	}
}

func TestClaimedEnrollmentSurvives(t *testing.T) {
	pool := openTestDB(t)
	f := seedFixture(t, pool)
	ctx := context.Background()
	if columnExists(t, "enrollments", "claim_code") {
		pool.Exec(ctx, `SELECT retire_roster_rows()`)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM enrollments WHERE id=$1`, f.StudentEnrollmentID).Scan(&status); err != nil || status != "active" {
		t.Fatalf("claimed enrollment: status=%q err=%v", status, err)
	}
}
