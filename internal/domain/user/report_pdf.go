package user

import (
	"bytes"
	"fmt"
	"strings"
)

type reportLine struct {
	text    string
	heading bool
}

func learningReportLines(r *LearningReport) []reportLine {
	var lines []reportLine
	add := func(s string) { lines = append(lines, reportLine{text: s}) }
	heading := func(s string) { lines = append(lines, reportLine{text: s, heading: true}) }
	heading("QWISH / LEARNING JOURNEY")
	add(r.Name)
	add("Generated " + r.Generated.UTC().Format("02 Jan 2006, 15:04 UTC") + " | Method v1")
	add("Qwish member since " + r.MemberSince.Format("02 Jan 2006"))
	heading("01 / Evidence at a glance")
	add(fmt.Sprintf("%d distinct assessments | %d questions | %d active assessment days (UTC)", r.Assessments, r.Questions, r.ActiveDays))
	add(reportAccuracy(r.Correct, r.Questions))
	add(fmt.Sprintf("Current streak: %d days | Longest streak: %d days", r.Streak, r.LongestStreak))
	if r.FirstEvidence != nil && r.LastEvidence != nil {
		add("Evidence window: " + r.FirstEvidence.Format("02 Jan 2006") + " to " + r.LastEvidence.Format("02 Jan 2006"))
	} else {
		add("No completed, scored assessments are recorded yet. This is not a low score.")
	}
	heading("02 / Education journey & current study")
	if len(r.Stages) == 0 {
		add("No institution-managed enrollment history is linked to this account.")
	}
	for _, s := range r.Stages {
		end := "present"
		if s.End != nil {
			end = s.End.Format("02 Jan 2006")
		}
		heading(s.Institution + " | " + s.Grade)
		add(s.Start.Format("02 Jan 2006") + " to " + end + " | Institution record")
		if s.End == nil {
			add("Current enrollment status: " + s.Status)
		}
		add(fmt.Sprintf("%d institution assessments | %s", s.Assessments, reportAccuracy(s.Correct, s.Questions)))
	}
	add("Stage results cover Qwish assessments from that institution during the recorded period. Public practice is included in overall/domain results, not assigned to a school stage.")
	add("History follows recorded promotions. Direct grade edits and periods before Qwish may be incomplete. These are practice outcomes, not academic marks or degree verification.")
	for _, e := range r.Education {
		heading(e.InstitutionName + " | Self-reported education")
		add(strings.TrimSpace(e.Degree + " " + e.Field))
		start, end := "unknown", "unknown"
		if e.StartYear != nil {
			start = fmt.Sprint(*e.StartYear)
		}
		if e.EndYear != nil {
			end = fmt.Sprint(*e.EndYear)
		}
		if e.IsCurrent {
			end = "currently pursuing (self-reported)"
		}
		add(start + " to " + end)
		add("No verified marks or assessment-to-course link is recorded for this entry.")
	}
	heading("03 / Current engagement & recent progress")
	add(fmt.Sprintf("Assignments: %d submitted / %d available non-excused | %d past due and unsubmitted", r.Submitted, r.Assigned, r.Overdue))
	if r.Assigned == 0 {
		add("No available assignments are recorded; course completion cannot be estimated.")
	}
	if r.RecentAccuracy != nil {
		add(fmt.Sprintf("Last 30 days: %.1f%% accuracy across %d first attempts", *r.RecentAccuracy, r.RecentCount))
	} else {
		add("Last 30 days: no new scored assessments.")
	}
	if r.PreviousAccuracy != nil {
		add(fmt.Sprintf("Previous 30 days: %.1f%% accuracy across %d first attempts", *r.PreviousAccuracy, r.PreviousCount))
	}
	if r.RecentCount >= 3 && r.PreviousCount >= 3 && r.RecentAccuracy != nil && r.PreviousAccuracy != nil {
		add(fmt.Sprintf("Observed change: %+.1f percentage points. Assessment mix may differ; this is not a measured change in ability.", *r.RecentAccuracy-*r.PreviousAccuracy))
	} else {
		add("Trend withheld until each 30-day period contains at least 3 scored assessments.")
	}
	heading("04 / Domain strengths & peer context")
	if len(r.Domains) == 0 {
		add("Complete assessments across your learning domains to build this section.")
	}
	for _, d := range r.Domains {
		heading(strings.ReplaceAll(d.Name, "_", " "))
		add(fmt.Sprintf("%s | %d assessments", reportAccuracy(d.Correct, d.Questions), d.Assessments))
		evidence := "Early evidence - fewer than 3 assessments or 20 questions."
		if d.Assessments >= 3 && d.Questions >= 20 {
			evidence = "Developing - keep practising this domain."
			if float64(d.Correct)/float64(d.Questions) >= 0.8 {
				evidence = "Observed strength - at least 80% accuracy on this assessment set."
			} else if float64(d.Correct)/float64(d.Questions) < 0.6 {
				evidence = "Review priority - below 60% accuracy on this assessment set."
			}
		}
		add(evidence)
		if d.PeerStanding != nil {
			add(fmt.Sprintf("Mean same-assessment standing: scored above %.1f%% of peers; %d comparable assessments, at least %d other students per assessment.", *d.PeerStanding, d.PeerAssessments, d.PeerCount))
		} else {
			add("Peer comparison unavailable: requires 3 assessments with at least 5 other active students each.")
		}
	}
	heading("05 / Recommended next steps")
	if r.Assessments < 3 {
		add("Build a baseline: complete at least 3 different assessments in your current learning domain.")
	}
	if r.Overdue > 0 {
		add(fmt.Sprintf("Review your %d overdue assignments with your teacher.", r.Overdue))
	}
	if len(r.Domains) > 0 {
		d := r.Domains[len(r.Domains)-1]
		add("Start your next review with " + strings.ReplaceAll(d.Name, "_", " ") + ", your lowest observed domain accuracy. Review missed concepts before trying new assessments.")
	}
	if len(r.Stages) == 0 && len(r.Education) == 0 {
		add("Add your current education and link your institution to give future evidence its learning context.")
	}
	add("Use this report with teacher feedback and official academic records; it covers only learning recorded on Qwish.")
	heading("06 / How to read and trust this report")
	add("Source: Qwish completed assessment records, institution-managed enrollments and promotions, assignment records, and separately labelled self-reported education. All sections use one database snapshot.")
	add("Accuracy = correct answers / total questions in first completed scored attempts. Unanswered questions remain in the denominator. Retakes do not replace first scores. Active assessment days count these first attempts only.")
	add("Domain standing averages the percentage of other active students with strictly lower first-attempt accuracy on the same quiz ID. Ties are not counted as lower. Sampled questions and revisions can differ. Cohorts are all-time as of generation, not a national or age-matched sample. No other student's identity is exported.")
	add("Small-sample thresholds are reporting safeguards, not confidence intervals. Assessments may differ in difficulty and coverage; domain labels are descriptive, not certifications. This report does not infer intelligence, employability, or overall academic performance.")
	add("This export is a dated summary, not a digitally signed credential. Qwish points and Qwish Score are not accuracy and are not used as substitutes for academic results.")
	return lines
}

// PDF text is hex encoded, never interpolated as executable PDF operators.
// Courier provides deterministic line widths; objects and xref offsets are
// calculated from actual bytes rather than hard-coded placeholder offsets.
func reportPDFText(s string) string {
	var b []byte
	for _, r := range s {
		if r >= 32 && r <= 126 {
			b = append(b, byte(r))
		} else if r >= 160 && r <= 255 {
			b = append(b, byte(r))
		} else {
			b = append(b, '?')
		}
	}
	return fmt.Sprintf("<%X>", b)
}

func renderLearningReport(r *LearningReport) []byte {
	var pages []string
	var page strings.Builder
	y := 780
	newPage := func() {
		if page.Len() > 0 {
			pages = append(pages, page.String())
			page.Reset()
		}
		y = 780
	}
	for _, line := range learningReportLines(r) {
		size, step := 10, 15
		if line.heading {
			size, step = 12, 19
			if y < 110 {
				newPage()
			}
			y -= 12
		}
		width := 82
		if line.heading {
			width = 68
		}
		// Word wrap, including unbroken user-supplied names longer than a line.
		var wrapped []string
		current := ""
		for _, word := range strings.Fields(line.text) {
			runes := []rune(word)
			for len(runes) > width {
				if current != "" {
					wrapped = append(wrapped, current)
					current = ""
				}
				wrapped = append(wrapped, string(runes[:width]))
				runes = runes[width:]
			}
			word = string(runes)
			if len([]rune(current))+len(runes)+1 > width {
				wrapped = append(wrapped, current)
				current = ""
			}
			if current != "" {
				current += " "
			}
			current += word
		}
		if current != "" {
			wrapped = append(wrapped, current)
		}
		for _, text := range wrapped {
			if y < 65 {
				newPage()
			}
			font := "F1"
			if line.heading {
				font = "F2"
			}
			fmt.Fprintf(&page, "BT /%s %d Tf 0.12 0.16 0.20 rg 1 0 0 1 48 %d Tm %s Tj ET\n", font, size, y, reportPDFText(text))
			y -= step
		}
		y -= 5
	}
	if page.Len() > 0 {
		pages = append(pages, page.String())
	}
	objects := []string{"", "", "<< /Type /Font /Subtype /Type1 /BaseFont /Courier /Encoding /WinAnsiEncoding >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Courier-Bold /Encoding /WinAnsiEncoding >>"}
	var kids []string
	for i, content := range pages {
		pageID := len(objects) + 1
		streamID := pageID + 1
		kids = append(kids, fmt.Sprintf("%d 0 R", pageID))
		content += fmt.Sprintf("BT /F1 8 Tf 0.4 0.4 0.4 rg 1 0 0 1 48 30 Tm %s Tj ET\n", reportPDFText(fmt.Sprintf("Qwish learning report | %s | Page %d of %d", r.Generated.UTC().Format("2006-01-02"), i+1, len(pages))))
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>", streamID), fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
	}
	objects[0] = "<< /Type /Catalog /Pages 2 0 R >>"
	objects[1] = fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages))
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, obj := range objects {
		offsets = append(offsets, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	start := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, o := range offsets[1:] {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), start)
	return out.Bytes()
}
