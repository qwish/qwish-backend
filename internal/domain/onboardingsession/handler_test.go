package onboardingsession

import (
	"net/http/httptest"
	"testing"
)

func TestSessionBearer(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{name: "valid", header: "Bearer opaque-session-id", want: "opaque-session-id", ok: true},
		{name: "missing", ok: false},
		{name: "wrong scheme", header: "Basic abc", ok: false},
		{name: "empty", header: "Bearer   ", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/onboarding/session/recommendations", nil)
			req.Header.Set("Authorization", tt.header)
			got, ok := sessionBearer(req)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("sessionBearer() = (%q, %v), want (%q, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}
