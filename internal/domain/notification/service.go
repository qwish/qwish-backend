package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/qwish/backend/internal/httpx"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db        *pgxpool.Pool
	apiKey    string
	fromEmail string
	push      pusherAdapter
	pushQ     chan pushJob

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
		fromEmail:     "Qwish <noreply@mail.qwish.in>",
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

	resp, err := httpx.Client.Do(req)
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

// SendUserWelcome greets a user immediately after their first profile is
// created. Repeated logins do not call this path.
func (s *Service) SendUserWelcome(ctx context.Context, to, name, appURL string) error {
	return s.SendEmail(ctx, to, "Welcome to Qwish",
		tmplUserWelcome(name, s.dashURL(appURL)), "user_welcome")
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

// SendTeacherInvite emails a teacher invite link to the given address.
func (s *Service) SendTeacherInvite(ctx context.Context, to, name, instName, inviteToken, appURL, inviteID string) error {
	inviteLink := fmt.Sprintf("%s/auth/teacher-signup?token=%s", appURL, inviteToken)
	return s.SendEmail(ctx, to, "You're invited to teach on Qwish",
		tmplTeacherInvite(name, instName, inviteLink),
		fmt.Sprintf("teacher_invite:%s", inviteID))
}

// SendTeacherVerified tells a teacher their institution approved their account
// and they can now sign in with email + OTP.
func (s *Service) SendTeacherVerified(ctx context.Context, to, name, instName, loginURL string) error {
	return s.SendEmail(ctx, to, "Your Qwish teacher account is verified",
		tmplTeacherVerified(name, instName, loginURL), "teacher_verified")
}

// SendAccountInvite tells a directly added institute admin or teacher their
// account exists and that they sign in with email + one-time code.
func (s *Service) SendAccountInvite(ctx context.Context, to, name, roleLabel, instName, loginURL, reference string) error {
	return s.SendEmail(ctx, to, "You’re invited to "+instName+" on Qwish",
		tmplAccountInvite(name, roleLabel, instName, s.dashURL(loginURL)), reference)
}

// SendInstitutionInvite emails a "bring Qwish to your institution" invite with a
// link to the public application form. reference ties it back to the originating
// contact submission (e.g. "institution_invite:<submissionID>").
func (s *Service) SendInstitutionInvite(ctx context.Context, to, name, applyLink, reference string) error {
	return s.SendEmail(ctx, to, "Bring Qwish to your institution",
		tmplInstitutionInvite(name, applyLink), reference)
}

// SendWeeklyInsights emails the user their weekly score breakdown.
func (s *Service) SendWeeklyInsights(ctx context.Context, to, name string, pointsThisWeek int64, deltaPct float64, quizzes int, avgScore float64, streak int, domain, suggestion string) error {
	trend := fmt.Sprintf("%+.0f%% vs last week", deltaPct)
	return s.SendEmail(ctx, to, "Your weekly Qwish insights",
		tmplWeeklyInsights(name, pointsThisWeek, trend, quizzes, avgScore, streak, domain, suggestion),
		"weekly_insights")
}

// SendAppLoginDenied explains a rejected, verified app login privately by email.
func (s *Service) SendAppLoginDenied(ctx context.Context, to string) error {
	return s.SendEmail(ctx, to, "Your Qwish app login was declined", tmplAppLoginDenied())
}
