// Package jobs implements a PostgreSQL transactional outbox with leased workers.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}
type Job struct {
	ID, Kind, Owner, Token string
	Payload                json.RawMessage
	Attempts               int
}
type Handler func(context.Context, Job) (any, error)
type TxHandler func(context.Context, pgx.Tx, Job) (any, error)
type Queue struct {
	db            *pgxpool.Pool
	handlers      map[string]Handler
	transactional map[string]TxHandler
	wg            sync.WaitGroup
}

func New(db *pgxpool.Pool) *Queue {
	return &Queue{db: db, handlers: map[string]Handler{}, transactional: map[string]TxHandler{}}
}

// Register all handlers before Start. External handlers must tolerate redelivery.
func (q *Queue) Register(kind string, h Handler)     { q.handlers[kind] = h }
func (q *Queue) RegisterTx(kind string, h TxHandler) { q.transactional[kind] = h }
func Enqueue(ctx context.Context, db Execer, kind, key, owner string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = db.Exec(ctx, `INSERT INTO background_jobs(kind,dedupe_key,owner_id,payload) VALUES($1,$2,NULLIF($3,''),$4) ON CONFLICT(kind,dedupe_key) DO NOTHING`, kind, key, owner, raw)
	return err
}
func (q *Queue) Start(ctx context.Context, workers int) {
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				maintenance, cancel := context.WithTimeout(ctx, 10*time.Second)
				_, err := q.db.Exec(maintenance, `DELETE FROM distributed_rate_limits WHERE tat<now()-interval '1 day'`)
				if err == nil {
					_, err = q.db.Exec(maintenance, `DELETE FROM background_jobs WHERE id IN(SELECT id FROM background_jobs WHERE state='completed' AND updated_at<now()-interval '30 days' ORDER BY updated_at LIMIT 1000)`)
				}
				if err != nil && ctx.Err() == nil {
					log.Printf("jobs maintenance: %v", err)
				}
				cancel()
			}
		}
	}()

	critical, delivery, generation := []string{}, []string{}, []string{}
	for k := range q.handlers {
		if k == "question_generation" {
			generation = append(generation, k)
		} else {
			delivery = append(delivery, k)
		}
	}
	for k := range q.transactional {
		if k == "announcement_notification" {
			delivery = append(delivery, k)
		} else {
			critical = append(critical, k)
		}
	}
	groups := [][]string{}
	for _, g := range [][]string{critical, delivery, generation} {
		if len(g) > 0 {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return
	}
	for i := range max(workers, len(groups)) {
		kinds := groups[i%len(groups)]
		q.wg.Add(1)
		go func() {
			defer q.wg.Done()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				found := q.process(ctx, kinds)
				if !found {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
					}
				}
			}
		}()
	}
}
func (q *Queue) Wait() { q.wg.Wait() }
func (q *Queue) process(ctx context.Context, kinds []string) bool {
	_, cleanupErr := q.db.Exec(ctx, `UPDATE background_jobs SET state='failed',last_error=COALESCE(last_error,'lease expired after final attempt'),updated_at=now() WHERE state='running' AND lease_until<now() AND attempts>=max_attempts`)
	if cleanupErr != nil {
		return false
	}
	var j Job
	j.Token = uuid.NewString()
	err := q.db.QueryRow(ctx, `WITH candidate AS (
 SELECT id FROM background_jobs WHERE kind=ANY($1) AND attempts<max_attempts
 AND ((state='pending' AND available_at<=now()) OR (state='running' AND lease_until<now()))
 ORDER BY available_at,id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE background_jobs j SET state='running',attempts=attempts+1,lease_token=$2::uuid,lease_until=now()+interval '3 minutes',updated_at=now()
 FROM candidate c WHERE j.id=c.id RETURNING j.id::text,j.kind,COALESCE(j.owner_id,''),j.payload,j.attempts`, kinds, j.Token).Scan(&j.ID, &j.Kind, &j.Owner, &j.Payload, &j.Attempts)
	if err == pgx.ErrNoRows {
		return false
	}
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("jobs: claim: %v", err)
		}
		return false
	}
	jobCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var result any
	if h, ok := q.transactional[j.Kind]; ok {
		var tx pgx.Tx
		tx, err = q.db.Begin(jobCtx)
		if err == nil {
			defer tx.Rollback(context.Background())
			var token string
			err = tx.QueryRow(jobCtx, `SELECT lease_token::text FROM background_jobs WHERE id=$1 AND state='running' FOR UPDATE`, j.ID).Scan(&token)
			if err == nil && token != j.Token {
				return true
			}
			if err == nil {
				result, err = invoke(func() (any, error) { return h(jobCtx, tx, j) })
			}
			if err == nil {
				err = finish(jobCtx, tx, j, result)
			}
			if err == nil {
				err = tx.Commit(jobCtx)
			} else {
				_ = tx.Rollback(context.Background())
			}
		}
	} else {
		result, err = invoke(func() (any, error) { return q.handlers[j.Kind](jobCtx, j) })
		if err == nil {
			err = finish(jobCtx, q.db, j, result)
		}
	}
	if err != nil {
		// A fresh bounded context releases the lease even after cancellation.
		retryCtx, retryCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer retryCancel()
		_, retryErr := q.db.Exec(retryCtx, `UPDATE background_jobs SET state=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'pending' END,
  available_at=now()+make_interval(secs=>$3),lease_until=NULL,lease_token=NULL,last_error=$4,updated_at=now()
  WHERE id=$1 AND lease_token=$2::uuid`, j.ID, j.Token, min(300, 1<<min(j.Attempts, 8)), truncate(err.Error(), 1000))
		if retryErr != nil {
			log.Printf("jobs: release %s: %v", j.ID, retryErr)
		}
		log.Printf("jobs: %s attempt %d failed: %v", j.Kind, j.Attempts, err)
	}
	return true
}
func finish(ctx context.Context, db Execer, j Job, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tag, err := db.Exec(ctx, `UPDATE background_jobs SET state='completed',result=$3,lease_until=NULL,lease_token=NULL,last_error=NULL,updated_at=now() WHERE id=$1 AND lease_token=$2::uuid`, j.ID, j.Token, raw)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("job lease lost")
	}
	return err
}
func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// A malformed job must enter the retry/dead-letter path without killing the API.
func invoke(fn func() (any, error)) (result any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("job handler panicked: %v", p)
		}
	}()
	return fn()
}
