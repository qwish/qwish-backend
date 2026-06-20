package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db        *pgxpool.Pool
	apiKey    string
	fromEmail string
	push      pusherAdapter

	// Dashboard URLs used for "go to dashboard" buttons in emails.
	instituteURL  string // institution admin dashboard
	superAdminURL string // internal admin console

	mu          sync.RWMutex
	subscribers map[string][]chan Notification
}

func NewService(db *pgxpool.Pool, apiKey, instituteURL, superAdminURL string) *Service {
	return &Service{
		db:            db,
		apiKey:        apiKey,
		fromEmail:     "Qwish <noreply@qwish.in>",
		instituteURL:  instituteURL,
		superAdminURL: superAdminURL,
		subscribers:   make(map[string][]chan Notification),
	}
}

type EmailPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// SendEmail delivers an email via Resend and writes a row to notification_log.
// reference is an optional free-form context string (e.g. "teacher_invite:<id>").
func (s *Service) SendEmail(ctx context.Context, to, subject, html string, reference ...string) error {
	ref := ""
	if len(reference) > 0 {
		ref = reference[0]
	}

	if s.apiKey == "" {
		log.Printf("[notification] skipping email to %s (no API key configured): %s", to, subject)
		s.logSend(ctx, to, subject, "sent", "", ref) // log even when skipped so devs can see intent
		return nil
	}

	payload := EmailPayload{
		From:    s.fromEmail,
		To:      []string{to},
		Subject: subject,
		HTML:    html,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(body))
	if err != nil {
		s.logSend(ctx, to, subject, "failed", err.Error(), ref)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logSend(ctx, to, subject, "failed", err.Error(), ref)
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		errMsg := fmt.Sprintf("resend error %d: %s", resp.StatusCode, string(raw))
		s.logSend(ctx, to, subject, "failed", errMsg, ref)
		return fmt.Errorf("%s", errMsg)
	}

	s.logSend(ctx, to, subject, "sent", "", ref)
	return nil
}

// logSend inserts a row into notification_log. Errors are swallowed (best-effort).
func (s *Service) logSend(ctx context.Context, to, subject, status, errMsg, reference string) {
	if s.db == nil {
		return
	}
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	var refPtr *string
	if reference != "" {
		refPtr = &reference
	}
	s.db.Exec(ctx,
		`INSERT INTO notification_log (to_email, subject, status, error, reference)
		 VALUES ($1, $2, $3, $4, $5)`,
		to, subject, status, errPtr, refPtr)
}

// ── Typed email helpers ───────────────────────────────────────────────────────
//
// Each helper builds its HTML from the shared branded layout in templates.go.
// Dynamic values are escaped inside the template builders, so callers pass
// plain strings.

// SendLoginOTP emails a one-time sign-in code. expiryMinutes should match the
// OTP lifetime configured for the provider (Supabase default: 60 minutes).
//
// NOTE: OTP delivery currently runs through Supabase (auth.SupabaseSendOTP),
// which renders its own email. Wire this helper in only if/when OTP codes are
// generated in-house; alternatively paste otpCode markup into the Supabase
// "Magic Link / OTP" template using its {{ .Token }} placeholder.
func (s *Service) SendLoginOTP(ctx context.Context, to, code string, expiryMinutes int) error {
	return s.SendEmail(ctx, to, "Your Qwish verification code",
		tmplLoginOTP(code, expiryMinutes), "login_otp")
}

func (s *Service) SendInstitutionApproval(ctx context.Context, contactEmail, instName, adminEmail, adminPassword, sCode, tCode string) error {
	return s.SendEmail(ctx, contactEmail, "Your Qwish Institution Has Been Approved",
		tmplInstitutionApproval(instName, adminEmail, adminPassword, sCode, tCode, s.dashURL(s.instituteURL)), "institution_approval")
}

// dashURL returns u, falling back to the public brand site when the dashboard
// URL hasn't been configured so email buttons never point at an empty href.
func (s *Service) dashURL(u string) string {
	if u == "" {
		return "https://qwish.in"
	}
	return u
}

func (s *Service) SendInstitutionRejection(ctx context.Context, contactEmail, instName, reason string) error {
	return s.SendEmail(ctx, contactEmail, "Qwish Institution Application Update",
		tmplInstitutionRejection(instName, reason), "institution_rejection")
}

func (s *Service) SendPasswordReset(ctx context.Context, email, resetLink string) error {
	return s.SendEmail(ctx, email, "Qwish Password Reset",
		tmplPasswordReset(resetLink, 15), "password_reset")
}

// SendTeacherInvite emails a teacher invite link to the given address.
func (s *Service) SendTeacherInvite(ctx context.Context, to, name, instName, inviteToken, appURL, inviteID string) error {
	inviteLink := fmt.Sprintf("%s/auth/teacher-signup?token=%s", appURL, inviteToken)
	return s.SendEmail(ctx, to, "You're invited to teach on Qwish",
		tmplTeacherInvite(name, instName, inviteLink),
		fmt.Sprintf("teacher_invite:%s", inviteID))
}

// SendInstitutionInvite emails a "bring Qwish to your institution" invite with a
// link to the public application form. reference ties it back to the originating
// contact submission (e.g. "institution_invite:<submissionID>").
func (s *Service) SendInstitutionInvite(ctx context.Context, to, name, applyLink, reference string) error {
	return s.SendEmail(ctx, to, "Bring Qwish to your institution",
		tmplInstitutionInvite(name, applyLink), reference)
}

func (s *Service) SendAdminInvite(ctx context.Context, to, name, role, inviteLink string) error {
	return s.SendEmail(ctx, to, "You're invited to join Qwish as an Admin",
		tmplAdminInvite(name, role, inviteLink), "admin_invite")
}

// SendWeeklyInsights emails the user their weekly score breakdown.
func (s *Service) SendWeeklyInsights(ctx context.Context, to, name string, pointsThisWeek int64, deltaPct float64, quizzes int, avgScore float64, streak int, domain, suggestion string) error {
	trend := fmt.Sprintf("%+.0f%% vs last week", deltaPct)
	return s.SendEmail(ctx, to, "Your weekly Qwish insights",
		tmplWeeklyInsights(name, pointsThisWeek, trend, quizzes, avgScore, streak, domain, suggestion),
		"weekly_insights")
}

func (s *Service) SendAdminWelcome(ctx context.Context, to, name, role string) error {
	// Institution admins land on the institution dashboard; internal admin roles
	// (super_admin/moderator/support_agent) land on the super-admin console.
	dash := s.dashURL(s.superAdminURL)
	if role == "institution_admin" {
		dash = s.dashURL(s.instituteURL)
	}
	return s.SendEmail(ctx, to, "Qwish Admin Access Granted",
		tmplAdminWelcome(name, role, dash), "admin_welcome")
}
