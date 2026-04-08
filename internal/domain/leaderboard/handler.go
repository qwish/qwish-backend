package leaderboard

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

type Entry struct {
	Rank          int    `json:"rank"`
	UserID        string `json:"user_id"`
	DisplayName   string `json:"display_name"`
	TotalPoints   int64  `json:"total_points"`
	CurrentStreak int    `json:"current_streak"`
}

// GET /api/v1/leaderboard?scope=institution|global
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "institution"
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

	var total int
	var entries []Entry

	if scope == "institution" {
		h.db.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM users WHERE institution_id=$1 AND status='active' AND role IN ('student','teacher')`, instID,
		).Scan(&total)

		rows, err := h.db.Query(r.Context(),
			`SELECT id, display_name, total_points, current_streak,
			        RANK() OVER (ORDER BY total_points DESC) as rank
			 FROM users WHERE institution_id=$1 AND status='active' AND role IN ('student','teacher')
			 ORDER BY total_points DESC LIMIT $2 OFFSET $3`,
			instID, limit, offset)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var e Entry
				rows.Scan(&e.UserID, &e.DisplayName, &e.TotalPoints, &e.CurrentStreak, &e.Rank)
				entries = append(entries, e)
			}
		}
	} else {
		h.db.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM users WHERE status='active' AND role IN ('student','teacher')`,
		).Scan(&total)

		rows, err := h.db.Query(r.Context(),
			`SELECT id, display_name, total_points, current_streak,
			        RANK() OVER (ORDER BY total_points DESC) as rank
			 FROM users WHERE status='active' AND role IN ('student','teacher')
			 ORDER BY total_points DESC LIMIT $1 OFFSET $2`,
			limit, offset)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var e Entry
				rows.Scan(&e.UserID, &e.DisplayName, &e.TotalPoints, &e.CurrentStreak, &e.Rank)
				entries = append(entries, e)
			}
		}
	}

	if entries == nil {
		entries = []Entry{}
	}

	// Get current user's rank
	var myRank int
	var myPoints int64
	if scope == "institution" {
		h.db.QueryRow(r.Context(),
			`SELECT COUNT(*)+1 FROM users WHERE institution_id=$1 AND total_points > (SELECT total_points FROM users WHERE id=$2) AND status='active'`,
			instID, userID,
		).Scan(&myRank)
	} else {
		h.db.QueryRow(r.Context(),
			`SELECT COUNT(*)+1 FROM users WHERE total_points > (SELECT total_points FROM users WHERE id=$1) AND status='active'`,
			userID,
		).Scan(&myRank)
	}
	h.db.QueryRow(r.Context(), `SELECT total_points FROM users WHERE id=$1`, userID).Scan(&myPoints)

	middleware.JSONWithMeta(w, http.StatusOK, map[string]interface{}{
		"scope":     scope,
		"my_rank":   myRank,
		"my_points": myPoints,
		"entries":   entries,
	}, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

