package notification

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestNotificationOutboxRollbackAndTeacherDedupe(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var userID string
	ref := uuid.NewString()
	if err = p.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role) VALUES(gen_random_uuid(),'Teacher','T',$1,'teacher') RETURNING id`, ref+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		p.Exec(ctx, `DELETE FROM background_jobs WHERE owner_id=$1 OR dedupe_key LIKE $2`, userID, "teacher:"+userID+":%")
		p.Exec(ctx, `DELETE FROM user_notifications WHERE user_id=$1`, userID)
		p.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	}()
	s := NewService(p, "", "", "")
	s.SetPusher(func(context.Context, string, string, string, map[string]string) error { return nil })
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.EmitTx(ctx, tx, userID, "test", "Title", "Body", WithReference(ref)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM user_notifications WHERE user_id=$1`, userID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("notification survived rollback: %d %v", n, err)
	}
	if err = p.QueryRow(ctx, `SELECT count(*) FROM background_jobs WHERE owner_id=$1`, userID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("push job survived rollback: %d %v", n, err)
	}
	for range 2 {
		if err = s.emitTeacher(ctx, userID, TopicOverdueWork, "test", "Title", "Body", ref, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err = p.QueryRow(ctx, `SELECT count(*) FROM user_notifications WHERE user_id=$1 AND reference=$2`, userID, ref).Scan(&n); err != nil || n != 1 {
		t.Fatalf("teacher notification count: %d %v", n, err)
	}
	if err = p.QueryRow(ctx, `SELECT count(*) FROM background_jobs WHERE kind='push' AND owner_id=$1`, userID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("teacher push count: %d %v", n, err)
	}
	if err = p.QueryRow(ctx, `SELECT count(*) FROM background_jobs WHERE kind='email' AND dedupe_key=$1`, "teacher:"+userID+":"+ref+":"+ref+"@example.test").Scan(&n); err != nil || n != 1 {
		t.Fatalf("teacher email count: %d %v", n, err)
	}
}
