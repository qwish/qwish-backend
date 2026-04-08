package points

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler {
	return &Handler{db: db}
}

// GET /api/v1/users/me/points
func (h *Handler) GetBalance(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	var total int64
	h.db.QueryRow(r.Context(), `SELECT total_points FROM users WHERE id=$1`, userID).Scan(&total)

	// Expiring within 30 days
	soon := time.Now().AddDate(0, 0, 30)
	var expiringAmount int64
	var expiresAt *time.Time
	h.db.QueryRow(r.Context(),
		`SELECT COALESCE(SUM(amount),0), MIN(expires_at) FROM points_ledger
		 WHERE user_id=$1 AND expires_at IS NOT NULL AND expires_at <= $2 AND amount > 0`,
		userID, soon,
	).Scan(&expiringAmount, &expiresAt)

	resp := map[string]interface{}{
		"total_points": total,
	}
	if expiringAmount > 0 {
		resp["expiring_soon"] = map[string]interface{}{
			"amount":     expiringAmount,
			"expires_at": expiresAt,
		}
	} else {
		resp["expiring_soon"] = nil
	}
	middleware.JSON(w, http.StatusOK, resp)
}

// GET /api/v1/users/me/points/ledger
func (h *Handler) GetLedger(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	offset := (page - 1) * limit

	var total int
	h.db.QueryRow(r.Context(), `SELECT COUNT(*) FROM points_ledger WHERE user_id=$1`, userID).Scan(&total)

	rows, err := h.db.Query(r.Context(),
		`SELECT id, amount, reason, reference_id, balance_after, expires_at, created_at
		 FROM points_ledger WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type Entry struct {
		ID          string     `json:"id"`
		Amount      int64      `json:"amount"`
		Reason      string     `json:"reason"`
		ReferenceID *string    `json:"reference_id,omitempty"`
		BalanceAfter int64     `json:"balance_after"`
		ExpiresAt   *time.Time `json:"expires_at,omitempty"`
		CreatedAt   time.Time  `json:"created_at"`
	}

	var entries []Entry
	for rows.Next() {
		var e Entry
		rows.Scan(&e.ID, &e.Amount, &e.Reason, &e.ReferenceID, &e.BalanceAfter, &e.ExpiresAt, &e.CreatedAt)
		entries = append(entries, e)
	}
	if entries == nil {
		entries = []Entry{}
	}
	middleware.JSONWithMeta(w, http.StatusOK, entries, &middleware.Meta{Page: page, Limit: limit, Total: total})
}

// GET /api/v1/users/me/streak  — handled by streak domain handler
