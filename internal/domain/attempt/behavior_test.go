package attempt

import "testing"

func TestValidateBehaviorEvent(t *testing.T) {
	questionID := "22222222-2222-4222-8222-222222222222"
	changeCount := 2
	hiddenMs := 1000
	validID := "11111111-1111-4111-8111-111111111111"
	tests := []struct {
		name    string
		event   BehaviorEvent
		wantErr bool
	}{
		{"view", BehaviorEvent{ClientEventID: validID, EventType: "question_viewed", QuestionID: &questionID}, false},
		{"answer changes", BehaviorEvent{ClientEventID: validID, EventType: "answer_changed", QuestionID: &questionID, ChangeCount: &changeCount}, false},
		{"focus return", BehaviorEvent{ClientEventID: validID, EventType: "focus_gained", HiddenMs: &hiddenMs}, false},
		{"unknown type", BehaviorEvent{ClientEventID: validID, EventType: "keystroke", QuestionID: &questionID}, true},
		{"answer content cannot be represented", BehaviorEvent{ClientEventID: validID, EventType: "answer_changed", QuestionID: &questionID}, true},
		{"question missing", BehaviorEvent{ClientEventID: validID, EventType: "timer_expired"}, true},
		{"invalid id", BehaviorEvent{ClientEventID: "nope", EventType: "focus_lost"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateBehaviorEvent(test.event)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateBehaviorEvent() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
