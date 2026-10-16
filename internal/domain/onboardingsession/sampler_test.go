package onboardingsession

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

// letters turns "AAAAAAABDB" into option positions.
func letters(s string) []int {
	out := make([]int, len(s))
	for i, c := range s {
		out[i] = int(c - 'A')
	}
	return out
}

// The walkthrough from the product sheet: 1A-5A, 6A, 7A, 8B, 9D, 10B.
func TestProfileMatchesProductWalkthrough(t *testing.T) {
	top, subs := profile(letters("AAAAAAABDB"))
	if want := []string{"quantitative", "aptitude"}; !slices.Equal(top, want) {
		t.Fatalf("top = %v; want %v", top, want)
	}
	want := []string{
		// primary: Q2A's Data Interpretation, then Q6A
		"apt_di", "quant_arithmetic", "quant_percentages", "quant_tsw", "quant_number_system",
		// secondary: Q9D; Q7A and Q8B drop out, their topics scored nothing
		"current_india", "current_world", "current_business_tech", "current_sports",
		// wildcard: Q10B
		"ent_anime", "ent_games", "ent_comics", "ent_scifi_fantasy",
	}
	if !slices.Equal(subs, want) {
		t.Fatalf("subs = %v; want %v", subs, want)
	}
}

func TestProfileRoutesFlatScoresToMixed(t *testing.T) {
	// Entertainment 4; Social Studies, Current Affairs and Science 3.
	top, subs := profile(letters("DDCEBAAAAA"))
	if !slices.Equal(top, []string{"general", "aptitude"}) || !slices.Equal(subs, []string{"general_mixed", "apt_mixed"}) {
		t.Fatalf("profile = %v, %v; want the mixed route", top, subs)
	}
	top, _ = profile([]int{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1})
	if !slices.Equal(top, []string{"general", "aptitude"}) {
		t.Fatalf("all skipped: top = %v; want the mixed route", top)
	}
}

func TestOptionIndexMatchesTextOnly(t *testing.T) {
	opts := json.RawMessage(`["A","B","C"]`)
	for answer, want := range map[string]int{`"C"`: 2, `"Z"`: -1, `""`: -1, `3`: -1} {
		if got := optionIndex(opts, json.RawMessage(answer)); got != want {
			t.Errorf("optionIndex(%s) = %d; want %d", answer, got, want)
		}
	}
}

// The seeded quiz and the scoring sheet must agree, every slug must exist, and
// the sampler must serve the quiz, keep it across calls, and store the
// profiled sub-topics where signup copies them into the user's interests.
func TestDiscoveryQuizMatchesSheetAndProfiles(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	for q, choices := range profileQuiz {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT jsonb_array_length(options) FROM questions WHERE quiz_id=$1 AND position=$2`,
			discoveryQuizID, q+1).Scan(&n); err != nil || n != len(choices) {
			t.Fatalf("Q%d: seeded %d options, %v; sheet has %d", q+1, n, err, len(choices))
		}
		for _, c := range choices {
			for _, p := range c.Points {
				var ok bool
				pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM domains WHERE slug=$1)`, p.Domain).Scan(&ok)
				if !ok {
					t.Errorf("Q%d: unknown topic %q", q+1, p.Domain)
				}
			}
			for _, tg := range c.Tags {
				var ok bool
				pool.QueryRow(ctx,
					`SELECT EXISTS (SELECT 1 FROM subdomains WHERE slug=$1 AND domain_slug=$2)`,
					tg.Sub, tg.Domain).Scan(&ok)
				if !ok {
					t.Errorf("Q%d: %s is not a sub-topic of %s", q+1, tg.Sub, tg.Domain)
				}
			}
		}
	}

	id, err := svc.Create(ctx, "en", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id) })

	first, err := svc.Sampler(ctx, id)
	if err != nil {
		t.Fatalf("Sampler: %v", err)
	}
	if len(first) != len(profileQuiz) || first[0].QuizID != discoveryQuizID || first[9].Position != 10 {
		t.Fatalf("Sampler = %d questions; want the %d-question discovery quiz in order", len(first), len(profileQuiz))
	}
	again, err := svc.Sampler(ctx, id)
	if err != nil || len(again) != len(first) || again[0].ID != first[0].ID {
		t.Fatalf("second Sampler call changed the set: %v", err)
	}

	var answers []Answer
	for i, pick := range letters("AAAAAAABDB") {
		var opts []string
		json.Unmarshal(first[i].Options, &opts)
		answer, _ := json.Marshal(opts[pick])
		answers = append(answers, Answer{QuestionID: first[i].ID, Answer: answer})
	}
	res, err := svc.SubmitSampler(ctx, id, answers)
	if err != nil {
		t.Fatalf("SubmitSampler: %v", err)
	}
	if !res.Profile || res.TotalCorrect != 10 || res.TotalQuestions != 10 || len(res.Review) != 0 {
		t.Fatalf("result = %+v; want a 10/10-answered profile", res.SubmitResult)
	}
	if len(res.Interests) != 2 || res.Interests[0].Slug != "quantitative" || res.Interests[1].Slug != "aptitude" {
		t.Fatalf("interests = %+v; want [quantitative aptitude]", res.Interests)
	}
	_, want := profile(letters("AAAAAAABDB"))
	_, topics, err := svc.Prefs(ctx, id)
	if err != nil || !slices.Equal(topics, want) {
		t.Fatalf("session topics = %v, %v; want %v", topics, err, want)
	}
	if _, err := svc.SubmitSampler(ctx, id, answers); err != ErrAlreadySubmitted {
		t.Fatalf("second submit = %v; want ErrAlreadySubmitted", err)
	}
}
