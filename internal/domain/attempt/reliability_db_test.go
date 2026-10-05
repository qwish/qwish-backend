package attempt

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/streak"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestCompletionDoesNotLockQuizAndRetriesReturnSameResponse(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	tag := uuid.NewString()
	var teacher, student, qid string
	if err = p.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role) VALUES(gen_random_uuid(),'Teacher','T',$1,'teacher') RETURNING id`, tag+"t@example.test").Scan(&teacher); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role) VALUES(gen_random_uuid(),'Student','S',$1,'student') RETURNING id`, tag+"s@example.test").Scan(&student); err != nil {
		t.Fatal(err)
	}
	if err = p.QueryRow(ctx, `INSERT INTO quizzes(created_by,title,type,visibility,status) VALUES($1,'concurrency','knowledge_check','public','published') RETURNING id`, teacher).Scan(&qid); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO questions(quiz_id,position,type,prompt,options,correct_answer) VALUES($1,1,'multiple_choice','Q','["A","B"]','"A"')`, qid); err != nil {
		t.Fatal(err)
	}
	defer func() {
		p.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, qid)
		p.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, qid)
		p.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, student, teacher)
	}()
	svc := NewService(p, quiz.NewService(p), streak.NewService(p))
	start, err := svc.Start(ctx, student, qid, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.SubmitAnswer(ctx, student, start.AttemptID, AnswerReq{QuestionID: start.Questions[0].ID, Answer: json.RawMessage(`"A"`)}); err != nil {
		t.Fatal(err)
	}
	lock, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err = lock.Exec(ctx, `SELECT id FROM quizzes WHERE id=$1 FOR NO KEY UPDATE`, qid); err != nil {
		t.Fatal(err)
	}
	budget, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	first, err := svc.Complete(budget, student, start.AttemptID)
	if err != nil {
		t.Fatalf("completion blocked on shared quiz: %v", err)
	}
	_ = lock.Rollback(ctx)
	second, err := svc.Complete(ctx, student, start.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("retry response changed")
	}
	var count int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM points_ledger WHERE user_id=$1 AND reason='quiz_attempt'`, student).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate reward: %d %v", count, err)
	}
	if err = p.QueryRow(ctx, `SELECT count(*) FROM background_jobs WHERE kind='attempt_learning' AND dedupe_key=$1`, start.AttemptID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("missing/duplicate outbox: %d %v", count, err)
	}
}
