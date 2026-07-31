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
	CurrentStreak      int    `json:"current_streak"`
	LongestStreak      int    `json:"longest_streak"`
	GraceWindowActive  bool   `json:"grace_window_active"`
	NextMilestone      int    `json:"next_milestone"`
	ProgressToMilestone int   `json:"progress_to_milestone"`
}

func (s *Service) GetInfo(ctx context.Context, userID string) (*StreakInfo, error) {
	info := &StreakInfo{}
	err := s.db.QueryRow(ctx,
		`SELECT current_streak, longest_streak, grace_window_active FROM streaks WHERE user_id=$1`, userID,
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
	var grace, m7, m15, m30 bool

	err = tx.QueryRow(ctx,
		`WITH ins AS (
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
		    FOR NO KEY UPDATE OF s`, userID,
	).Scan(&timezone, &current, &longest, &lastDate, &grace, &m7, &m15, &m30)
	if err != nil {
		// The upsert above runs in the same snapshot as the SELECT, so a row
		// created by this very statement is not yet visible to it. Re-read.
		if err = tx.QueryRow(ctx,
			`SELECT COALESCE((SELECT i.timezone FROM users u
			                    JOIN institutions i ON i.id = u.institution_id
			                   WHERE u.id=$1), 'UTC'),
			        current_streak, longest_streak, last_completed_date::text,
			        grace_window_active, milestone_7_claimed,
			        milestone_15_claimed, milestone_30_claimed
			   FROM streaks WHERE user_id=$1 FOR NO KEY UPDATE`, userID,
		).Scan(&timezone, &current, &longest, &lastDate, &grace, &m7, &m15, &m30); err != nil {
			timezone = "UTC"
			current, longest, lastDate, grace, m7, m15, m30 = 0, 0, nil, false, false, false, false
		}
	}

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	today := time.Now().In(loc).Truncate(24 * time.Hour)
	todayDate := today.Format("2006-01-02")
	yesterdayDate := today.AddDate(0, 0, -1).Format("2006-01-02")

	// Already completed today → no change
	if lastDate != nil && *lastDate == todayDate {
		return 0, nil
	}

	// Determine new streak value
	if lastDate == nil {
		// First ever completion
		current = 1
	} else if *lastDate == yesterdayDate {
		// Consecutive day
		current++
	} else if grace && *lastDate == today.AddDate(0, 0, -2).Format("2006-01-02") {
		// Grace window: missed yesterday, completing within 12h grace
		current++
	} else {
		// Streak broken
		current = 1
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
	if current >= 7 {
		streakBadges = append(streakBadges, "on_a_roll")
	}
	if current >= 30 {
		streakBadges = append(streakBadges, "unstoppable")
	}

	// Both updates, the rank lookup, and every badge insert in one statement.
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

	// Reset streaks for users whose grace window expired (last completed 2+ days ago)
	_, err = s.db.Exec(ctx,
		`UPDATE streaks SET current_streak=0, grace_window_active=false,
		 milestone_7_claimed=false, milestone_15_claimed=false, milestone_30_claimed=false
		 WHERE grace_window_active=true
		 AND last_completed_date < (CURRENT_DATE - INTERVAL '2 days')::date`)
	if err != nil {
		return err
	}

	// Sync denormalized fields
	s.db.Exec(ctx,
		`UPDATE users u SET current_streak=s.current_streak, longest_streak=s.longest_streak
		 FROM streaks s WHERE s.user_id=u.id`)

	return nil
}
