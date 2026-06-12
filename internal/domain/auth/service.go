package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/config"
)

type Service struct {
	db  *pgxpool.Pool
	cfg *config.Config
}

func NewService(db *pgxpool.Pool, cfg *config.Config) *Service {
	return &Service{db: db, cfg: cfg}
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

	resp, err := http.DefaultClient.Do(req)
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// UserExistsByEmail returns true if a user with the given email exists in the DB.
func (s *Service) UserExistsByEmail(ctx context.Context, email string) bool {
	var exists bool
	s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email = $1 AND deleted_at IS NULL)`, email,
	).Scan(&exists)
	return exists
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
		`SELECT id, supabase_uid, name, email, role, status FROM admin_accounts
		 WHERE (supabase_uid = $1 OR email = $2) AND deleted_at IS NULL`,
		uid, email,
	).Scan(&a.ID, &a.SupabaseUID, &a.Name, &a.Email, &a.Role, &a.Status)
	if err != nil {
		return nil, err
	}
	if a.SupabaseUID != uid {
		s.db.Exec(ctx, `UPDATE admin_accounts SET supabase_uid = $1 WHERE id = $2`, uid, a.ID)
	}
	return &a, nil
}

// CreateUser inserts a new user into the users table.
func (s *Service) CreateUser(ctx context.Context, supabaseUID, fullName, email, role string, institutionID *string) (UserProfile, error) {
	var u UserProfile
	err := s.db.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role, institution_id)
		 VALUES ($1, $2, $2, $3, $4, $5)
		 RETURNING id, full_name, display_name, email, role, institution_id, status, total_points, current_streak, longest_streak, member_since`,
		supabaseUID, fullName, email, role, institutionID,
	).Scan(&u.ID, &u.FullName, &u.DisplayName, &u.Email, &u.Role,
		&u.InstitutionID, &u.Status, &u.TotalPoints, &u.CurrentStreak, &u.LongestStreak, &u.MemberSince)
	if err != nil {
		return u, err
	}
	s.db.Exec(ctx, `INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, u.ID)
	return u, nil
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
func (s *Service) UpdateUserInstitution(ctx context.Context, userID, code string) error {
	instID, role, err := s.FindInstitutionByReferralCode(ctx, code)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx,
		`UPDATE users SET institution_id = $1, role = $2, updated_at = now() WHERE id = $3`,
		instID, role, userID)
	return err
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

	resp, err := http.DefaultClient.Do(req)
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
