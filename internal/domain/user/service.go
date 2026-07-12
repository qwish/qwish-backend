package user

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrProfilePrivate is returned when a viewer is not allowed to see a private
// profile (the owner has not enabled recruiter visibility and the viewer is
// neither the owner nor a follower).
var ErrProfilePrivate = errors.New("profile is private")

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

// GetPublicProfile returns the public view of targetID's profile, enforcing
// privacy: a profile is private by default and only visible to the owner, to
// followers, or once the owner enables recruiter visibility. Returns
// ErrProfilePrivate otherwise.
func (s *Service) GetPublicProfile(ctx context.Context, viewerID, targetID string) (*PublicProfile, error) {
	p := &PublicProfile{}
	var instName *string
	var recruiterVisible bool
	err := s.db.QueryRow(ctx,
		`SELECT u.id, u.display_name, i.name, u.total_points, u.current_streak, u.longest_streak,
		        u.recruiter_visible
		 FROM users u
		 LEFT JOIN institutions i ON i.id = u.institution_id
		 WHERE u.id = $1 AND u.deleted_at IS NULL AND u.status = 'active'`, targetID,
	).Scan(&p.ID, &p.DisplayName, &instName, &p.TotalPoints, &p.CurrentStreak, &p.LongestStreak, &recruiterVisible)
	if err != nil {
		return nil, err
	}

	if viewerID != targetID && !recruiterVisible {
		var followsTarget bool
		s.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM user_follows WHERE follower_id=$1 AND followee_id=$2)`,
			viewerID, targetID).Scan(&followsTarget)
		if !followsTarget {
			return nil, ErrProfilePrivate
		}
	}
	if instName != nil {
		p.Institution = *instName
	}

	// Quizzes completed
	s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM quiz_attempts WHERE user_id = $1 AND status = 'completed'`, targetID,
	).Scan(&p.QuizzesCompleted)

	// Badges
	rows, _ := s.db.Query(ctx,
		`SELECT badge_type FROM badges WHERE user_id = $1 ORDER BY earned_at`, targetID)
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

// ── Settings: theme (dark mode) + privacy ──────────────────────────────────────

type Settings struct {
	Theme            string `json:"theme"`             // 'auto' | 'light' | 'dark'
	ProfilePrivate   bool   `json:"profile_private"`   // private by default
	RecruiterVisible bool   `json:"recruiter_visible"` // opt-in recruiter discovery
}

func (s *Service) GetSettings(ctx context.Context, userID string) (*Settings, error) {
	st := &Settings{}
	err := s.db.QueryRow(ctx,
		`SELECT theme, profile_private, recruiter_visible FROM users WHERE id=$1`, userID,
	).Scan(&st.Theme, &st.ProfilePrivate, &st.RecruiterVisible)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// UpdateSettings applies the non-nil fields. Returns ErrInvalidTheme for a bad
// theme value.
func (s *Service) UpdateSettings(ctx context.Context, userID string, theme *string, private, recruiter *bool) (*Settings, error) {
	if theme != nil {
		switch *theme {
		case "auto", "light", "dark":
		default:
			return nil, ErrInvalidTheme
		}
		s.db.Exec(ctx, `UPDATE users SET theme=$1, updated_at=now() WHERE id=$2`, *theme, userID)
	}
	if private != nil {
		s.db.Exec(ctx, `UPDATE users SET profile_private=$1, updated_at=now() WHERE id=$2`, *private, userID)
	}
	if recruiter != nil {
		s.db.Exec(ctx, `UPDATE users SET recruiter_visible=$1, updated_at=now() WHERE id=$2`, *recruiter, userID)
	}
	return s.GetSettings(ctx, userID)
}

var ErrInvalidTheme = errors.New("theme must be one of: auto, light, dark")

// ── Notification preferences ────────────────────────────────────────────────

type NotifPrefs struct {
	PushRankChanges     bool `json:"push_rank_changes"`
	PushWeeklyDigest    bool `json:"push_weekly_digest"`
	PushStreakNudge     bool `json:"push_streak_nudge"`
	PushStudyGroup      bool `json:"push_study_group"`
	EmailWeeklyInsights bool `json:"email_weekly_insights"`
}

func defaultNotifPrefs() *NotifPrefs {
	return &NotifPrefs{true, true, true, true, true}
}

// GetNotifPrefs returns the user's preferences, falling back to all-enabled
// defaults when no row exists yet.
func (s *Service) GetNotifPrefs(ctx context.Context, userID string) (*NotifPrefs, error) {
	p := &NotifPrefs{}
	err := s.db.QueryRow(ctx,
		`SELECT push_rank_changes, push_weekly_digest, push_streak_nudge, push_study_group, email_weekly_insights
		 FROM notification_preferences WHERE user_id=$1`, userID,
	).Scan(&p.PushRankChanges, &p.PushWeeklyDigest, &p.PushStreakNudge, &p.PushStudyGroup, &p.EmailWeeklyInsights)
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultNotifPrefs(), nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// UpdateNotifPrefs upserts the user's preferences, applying only the supplied
// (non-nil) fields over the current values.
func (s *Service) UpdateNotifPrefs(ctx context.Context, userID string, in map[string]bool) (*NotifPrefs, error) {
	cur, err := s.GetNotifPrefs(ctx, userID)
	if err != nil {
		return nil, err
	}
	if v, ok := in["push_rank_changes"]; ok {
		cur.PushRankChanges = v
	}
	if v, ok := in["push_weekly_digest"]; ok {
		cur.PushWeeklyDigest = v
	}
	if v, ok := in["push_streak_nudge"]; ok {
		cur.PushStreakNudge = v
	}
	if v, ok := in["push_study_group"]; ok {
		cur.PushStudyGroup = v
	}
	if v, ok := in["email_weekly_insights"]; ok {
		cur.EmailWeeklyInsights = v
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO notification_preferences
		   (user_id, push_rank_changes, push_weekly_digest, push_streak_nudge, push_study_group, email_weekly_insights, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6, now())
		 ON CONFLICT (user_id) DO UPDATE SET
		   push_rank_changes=EXCLUDED.push_rank_changes,
		   push_weekly_digest=EXCLUDED.push_weekly_digest,
		   push_streak_nudge=EXCLUDED.push_streak_nudge,
		   push_study_group=EXCLUDED.push_study_group,
		   email_weekly_insights=EXCLUDED.email_weekly_insights,
		   updated_at=now()`,
		userID, cur.PushRankChanges, cur.PushWeeklyDigest, cur.PushStreakNudge, cur.PushStudyGroup, cur.EmailWeeklyInsights)
	if err != nil {
		return nil, err
	}
	return cur, nil
}

// ── Weekly score insights ────────────────────────────────────────────────────

type WeeklyInsights struct {
	WeekStart       time.Time `json:"week_start"`
	WeekEnd         time.Time `json:"week_end"`
	PointsThisWeek  int64     `json:"points_this_week"`
	PointsLastWeek  int64     `json:"points_last_week"`
	PointsDeltaPct  float64   `json:"points_delta_pct"`
	QuizzesThisWeek int       `json:"quizzes_this_week"`
	AvgScoreThisWeek float64  `json:"avg_score_this_week"`
	CurrentStreak   int       `json:"current_streak"`
	Domain          *string   `json:"domain,omitempty"`
	DomainRank      *int      `json:"domain_rank,omitempty"`
	Suggestion      string    `json:"suggestion"`
}

// GetWeeklyInsights computes the user's last-7-days performance breakdown plus a
// week-over-week comparison and a coaching suggestion.
func (s *Service) GetWeeklyInsights(ctx context.Context, userID, instID string) (*WeeklyInsights, error) {
	now := time.Now().UTC()
	weekStart := now.AddDate(0, 0, -7)
	prevStart := now.AddDate(0, 0, -14)

	wi := &WeeklyInsights{WeekStart: weekStart, WeekEnd: now}

	// Points earned this week vs the week before (from the ledger).
	s.db.QueryRow(ctx,
		`SELECT
		   COALESCE(SUM(amount) FILTER (WHERE created_at >= $2 AND amount > 0),0),
		   COALESCE(SUM(amount) FILTER (WHERE created_at >= $3 AND created_at < $2 AND amount > 0),0)
		 FROM points_ledger WHERE user_id=$1`,
		userID, weekStart, prevStart,
	).Scan(&wi.PointsThisWeek, &wi.PointsLastWeek)
	if wi.PointsLastWeek > 0 {
		wi.PointsDeltaPct = float64(wi.PointsThisWeek-wi.PointsLastWeek) / float64(wi.PointsLastWeek) * 100
	} else if wi.PointsThisWeek > 0 {
		wi.PointsDeltaPct = 100
	}

	// Quizzes completed + average score this week.
	s.db.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(AVG(score_pct),0)
		 FROM quiz_attempts WHERE user_id=$1 AND status='completed' AND completed_at >= $2`,
		userID, weekStart,
	).Scan(&wi.QuizzesThisWeek, &wi.AvgScoreThisWeek)

	// Streak + domain.
	var domain *string
	s.db.QueryRow(ctx, `SELECT current_streak, domain FROM users WHERE id=$1`, userID).
		Scan(&wi.CurrentStreak, &domain)
	if domain != nil && *domain != "" {
		wi.Domain = domain
		var rank int
		var myPoints int64
		s.db.QueryRow(ctx, `SELECT total_points FROM users WHERE id=$1`, userID).Scan(&myPoints)
		s.db.QueryRow(ctx,
			`SELECT COUNT(*)+1 FROM users WHERE domain=$1 AND status='active' AND total_points > $2`,
			*domain, myPoints).Scan(&rank)
		wi.DomainRank = &rank
	}

	wi.Suggestion = buildSuggestion(wi)
	return wi, nil
}

// ── Insights breakdown ──────────────────────────────────────────────────────

// ScoreComponents are the five Qwish Score inputs as lifetime fractions (0–1).
type ScoreComponents struct {
	Accuracy    float64 `json:"accuracy"`
	Difficulty  float64 `json:"difficulty"`
	Consistency float64 `json:"consistency"`
	Speed       float64 `json:"speed"`
	Activity    float64 `json:"activity"`
}

type SubdomainPerf struct {
	Slug      string  `json:"slug"`
	Label     string  `json:"label"`
	AvgScore  float64 `json:"avg_score"` // question-weighted accuracy, 0–100
	Questions int     `json:"questions"`
	Attempts  int     `json:"attempts"`
	LowSample bool    `json:"low_sample"` // < 10 answered questions
}

type DomainPerf struct {
	Slug       string          `json:"slug"`
	Label      string          `json:"label"`
	AvgScore   float64         `json:"avg_score"`
	Questions  int             `json:"questions"`
	Attempts   int             `json:"attempts"`
	LowSample  bool            `json:"low_sample"`
	Subdomains []SubdomainPerf `json:"subdomains"`
}

type InsightsBreakdown struct {
	QwishScore float64         `json:"qwish_score"` // weighted sum of components, 0–100
	Components ScoreComponents `json:"components"`
	Domains    []DomainPerf    `json:"domains"`
}

const lowSampleQuestions = 10

func (s *Service) GetInsightsBreakdown(ctx context.Context, userID string) (*InsightsBreakdown, error) {
	var c ScoreComponents

	// Accuracy (50%): question-weighted across completed attempts.
	var totalCorrect, totalQuestions int64
	s.db.QueryRow(ctx,
		`SELECT COALESCE(SUM(total_correct),0), COALESCE(SUM(total_questions),0)
		 FROM quiz_attempts WHERE user_id=$1 AND status='completed'`, userID,
	).Scan(&totalCorrect, &totalQuestions)
	if totalQuestions > 0 {
		c.Accuracy = float64(totalCorrect) / float64(totalQuestions)
	}

	// Difficulty (20%): correct difficulty over all answered difficulty.
	// Speed (10%): mean per-response speed factor over correct answers, using
	// the same piecewise curve as scoring at completion time.
	var totalDiff, correctDiff, speedAvg float64
	s.db.QueryRow(ctx, `
		SELECT
		  COALESCE(SUM(q.difficulty),0),
		  COALESCE(SUM(q.difficulty) FILTER (WHERE qr.is_correct),0),
		  COALESCE(AVG(CASE
		      WHEN qr.time_taken_ms < 1000 THEN 0.1
		      WHEN qr.time_taken_ms <= (q.time_limit_seconds*1000)/3.0 THEN 1.0
		      ELSE GREATEST(
		        (q.time_limit_seconds*1000.0 - qr.time_taken_ms)
		        / NULLIF(q.time_limit_seconds*1000.0 - q.time_limit_seconds*1000.0/3.0, 0), 0.1)
		    END) FILTER (WHERE qr.is_correct AND qr.time_taken_ms IS NOT NULL), 0)
		FROM question_responses qr
		JOIN questions q ON q.id = qr.question_id
		JOIN quiz_attempts a ON a.id = qr.attempt_id
		WHERE a.user_id=$1 AND a.status='completed'`, userID,
	).Scan(&totalDiff, &correctDiff, &speedAvg)
	if totalDiff > 0 {
		c.Difficulty = correctDiff / totalDiff
	}
	c.Speed = speedAvg

	// Consistency (15%) from streak, Activity (5%) from completed count —
	// same tiers as scoring.CalculateQwishScore.
	var streak, completed int
	s.db.QueryRow(ctx, `SELECT current_streak FROM users WHERE id=$1`, userID).Scan(&streak)
	s.db.QueryRow(ctx, `SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed'`, userID).Scan(&completed)
	c.Consistency = streakTier(streak)
	c.Activity = activityTier(completed)

	qwishScore := c.Accuracy*50 + c.Difficulty*20 + c.Consistency*15 + c.Speed*10 + c.Activity*5

	domains, err := s.domainPerformance(ctx, userID)
	if err != nil {
		return nil, err
	}

	return &InsightsBreakdown{
		QwishScore: round1(qwishScore),
		Components: c,
		Domains:    domains,
	}, nil
}

// domainPerformance returns question-weighted accuracy per domain, each with a
// subdomain roll-up. Grouped once at (domain, subdomain) then folded by domain.
func (s *Service) domainPerformance(ctx context.Context, userID string) ([]DomainPerf, error) {
	rows, err := s.db.Query(ctx, `
		SELECT COALESCE(qz.domain,'general'), COALESCE(dm.label,'General'), COALESCE(dm.sort,99),
		       COALESCE(qz.subdomain,'general_mixed'), COALESCE(sd.label,'Mixed'), COALESCE(sd.sort,99),
		       COUNT(*) AS questions,
		       COUNT(*) FILTER (WHERE qr.is_correct) AS correct,
		       COUNT(DISTINCT a.id) AS attempts
		FROM quiz_attempts a
		JOIN question_responses qr ON qr.attempt_id = a.id
		JOIN quizzes qz ON qz.id = a.quiz_id
		LEFT JOIN domains dm ON dm.slug = qz.domain
		LEFT JOIN subdomains sd ON sd.slug = qz.subdomain
		WHERE a.user_id=$1 AND a.status='completed'
		GROUP BY qz.domain, dm.label, dm.sort, qz.subdomain, sd.label, sd.sort
		ORDER BY COALESCE(dm.sort,99), COALESCE(sd.sort,99)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var order []string // preserve domain sort order
	byDomain := map[string]*DomainPerf{}
	corr := map[string]int{} // domain slug → correct count for weighted avg

	for rows.Next() {
		var dSlug, dLabel, sdSlug, sdLabel string
		var dSort, sdSort, questions, correct, attempts int
		if err := rows.Scan(&dSlug, &dLabel, &dSort, &sdSlug, &sdLabel, &sdSort, &questions, &correct, &attempts); err != nil {
			return nil, err
		}
		dp, ok := byDomain[dSlug]
		if !ok {
			dp = &DomainPerf{Slug: dSlug, Label: dLabel}
			byDomain[dSlug] = dp
			order = append(order, dSlug)
		}
		sub := SubdomainPerf{
			Slug: sdSlug, Label: sdLabel, Questions: questions, Attempts: attempts,
			LowSample: questions < lowSampleQuestions,
		}
		if questions > 0 {
			sub.AvgScore = round1(float64(correct) / float64(questions) * 100)
		}
		dp.Subdomains = append(dp.Subdomains, sub)
		dp.Questions += questions
		dp.Attempts += attempts
		corr[dSlug] += correct
	}

	out := make([]DomainPerf, 0, len(order))
	for _, slug := range order {
		dp := byDomain[slug]
		if dp.Questions > 0 {
			dp.AvgScore = round1(float64(corr[slug]) / float64(dp.Questions) * 100)
		}
		dp.LowSample = dp.Questions < lowSampleQuestions
		out = append(out, *dp)
	}
	return out, nil
}

// ── Score trend ─────────────────────────────────────────────────────────────

type TrendPoint struct {
	Label string  `json:"label"`
	Value float64 `json:"value"` // avg score_pct in the bucket, carried forward
}

// GetScoreTrend returns bucketed average score_pct over time for the chart.
// range: "4w" → 4 weekly buckets, "12w" → 12 weekly, "all" → 12 monthly.
// Empty buckets carry forward the previous value so the line stays continuous.
func (s *Service) GetScoreTrend(ctx context.Context, userID, rng string) ([]TrendPoint, error) {
	unit, buckets := "week", 12
	switch rng {
	case "4w":
		unit, buckets = "week", 4
	case "all":
		unit, buckets = "month", 12
	}

	// $1 unit ('week'|'month'), $2 userID, $3 bucket count. Interval strings are
	// built from the unit, e.g. "3 week" / "1 month" — both valid ::interval.
	rows, err := s.db.Query(ctx, `
		WITH buckets AS (
		  SELECT generate_series(
		    date_trunc($1, now()) - (($3 - 1) || ' ' || $1)::interval,
		    date_trunc($1, now()),
		    ('1 ' || $1)::interval
		  ) AS b
		)
		SELECT buckets.b, AVG(qa.score_pct)
		FROM buckets
		LEFT JOIN quiz_attempts qa
		  ON qa.user_id = $2 AND qa.status = 'completed'
		  AND date_trunc($1, qa.completed_at) = buckets.b
		GROUP BY buckets.b
		ORDER BY buckets.b`, unit, userID, buckets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]TrendPoint, 0, buckets)
	var last float64
	for rows.Next() {
		var b time.Time
		var avg *float64
		if err := rows.Scan(&b, &avg); err != nil {
			return nil, err
		}
		if avg != nil {
			last = round1(*avg)
		}
		out = append(out, TrendPoint{Label: trendLabel(b, unit), Value: last})
	}
	return out, nil
}

func trendLabel(b time.Time, unit string) string {
	if unit == "month" {
		return b.Format("Jan")
	}
	return b.Format("1/2") // month/day of the week bucket
}

func streakTier(streak int) float64 {
	switch {
	case streak >= 30:
		return 1.0
	case streak >= 15:
		return 0.8
	case streak >= 7:
		return 0.6
	case streak >= 3:
		return 0.4
	case streak >= 1:
		return 0.2
	default:
		return 0
	}
}

func activityTier(count int) float64 {
	switch {
	case count >= 50:
		return 1.0
	case count >= 20:
		return 0.8
	case count >= 10:
		return 0.6
	case count >= 5:
		return 0.4
	case count >= 1:
		return 0.2
	default:
		return 0
	}
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

func buildSuggestion(wi *WeeklyInsights) string {
	switch {
	case wi.QuizzesThisWeek == 0:
		return "You didn't practice this week — take one quiz today to restart your momentum."
	case wi.CurrentStreak == 0:
		return "Your streak reset. Complete a quiz today and tomorrow to rebuild it."
	case wi.AvgScoreThisWeek < 60:
		return "Your average score dipped below 60%. Slow down and review explanations after each quiz."
	case wi.PointsDeltaPct < 0:
		return "You earned fewer points than last week. Try two short quizzes a day to bounce back."
	default:
		return "Strong week! Keep the streak alive and aim to climb your domain leaderboard."
	}
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

