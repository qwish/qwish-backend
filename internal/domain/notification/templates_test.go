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

// Every email uses the branded layout, including the two that used to be bare.
func TestPlainEmailsUseBrandedLayout(t *testing.T) {
	branded := emailLayout("x", "")
	marker := branded[:strings.Index(branded, "<body")]
	for name, html := range map[string]string{
		"app login declined": tmplAppLoginDenied(),
		"announcement":       AnnouncementEmailHTML("Exam week", "Body", strPtr("Open"), strPtr("https://app.qwish.in/notices")),
	} {
		if !strings.HasPrefix(html, marker) {
			t.Errorf("%s does not use emailLayout", name)
		}
	}
}

func TestAnnouncementEmailEscapesAndOnlyLinksHTTP(t *testing.T) {
	html := AnnouncementEmailHTML(`<b>T</b>`, `<i>B</i>`, strPtr(`<x>Go`), strPtr("https://qwish.in/a?b=1&c=2"))
	if strings.Contains(html, "<b>T</b>") || strings.Contains(html, "<i>B</i>") || strings.Contains(html, "<x>Go") {
		t.Fatal("announcement rendered unescaped input")
	}
	if !strings.Contains(html, "https://qwish.in/a?b=1&amp;c=2") {
		t.Fatal("announcement link missing or unescaped")
	}
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,x", "qwish.in/no-scheme"} {
		if out := AnnouncementEmailHTML("T", "B", strPtr("Go"), strPtr(bad)); strings.Contains(out, bad) {
			t.Errorf("announcement rendered a %q link", bad)
		}
	}
}

// The header shows the hosted brand logo, sized for email clients, with alt
// text so a client that blocks images still reads "Qwish".
func TestEmailLayoutUsesBrandLogo(t *testing.T) {
	html := emailLayout("pre", "<p>x</p>")
	for _, want := range []string{`src="https://qwish.in/logo.png"`, `alt="Qwish"`, `width="56"`, `height="56"`} {
		if !strings.Contains(html, want) {
			t.Errorf("layout missing %s", want)
		}
	}
}
