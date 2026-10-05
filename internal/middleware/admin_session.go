package middleware

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TrackAdminSessions records authenticated admin sessions. Authenticate checks
// live revocation and policy before this observer runs, on every API surface.
func TrackAdminSessions(db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			adminID := GetAdminID(r)
			sid, method := sessionClaims(r)
			if adminID == "" {
				next.ServeHTTP(w, r)
				return
			}
			if sid == "" {
				Unauthorized(w)
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
			next.ServeHTTP(w, r)
		})
	}
}

const ContextKeySessionID contextKey = "session_id"

// GetSessionID returns the session ID from the verified access token.
func GetSessionID(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeySessionID).(string)
	return v
}

func sessionClaims(r *http.Request) (sid, method string) {
	sid = GetSessionID(r)
	method, _ = r.Context().Value(contextKeyAuthMethod).(string)
	return sid, method
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
