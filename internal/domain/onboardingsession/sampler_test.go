package onboardingsession

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

func TestInferInterestsRanksCorrectBySpeed(t *testing.T) {
	got := inferInterests([]topicScore{
		{Domain: "verbal", Correct: true, ElapsedMs: 50_000, LimitMs: 60_000},
		{Domain: "logical", Correct: true, ElapsedMs: 5_000, LimitMs: 60_000},
		{Domain: "quantitative", Correct: false, ElapsedMs: 1_000, LimitMs: 60_000},
		{Domain: "general", Correct: true, ElapsedMs: 30_000, LimitMs: 0},
		{Domain: "aptitude", Correct: true, ElapsedMs: 90_000, LimitMs: 60_000},
	})
	want := []string{"logical", "general", "verbal"}
	if !slices.Equal(got, want) {
		t.Fatalf("inferInterests = %v; want %v", got, want)
	}
}

func TestInferInterestsEmptyWhenNothingCorrect(t *testing.T) {
	got := inferInterests([]topicScore{
		{Domain: "verbal", Correct: false, ElapsedMs: 2_000, LimitMs: 60_000},
	})
	if got == nil || len(got) != 0 {
		t.Fatalf("inferInterests = %#v; want an empty, non-nil list", got)
	}
}

// The sampler hands out at most one question per subject, never from a quiz
// the pre-signup player cannot render, keeps the same set across calls, and
// turns the subjects answered correctly into the session topics that signup
// copies into the user's interests.
func TestSamplerDrawsPerSubjectAndInfersInterests(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	var author string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email='system@qwish.internal'`).Scan(&author); err != nil {
		t.Skipf("system author missing: %v", err)
	}
	mk := func(title, domain, qType string) {
		var qid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO quizzes (created_by, title, type, visibility, status, question_count, domain, published_at)
			 VALUES ($1,$2,'knowledge_check','public','published',1,$3, now()) RETURNING id`,
			author, title, domain).Scan(&qid); err != nil {
			t.Fatalf("insert quiz: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
			 VALUES ($1,1,$2,'p','["A","B"]','"A"',60)`, qid, qType); err != nil {
			t.Fatalf("insert question: %v", err)
		}
		t.Cleanup(func() {
			pool.Exec(ctx, `DELETE FROM questions WHERE quiz_id=$1`, qid)
			pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, qid)
		})
	}
	// Other quizzes may exist; these guarantee the draw is not empty.
	mk("sampler-verbal", "verbal", "multiple_choice")
	mk("sampler-logical", "logical", "multiple_choice")
	mk("sampler-puzzle", "aptitude", "puzzle")

	id, err := svc.Create(ctx, "en", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id) })

	first, err := svc.Sampler(ctx, id)
	if err != nil {
		t.Fatalf("Sampler: %v", err)
	}
	if len(first) == 0 || len(first) > samplerSize {
		t.Fatalf("Sampler returned %d questions; want 1..%d", len(first), samplerSize)
	}
	domains := map[string]bool{}
	for _, q := range first {
		if domains[q.Domain] {
			t.Fatalf("subject %q drawn twice", q.Domain)
		}
		domains[q.Domain] = true
		var mixed bool
		pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM questions WHERE quiz_id=$1 AND type <> 'multiple_choice')`,
			q.QuizID).Scan(&mixed)
		if mixed {
			t.Fatalf("drew from quiz %s, which has a non-MCQ question", q.QuizID)
		}
	}
	again, err := svc.Sampler(ctx, id)
	if err != nil || len(again) != len(first) {
		t.Fatalf("second Sampler call = %d questions, %v; want %d", len(again), err, len(first))
	}
	for i := range first {
		if again[i].ID != first[i].ID {
			t.Fatal("second Sampler call changed the set")
		}
	}

	// Answer only the first question correctly.
	var answers []Answer
	for i, q := range first {
		answer := json.RawMessage(`"not an option"`)
		if i == 0 {
			if err := pool.QueryRow(ctx,
				`SELECT correct_answer FROM questions WHERE id=$1`, q.ID).Scan(&answer); err != nil {
				t.Fatalf("read correct answer: %v", err)
			}
		}
		answers = append(answers, Answer{QuestionID: q.ID, Answer: answer, ElapsedMs: 4_000})
	}
	res, err := svc.SubmitSampler(ctx, id, answers)
	if err != nil {
		t.Fatalf("SubmitSampler: %v", err)
	}
	want := first[0].Domain
	if res.TotalCorrect != 1 || len(res.Topics) != len(first) {
		t.Fatalf("result = %d correct over %d topics; want 1 over %d",
			res.TotalCorrect, len(res.Topics), len(first))
	}
	if len(res.Interests) != 1 || res.Interests[0].Slug != want {
		t.Fatalf("interests = %+v; want [%s]", res.Interests, want)
	}
	for _, q := range res.Recommendations {
		if q.Domain == nil || *q.Domain != want {
			t.Fatalf("recommended %q outside the inferred interest %s", q.Title, want)
		}
	}

	_, topics, err := svc.Prefs(ctx, id)
	if err != nil || !slices.Equal(topics, []string{want}) {
		t.Fatalf("session topics = %v, %v; want [%s]", topics, err, want)
	}
	if _, err := svc.SubmitSampler(ctx, id, answers); err != ErrAlreadySubmitted {
		t.Fatalf("second submit = %v; want ErrAlreadySubmitted", err)
	}
}
