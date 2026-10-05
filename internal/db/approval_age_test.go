package db

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Approval age is measured from submitted_for_approval_at, which the database
// stamps on every move into pending_approval — whichever code path makes it.
func TestSubmittedForApprovalAtStampsEveryEntry(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	var userID, quizID string
	if err := tx.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), 'a', 'a', 'approval-'||gen_random_uuid()||'@example.test', 'teacher') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO quizzes (created_by, title, type, visibility, status)
		VALUES ($1, 'q', 'knowledge_check', 'public', 'draft') RETURNING id`, userID).Scan(&quizID); err != nil {
		t.Fatal(err)
	}
	stamp := func() (set bool) {
		t.Helper()
		if err := tx.QueryRow(ctx, `SELECT submitted_for_approval_at IS NOT NULL FROM quizzes WHERE id=$1`, quizID).Scan(&set); err != nil {
			t.Fatal(err)
		}
		return set
	}
	if stamp() {
		t.Fatal("draft quiz already has a submission time")
	}
	if _, err := tx.Exec(ctx, `UPDATE quizzes SET status='pending_approval', updated_at=now() - interval '1 day' WHERE id=$1`, quizID); err != nil {
		t.Fatal(err)
	}
	if !stamp() {
		t.Fatal("move into pending_approval did not stamp submitted_for_approval_at")
	}
	// Resubmission after rejection restarts the clock; edits while pending do not.
	var first, again, edited string
	tx.QueryRow(ctx, `UPDATE quizzes SET submitted_for_approval_at = now() - interval '3 days' WHERE id=$1 RETURNING submitted_for_approval_at::text`, quizID).Scan(&first)
	tx.QueryRow(ctx, `UPDATE quizzes SET title='edited' WHERE id=$1 RETURNING submitted_for_approval_at::text`, quizID).Scan(&edited)
	if edited != first {
		t.Errorf("edit while pending moved the clock: %s -> %s", first, edited)
	}
	tx.Exec(ctx, `UPDATE quizzes SET status='rejected' WHERE id=$1`, quizID)
	tx.QueryRow(ctx, `UPDATE quizzes SET status='pending_approval' WHERE id=$1 RETURNING submitted_for_approval_at::text`, quizID).Scan(&again)
	if again == first {
		t.Error("resubmission did not restart the clock")
	}
}
