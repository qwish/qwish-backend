package auth

import (
	"testing"

	"github.com/qwish/backend/internal/config"
)

func demoSvc(email, otp string) *Service {
	return &Service{cfg: &config.Config{DemoLoginEmail: email, DemoLoginOTP: otp}}
}

func TestIsDemoLogin(t *testing.T) {
	const email = "play-review@qwish.in"
	const otp = "314159"

	cases := []struct {
		name            string
		svc             *Service
		email, otp      string
		wantEmail, want bool
	}{
		{"unconfigured", demoSvc("", ""), email, otp, false, false},
		{"email without code", demoSvc(email, ""), email, otp, false, false},
		{"code without email", demoSvc("", otp), email, otp, false, false},
		{"exact match", demoSvc(email, otp), email, otp, true, true},
		{"case and space insensitive", demoSvc(email, otp), "  Play-Review@Qwish.IN ", otp, true, true},
		{"wrong code", demoSvc(email, otp), email, "314158", true, false},
		{"empty code", demoSvc(email, otp), email, "", true, false},
		{"code prefix", demoSvc(email, otp), email, "31415", true, false},
		{"other address", demoSvc(email, otp), "learner@qwish.in", otp, false, false},
		{"other address, right code", demoSvc(email, otp), "learner@qwish.in", otp, false, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.svc.IsDemoLoginEmail(c.email); got != c.wantEmail {
				t.Errorf("IsDemoLoginEmail = %v, want %v", got, c.wantEmail)
			}
			if got := c.svc.IsDemoLogin(c.email, c.otp); got != c.want {
				t.Errorf("IsDemoLogin = %v, want %v", got, c.want)
			}
		})
	}
}

// The config loader lowercases DEMO_LOGIN_EMAIL, and NormalizeEmail lowercases
// the request. A stored address that skipped the loader must not silently stop
// matching, which is what this pins.
func TestIsDemoLoginEmailRequiresNormalizedConfig(t *testing.T) {
	s := demoSvc("Play-Review@Qwish.IN", "314159")
	if s.IsDemoLoginEmail("play-review@qwish.in") {
		t.Fatal("unnormalized config matched; config.Load must lowercase DEMO_LOGIN_EMAIL")
	}
}
