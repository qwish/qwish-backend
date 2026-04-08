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

// RecordCompletion updates the streak for a user after completing a quiz.
// Returns bonus points awarded (0 if no milestone hit).
func (s *Service) RecordCompletion(ctx context.Context, userID string, cfg *scoring.Config) (int64, error) {
	// Get current timezone for user's institution
	var timezone string
	s.db.QueryRow(ctx,
		`SELECT COALESCE(i.timezone,'UTC') FROM users u LEFT JOIN institutions i ON i.id=u.institution_id WHERE u.id=$1`, userID,
	).Scan(&timezone)

	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	today := time.Now().In(loc).Truncate(24 * time.Hour)
	todayDate := today.Format("2006-01-02")
	yesterdayDate := today.AddDate(0, 0, -1).Format("2006-01-02")

	var current, longest int
	var lastDate *string
	var grace, m7, m15, m30 bool

	err = s.db.QueryRow(ctx,
		`SELECT current_streak, longest_streak, last_completed_date::text, grace_window_active,
		        milestone_7_claimed, milestone_15_claimed, milestone_30_claimed
		 FROM streaks WHERE user_id=$1`, userID,
	).Scan(&current, &longest, &lastDate, &grace, &m7, &m15, &m30)
	if err != nil {
		// Create streak record
		s.db.Exec(ctx, `INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, userID)
		current, longest, lastDate, grace, m7, m15, m30 = 0, 0, nil, false, false, false, false
	}

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

	s.db.Exec(ctx,
		`UPDATE streaks SET current_streak=$1, longest_streak=$2, last_completed_date=$3,
		 grace_window_active=false, milestone_7_claimed=$4, milestone_15_claimed=$5, milestone_30_claimed=$6,
		 updated_at=now()
		 WHERE user_id=$7`,
		current, longest, todayDate, m7, m15, m30, userID)

	// Update denormalized fields on users
	s.db.Exec(ctx,
		`UPDATE users SET current_streak=$1, longest_streak=$2, last_completed_date=$3, updated_at=now() WHERE id=$4`,
		current, longest, todayDate, userID)

	// top_10 badge check
	var rank int
	s.db.QueryRow(ctx,
		`SELECT COUNT(*)+1 FROM users WHERE institution_id=(SELECT institution_id FROM users WHERE id=$1) AND total_points > (SELECT total_points FROM users WHERE id=$1) AND status='active'`,
		userID,
	).Scan(&rank)
	if rank <= 10 {
		s.db.Exec(ctx, `INSERT INTO badges (user_id, badge_type) VALUES ($1,'top_10') ON CONFLICT DO NOTHING`, userID)
	}

	// on_a_roll badge
	if current >= 7 {
		s.db.Exec(ctx, `INSERT INTO badges (user_id, badge_type) VALUES ($1,'on_a_roll') ON CONFLICT DO NOTHING`, userID)
	}
	// unstoppable badge
	if current >= 30 {
		s.db.Exec(ctx, `INSERT INTO badges (user_id, badge_type) VALUES ($1,'unstoppable') ON CONFLICT DO NOTHING`, userID)
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
