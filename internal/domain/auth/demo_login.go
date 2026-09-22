package auth

// Store-review demo login.
//
// App store reviewers cannot read the OTP mailbox, and Supabase has no
// fixed-OTP test account for email (the SMS equivalent exists; the email one
// has never shipped). So one configured address accepts one configured code
// and receives a normally minted session — the same session the passkey path
// issues, which every protected route already accepts.
//
// Off unless DEMO_LOGIN_EMAIL and DEMO_LOGIN_OTP are both set. The account is
// email+OTP only and cannot enrol a passkey: a credential created on a
// reviewer's device would outlive the review with nothing on our side able to
// revoke it. The enrolment guards live in passkey_user.go.

import (
	"context"
	"crypto/subtle"
)

// IsDemoLoginEmail reports whether email is the configured review address.
func (s *Service) IsDemoLoginEmail(email string) bool {
	if s.cfg.DemoLoginEmail == "" || s.cfg.DemoLoginOTP == "" {
		return false
	}
	return NormalizeEmail(email) == s.cfg.DemoLoginEmail
}

// IsDemoLogin reports whether this email and code are the review credential.
//
// Six digits is the whole keyspace the app can type, so the comparison is
// constant-time and the route it guards is rate limited; the address itself is
// the rest of the secret.
func (s *Service) IsDemoLogin(email, otp string) bool {
	if !s.IsDemoLoginEmail(email) {
		return false
	}
	want := s.cfg.DemoLoginOTP
	return len(otp) == len(want) &&
		subtle.ConstantTimeCompare([]byte(otp), []byte(want)) == 1
}

// MintDemoSession issues a session for the review account.
//
// The account has to already exist as an ordinary learner, seeded once through
// the real OTP flow, so this never creates a user and never reports a new one:
// a reviewer must land on the app, not on the create-profile screen.
func (s *Service) MintDemoSession(ctx context.Context, email string) (uid, access, refresh string, err error) {
	u, err := s.getUserByEmail(ctx, email)
	if err != nil {
		return "", "", "", err
	}
	access, refresh, err = s.mintSession(u.SupabaseUID, u.Email, u.TokenGeneration)
	if err != nil {
		return "", "", "", err
	}
	return u.SupabaseUID, access, refresh, nil
}
