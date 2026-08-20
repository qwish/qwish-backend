package onboardingsession

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/qwish/backend/internal/domain/attempt"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/streak"
)

func TestClaimAppliesPrefsAndIsSingleUse(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		 VALUES (gen_random_uuid(), 'Calib Tester', 'Calib', $1, 'student') RETURNING id`,
		"calib+"+tag+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) })

	sess, err := svc.Create(ctx, "hi", []string{"verbal", "logical"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, sess) })

	if err := svc.Claim(ctx, sess, userID); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var lang string
	var topics []string
	if err := pool.QueryRow(ctx,
		`SELECT preferred_language, interest_domains FROM users WHERE id=$1`, userID).
		Scan(&lang, &topics); err != nil {
		t.Fatalf("read back user: %v", err)
	}
	if lang != "hi" || len(topics) != 2 {
		t.Fatalf("user prefs = %q %v; want hi and 2 topics", lang, topics)
	}

	// Claiming twice must not reapply or error the caller's signup.
	if err := svc.Claim(ctx, sess, userID); err != ErrSession {
		t.Fatalf("second Claim = %v; want ErrSession", err)
	}
}

func TestClaimOfExpiredSessionReportsErrSession(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	svc := NewService(pool, nil)

	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID string
	pool.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		 VALUES (gen_random_uuid(), 'Calib Expired', 'Calib', $1, 'student') RETURNING id`,
		"calibx+"+tag+"@example.test").Scan(&userID)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID) })

	sess, _ := svc.Create(ctx, "en", nil)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, sess) })
	pool.Exec(ctx, `UPDATE onboarding_sessions SET expires_at = now() - interval '1 minute' WHERE id=$1`, sess)

	if err := svc.Claim(ctx, sess, userID); err != ErrSession {
		t.Fatalf("Claim of expired session = %v; want ErrSession", err)
	}
}

func TestClaimReplaysCalibrationIntoAScoredAttempt(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	quizSvc := quiz.NewService(pool)
	svc := NewService(pool, quizSvc)
	svc.SetAttempts(attempt.NewService(pool, quizSvc, streak.NewService(pool)))

	var author string
	if err := pool.QueryRow(ctx,
		`SELECT id FROM users WHERE email='system@qwish.internal'`).Scan(&author); err != nil {
		t.Skipf("system author missing: %v", err)
	}

	var quizID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO quizzes (created_by, title, type, visibility, status, question_count, domain, published_at)
		 VALUES ($1,'calib-replay','knowledge_check','public','published',2,'general', now()) RETURNING id`,
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

	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		 VALUES (gen_random_uuid(), 'Calib Replay', 'Calib', $1, 'student') RETURNING id`,
		"calibr+"+tag+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM question_responses WHERE attempt_id IN
		                 (SELECT id FROM quiz_attempts WHERE user_id=$1)`, userID)
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE user_id=$1`, userID)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	sess, err := svc.Create(ctx, "en", []string{"general"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM onboarding_sessions WHERE id=$1`, sess) })

	// Both correct, and slow enough that the sub-second guess penalty does not
	// apply — this is what the replay must preserve.
	if _, err := svc.Submit(ctx, sess, quizID, []Answer{
		{QuestionID: q1, Answer: json.RawMessage(`"A"`), ElapsedMs: 6000},
		{QuestionID: q2, Answer: json.RawMessage(`"B"`), ElapsedMs: 7000},
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	if err := svc.Claim(ctx, sess, userID); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var status string
	var scorePct *float64
	var totalQuestions int
	if err := pool.QueryRow(ctx,
		`SELECT status, score_pct, total_questions FROM quiz_attempts
		  WHERE user_id=$1 AND quiz_id=$2`, userID, quizID).
		Scan(&status, &scorePct, &totalQuestions); err != nil {
		t.Fatalf("no attempt was materialised: %v", err)
	}
	if status != "completed" {
		t.Fatalf("attempt status = %q; want completed", status)
	}
	if scorePct == nil || *scorePct <= 0 {
		t.Fatalf("score_pct = %v; want a positive Qwish Score", scorePct)
	}
	if totalQuestions != 2 {
		t.Fatalf("total_questions = %d; want 2", totalQuestions)
	}

	// Both answers landed, and the recorded time is the one the client measured
	// — not the near-zero the DB clock would have produced during replay.
	var answered, fastAnswers int
	pool.QueryRow(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE time_taken_ms < 1000)
		   FROM question_responses qr
		   JOIN quiz_attempts qa ON qa.id = qr.attempt_id
		  WHERE qa.user_id=$1`, userID).Scan(&answered, &fastAnswers)
	if answered != 2 {
		t.Fatalf("recorded %d responses; want 2", answered)
	}
	if fastAnswers != 0 {
		t.Fatalf("%d responses recorded as sub-second; the elapsed time was lost in replay", fastAnswers)
	}
}
