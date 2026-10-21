package quiz

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// A teacher's per-attempt question count must round-trip through update, read
// and duplicate; the attempt engine samples a random subset from it.
func TestTeacherQuestionLimitRoundTrip(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	tag := fmt.Sprintf("qlimit%d", time.Now().UnixNano())
	var teacher, quizID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', 'teacher') RETURNING id`, tag).Scan(&teacher); err != nil {
		t.Fatalf("seed teacher: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO quizzes (created_by, title, type, visibility, status, question_count)
		VALUES ($1, $2, 'knowledge_check', 'public', 'draft', 3) RETURNING id`, teacher, tag).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM quizzes WHERE created_by=$1`, teacher)
		pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, teacher)
	})
	for i := 1; i <= 3; i++ {
		if _, err := pool.Exec(ctx, `INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer)
			VALUES ($1, $2, 'multiple_choice', $3, '["A","B"]', '"A"')`, quizID, i, fmt.Sprintf("Q%d", i)); err != nil {
			t.Fatalf("seed question: %v", err)
		}
	}

	svc := NewService(pool)
	limit := 2
	req := CreateQuizReq{Title: tag, Type: "knowledge_check", Visibility: "public", QuestionLimit: &limit}
	if err := svc.Update(ctx, quizID, teacher, req); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := svc.GetByID(ctx, quizID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.QuestionLimit == nil || *got.QuestionLimit != 2 || got.QuestionCount != 2 {
		t.Fatalf("after update: limit=%v count=%d, want 2/2", got.QuestionLimit, got.QuestionCount)
	}

	dup, err := svc.Duplicate(ctx, quizID, teacher, DuplicateQuizReq{})
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	var dupLimit *int
	pool.QueryRow(ctx, `SELECT question_limit FROM quizzes WHERE id=$1`, dup.ID).Scan(&dupLimit)
	if dupLimit == nil || *dupLimit != 2 {
		t.Fatalf("duplicate limit=%v, want 2", dupLimit)
	}

	req.QuestionLimit = nil
	if err := svc.Update(ctx, quizID, teacher, req); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, _ = svc.GetByID(ctx, quizID); got.QuestionLimit != nil || got.QuestionCount != 3 {
		t.Fatalf("after clear: limit=%v count=%d, want nil/3", got.QuestionLimit, got.QuestionCount)
	}
}

// A teacher's availability window must round-trip, and a quiz whose window has
// already closed must not publish (no learner could ever open it).
func TestTeacherScheduleRoundTripAndPublishGuard(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — skipping database integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	tag := fmt.Sprintf("qsched%d", time.Now().UnixNano())
	var teacher, quizID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', 'teacher') RETURNING id`, tag).Scan(&teacher); err != nil {
		t.Fatalf("seed teacher: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO quizzes (created_by, title, type, visibility, status, question_count)
		VALUES ($1, $2, 'knowledge_check', 'public', 'draft', 1) RETURNING id`, teacher, tag).Scan(&quizID); err != nil {
		t.Fatalf("seed quiz: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM quizzes WHERE created_by=$1`, teacher)
		pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, teacher)
	})

	svc := NewService(pool)
	start, end := time.Now().Add(-2*time.Hour).Truncate(time.Second), time.Now().Add(-time.Hour).Truncate(time.Second)
	req := CreateQuizReq{Title: tag, Type: "knowledge_check", Visibility: "public", StartsAt: &start, EndsAt: &end}
	if err := svc.Update(ctx, quizID, teacher, req); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := svc.GetByID(ctx, quizID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.StartsAt == nil || !got.StartsAt.Equal(start) || got.EndsAt == nil || !got.EndsAt.Equal(end) {
		t.Fatalf("window = %v–%v, want %v–%v", got.StartsAt, got.EndsAt, start, end)
	}
	if _, err := svc.Publish(ctx, quizID, teacher); err == nil {
		t.Fatal("published a quiz whose end time has passed")
	}

	later := time.Now().Add(time.Hour)
	req.EndsAt = &later
	if err := svc.Update(ctx, quizID, teacher, req); err != nil {
		t.Fatalf("update: %v", err)
	}
	if status, err := svc.Publish(ctx, quizID, teacher); err != nil || status != "published" {
		t.Fatalf("publish open window: %q %v", status, err)
	}
}
