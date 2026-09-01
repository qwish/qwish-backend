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
	ContextKeyUserID      contextKey = "user_id"
	ContextKeyRole        contextKey = "role"
	ContextKeyInstID      contextKey = "institution_id"
	ContextKeyAdminID     contextKey = "admin_id"
	ContextKeySupabaseUID contextKey = "supabase_uid"
	ContextKeyEmail       contextKey = "email"
	ContextKeyUserRecord  contextKey = "user_record"
)

type userRow struct {
	ID            string
	Role          string
	InstitutionID *string
	Status        string
	TokenGen      int
}

// tokenGen reads the `gen` claim stamped into passkey-minted tokens
// (internal/domain/auth/passkey.go). Absent means either a Supabase-issued token
// or one minted before migration 040 — both read as generation 0, matching the
// column default, so nobody is logged out by the deploy that adds this.
func tokenGen(claims jwt.MapClaims) int {
	if f, ok := claims["gen"].(float64); ok {
		return int(f)
	}
	return 0
}

func Authenticate(jwtSecret, supabaseURL string, db *pgxpool.Pool) func(http.Handler) http.Handler {
	keyFunc := makeKeyFunc(jwtSecret, supabaseURL)
	opts := parseOpts(supabaseURL)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			var tokenStr string
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
			} else {
				tokenStr = r.URL.Query().Get("token")
			}
			if tokenStr == "" {
				Unauthorized(w)
				return
			}

			token, err := jwt.Parse(tokenStr, keyFunc, opts...)
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

			// One round trip for everything the rest of this middleware needs:
			// the users row, its institution's status, and any admin_accounts
			// row for the same uid. This used to be two or three sequential
			// queries on every authenticated request. The outer LEFT JOINs off a
			// single-row VALUES mean exactly one row always comes back, so a
			// scan error here is a real failure, not "no such user".
			var u userRow
			var instStatus, adminID, adminRole, adminStatus string
			var adminTokenGen int
			err = db.QueryRow(r.Context(), `
				SELECT COALESCE(u.id::text,''), COALESCE(u.role,''),
				       u.institution_id, COALESCE(u.status,''),
				       COALESCE(u.token_generation,0),
				       COALESCE(i.status,''),
				       COALESCE(a.id::text,''), COALESCE(a.role,''), COALESCE(a.status,''),
				       COALESCE(a.token_generation,0)
				  FROM (VALUES ($1::uuid)) AS p(uid)
				  LEFT JOIN users u
				         ON u.supabase_uid = p.uid AND u.deleted_at IS NULL
				  LEFT JOIN institutions i ON i.id = u.institution_id
				  LEFT JOIN admin_accounts a
				         ON a.supabase_uid = p.uid AND a.deleted_at IS NULL`,
				supabaseUID,
			).Scan(&u.ID, &u.Role, &u.InstitutionID, &u.Status, &u.TokenGen,
				&instStatus, &adminID, &adminRole, &adminStatus, &adminTokenGen)
			if err != nil {
				// Malformed sub (not a uuid) or a dead database — either way the
				// request cannot be authenticated.
				Unauthorized(w)
				return
			}

			// No users row: fall back to the admin_accounts identity, exactly as
			// the previous two-query version did.
			if u.ID == "" {
				if adminID == "" {
					Unauthorized(w)
					return
				}
				// Session revocation: a token minted before the last
				// "sign out everywhere" is dead on the very next request,
				// rather than lingering for the access token's full hour.
				if tokenGen(claims) != adminTokenGen {
					Unauthorized(w)
					return
				}
				switch adminStatus {
				case "active":
					// proceed
				case "pending", "invite_failed":
					// First successful auth = invite accepted. Promote to active.
					db.Exec(r.Context(),
						`UPDATE admin_accounts SET status='active', accepted_at=now() WHERE id=$1`, adminID)
				default: // suspended (deleted rows are excluded by the query)
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

			// Session revocation — see the admin branch above.
			if tokenGen(claims) != u.TokenGen {
				Unauthorized(w)
				return
			}

			// Check institution suspension for non-admin users
			if u.InstitutionID != nil && (u.Role == "student" || u.Role == "teacher") &&
				instStatus == "suspended" {
				Error(w, http.StatusForbidden, "INSTITUTION_SUSPENDED", "your institution is currently suspended")
				return
			}

			ctx := context.WithValue(r.Context(), ContextKeyUserID, u.ID)
			ctx = context.WithValue(ctx, ContextKeyUserRecord, true)
			ctx = context.WithValue(ctx, ContextKeyRole, u.Role)
			if u.InstitutionID != nil {
				ctx = context.WithValue(ctx, ContextKeyInstID, *u.InstitutionID)
			}

			// A super_admin/moderator/support_agent may be resolved here via the
			// users table (it's checked first) while also having an admin_accounts
			// row. Admin handlers write the actor into admin_accounts FK columns and
			// the audit log, so surface that admin id when one exists. Best-effort:
			// when there's no admin_accounts row, GetAdminID stays empty and the
			// handlers fall back to NULL.
			if adminID != "" &&
				(u.Role == "super_admin" || u.Role == "moderator" || u.Role == "support_agent") {
				ctx = context.WithValue(ctx, ContextKeyAdminID, adminID)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireUserRecord rejects an admin_accounts-only principal from routes whose
// data is backed by users.id. Authenticate accepts both identity tables because
// admin and app routes share a router; without this second boundary an admin ID
// reaches user foreign keys and turns an authorization mistake into assorted
// 404/500 responses (and, more importantly, crosses identity surfaces).
func RequireUserRecord() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			isUser, _ := r.Context().Value(ContextKeyUserRecord).(bool)
			if !isUser {
				Forbidden(w)
				return
			}
			next.ServeHTTP(w, r)
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

func GetSupabaseUID(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeySupabaseUID).(string)
	return v
}

func GetEmail(r *http.Request) string {
	v, _ := r.Context().Value(ContextKeyEmail).(string)
	return v
}

// AuthenticateJWTOnly validates the JWT signature without requiring a DB user record.
// Use for endpoints where the user may not yet exist in the DB (e.g. create-profile).
func AuthenticateJWTOnly(jwtSecret, supabaseURL string) func(http.Handler) http.Handler {
	keyFunc := makeKeyFunc(jwtSecret, supabaseURL)
	opts := parseOpts(supabaseURL)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") {
				Unauthorized(w)
				return
			}
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

			token, err := jwt.Parse(tokenStr, keyFunc, opts...)
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
			email, _ := claims["email"].(string)
			if supabaseUID == "" {
				Unauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), ContextKeySupabaseUID, supabaseUID)
			ctx = context.WithValue(ctx, ContextKeyEmail, email)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
