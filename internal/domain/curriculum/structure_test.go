package curriculum

import (
	"strings"
	"testing"
)

func baseInput() VersionInput {
	return VersionInput{Label: "2027 edition", Subject: "Physics", Grade: "11", Chapters: []ChapterInput{
		{Title: "Motion", Concepts: []ConceptInput{
			{Code: "K01", Title: "Displacement"},
			{Code: "K02", Title: "Velocity"},
		}},
	}}
}

func TestPrerequisitesMustExistAndNotCycle(t *testing.T) {
	in := baseInput()
	in.Chapters[0].Concepts[1].Prerequisites = []string{"K01"}
	if err := in.Validate(); err != nil {
		t.Fatalf("valid prerequisite rejected: %v", err)
	}

	in = baseInput()
	in.Chapters[0].Concepts[0].Prerequisites = []string{"K99"}
	if err := in.Validate(); err == nil || !strings.Contains(err.Error(), "isn't a concept") {
		t.Fatalf("unknown prerequisite: got %v", err)
	}

	in = baseInput()
	in.Chapters[0].Concepts[0].Prerequisites = []string{"K02"}
	in.Chapters[0].Concepts[1].Prerequisites = []string{"k01"} // codes compare case-insensitively
	if err := in.Validate(); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle: got %v", err)
	}

	in = baseInput()
	in.Chapters[0].Concepts[0].Prerequisites = []string{"K01"}
	if err := in.Validate(); err == nil || !strings.Contains(err.Error(), "own prerequisite") {
		t.Fatalf("self prerequisite: got %v", err)
	}
}

func TestLevelsMustComeFromTheChosenFramework(t *testing.T) {
	in := baseInput()
	in.Chapters[0].Concepts[0].CognitiveLevel = "apply"
	if err := in.Validate(); err == nil {
		t.Fatal("a level with no framework chosen should be rejected")
	}
	in = baseInput()
	in.Settings.OutcomeFramework = "blooms"
	in.Settings.DifficultyScale = "three"
	in.Chapters[0].Concepts[0].CognitiveLevel = "apply"
	in.Chapters[0].Concepts[0].Difficulty = "core"
	if err := in.Validate(); err != nil {
		t.Fatalf("valid levels rejected: %v", err)
	}
	if in.Settings.MinStudents != 10 {
		t.Errorf("min_students_reported defaults to 10, got %d", in.Settings.MinStudents)
	}
}

func TestGroupingBoundsChapters(t *testing.T) {
	in := baseInput()
	in.Settings.Grouping = "terms"
	in.Settings.GroupCount = 2
	in.Chapters[0].Group = 3
	if err := in.Validate(); err == nil {
		t.Fatal("chapter in term 3 of 2 should be rejected")
	}
	in = baseInput()
	in.Chapters[0].Group = 3 // ignored without grouping
	if err := in.Validate(); err != nil || in.Chapters[0].Group != 0 {
		t.Fatalf("ungrouped chapter group should reset to 0: %v, %d", err, in.Chapters[0].Group)
	}
}

func TestPublishIssues(t *testing.T) {
	w := func(f float64) *float64 { return &f }
	v := Version{
		Settings: Settings{WeightageEnabled: true, RequiredFields: []string{"learning_outcome"}},
		Chapters: []Chapter{
			{Title: "Motion", ChapterDetails: ChapterDetails{Weightage: w(60)}, Concepts: []Concept{
				{ConceptInput: ConceptInput{Code: "K01", Title: "A", LearningOutcome: "x"}},
				{ConceptInput: ConceptInput{Code: "K08", Title: "Free fall"}},
			}},
			{Title: "Energy", ChapterDetails: ChapterDetails{Weightage: w(35)}},
		},
	}
	issues := strings.Join(PublishIssues(v), " | ")
	for _, want := range []string{"“Energy” has no concepts", "K08 has no learning outcome", "Weightage totals 95%"} {
		if !strings.Contains(issues, want) {
			t.Errorf("missing %q in %q", want, issues)
		}
	}
	v.Settings = Settings{}
	v.Chapters = v.Chapters[:1]
	if got := PublishIssues(v); len(got) != 0 {
		t.Errorf("no rules, complete content: want no issues, got %v", got)
	}
}
