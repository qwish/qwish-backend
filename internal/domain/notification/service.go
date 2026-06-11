package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	db        *pgxpool.Pool
	apiKey    string
	fromEmail string
	push      pusherAdapter
}

func NewService(db *pgxpool.Pool, apiKey string) *Service {
	return &Service{
		db:        db,
		apiKey:    apiKey,
		fromEmail: "QuizApp <noreply@quizapp.in>",
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

func (s *Service) SendInstitutionApproval(ctx context.Context, contactEmail, instName, adminEmail, adminPassword, sCode, tCode string) error {
	html := fmt.Sprintf(`
<h2>Welcome to QuizApp!</h2>
<p>Your institution <strong>%s</strong> has been approved.</p>
<h3>Admin Credentials</h3>
<p>Email: <strong>%s</strong></p>
<p>Temporary Password: <strong>%s</strong></p>
<h3>Referral Codes</h3>
<p>Student Code: <strong>%s</strong></p>
<p>Teacher Code: <strong>%s</strong></p>
<p>Please change your password after first login.</p>
`, instName, adminEmail, adminPassword, sCode, tCode)
	return s.SendEmail(ctx, contactEmail, "Your QuizApp Institution Has Been Approved", html, "institution_approval")
}

func (s *Service) SendInstitutionRejection(ctx context.Context, contactEmail, instName, reason string) error {
	html := fmt.Sprintf(`
<h2>QuizApp Institution Application</h2>
<p>We're sorry, but your application for <strong>%s</strong> was not approved.</p>
<p><strong>Reason:</strong> %s</p>
<p>You may reapply after addressing the above issues.</p>
`, instName, reason)
	return s.SendEmail(ctx, contactEmail, "QuizApp Institution Application Update", html, "institution_rejection")
}

func (s *Service) SendPasswordReset(ctx context.Context, email, resetLink string) error {
	html := fmt.Sprintf(`
<h2>Password Reset</h2>
<p>Click the link below to reset your password. This link expires in 15 minutes.</p>
<p><a href="%s">Reset Password</a></p>
<p>If you did not request this, please ignore this email.</p>
`, resetLink)
	return s.SendEmail(ctx, email, "QuizApp Password Reset", html, "password_reset")
}

// SendTeacherInvite emails a teacher invite link to the given address.
func (s *Service) SendTeacherInvite(ctx context.Context, to, name, instName, inviteToken, appURL, inviteID string) error {
	inviteLink := fmt.Sprintf("%s/auth/teacher-signup?token=%s", appURL, inviteToken)
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + name
	}
	html := fmt.Sprintf(`
<h2>You're invited to join QuizApp as a Teacher</h2>
<p>%s,</p>
<p><strong>%s</strong> has invited you to join their institution on QuizApp as a teacher.</p>
<p>Click the button below to set up your account. This invite expires in 7 days.</p>
<p style="margin:24px 0">
  <a href="%s"
     style="background:#4F46E5;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;font-weight:bold">
    Accept Invitation
  </a>
</p>
<p style="font-size:12px;color:#888">Or copy this link: %s</p>
<p style="font-size:12px;color:#888">If you weren't expecting this invitation, you can safely ignore this email.</p>
`, greeting, instName, inviteLink, inviteLink)
	return s.SendEmail(ctx, to, "You're invited to teach on QuizApp", html,
		fmt.Sprintf("teacher_invite:%s", inviteID))
}

func (s *Service) SendAdminInvite(ctx context.Context, to, name, role, inviteLink string) error {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + name
	}
	html := fmt.Sprintf(`
<h2>You're invited to join QuizApp as an Admin</h2>
<p>%s,</p>
<p>You have been invited to join QuizApp as a <strong>%s</strong>.</p>
<p>Click the button below to set up your account and access the Admin Dashboard.</p>
<p style="margin:24px 0">
  <a href="%s"
     style="background:#4F46E5;color:#fff;padding:12px 24px;border-radius:6px;text-decoration:none;font-weight:bold">
    Accept Invitation
  </a>
</p>
<p style="font-size:12px;color:#888">Or copy this link: %s</p>
`, greeting, role, inviteLink, inviteLink)
	return s.SendEmail(ctx, to, "You're invited to join QuizApp as an Admin", html, "admin_invite")
}

func (s *Service) SendAdminWelcome(ctx context.Context, to, name, role string) error {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + name
	}
	html := fmt.Sprintf(`
<h2>Welcome to QuizApp Admins!</h2>
<p>%s,</p>
<p>You have been granted <strong>%s</strong> privileges on QuizApp.</p>
<p>You can now log in using your existing credentials at the Admin Dashboard.</p>
`, greeting, role)
	return s.SendEmail(ctx, to, "QuizApp Admin Access Granted", html, "admin_welcome")
}
