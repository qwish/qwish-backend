package admin

import "testing"

func TestValidContentURL(t *testing.T) {
	https := "https://qwish.in/campaign"
	path := "/quizzes"
	javascript := "javascript:alert(1)"
	protocolRelative := "//evil.example/path"
	for _, tt := range []struct {
		name  string
		value *string
		want  bool
	}{
		{"empty", nil, true},
		{"https", &https, true},
		{"app path", &path, true},
		{"javascript", &javascript, false},
		{"protocol relative", &protocolRelative, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := validContentURL(tt.value); got != tt.want {
				t.Fatalf("validContentURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestContentEnums(t *testing.T) {
	if !allIn([]string{"in_app_banner", "email"}, map[string]bool{"in_app_banner": true, "email": true}) {
		t.Fatal("valid delivery channels rejected")
	}
	if allIn([]string{"email", "webhook"}, map[string]bool{"email": true}) {
		t.Fatal("unknown delivery channel accepted")
	}
}
