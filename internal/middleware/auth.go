package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const (
	ContextKeyUserID   contextKey = "user_id"
	ContextKeyRole     contextKey = "role"
	ContextKeyInstID   contextKey = "institution_id"
	ContextKeyAdminID  contextKey = "admin_id"
)

type userRow struct {
	ID            string
	Role          string
	InstitutionID *string
	Status        string
}

func Authenticate(jwtSecret string, db *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				Unauthorized(w)
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

			token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
				return []byte(jwtSecret), nil
			}, jwt.WithValidMethods([]string{"HS256"}))
			if err != nil || !token.Valid {
				Unauthorized(w)
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				Unauthorized(w)
				return
			}

			supabaseUID, _ := claims["sub"].(string)
			if supabaseUID == "" {
				Unauthorized(w)
				return
			}

			// Look up in users table first, then admin_accounts
			var u userRow
			err = db.QueryRow(r.Context(),
				`SELECT id, role, institution_id, status FROM users WHERE supabase_uid = $1 AND deleted_at IS NULL`,
				supabaseUID,
			).Scan(&u.ID, &u.Role, &u.InstitutionID, &u.Status)

			if err != nil {
				// Try admin_accounts
				var adminID, adminRole, adminStatus string
				err2 := db.QueryRow(r.Context(),
					`SELECT id, role, status FROM admin_accounts WHERE supabase_uid = $1 AND deleted_at IS NULL`,
					supabaseUID,
				).Scan(&adminID, &adminRole, &adminStatus)
				if err2 != nil {
					Unauthorized(w)
					return
				}
				if adminStatus != "active" {
					Error(w, http.StatusForbidden, "ACCOUNT_SUSPENDED", "account is suspended")
					return
				}
				ctx := context.WithValue(r.Context(), ContextKeyAdminID, adminID)
				ctx = context.WithValue(ctx, ContextKeyUserID, adminID)
				ctx = context.WithValue(ctx, ContextKeyRole, adminRole)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			if u.Status == "suspended" {
				Error(w, http.StatusForbidden, "ACCOUNT_SUSPENDED", "account is suspended")
				return
			}

			// Check institution suspension for non-admin users
			if u.InstitutionID != nil && (u.Role == "student" || u.Role == "teacher") {
				var instStatus string
				_ = db.QueryRow(r.Context(),
					`SELECT status FROM institutions WHERE id = $1`,
					*u.InstitutionID,
				).Scan(&instStatus)
				if instStatus == "suspended" {
					Error(w, http.StatusForbidden, "INSTITUTION_SUSPENDED", "your institution is currently suspended")
					return
				}
			}

			ctx := context.WithValue(r.Context(), ContextKeyUserID, u.ID)
			ctx = context.WithValue(ctx, ContextKeyRole, u.Role)
			if u.InstitutionID != nil {
				ctx = context.WithValue(ctx, ContextKeyInstID, *u.InstitutionID)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireRole(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(roles))
	for _, r := range roles {
		allowed[r] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := r.Context().Value(ContextKeyRole).(string)
			if !allowed[role] {
				Forbidden(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func RequireCronSecret(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Cron-Secret") != secret {
				Unauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func GetUserID(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeyUserID).(string)
	return v
}

func GetRole(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeyRole).(string)
	return v
}

func GetInstitutionID(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeyInstID).(string)
	return v
}

func GetAdminID(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeyAdminID).(string)
	return v
}
