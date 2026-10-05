package jobs

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
)

func TestTransactionalEffectRollsBackOnFailureAndRetryCommitsOnce(t *testing.T) {
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
	key := uuid.NewString()
	kind := "regression_" + key
	q := New(p)
	calls := 0
	q.RegisterTx(kind, func(ctx context.Context, tx pgx.Tx, j Job) (any, error) {
		calls++
		_, err := tx.Exec(ctx, `INSERT INTO distributed_rate_limits(namespace,client_key,tat) VALUES($1,'effect',now())`, key)
		if err != nil {
			return nil, err
		}
		if calls == 1 {
			return nil, errors.New("simulated failure after write")
		}
		return map[string]any{"ok": true}, nil
	})
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = Enqueue(ctx, tx, kind, key, "", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	var n int
	_ = p.QueryRow(ctx, `SELECT count(*) FROM background_jobs WHERE kind=$1`, kind).Scan(&n)
	if n != 0 {
		t.Fatal("rolled-back outbox escaped transaction")
	}
	if err = Enqueue(ctx, p, kind, key, "", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		p.Exec(ctx, `DELETE FROM background_jobs WHERE kind=$1`, kind)
		p.Exec(ctx, `DELETE FROM distributed_rate_limits WHERE namespace=$1`, key)
	}()
	if !q.process(ctx, []string{kind}) {
		t.Fatal("job not claimed")
	}
	_ = p.QueryRow(ctx, `SELECT count(*) FROM distributed_rate_limits WHERE namespace=$1`, key).Scan(&n)
	if n != 0 {
		t.Fatal("failed job committed its effect")
	}
	_, err = p.Exec(ctx, `UPDATE background_jobs SET available_at=now() WHERE kind=$1`, kind)
	if err != nil {
		t.Fatal(err)
	}
	if !q.process(ctx, []string{kind}) {
		t.Fatal("retry not claimed")
	}
	if q.process(ctx, []string{kind}) {
		t.Fatal("completed job was delivered again")
	}
	var state string
	_ = p.QueryRow(ctx, `SELECT state FROM background_jobs WHERE kind=$1`, kind).Scan(&state)
	if state != "completed" {
		t.Fatal(state)
	}
}
