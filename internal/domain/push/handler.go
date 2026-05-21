package push

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

type registerReq struct {
	Token      string `json:"token"`
	Platform   string `json:"platform"`
	AppVersion string `json:"app_version"`
	Locale     string `json:"locale"`
}

// POST /api/v1/users/me/devices — register or refresh an FCM token.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Token == "" {
		middleware.BadRequest(w, "token is required")
		return
	}
	switch req.Platform {
	case "ios", "android", "web":
	default:
		req.Platform = "unknown"
	}

	userID := middleware.GetUserID(r)
	_, err := h.db.Exec(r.Context(),
		`INSERT INTO device_tokens (user_id, token, platform, app_version, locale, last_seen)
		 VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''), now())
		 ON CONFLICT (user_id, token) DO UPDATE
		   SET platform    = EXCLUDED.platform,
		       app_version = EXCLUDED.app_version,
		       locale      = EXCLUDED.locale,
		       last_seen   = now()`,
		userID, req.Token, req.Platform, req.AppVersion, req.Locale)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/v1/users/me/devices/{token} — unregister on logout / uninstall.
func (h *Handler) Unregister(w http.ResponseWriter, r *http.Request) {
	userID := middleware.GetUserID(r)
	token := chi.URLParam(r, "token")
	if token == "" {
		middleware.BadRequest(w, "token is required")
		return
	}
	if _, err := h.db.Exec(r.Context(),
		`DELETE FROM device_tokens WHERE user_id=$1 AND token=$2`, userID, token,
	); err != nil {
		middleware.InternalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
