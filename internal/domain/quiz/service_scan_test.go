package quiz

import (
	"errors"
	"strings"
	"testing"
)

type failingQuizRows struct {
	next bool
}

func (r *failingQuizRows) Next() bool {
	if r.next {
		return false
	}
	r.next = true
	return true
}

func (*failingQuizRows) Scan(...interface{}) error {
	return errors.New("column count mismatch")
}

func (*failingQuizRows) Close() {}

func TestScanQuizRowsReturnsScanErrors(t *testing.T) {
	service := &Service{}
	quizzes, err := service.scanQuizRows(&failingQuizRows{})
	if err == nil || !strings.Contains(err.Error(), "column count mismatch") {
		t.Fatalf("expected scan error, got quizzes=%v err=%v", quizzes, err)
	}
	if quizzes != nil {
		t.Fatalf("expected no malformed quizzes, got %v", quizzes)
	}
}
