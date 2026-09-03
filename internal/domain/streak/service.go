package streak

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/scoring"
)

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

type StreakInfo struct {
	CurrentStreak       int  `json:"current_streak"`
	LongestStreak       int  `json:"longest_streak"`
	GraceWindowActive   bool `json:"grace_window_active"`
	NextMilestone       int  `json:"next_milestone"`
	ProgressToMilestone int  `json:"progress_to_milestone"`
}

func (s *Service) GetInfo(ctx context.Context, userID string) (*StreakInfo, error) {
	info := &StreakInfo{}
	// Derived from last_completed_date rather than trusting the stored column:
	// the nightly reset is an in-process cron, so a restart across the wrong
	// night used to leave a dead streak on display indefinitely.
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE((SELECT CASE WHEN last_completed_date >= CURRENT_DATE - 2 THEN current_streak ELSE 0 END
		                          FROM streaks WHERE user_id=$1), 0),
		        COALESCE((SELECT longest_streak FROM streaks WHERE user_id=$1), 0),
		        COALESCE((SELECT last_completed_date = CURRENT_DATE - 2 FROM streaks WHERE user_id=$1), false)`, userID,
	).Scan(&info.CurrentStreak, &info.LongestStreak, &info.GraceWindowActive)
	if err != nil {
		return nil, err
	}
	info.NextMilestone = nextMilestone(info.CurrentStreak)
	info.ProgressToMilestone = info.CurrentStreak
	return info, nil
}

func nextMilestone(current int) int {
	milestones := []int{7, 15, 30}
	for _, m := range milestones {
		if current < m {
			return m
		}
	}
	return 30
}

// localDay is midnight of the calendar day `now` falls on in loc. This used to
// be time.Truncate(24h), which rounds absolute time since the epoch and so
// lands on UTC midnight — the wrong calendar day for part of every day in any
// non-UTC zone (e.g. before 05:30 in IST, after 20:00 in EDT).
func localDay(now time.Time, loc *time.Location) time.Time {
	t := now.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// nextStreak decides the streak value for a completion happening on `today`.
// broke reports that the old streak was lost (milestones re-arm); done reports
// that today was already counted and nothing should change.
//
// The one-day grace (completing the day after a miss keeps the streak) is
// decided from the date alone, not from streaks.grace_window_active: the flag
// is only written by a nightly in-process cron, so a restart across that window
// silently cost users a grace they were entitled to.
func nextStreak(current int, lastDate *string, today time.Time) (next int, broke, done bool) {
	if lastDate == nil {
		return 1, false, false
	}
	switch *lastDate {
	case today.Format("2006-01-02"):
		return current, false, true
	case today.AddDate(0, 0, -1).Format("2006-01-02"),
		today.AddDate(0, 0, -2).Format("2006-01-02"):
		return current + 1, false, false
	default:
		return 1, true, false
	}
}

func (s *Service) RecordCompletion(ctx context.Context, userID string, cfg *scoring.Config) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Ensure the streak row exists and read it locked, together with the
	// institution timezone, in one round trip. The old code did a timezone
	// SELECT, a streak SELECT, and — on the common first-completion path — an
	// INSERT plus a second SELECT: four exchanges for data that one statement
	// returns. The upsert is unconditional and idempotent, so the "row missing"
	// branch disappears entirely.
	var timezone string
	var current, longest int
	var lastDate *string
	var m7, m15, m30 bool

	err = tx.QueryRow(ctx,
		`WITH ins AS (
		   INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING
		 )
		 SELECT COALESCE(i.timezone, 'UTC'),
		        s.current_streak, s.longest_streak, s.last_completed_date::text,
		        s.milestone_7_claimed,
		        s.milestone_15_claimed, s.milestone_30_claimed
		   FROM users u
		   LEFT JOIN institutions i ON i.id = u.institution_id
		   LEFT JOIN streaks s ON s.user_id = u.id
		  WHERE u.id = $1
		    FOR NO KEY UPDATE OF s`, userID,
	).Scan(&timezone, &current, &longest, &lastDate, &m7, &m15, &m30)
	if err != nil {
		// The upsert above runs in the same snapshot as the SELECT, so a row
		// created by this very statement is not yet visible to it. Re-read.
		if err = tx.QueryRow(ctx,
			`SELECT COALESCE((SELECT i.timezone FROM users u
			                    JOIN institutions i ON i.id = u.institution_id
			                   WHERE u.id=$1), 'UTC'),
			        current_streak, longest_streak, last_completed_date::text,
			        milestone_7_claimed,
			        milestone_15_claimed, milestone_30_claimed
			   FROM streaks WHERE user_id=$1 FOR NO KEY UPDATE`, userID,
		).Scan(&timezone, &current, &longest, &lastDate, &m7, &m15, &m30); err != nil {
			timezone = "UTC"
			current, longest, lastDate, m7, m15, m30 = 0, 0, nil, false, false, false
		}
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	today := localDay(time.Now(), loc)
	todayDate := today.Format("2006-01-02")

	next, broke, done := nextStreak(current, lastDate, today)
	if done {
		// Already completed today → no change
		return 0, nil
	}
	current = next
	if broke {
		m7, m15, m30 = false, false, false
	}

	if current > longest {
		longest = current
	}

	// Milestone bonuses
	var bonus int64
	if current >= 7 && !m7 {
		m7 = true
		bonus += int64(cfg.StreakBonus7Day)
	}
	if current >= 15 && !m15 {
		m15 = true
		bonus += int64(cfg.StreakBonus15Day)
	}
	if current >= 30 && !m30 {
		m30 = true
		bonus += int64(cfg.StreakBonus30Day)
	}

	// Streak badges that depend only on the new streak length are decided here;
	// top_10 depends on a rank the database has to compute, so it is added by
	// the statement below rather than in Go.
	streakBadges := []string{}
	if current >= 3 {
		streakBadges = append(streakBadges, "warming_up")
	}
	if current >= 7 {
		streakBadges = append(streakBadges, "on_a_roll")
	}
	if current >= 14 {
		streakBadges = append(streakBadges, "locked_in")
	}
	if current >= 30 {
		streakBadges = append(streakBadges, "unstoppable")
	}
	if current >= 60 {
		streakBadges = append(streakBadges, "iron_will")
	}

	// Both updates and every streak badge insert happen in one statement.
	// This was up to six sequential round trips inside the transaction; none of
	// them depended on another's result, so they chain as CTEs instead.
	if _, err := tx.Exec(ctx,
		`WITH s AS (
		   UPDATE streaks
		      SET current_streak=$2, longest_streak=$3, last_completed_date=$4,
		          grace_window_active=false, milestone_7_claimed=$5,
		          milestone_15_claimed=$6, milestone_30_claimed=$7, updated_at=now()
		    WHERE user_id=$1
		 ), u AS (
		   UPDATE users
		      SET current_streak=$2, longest_streak=$3, last_completed_date=$4, updated_at=now()
		    WHERE id=$1
		 )
		 INSERT INTO badges (user_id, badge_type)
		 SELECT $1, bt FROM unnest($8::text[]) AS bt
		 ON CONFLICT DO NOTHING`,
		userID, current, longest, todayDate, m7, m15, m30, streakBadges,
	); err != nil {
		return 0, err
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return bonus, nil
}

// DailyReset is called by the cron job. Activates grace windows and resets broken streaks.
func (s *Service) DailyReset(ctx context.Context) error {
	// Activate grace window for users who didn't complete yesterday (and didn't already have grace active)
	_, err := s.db.Exec(ctx,
		`UPDATE streaks SET grace_window_active=true
		 WHERE last_completed_date = (CURRENT_DATE - INTERVAL '2 days')::date
		 AND grace_window_active=false
		 AND current_streak > 0`)
	if err != nil {
		return err
	}

	// Reset streaks whose grace window has passed. Keyed on the date alone: the
	// old version required grace_window_active, which only the query above sets
	// and only on the single day last_completed_date = CURRENT_DATE - 2. Miss
	// that one run (this cron lives in-process, so any restart or deploy can)
	// and the flag stayed false forever, leaving a dead streak on display
	// indefinitely. Date-keyed, every run heals whatever earlier runs missed.
	_, err = s.db.Exec(ctx,
		`UPDATE streaks SET current_streak=0, grace_window_active=false,
		 milestone_7_claimed=false, milestone_15_claimed=false, milestone_30_claimed=false
		 WHERE last_completed_date < (CURRENT_DATE - INTERVAL '2 days')::date
		 AND (current_streak > 0 OR grace_window_active)`)
	if err != nil {
		return err
	}

	// Sync denormalized fields
	s.db.Exec(ctx,
		`UPDATE users u SET current_streak=s.current_streak, longest_streak=s.longest_streak
		 FROM streaks s WHERE s.user_id=u.id`)

	return nil
}
