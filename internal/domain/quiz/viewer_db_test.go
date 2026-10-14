package quiz

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The detail endpoint must report the caller's own has_played/is_saved, as the
// list does: the app hides "Play" on a finished play-and-win quiz from it, and a
// stale false sends the learner into "you have already attempted this quiz".
func TestGetForViewerReportsCallerState(t *testing.T) {
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

	tag := fmt.Sprintf("viewer%d", time.Now().UnixNano())
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	newUser := func(role, label string) string {
		var id string
		must(label, pool.QueryRow(ctx, `
			INSERT INTO users (supabase_uid, full_name, display_name, email, role)
			VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', $2) RETURNING id`,
			label+tag, role).Scan(&id))
		return id
	}
	teacher := newUser("teacher", "teacher")
	player := newUser("student", "player")
	other := newUser("student", "other")
	newQuiz := func(name, kind string) string {
		var id string
		must(name, pool.QueryRow(ctx, `
			INSERT INTO quizzes (created_by, title, type, visibility, status, published_at)
			VALUES ($1, $2, $3, 'public', 'published', now()) RETURNING id`,
			teacher, tag+" "+name, kind).Scan(&id))
		_, err := pool.Exec(ctx, `INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer)
			VALUES ($1, 1, 'multiple_choice', 'Q', '["A","B"]', '"A"')`, id)
		must(name+" question", err)
		return id
	}
	pw := newQuiz("pw", "play_and_win")
	kc := newQuiz("kc", "knowledge_check")
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM saved_quizzes WHERE quiz_id IN ($1, $2)`, pw, kc)
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id IN ($1, $2)`, pw, kc)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id IN ($1, $2)`, pw, kc)
		pool.Exec(ctx, `DELETE FROM users WHERE full_name LIKE '%'||$1`, tag)
	})
	_, err = pool.Exec(ctx, `INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, started_at, completed_at)
		VALUES ($1, $2, 'completed', 50, now() - interval '5 minutes', now())`, pw, player)
	must("attempt", err)
	_, err = pool.Exec(ctx, `INSERT INTO saved_quizzes (user_id, quiz_id) VALUES ($1, $2)`, player, kc)
	must("save", err)

	svc := NewService(pool)
	for _, tc := range []struct {
		name, quiz, viewer string
		played, saved      bool
	}{
		{"finished play-and-win", pw, player, true, false},
		{"someone else's attempt", pw, other, false, false},
		{"saved knowledge check", kc, player, false, true},
		{"not saved by this viewer", kc, other, false, false},
	} {
		got, err := svc.GetForViewer(ctx, tc.quiz, tc.viewer)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got.HasPlayed != tc.played || got.IsSaved != tc.saved {
			t.Errorf("%s: has_played=%v is_saved=%v, want %v %v", tc.name, got.HasPlayed, got.IsSaved, tc.played, tc.saved)
		}
		if len(got.QuestionTypes) != 1 || got.QuestionTypes[0] != "multiple_choice" {
			t.Errorf("%s: question_types=%v", tc.name, got.QuestionTypes)
		}
	}
}
