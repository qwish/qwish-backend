package curriculum

import (
	"strings"
	"testing"
)

func sampleVersion() VersionInput {
	return VersionInput{Label: "2026", Subject: "Mathematics", Grade: "6", Chapters: []ChapterInput{
		{Title: "Fractions", Concepts: []ConceptInput{{Code: "F-01", Title: "Equivalent fractions", LearningOutcome: "Compare equivalent fractions."}}},
	}}
}

func TestVersionValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*VersionInput)
		valid  bool
	}{
		{"complete", func(*VersionInput) {}, true},
		{"empty draft", func(v *VersionInput) { v.Chapters = nil }, true},
		{"missing subject", func(v *VersionInput) { v.Subject = " " }, false},
		{"blank concept", func(v *VersionInput) { v.Chapters[0].Concepts[0].Title = " " }, false},
		{"duplicate across chapters", func(v *VersionInput) {
			v.Chapters = append(v.Chapters, ChapterInput{Title: "More", Concepts: []ConceptInput{{Code: " f-01 ", Title: "Duplicate"}}})
		}, false},
		{"too many chapters", func(v *VersionInput) { v.Chapters = make([]ChapterInput, 51) }, false},
		{"outcome too long", func(v *VersionInput) { v.Chapters[0].Concepts[0].LearningOutcome = strings.Repeat("x", 1001) }, false},
		{"unicode titles", func(v *VersionInput) { v.Chapters[0].Title = strings.Repeat("अ", 160) }, true},
		{"nul", func(v *VersionInput) { v.Grade = "6\x00" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := sampleVersion()
			tc.change(&v)
			err := v.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("Validate() = %v, valid=%v", err, tc.valid)
			}
		})
	}
}

func TestYearValidation(t *testing.T) {
	for _, tc := range []struct {
		start, end string
		valid      bool
	}{
		{"2026-06-01", "2027-05-31", true},
		{"2026-06-01", "2026-06-01", true},
		{"2026-06-01", "2026-05-31", false},
		{"2026-02-30", "2027-05-31", false},
		{"0000-01-01", "2027-05-31", false},
		{"2026-06-01", "2201-01-01", false},
	} {
		in := YearInput{Name: " 2026–27 ", StartsOn: tc.start, EndsOn: tc.end}
		err := in.Validate()
		if (err == nil) != tc.valid {
			t.Errorf("%s/%s: %v", tc.start, tc.end, err)
		}
		if in.Name != "2026–27" {
			t.Fatal("name was not trimmed")
		}
	}
}
