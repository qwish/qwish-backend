package onboardingsession

import "testing"

func TestNormalizeLanguage(t *testing.T) {
	for _, in := range []string{"en", "hi", "mr"} {
		got, err := normalizeLanguage(in)
		if err != nil || got != in {
			t.Fatalf("normalizeLanguage(%q) = %q, %v; want %q, nil", in, got, err, in)
		}
	}

	// Empty falls back to the default rather than erroring: a user who skips
	// the picker still gets a session.
	got, err := normalizeLanguage("")
	if err != nil || got != "en" {
		t.Fatalf(`normalizeLanguage("") = %q, %v; want "en", nil`, got, err)
	}

	if _, err := normalizeLanguage("klingon"); err == nil {
		t.Fatal("normalizeLanguage(\"klingon\") returned nil error; want ErrBadLanguage")
	}
}

func TestNormalizeTopics(t *testing.T) {
	got, err := normalizeTopics([]string{"verbal", "logical", "verbal"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Deduplicated and sorted, so two sessions with the same picks compare equal.
	if len(got) != 2 || got[0] != "logical" || got[1] != "verbal" {
		t.Fatalf("normalizeTopics = %v; want [logical verbal]", got)
	}

	empty, err := normalizeTopics(nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("normalizeTopics(nil) = %v, %v; want empty, nil", empty, err)
	}

	if _, err := normalizeTopics([]string{"astrology"}); err == nil {
		t.Fatal("normalizeTopics([astrology]) returned nil error; want ErrBadTopic")
	}
}
