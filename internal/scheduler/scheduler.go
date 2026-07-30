package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/domain/push"
	"github.com/qwish/backend/internal/domain/scoring"
	"github.com/qwish/backend/internal/domain/streak"
	"github.com/qwish/backend/internal/domain/user"
)

type Scheduler struct {
	db        *pgxpool.Pool
	streakSvc *streak.Service
	pushSvc   *push.Service
	notifSvc  *notification.Service
	userSvc   *user.Service
}

func New(db *pgxpool.Pool, streakSvc *streak.Service, pushSvc *push.Service, notifSvc *notification.Service, userSvc *user.Service) *Scheduler {
	return &Scheduler{db: db, streakSvc: streakSvc, pushSvc: pushSvc, notifSvc: notifSvc, userSvc: userSvc}
}

// ExpirePoints runs nightly. Marks points older than institution/config expiry.
func (s *Scheduler) ExpirePoints(ctx context.Context) error {
	log.Println("[cron] running expire-points")

	cfg, err := scoring.LoadConfig(ctx, s.db)
	if err != nil {
		return err
	}

	// Find unexpired positive ledger entries that are past expiry
	rows, err := s.db.Query(ctx,
		`SELECT pl.id, pl.user_id, pl.amount FROM points_ledger pl
		 WHERE pl.expires_at IS NOT NULL AND pl.expires_at <= now() AND pl.amount > 0
		 AND NOT EXISTS (
		   SELECT 1 FROM points_ledger e WHERE e.reference_id=pl.id AND e.reason='expiry'
		 )`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var ledgerID, userID string
		var amount int64
		rows.Scan(&ledgerID, &userID, &amount)

		// Get current balance
		var balance int64
		s.db.QueryRow(ctx, `SELECT total_points FROM users WHERE id=$1`, userID).Scan(&balance)
		deduction := amount
		if deduction > balance {
			deduction = balance
		}
		newBalance := balance - deduction

		s.db.Exec(ctx, `UPDATE users SET total_points=$1, updated_at=now() WHERE id=$2`, newBalance, userID)
		expiry := time.Now().AddDate(0, int(cfg.PointsExpiryMonths), 0)
		s.db.Exec(ctx,
			`INSERT INTO points_ledger (user_id, amount, reason, reference_id, balance_after, expires_at)
			 VALUES ($1,$2,'expiry',$3,$4,$5)`,
			userID, -deduction, ledgerID, newBalance, expiry)
	}
	log.Println("[cron] expire-points done")
	return nil
}

// ResetStreaks runs daily at 00:05 UTC.
func (s *Scheduler) ResetStreaks(ctx context.Context) error {
	log.Println("[cron] running reset-streaks")
	err := s.streakSvc.DailyReset(ctx)
	log.Println("[cron] reset-streaks done")
	return err
}

// SnapshotLeaderboard runs every Monday at 00:01 UTC.
func (s *Scheduler) SnapshotLeaderboard(ctx context.Context) error {
	log.Println("[cron] running snapshot-leaderboard")

	weekStart := time.Now().Truncate(7 * 24 * time.Hour)

	// Global snapshot
	rows, err := s.db.Query(ctx,
		`SELECT id, display_name, total_points, current_streak,
		        RANK() OVER (ORDER BY total_points DESC) as rank
		 FROM users WHERE status='active' AND role IN ('student','teacher')
		 ORDER BY total_points DESC LIMIT 100`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type rankEntry struct {
		Rank          int    `json:"rank"`
		UserID        string `json:"user_id"`
		DisplayName   string `json:"display_name"`
		TotalPoints   int64  `json:"total_points"`
		CurrentStreak int    `json:"current_streak"`
	}
	var globalRankings []rankEntry
	for rows.Next() {
		var e rankEntry
		rows.Scan(&e.UserID, &e.DisplayName, &e.TotalPoints, &e.CurrentStreak, &e.Rank)
		globalRankings = append(globalRankings, e)
	}

	globalJSON, _ := json.Marshal(globalRankings)
	s.db.Exec(ctx,
		`INSERT INTO leaderboard_snapshots (scope, week_start, rankings) VALUES ('global',$1,$2)`,
		weekStart.Format("2006-01-02"), globalJSON)

	// Per-institution snapshots
	instRows, _ := s.db.Query(ctx, `SELECT id FROM institutions WHERE status='verified'`)
	defer instRows.Close()
	for instRows.Next() {
		var instID string
		instRows.Scan(&instID)

		iRows, _ := s.db.Query(ctx,
			`SELECT id, display_name, total_points, current_streak,
			        RANK() OVER (ORDER BY total_points DESC) as rank
			 FROM users WHERE institution_id=$1 AND status='active' AND role IN ('student','teacher')
			 ORDER BY total_points DESC LIMIT 100`, instID)

		var instRankings []rankEntry
		for iRows.Next() {
			var e rankEntry
			iRows.Scan(&e.UserID, &e.DisplayName, &e.TotalPoints, &e.CurrentStreak, &e.Rank)
			instRankings = append(instRankings, e)
		}
		iRows.Close()

		instJSON, _ := json.Marshal(instRankings)
		s.db.Exec(ctx,
			`INSERT INTO leaderboard_snapshots (scope, institution_id, week_start, rankings)
			 VALUES ('institution',$1,$2,$3)`,
			instID, weekStart.Format("2006-01-02"), instJSON)
	}

	log.Println("[cron] snapshot-leaderboard done")
	return nil
}

// SendStreakNudges pushes a reminder to users who have an active streak but
// haven't completed a quiz today, so they don't break it. Respects the
// push_streak_nudge preference. Intended to run in the evening (e.g. 18:00 local
// — here scheduled in UTC by the caller).
func (s *Scheduler) SendStreakNudges(ctx context.Context) error {
	log.Println("[cron] running streak-nudges")
	if s.notifSvc == nil {
		return nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT u.id, u.current_streak
		 FROM users u
		 LEFT JOIN notification_preferences np ON np.user_id = u.id
		 WHERE u.status='active' AND u.role IN ('student','teacher')
		   AND u.current_streak > 0
		   AND (u.last_completed_date IS NULL OR u.last_completed_date < CURRENT_DATE)
		   AND COALESCE(np.push_streak_nudge, true)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type nudge struct {
		id     string
		streak int
	}
	var targets []nudge
	for rows.Next() {
		var n nudge
		rows.Scan(&n.id, &n.streak)
		targets = append(targets, n)
	}
	rows.Close()
	for _, n := range targets {
		body := fmt.Sprintf("Your %d-day streak is at risk! Complete a quiz before midnight to keep it alive.", n.streak)
		s.notifSvc.Emit(ctx, n.id, "streak", "Keep your streak alive 🔥", body,
			notification.WithIcon("local_fire_department"), notification.WithColor("warning"),
			notification.WithReference("streak_nudge"))
	}
	log.Printf("[cron] streak-nudges done (%d sent)", len(targets))
	return nil
}

// SendWeeklyDigests pushes each active user a summary of their past week.
// Respects the push_weekly_digest preference. Intended to run weekly.
func (s *Scheduler) SendWeeklyDigests(ctx context.Context) error {
	log.Println("[cron] running weekly-digests")
	if s.notifSvc == nil {
		return nil
	}
	weekStart := time.Now().UTC().AddDate(0, 0, -7)
	rows, err := s.db.Query(ctx,
		`SELECT u.id, u.current_streak,
		   COALESCE((SELECT SUM(amount) FROM points_ledger
		             WHERE user_id=u.id AND amount>0 AND created_at >= $1),0) AS pts,
		   (SELECT COUNT(*) FROM quiz_attempts
		    WHERE user_id=u.id AND status='completed' AND completed_at >= $1) AS quizzes
		 FROM users u
		 LEFT JOIN notification_preferences np ON np.user_id = u.id
		 WHERE u.status='active' AND u.role IN ('student','teacher')
		   AND COALESCE(np.push_weekly_digest, true)`, weekStart)
	if err != nil {
		return err
	}
	defer rows.Close()
	type digest struct {
		id      string
		streak  int
		points  int64
		quizzes int
	}
	var targets []digest
	for rows.Next() {
		var d digest
		rows.Scan(&d.id, &d.streak, &d.points, &d.quizzes)
		targets = append(targets, d)
	}
	rows.Close()
	sent := 0
	for _, d := range targets {
		if d.quizzes == 0 && d.points == 0 {
			continue // nothing to report; skip silent weeks
		}
		body := fmt.Sprintf("This week: %d points across %d quizzes. Streak: %d days. Tap to see your full breakdown.",
			d.points, d.quizzes, d.streak)
		s.notifSvc.Emit(ctx, d.id, "system", "Your weekly recap 📈", body,
			notification.WithIcon("insights"), notification.WithReference("weekly_digest"))
		sent++
	}
	log.Printf("[cron] weekly-digests done (%d sent)", sent)
	return nil
}

// SendRankChangeAlerts pushes a notification when a user's global leaderboard
// rank improves since the last alert. Records every user's current rank so the
// next run only fires on change. Respects the push_rank_changes preference.
func (s *Scheduler) SendRankChangeAlerts(ctx context.Context) error {
	log.Println("[cron] running rank-change-alerts")
	if s.notifSvc == nil {
		return nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT id, rank, last_notified_rank, notify FROM (
		   SELECT u.id,
		          RANK() OVER (ORDER BY u.total_points DESC) AS rank,
		          u.last_notified_rank,
		          COALESCE(np.push_rank_changes, true) AS notify
		   FROM users u
		   LEFT JOIN notification_preferences np ON np.user_id = u.id
		   WHERE u.status='active' AND u.role IN ('student','teacher')
		 ) ranked`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type entry struct {
		id       string
		rank     int
		prevRank *int
		notify   bool
	}
	var entries []entry
	for rows.Next() {
		var e entry
		rows.Scan(&e.id, &e.rank, &e.prevRank, &e.notify)
		entries = append(entries, e)
	}
	rows.Close()
	sent := 0
	for _, e := range entries {
		// Notify only on an improvement (lower rank number) the user opted into.
		if e.notify && e.prevRank != nil && e.rank < *e.prevRank {
			body := fmt.Sprintf("You climbed to #%d on the global leaderboard (up from #%d). Keep going!", e.rank, *e.prevRank)
			s.notifSvc.Emit(ctx, e.id, "rank", "You moved up the leaderboard 🏆", body,
				notification.WithIcon("emoji_events"), notification.WithColor("success"),
				notification.WithReference("rank_change"))
			sent++
		}
		s.db.Exec(ctx, `UPDATE users SET last_notified_rank=$1 WHERE id=$2`, e.rank, e.id)
	}
	log.Printf("[cron] rank-change-alerts done (%d sent)", sent)
	return nil
}

// SendWeeklyInsightsEmail emails each opted-in user their weekly score insights.
// Respects the email_weekly_insights preference.
func (s *Scheduler) SendWeeklyInsightsEmail(ctx context.Context) error {
	log.Println("[cron] running weekly-insights-email")
	if s.notifSvc == nil || s.userSvc == nil {
		return nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT u.id, u.email, u.display_name, COALESCE(u.institution_id::text,'')
		 FROM users u
		 LEFT JOIN notification_preferences np ON np.user_id = u.id
		 WHERE u.status='active' AND u.role IN ('student','teacher')
		   AND u.deleted_at IS NULL
		   AND COALESCE(np.email_weekly_insights, true)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type recipient struct {
		id, email, name, instID string
	}
	var recipients []recipient
	for rows.Next() {
		var r recipient
		rows.Scan(&r.id, &r.email, &r.name, &r.instID)
		recipients = append(recipients, r)
	}
	rows.Close()
	sent := 0
	for _, r := range recipients {
		wi, err := s.userSvc.GetWeeklyInsights(ctx, r.id, r.instID)
		if err != nil {
			continue
		}
		if wi.QuizzesThisWeek == 0 && wi.PointsThisWeek == 0 {
			continue // skip users with no activity
		}
		domain := ""
		if wi.Domain != nil {
			domain = *wi.Domain
		}
		s.notifSvc.SendWeeklyInsights(ctx, r.email, r.name, wi.PointsThisWeek, wi.PointsDeltaPct,
			wi.QuizzesThisWeek, wi.AvgScoreThisWeek, wi.CurrentStreak, domain, wi.Suggestion)
		sent++
	}
	log.Printf("[cron] weekly-insights-email done (%d sent)", sent)
	return nil
}

// CloseExpiredQuizzes closes P&W quizzes past their ends_at.
func (s *Scheduler) CloseExpiredQuizzes(ctx context.Context) error {
	log.Println("[cron] running close-expired-quizzes")
	_, err := s.db.Exec(ctx,
		`UPDATE quizzes SET status='closed', updated_at=now()
		 WHERE type='play_and_win' AND status='published' AND ends_at IS NOT NULL AND ends_at <= now()`)
	log.Println("[cron] close-expired-quizzes done")
	return err
}

// staleAttemptAge is how long an attempt may sit in_progress before the
// sweeper calls it abandoned.
//
// ponytail: flat 2h threshold. Quizzes run minutes — questions default to
// time_limit_seconds=15 (migrations/001_initial.sql:133) — so this is roughly
// eight times the longest realistic attempt, which leaves room for a user who
// backgrounds the app mid-quiz. If a long-form quiz format ever ships, derive
// this per-quiz from SUM(questions.time_limit_seconds) instead of a constant.
const staleAttemptAge = 2 * time.Hour

func staleCutoff(now time.Time) time.Time { return now.Add(-staleAttemptAge) }

// AbandonStaleAttempts flips in-progress attempts older than staleAttemptAge to
// status='abandoned'. Nothing else in the codebase ever writes that status, so
// without this job abandon_rate has nothing to read.
//
// Idempotent: the WHERE clause excludes rows already converted, so a re-run is
// a no-op and a missed run self-heals on the next tick.
//
// completed_at is deliberately left NULL — the attempt did not complete. That
// is why the abandonment metrics bucket on started_at.
func (s *Scheduler) AbandonStaleAttempts(ctx context.Context) error {
	log.Println("[cron] running abandon-stale-attempts")
	tag, err := s.db.Exec(ctx,
		`UPDATE quiz_attempts
		    SET status = 'abandoned'
		  WHERE status = 'in_progress'
		    AND started_at < $1`, staleCutoff(time.Now()))
	if err != nil {
		log.Printf("[cron] abandon-stale-attempts failed: %v", err)
		return err
	}
	log.Printf("[cron] abandon-stale-attempts done — %d attempts abandoned", tag.RowsAffected())
	return nil
}

// RecomputeQuestionDifficulty runs nightly. It refines questions.difficulty
// from real response data — empirical hardness (1 - correct-rate) blended with
// time-taken and clue-usage signals — shrunk toward the subdomain (or, absent
// that, the question-type) prior by sample size. Cold questions ride the prior;
// after ~20+ responses the observed correct-rate dominates.
func (s *Scheduler) RecomputeQuestionDifficulty(ctx context.Context) error {
	log.Println("[cron] running recompute-question-difficulty")

	rows, err := s.db.Query(ctx, `
		SELECT qr.question_id, q.type, sd.difficulty,
		       COUNT(*)                                                 AS n,
		       AVG(CASE WHEN qr.is_correct THEN 1.0 ELSE 0.0 END)       AS p,
		       COALESCE(AVG(LEAST(qr.time_taken_ms::float
		           / NULLIF(q.time_limit_seconds * 1000, 0), 1.0)), 0)  AS time_ratio,
		       COALESCE(AVG(CASE
		           WHEN q.clues IS NOT NULL AND jsonb_typeof(q.clues) = 'array'
		                AND jsonb_array_length(q.clues) > 0
		           THEN LEAST(qr.clues_used::float / jsonb_array_length(q.clues), 1.0)
		           ELSE 0 END), 0)                                      AS clue_frac
		FROM question_responses qr
		JOIN questions q  ON q.id  = qr.question_id
		JOIN quizzes  qz  ON qz.id = q.quiz_id
		LEFT JOIN subdomains sd ON sd.slug = qz.subdomain
		WHERE qr.time_taken_ms IS NOT NULL
		GROUP BY qr.question_id, q.type, sd.difficulty`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type update struct {
		id   string
		diff float64
	}
	var updates []update
	for rows.Next() {
		var qid, qtype string
		var subPrior *float64
		var n int
		var p, timeRatio, clueFrac float64
		if err := rows.Scan(&qid, &qtype, &subPrior, &n, &p, &timeRatio, &clueFrac); err != nil {
			return err
		}
		prior := scoring.GetQuestionDifficultyCoefficient(qtype)
		if subPrior != nil {
			prior = *subPrior
		}
		updates = append(updates, update{qid, deriveDifficulty(prior, n, p, timeRatio, clueFrac)})
	}
	rows.Close()

	// Buffer then write so we don't hold the read cursor while updating.
	for _, u := range updates {
		s.db.Exec(ctx, `UPDATE questions SET difficulty=$1 WHERE id=$2`, u.diff, u.id)
	}

	log.Printf("[cron] recompute-question-difficulty done (%d questions)", len(updates))
	return nil
}

// deriveDifficulty is the pure item-difficulty model (see the job above).
// prior/return are difficulty coefficients in [0.4,1.0]; p, timeRatio, clueFrac
// are in [0,1]; n is the response count driving shrinkage toward the prior.
func deriveDifficulty(prior float64, n int, p, timeRatio, clueFrac float64) float64 {
	const shrinkK = 20.0 // responses for empirical signal to reach ~half weight
	rawHard := clamp01(0.65*(1.0-p) + 0.25*timeRatio + 0.10*clueFrac)
	emp := 0.40 + 0.60*rawHard // map hardness [0,1] → coefficient [0.4,1.0]
	w := float64(n) / (float64(n) + shrinkK)
	return clampRange(w*emp+(1.0-w)*prior, 0.40, 1.00)
}

func clamp01(v float64) float64 { return clampRange(v, 0, 1) }

func clampRange(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
