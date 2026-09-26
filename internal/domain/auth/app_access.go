package auth

import (
	"context"
	"log"
	"net/http"

	"github.com/qwish/backend/internal/middleware"
)

type AppLoginNoticeSender interface {
	SendAppLoginDenied(context.Context, string) error
}

// Called only after OTP/passkey verification, never from an email lookup.
func (h *Handler) rejectAppLogin(w http.ResponseWriter, r *http.Request, role, email string) bool {
	if r.Header.Get("X-Qwish-Client") != "numpie" || role == "student" || role == "parent" {
		return false
	}
	if sender, ok := h.welcomeSender.(AppLoginNoticeSender); ok {
		if err := sender.SendAppLoginDenied(r.Context(), email); err != nil {
			log.Printf("auth: app-login notice delivery failed: %v", err)
		}
	}
	middleware.Error(w, http.StatusForbidden, "APP_LOGIN_DENIED", "Login failed")
	return true
}

// Refresh has already authenticated the subject. Do not send another email on
// automatic renewal; login ceremonies send the explanatory notice.
func (h *Handler) rejectAppRefresh(w http.ResponseWriter, r *http.Request, uid string) bool {
	if r.Header.Get("X-Qwish-Client") != "numpie" {
		return false
	}
	var allowed bool
	err := h.svc.db.QueryRow(r.Context(), `SELECT NOT EXISTS(
 SELECT 1 FROM users WHERE supabase_uid=$1 AND deleted_at IS NULL AND role NOT IN ('student','parent')
 UNION ALL SELECT 1 FROM admin_accounts WHERE supabase_uid=$1 AND deleted_at IS NULL)`, uid).Scan(&allowed)
	if err != nil {
		middleware.InternalError(w)
		return true
	}
	if !allowed {
		middleware.Error(w, http.StatusForbidden, "APP_LOGIN_DENIED", "Login failed")
		return true
	}
	return false
}
