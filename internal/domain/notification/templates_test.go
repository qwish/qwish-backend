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
