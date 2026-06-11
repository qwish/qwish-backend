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

// ── Profile views ─────────────────────────────────────────────────────────────

type ProfileViewStats struct {
	Today    int `json:"today"`
	ThisWeek int `json:"this_week"`
	Total    int `json:"total"`
}

func (s *Service) RecordProfileView(ctx context.Context, viewerID, viewedID string) {
	s.db.Exec(ctx,
		`INSERT INTO profile_views (viewer_id, viewed_id) VALUES ($1, $2)`,
		viewerID, viewedID)
}

func (s *Service) GetProfileViews(ctx context.Context, userID string) (*ProfileViewStats, error) {
	v := &ProfileViewStats{}
	err := s.db.QueryRow(ctx,
		`SELECT
		  COUNT(*) FILTER (WHERE viewed_at >= CURRENT_DATE)                   AS today,
		  COUNT(*) FILTER (WHERE viewed_at >= CURRENT_DATE - INTERVAL '7 days') AS this_week,
		  COUNT(*)                                                              AS total
		 FROM profile_views
		 WHERE viewed_id = $1 AND (viewer_id IS NULL OR viewer_id != $1)`,
		userID,
	).Scan(&v.Today, &v.ThisWeek, &v.Total)
	return v, err
}

// ── Rank & percentile ─────────────────────────────────────────────────────────

type RankInfo struct {
	GlobalRank      int     `json:"global_rank"`
	GlobalTotal     int     `json:"global_total"`
	InstRank        *int    `json:"institution_rank,omitempty"`
	InstTotal       *int    `json:"institution_total,omitempty"`
	DomainRank      *int    `json:"domain_rank,omitempty"`
	DomainTotal     *int    `json:"domain_total,omitempty"`
	TopPercentile   float64 `json:"top_percentile"` // e.g. 12.5 → "Top 12.5%"
}

func (s *Service) GetRank(ctx context.Context, userID, instID string) (*RankInfo, error) {
	ri := &RankInfo{}

	var totalPoints int64
	var domain *string
	s.db.QueryRow(ctx,
		`SELECT total_points, domain FROM users WHERE id = $1`, userID,
	).Scan(&totalPoints, &domain)

	// Global rank
	s.db.QueryRow(ctx,
		`SELECT COUNT(*)+1 FROM users WHERE status='active' AND total_points > $1`, totalPoints,
	).Scan(&ri.GlobalRank)
	s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE status='active'`,
	).Scan(&ri.GlobalTotal)

	if ri.GlobalTotal > 0 {
		above := float64(ri.GlobalRank - 1)
		ri.TopPercentile = above / float64(ri.GlobalTotal) * 100
	}

	// Institution rank
	if instID != "" {
		var ir, it int
		s.db.QueryRow(ctx,
			`SELECT COUNT(*)+1 FROM users WHERE institution_id=$1 AND status='active' AND total_points > $2`,
			instID, totalPoints,
		).Scan(&ir)
		s.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE institution_id=$1 AND status='active'`, instID,
		).Scan(&it)
		ri.InstRank = &ir
		ri.InstTotal = &it
	}

	// Domain rank
	if domain != nil && *domain != "" {
		var dr, dt int
		s.db.QueryRow(ctx,
			`SELECT COUNT(*)+1 FROM users WHERE domain=$1 AND status='active' AND total_points > $2`,
			*domain, totalPoints,
		).Scan(&dr)
		s.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM users WHERE domain=$1 AND status='active'`, *domain,
		).Scan(&dt)
		ri.DomainRank = &dr
		ri.DomainTotal = &dt
	}

	return ri, nil
}

// ── Milestones ────────────────────────────────────────────────────────────────

type Milestone struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Progress    float64 `json:"progress"`   // 0.0–1.0
	Current     int64   `json:"current"`
	Target      int64   `json:"target"`
	Completed   bool    `json:"completed"`
}

type milestoneSpec struct {
	id, title, desc, typ string
	target               int64
}

var milestoneSpecs = []milestoneSpec{
	{"first_quiz", "First Steps", "Complete your first quiz", "quizzes", 1},
	{"quiz_5", "Getting Started", "Complete 5 quizzes", "quizzes", 5},
	{"quiz_25", "Quiz Enthusiast", "Complete 25 quizzes", "quizzes", 25},
	{"quiz_100", "Century Club", "Complete 100 quizzes", "quizzes", 100},
	{"points_100", "Point Scorer", "Earn 100 points", "points", 100},
	{"points_500", "Achiever", "Earn 500 points", "points", 500},
	{"points_2000", "Elite", "Earn 2000 points", "points", 2000},
	{"points_5000", "Champion", "Earn 5000 points", "points", 5000},
	{"streak_3", "On Fire", "Maintain a 3-day streak", "streak", 3},
	{"streak_7", "Streak Master", "Maintain a 7-day streak", "streak", 7},
	{"streak_30", "Streak Legend", "Maintain a 30-day streak", "streak", 30},
}

func (s *Service) GetMilestones(ctx context.Context, userID string) ([]Milestone, error) {
	var totalPoints int64
	var longestStreak int
	var quizzesTaken int
	s.db.QueryRow(ctx,
		`SELECT u.total_points, u.longest_streak,
		        (SELECT COUNT(*) FROM quiz_attempts WHERE user_id=u.id AND status='completed')
		 FROM users u WHERE u.id = $1`, userID,
	).Scan(&totalPoints, &longestStreak, &quizzesTaken)

	result := make([]Milestone, 0, len(milestoneSpecs))
	for _, spec := range milestoneSpecs {
		var current int64
		switch spec.typ {
		case "quizzes":
			current = int64(quizzesTaken)
		case "points":
			current = totalPoints
		case "streak":
			current = int64(longestStreak)
		}
		progress := float64(current) / float64(spec.target)
		if progress > 1 {
			progress = 1
		}
		result = append(result, Milestone{
			ID:          spec.id,
			Title:       spec.title,
			Description: spec.desc,
			Progress:    progress,
			Current:     current,
			Target:      spec.target,
			Completed:   current >= spec.target,
		})
	}
	return result, nil
}

// ── Education ─────────────────────────────────────────────────────────────────

type Education struct {
	ID              string `json:"id"`
	InstitutionName string `json:"institution_name"`
	Degree          string `json:"degree,omitempty"`
	Field           string `json:"field,omitempty"`
	StartYear       *int   `json:"start_year,omitempty"`
	EndYear         *int   `json:"end_year,omitempty"`
	IsCurrent       bool   `json:"is_current"`
}

func (s *Service) GetEducation(ctx context.Context, userID string) ([]Education, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, institution_name, COALESCE(degree,''), COALESCE(field,''),
		        start_year, end_year, is_current
		 FROM user_education WHERE user_id=$1 ORDER BY COALESCE(start_year,0) DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Education
	for rows.Next() {
		var e Education
		rows.Scan(&e.ID, &e.InstitutionName, &e.Degree, &e.Field,
			&e.StartYear, &e.EndYear, &e.IsCurrent)
		list = append(list, e)
	}
	if list == nil {
		list = []Education{}
	}
	return list, nil
}

func (s *Service) AddEducation(ctx context.Context, userID string, e Education) (Education, error) {
	var out Education
	err := s.db.QueryRow(ctx,
		`INSERT INTO user_education (user_id, institution_name, degree, field, start_year, end_year, is_current)
		 VALUES ($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6,$7)
		 RETURNING id, institution_name, COALESCE(degree,''), COALESCE(field,''),
		           start_year, end_year, is_current`,
		userID, e.InstitutionName, e.Degree, e.Field, e.StartYear, e.EndYear, e.IsCurrent,
	).Scan(&out.ID, &out.InstitutionName, &out.Degree, &out.Field,
		&out.StartYear, &out.EndYear, &out.IsCurrent)
	return out, err
}

func (s *Service) DeleteEducation(ctx context.Context, userID, educationID string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM user_education WHERE id=$1 AND user_id=$2`, educationID, userID)
	return err
}

// ── Skills ────────────────────────────────────────────────────────────────────

func (s *Service) GetSkills(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.Query(ctx,
		`SELECT skill_name FROM user_skills WHERE user_id=$1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var skills []string
	for rows.Next() {
		var sk string
		rows.Scan(&sk)
		skills = append(skills, sk)
	}
	if skills == nil {
		skills = []string{}
	}
	return skills, nil
}

func (s *Service) AddSkill(ctx context.Context, userID, skill string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO user_skills (user_id, skill_name) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		userID, skill)
	return err
}

func (s *Service) DeleteSkill(ctx context.Context, userID, skill string) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM user_skills WHERE user_id=$1 AND skill_name=$2`, userID, skill)
	return err
}

// ── Domain ────────────────────────────────────────────────────────────────────

func (s *Service) UpdateDomain(ctx context.Context, userID, domain string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET domain=$1, updated_at=now() WHERE id=$2`, domain, userID)
	return err
}

// ── Recommendations ───────────────────────────────────────────────────────────

type RecommendedQuiz struct {
	ID            string  `json:"id"`
	Title         string  `json:"title"`
	Description   *string `json:"description,omitempty"`
	QuestionCount int     `json:"question_count"`
	Type          string  `json:"type"`
}

func (s *Service) GetRecommendations(ctx context.Context, userID, instID string) ([]RecommendedQuiz, error) {
	rows, err := s.db.Query(ctx,
		`SELECT q.id, q.title, q.description, q.question_count, q.type
		 FROM quizzes q
		 WHERE (q.institution_id = $1 OR q.visibility = 'public')
		   AND q.status = 'published'
		   AND q.deleted_at IS NULL
		   AND q.id NOT IN (
		       SELECT quiz_id FROM quiz_attempts WHERE user_id = $2 AND status = 'completed'
		   )
		 ORDER BY q.published_at DESC
		 LIMIT 5`,
		instID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []RecommendedQuiz
	for rows.Next() {
		var q RecommendedQuiz
		if err := rows.Scan(&q.ID, &q.Title, &q.Description, &q.QuestionCount, &q.Type); err != nil {
			return nil, err
		}
		list = append(list, q)
	}
	if list == nil {
		list = []RecommendedQuiz{}
	}
	return list, nil
}

