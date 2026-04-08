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

// SupabaseSignUp creates a user in Supabase Auth and returns the uid + tokens.
func (s *Service) SupabaseSignUp(ctx context.Context, email, password string) (*SupabaseAuthResponse, error) {
	return s.supabasePost(ctx, "/auth/v1/signup", map[string]string{
		"email":    email,
		"password": password,
	})
}

// SupabaseLogin authenticates with Supabase and returns tokens.
func (s *Service) SupabaseLogin(ctx context.Context, email, password string) (*SupabaseAuthResponse, error) {
	return s.supabasePost(ctx, "/auth/v1/token?grant_type=password", map[string]string{
		"email":    email,
		"password": password,
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

// FindInstitutionByReferralCode looks up an institution by student or teacher code.
// Returns (institutionID, role, error).
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
	// Create streak record
	s.db.Exec(ctx, `INSERT INTO streaks (user_id) VALUES ($1) ON CONFLICT DO NOTHING`, u.ID)
	return u, nil
}

// GetInstitutionName returns just the institution name for signup confirmation.
func (s *Service) GetInstitutionName(ctx context.Context, id string) string {
	var name string
	s.db.QueryRow(ctx, `SELECT name FROM institutions WHERE id = $1`, id).Scan(&name)
	return name
}

// UpdateUserInstitution switches a user's institution.
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

// StoreForgotPasswordOTP stores a 6-digit OTP with 15-minute expiry.
// We reuse Supabase's built-in OTP reset flow.
func (s *Service) SendPasswordResetOTP(ctx context.Context, email string) error {
	_, err := s.supabasePost(ctx, "/auth/v1/recover", map[string]string{
		"email": email,
	})
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
	req.Header.Set("apikey", s.cfg.SupabaseServiceKey)

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

// SupabaseAuthResponse is a simplified representation of Supabase's auth response.
type SupabaseAuthResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	User         struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	} `json:"user"`
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
