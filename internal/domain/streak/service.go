package streak

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/scoring"
)

// ist is the platform day for learners without an institute. A FixedZone, as
// in metrics, so it holds in a container with no tzdata (India has no DST).
var ist = time.FixedZone("IST", 5*3600+1800)

// clock is time.Now, swappable so tests can place a completion at a given hour.
var clock = time.Now

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

	bonus, err := s.RecordCompletionTx(ctx, tx, userID, cfg, "")
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return bonus, nil
}

// RecordCompletionTx commits milestone claims and their credit with the attempt.
func (s *Service) RecordCompletionTx(ctx context.Context, tx pgx.Tx, userID string, cfg *scoring.Config, reference string) (int64, error) {
	var err error
	if _, err = tx.Exec(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, userID); err != nil {
		return 0, err
	}

	// Ensure the streak row exists, then read it locked together with the
	// institution timezone. The lock must be taken from streaks itself:
	// Postgres refuses FOR UPDATE on the nullable side of an outer join, and a
	// single statement that tried (users LEFT JOIN streaks ... FOR NO KEY
	// UPDATE OF s) failed on every call, so no completion moved any streak.
	var timezone string
	var current, longest int
	var lastDate *string
	var m7, m15, m30 bool

	if _, err = tx.Exec(ctx, `INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID); err != nil {
		return 0, err
	}
	if err = tx.QueryRow(ctx,
		`SELECT COALESCE(i.timezone, ''),
		        s.current_streak, s.longest_streak, s.last_completed_date::text,
		        s.milestone_7_claimed, s.milestone_15_claimed, s.milestone_30_claimed
		   FROM streaks s
		   JOIN users u ON u.id = s.user_id
		   LEFT JOIN institutions i ON i.id = u.institution_id
		  WHERE s.user_id = $1
		    FOR NO KEY UPDATE OF s`, userID,
	).Scan(&timezone, &current, &longest, &lastDate, &m7, &m15, &m30); err != nil {
		return 0, err
	}

	// No institute (independent learners, or between institutes) means the
	// platform day, IST — never UTC, which cut the day at 05:30 IST and folded
	// a late-night quiz into the day before.
	loc := ist
	if timezone != "" {
		if l, err := time.LoadLocation(timezone); err == nil {
			loc = l
		}
	}
	today := localDay(clock(), loc)
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

	if bonus > 0 {
		_, err = tx.Exec(ctx, `WITH bal AS (
   UPDATE users SET total_points=total_points+$1,updated_at=now() WHERE id=$2 RETURNING total_points)
   INSERT INTO points_ledger(user_id,amount,reason,reference_id,balance_after,expires_at)
   SELECT $2,$1,'streak_bonus',NULLIF($3,'')::uuid,total_points,$4 FROM bal`, bonus, userID, reference, clock().AddDate(0, int(cfg.PointsExpiryMonths), 0))
		if err != nil {
			return 0, err
		}
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
