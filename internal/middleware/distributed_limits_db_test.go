package middleware

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDistributedGCRAIsAtomicAcrossConnections(t *testing.T) {
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
	defer p.Exec(ctx, `DELETE FROM distributed_rate_limits WHERE namespace=$1`, key)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var allowed bool
			var retry float64
			var remaining int
			err := p.QueryRow(ctx, `SELECT * FROM consume_rate_limit($1,'client',5,3600)`, key).Scan(&allowed, &retry, &remaining)
			if err != nil {
				t.Error(err)
			}
			if allowed {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 5 {
		t.Fatalf("allowed %d requests, expected 5", accepted.Load())
	}
}
