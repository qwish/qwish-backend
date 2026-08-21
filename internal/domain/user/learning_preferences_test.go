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
}
