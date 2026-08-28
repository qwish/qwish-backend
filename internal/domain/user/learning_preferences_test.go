package user

import (
	"context"
	"errors"
	"testing"
)

func TestUpdateLearningPreferencesRejectsInvalidInputBeforeDatabase(t *testing.T) {
	svc := &Service{}
	if _, err := svc.UpdateLearningPreferences(context.Background(), "user", "xx", []string{"general"}); !errors.Is(err, ErrInvalidLearningLanguage) {
		t.Fatalf("language error = %v", err)
	}
	if _, err := svc.UpdateLearningPreferences(context.Background(), "user", "en", nil); !errors.Is(err, ErrInvalidLearningTopics) {
		t.Fatalf("topics error = %v", err)
	}
	if _, err := svc.UpdateLearningPreferences(context.Background(), "user", "en", []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}); !errors.Is(err, ErrInvalidLearningTopics) {
		t.Fatalf("fewer than %d topics error = %v", MinLearningTopics, err)
	}
	duplicates := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "nine"}
	if _, err := svc.UpdateLearningPreferences(context.Background(), "user", "en", duplicates); !errors.Is(err, ErrInvalidLearningTopics) {
		t.Fatalf("fewer than %d unique topics error = %v", MinLearningTopics, err)
	}
}
