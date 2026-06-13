package notification

import (
	"fmt"
	"html"
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
        <!-- Header / wordmark -->
        <tr>
          <td align="center" style="padding:8px 0 24px 0;">
            <span style="font-family:%[3]s;font-size:26px;font-weight:700;letter-spacing:-0.5px;color:%[4]s;">Qwish<span style="color:%[5]s;">.</span></span>
          </td>
        </tr>
        <!-- Card -->
        <tr>
          <td style="background:%[6]s;border-radius:20px;box-shadow:rgba(0,0,0,0.04) 0px 2px 6px, rgba(0,0,0,0.06) 0px 4px 12px;padding:40px;font-family:%[3]s;color:%[4]s;">
%[7]s
          </td>
        </tr>
        <!-- Footer -->
        <tr>
          <td align="center" style="padding:24px 16px 8px 16px;font-family:%[3]s;font-size:12px;line-height:18px;color:%[8]s;">
            One score. Nationwide visibility.<br>
            &copy; %[9]d Qwish &nbsp;&middot;&nbsp; <a href="https://qwish.in" style="color:%[8]s;text-decoration:underline;">qwish.in</a><br>
            You received this email because you have a Qwish account.
          </td>
        </tr>
      </table>
    </td>
  </tr>
</table>
</body>
</html>`,
		preheader,    // 1
		colorPageBg,  // 2
		fontStack,    // 3
		colorText,    // 4
		colorPrimary, // 5
		colorCard,    // 6
		bodyHTML,     // 7
		colorMuted,   // 8
		time.Now().Year(),
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

func tmplAdminInvite(name, role, inviteLink string) string {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + esc(name)
	}
	body := heading("You’re invited to join Qwish as an Admin") +
		paragraph(greeting+",") +
		paragraph("You’ve been invited to join Qwish as a <strong>"+esc(role)+"</strong>.") +
		paragraph("Set up your account to access the Admin Dashboard.") +
		primaryButton("Accept invitation", inviteLink) +
		fallbackLink(inviteLink) +
		mutedNote("If you weren’t expecting this invitation, you can safely ignore this email.")
	return emailLayout("You’re invited to join Qwish as an Admin", body)
}

func tmplInstitutionApproval(instName, adminEmail, adminPassword, sCode, tCode string) string {
	body := heading("Welcome to Qwish! 🎉") +
		paragraph("Your institution <strong>"+esc(instName)+"</strong> has been approved.") +
		paragraph("Here are your admin credentials and referral codes:") +
		credentialBox([][2]string{
			{"Admin email", esc(adminEmail)},
			{"Temporary password", esc(adminPassword)},
			{"Student code", esc(sCode)},
			{"Teacher code", esc(tCode)},
		}) +
		primaryButton("Go to dashboard", "https://qwish.in") +
		mutedNote("For security, please change your temporary password right after your first login.")
	return emailLayout("Your Qwish institution has been approved", body)
}

func tmplInstitutionRejection(instName, reason string) string {
	body := heading("Application update") +
		paragraph("We’re sorry — your application for <strong>"+esc(instName)+"</strong> wasn’t approved.") +
		credentialBox([][2]string{{"Reason", esc(reason)}}) +
		paragraph("You’re welcome to reapply once the above has been addressed.")
	return emailLayout("Update on your Qwish institution application", body)
}

func tmplPasswordReset(resetLink string, expiryMinutes int) string {
	body := heading("Reset your password") +
		paragraph(fmt.Sprintf("Tap the button below to choose a new password. This link expires in %d minutes.", expiryMinutes)) +
		primaryButton("Reset password", resetLink) +
		fallbackLink(resetLink) +
		mutedNote("If you didn’t request a password reset, you can safely ignore this email.")
	return emailLayout("Reset your Qwish password", body)
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

func tmplAdminWelcome(name, role string) string {
	greeting := "Hi"
	if name != "" {
		greeting = "Hi " + esc(name)
	}
	body := heading("Welcome to Qwish Admins!") +
		paragraph(greeting+",") +
		paragraph("You’ve been granted <strong>"+esc(role)+"</strong> privileges on Qwish.") +
		paragraph("You can now sign in with your existing credentials to access the Admin Dashboard.") +
		primaryButton("Open dashboard", "https://qwish.in")
	return emailLayout("Your Qwish admin access is ready", body)
}
