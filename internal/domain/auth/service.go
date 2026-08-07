package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/qwish/backend/internal/httpx"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/config"
)

type Service struct {
	db  *pgxpool.Pool
	cfg *config.Config
	wa  *webauthn.WebAuthn // nil if passkey config is invalid; endpoints then 503
}

func NewService(db *pgxpool.Pool, cfg *config.Config) *Service {
	s := &Service{db: db, cfg: cfg}

	origins := make([]string, 0, 2)
	for _, o := range strings.Split(cfg.WebAuthnRPOrigins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.WebAuthnRPID,
		RPDisplayName: cfg.WebAuthnRPDisplayName,
		RPOrigins:     origins,
		// Without this block go-webauthn falls back to its zero value, where
		// ResidentKey is "discouraged". Discoverable (usernameless / autofill)
		// login then only works when an authenticator happens to create a
		// resident key anyway — true for iCloud Keychain and Google Password
		// Manager, false for roaming security keys — so the feature appeared to
		// work while silently failing for a slice of users.
		//
		// Preferred, not Required: a Required resident key makes enrolment fail
		// outright on authenticators with no free credential slots, and an
		// email+passkey login does not need discoverability.
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationPreferred,
		},
	})
	if err != nil {
		log.Printf("auth: passkey/WebAuthn disabled — invalid config: %v", err)
	} else {
		s.wa = wa
	}
	return s
}

// SupabaseSendOTP sends a magic-link / OTP email via Supabase.
func (s *Service) SupabaseSendOTP(ctx context.Context, email string) error {
	b, _ := json.Marshal(map[string]string{"email": email})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.SupabaseURL+"/auth/v1/otp", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", s.cfg.SupabaseAnonKey)

	resp, err := httpx.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase error %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// SupabaseVerifyOTP verifies the 6-digit OTP and returns tokens.
func (s *Service) SupabaseVerifyOTP(ctx context.Context, email, token string) (*SupabaseAuthResponse, error) {
	return s.supabasePost(ctx, "/auth/v1/verify", map[string]string{
		"type":  "email",
		"email": email,
		"token": token,
	})
}

// SupabaseRefresh refreshes a session.
func (s *Service) SupabaseRefresh(ctx context.Context, refreshToken string) (*SupabaseAuthResponse, error) {
	return s.supabasePost(ctx, "/auth/v1/token?grant_type=refresh_token", map[string]string{
		"refresh_token": refreshToken,
	})
}

// SupabaseLogout invalidates the user's session.
func (s *Service) SupabaseLogout(ctx context.Context, accessToken string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.SupabaseURL+"/auth/v1/logout", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("apikey", s.cfg.SupabaseServiceKey)
	resp, err := httpx.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// NormalizeEmail is the one definition of "the same address" in this codebase.
// The DB trigger from migration 038 applies the identical rule on write, so a
// value compared here matches the value that will be stored.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// UserExistsByEmail returns true if a user with the given email exists in the DB.
func (s *Service) UserExistsByEmail(ctx context.Context, email string) bool {
	var exists bool
	s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE lower(btrim(email)) = $1 AND deleted_at IS NULL)`,
		NormalizeEmail(email),
	).Scan(&exists)
	return exists
}

// ErrEmailTaken reports an address that already holds an identity somewhere in
// Qwish. Surface is a human phrase naming where, Role the role held there.
//
// One address is one person on one surface: a student in the app cannot also be
// a super admin in the console. Enforced in the database by the
// one_identity_per_email trigger; this type is how the API says so in advance,
// with a sentence a person can act on.
type ErrEmailTaken struct {
	Surface string
	Role    string
}

func (e ErrEmailTaken) Error() string {
	return "email already registered on " + e.Surface + " as " + e.Role
}

// Human returns the message shown to whoever hit the rule.
func (e ErrEmailTaken) Human() string {
	return "That email is already registered on " + e.Surface + " as a " +
		strings.ReplaceAll(e.Role, "_", " ") + ". One email address can hold one Qwish account."
}

var surfaceForRole = map[string]string{
	"student":           "the Qwish app",
	"parent":            "the Qwish app",
	"teacher":           "the teacher panel",
	"institution_admin": "the institute dashboard",
	"super_admin":       "the admin console",
	"moderator":         "the admin console",
	"support_agent":     "the admin console",
}

// EmailIdentity reports the identity already holding an address, across both
// identity tables. Returns nil when the address is free.
//
// Callers use it to refuse early — at invite or provision time — so nobody is
// emailed a link that can only dead-end. The trigger is still the authority;
// this check can lose a race with a concurrent signup, which is exactly why it
// is not the only guard.
func (s *Service) EmailIdentity(ctx context.Context, email string) *ErrEmailTaken {
	return EmailIdentityIn(ctx, s.db, email)
}

// EmailIdentityIn is EmailIdentity for callers that hold a pool rather than an
// auth Service — the institution and admin handlers, which must refuse an
// invite before it is sent.
func EmailIdentityIn(ctx context.Context, db *pgxpool.Pool, email string) *ErrEmailTaken {
	norm := NormalizeEmail(email)
	if norm == "" {
		return nil
	}

	var role string
	err := db.QueryRow(ctx,
		`SELECT role FROM users
		  WHERE lower(btrim(email)) = $1 AND deleted_at IS NULL
		 UNION ALL
		 SELECT role FROM admin_accounts
		  WHERE lower(btrim(email)) = $1 AND deleted_at IS NULL
		 LIMIT 1`,
		norm,
	).Scan(&role)
	if err != nil {
		return nil // no row, or a read failure: let the trigger be the authority
	}

	surface, ok := surfaceForRole[role]
	if !ok {
		surface = "Qwish"
	}
	return &ErrEmailTaken{Surface: surface, Role: role}
}

// IsEmailTakenErr reports whether a database error is the one_identity_per_email
// trigger refusing a write, as opposed to an ordinary duplicate key.
func IsEmailTakenErr(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == "one_identity_per_email"
}

// GetUserBySupabaseUID returns the local user record for a Supabase UID, if it exists.
func (s *Service) GetUserBySupabaseUID(ctx context.Context, uid string) (*UserProfile, error) {
	var u UserProfile
	err := s.db.QueryRow(ctx,
		`SELECT id, full_name, display_name, email, role, institution_id, status, total_points, current_streak, longest_streak, member_since
		 FROM users WHERE supabase_uid = $1 AND deleted_at IS NULL`,
		uid,
	).Scan(&u.ID, &u.FullName, &u.DisplayName, &u.Email, &u.Role,
		&u.InstitutionID, &u.Status, &u.TotalPoints, &u.CurrentStreak, &u.LongestStreak, &u.MemberSince)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetAdminForLogin returns an active admin_accounts record matching the
// Supabase UID or email. Invited admins may have a placeholder supabase_uid
// (set before their auth user existed), so match by email too and self-heal
// the stored UID to the real one on first login.
func (s *Service) GetAdminForLogin(ctx context.Context, uid, email string) (*AdminAccount, error) {
	var a AdminAccount
	err := s.db.QueryRow(ctx,
		// Case-insensitive on email: stored addresses are normalised (038) and
		// the address Supabase hands back is whatever the admin typed. A
		// case-sensitive match here would lock out an invited admin whose
		// stored row was lowercased.
		`SELECT id, supabase_uid, name, email, role, status, token_generation
		   FROM admin_accounts
		 WHERE (supabase_uid = $1 OR lower(btrim(email)) = $2) AND deleted_at IS NULL`,
		uid, NormalizeEmail(email),
	).Scan(&a.ID, &a.SupabaseUID, &a.Name, &a.Email, &a.Role, &a.Status, &a.TokenGeneration)
	if err != nil {
		return nil, err
	}
	if a.SupabaseUID != uid {
		s.db.Exec(ctx, `UPDATE admin_accounts SET supabase_uid = $1 WHERE id = $2`, uid, a.ID)
	}
	return &a, nil
}

// ActivateAdmin promotes a pending/invite_failed admin to active on their first
// successful sign-in (invite accepted). Mirrors the middleware self-heal so an
// invited admin isn't blocked at login before reaching a protected route.
func (s *Service) ActivateAdmin(ctx context.Context, id string) {
	s.db.Exec(ctx, `UPDATE admin_accounts SET status='active', accepted_at=now() WHERE id=$1`, id)
}

// CreateUser inserts a new user into the users table.
func (s *Service) CreateUser(ctx context.Context, supabaseUID, fullName, email, role string, institutionID *string, status string) (UserProfile, error) {
	var u UserProfile
	err := s.db.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id, status)
		 VALUES ($1, $2, $2, $3, $4, $5, $6)
		 RETURNING id, full_name, display_name, email, role, institution_id, status, total_points, current_streak, longest_streak, member_since`,
		supabaseUID, fullName, email, role, institutionID, status,
	).Scan(&u.ID, &u.FullName, &u.DisplayName, &u.Email, &u.Role,
		&u.InstitutionID, &u.Status, &u.TotalPoints, &u.CurrentStreak, &u.LongestStreak, &u.MemberSince)
	if err != nil {
		return u, err
	}
	s.db.Exec(ctx, `INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, u.ID)
	return u, nil
}

// CreateStudentEnrollment records a referral-code signup as an active
// enrollment with the academic fields left blank for an admin to fill in.
//
// Without this the student would carry an institution_id that no roster query
// would ever surface, since rosters are built from enrollments.
func (s *Service) CreateStudentEnrollment(ctx context.Context, instID, userID, fullName string) (string, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
		 VALUES ($1,$2,$3,'active',now()) RETURNING id`, instID, userID, fullName).Scan(&id)
	return id, err
}

// TeacherInvite is a pending teacher invitation looked up by its email token.
type TeacherInvite struct {
	ID              string    `json:"id"`
	InstitutionID   string    `json:"institution_id"`
	InstitutionName string    `json:"institution_name"`
	Email           string    `json:"email"`
	Name            *string   `json:"name,omitempty"`
	Status          string    `json:"status"` // 'pending' | 'accepted' | 'expired' | 'revoked'
	ExpiresAt       time.Time `json:"expires_at"`
}

// GetTeacherInviteByToken fetches an invite (any status) by its token. A
// pending invite past expires_at is reported with status 'expired'.
func (s *Service) GetTeacherInviteByToken(ctx context.Context, token string) (*TeacherInvite, error) {
	inv := &TeacherInvite{}
	err := s.db.QueryRow(ctx,
		`SELECT ti.id, ti.institution_id, i.name, ti.email, ti.name, ti.status, ti.expires_at
		 FROM teacher_invites ti
		 JOIN institutions i ON i.id = ti.institution_id
		 WHERE ti.token = $1`, token,
	).Scan(&inv.ID, &inv.InstitutionID, &inv.InstitutionName, &inv.Email, &inv.Name, &inv.Status, &inv.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if inv.Status == "pending" && time.Now().After(inv.ExpiresAt) {
		inv.Status = "expired"
	}
	return inv, nil
}

// MarkTeacherInviteAccepted flags the invite consumed.
func (s *Service) MarkTeacherInviteAccepted(ctx context.Context, inviteID string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE teacher_invites SET status='accepted', accepted_at=now() WHERE id=$1`, inviteID)
	return err
}

// FindInstitutionByReferralCode looks up an institution by student or teacher code.
func (s *Service) FindInstitutionByReferralCode(ctx context.Context, code string) (string, string, error) {
	var instID, role string
	err := s.db.QueryRow(ctx,
		`SELECT id, 'student' as role FROM institutions WHERE student_referral_code = $1 AND status = 'verified'`,
		code,
	).Scan(&instID, &role)
	if err == nil {
		return instID, role, nil
	}
	err = s.db.QueryRow(ctx,
		`SELECT id, 'teacher' as role FROM institutions WHERE teacher_referral_code = $1 AND status = 'verified'`,
		code,
	).Scan(&instID, &role)
	if err == nil {
		return instID, role, nil
	}
	return "", "", fmt.Errorf("invalid referral code")
}

// GetInstitutionName returns the institution name for a given ID.
func (s *Service) GetInstitutionName(ctx context.Context, id string) string {
	var name string
	s.db.QueryRow(ctx, `SELECT name FROM institutions WHERE id = $1`, id).Scan(&name)
	return name
}

// UpdateUserInstitution switches a user's institution via referral code.
//
// For a student this is an enrollment change, not just a column write. Rosters
// are built from enrollments, so setting users.institution_id alone would leave
// the student invisible to the institution they just joined.
//
// Teachers have no enrollment — their institution relationship is the users row
// plus group_teachers — so they take the column-only path.
func (s *Service) UpdateUserInstitution(ctx context.Context, userID, code string) error {
	instID, role, err := s.FindInstitutionByReferralCode(ctx, code)
	if err != nil {
		return err
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE users SET institution_id = $1, role = $2, updated_at = now() WHERE id = $3`,
		instID, role, userID); err != nil {
		return err
	}

	if role == "student" {
		// Already here: re-entering the same code is a no-op rather than a
		// transfer out and straight back in, which would leave a dead row in
		// the student's history for nothing.
		var alreadyHere int
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM enrollments
			  WHERE user_id=$1 AND institution_id=$2 AND status IN ('active','suspended')`,
			userID, instID).Scan(&alreadyHere); err != nil {
			return err
		}
		if alreadyHere == 0 {
			// End any live enrollment first: enrollments_one_active_per_user
			// would reject the insert while the old row is still live.
			if _, err := tx.Exec(ctx,
				`UPDATE enrollments
				    SET status='transferred', ended_at=now(), updated_at=now()
				  WHERE user_id=$1 AND status IN ('active','suspended')`, userID); err != nil {
				return err
			}

			var fullName string
			if err := tx.QueryRow(ctx,
				`SELECT full_name FROM users WHERE id=$1`, userID).Scan(&fullName); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
				 VALUES ($1,$2,$3,'active',now())`, instID, userID, fullName); err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}

func (s *Service) supabasePost(ctx context.Context, path string, body map[string]string) (*SupabaseAuthResponse, error) {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.SupabaseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", s.cfg.SupabaseAnonKey)

	resp, err := httpx.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("supabase error %d: %s", resp.StatusCode, string(raw))
	}

	var result SupabaseAuthResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SupabaseAuthResponse represents Supabase's auth response.
type SupabaseAuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
}

type AdminAccount struct {
	ID          string
	SupabaseUID string
	Name        string
	Email       string
	Role        string
	Status      string
	// TokenGeneration backs passkey session revocation; see
	// migrations/040_passkey_token_generation.sql.
	TokenGeneration int
}

type UserProfile struct {
	ID            string     `json:"id"`
	FullName      string     `json:"full_name"`
	DisplayName   string     `json:"display_name"`
	Email         string     `json:"email"`
	Role          string     `json:"role"`
	InstitutionID *string    `json:"institution_id,omitempty"`
	Status        string     `json:"status"`
	TotalPoints   int64      `json:"total_points"`
	CurrentStreak int        `json:"current_streak"`
	LongestStreak int        `json:"longest_streak"`
	MemberSince   time.Time  `json:"member_since"`
	LastActiveAt  *time.Time `json:"last_active_at,omitempty"`
}
