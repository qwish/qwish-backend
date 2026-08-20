package onboardingsession

import (
	"context"
	"testing"
)

func TestSessionLifecycle(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	id, err := svc.Create(ctx, "hi", []string{"verbal", "logical"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id)
	})

	lang, topics, err := svc.Prefs(ctx, id)
	if err != nil || lang != "hi" || len(topics) != 2 {
		t.Fatalf("Prefs = %q, %v, %v; want hi, 2 topics, nil", lang, topics, err)
	}

	if err := svc.UpdatePrefs(ctx, id, "en", []string{"general"}); err != nil {
		t.Fatalf("UpdatePrefs: %v", err)
	}
	lang, topics, _ = svc.Prefs(ctx, id)
	if lang != "en" || len(topics) != 1 || topics[0] != "general" {
		t.Fatalf("after update: %q %v; want en [general]", lang, topics)
	}
}

func TestExpiredSessionIsInvisible(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	id, err := svc.Create(ctx, "en", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id)
	})

	if _, err := pool.Exec(ctx,
		`UPDATE onboarding_sessions SET expires_at = now() - interval '1 minute' WHERE id=$1`, id); err != nil {
		t.Fatalf("age the session: %v", err)
	}

	if _, _, err := svc.Prefs(ctx, id); err != ErrSession {
		t.Fatalf("Prefs on expired session = %v; want ErrSession", err)
	}
	if err := svc.UpdatePrefs(ctx, id, "en", nil); err != ErrSession {
		t.Fatalf("UpdatePrefs on expired session = %v; want ErrSession", err)
	}
}

func TestRecommendationsExcludeNonMCQAndRespectTopics(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	// A system author to hang quizzes off. Migration 023 seeds one.
	var author string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email='system@qwish.internal'`).Scan(&author); err != nil {
		t.Skipf("system author missing: %v", err)
	}

	mk := func(title, domain, qType string) string {
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
		return qid
	}

	wanted := mk("calib-mcq-verbal", "verbal", "multiple_choice")
	puzzle := mk("calib-puzzle-verbal", "verbal", "puzzle")
	offTopic := mk("calib-mcq-logical", "logical", "multiple_choice")

	id, err := svc.Create(ctx, "en", []string{"verbal"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, id) })

	got, err := svc.Recommendations(ctx, id)
	if err != nil {
		t.Fatalf("Recommendations: %v", err)
	}

	ids := map[string]bool{}
	for _, q := range got {
		ids[q.ID] = true
	}
	if !ids[wanted] {
		t.Error("MCQ quiz in a picked topic was not recommended")
	}
	if ids[puzzle] {
		t.Error("quiz with a non-MCQ question was recommended; the player cannot render it")
	}
	if ids[offTopic] {
		t.Error("quiz outside the picked topics was recommended")
	}
}
