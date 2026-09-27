package curriculum

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound   = errors.New("academic resource not found")
	ErrConflict   = errors.New("academic resource conflicts with an existing record")
	ErrPublished  = errors.New("published versions cannot be edited")
	ErrRevision   = errors.New("the draft changed; reload before saving")
	ErrNoteNeeded = errors.New("this curriculum asks for a change note on every revision")
)

// PublishBlockedError lists every reason a draft can't be published yet, so
// the editor can show all of them rather than one at a time.
type PublishBlockedError struct{ Issues []string }

func (e *PublishBlockedError) Error() string { return strings.Join(e.Issues, "; ") }

type YearInput struct {
	Name     string `json:"name"`
	StartsOn string `json:"starts_on"`
	EndsOn   string `json:"ends_on"`
}

type Year struct {
	ID string `json:"id"`
	YearInput
	Stats *YearStats `json:"stats,omitempty"`
}

// ConceptDetails is the optional per-concept metadata stored as JSONB.
type ConceptDetails struct {
	CognitiveLevel  string   `json:"cognitive_level,omitempty"`
	Difficulty      string   `json:"difficulty,omitempty"`
	TeachingPeriods *int     `json:"teaching_periods,omitempty"`
	Weight          *float64 `json:"weight,omitempty"`
	// Prerequisites are concept codes within the same version.
	Prerequisites  []string `json:"prerequisites,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Misconceptions []string `json:"misconceptions,omitempty"`
	TextbookRef    string   `json:"textbook_ref,omitempty"`
	Optional       bool     `json:"optional,omitempty"`
}

type ConceptInput struct {
	Code            string `json:"code"`
	Title           string `json:"title"`
	LearningOutcome string `json:"learning_outcome"`
	ConceptDetails
}

// ChapterDetails is the optional per-chapter plan stored as JSONB.
type ChapterDetails struct {
	// Group is the term or unit number (1-based) when the version groups
	// chapters; 0 means ungrouped.
	Group          int      `json:"group,omitempty"`
	Weightage      *float64 `json:"weightage,omitempty"`
	PlannedPeriods *int     `json:"planned_periods,omitempty"`
	WeekFrom       *int     `json:"week_from,omitempty"`
	WeekTo         *int     `json:"week_to,omitempty"`
	Optional       bool     `json:"optional,omitempty"`
}

type ChapterInput struct {
	Title    string         `json:"title"`
	Concepts []ConceptInput `json:"concepts"`
	ChapterDetails
}

// Settings are a version's structure and evidence rules.
type Settings struct {
	Board             string   `json:"board,omitempty"`
	Stream            string   `json:"stream,omitempty"`
	Medium            string   `json:"medium,omitempty"`
	Description       string   `json:"description,omitempty"`
	Grouping          string   `json:"grouping,omitempty"` // "", "terms", "units"
	GroupCount        int      `json:"group_count,omitempty"`
	CodePattern       string   `json:"code_pattern,omitempty"`
	OutcomeFramework  string   `json:"outcome_framework,omitempty"` // "", "blooms", "three_level"
	DifficultyScale   string   `json:"difficulty_scale,omitempty"`  // "", "three", "five"
	RequiredFields    []string `json:"required_fields,omitempty"`
	WeightageEnabled  bool     `json:"weightage_enabled,omitempty"`
	TeachingPlan      bool     `json:"teaching_plan,omitempty"`
	MinStudents       int      `json:"min_students_reported,omitempty"`
	FlagPrereqOrder   bool     `json:"flag_prerequisite_order,omitempty"`
	RequireChangeNote bool     `json:"require_change_note,omitempty"`
}

type VersionInput struct {
	Label    string         `json:"label"`
	Subject  string         `json:"subject"`
	Grade    string         `json:"grade"`
	Chapters []ChapterInput `json:"chapters"`
	Settings Settings       `json:"settings"`
	// ChangeNote explains a save; required when Settings.RequireChangeNote.
	ChangeNote string `json:"change_note,omitempty"`
	// CopiedFromVersionID records the edition a new version started from.
	CopiedFromVersionID string `json:"copied_from_version_id,omitempty"`
}

// Levels and scales the settings may name. A concept's level/difficulty must
// come from the set its version chose.
var (
	outcomeLevels = map[string][]string{
		"blooms":      {"remember", "understand", "apply", "analyse", "evaluate", "create"},
		"three_level": {"recall", "apply", "reason"},
	}
	difficultyLevels = map[string][]string{
		"three": {"foundational", "core", "advanced"},
		"five":  {"1", "2", "3", "4", "5"},
	}
	requirableFields = map[string]bool{
		"learning_outcome": true, "cognitive_level": true, "difficulty": true,
		"teaching_periods": true, "prerequisites": true,
	}
)

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

type CreateInput struct {
	Name string `json:"name"`
	VersionInput
}

type Concept struct {
	ID string `json:"id"`
	ConceptInput
	// MappedQuestionsPrevious counts questions mapped to this concept's code in
	// the curriculum's other editions — the mappings that carry over only while
	// the code is unchanged.
	MappedQuestionsPrevious int `json:"mapped_questions_previous"`
}

type Chapter struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Concepts []Concept `json:"concepts"`
	ChapterDetails
}

type VersionSummary struct {
	ID           string     `json:"id"`
	CurriculumID string     `json:"curriculum_id"`
	Name         string     `json:"name"`
	Label        string     `json:"label"`
	Subject      string     `json:"subject"`
	Grade        string     `json:"grade"`
	Status       string     `json:"status"`
	Revision     int        `json:"revision"`
	PublishedAt  *time.Time `json:"published_at"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty"`
	// Filled by the list endpoint only.
	ChapterCount    *int            `json:"chapter_count,omitempty"`
	ConceptCount    *int            `json:"concept_count,omitempty"`
	AssignedClasses []AssignedClass `json:"assigned_classes,omitempty"`
}

// AssignedClass is a live assignment in an academic year that covers today.
type AssignedClass struct {
	GroupID          string `json:"group_id"`
	Name             string `json:"name"`
	AcademicYearName string `json:"academic_year_name"`
}

type Version struct {
	VersionSummary
	Settings            Settings  `json:"settings"`
	CopiedFromVersionID *string   `json:"copied_from_version_id"`
	CopiedFromLabel     *string   `json:"copied_from_label"`
	Chapters            []Chapter `json:"chapters"`
}

// Revision is one entry in a version's history.
type Revision struct {
	Revision  int       `json:"revision"`
	Action    string    `json:"action"`
	Note      string    `json:"note"`
	ActorName string    `json:"actor_name"`
	CreatedAt time.Time `json:"created_at"`
}

type AssignmentInput struct {
	AcademicYearID string `json:"academic_year_id"`
	VersionID      string `json:"version_id"`
}

type Assignment struct {
	ID               string         `json:"id"`
	GroupID          string         `json:"group_id"`
	AcademicYearID   string         `json:"academic_year_id"`
	AcademicYearName string         `json:"academic_year_name"`
	Version          VersionSummary `json:"version"`
}

func validText(value *string, field string, max int) error {
	*value = strings.TrimSpace(*value)
	if n := utf8.RuneCountInString(*value); n < 1 || n > max {
		return fmt.Errorf("%s must contain 1–%d characters", field, max)
	}
	if strings.ContainsRune(*value, '\x00') {
		return fmt.Errorf("%s contains an invalid character", field)
	}
	return nil
}

func (in *YearInput) Validate() error {
	if err := validText(&in.Name, "name", 120); err != nil {
		return err
	}
	start, err := time.Parse("2006-01-02", in.StartsOn)
	if err != nil || start.Year() < 1900 || start.Year() > 2200 {
		return errors.New("starts_on must be a date between 1900 and 2200")
	}
	end, err := time.Parse("2006-01-02", in.EndsOn)
	if err != nil || end.Year() < 1900 || end.Year() > 2200 || end.Before(start) {
		return errors.New("ends_on must be a date on or after starts_on, no later than 2200")
	}
	return nil
}

func (in *VersionInput) Validate() error {
	for _, f := range []struct {
		value *string
		name  string
		max   int
	}{
		{&in.Label, "label", 80}, {&in.Subject, "subject", 120}, {&in.Grade, "grade", 80},
	} {
		if err := validText(f.value, f.name, f.max); err != nil {
			return err
		}
	}
	if len(in.Chapters) > 50 {
		return errors.New("a version supports at most 50 chapters")
	}
	count := 0
	codes := map[string]bool{}
	for i := range in.Chapters {
		ch := &in.Chapters[i]
		if err := validText(&ch.Title, "chapter title", 160); err != nil {
			return err
		}
		for j := range ch.Concepts {
			c := &ch.Concepts[j]
			if err := validText(&c.Code, "concept code", 80); err != nil {
				return err
			}
			if err := validText(&c.Title, "concept title", 160); err != nil {
				return err
			}
			c.LearningOutcome = strings.TrimSpace(c.LearningOutcome)
			if utf8.RuneCountInString(c.LearningOutcome) > 1000 || strings.ContainsRune(c.LearningOutcome, '\x00') {
				return errors.New("learning_outcome must contain at most 1000 valid characters")
			}
			key := strings.ToLower(c.Code)
			if codes[key] {
				return fmt.Errorf("duplicate concept code: %s", c.Code)
			}
			codes[key] = true
			count++
		}
	}
	if count > 500 {
		return errors.New("a version supports at most 500 concepts")
	}
	return in.validateStructure(codes)
}

// validateStructure checks settings and the optional chapter/concept details.
// codes holds every concept code in the version, lower-cased.
func (in *VersionInput) validateStructure(codes map[string]bool) error {
	st := &in.Settings
	for _, f := range []struct {
		v    *string
		name string
		max  int
	}{
		{&st.Board, "board", 80}, {&st.Stream, "stream", 80}, {&st.Medium, "medium", 80},
		{&st.Description, "description", 1000}, {&st.CodePattern, "code_pattern", 60},
	} {
		*f.v = strings.TrimSpace(*f.v)
		if utf8.RuneCountInString(*f.v) > f.max || strings.ContainsRune(*f.v, '\x00') {
			return fmt.Errorf("%s must contain at most %d valid characters", f.name, f.max)
		}
	}
	switch st.Grouping {
	case "":
		st.GroupCount = 0
	case "terms", "units":
		if st.GroupCount < 1 || st.GroupCount > 12 {
			return errors.New("group_count must be between 1 and 12")
		}
	default:
		return errors.New("grouping must be terms, units or empty")
	}
	if st.OutcomeFramework != "" && outcomeLevels[st.OutcomeFramework] == nil {
		return errors.New("outcome_framework must be blooms, three_level or empty")
	}
	if st.DifficultyScale != "" && difficultyLevels[st.DifficultyScale] == nil {
		return errors.New("difficulty_scale must be three, five or empty")
	}
	for _, f := range st.RequiredFields {
		if !requirableFields[f] {
			return fmt.Errorf("required_fields: %q can't be required", f)
		}
	}
	if st.MinStudents == 0 {
		st.MinStudents = 10
	}
	if st.MinStudents < 1 || st.MinStudents > 500 {
		return errors.New("min_students_reported must be between 1 and 500")
	}
	in.ChangeNote = strings.TrimSpace(in.ChangeNote)
	if utf8.RuneCountInString(in.ChangeNote) > 500 {
		return errors.New("change_note must contain at most 500 characters")
	}

	prereqs := map[string][]string{}
	for i := range in.Chapters {
		ch := &in.Chapters[i]
		if st.Grouping == "" {
			ch.Group = 0
		} else if ch.Group < 0 || ch.Group > st.GroupCount {
			return fmt.Errorf("chapter %d: group must be between 1 and %d", i+1, st.GroupCount)
		}
		if ch.Weightage != nil && (*ch.Weightage < 0 || *ch.Weightage > 100) {
			return fmt.Errorf("chapter %d: weightage must be 0–100", i+1)
		}
		if err := nonNegative(ch.PlannedPeriods, fmt.Sprintf("chapter %d: planned_periods", i+1), 1000); err != nil {
			return err
		}
		if err := nonNegative(ch.WeekFrom, fmt.Sprintf("chapter %d: week_from", i+1), 60); err != nil {
			return err
		}
		if err := nonNegative(ch.WeekTo, fmt.Sprintf("chapter %d: week_to", i+1), 60); err != nil {
			return err
		}
		if ch.WeekFrom != nil && ch.WeekTo != nil && *ch.WeekTo < *ch.WeekFrom {
			return fmt.Errorf("chapter %d: week_to is before week_from", i+1)
		}
		for j := range ch.Concepts {
			c := &ch.Concepts[j]
			where := fmt.Sprintf("concept %s", c.Code)
			if c.CognitiveLevel != "" && !contains(outcomeLevels[st.OutcomeFramework], c.CognitiveLevel) {
				return fmt.Errorf("%s: cognitive_level %q isn't in the version's framework", where, c.CognitiveLevel)
			}
			if c.Difficulty != "" && !contains(difficultyLevels[st.DifficultyScale], c.Difficulty) {
				return fmt.Errorf("%s: difficulty %q isn't in the version's scale", where, c.Difficulty)
			}
			if err := nonNegative(c.TeachingPeriods, where+": teaching_periods", 200); err != nil {
				return err
			}
			if c.Weight != nil && (*c.Weight < 0 || *c.Weight > 100) {
				return fmt.Errorf("%s: weight must be 0–100", where)
			}
			if err := cleanList(&c.Tags, where+": tags", 10, 40); err != nil {
				return err
			}
			if err := cleanList(&c.Misconceptions, where+": misconceptions", 10, 300); err != nil {
				return err
			}
			if err := cleanList(&c.Prerequisites, where+": prerequisites", 20, 80); err != nil {
				return err
			}
			c.TextbookRef = strings.TrimSpace(c.TextbookRef)
			if utf8.RuneCountInString(c.TextbookRef) > 200 {
				return fmt.Errorf("%s: textbook_ref must contain at most 200 characters", where)
			}
			key := strings.ToLower(c.Code)
			for _, p := range c.Prerequisites {
				pk := strings.ToLower(p)
				if pk == key {
					return fmt.Errorf("%s can't be its own prerequisite", c.Code)
				}
				if !codes[pk] {
					return fmt.Errorf("%s: prerequisite %s isn't a concept in this version", c.Code, p)
				}
				prereqs[key] = append(prereqs[key], pk)
			}
		}
	}
	if cycle := findCycle(prereqs); cycle != "" {
		return fmt.Errorf("prerequisites form a cycle through %s", cycle)
	}
	return nil
}

func nonNegative(v *int, name string, max int) error {
	if v != nil && (*v < 0 || *v > max) {
		return fmt.Errorf("%s must be 0–%d", name, max)
	}
	return nil
}

// cleanList trims, drops blanks and duplicates, and enforces limits.
func cleanList(list *[]string, name string, maxItems, maxLen int) error {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range *list {
		v = strings.TrimSpace(v)
		if v == "" || seen[strings.ToLower(v)] {
			continue
		}
		if utf8.RuneCountInString(v) > maxLen || strings.ContainsRune(v, '\x00') {
			return fmt.Errorf("%s: each item must contain at most %d valid characters", name, maxLen)
		}
		seen[strings.ToLower(v)] = true
		out = append(out, v)
	}
	if len(out) > maxItems {
		return fmt.Errorf("%s: at most %d items", name, maxItems)
	}
	*list = out
	return nil
}

// findCycle returns a concept code on a prerequisite cycle, or "".
func findCycle(edges map[string][]string) string {
	const (
		unseen = iota
		active
		done
	)
	state := map[string]int{}
	var visit func(string) string
	visit = func(n string) string {
		switch state[n] {
		case active:
			return n
		case done:
			return ""
		}
		state[n] = active
		for _, m := range edges[n] {
			if c := visit(m); c != "" {
				return c
			}
		}
		state[n] = done
		return ""
	}
	for n := range edges {
		if c := visit(n); c != "" {
			return c
		}
	}
	return ""
}

// PublishIssues lists what blocks publishing a version: structure the
// publish step always needs, plus whatever its settings make mandatory.
func PublishIssues(v Version) []string {
	issues := []string{}
	if len(v.Chapters) == 0 {
		return append(issues, "Add at least one chapter.")
	}
	required := map[string]bool{}
	for _, f := range v.Settings.RequiredFields {
		required[f] = true
	}
	total := 0.0
	for _, ch := range v.Chapters {
		if len(ch.Concepts) == 0 {
			issues = append(issues, fmt.Sprintf("Chapter “%s” has no concepts.", ch.Title))
		}
		if v.Settings.WeightageEnabled {
			if ch.Weightage == nil {
				issues = append(issues, fmt.Sprintf("Chapter “%s” has no weightage.", ch.Title))
			} else {
				total += *ch.Weightage
			}
		}
		for _, c := range ch.Concepts {
			missing := []string{}
			if required["learning_outcome"] && c.LearningOutcome == "" {
				missing = append(missing, "learning outcome")
			}
			if required["cognitive_level"] && c.CognitiveLevel == "" {
				missing = append(missing, "cognitive level")
			}
			if required["difficulty"] && c.Difficulty == "" {
				missing = append(missing, "difficulty")
			}
			if required["teaching_periods"] && c.TeachingPeriods == nil {
				missing = append(missing, "teaching periods")
			}
			if required["prerequisites"] && len(c.Prerequisites) == 0 {
				missing = append(missing, "prerequisites")
			}
			if len(missing) > 0 {
				issues = append(issues, fmt.Sprintf("%s has no %s.", c.Code, strings.Join(missing, ", ")))
			}
		}
	}
	if v.Settings.WeightageEnabled && (total < 99.99 || total > 100.01) {
		issues = append(issues, fmt.Sprintf("Weightage totals %.4g%% (needs 100%%).", total))
	}
	return issues
}
