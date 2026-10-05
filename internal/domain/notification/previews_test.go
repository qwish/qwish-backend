package notification

import (
	"html"
	"os"
	"strings"
	"testing"
)

// emailPreview is one email Qwish sends: its subject, when it goes out, and
// the HTML rendered by the same template the sender uses.
type emailPreview struct{ name, subject, trigger, html string }

func strPtr(s string) *string { return &s }

// emailPreviews lists every email the backend sends, with sample data. Add a
// row here whenever a template is added, so the preview page stays complete.
func emailPreviews() []emailPreview {
	const teacherURL, instituteURL, appURL = "https://teacher.qwish.in", "https://institute.qwish.in", "https://app.qwish.in"
	return []emailPreview{
		{"Login code", "Your Qwish verification code", "Someone requests a sign-in code (app and panels)",
			tmplLoginOTP("428913", 10)},
		{"Welcome", "Welcome to Qwish", "A student finishes creating their profile",
			tmplUserWelcome("Aarav Shah", appURL)},
		{"Account invite — institute admin", "You’re invited to Green Valley School on Qwish", "Super admin adds an institution, or provisions / approves one",
			tmplAccountInvite("Ravi Iyer", "the institute admin", "Green Valley School", instituteURL)},
		{"Account invite — teacher", "You’re invited to Green Valley School on Qwish", "Institute admin adds a teacher directly",
			tmplAccountInvite("Meera Nair", "a teacher", "Green Valley School", teacherURL)},
		{"Account invite — Qwish staff", "You’re invited to Qwish on Qwish", "Super admin invites a moderator, support agent or super admin",
			tmplAccountInvite("Kabir Rao", "a moderator", "Qwish", "https://admin.qwish.in")},
		{"Teacher invite link (older flow)", "You're invited to teach on Qwish", "Institute admin sends a link invite (Teachers → invite API)",
			tmplTeacherInvite("Meera Nair", "Green Valley School", teacherURL+"/auth/teacher-signup?token=…")},
		{"Teacher verified", "Your Qwish teacher account is verified", "Institute admin verifies a teacher who joined with a code or link",
			tmplTeacherVerified("Meera Nair", "Green Valley School", teacherURL)},
		{"Institution invite", "Bring Qwish to your institution", "Super admin replies to a contact form with the application link",
			tmplInstitutionInvite("Priya Menon", "https://qwish.in/institutions/apply")},
		{"Institution application rejected", "Qwish Institution Application Update", "Super admin rejects an institution application",
			tmplInstitutionRejection("Sunrise Tutorials", "We couldn’t verify the institution’s registration details. Please reapply with your registration certificate.")},
		{"Weekly insights", "Your weekly Qwish insights", "Weekly digest to students (scheduler)",
			tmplWeeklyInsights("Aarav Shah", 1240, "+18% vs last week", 9, 76.5, 5, "Mathematics", "Try a fractions practice quiz to lock in this week’s gains.")},
		{"Teacher notification — overdue work", "3 students have overdue work in 9A", "Teacher notification topics with email on (overdue work, follow-up evidence, decisions…)",
			tmplTeacherNotice("3 students have overdue work in 9A", "Fractions check was due yesterday. Open the class to see who hasn’t submitted and send a reminder.", teacherURL+"/classes/detail?id=…&tab=assignments")},
		{"App login declined", "Your Qwish app login was declined", "A staff or teacher account signs in to the student app",
			tmplAppLoginDenied()},
		{"Announcement", "Exam week timetable", "Super admin schedules an announcement with the email channel",
			AnnouncementEmailHTML("Exam week timetable", "Mid-term assessments run 14–18 October. Check your assigned quizzes for the schedule.", strPtr("View timetable"), strPtr("https://app.qwish.in/notices"))},
	}
}

// Every template renders, so a broken template fails the suite rather than the preview.
func TestEmailPreviewsRender(t *testing.T) {
	for _, p := range emailPreviews() {
		if strings.TrimSpace(p.html) == "" {
			t.Errorf("%s rendered empty", p.name)
		}
	}
}

// Writes the preview page when EMAIL_PREVIEWS_OUT is set:
//
//	EMAIL_PREVIEWS_OUT=$PWD/email_previews.html go test ./internal/domain/notification -run WriteEmailPreviews
func TestWriteEmailPreviews(t *testing.T) {
	out := os.Getenv("EMAIL_PREVIEWS_OUT")
	if out == "" {
		t.Skip("EMAIL_PREVIEWS_OUT not set")
	}
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Qwish email previews</title>
<style>
  body { margin: 0; background: #e9e9e9; font-family: -apple-system, system-ui, sans-serif; color: #222 }
  header { max-width: 680px; margin: 0 auto; padding: 28px 16px 8px }
  header h1 { margin: 0 0 6px; font-size: 22px }
  header p { margin: 0; font-size: 13px; color: #555 }
  nav { max-width: 680px; margin: 0 auto; padding: 8px 16px 20px; display: flex; flex-wrap: wrap; gap: 6px }
  nav a { font-size: 12px; background: #fff; border: 1px solid #ccc; border-radius: 999px; padding: 4px 10px; color: #222; text-decoration: none }
  .wrap { max-width: 680px; margin: 0 auto 28px; box-shadow: 0 4px 16px rgba(0,0,0,.15); background: #fff }
  .label { position: sticky; top: 0; background: #222; color: #fff; padding: 10px 16px; z-index: 2 }
  .label b { font-size: 13px }
  .label span { display: block; font-size: 12px; color: #bbb; margin-top: 2px }
  iframe { width: 100%; height: 760px; border: 0; display: block; background: #fff }
</style>
</head>
<body>
<header>
  <h1>Qwish email previews</h1>
  <p>Generated from the backend templates (internal/domain/notification). Sample data only. Regenerate:
  <code>EMAIL_PREVIEWS_OUT=$PWD/email_previews.html go test ./internal/domain/notification -run WriteEmailPreviews</code></p>
</header>
<nav>`)
	previews := emailPreviews()
	for i, p := range previews {
		b.WriteString(`<a href="#e` + string(rune('a'+i)) + `">` + html.EscapeString(p.name) + `</a>`)
	}
	b.WriteString("</nav>\n")
	for i, p := range previews {
		b.WriteString(`<div class="wrap" id="e` + string(rune('a'+i)) + `"><div class="label"><b>` + html.EscapeString(p.name) +
			`</b><span>Subject: ` + html.EscapeString(p.subject) + ` · ` + html.EscapeString(p.trigger) + `</span></div><iframe` +
			` title="` + html.EscapeString(p.name) + `" srcdoc="` + html.EscapeString(p.html) + `"></iframe></div>` + "\n")
	}
	b.WriteString("</body>\n</html>\n")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d previews to %s", len(previews), out)
}
