package user

import (
	"strings"
	"testing"
	"time"
)

func TestValidKind(t *testing.T) {
	for _, k := range []string{"experience", "certification", "achievement", "course"} {
		if !validKind(k) {
			t.Fatalf("validKind(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"", "education", "skill", "EXPERIENCE"} {
		if validKind(k) {
			t.Fatalf("validKind(%q) = true, want false", k)
		}
	}
}

func strp(s string) *string { return &s }

func TestApplyValidatesPortfolioEntries(t *testing.T) {
	ongoing := true
	tests := []struct {
		name    string
		in      profileEntryInput
		wantErr string
	}{
		{"minimal draft needs only title and subtype", profileEntryInput{Subtype: strp("project"), Title: "Robot"}, ""},
		{"legacy kind still accepted", profileEntryInput{Kind: "course", Title: "Algebra"}, ""},
		{"unknown subtype", profileEntryInput{Subtype: strp("hobby"), Title: "x"}, "subtype"},
		{"blank title", profileEntryInput{Kind: "course", Title: "  "}, "title is required"},
		{"end before start", profileEntryInput{Kind: "course", Title: "x", StartDate: strp("2025-02-01"), EndDate: strp("2025-01-01")}, "end_date"},
		{"ongoing with end date", profileEntryInput{Subtype: strp("internship"), Title: "x", EndDate: strp("2025-01-01"), Ongoing: &ongoing}, "ongoing"},
		{"detail key from another subtype", profileEntryInput{Subtype: strp("project"), Title: "x", Details: &map[string]string{"work_mode": "remote"}}, "details.work_mode"},
		{"closed-set value enforced", profileEntryInput{Subtype: strp("hackathon"), Title: "x", Details: &map[string]string{"result": "champion"}}, "unsupported"},
		{"details without subtype", profileEntryInput{Kind: "course", Title: "x", Details: &map[string]string{"role": "a"}}, "subtype"},
		{"non-http link", profileEntryInput{Kind: "course", Title: "x", Links: &[]string{"javascript:alert(1)"}}, "links"},
		{"bad academic year", profileEntryInput{Kind: "course", Title: "x", AcademicYear: strp("2025")}, "academic_year"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var e profileEntry
			err := tt.in.apply(&e, true)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestApplySubtypeDerivesLegacyKind(t *testing.T) {
	var e profileEntry
	if err := (&profileEntryInput{Subtype: strp("hackathon"), Title: "SIH"}).apply(&e, true); err != nil {
		t.Fatal(err)
	}
	if e.Kind != "achievement" {
		t.Fatalf("kind = %q, want achievement", e.Kind)
	}
}

// An older client's PATCH carries no portfolio fields; they must survive it.
func TestApplyLegacyPatchKeepsPortfolioFields(t *testing.T) {
	e := profileEntry{Kind: "experience", Subtype: strp("internship"), Ongoing: true,
		Details: map[string]string{"work_mode": "remote"}, Skills: []string{"Go"}}
	if err := (&profileEntryInput{Kind: "experience", Title: "Acme", EndDate: strp("2025-06-01")}).apply(&e, false); err != nil {
		t.Fatal(err)
	}
	if *e.Subtype != "internship" || e.Details["work_mode"] != "remote" || len(e.Skills) != 1 {
		t.Fatalf("portfolio fields lost: %+v", e)
	}
	if e.Ongoing {
		t.Fatal("setting an end date should end an ongoing entry")
	}
}

func TestMissingForSubmit(t *testing.T) {
	e := profileEntry{Subtype: strp("internship"), Title: "x", Details: map[string]string{}}
	got := strings.Join(missingForSubmit(&e), ",")
	if got != "org,start_date,end_date" {
		t.Fatalf("missing = %s", got)
	}
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	e.Org, e.StartDate, e.Ongoing = strp("Acme"), &start, true
	if m := missingForSubmit(&e); len(m) != 0 {
		t.Fatalf("complete internship still missing %v", m)
	}
	if m := missingForSubmit(&profileEntry{Title: "legacy"}); len(m) != 0 {
		t.Fatalf("legacy entries need nothing extra, got %v", m)
	}
}

func TestCleanSkillsDedupesCaseInsensitively(t *testing.T) {
	got, err := cleanSkills([]string{"Go", " go ", "", "Flutter"})
	if err != nil || strings.Join(got, ",") != "Go,Flutter" {
		t.Fatalf("got %v, %v", got, err)
	}
}
