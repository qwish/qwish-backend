package leaderboard

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	qdb "github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/middleware"
)

const quizzesRequiredToUnlock = 5

// pageTTL bounds how stale a shared leaderboard page can be.
const pageTTL = 30 * time.Second

type cachedPage struct {
	entries []Entry
	total   int
	expires time.Time
}

type Handler struct {
	db    *pgxpool.Pool
	mu    sync.Mutex
	cache map[string]cachedPage
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db, cache: map[string]cachedPage{}}
}

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
const leaderboardScoreCTE = `WITH scored AS (
	SELECT u.id, u.display_name, i.name AS institution_name, u.institution_id,
	       COALESCE(u.total_points, 0) AS total_points,
	       COALESCE(u.current_streak, 0) AS current_streak,
	       COALESCE(ls.qwish_score,100)::float8 AS qwish_score,
	       COALESCE(ls.completed_quizzes,0) AS completed_quizzes
	  FROM users u
	  LEFT JOIN institutions i ON i.id=u.institution_id
	  LEFT JOIN leaderboard_scores ls ON ls.user_id=u.id
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
			`SELECT COALESCE((SELECT completed_quizzes FROM leaderboard_scores WHERE user_id=$1),0)`,
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
	entries, total, err := h.page(r.Context(), scope, instID, domain, limit, offset)
	if err != nil {
		middleware.InternalError(w)
		return
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
				 WHERE me.id=$2 AND `+qdb.LiveMemberSQL("me.id", "$1")+` AND ($3='' OR LOWER(mu.domain)=LOWER($3))
			) THEN (
				SELECT COUNT(*)+1 FROM scored s JOIN users u ON u.id=s.id
				 WHERE `+qdb.LiveMemberSQL("s.id", "$1")+`
				   AND s.completed_quizzes >= 5
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
				 WHERE s.completed_quizzes >= 5
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

// page loads one ranked page plus the total. It is identical for every caller
// with the same key and each miss scans every active student twice, so it is
// shared through a short-lived cache; my_rank stays live per request.
func (h *Handler) page(ctx context.Context, scope, instID, domain string, limit, offset int) ([]Entry, int, error) {
	key := fmt.Sprintf("%s|%s|%s|%d|%d", scope, instID, strings.ToLower(domain), limit, offset)
	h.mu.Lock()
	if c, ok := h.cache[key]; ok && time.Now().Before(c.expires) {
		h.mu.Unlock()
		return c.entries, c.total, nil
	}
	h.mu.Unlock()

	entries, total, err := h.loadPage(ctx, scope, instID, domain, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	now := time.Now()
	h.mu.Lock()
	// ponytail: whole-map sweep on insert; keys are pages × institutions, small.
	for k, c := range h.cache {
		if now.After(c.expires) {
			delete(h.cache, k)
		}
	}
	h.cache[key] = cachedPage{entries, total, now.Add(pageTTL)}
	h.mu.Unlock()
	return entries, total, nil
}

func (h *Handler) loadPage(ctx context.Context, scope, instID, domain string, limit, offset int) ([]Entry, int, error) {
	total := 0
	entries := []Entry{}

	if scope == "institution" {
		if err := h.db.QueryRow(ctx, `
			`+leaderboardScoreCTE+`SELECT COUNT(*) FROM scored s
			 JOIN users u ON u.id=s.id
			 WHERE `+qdb.LiveMemberSQL("s.id", "$1")+`
			   AND s.completed_quizzes >= 5
			   AND ($2='' OR LOWER(u.domain)=LOWER($2))`, instID, domain).Scan(&total); err != nil {
			return nil, 0, err
		}

		rows, err := h.db.Query(ctx, leaderboardScoreCTE+`
			SELECT s.id, s.display_name, s.institution_name, s.qwish_score, s.total_points, s.current_streak,
			       RANK() OVER (ORDER BY s.qwish_score DESC) AS rank
			  FROM scored s JOIN users u ON u.id=s.id
			 WHERE `+qdb.LiveMemberSQL("s.id", "$1")+`
			   AND s.completed_quizzes >= 5
			   AND ($2='' OR LOWER(u.domain)=LOWER($2))
			 ORDER BY s.qwish_score DESC, s.id LIMIT $3 OFFSET $4`, instID, domain, limit, offset)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			if err := rows.Scan(&e.UserID, &e.DisplayName, &e.InstitutionName, &e.QwishScore, &e.TotalPoints, &e.CurrentStreak, &e.Rank); err != nil {
				return nil, 0, err
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
	} else {
		if err := h.db.QueryRow(ctx, leaderboardScoreCTE+`
			SELECT COUNT(*) FROM scored s JOIN users u ON u.id=s.id
			 WHERE s.completed_quizzes >= 5
			   AND ($1='' OR LOWER(u.domain)=LOWER($1))`, domain).Scan(&total); err != nil {
			return nil, 0, err
		}

		rows, err := h.db.Query(ctx, leaderboardScoreCTE+`
			SELECT s.id, s.display_name, s.institution_name, s.qwish_score, s.total_points, s.current_streak,
			       RANK() OVER (ORDER BY s.qwish_score DESC) AS rank
			  FROM scored s JOIN users u ON u.id=s.id
			 WHERE s.completed_quizzes >= 5
			   AND ($1='' OR LOWER(u.domain)=LOWER($1))
			 ORDER BY s.qwish_score DESC, s.id LIMIT $2 OFFSET $3`, domain, limit, offset)
		if err != nil {
			return nil, 0, err
		}
		defer rows.Close()
		for rows.Next() {
			var e Entry
			if err := rows.Scan(&e.UserID, &e.DisplayName, &e.InstitutionName, &e.QwishScore, &e.TotalPoints, &e.CurrentStreak, &e.Rank); err != nil {
				return nil, 0, err
			}
			entries = append(entries, e)
		}
		if err := rows.Err(); err != nil {
			return nil, 0, err
		}
	}
	return entries, total, nil
}
