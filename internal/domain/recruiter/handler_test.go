package recruiter

import (
	"strings"
	"testing"
)

func TestScoredCandidatesExposesCompletedCount(t *testing.T) {
	if !strings.Contains(scoredCandidates, "AS completed") {
		t.Fatal("scoredCandidates must expose its completed assessment count to outer queries")
	}
}
