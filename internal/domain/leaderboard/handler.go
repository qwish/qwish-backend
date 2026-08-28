package leaderboard

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

const quizzesRequiredToUnlock = 5

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

type Entry struct {
	Rank            int     `json:"rank"`
	UserID          string  `json:"user_id"`
	DisplayName     string  `json:"display_name"`
	InstitutionName *string `json:"institution_name,omitempty"`
	QwishScore      float64 `json:"qwish_score"`
	TotalPoints     int64   `json:"total_points"`
	CurrentStreak   int     `json:"current_streak"`
}

// leaderboardScoreCTE is the server-authoritative score used for ranking.
// It mirrors the lifetime Insights formula: confidence-adjusted accuracy,
// difficulty, smooth consistency/activity, and response speed. Keeping the
// calculation in SQL means clients cannot submit or alter a ranking score.
const leaderboardScoreCTE = `WITH attempt_stats AS (
	SELECT user_id,
	       COALESCE(SUM(total_correct), 0)::float8 AS total_correct,
	       COALESCE(SUM(total_questions), 0)::float8 AS total_questions,
	       COUNT(*)::float8 AS completed
	  FROM quiz_attempts
	 WHERE status='completed'
	 GROUP BY user_id
), response_stats AS (
	SELECT a.user_id,
	       COALESCE(SUM(q.difficulty), 0)::float8 AS total_difficulty,
	       COALESCE(SUM(q.difficulty) FILTER (WHERE qr.is_correct), 0)::float8 AS correct_difficulty,
	       COALESCE(AVG(CASE
	         WHEN qr.time_taken_ms < 1000 THEN 0.1
	         WHEN qr.time_taken_ms <= (q.time_limit_seconds * 1000) / 3.0 THEN 1.0
	         ELSE GREATEST(
	           (q.time_limit_seconds * 1000.0 - qr.time_taken_ms) /
	           NULLIF(q.time_limit_seconds * 1000.0 - q.time_limit_seconds * 1000.0 / 3.0, 0), 0.1)
	       END) FILTER (WHERE qr.is_correct AND qr.time_taken_ms IS NOT NULL), 0)::float8 AS speed
	  FROM question_responses qr
	  JOIN questions q ON q.id=qr.question_id
	  JOIN quiz_attempts a ON a.id=qr.attempt_id AND a.status='completed'
	 GROUP BY a.user_id
), scored AS (
	SELECT u.id, u.display_name, i.name AS institution_name, u.institution_id,
	       COALESCE(u.total_points, 0) AS total_points,
	       COALESCE(u.current_streak, 0) AS current_streak,
	       (100 + 8 * (
	         CASE WHEN COALESCE(at.total_questions, 0) > 0 THEN
	           ((COALESCE(at.total_correct, 0) + 5.0) /
	            (COALESCE(at.total_questions, 0) + 10.0)) * 50.0
	         ELSE 0 END
	         + CASE WHEN COALESCE(rs.total_difficulty, 0) > 0 THEN
	           (rs.correct_difficulty / rs.total_difficulty) * 20.0
	           ELSE 0 END
	         + (1 - EXP(-COALESCE(u.current_streak, 0)::float8 / 14.0)) * 15.0
	         + COALESCE(rs.speed, 0) * 10.0
	         + (1 - EXP(-COALESCE(at.completed, 0) / 20.0)) * 5.0
	       ))::float8 AS qwish_score
	  FROM users u
	  LEFT JOIN institutions i ON i.id=u.institution_id
	  LEFT JOIN attempt_stats at ON at.user_id=u.id
	  LEFT JOIN response_stats rs ON rs.user_id=u.id
	 WHERE u.status='active' AND u.role='student'
)
`

// GET /api/v1/leaderboard?scope=institution|global&domain=<optional>
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "institution"
	}
	if scope != "institution" && scope != "global" {
		middleware.BadRequest(w, "scope must be institution or global")
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	userID := middleware.GetUserID(r)
	instID := middleware.GetInstitutionID(r)
	role := middleware.GetRole(r)
	if role == "super_admin" {
		if requested := r.URL.Query().Get("institution_id"); requested != "" {
			instID = requested
		}
	}
	if scope == "institution" && instID == "" {
		middleware.BadRequest(w, "institution_id is required for institution scope")
		return
	}

	// The mobile lock is presentation only; enforce the same eligibility at the
	// data boundary so a direct HTTP request cannot bypass it.
	if role == "student" {
		var completed int
		if err := h.db.QueryRow(r.Context(),
			`SELECT COUNT(DISTINCT quiz_id) FROM quiz_attempts WHERE user_id=$1 AND status='completed'`,
			userID,
		).Scan(&completed); err != nil {
			middleware.InternalError(w)
			return
		}
		if completed < quizzesRequiredToUnlock {
			middleware.Error(w, http.StatusForbidden, "LEADERBOARD_LOCKED", "complete 5 different quizzes to unlock the leaderboard")
			return
		}
	}

	domain := r.URL.Query().Get("domain")
	var total int
	var entries []Entry

	if scope == "institution" {
		if err := h.db.QueryRow(r.Context(), `
			`+leaderboardScoreCTE+`SELECT COUNT(*) FROM scored s
			 JOIN users u ON u.id=s.id
			 WHERE s.institution_id=$1
			   AND (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=s.id AND qa.status='completed') >= 5
			   AND ($2='' OR LOWER(u.domain)=LOWER($2))`, instID, domain).Scan(&total); err != nil {
			middleware.InternalError(w)
			return
		}

		rows, err := h.db.Query(r.Context(), leaderboardScoreCTE+`
			SELECT s.id, s.display_name, s.institution_name, s.qwish_score, s.total_points, s.current_streak,
			       RANK() OVER (ORDER BY s.qwish_score DESC) AS rank
			  FROM scored s JOIN users u ON u.id=s.id
			 WHERE s.institution_id=$1
			   AND (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=s.id AND qa.status='completed') >= 5
			   AND ($2='' OR LOWER(u.domain)=LOWER($2))
			 ORDER BY s.qwish_score DESC, s.id LIMIT $3 OFFSET $4`, instID, domain, limit, offset)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			if err := rows.Scan(&e.UserID, &e.DisplayName, &e.InstitutionName, &e.QwishScore, &e.TotalPoints, &e.CurrentStreak, &e.Rank); err != nil {
				middleware.InternalError(w)
				return
			}
			entries = append(entries, e)
		}
		if rows.Err() != nil {
			middleware.InternalError(w)
			return
		}
	} else {
		if err := h.db.QueryRow(r.Context(), leaderboardScoreCTE+`
			SELECT COUNT(*) FROM scored s JOIN users u ON u.id=s.id
			 WHERE (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=s.id AND qa.status='completed') >= 5
			   AND ($1='' OR LOWER(u.domain)=LOWER($1))`, domain).Scan(&total); err != nil {
			middleware.InternalError(w)
			return
		}

		rows, err := h.db.Query(r.Context(), leaderboardScoreCTE+`
			SELECT s.id, s.display_name, s.institution_name, s.qwish_score, s.total_points, s.current_streak,
			       RANK() OVER (ORDER BY s.qwish_score DESC) AS rank
			  FROM scored s JOIN users u ON u.id=s.id
			 WHERE (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=s.id AND qa.status='completed') >= 5
			   AND ($1='' OR LOWER(u.domain)=LOWER($1))
			 ORDER BY s.qwish_score DESC, s.id LIMIT $2 OFFSET $3`, domain, limit, offset)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			if err := rows.Scan(&e.UserID, &e.DisplayName, &e.InstitutionName, &e.QwishScore, &e.TotalPoints, &e.CurrentStreak, &e.Rank); err != nil {
				middleware.InternalError(w)
				return
			}
			entries = append(entries, e)
		}
		if rows.Err() != nil {
			middleware.InternalError(w)
			return
		}
	}
	if entries == nil {
		entries = []Entry{}
	}

	var myRank int
	var myPoints int64
	var myQwishScore float64
	var myInstitutionName *string
	if role == "student" {
		var err error
		if scope == "institution" {
			err = h.db.QueryRow(r.Context(), leaderboardScoreCTE+`
			SELECT CASE WHEN EXISTS(
				SELECT 1 FROM scored me JOIN users mu ON mu.id=me.id
				 WHERE me.id=$2 AND me.institution_id=$1 AND ($3='' OR LOWER(mu.domain)=LOWER($3))
			) THEN (
				SELECT COUNT(*)+1 FROM scored s JOIN users u ON u.id=s.id
				 WHERE s.institution_id=$1
				   AND (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=s.id AND qa.status='completed') >= 5
				   AND ($3='' OR LOWER(u.domain)=LOWER($3))
				   AND s.qwish_score>(SELECT qwish_score FROM scored WHERE id=$2)
			) ELSE 0 END,
			COALESCE((SELECT qwish_score FROM scored WHERE id=$2),100),
			COALESCE((SELECT total_points FROM scored WHERE id=$2),0),
			(SELECT institution_name FROM scored WHERE id=$2)`, instID, userID, domain).Scan(&myRank, &myQwishScore, &myPoints, &myInstitutionName)
		} else {
			err = h.db.QueryRow(r.Context(), leaderboardScoreCTE+`
			SELECT CASE WHEN EXISTS(
				SELECT 1 FROM scored me JOIN users mu ON mu.id=me.id
				 WHERE me.id=$1 AND ($2='' OR LOWER(mu.domain)=LOWER($2))
			) THEN (
				SELECT COUNT(*)+1 FROM scored s JOIN users u ON u.id=s.id
				 WHERE (SELECT COUNT(DISTINCT qa.quiz_id) FROM quiz_attempts qa WHERE qa.user_id=s.id AND qa.status='completed') >= 5
				   AND ($2='' OR LOWER(u.domain)=LOWER($2))
				   AND s.qwish_score>(SELECT qwish_score FROM scored WHERE id=$1)
			) ELSE 0 END,
			COALESCE((SELECT qwish_score FROM scored WHERE id=$1),100),
			COALESCE((SELECT total_points FROM scored WHERE id=$1),0),
			(SELECT institution_name FROM scored WHERE id=$1)`, userID, domain).Scan(&myRank, &myQwishScore, &myPoints, &myInstitutionName)
		}
		if err != nil {
			middleware.InternalError(w)
			return
		}
	}

	middleware.JSONWithMeta(w, http.StatusOK, map[string]interface{}{
		"scope":               scope,
		"domain":              domain,
		"my_rank":             myRank,
		"my_qwish_score":      myQwishScore,
		"my_institution_name": myInstitutionName,
		"my_points":           myPoints,
		"entries":             entries,
	}, &middleware.Meta{Page: page, Limit: limit, Total: total})
}
