package auth

import (
	"context"
	"net/http"
	"time"

	"github.com/qwish/backend/internal/middleware"
)

// recordSignIn notes a successful sign-in. Best effort: a failed write must
// never block the sign-in it describes.
func (s *Service) recordSignIn(ctx context.Context, userID, method string, r *http.Request) {
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	s.db.Exec(ctx, `INSERT INTO user_sign_ins (user_id, method, ip, user_agent) VALUES ($1,$2,$3,$4)`,
		userID, method, middleware.ClientIP(r), ua)
}

// GET /api/v1/users/me/sign-ins — the caller's last 20 sign-ins.
func (h *Handler) MySignIns(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.db.Query(r.Context(),
		`SELECT method, COALESCE(ip,''), COALESCE(user_agent,''), created_at FROM user_sign_ins
		  WHERE user_id=$1 ORDER BY created_at DESC LIMIT 20`, middleware.GetUserID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type signIn struct {
		Method    string    `json:"method"`
		IP        string    `json:"ip"`
		UserAgent string    `json:"user_agent"`
		At        time.Time `json:"at"`
	}
	out := []signIn{}
	for rows.Next() {
		var s signIn
		rows.Scan(&s.Method, &s.IP, &s.UserAgent, &s.At)
		out = append(out, s)
	}
	middleware.JSON(w, http.StatusOK, out)
}
