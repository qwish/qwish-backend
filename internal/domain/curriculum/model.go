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
	ErrIncomplete = errors.New("add at least one chapter and a concept in every chapter before publishing")
)

type YearInput struct {
	Name     string `json:"name"`
	StartsOn string `json:"starts_on"`
	EndsOn   string `json:"ends_on"`
}

type Year struct {
	ID string `json:"id"`
	YearInput
}

type ConceptInput struct {
	Code            string `json:"code"`
	Title           string `json:"title"`
	LearningOutcome string `json:"learning_outcome"`
}

type ChapterInput struct {
	Title    string         `json:"title"`
	Concepts []ConceptInput `json:"concepts"`
}

type VersionInput struct {
	Label    string         `json:"label"`
	Subject  string         `json:"subject"`
	Grade    string         `json:"grade"`
	Chapters []ChapterInput `json:"chapters"`
}

type CreateInput struct {
	Name string `json:"name"`
	VersionInput
}

type Concept struct {
	ID string `json:"id"`
	ConceptInput
}

type Chapter struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Concepts []Concept `json:"concepts"`
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
}

type Version struct {
	VersionSummary
	Chapters []Chapter `json:"chapters"`
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
	return nil
}
