package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
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

// DispatchAnnouncements promotes due scheduled announcements to sent and fans
// out notification/email channels. The conditional UPDATE is the delivery
// claim: concurrent cron runs cannot claim the same announcement twice.
func (s *Scheduler) DispatchAnnouncements(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT id, title, body, cta_label, cta_url, delivery_types, audience
		FROM announcements WHERE status='scheduled' AND (scheduled_at IS NULL OR scheduled_at<=now())
		ORDER BY COALESCE(scheduled_at,created_at) LIMIT 25`)
	if err != nil {
		return err
	}
	type due struct {
		id, title, body, audience string
		ctaLabel, ctaURL          *string
		channels                  []string
	}
	items := []due{}
	for rows.Next() {
		var a due
		if err := rows.Scan(&a.id, &a.title, &a.body, &a.ctaLabel, &a.ctaURL, &a.channels, &a.audience); err != nil {
			rows.Close()
			return err
		}
		items = append(items, a)
	}
	rows.Close()
	for _, a := range items {
		tag, err := s.db.Exec(ctx, `UPDATE announcements SET status='sent',sent_at=now() WHERE id=$1 AND status='scheduled'`, a.id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		recipients, err := s.announcementRecipients(ctx, a.id, a.audience)
		if err != nil {
			return err
		}
		for _, recipient := range recipients {
			for _, channel := range a.channels {
				switch channel {
				case "in_app_notification":
					s.notifSvc.Emit(ctx, recipient.id, "announcement", a.title, a.body, notification.WithIcon("notifications"), notification.WithColor("indigo"), notification.WithReference(a.id))
				case "email":
					cta := ""
					if a.ctaLabel != nil && a.ctaURL != nil {
						cta = fmt.Sprintf(`<p><a href="%s">%s</a></p>`, html.EscapeString(*a.ctaURL), html.EscapeString(*a.ctaLabel))
					}
					body := fmt.Sprintf(`<h1>%s</h1><p>%s</p>%s`, html.EscapeString(a.title), html.EscapeString(a.body), cta)
					if err := s.notifSvc.SendEmail(ctx, recipient.email, a.title, body, "announcement:"+a.id); err != nil {
						log.Printf("[announcement] email %s: %v", a.id, err)
					}
				}
			}
		}
	}
	return nil
}

type announcementRecipient struct{ id, email string }

func (s *Scheduler) announcementRecipients(ctx context.Context, announcementID, audience string) ([]announcementRecipient, error) {
	rows, err := s.db.Query(ctx, `SELECT u.id,u.email FROM users u LEFT JOIN institutions i ON i.id=u.institution_id
		WHERE u.status='active' AND u.deleted_at IS NULL AND (
		 $2='all' OR ($2='students' AND u.role='student') OR ($2='teachers' AND u.role='teacher') OR
		 ($2='institution' AND EXISTS(SELECT 1 FROM announcement_institutions ai WHERE ai.announcement_id=$1 AND ai.institution_id=u.institution_id)) OR
		 ($2='country' AND lower(COALESCE(i.onboarding_country,'')) IN ('india','in')))`, announcementID, audience)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []announcementRecipient{}
	for rows.Next() {
		var r announcementRecipient
		if err := rows.Scan(&r.id, &r.email); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	return list, rows.Err()
}

// ExpirePoints runs nightly. Marks points older than institution/config expiry.
func (s *Scheduler) ExpirePoints(ctx context.Context) error {
	log.Println("[cron] running expire-points")

	cfg, err := scoring.LoadConfig(ctx, s.db)
	if err != nil {
		return err
	}

	// Expire every due ledger entry in one statement. This was a read followed by
	// three round trips per expiring row — on a nightly sweep over the whole
	// ledger that is thousands of sequential exchanges.
	//
	// The deduction is clamped at the user's balance exactly as before. Ordering
	// matters when one user has several entries expiring at once: the running
	// sum over prior entries is subtracted first, so the second entry can only
	// take what the first one left, and balance_after stays a truthful running
	// figure rather than each row independently reading the same starting value.
	expiry := time.Now().AddDate(0, int(cfg.PointsExpiryMonths), 0)
	ct, err := s.db.Exec(ctx, `
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
		  FROM calc`, expiry)
	if err != nil {
		return err
	}

	log.Printf("[cron] expire-points done (%d entries expired)", ct.RowsAffected())
	return nil
}

// ResetStreaks runs daily at 00:05 UTC.
func (s *Scheduler) ResetStreaks(ctx context.Context) error {
	log.Println("[cron] running reset-streaks")
	err := s.streakSvc.DailyReset(ctx)
	if err == nil {
		s.sendStreakRecoveryAlerts(ctx)
	}
	log.Println("[cron] reset-streaks done")
	return err
}

// sendStreakRecoveryAlerts tells users who missed yesterday that their streak
// is in its grace window: completing a quiz before midnight tonight keeps it,
// otherwise tomorrow's reset drops it to zero. Runs right after DailyReset, so
// grace_window_active is current. The NOT EXISTS makes a second run on the same
// day a no-op (the cron endpoint can be triggered manually).
func (s *Scheduler) sendStreakRecoveryAlerts(ctx context.Context) {
	if s.notifSvc == nil {
		return
	}
	rows, err := s.db.Query(ctx,
		`SELECT u.id, st.current_streak
		   FROM streaks st
		   JOIN users u ON u.id = st.user_id
		   LEFT JOIN notification_preferences np ON np.user_id = u.id
		  WHERE st.grace_window_active
		    AND st.last_completed_date = (CURRENT_DATE - INTERVAL '2 days')::date
		    AND st.current_streak > 0
		    AND u.status='active' AND u.role IN ('student','teacher')
		    AND COALESCE(np.push_streak_nudge, true)
		    AND NOT EXISTS (
		      SELECT 1 FROM user_notifications n
		       WHERE n.user_id = u.id AND n.reference = 'streak_recovery'
		         AND n.created_at >= CURRENT_DATE
		    )`)
	if err != nil {
		log.Printf("[cron] streak-recovery query failed: %v", err)
		return
	}
	defer rows.Close()
	type target struct {
		id     string
		streak int
	}
	var targets []target
	for rows.Next() {
		var t target
		rows.Scan(&t.id, &t.streak)
		targets = append(targets, t)
	}
	rows.Close()
	for _, t := range targets {
		body := fmt.Sprintf("You missed yesterday, so your %d-day streak is on its last chance. Complete a quiz before midnight tonight and it carries on — miss again and it resets to zero.", t.streak)
		s.notifSvc.Emit(ctx, t.id, "streak", "Save your streak 🔥", body,
			notification.WithIcon("local_fire_department"), notification.WithColor("danger"),
			notification.WithReference("streak_recovery"))
	}
	log.Printf("[cron] streak-recovery alerts sent (%d)", len(targets))
}

// SnapshotLeaderboard records a weekly historical snapshot for reporting and
// auditing. The public leaderboard itself is live.
func (s *Scheduler) SnapshotLeaderboard(ctx context.Context) error {
	log.Println("[cron] running snapshot-leaderboard")

	weekStart := time.Now().Truncate(7 * 24 * time.Hour)

	// Global snapshot
	rows, err := s.db.Query(ctx,
		`SELECT id, display_name, total_points, current_streak,
		        RANK() OVER (ORDER BY total_points DESC) as rank
		 FROM users u WHERE u.status='active' AND u.role='student'
		   AND (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=u.id AND qa.status='completed') >= 5
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

	// Per-institution snapshots: one statement for every institution instead of
	// a list query plus two round trips each. The top-100-per-institution cut is
	// a partitioned ROW_NUMBER, and the JSON is assembled in the database, so
	// nothing has to travel to Go and back.
	if _, err := s.db.Exec(ctx, `
		WITH ranked AS (
		  SELECT u.institution_id,
		         u.id, u.display_name, u.total_points, u.current_streak,
		         RANK() OVER (PARTITION BY u.institution_id ORDER BY u.total_points DESC) AS rank,
		         ROW_NUMBER() OVER (PARTITION BY u.institution_id ORDER BY u.total_points DESC) AS rn
		    FROM users u
		    JOIN institutions i ON i.id = u.institution_id AND i.status = 'verified'
			   WHERE u.status = 'active' AND u.role='student'
			     AND (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=u.id AND qa.status='completed') >= 5
		)
		INSERT INTO leaderboard_snapshots (scope, institution_id, week_start, rankings)
		SELECT 'institution', institution_id, $1,
		       jsonb_agg(jsonb_build_object(
		         'rank', rank, 'user_id', id, 'display_name', display_name,
		         'total_points', total_points, 'current_streak', current_streak
		       ) ORDER BY rank)
		  FROM ranked
		 WHERE rn <= 100
		 GROUP BY institution_id`, weekStart.Format("2006-01-02")); err != nil {
		return err
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
			     AND (u.role='teacher' OR (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=u.id AND qa.status='completed') >= 5)
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
		   WHERE u.status='active' AND u.role='student'
		     AND (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=u.id AND qa.status='completed') >= 5
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
	ids := make([]string, 0, len(entries))
	ranks := make([]int, 0, len(entries))
	for _, e := range entries {
		// Notify only on an improvement (lower rank number) the user opted into.
		if e.notify && e.prevRank != nil && e.rank < *e.prevRank {
			body := fmt.Sprintf("You climbed to #%d on the global leaderboard (up from #%d). Keep going!", e.rank, *e.prevRank)
			s.notifSvc.Emit(ctx, e.id, "rank", "You moved up the leaderboard 🏆", body,
				notification.WithIcon("emoji_events"), notification.WithColor("success"),
				notification.WithReference("rank_change"))
			sent++
		}
		ids = append(ids, e.id)
		ranks = append(ranks, e.rank)
	}
	// One write-back for every user instead of one per user.
	if len(ids) > 0 {
		s.db.Exec(ctx,
			`UPDATE users u SET last_notified_rank = t.rank
			   FROM unnest($1::uuid[], $2::int[]) AS t(id, rank)
			  WHERE u.id = t.id`, ids, ranks)
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

	// One UPDATE for the whole batch. The difficulty model itself stays in Go —
	// it is easier to test there — but writing it back a row at a time meant one
	// round trip per question in the platform.
	if len(updates) > 0 {
		ids := make([]string, len(updates))
		diffs := make([]float64, len(updates))
		for i, u := range updates {
			ids[i], diffs[i] = u.id, u.diff
		}
		if _, err := s.db.Exec(ctx,
			`UPDATE questions q SET difficulty = t.diff
			   FROM unnest($1::uuid[], $2::float8[]) AS t(id, diff)
			  WHERE q.id = t.id`, ids, diffs); err != nil {
			return err
		}
	}

	log.Printf("[cron] recompute-question-difficulty done (%d questions)", len(updates))
	return nil
}

// deriveDifficulty uses a Beta-Binomial posterior for correctness, preventing
// one or two answers from making a new question look extremely easy or hard.
// Time and clue signals are confidence-weighted by the same prior strength.
func deriveDifficulty(prior float64, n int, p, timeRatio, clueFrac float64) float64 {
	const priorStrength = 20.0
	priorHard := clamp01((prior - 0.40) / 0.60)
	posteriorWrong := (float64(n)*(1.0-clamp01(p)) + priorStrength*priorHard) /
		(float64(n) + priorStrength)
	confidence := float64(n) / (float64(n) + priorStrength)
	rawHard := clamp01(0.65*posteriorWrong +
		0.25*(confidence*clamp01(timeRatio)+(1-confidence)*priorHard) +
		0.10*(confidence*clamp01(clueFrac)+(1-confidence)*priorHard))
	return clampRange(0.40+0.60*rawHard, 0.40, 1.00)
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

// PurgeOnboardingSessions deletes unclaimed pre-signup sessions past their
// expiry. Claimed ones are kept: they are the only record of where a user's
// first attempt came from.
func (s *Scheduler) PurgeOnboardingSessions(ctx context.Context) error {
	ct, err := s.db.Exec(ctx,
		`DELETE FROM onboarding_sessions WHERE claimed_by IS NULL AND expires_at < now()`)
	if err != nil {
		return err
	}
	log.Printf("[scheduler] purged %d expired onboarding sessions", ct.RowsAffected())
	return nil
}
