package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/scoring"
)

// ErrProfilePrivate is returned when a viewer is not allowed to see a private
// profile (the owner has not enabled recruiter visibility and the viewer is
// neither the owner nor a follower).
var ErrProfilePrivate = errors.New("profile is private")

var (
	ErrInvalidLearningLanguage = errors.New("unsupported language")
	ErrInvalidLearningTopics   = errors.New("select between 10 and 50 valid topics")
	ErrInterestsRequired       = errors.New("select at least 10 topics")
)

const MinLearningTopics = 10

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
	ID               string   `json:"id"`
	DisplayName      string   `json:"display_name"`
	Institution      string   `json:"institution,omitempty"`
	TotalPoints      int64    `json:"total_points"`
	CurrentStreak    int      `json:"current_streak"`
	LongestStreak    int      `json:"longest_streak"`
	QuizzesCompleted int      `json:"quizzes_completed"`
	Badges           []string `json:"badges"`
}

type Stats struct {
	TotalPoints   int64   `json:"total_points"`
	QuizzesTaken  int     `json:"quizzes_taken"`
	AverageScore  float64 `json:"average_score"`
	CurrentStreak int     `json:"current_streak"`
	LongestStreak int     `json:"longest_streak"`
}

type Badge struct {
	BadgeType   string     `json:"badge_type"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Category    string     `json:"category"`
	Rarity      string     `json:"rarity"`
	Icon        string     `json:"icon"`
	Current     int64      `json:"current"`
	Target      int64      `json:"target"`
	EarnedAt    *time.Time `json:"earned_at,omitempty"`
	Earned      bool       `json:"earned"`
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

type achievementSpec struct {
	id, name, description, category, rarity, icon string
	target                                        int64
}

var achievementSpecs = []achievementSpec{
	{"welcome_aboard", "Welcome Aboard", "Complete onboarding", "getting_started", "Common", "👋", 1},
	{"profile_ready", "Profile Ready", "Complete 100% of required profile fields", "getting_started", "Common", "✨", 1},
	{"first_quiz", "First Sprint", "Complete your very first assessment", "getting_started", "Common", "🚀", 1},
	{"score_unlocked", "Score Unlocked", "Receive your first Qwish Score", "getting_started", "Common", "🔓", 1},
	{"first_steps", "First Steps", "Complete 3 quizzes", "getting_started", "Common", "👣", 3},
	{"getting_serious", "Getting Serious", "Complete 10 quizzes", "getting_started", "Common", "🎯", 10},
	{"quiz_machine", "Quiz Machine", "Complete 25 quizzes", "quiz_challenge", "Rare", "⚙️", 25},
	{"half_century", "Half Century", "Complete 50 quizzes", "quiz_challenge", "Rare", "50", 50},
	{"century", "Century", "Attempt 100 quizzes in total", "quiz_challenge", "Epic", "💯", 100},
	{"quiz_storm", "Quiz Storm", "Complete 5 quizzes in one day", "quiz_challenge", "Rare", "🌪️", 5},
	{"marathon_mind", "Marathon Mind", "Complete 10 quizzes in one day", "quiz_challenge", "Epic", "🏃", 10},
	{"warming_up", "Warming Up", "Maintain a 3-day streak", "streak", "Common", "🔥", 3},
	{"on_a_roll", "On a Roll", "Maintain a 7-day study streak", "streak", "Rare", "🔥", 7},
	{"locked_in", "Locked In", "Maintain a 14-day streak", "streak", "Rare", "🔒", 14},
	{"unstoppable", "Unstoppable", "Reach a 30-day streak. Championship territory.", "streak", "Epic", "⚡", 30},
	{"iron_will", "Iron Will", "Reach a 60-day streak", "streak", "Legendary", "🛡️", 60},
	{"sharp_mind", "Sharp Mind", "Score 90% or above on any quiz", "performance", "Common", "🧠", 1},
	{"perfect_score", "Perfect Score", "Score 100% on any assessment", "performance", "Epic", "⭐", 1},
	{"triple_threat", "Triple Threat", "Score 90%+ on 3 consecutive quizzes", "performance", "Epic", "🎯", 3},
	{"hot_streak", "Hot Streak", "Answer 10 questions correctly in a row", "performance", "Rare", "🔥", 10},
	{"explorer", "Explorer", "Attempt quizzes in 3 different domains", "mastery", "Common", "🗺️", 3},
	{"jack_of_all_trades", "Jack of All Trades", "Attempt quizzes in 5 different domains", "mastery", "Rare", "🧩", 5},
	{"try_everything", "Try Everything", "Attempt every active question type on the platform", "mastery", "Epic", "🎮", 1},
	{"crowd_pleaser", "Crowd Pleaser", "Share your Qwish scorecard 3 times", "social", "Rare", "📣", 3},
	{"on_the_board", "On the Board", "Appear on your institution leaderboard", "ranking", "Common", "📍", 1},
	{"top_10", "Top 10", "Break into the Top 10 of your institution", "ranking", "Epic", "🏆", 1},
	{"number_one", "Number One", "Reach #1 in your institution leaderboard", "ranking", "Legendary", "👑", 1},
	{"close_call", "Close Call", "Miss a perfect score by exactly one question", "secret", "Rare", "😮", 1},
}

func (s *Service) GetBadges(ctx context.Context, userID string) ([]Badge, error) {
	// Every value below is calculated from authenticated, server-owned records.
	// There is deliberately no API that accepts current/target/progress values.
	current := map[string]int64{}
	var quizzes, bestDay, longest, domains, questionTypes, activeTypes, shares int64
	var profileReady, any90, perfect, closeCall bool
	err := s.db.QueryRow(ctx, `WITH mine AS MATERIALIZED (
	    SELECT id, quiz_id, score_pct, total_correct, total_questions, completed_at
	      FROM quiz_attempts WHERE user_id=$1 AND status='completed'
	  ) SELECT
	  (SELECT COUNT(*) FROM mine),
	  COALESCE((SELECT MAX(n) FROM (SELECT COUNT(*) n FROM mine GROUP BY completed_at::date) d),0),
	  COALESCE((SELECT longest_streak FROM users WHERE id=$1),0),
	  (SELECT COUNT(DISTINCT q.domain) FROM mine a JOIN quizzes q ON q.id=a.quiz_id WHERE q.domain IS NOT NULL),
	  (SELECT COUNT(DISTINCT qu.type) FROM question_responses r JOIN mine a ON a.id=r.attempt_id JOIN questions qu ON qu.id=r.question_id),
	  (SELECT COUNT(DISTINCT qu.type) FROM questions qu JOIN quizzes q ON q.id=qu.quiz_id WHERE q.status='published' AND q.deleted_at IS NULL),
	  (SELECT COUNT(*) FROM scorecard_share_days WHERE user_id=$1),
	  COALESCE((SELECT full_name<>'' AND display_name<>'' AND email<>'' AND domain IS NOT NULL AND domain<>'' FROM users WHERE id=$1),false),
	  COALESCE((SELECT bool_or(score_pct>=90) FROM mine),false),
	  COALESCE((SELECT bool_or(score_pct=100) FROM mine),false),
	  COALESCE((SELECT bool_or(total_questions>0 AND total_correct=total_questions-1) FROM mine),false)`, userID).
		Scan(&quizzes, &bestDay, &longest, &domains, &questionTypes, &activeTypes, &shares, &profileReady, &any90, &perfect, &closeCall)
	if err != nil {
		return nil, err
	}
	for _, id := range []string{"first_quiz", "score_unlocked", "first_steps", "getting_serious", "quiz_machine", "half_century", "century"} {
		current[id] = quizzes
	}
	current["welcome_aboard"] = 1
	if profileReady {
		current["profile_ready"] = 1
	}
	current["quiz_storm"] = bestDay
	current["marathon_mind"] = bestDay
	for _, id := range []string{"warming_up", "on_a_roll", "locked_in", "unstoppable", "iron_will"} {
		current[id] = longest
	}
	current["explorer"] = domains
	current["jack_of_all_trades"] = domains
	current["crowd_pleaser"] = shares
	if activeTypes > 0 {
		current["try_everything"] = questionTypes
	}

	if any90 {
		current["sharp_mind"] = 1
	}
	if perfect {
		current["perfect_score"] = 1
	}
	if closeCall {
		current["close_call"] = 1
	}

	// PostgreSQL reduces the full history to two integers, avoiding an
	// unbounded answer stream and allocations in the API process.
	var bestScoreRun, bestCorrectRun int64
	err = s.db.QueryRow(ctx, `WITH
	 scores AS (SELECT score_pct>=90 good, row_number() OVER (ORDER BY completed_at,id) rn FROM quiz_attempts WHERE user_id=$1 AND status='completed'),
	 score_groups AS (SELECT good, rn-row_number() OVER (PARTITION BY good ORDER BY rn) grp FROM scores),
	 answers AS (SELECT COALESCE(r.is_correct,false) good, row_number() OVER (ORDER BY a.completed_at,a.id,r.submitted_at,r.id) rn FROM quiz_attempts a JOIN question_responses r ON r.attempt_id=a.id WHERE a.user_id=$1 AND a.status='completed'),
	 answer_groups AS (SELECT good, rn-row_number() OVER (PARTITION BY good ORDER BY rn) grp FROM answers)
	 SELECT COALESCE((SELECT MAX(n) FROM (SELECT COUNT(*) n FROM score_groups WHERE good GROUP BY grp) x),0),
	        COALESCE((SELECT MAX(n) FROM (SELECT COUNT(*) n FROM answer_groups WHERE good GROUP BY grp) x),0)`, userID).Scan(&bestScoreRun, &bestCorrectRun)
	if err != nil {
		return nil, err
	}
	current["triple_threat"] = bestScoreRun
	current["hot_streak"] = bestCorrectRun

	// Rank uses the incrementally maintained score and eligibility rules from
	// the leaderboard endpoint; it never scans answer history or trusts a client.
	var rank int64
	_ = s.db.QueryRow(ctx, `WITH me AS (
	 SELECT u.institution_id,u.role,COALESCE(ls.qwish_score,100) score,COALESCE(ls.completed_quizzes,0) completed
	 FROM users u LEFT JOIN leaderboard_scores ls ON ls.user_id=u.id WHERE u.id=$1
	) SELECT CASE WHEN institution_id IS NULL OR role<>'student' OR completed<5 THEN 0 ELSE
	 (SELECT COUNT(*)+1 FROM users x LEFT JOIN leaderboard_scores xs ON xs.user_id=x.id
	   WHERE x.institution_id=me.institution_id AND x.status='active' AND x.role='student'
	     AND COALESCE(xs.qwish_score,100)>me.score) END FROM me`, userID).Scan(&rank)
	if rank > 0 {
		current["on_the_board"] = 1
	}
	if rank > 0 && rank <= 10 {
		current["top_10"] = 1
	}
	if rank == 1 {
		current["number_one"] = 1
	}

	earned := map[string]time.Time{}
	earnedRows, err := s.db.Query(ctx, `SELECT badge_type,earned_at FROM badges WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	for earnedRows.Next() {
		var id string
		var at time.Time
		if err := earnedRows.Scan(&id, &at); err != nil {
			earnedRows.Close()
			return nil, err
		}
		earned[id] = at
	}
	if err := earnedRows.Err(); err != nil {
		earnedRows.Close()
		return nil, err
	}
	earnedRows.Close()

	var newlyEarned []string
	for _, spec := range achievementSpecs {
		target := spec.target
		if spec.id == "try_everything" && activeTypes > 0 {
			target = activeTypes
		}
		if current[spec.id] >= target && earned[spec.id].IsZero() {
			newlyEarned = append(newlyEarned, spec.id)
		}
	}
	if len(newlyEarned) > 0 {
		rows, insertErr := s.db.Query(ctx, `INSERT INTO badges(user_id,badge_type) SELECT $1,x FROM unnest($2::text[]) x ON CONFLICT DO NOTHING RETURNING badge_type,earned_at`, userID, newlyEarned)
		err = insertErr
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var at time.Time
			if err := rows.Scan(&id, &at); err != nil {
				rows.Close()
				return nil, err
			}
			earned[id] = at
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	result := make([]Badge, 0, len(achievementSpecs))
	for _, spec := range achievementSpecs {
		target := spec.target
		if spec.id == "try_everything" && activeTypes > 0 {
			target = activeTypes
		}
		at, ok := earned[spec.id]
		var atp *time.Time
		if ok {
			v := at
			atp = &v
		}
		result = append(result, Badge{BadgeType: spec.id, Name: spec.name, Description: spec.description, Category: spec.category, Rarity: spec.rarity, Icon: spec.icon, Current: current[spec.id], Target: target, EarnedAt: atp, Earned: ok})
	}
	return result, nil
}

// RecordScorecardShare credits at most one share per server day. Replays and
// forged client counters cannot advance it more than once in that period.
func (s *Service) RecordScorecardShare(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO scorecard_share_days(user_id) VALUES($1) ON CONFLICT DO NOTHING`, userID)
	return err
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

func (s *Service) GetAttemptsAfter(ctx context.Context, userID string, at time.Time, id string, limit int) ([]AttemptSummary, int, error) {
	var total int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed'`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(ctx, `SELECT qa.id,qa.quiz_id,q.title,COALESCE(qa.score_pct,0),COALESCE(qa.points_delta,0),qa.status,qa.completed_at
		FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
		WHERE qa.user_id=$1 AND qa.status='completed' AND (qa.completed_at,qa.id)<($2,$3::uuid)
		ORDER BY qa.completed_at DESC,qa.id DESC LIMIT $4`, userID, at, id, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AttemptSummary{}
	for rows.Next() {
		var a AttemptSummary
		if err := rows.Scan(&a.ID, &a.QuizID, &a.QuizTitle, &a.ScorePct, &a.PointsDelta, &a.Status, &a.CompletedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (s *Service) UpdateDisplayName(ctx context.Context, userID, name string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE users SET display_name = $1, updated_at = now() WHERE id = $2`, name, userID)
	return err
}

// UpdatePersonalFields applies a SET clause built by buildUserPatch. The
// clause is assembled from a fixed column list, never from client input; the
// caller appends the user id as the final argument.
func (s *Service) UpdatePersonalFields(ctx context.Context, set string, args []interface{}) error {
	_, err := s.db.Exec(ctx,
		fmt.Sprintf(`UPDATE users SET %s, updated_at = now() WHERE id = $%d`, set, len(args)),
		args...)
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
	GlobalRank               int     `json:"global_rank"`
	GlobalTotal              int     `json:"global_total"`
	InstRank                 *int    `json:"institution_rank,omitempty"`
	InstTotal                *int    `json:"institution_total,omitempty"`
	DomainRank               *int    `json:"domain_rank,omitempty"`
	DomainTotal              *int    `json:"domain_total,omitempty"`
	TopPercentile            float64 `json:"top_percentile"` // e.g. 12.5 → "Top 12.5%"
	DistinctQuizzesCompleted int     `json:"distinct_quizzes_completed"`
	LeaderboardUnlocked      bool    `json:"leaderboard_unlocked"`
}

func (s *Service) GetRank(ctx context.Context, userID, instID string) (*RankInfo, error) {
	ri := &RankInfo{}

	// A CTE binds the caller's points/domain once, then every rank/total is a
	// scalar subquery referencing it — collapsing 7 sequential round-trips into
	// one. Institution/domain counts are computed unconditionally (cheap) and
	// only surfaced below when applicable; NULLIF guards the empty instID so the
	// ::uuid cast doesn't blow up.
	var domain *string
	var role string
	var ir, it, dr, dt int
	err := s.db.QueryRow(ctx, `
		WITH me AS (
			SELECT total_points, domain, role FROM users WHERE id=$1
		), eligible AS (
			SELECT u.id, u.total_points, u.domain, u.institution_id
			  FROM users u
			 WHERE u.status='active' AND u.role='student'
			   AND (
				SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa
				 WHERE qa.user_id=u.id AND qa.status='completed'
			   ) >= 5
		)
		SELECT
			(SELECT domain FROM me),
			(SELECT role FROM me),
			(SELECT COUNT(DISTINCT quiz_id) FROM quiz_attempts WHERE user_id=$1 AND status='completed'),
			(SELECT COUNT(*)+1 FROM eligible WHERE total_points>(SELECT total_points FROM me)),
			(SELECT COUNT(*) FROM eligible),
			(SELECT COUNT(*)+1 FROM eligible WHERE institution_id=NULLIF($2,'')::uuid AND total_points>(SELECT total_points FROM me)),
			(SELECT COUNT(*) FROM eligible WHERE institution_id=NULLIF($2,'')::uuid),
			(SELECT COUNT(*)+1 FROM eligible WHERE LOWER(domain)=LOWER((SELECT domain FROM me)) AND total_points>(SELECT total_points FROM me)),
			(SELECT COUNT(*) FROM eligible WHERE LOWER(domain)=LOWER((SELECT domain FROM me)))`,
		userID, instID,
	).Scan(&domain, &role, &ri.DistinctQuizzesCompleted, &ri.GlobalRank, &ri.GlobalTotal, &ir, &it, &dr, &dt)
	if err != nil {
		return nil, err
	}
	ri.LeaderboardUnlocked = role == "student" && ri.DistinctQuizzesCompleted >= 5
	if !ri.LeaderboardUnlocked {
		ri.GlobalRank = 0
		ri.GlobalTotal = 0
		return ri, nil
	}

	if ri.GlobalTotal > 0 {
		above := float64(ri.GlobalRank - 1)
		ri.TopPercentile = above / float64(ri.GlobalTotal) * 100
	}
	if instID != "" {
		ri.InstRank = &ir
		ri.InstTotal = &it
	}
	if domain != nil && *domain != "" {
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
	Progress    float64 `json:"progress"` // 0.0–1.0
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
	ID                   string     `json:"id"`
	Title                string     `json:"title"`
	Description          *string    `json:"description,omitempty"`
	QuestionCount        int        `json:"question_count"`
	Type                 string     `json:"type"`
	Domain               *string    `json:"domain,omitempty"`
	Subdomain            *string    `json:"subdomain,omitempty"`
	PublishedAt          *time.Time `json:"published_at,omitempty"`
	RecommendationReason string     `json:"recommendation_reason"`
	RecommendationScore  float64    `json:"-"`
	interestMatch        bool
	weaknessScore        float64
	difficultyScore      float64
	saved                bool
}

type LearningPreferences struct {
	Language string   `json:"language"`
	Topics   []string `json:"topics"`
}

func (s *Service) GetLearningPreferences(ctx context.Context, userID string) (*LearningPreferences, error) {
	p := &LearningPreferences{}
	err := s.db.QueryRow(ctx,
		`SELECT preferred_language, COALESCE(interest_domains, '{}') FROM users WHERE id=$1`,
		userID).Scan(&p.Language, &p.Topics)
	return p, err
}

func (s *Service) UpdateLearningPreferences(ctx context.Context, userID, language string, topics []string) (*LearningPreferences, error) {
	if language != "en" && language != "hi" && language != "mr" {
		return nil, ErrInvalidLearningLanguage
	}
	if len(topics) < MinLearningTopics || len(topics) > 50 {
		return nil, ErrInvalidLearningTopics
	}
	seen := make(map[string]struct{}, len(topics))
	clean := make([]string, 0, len(topics))
	for _, topic := range topics {
		if topic == "" {
			return nil, ErrInvalidLearningTopics
		}
		if _, ok := seen[topic]; !ok {
			seen[topic] = struct{}{}
			clean = append(clean, topic)
		}
	}
	if len(clean) < MinLearningTopics {
		return nil, ErrInvalidLearningTopics
	}
	var valid int
	if err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM subdomains WHERE slug = ANY($1::text[])`, clean,
	).Scan(&valid); err != nil {
		return nil, err
	}
	if valid != len(clean) {
		return nil, ErrInvalidLearningTopics
	}
	if _, err := s.db.Exec(ctx,
		`UPDATE users SET preferred_language=$2, interest_domains=$3, updated_at=now() WHERE id=$1`,
		userID, language, clean); err != nil {
		return nil, err
	}
	return &LearningPreferences{Language: language, Topics: clean}, nil
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
	WeekStart        time.Time `json:"week_start"`
	WeekEnd          time.Time `json:"week_end"`
	PointsThisWeek   int64     `json:"points_this_week"`
	PointsLastWeek   int64     `json:"points_last_week"`
	PointsDeltaPct   float64   `json:"points_delta_pct"`
	QuizzesThisWeek  int       `json:"quizzes_this_week"`
	AvgScoreThisWeek float64   `json:"avg_score_this_week"`
	CurrentStreak    int       `json:"current_streak"`
	Domain           *string   `json:"domain,omitempty"`
	DomainRank       *int      `json:"domain_rank,omitempty"`
	Suggestion       string    `json:"suggestion"`
}

// GetWeeklyInsights computes the user's last-7-days performance breakdown plus a
// week-over-week comparison and a coaching suggestion.
func (s *Service) GetWeeklyInsights(ctx context.Context, userID, instID string) (*WeeklyInsights, error) {
	now := time.Now().UTC()
	weekStart := now.AddDate(0, 0, -7)
	prevStart := now.AddDate(0, 0, -14)

	wi := &WeeklyInsights{WeekStart: weekStart, WeekEnd: now}

	// All five weekly figures — ledger points (this/last week), quizzes + average
	// this week, streak/domain, and domain rank — in a single round-trip. A CTE
	// binds the user's row once so the domain-rank subquery can reference it.
	var domain *string
	var domainRank int
	s.db.QueryRow(ctx, `
		WITH me AS (SELECT current_streak, domain, total_points FROM users WHERE id=$1)
		SELECT
			(SELECT COALESCE(SUM(amount) FILTER (WHERE created_at >= $2 AND amount > 0),0) FROM points_ledger WHERE user_id=$1),
			(SELECT COALESCE(SUM(amount) FILTER (WHERE created_at >= $3 AND created_at < $2 AND amount > 0),0) FROM points_ledger WHERE user_id=$1),
			(SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed' AND completed_at >= $2),
			(SELECT COALESCE(AVG(score_pct),0) FROM quiz_attempts WHERE user_id=$1 AND status='completed' AND completed_at >= $2),
			(SELECT current_streak FROM me),
			(SELECT domain FROM me),
			(SELECT COUNT(*)+1 FROM users WHERE domain=(SELECT domain FROM me) AND status='active' AND total_points > (SELECT total_points FROM me))`,
		userID, weekStart, prevStart,
	).Scan(&wi.PointsThisWeek, &wi.PointsLastWeek, &wi.QuizzesThisWeek, &wi.AvgScoreThisWeek,
		&wi.CurrentStreak, &domain, &domainRank)

	if wi.PointsLastWeek > 0 {
		wi.PointsDeltaPct = float64(wi.PointsThisWeek-wi.PointsLastWeek) / float64(wi.PointsLastWeek) * 100
	} else if wi.PointsThisWeek > 0 {
		wi.PointsDeltaPct = 100
	}
	if domain != nil && *domain != "" {
		wi.Domain = domain
		wi.DomainRank = &domainRank
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
	QwishScore float64         `json:"qwish_score"` // weighted components scaled to 100–900
	Components ScoreComponents `json:"components"`
	Domains    []DomainPerf    `json:"domains"`
}

const lowSampleQuestions = 10

func (s *Service) GetInsightsBreakdown(ctx context.Context, userID string) (*InsightsBreakdown, error) {
	var c ScoreComponents

	// All five score inputs in one round-trip. The difficulty/speed aggregate is
	// an expensive join, so it's bound once in a CTE and its three outputs read
	// back as scalars; the cheap counts sit alongside as independent subqueries.
	//   Accuracy (50%): question-weighted across completed attempts.
	//   Difficulty (20%): correct difficulty over all answered difficulty.
	//   Speed (10%): mean per-response speed factor over correct answers.
	//   Consistency (15%) from streak, Activity (5%) from completed count.
	var totalCorrect, totalQuestions int64
	var totalDiff, correctDiff, speedAvg float64
	var streak, completed int
	if err := s.db.QueryRow(ctx, `
		WITH diff AS (
			SELECT
			  COALESCE(SUM(q.difficulty),0) AS total_diff,
			  COALESCE(SUM(q.difficulty) FILTER (WHERE qr.is_correct),0) AS correct_diff,
			  COALESCE(AVG(CASE
			      WHEN qr.time_taken_ms < 1000 THEN 0.1
			      WHEN qr.time_taken_ms <= (q.time_limit_seconds*1000)/3.0 THEN 1.0
			      ELSE GREATEST(
			        (q.time_limit_seconds*1000.0 - qr.time_taken_ms)
			        / NULLIF(q.time_limit_seconds*1000.0 - q.time_limit_seconds*1000.0/3.0, 0), 0.1)
			    END) FILTER (WHERE qr.is_correct AND qr.time_taken_ms IS NOT NULL), 0) AS speed_avg
			FROM question_responses qr
			JOIN questions q ON q.id = qr.question_id
			JOIN quiz_attempts a ON a.id = qr.attempt_id
			WHERE a.user_id=$1 AND a.status='completed'
		)
		SELECT
			(SELECT COALESCE(SUM(total_correct),0) FROM quiz_attempts WHERE user_id=$1 AND status='completed'),
			(SELECT COALESCE(SUM(total_questions),0) FROM quiz_attempts WHERE user_id=$1 AND status='completed'),
			(SELECT total_diff FROM diff),
			(SELECT correct_diff FROM diff),
			(SELECT speed_avg FROM diff),
			COALESCE((SELECT current_streak FROM users WHERE id=$1), 0),
			(SELECT COUNT(*) FROM quiz_attempts WHERE user_id=$1 AND status='completed')`,
		userID,
	).Scan(&totalCorrect, &totalQuestions, &totalDiff, &correctDiff, &speedAvg, &streak, &completed); err != nil {
		return nil, err
	}

	scoreParts := scoring.CalculateQwishScoreComponents(scoring.QwishScoreFactors{
		TotalCorrect:      int(totalCorrect),
		TotalQuestions:    int(totalQuestions),
		Streak:            streak,
		ActivityCount:     completed,
		SpeedSum:          speedAvg * float64(totalCorrect),
		TotalDifficulty:   totalDiff,
		CorrectDifficulty: correctDiff,
	})
	c = ScoreComponents{
		Accuracy:    scoreParts.Accuracy,
		Difficulty:  scoreParts.Difficulty,
		Consistency: scoreParts.Consistency,
		Speed:       scoreParts.Speed,
		Activity:    scoreParts.Activity,
	}

	qwishScore := scaleQwish(scoring.CalculateQwishScore(scoring.QwishScoreFactors{
		TotalCorrect:      int(totalCorrect),
		TotalQuestions:    int(totalQuestions),
		Streak:            streak,
		ActivityCount:     completed,
		SpeedSum:          speedAvg * float64(totalCorrect),
		TotalDifficulty:   totalDiff,
		CorrectDifficulty: correctDiff,
	}))

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
	Value float64 `json:"value"` // avg score_pct scaled to 100–900, carried forward
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
			last = *avg
		}
		out = append(out, TrendPoint{Label: trendLabel(b, unit), Value: round1(scaleQwish(last))})
	}
	return out, nil
}

func trendLabel(b time.Time, unit string) string {
	if unit == "month" {
		return b.Format("Jan")
	}
	return b.Format("1/2") // month/day of the week bucket
}

func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}

// Qwish Score display range. The weighted formula yields 0–100; every user
// starts at qwishScoreMin and tops out at qwishScoreMax.
const (
	qwishScoreMin = 100.0
	qwishScoreMax = 900.0
)

// scaleQwish maps a 0–100 weighted score onto the [100, 900] display range.
func scaleQwish(pct float64) float64 {
	s := qwishScoreMin + pct/100*(qwishScoreMax-qwishScoreMin)
	if s < qwishScoreMin {
		return qwishScoreMin
	}
	if s > qwishScoreMax {
		return qwishScoreMax
	}
	return s
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
		`WITH learner AS (
		   SELECT COALESCE(interest_domains, '{}') AS interests
		   FROM users WHERE id = $1
		 ), ability AS (
		   SELECT COALESCE(AVG(score_pct), 60)::float8 AS avg_score
		   FROM quiz_attempts
		   WHERE user_id = $1 AND status = 'completed'
		 ), subdomain_performance AS (
		   SELECT q.subdomain, AVG(qa.score_pct)::float8 AS avg_score
		   FROM quiz_attempts qa JOIN quizzes q ON q.id = qa.quiz_id
		   WHERE qa.user_id = $1 AND qa.status = 'completed' AND q.subdomain IS NOT NULL
		   GROUP BY q.subdomain
		 ), recent_domains AS (
		   SELECT q.domain, COUNT(*)::int AS uses
		   FROM (
		     SELECT quiz_id FROM quiz_attempts
		     WHERE user_id = $1 AND status = 'completed'
		     ORDER BY completed_at DESC LIMIT 3
		   ) recent JOIN quizzes q ON q.id = recent.quiz_id
		   WHERE q.domain IS NOT NULL GROUP BY q.domain
		 ), bandit_total AS (
		   SELECT COALESCE(SUM(impressions),0)::float8 AS total
		   FROM recommendation_bandit_stats WHERE user_id=$1
		 ), candidates AS (
		   SELECT q.id, q.title, q.description,
		          LEAST(q.question_count, COALESCE(q.question_limit, q.question_count)) AS question_count, q.type,
		          q.domain, q.subdomain, q.published_at,
		          COALESCE(qd.difficulty, 0.60) AS difficulty,
		          COALESCE(pop.completions, 0) AS completions,
		          (q.subdomain = ANY(l.interests) OR q.domain = ANY(l.interests)) AS interest_match,
		          CASE WHEN sp.avg_score IS NULL THEN 0 ELSE (100 - sp.avg_score) / 100 * 25 END AS weakness_score,
		          GREATEST(0, 20 * (1 - ABS((0.40 + 0.60 * a.avg_score / 100) - COALESCE(qd.difficulty, 0.60)) / 0.60)) AS difficulty_score,
		          EXISTS(SELECT 1 FROM saved_quizzes sq WHERE sq.quiz_id=q.id AND sq.user_id=$1) AS saved,
		          COALESCE(rd.uses, 0) AS recent_domain_uses,
		          COALESCE(hist.attempts, 0) AS prior_attempts,
		          COALESCE(bs.impressions, 0)::float8 AS bandit_impressions,
		          COALESCE(bs.rewards, 0)::float8 AS bandit_rewards,
		          bt.total AS bandit_total,
		          CASE WHEN lm.next_review_at <= now()
		               THEN 15*(1-lm.mastery) ELSE 0 END AS review_score
		   FROM quizzes q
		   CROSS JOIN learner l CROSS JOIN ability a CROSS JOIN bandit_total bt
		   LEFT JOIN subdomain_performance sp ON sp.subdomain = q.subdomain
		   LEFT JOIN recent_domains rd ON rd.domain = q.domain
		   LEFT JOIN LATERAL (SELECT AVG(difficulty)::float8 AS difficulty FROM questions WHERE quiz_id=q.id) qd ON true
		   LEFT JOIN LATERAL (SELECT COUNT(DISTINCT user_id)::int AS completions FROM quiz_attempts WHERE quiz_id=q.id AND status='completed') pop ON true
		   LEFT JOIN LATERAL (SELECT COUNT(*)::int AS attempts FROM quiz_attempts WHERE quiz_id=q.id AND user_id=$1 AND status='completed') hist ON true
		   LEFT JOIN recommendation_bandit_stats bs ON bs.user_id=$1 AND bs.quiz_id=q.id
		   LEFT JOIN learner_topic_mastery lm ON lm.user_id=$1
		     AND lm.topic=COALESCE(NULLIF(q.subdomain,''), NULLIF(q.domain,''))
		   WHERE (q.visibility='public' OR q.institution_id = NULLIF($2, '')::uuid)
		     AND q.status='published' AND q.deleted_at IS NULL
		     AND (q.starts_at IS NULL OR q.starts_at <= now())
		     AND (q.ends_at IS NULL OR q.ends_at > now())
		     AND NOT EXISTS (
		       SELECT 1 FROM quiz_attempts played
		       WHERE played.user_id=$1 AND played.quiz_id=q.id
		         AND played.status='completed'
		     )
		 )
		 SELECT id, title, description, question_count, type, domain, subdomain, published_at,
		        interest_match, weakness_score, difficulty_score, saved,
		        (CASE WHEN interest_match THEN 30 ELSE 0 END
		         + weakness_score + difficulty_score
		         + LEAST(10, LN(1 + completions) * 2)
		         + GREATEST(0, 10 - EXTRACT(EPOCH FROM (now() - COALESCE(published_at, now() - interval '365 days'))) / 86400 / 9)
		         + CASE WHEN saved THEN 5 ELSE 0 END
		         - recent_domain_uses * 4 - LEAST(12, prior_attempts * 4) + review_score
		         + CASE WHEN bandit_impressions=0 THEN 12
		                ELSE 8*(bandit_rewards/bandit_impressions)
		                  + SQRT(2*LN(1+bandit_total)/bandit_impressions) END)::float8 AS rank_score
		 FROM candidates
		 ORDER BY rank_score DESC, published_at DESC, id
		 LIMIT 8`,
		userID, instID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []RecommendedQuiz
	for rows.Next() {
		var q RecommendedQuiz
		if err := rows.Scan(&q.ID, &q.Title, &q.Description, &q.QuestionCount, &q.Type,
			&q.Domain, &q.Subdomain, &q.PublishedAt, &q.interestMatch, &q.weaknessScore,
			&q.difficultyScore, &q.saved, &q.RecommendationScore); err != nil {
			return nil, err
		}
		q.RecommendationScore = round1(q.RecommendationScore)
		q.RecommendationReason = recommendationReason(q)
		list = append(list, q)
	}
	if list == nil {
		return s.GetFeaturedQuizzes(ctx)
	}
	ids := make([]string, len(list))
	for i := range list {
		ids[i] = list[i].ID
	}
	_, _ = s.db.Exec(ctx, `
		INSERT INTO recommendation_bandit_stats (user_id, quiz_id, impressions)
		SELECT $1, id, 1 FROM unnest($2::uuid[]) id
		ON CONFLICT (user_id, quiz_id) DO UPDATE SET
		  impressions=recommendation_bandit_stats.impressions+1, updated_at=now()`, userID, ids)
	return list, nil
}

// PickQuiz chooses one currently available assessment the learner has never
// attempted. Selected interests define the pool; random ordering only happens
// inside that server-authorized candidate set. excludeID lets the
// detail screen request a different choice without trusting the client to
// supply user history or interests.
func (s *Service) PickQuiz(ctx context.Context, userID, instID, excludeID string) (*RecommendedQuiz, error) {
	var interestCount int
	if err := s.db.QueryRow(ctx,
		`SELECT cardinality(ARRAY(
		   SELECT DISTINCT unnest(COALESCE(interest_domains, '{}'::text[]))
		 )) FROM users WHERE id=$1`,
		userID,
	).Scan(&interestCount); err != nil {
		return nil, err
	}
	if interestCount < MinLearningTopics {
		return nil, ErrInterestsRequired
	}

	q := &RecommendedQuiz{}
	var interestMatch bool
	err := s.db.QueryRow(ctx, `
		WITH learner AS (
		  SELECT COALESCE(interest_domains, '{}') AS interests
		    FROM users WHERE id=$1
		), candidates AS (
		  SELECT q.id, q.title, q.description,
		         LEAST(q.question_count, COALESCE(q.question_limit, q.question_count)) AS question_count,
		         q.type, q.domain, q.subdomain, q.published_at,
		         COALESCE(q.domain = ANY(l.interests) OR q.subdomain = ANY(l.interests), false) AS interest_match
		    FROM quizzes q CROSS JOIN learner l
		   WHERE (q.visibility='public' OR q.institution_id = NULLIF($2, '')::uuid)
		     AND q.status='published' AND q.deleted_at IS NULL
		     AND (q.starts_at IS NULL OR q.starts_at <= now())
		     AND (q.ends_at IS NULL OR q.ends_at > now())
		     AND COALESCE(q.domain = ANY(l.interests) OR q.subdomain = ANY(l.interests), false)
		     AND ($3='' OR q.id <> $3::uuid)
		     AND NOT EXISTS (
		       SELECT 1 FROM quiz_attempts played
		        WHERE played.user_id=$1 AND played.quiz_id=q.id
		     )
		)
		SELECT id, title, description, question_count, type, domain, subdomain,
		       published_at, interest_match
		  FROM candidates
		 ORDER BY random()
		 LIMIT 1`, userID, instID, excludeID).Scan(
		&q.ID, &q.Title, &q.Description, &q.QuestionCount, &q.Type,
		&q.Domain, &q.Subdomain, &q.PublishedAt, &interestMatch,
	)
	if err != nil {
		return nil, err
	}
	if interestMatch {
		q.RecommendationReason = "Matches your interests"
	} else {
		q.RecommendationReason = "An unplayed assessment for you"
	}
	return q, nil
}

func recommendationReason(q RecommendedQuiz) string {
	switch {
	case q.interestMatch:
		return "Matches your interests"
	case q.weaknessScore >= 10:
		return "Build strength in this topic"
	case q.saved:
		return "From your saved assessments"
	case q.difficultyScore >= 17:
		return "A good match for your level"
	default:
		return "Popular with Qwish learners"
	}
}
