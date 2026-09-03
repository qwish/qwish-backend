package user

import "testing"

func TestAchievementCatalog(t *testing.T) {
	if got := len(achievementSpecs); got != 28 {
		t.Fatalf("achievement catalog has %d entries, want 28", got)
	}
	seen := make(map[string]bool, len(achievementSpecs))
	for _, spec := range achievementSpecs {
		if spec.id == "" || spec.name == "" || spec.description == "" || spec.category == "" || spec.rarity == "" || spec.icon == "" || spec.target < 1 {
			t.Fatalf("incomplete achievement spec: %+v", spec)
		}
		if seen[spec.id] {
			t.Fatalf("duplicate achievement id %q", spec.id)
		}
		seen[spec.id] = true
	}
}
