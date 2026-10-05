package notification

import (
	"strings"
	"testing"
)

func TestUserWelcomeTemplateEscapesNameAndIncludesAppURL(t *testing.T) {
	const appURL = "https://app.qwish.in"
	html := tmplUserWelcome(`<script>alert("x")</script>`, appURL)

	if strings.Contains(html, "<script>") {
		t.Fatal("welcome template rendered unescaped user input")
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("welcome template did not include the escaped name")
	}
	if !strings.Contains(html, appURL) {
		t.Fatal("welcome template did not include the app URL")
	}
}

func TestAccountInviteSaysEmailCodeAndEscapes(t *testing.T) {
	const login = "https://teacher.qwish.in"
	html := tmplAccountInvite(`<b>Asha</b>`, "a teacher", `St. <Mary's>`, login)
	if strings.Contains(html, "<b>Asha</b>") || strings.Contains(html, "<Mary's>") {
		t.Fatal("invite rendered unescaped input")
	}
	if !strings.Contains(html, login) || !strings.Contains(html, "one-time code") {
		t.Fatal("invite must link the sign-in page and explain the email code")
	}
	if strings.Contains(strings.ToLower(html), "password") {
		t.Fatal("invite must not mention a password; sign-in is email + code")
	}
}
