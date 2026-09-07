package learning

import "testing"

func TestInsightStatusRequiresRepeatedMappedEvidence(t *testing.T) {
	tests := []struct {
		name           string
		mapped         bool
		distinct, high int
		want           string
	}{
		{"unmapped errors remain insufficient", false, 4, 4, "insufficient_evidence"},
		{"one confident mapped error is a review flag", true, 1, 1, "review_flag"},
		{"one uncertain mapped error remains insufficient", true, 1, 0, "insufficient_evidence"},
		{"two distinct mapped errors form a possible misconception", true, 2, 0, "possible_misconception"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := insightStatus(tt.mapped, tt.distinct, tt.high); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
