package notification

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Branded email templates
//
// All Qwish transactional emails share one layout so they render consistently
// across clients (Gmail, Outlook, Apple Mail). The design follows the Qwish
// brand system (see qwish-brand-website/DESIGN.md): warm, Airbnb-inspired —
// Rausch-red primary CTA, indigo data accent, soft surfaces, rounded shapes.
//
// Email HTML rules followed here:
//   - Table-based layout (Outlook ignores most modern CSS / flexbox / grid).
//   - Inline styles only; no <style> blocks or external CSS.
//   - 600px max content width; renders single-column on mobile.
//   - All caller-supplied values are HTML-escaped via esc() to prevent markup
//     injection into the email body.
// ─────────────────────────────────────────────────────────────────────────────

const (
	colorPrimary = "#FF385C" // Rausch Red — primary CTA + highlights
	colorIndigo  = "#6C63FF" // Indigo — data accents (OTP, percentile)
	colorText    = "#222222" // primary text (not pure black)
	colorMuted   = "#6A6A6A" // secondary text
	colorPageBg  = "#F7F7F7" // warm off-white page background
	colorCard    = "#FFFFFF" // card surface
	colorBorder  = "#EBEBEB" // hairline dividers

	fontStack = "Circular, -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, Inter, Roboto, Helvetica, Arial, sans-serif"
)

// esc HTML-escapes a caller-supplied string for safe interpolation into email
// markup. Use for every dynamic value that originates from user input.
func esc(s string) string { return html.EscapeString(s) }

// logoURL is the hosted brand mark (512×512 PNG), shown at 56×56.
const logoURL = "https://qwish.in/logo.png"

// emailLayout wraps body content in the shared branded shell. preheader is the
// short snippet shown in inbox previews (hidden in the body). bodyHTML is the
// inner content, already escaped where it contains dynamic values.
func emailLayout(preheader, bodyHTML string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta name="color-scheme" content="light">
<title>Qwish</title>
</head>
<body style="margin:0;padding:0;background:%[2]s;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:%[2]s;font-size:1px;line-height:1px;">%[1]s</div>
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background:%[2]s;">
  <tr>
    <td align="center" style="padding:32px 16px;">
      <table role="presentation" width="600" cellpadding="0" cellspacing="0" style="width:100%%;max-width:600px;">
        <!-- Header / logo. Alt text is styled as the wordmark for clients that block images. -->
        <tr>
          <td align="center" style="padding:8px 0 24px 0;">
            <a href="https://qwish.in" target="_blank" style="text-decoration:none;"><img src="%[9]s" width="56" height="56" alt="Qwish" style="display:block;width:56px;height:56px;border:0;outline:none;text-decoration:none;border-radius:12px;font-family:%[3]s;font-size:26px;font-weight:700;color:%[4]s;"></a>
          </td>
        </tr>
        <!-- Card -->
        <tr>
          <td style="background:%[5]s;border-radius:20px;box-shadow:rgba(0,0,0,0.04) 0px 2px 6px, rgba(0,0,0,0.06) 0px 4px 12px;padding:40px;font-family:%[3]s;color:%[4]s;">
%[6]s
          </td>
        </tr>
        <!-- Footer -->
        <tr>
          <td align="center" style="padding:24px 16px 8px 16px;font-family:%[3]s;font-size:12px;line-height:18px;color:%[7]s;">
            One score. Nationwide visibility.<br>
            &copy; %[8]d Qwish &nbsp;&middot;&nbsp; <a href="https://qwish.in" style="color:%[7]s;text-decoration:underline;">qwish.in</a><br>
            You received this email because you have a Qwish account.
          </td>
        </tr>
      </table>
    </td>
  </tr>
</table>
</body>
</html>`,
		preheader,         // 1
		colorPageBg,       // 2
		fontStack,         // 3
		colorText,         // 4
		colorCard,         // 5
		bodyHTML,          // 6
		colorMuted,        // 7
		time.Now().Year(), // 8
		logoURL,           // 9
	)
}

// ── Reusable content components ───────────────────────────────────────────────

// heading renders an h1-style title. text must be pre-escaped if dynamic.
func heading(text string) string {
	return fmt.Sprintf(
		`<h1 style="margin:0 0 16px 0;font-size:24px;line-height:30px;font-weight:700;letter-spacing:-0.4px;color:%s;">%s</h1>`,
		colorText, text)
}

// paragraph renders a body paragraph. text must be pre-escaped if dynamic.
func paragraph(text string) string {
	return fmt.Sprintf(
		`<p style="margin:0 0 16px 0;font-size:16px;line-height:24px;color:%s;">%s</p>`,
		colorText, text)
}

// mutedNote renders small secondary text (e.g. "ignore if you didn't expect this").
func mutedNote(text string) string {
	return fmt.Sprintf(
		`<p style="margin:16px 0 0 0;font-size:13px;line-height:20px;color:%s;">%s</p>`,
		colorMuted, text)
}

// primaryButton renders a bulletproof, centered CTA button. href is emitted raw
// (callers build trusted links); label must be pre-escaped if dynamic.
func primaryButton(label, href string) string {
	return fmt.Sprintf(`
<table role="presentation" cellpadding="0" cellspacing="0" style="margin:28px 0;">
  <tr>
    <td align="center" bgcolor="%s" style="border-radius:8px;">
      <a href="%s" target="_blank" style="display:inline-block;padding:14px 32px;font-family:%s;font-size:16px;font-weight:600;color:#ffffff;text-decoration:none;border-radius:8px;">%s</a>
    </td>
  </tr>
</table>`, colorPrimary, href, fontStack, label)
}

// fallbackLink shows the raw URL under a button for clients that strip buttons.
func fallbackLink(href string) string {
	return fmt.Sprintf(
		`<p style="margin:0 0 8px 0;font-size:13px;line-height:20px;color:%s;">Or paste this link into your browser:<br><a href="%s" style="color:%s;word-break:break-all;">%s</a></p>`,
		colorMuted, href, colorIndigo, href)
}

// otpCode renders a large, spaced verification code in the indigo data accent.
func otpCode(code string) string {
	return fmt.Sprintf(`
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="margin:24px 0;">
  <tr>
    <td align="center" style="background:#F4F3FF;border:1px solid #E5E2FF;border-radius:16px;padding:24px;">
      <div style="font-family:%s;font-size:38px;font-weight:700;letter-spacing:10px;color:%s;">%s</div>
    </td>
  </tr>
</table>`, fontStack, colorIndigo, code)
}

// statRows renders a borderless key/value table. Each row is {label, value};
// values are emitted as-is, so escape dynamic values before passing them in.
func statRows(rows [][2]string) string {
	var b strings.Builder
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="margin:8px 0 4px 0;font-size:15px;">`)
	for _, r := range rows {
		b.WriteString(fmt.Sprintf(
			`<tr><td style="padding:8px 0;color:%s;border-bottom:1px solid %s;">%s</td><td style="padding:8px 0;text-align:right;color:%s;border-bottom:1px solid %s;"><strong>%s</strong></td></tr>`,
			colorMuted, colorBorder, r[0], colorText, colorBorder, r[1]))
	}
	b.WriteString(`</table>`)
	return b.String()
}

// credentialBox highlights sensitive setup info (temp passwords, codes).
func credentialBox(rows [][2]string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="margin:20px 0;background:%s;border-radius:16px;">`, colorPageBg))
	b.WriteString(`<tr><td style="padding:20px 24px;">`)
	b.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="font-size:15px;">`)
	for _, r := range rows {
		b.WriteString(fmt.Sprintf(
			`<tr><td style="padding:6px 0;color:%s;">%s</td><td style="padding:6px 0;text-align:right;color:%s;"><strong>%s</strong></td></tr>`,
			colorMuted, r[0], colorText, r[1]))
	}
	b.WriteString(`</table></td></tr></table>`)
	return b.String()
}

// tipBox renders a soft callout (e.g. "what to do next").
func tipBox(text string) string {
	return fmt.Sprintf(
		`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="margin:20px 0 0 0;"><tr><td style="background:#F4F3FF;border-radius:12px;padding:16px 20px;font-size:15px;line-height:22px;color:%s;">💡 <strong>What to do next:</strong> %s</td></tr></table>`,
		colorText, text)
}

// ── Per-email builders (return ready-to-send HTML) ────────────────────────────
//
// Each builder escapes dynamic values internally, so callers pass plain strings.

func tmplLoginOTP(code string, expiryMinutes int) string {
	body := heading("Verify your email") +
		paragraph("Use the code below to sign in to Qwish. It’s valid for "+fmt.Sprintf("%d", expiryMinutes)+" minutes.") +
		otpCode(esc(code)) +
		mutedNote("If you didn’t try to sign in, you can safely ignore this email — your account stays secure.")
	return emailLayout("Your Qwish verification code is "+esc(code), body)
}

func tmplUserWelcome(name, appURL string) string {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + esc(name)
	}
	body := heading("Welcome to Qwish! 🎉") +
		paragraph(greeting+",") +
		paragraph("Your account is ready. Discover quizzes, build your streak, and turn every answer into progress you can see.") +
		primaryButton("Start exploring", appURL) +
		fallbackLink(appURL) +
		mutedNote("We’re glad you’re here. Let’s make your next answer count.")
	return emailLayout("Your Qwish account is ready", body)
}

func tmplTeacherInvite(name, instName, inviteLink string) string {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + esc(name)
	}
	body := heading("You’re invited to teach on Qwish") +
		paragraph(greeting+",") +
		paragraph("<strong>"+esc(instName)+"</strong> has invited you to join their institution on Qwish as a teacher.") +
		paragraph("Set up your account to start authoring quizzes and tracking your students. This invite expires in 7 days.") +
		primaryButton("Accept invitation", inviteLink) +
		fallbackLink(inviteLink) +
		mutedNote("If you weren’t expecting this invitation, you can safely ignore this email.")
	return emailLayout(esc(instName)+" invited you to teach on Qwish", body)
}

func tmplTeacherVerified(name, instName, loginURL string) string {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + esc(name)
	}
	body := heading("You’re verified — welcome to Qwish") +
		paragraph(greeting+",") +
		paragraph("<strong>"+esc(instName)+"</strong> has verified your teacher account. You can now sign in and start authoring quizzes and tracking your students.") +
		paragraph("Sign in with your email — we’ll send you a one-time code (OTP) each time.") +
		primaryButton("Sign in to Qwish", loginURL) +
		fallbackLink(loginURL) +
		mutedNote("If you weren’t expecting this, you can safely ignore this email.")
	return emailLayout(esc(instName)+" verified your Qwish teacher account", body)
}

// tmplAccountInvite tells someone an account was created for them. There is
// no password: they sign in with their email and a one-time code.
func tmplAccountInvite(name, roleLabel, instName, loginURL string) string {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + esc(name)
	}
	body := heading("You’re invited to Qwish") +
		paragraph(greeting+",") +
		paragraph("<strong>"+esc(instName)+"</strong> has added you to Qwish as "+esc(roleLabel)+".") +
		paragraph("Sign in with this email address — we’ll send you a one-time code each time you sign in.") +
		primaryButton("Sign in to Qwish", loginURL) +
		fallbackLink(loginURL) +
		mutedNote("If you weren’t expecting this, you can safely ignore this email.")
	return emailLayout(esc(instName)+" invited you to Qwish", body)
}

func tmplInstitutionInvite(name, applyLink string) string {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + esc(name)
	}
	body := heading("Bring Qwish to your institution") +
		paragraph(greeting+", thanks for your interest in Qwish — we’d love to have your institution on board.") +
		paragraph("To get started, complete our short institution application. Once you submit, our team reviews the details and provisions your dashboard credentials.") +
		primaryButton("Apply to join", applyLink) +
		fallbackLink(applyLink) +
		mutedNote("If you didn’t request this, you can safely ignore this email.")
	return emailLayout("Bring Qwish to your institution — apply now", body)
}

func tmplInstitutionRejection(instName, reason string) string {
	body := heading("Application update") +
		paragraph("We’re sorry — your application for <strong>"+esc(instName)+"</strong> wasn’t approved.") +
		credentialBox([][2]string{{"Reason", esc(reason)}}) +
		paragraph("You’re welcome to reapply once the above has been addressed.")
	return emailLayout("Update on your Qwish institution application", body)
}

func tmplWeeklyInsights(name string, pointsThisWeek int64, trend string, quizzes int, avgScore float64, streak int, domain, suggestion string) string {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + esc(name)
	}
	rows := [][2]string{
		{"Points earned", fmt.Sprintf(`%d <span style="color:%s">(%s)</span>`, pointsThisWeek, colorMuted, esc(trend))},
		{"Quizzes completed", fmt.Sprintf("%d", quizzes)},
		{"Average score", fmt.Sprintf("%.0f%%", avgScore)},
		{"Current streak", fmt.Sprintf("%d days", streak)},
	}
	if domain != "" {
		rows = append(rows, [2]string{"Top domain", esc(domain)})
	}
	body := heading("Your week on Qwish 📊") +
		paragraph(greeting+", here’s how your week went:") +
		statRows(rows) +
		tipBox(esc(suggestion))
	return emailLayout("Your weekly Qwish insights are ready", body)
}

// tmplTeacherNotice is the email copy of a teacher notification (overdue work,
// topic requests, follow-up evidence, decisions).
func tmplTeacherNotice(title, body, link string) string {
	content := heading(title) + paragraph(body)
	if link != "" {
		content += primaryButton("Open Qwish", link)
	}
	return emailLayout(body, content)
}

// tmplAppLoginDenied is sent when a staff or teacher account tries the student
// app.
func tmplAppLoginDenied() string {
	body := heading("This account can’t sign in to the Qwish app") +
		paragraph("Someone just signed in to the Qwish student app with this email, and the sign-in was declined.") +
		paragraph("Administrator and teacher accounts work on the web instead: use your institute dashboard, teacher panel or admin console with this same email.") +
		mutedNote("If this wasn’t you, you can safely ignore this email — nothing on your account changed.")
	return emailLayout("Your Qwish app sign-in was declined", body)
}

// AnnouncementEmailHTML is a scheduled announcement's email. Title, body and
// call to action come from a super admin, so all are escaped, and the link is
// rendered only for an absolute http(s) URL.
func AnnouncementEmailHTML(title, body string, ctaLabel, ctaURL *string) string {
	content := heading(esc(title)) + paragraph(strings.ReplaceAll(esc(body), "\n", "<br>"))
	if ctaLabel != nil && ctaURL != nil && *ctaLabel != "" {
		if u, err := url.Parse(*ctaURL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			content += primaryButton(esc(*ctaLabel), esc(*ctaURL)) + fallbackLink(esc(*ctaURL))
		}
	}
	return emailLayout(esc(title), content)
}
