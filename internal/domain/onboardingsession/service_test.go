package onboardingsession

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/qwish/backend/internal/domain/quiz"
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

	mk := func(title, domain, subdomain, qType string) string {
		var qid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO quizzes (created_by, title, type, visibility, status, question_count, domain, subdomain, published_at)
			 VALUES ($1,$2,'knowledge_check','public','published',1,$3,$4, now()) RETURNING id`,
			author, title, domain, subdomain).Scan(&qid); err != nil {
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

	wanted := mk("calib-mcq-verbal", "verbal", "verbal_grammar", "multiple_choice")
	puzzle := mk("calib-puzzle-verbal", "verbal", "verbal_grammar", "puzzle")
	offTopic := mk("calib-mcq-logical", "logical", "logical_series", "multiple_choice")

	id, err := svc.Create(ctx, "en", []string{"verbal_grammar"})
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

func TestSubmitGradesAndIsSingleUse(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	quizSvc := quiz.NewService(pool)
	svc := NewService(pool, quizSvc)

	var author string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email='system@qwish.internal'`).Scan(&author); err != nil {
		t.Skipf("system author missing: %v", err)
	}

	var quizID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO quizzes (created_by, title, type, visibility, status, question_count, domain, published_at)
		 VALUES ($1,'calib-submit','knowledge_check','public','published',2,'general', now()) RETURNING id`,
		author).Scan(&quizID); err != nil {
		t.Fatalf("insert quiz: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM questions WHERE quiz_id=$1`, quizID)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quizID)
	})

	var q1, q2 string
	pool.QueryRow(ctx,
		`INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
		 VALUES ($1,1,'multiple_choice','one','["A","B"]','"A"',60) RETURNING id`, quizID).Scan(&q1)
	pool.QueryRow(ctx,
		`INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
		 VALUES ($1,2,'multiple_choice','two','["A","B"]','"B"',60) RETURNING id`, quizID).Scan(&q2)

	sess, err := svc.Create(ctx, "en", []string{"general"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, sess) })

	// Questions must never carry the answer key.
	qs, err := svc.Questions(ctx, sess, quizID)
	if err != nil {
		t.Fatalf("Questions: %v", err)
	}
	if len(qs) != 2 {
		t.Fatalf("got %d questions, want 2", len(qs))
	}
	raw, _ := json.Marshal(qs)
	if bytes.Contains(raw, []byte("correct_answer")) {
		t.Fatal("questions payload contains correct_answer")
	}

	res, err := svc.Submit(ctx, sess, quizID, []Answer{
		{QuestionID: q1, Answer: json.RawMessage(`"A"`), ElapsedMs: 4000},
		{QuestionID: q2, Answer: json.RawMessage(`"A"`), ElapsedMs: 5000},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.TotalCorrect != 1 || res.TotalQuestions != 2 {
		t.Fatalf("got %d/%d, want 1/2", res.TotalCorrect, res.TotalQuestions)
	}
	if len(res.Review) != 2 {
		t.Fatalf("got %d review items, want 2", len(res.Review))
	}

	// A second submit would let a user grind the calibration for a better score.
	if _, err := svc.Submit(ctx, sess, quizID, nil); err != ErrAlreadySubmitted {
		t.Fatalf("second Submit = %v; want ErrAlreadySubmitted", err)
	}
}
