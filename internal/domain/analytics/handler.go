package analytics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/algorithms"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

// Trends returns bounded-memory approximations over recent completed attempts.
// GET /api/v1/admin/analytics/trends?hours=24
func (h *Handler) Trends(w http.ResponseWriter, r *http.Request) {
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours < 1 || hours > 24*90 {
		hours = 24
	}
	rows, err := h.db.Query(r.Context(), `SELECT qa.user_id::text, COALESCE(q.domain,'uncategorized')
		FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
		WHERE qa.status='completed' AND qa.completed_at >= $1`, time.Now().Add(-time.Duration(hours)*time.Hour))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	nodes := []string{"0", "1", "2", "3", "4", "5", "6", "7"}
	ring := algorithms.NewHashRing(nodes, 64)
	counts := make(map[string]*algorithms.CountMinSketch, len(nodes))
	for _, node := range nodes {
		counts[node] = algorithms.NewCountMinSketch(512, 5)
	}
	top := algorithms.NewSpaceSaving(20)
	uniques := algorithms.NewHyperLogLog(12)
	var events uint64
	for rows.Next() {
		var uid, domain string
		if rows.Scan(&uid, &domain) != nil {
			middleware.InternalError(w)
			return
		}
		events++
		counts[ring.Node(domain)].Add(domain, 1)
		top.Add(domain)
		uniques.Add(uid)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	items := top.Top()
	for i := range items {
		items[i].Count = counts[ring.Node(items[i].Key)].Estimate(items[i].Key)
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"window_hours": hours, "attempts": events, "estimated_unique_learners": uniques.Count(), "top_domains": items})
}
