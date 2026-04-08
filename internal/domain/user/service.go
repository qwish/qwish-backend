package user

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{db: db}
}

type Profile struct {
	ID            string     `json:"id"`
	FullName      string     `json:"full_name"`
	DisplayName   string     `json:"display_name"`
	Email         string     `json:"email"`
	Role          string     `json:"role"`
	InstitutionID *string    `json:"institution_id,omitempty"`
	Institution   *InstInfo  `json:"institution,omitempty"`
	Status        string     `json:"status"`
	TotalPoints   int64      `json:"total_points"`
	CurrentStreak int        `json:"current_streak"`
	LongestStreak int        `json:"longest_streak"`
	MemberSince   time.Time  `json:"member_since"`
	LastActiveAt  *time.Time `json:"last_active_at,omitempty"`
}

type InstInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type PublicProfile struct {
	ID              string `json:"id"`
	DisplayName     string `json:"display_name"`
	Institution     string `json:"institution,omitempty"`
	TotalPoints     int64  `json:"total_points"`
	CurrentStreak   int    `json:"current_streak"`
	LongestStreak   int    `json:"longest_streak"`
	QuizzesCompleted int   `json:"quizzes_completed"`
	Badges          []string `json:"badges"`
}

type Stats struct {
	TotalPoints      int64   `json:"total_points"`
	QuizzesTaken     int     `json:"quizzes_taken"`
	AverageScore     float64 `json:"average_score"`
	CurrentStreak    int     `json:"current_streak"`
	LongestStreak    int     `json:"longest_streak"`
}

type Badge struct {
	BadgeType string    `json:"badge_type"`
	EarnedAt  time.Time `json:"earned_at"`
	Earned    bool      `json:"earned"`
}

type AttemptSummary struct {
	ID          string     `json:"id"`
	QuizID      string     `json:"quiz_id"`
	QuizTitle   string     `json:"quiz_title"`
	ScorePct    float64    `json:"score_pct"`
	PointsDelta int64      `json:"points_delta"`
	Status      string     `json:"status"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func (s *Service) GetProfile(ctx context.Context, userID string) (*Profile, error) {
	p := &Profile{}
	var instID *string
	err := s.db.QueryRow(ctx,
		`SELECT u.id, u.full_name, u.display_name, u.email, u.role, u.institution_id,
		        u.status, u.total_points, u.current_streak, u.longest_streak, u.member_since, u.last_active_at
		 FROM users u WHERE u.id = $1 AND u.deleted_at IS NULL`, userID,
	).Scan(&p.ID, &p.FullName, &p.DisplayName, &p.Email, &p.Role, &instID,
		&p.Status, &p.TotalPoints, &p.CurrentStreak, &p.LongestStreak, &p.MemberSince, &p.LastActiveAt)
	if err != nil {
		return nil, err
	}
	if instID != nil {
		p.InstitutionID = instID
		var inst InstInfo
		s.db.QueryRow(ctx, `SELECT id, name FROM institutions WHERE id = $1`, *instID).
			Scan(&inst.ID, &inst.Name)
		p.Institution = &inst
	}
	return p, nil
}

func (s *Service) GetPublicProfile(ctx context.Context, userID string) (*PublicProfile, error) {
	p := &PublicProfile{}
	var instName *string
	err := s.db.QueryRow(ctx,
		`SELECT u.id, u.display_name, i.name, u.total_points, u.current_streak, u.longest_streak
		 FROM users u
		 LEFT JOIN institutions i ON i.id = u.institution_id
		 WHERE u.id = $1 AND u.deleted_at IS NULL AND u.status = 'active'`, userID,
	).Scan(&p.ID, &p.DisplayName, &instName, &p.TotalPoints, &p.CurrentStreak, &p.LongestStreak)
	if err != nil {
		return nil, err
	}
	if instName != nil {
		p.Institution = *instName
	}

	// Quizzes completed
	s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM quiz_attempts WHERE user_id = $1 AND status = 'completed'`, userID,
	).Scan(&p.QuizzesCompleted)

	// Badges
	rows, _ := s.db.Query(ctx,
		`SELECT badge_type FROM badges WHERE user_id = $1 ORDER BY earned_at`, userID)
	defer rows.Close()
	for rows.Next() {
		var bt string
		rows.Scan(&bt)
		p.Badges = append(p.Badges, bt)
	}
	if p.Badges == nil {
		p.Badges = []string{}
	}
	return p, nil
}

func (s *Service) GetStats(ctx context.Context, userID string) (*Stats, error) {
	st := &Stats{}
	s.db.QueryRow(ctx,
		`SELECT u.total_points, u.current_streak, u.longest_streak FROM users u WHERE u.id = $1`, userID,
	).Scan(&st.TotalPoints, &st.CurrentStreak, &st.LongestStreak)

	s.db.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(AVG(score_pct),0) FROM quiz_attempts WHERE user_id = $1 AND status = 'completed'`, userID,
	).Scan(&st.QuizzesTaken, &st.AverageScore)
	return st, nil
}

var allBadgeTypes = []string{
	"first_quiz", "on_a_roll", "unstoppable", "top_10",
	"perfect_score", "speed_demon", "sharp_mind", "explorer",
}

func (s *Service) GetBadges(ctx context.Context, userID string) ([]Badge, error) {
	rows, err := s.db.Query(ctx,
		`SELECT badge_type, earned_at FROM badges WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	earned := map[string]time.Time{}
	for rows.Next() {
		var bt string
		var ea time.Time
		rows.Scan(&bt, &ea)
		earned[bt] = ea
	}

	result := make([]Badge, 0, len(allBadgeTypes))
	for _, bt := range allBadgeTypes {
		if ea, ok := earned[bt]; ok {
			result = append(result, Badge{BadgeType: bt, EarnedAt: ea, Earned: true})
		} else {
			result = append(result, Badge{BadgeType: bt, Earned: false})
		}
	}
	return result, nil
}

func (s *Service) GetAttempts(ctx context.Context, userID string, page, limit int) ([]AttemptSummary, int, error) {
	offset := (page - 1) * limit
	var total int
	s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM quiz_attempts WHERE user_id = $1 AND status = 'completed'`, userID,
	).Scan(&total)

	rows, err := s.db.Query(ctx,
		`SELECT qa.id, qa.quiz_id, q.title, COALESCE(qa.score_pct,0), COALESCE(qa.points_delta,0), qa.status, qa.completed_at
		 FROM quiz_attempts qa
		 JOIN quizzes q ON q.id = qa.quiz_id
		 WHERE qa.user_id = $1 AND qa.status = 'completed'
		 ORDER BY qa.completed_at DESC
		 LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var attempts []AttemptSummary
	for rows.Next() {
		var a AttemptSummary
		rows.Scan(&a.ID, &a.QuizID, &a.QuizTitle, &a.ScorePct, &a.PointsDelta, &a.Status, &a.CompletedAt)
		attempts = append(attempts, a)
	}
	if attempts == nil {
		attempts = []AttemptSummary{}
	}
	return attempts, total, nil
}

func (s *Service) UpdateDisplayName(ctx context.Context, userID, name string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET display_name = $1, updated_at = now() WHERE id = $2`, name, userID)
	return err
}

func (s *Service) SoftDelete(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET status = 'deleted', deleted_at = now(),
		 full_name = '[Deleted User]', display_name = '[Deleted]', email = 'deleted-'||id||'@deleted.invalid',
		 updated_at = now()
		 WHERE id = $1`, userID)
	return err
}
