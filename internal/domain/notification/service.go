package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

type Service struct {
	apiKey    string
	fromEmail string
}

func NewService(apiKey string) *Service {
	return &Service{
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

func (s *Service) SendEmail(ctx context.Context, to, subject, html string) error {
	if s.apiKey == "" {
		log.Printf("[notification] skipping email to %s (no API key configured): %s", to, subject)
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
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("resend error %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

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
	return s.SendEmail(ctx, contactEmail, "Your QuizApp Institution Has Been Approved", html)
}

func (s *Service) SendInstitutionRejection(ctx context.Context, contactEmail, instName, reason string) error {
	html := fmt.Sprintf(`
<h2>QuizApp Institution Application</h2>
<p>We're sorry, but your application for <strong>%s</strong> was not approved.</p>
<p><strong>Reason:</strong> %s</p>
<p>You may reapply after addressing the above issues.</p>
`, instName, reason)
	return s.SendEmail(ctx, contactEmail, "QuizApp Institution Application Update", html)
}

func (s *Service) SendPasswordReset(ctx context.Context, email, resetLink string) error {
	html := fmt.Sprintf(`
<h2>Password Reset</h2>
<p>Click the link below to reset your password. This link expires in 15 minutes.</p>
<p><a href="%s">Reset Password</a></p>
<p>If you did not request this, please ignore this email.</p>
`, resetLink)
	return s.SendEmail(ctx, email, "QuizApp Password Reset", html)
}
