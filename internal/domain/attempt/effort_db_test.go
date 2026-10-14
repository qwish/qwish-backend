package attempt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/streak"
)

// Runs only with TEST_DATABASE_URL pointed at a migrated scratch database.
// No job worker runs, so question difficulty stays at its seed and every
// learner below faces identical questions.
func TestCompleteDiscountsLowEffortAnswers(t *testing.T) {
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
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}

	tag := fmt.Sprintf("effort%d", time.Now().UnixNano())
	newUser := func(role, name string) string {
		var id string
		must(role, pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
			VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', $2) RETURNING id`, name+tag, role).Scan(&id))
		t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id) })
		return id
	}
	teacher := newUser("teacher", "t")
	var quizID string
	must("quiz", pool.QueryRow(ctx, `INSERT INTO quizzes (created_by, title, type, visibility, status, published_at)
		VALUES ($1, $2, 'knowledge_check', 'public', 'published', now()) RETURNING id`, teacher, tag).Scan(&quizID))
	for i := 1; i <= 4; i++ {
		_, err := pool.Exec(ctx, `INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds)
			VALUES ($1, $2, 'multiple_choice', $3, '["A","B","C","D"]', '"A"', 30)`, quizID, i, fmt.Sprintf("Q%d %s", i, tag))
		must("question", err)
	}

	svc := NewService(pool, quiz.NewService(pool), streak.NewService(pool))

	// play answers 3 of 4 correctly, forces each answer's server time to
	// timeMs, records any behaviour events, then completes.
	play := func(student string, timeMs int, events func(st *StartAttemptResp) []BehaviorEvent) (*CompleteResp, string) {
		t.Helper()
		st, err := svc.Start(ctx, student, quizID, "")
		must("start", err)
		for i, q := range st.Questions {
			ans := json.RawMessage(`"B"`)
			if i < 3 {
				ans = json.RawMessage(`"A"`)
			}
			_, err := svc.SubmitAnswer(ctx, student, st.AttemptID, AnswerReq{QuestionID: q.ID, Answer: ans})
			must("answer", err)
		}
		_, err = pool.Exec(ctx, `UPDATE question_responses SET time_taken_ms=$2 WHERE attempt_id=$1`, st.AttemptID, timeMs)
		must("force time", err)
		if events != nil {
			_, err := svc.RecordBehavior(ctx, student, st.AttemptID, BehaviorBatch{Events: events(st)})
			must("behavior", err)
		}
		res, err := svc.Complete(ctx, student, st.AttemptID)
		must("complete", err)
		return res, st.AttemptID
	}
	discounts := func(attemptID string) map[int]float64 {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT q.position, qr.rating_discount FROM question_responses qr
			JOIN questions q ON q.id=qr.question_id WHERE qr.attempt_id=$1 AND qr.rating_discount IS NOT NULL`, attemptID)
		must("discounts", err)
		defer rows.Close()
		out := map[int]float64{}
		for rows.Next() {
			var pos int
			var d float32
			must("scan", rows.Scan(&pos, &d))
			out[pos] = float64(d)
		}
		return out
	}
	hidden := func(ms int) *int { return &ms }

	considered, aID := play(newUser("student", "a"), 20000, nil)
	if d := discounts(aID); len(d) != 0 {
		t.Fatalf("considered answers should carry no discount, got %v", d)
	}

	rushed, bID := play(newUser("student", "b"), 500, nil)
	if d := discounts(bID); len(d) != 4 || d[1] < 0.89 || d[1] > 0.91 {
		t.Fatalf("every rushed answer should be discounted 0.9, got %v", d)
	}
	if !(rushed.QwishScore < considered.QwishScore) {
		t.Fatalf("rushed %v should rate below considered %v", rushed.QwishScore, considered.QwishScore)
	}

	// Hidden time on question 1 discounts only question 1. Hidden time with no
	// question attached (the app backgrounded between questions) discounts
	// nothing.
	_, cID := play(newUser("student", "c"), 20000, func(st *StartAttemptResp) []BehaviorEvent {
		q0 := st.Questions[0].ID
		return []BehaviorEvent{
			{ClientEventID: uuid.NewString(), EventType: "focus_gained", QuestionID: &q0, HiddenMs: hidden(8000)},
			{ClientEventID: uuid.NewString(), EventType: "focus_gained", HiddenMs: hidden(60000)},
		}
	})
	if d := discounts(cID); len(d) != 1 || d[1] != 0.5 {
		t.Fatalf("only question 1 should be discounted 0.5, got %v", d)
	}
}
