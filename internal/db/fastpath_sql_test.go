package db

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Several hot-path queries were collapsed from many sequential round trips into
// single statements built out of CTEs, LATERALs and unnest(). Those rewrites are
// only exercised by code paths that need a real database, so a plain `go test`
// would report success while shipping a statement Postgres cannot even parse.
//
// PREPARE asks the server to parse and plan a statement — resolving every table,
// column and parameter type — without executing it. That catches a typo'd column
// or a bad parameter reference in any of these without touching a single row.
//
// Run against a database with migrations applied:
//
//	TEST_DATABASE_URL=postgres://... go test ./internal/db -run PreparesOnServer
//
// ponytail: parse/plan check only. It proves the SQL is valid against the live
// schema, not that the arithmetic is right — the point-expiry maths and the
// badge predicates still need their own behavioural tests if they ever change.
var fastPathStatements = map[string]string{
	"attempt.Complete/preamble": `
		SELECT
		  CASE WHEN $4 = 'knowledge_check' THEN EXISTS (
		    SELECT 1 FROM quiz_attempts
		     WHERE quiz_id=$2 AND user_id=$1 AND status='completed' AND id <> $3
		  ) ELSE false END,
		  COALESCE((SELECT current_streak FROM streaks WHERE user_id=$1), 0),
		  (SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed'),
		  COALESCE((SELECT i.point_multiplier FROM users u
		              JOIN institutions i ON i.id = u.institution_id
		             WHERE u.id=$1), 1.0)`,

	"attempt.Complete/commit": `
		WITH att AS (
		  UPDATE quiz_attempts
		     SET status='completed', score_pct=$1, points_delta=$2,
		         total_correct=$3, total_questions=$4, completed_at=now()
		   WHERE id=$5
		), bal AS (
		  UPDATE users
		     SET total_points = GREATEST(0, total_points + $2), updated_at=now()
		   WHERE id=$6 AND NOT $7::bool
		  RETURNING total_points
		), led AS (
		  INSERT INTO points_ledger (user_id, amount, reason, reference_id, balance_after, expires_at)
		  SELECT $6, $2, 'quiz_attempt', $5, total_points, $8 FROM bal
		)
		SELECT COALESCE(
		  (SELECT total_points FROM bal),
		  (SELECT total_points FROM users WHERE id=$6)
		)`,

	"attempt.Complete/streakBonus": `
		WITH bal AS (
		  UPDATE users SET total_points = total_points + $1, updated_at=now()
		   WHERE id=$2 RETURNING total_points
		)
		INSERT INTO points_ledger (user_id, amount, reason, balance_after, expires_at)
		SELECT $2, $1, 'streak_bonus', total_points, $3 FROM bal`,

	"attempt.checkBadges/read": `
		SELECT
		  (SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed'),
		  (SELECT COUNT(DISTINCT q.type) FROM question_responses qr
		     JOIN questions q ON q.id = qr.question_id
		     JOIN quiz_attempts qa ON qa.id = qr.attempt_id
		    WHERE qa.user_id=$1 AND qa.status='completed'),
		  (SELECT COALESCE(MAX(qr.combo_level), 0) FROM question_responses qr
		     JOIN questions q ON q.id = qr.question_id
		    WHERE qr.attempt_id=$2 AND q.type='speed_chain'),
		  (SELECT COUNT(*) FROM question_responses qr
		     JOIN questions q ON q.id = qr.question_id
		    WHERE qr.attempt_id=$2 AND q.type='confidence_based'),
		  (SELECT COALESCE(SUM(CASE WHEN qr.is_correct THEN 1 ELSE 0 END), 0)
		     FROM question_responses qr JOIN questions q ON q.id = qr.question_id
		    WHERE qr.attempt_id=$2 AND q.type='confidence_based'),
		  (SELECT COALESCE(SUM(CASE WHEN qr.confidence_level='very_confident' AND qr.is_correct THEN 1 ELSE 0 END), 0)
		     FROM question_responses qr JOIN questions q ON q.id = qr.question_id
		    WHERE qr.attempt_id=$2 AND q.type='confidence_based')`,

	"attempt.checkBadges/write": `
		INSERT INTO badges (user_id, badge_type)
		SELECT $1, bt FROM unnest($2::text[]) AS bt
		ON CONFLICT DO NOTHING
		RETURNING badge_type`,

	"streak.RecordCompletion/read": `
		WITH ins AS (
		  INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING
		)
		SELECT COALESCE(i.timezone, 'UTC'),
		       s.current_streak, s.longest_streak, s.last_completed_date::text,
		       s.grace_window_active, s.milestone_7_claimed,
		       s.milestone_15_claimed, s.milestone_30_claimed
		  FROM users u
		  LEFT JOIN institutions i ON i.id = u.institution_id
		  LEFT JOIN streaks s ON s.user_id = u.id
		 WHERE u.id = $1
		   FOR NO KEY UPDATE OF s`,

	"streak.RecordCompletion/write": `
		WITH s AS (
		  UPDATE streaks
		     SET current_streak=$2, longest_streak=$3, last_completed_date=$4,
		         grace_window_active=false, milestone_7_claimed=$5,
		         milestone_15_claimed=$6, milestone_30_claimed=$7, updated_at=now()
		   WHERE user_id=$1
		), u AS (
		  UPDATE users
		     SET current_streak=$2, longest_streak=$3, last_completed_date=$4, updated_at=now()
		   WHERE id=$1
		), rank AS (
		  SELECT COUNT(*) + 1 AS r FROM users
		   WHERE institution_id = (SELECT institution_id FROM users WHERE id=$1)
		     AND total_points > (SELECT total_points FROM users WHERE id=$1)
		     AND status='active'
		)
		INSERT INTO badges (user_id, badge_type)
		SELECT $1, bt FROM unnest($8::text[]) AS bt
		UNION ALL
		SELECT $1, 'top_10' FROM rank WHERE r <= 10
		ON CONFLICT DO NOTHING`,

	"quiz.ReorderQuestions": `
		WITH owned AS (
		  SELECT id FROM quizzes
		   WHERE id=$1 AND created_by=$2 AND deleted_at IS NULL
		)
		UPDATE questions q
		   SET position = o.ord
		  FROM unnest($3::uuid[]) WITH ORDINALITY AS o(qid, ord)
		 WHERE q.id = o.qid
		   AND q.quiz_id = (SELECT id FROM owned)`,

	"quiz.getResults": `
		SELECT COUNT(*) FILTER (WHERE status='completed'),
		       COALESCE(AVG(score_pct) FILTER (WHERE status='completed'), 0),
		       COUNT(*),
		       COALESCE((SELECT question_count FROM quizzes WHERE id=$1), 0)
		FROM quiz_attempts WHERE quiz_id=$1`,

	"notification.List/counts": `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE read_at IS NULL)
		FROM user_notifications WHERE user_id=$1`,

	"offline.Sync": `
		INSERT INTO practice_sessions
		  (id, user_id, quiz_id, total_questions, correct_count, score_pct, answers, completed_at)
		SELECT id, $2, quiz_id, total_questions, correct_count, score_pct, answers, completed_at
		  FROM unnest($1::uuid[], $3::uuid[], $4::int[], $5::int[], $6::float8[], $7::jsonb[], $8::timestamptz[])
		    AS t(id, quiz_id, total_questions, correct_count, score_pct, answers, completed_at)
		ON CONFLICT (id) DO NOTHING`,

	"push.prune": `DELETE FROM device_tokens WHERE token = ANY($1)`,

	"admin.countBands/difficulty": `
		SELECT e.ord, c.count
		  FROM unnest($2::float8[], $3::float8[]) WITH ORDINALITY AS e(lo, hi, ord)
		  CROSS JOIN LATERAL (
			SELECT COUNT(*)
			FROM questions qn
			JOIN quizzes q ON q.id = qn.quiz_id
			WHERE q.deleted_at IS NULL
			  AND qn.difficulty >= e.lo AND qn.difficulty < e.hi
			  AND ($1::uuid IS NULL OR q.institution_id = $1)
		  ) AS c(count)
		 ORDER BY e.ord`,

	"admin.countBands/streak": `
		SELECT e.ord, c.count
		  FROM unnest($2::float8[], $3::float8[]) WITH ORDINALITY AS e(lo, hi, ord)
		  CROSS JOIN LATERAL (
			SELECT COUNT(*)
			FROM streaks st
			JOIN users u ON u.id = st.user_id
			WHERE u.deleted_at IS NULL
			  AND st.current_streak >= e.lo AND st.current_streak < e.hi
			  AND ($1::uuid IS NULL OR u.institution_id = $1)
		  ) AS c(count)
		 ORDER BY e.ord`,

	"scheduler.ExpirePoints": `
		WITH due AS (
		  SELECT pl.id, pl.user_id, pl.amount,
		         u.total_points,
		         COALESCE(SUM(pl.amount) OVER (
		           PARTITION BY pl.user_id ORDER BY pl.expires_at, pl.id
		           ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING), 0) AS taken_before
		    FROM points_ledger pl
		    JOIN users u ON u.id = pl.user_id
		   WHERE pl.expires_at IS NOT NULL AND pl.expires_at <= now() AND pl.amount > 0
		     AND NOT EXISTS (
		       SELECT 1 FROM points_ledger e
		        WHERE e.reference_id = pl.id AND e.reason = 'expiry'
		     )
		), calc AS (
		  SELECT id, user_id, amount,
		         LEAST(amount, GREATEST(0, total_points - taken_before)) AS deduction,
		         GREATEST(0, total_points - taken_before) AS balance_before
		    FROM due
		), bal AS (
		  UPDATE users u
		     SET total_points = GREATEST(0, u.total_points - t.total_deduction),
		         updated_at = now()
		    FROM (SELECT user_id, SUM(deduction) AS total_deduction
		            FROM calc GROUP BY user_id) t
		   WHERE u.id = t.user_id
		)
		INSERT INTO points_ledger (user_id, amount, reason, reference_id, balance_after, expires_at)
		SELECT user_id, -deduction, 'expiry', id, balance_before - deduction, $1
		  FROM calc`,

	"scheduler.SnapshotLeaderboard/institutions": `
		WITH ranked AS (
		  SELECT u.institution_id,
		         u.id, u.display_name, u.total_points, u.current_streak,
		         RANK() OVER (PARTITION BY u.institution_id ORDER BY u.total_points DESC) AS rank,
		         ROW_NUMBER() OVER (PARTITION BY u.institution_id ORDER BY u.total_points DESC) AS rn
		    FROM users u
		    JOIN institutions i ON i.id = u.institution_id AND i.status = 'verified'
		   WHERE u.status = 'active' AND u.role IN ('student','teacher')
		)
		INSERT INTO leaderboard_snapshots (scope, institution_id, week_start, rankings)
		SELECT 'institution', institution_id, $1,
		       jsonb_agg(jsonb_build_object(
		         'rank', rank, 'user_id', id, 'display_name', display_name,
		         'total_points', total_points, 'current_streak', current_streak
		       ) ORDER BY rank)
		  FROM ranked
		 WHERE rn <= 100
		 GROUP BY institution_id`,

	"scheduler.RankAlerts/writeback": `
		UPDATE users u SET last_notified_rank = t.rank
		  FROM unnest($1::uuid[], $2::int[]) AS t(id, rank)
		 WHERE u.id = t.id`,

	"scheduler.RecomputeDifficulty": `
		UPDATE questions q SET difficulty = t.diff
		  FROM unnest($1::uuid[], $2::float8[]) AS t(id, diff)
		 WHERE q.id = t.id`,
}

func TestFastPathStatementsPrepareOnServer(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping SQL parse check")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	for name, sql := range fastPathStatements {
		t.Run(name, func(t *testing.T) {
			// A rolled-back transaction so PREPARE leaves nothing behind and the
			// statement name cannot collide across subtests.
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer tx.Rollback(ctx)

			if _, err := tx.Exec(ctx, "PREPARE check_stmt AS "+sql); err != nil {
				t.Fatalf("statement does not parse/plan against the live schema:\n%v\n\n%s",
					err, strings.TrimSpace(sql))
			}
		})
	}
}
