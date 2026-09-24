package middleware

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TrackAdminSessions records each admin console session (keyed by the token's
// session_id claim) and refuses a session an admin has revoked. It runs after
// Authenticate, so the token is already verified; the payload is only decoded
// here to read claims Authenticate doesn't keep.
//
// Tokens without a session_id (older passkey tokens) pass through untracked.
func TrackAdminSessions(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			adminID := GetAdminID(r)
			sid, method := sessionClaims(r)
			if adminID == "" || sid == "" {
				next.ServeHTTP(w, r)
				return
			}
			var revoked bool
			_ = db.QueryRow(r.Context(),
				`SELECT revoked_at IS NOT NULL FROM admin_sessions WHERE session_id = $1`, sid).Scan(&revoked)
			if revoked {
				Error(w, http.StatusUnauthorized, "SESSION_REVOKED", "this session was signed out")
				return
			}
			ip, ua := clientIP(r), r.UserAgent()
			// Best-effort and throttled to one write a minute per session: the
			// list only needs "last active", not every request.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				db.Exec(ctx, `
					INSERT INTO admin_sessions (session_id, admin_id, user_agent, ip, method)
					VALUES ($1, $2, $3, $4, $5)
					ON CONFLICT (session_id) DO UPDATE
					   SET last_seen = now(), ip = EXCLUDED.ip, user_agent = EXCLUDED.user_agent
					 WHERE admin_sessions.last_seen < now() - interval '1 minute'`,
					sid, adminID, truncate(ua, 300), ip, method)
			}()
			ctx := context.WithValue(r.Context(), ContextKeySessionID, sid)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

const ContextKeySessionID contextKey = "session_id"

// GetSessionID returns the current admin session id, when tracked.
func GetSessionID(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeySessionID).(string)
	return v
}

func sessionClaims(r *http.Request) (sid, method string) {
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var c struct {
		SessionID string `json:"session_id"`
		AMR       []struct {
			Method string `json:"method"`
		} `json:"amr"`
	}
	if json.Unmarshal(payload, &c) != nil {
		return "", ""
	}
	if len(c.AMR) > 0 {
		method = c.AMR[0].Method
	}
	return c.SessionID, method
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
