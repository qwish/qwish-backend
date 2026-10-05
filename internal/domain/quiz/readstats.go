package quiz

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/qwish/backend/internal/jobs"
)

func RegisterJobs(q *jobs.Queue) {
	q.RegisterTx("quiz_stats", func(ctx context.Context, tx pgx.Tx, j jobs.Job) (any, error) {
		var p struct {
			QuizID string `json:"quiz_id"`
		}
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,1))`, p.QuizID); err != nil {
			return nil, err
		}
		// Capture pending IDs before rebuilding, so only changes included in the
		// rebuild are acknowledged. Other running workers retain their own leases.
		var pending []string
		if err := tx.QueryRow(ctx, `SELECT COALESCE(array_agg(id::text),'{}') FROM background_jobs WHERE kind='quiz_stats' AND state='pending' AND payload->>'quiz_id'=$1`, p.QuizID).Scan(&pending); err != nil {
			return nil, err
		}
		_, err := tx.Exec(ctx, `INSERT INTO quiz_read_stats(quiz_id,completions,completed_attempts,started_count,avg_score_pct,avg_seconds,difficulty,updated_at)
 SELECT q.id,a.users,a.completed,a.started,a.score,a.seconds,d.difficulty,now()
 FROM quizzes q CROSS JOIN LATERAL (
 SELECT count(*) started,count(*) FILTER(WHERE status='completed') completed,count(DISTINCT user_id) FILTER(WHERE status='completed') users,
 (avg(score_pct) FILTER(WHERE status='completed'))::float8 score,
 (avg(EXTRACT(epoch FROM completed_at-started_at)) FILTER(WHERE status='completed'))::float8 seconds
 FROM quiz_attempts WHERE quiz_id=q.id)a
 CROSS JOIN LATERAL (SELECT avg(difficulty)::float8 difficulty FROM questions WHERE quiz_id=q.id)d WHERE q.id=$1
 ON CONFLICT(quiz_id) DO UPDATE SET completions=EXCLUDED.completions,completed_attempts=EXCLUDED.completed_attempts,started_count=EXCLUDED.started_count,
 avg_score_pct=EXCLUDED.avg_score_pct,avg_seconds=EXCLUDED.avg_seconds,difficulty=EXCLUDED.difficulty,updated_at=now()`, p.QuizID)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE background_jobs SET state='completed',updated_at=now() WHERE id=ANY($1::uuid[]) AND state='pending'`, pending)
		return nil, err
	})
}
