package leaderboard

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	qdb "github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/middleware"
	"golang.org/x/sync/singleflight"
)

const quizzesRequiredToUnlock = 5

// pageTTL bounds how stale a shared leaderboard page can be.
const pageTTL = 30 * time.Second

type Handler struct {
	db        *pgxpool.Pool
	mu        sync.Mutex
	snapshots map[string]*rankSnapshot
	flight    singleflight.Group
	epoch     uint64
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db, snapshots: make(map[string]*rankSnapshot)}
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
	// Bound page arithmetic before multiplying untrusted input.
	cutoff := rankCutoff(scope)
	if page > (cutoff+limit-1)/limit {
		page = (cutoff+limit-1)/limit + 1
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

	domain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain")))
	var myRank int
	var myTopPercent *int
	var myPoints int64
	var myQwishScore float64
	var myInstitutionName *string
	var matches bool
	if role == "student" {
		var completed int
		// Eligibility, account status and scope membership remain live. No population
		// scan is involved: this lookup uses the caller's primary key.
		err := h.db.QueryRow(r.Context(), `SELECT COALESCE(ls.completed_quizzes,0),
   COALESCE(ls.qwish_score,100), u.total_points, i.name,
   u.status='active' AND u.deleted_at IS NULL AND u.role='student'
   AND ($2='' OR COALESCE(lower(u.domain)=$2,false))
   AND ($3='global' OR `+qdb.LiveMemberSQL("u.id", "$4")+`)
   FROM users u LEFT JOIN leaderboard_scores ls ON ls.user_id=u.id
   LEFT JOIN institutions i ON i.id=u.institution_id WHERE u.id=$1`,
			userID, domain, scope, nullableInstitution(instID)).Scan(&completed, &myQwishScore, &myPoints, &myInstitutionName, &matches)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		if completed < quizzesRequiredToUnlock {
			middleware.Error(w, http.StatusForbidden, "LEADERBOARD_LOCKED", "complete 5 different quizzes to unlock the leaderboard")
			return
		}
	}
	snapshot, err := h.snapshot(r.Context(), scope, instID, domain)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	entries := snapshot.page(limit, offset)
	if role == "student" && matches {
		myRank, myTopPercent = snapshot.position(myQwishScore, cutoff)
	}

	middleware.JSONWithMeta(w, http.StatusOK, map[string]interface{}{
		"scope":               scope,
		"domain":              domain,
		"my_rank":             myRank,
		"my_top_percent":      myTopPercent,
		"rank_cutoff":         cutoff,
		"ranking_updated_at":  snapshot.updatedAt,
		"my_qwish_score":      myQwishScore,
		"my_institution_name": myInstitutionName,
		"my_points":           myPoints,
		"entries":             entries,
	}, &middleware.Meta{Page: page, Limit: limit, Total: snapshot.total})
}

// ClearCache is reserved for membership/institution changes and listener reconnects.
// Score changes expire through the 30-second TTL instead of flushing every scope.
func (h *Handler) ClearCache() {
	h.mu.Lock()
	h.epoch++
	h.snapshots = make(map[string]*rankSnapshot)
	h.mu.Unlock()
}

// Retained for callers/tests that load an uncached page.
func (h *Handler) loadPage(ctx context.Context, scope, instID, domain string, limit, offset int) ([]Entry, int, error) {
	s, err := h.loadSnapshot(ctx, scope, instID, strings.ToLower(domain))
	if err != nil {
		return nil, 0, err
	}
	return s.page(limit, offset), s.total, nil
}
