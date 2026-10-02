package auth

import "testing"

func TestIsDisposableEmail(t *testing.T) {
	cases := map[string]bool{
		"a@mailinator.com":       true,
		"A@MAILINATOR.COM ":      true,
		"a@inbox.mailinator.com": true,
		"a@gmail.com":            false,
		"a@school.edu":           false,
		"":                       false,
	}
	for email, want := range cases {
		if got := IsDisposableEmail(email); got != want {
			t.Errorf("IsDisposableEmail(%q) = %v, want %v", email, got, want)
		}
	}
}
