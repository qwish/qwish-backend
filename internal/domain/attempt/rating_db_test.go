package attempt

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/streak"
)

// Runs only with TEST_DATABASE_URL pointed at a migrated scratch database.
func TestCompleteUpdatesSkillRating(t *testing.T) {
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

	tag := fmt.Sprintf("rating%d", time.Now().UnixNano())
	var teacher, student, quizID string
	must("teacher", pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', 'teacher') RETURNING id`, "t"+tag).Scan(&teacher))
	must("student", pool.QueryRow(ctx, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', 'student') RETURNING id`, "s"+tag).Scan(&student))
	must("quiz", pool.QueryRow(ctx, `INSERT INTO quizzes (created_by, title, type, visibility, status, published_at)
		VALUES ($1, $2, 'knowledge_check', 'public', 'published', now()) RETURNING id`, teacher, tag).Scan(&quizID))
	for i := 1; i <= 4; i++ {
		_, err := pool.Exec(ctx, `INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer)
			VALUES ($1, $2, 'multiple_choice', $3, '["A","B","C","D"]', '"A"')`, quizID, i, fmt.Sprintf("Q%d %s", i, tag))
		must("question", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, student, teacher)
	})

	svc := NewService(pool, quiz.NewService(pool), streak.NewService(pool))
	play := func(correct int) *CompleteResp {
		t.Helper()
		st, err := svc.Start(ctx, student, quizID, "")
		must("start", err)
		for i, q := range st.Questions {
			ans := json.RawMessage(`"B"`)
			if i < correct {
				ans = json.RawMessage(`"A"`)
			}
			_, err := svc.SubmitAnswer(ctx, student, st.AttemptID, AnswerReq{QuestionID: q.ID, Answer: ans})
			must("answer", err)
		}
		res, err := svc.Complete(ctx, student, st.AttemptID)
		must("complete", err)
		return res
	}

	first := play(3)
	if first.ScorePct != 75 {
		t.Fatalf("score_pct should be plain accuracy, got %v", first.ScorePct)
	}
	if first.QwishScoreDelta <= 0 {
		t.Fatalf("first attempt should raise the rating, delta=%v", first.QwishScoreDelta)
	}
	var n int
	var lb float64
	must("rating", pool.QueryRow(ctx, `SELECT lr.n, ls.qwish_score FROM learner_ratings lr
		JOIN leaderboard_scores ls ON ls.user_id=lr.user_id WHERE lr.user_id=$1`, student).Scan(&n, &lb))
	if n != 4 || math.Abs(lb-first.QwishScore) > 1e-9 {
		t.Fatalf("n=%d leaderboard=%v response=%v", n, lb, first.QwishScore)
	}
	var moved int
	must("questions", pool.QueryRow(ctx, `SELECT COUNT(*) FROM questions WHERE quiz_id=$1 AND rating_n=1`, quizID).Scan(&moved))
	if moved != 4 {
		t.Fatalf("expected 4 questions to take evidence, got %d", moved)
	}

	// Replaying the same questions measures memory, not ability.
	second := play(4)
	if second.QwishScoreDelta != 0 || second.QwishScore != first.QwishScore {
		t.Fatalf("repeat moved rating: %+v", second)
	}
}
