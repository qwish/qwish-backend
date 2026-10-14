package user

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPaceLabelQuadrants(t *testing.T) {
	for _, tc := range []struct {
		accuracy, share float64
		want            string
	}{
		{90, 0.2, "fast_accurate"},
		{90, 0.6, "slow_steady"},
		{40, 0.1, "rushing"},
		{40, 0.8, "struggling"},
		{70, 1.0 / 3, "slow_steady"}, // the edges belong to accurate and slow
	} {
		if got := paceLabel(tc.accuracy, tc.share); got != tc.want {
			t.Errorf("paceLabel(%v, %v) = %q; want %q", tc.accuracy, tc.share, got, tc.want)
		}
	}
}

func TestDifficultyTierBuckets(t *testing.T) {
	for d, want := range map[float64]string{0.40: "easy", 0.60: "medium", 0.74: "medium", 0.75: "hard", 1.0: "hard"} {
		if got := difficultyTier(d); got != want {
			t.Errorf("difficultyTier(%v) = %q; want %q", d, got, want)
		}
	}
}

func TestPeakWindowNeedsVolumeAndLift(t *testing.T) {
	var hours [24]hourBucket
	hours[9] = hourBucket{answers: 30, correct: 15}  // 50%
	hours[21] = hourBucket{answers: 25, correct: 23} // 92%, 9 PM
	hours[2] = hourBucket{answers: 5, correct: 5}    // 100%, but too few
	got := peakWindow(hours)
	if got == nil || got.StartHour != 21 || got.EndHour != 24 {
		t.Fatalf("peakWindow = %+v; want 21–24", got)
	}
	if got.Accuracy <= got.OverallAccuracy {
		t.Fatalf("peak %v not above overall %v", got.Accuracy, got.OverallAccuracy)
	}

	var flat [24]hourBucket
	flat[9] = hourBucket{answers: 30, correct: 21}
	flat[21] = hourBucket{answers: 30, correct: 21}
	if got := peakWindow(flat); got != nil {
		t.Fatalf("no window beats the overall rate, got %+v", got)
	}
}

// End to end against a real schema: pace, tiers, the campus comparison (with
// its three-learner floor) and this week's activity.
func TestGetPerformance(t *testing.T) {
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

	tag := fmt.Sprintf("perf%d", time.Now().UnixNano())
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	id := func(what, sql string, args ...any) string {
		t.Helper()
		var v string
		must(what, pool.QueryRow(ctx, sql, args...).Scan(&v))
		return v
	}
	newUser := func(label string) string {
		return id(label, `INSERT INTO users (supabase_uid, full_name, display_name, email, role)
			VALUES (gen_random_uuid(), $1, $1, $1||'@example.test', 'student') RETURNING id`, label+tag)
	}
	inst := id("institution", `INSERT INTO institutions (name, type, contact_email, student_referral_code, teacher_referral_code, status)
		VALUES ($1, 'college', $1||'@example.test', $1||'s', $1||'t', 'verified') RETURNING id`, tag)
	teacher := newUser("teacher")
	quiz := id("quiz", `INSERT INTO quizzes (created_by, title, type, visibility, status, published_at, domain, question_count)
		VALUES ($1, $2, 'knowledge_check', 'public', 'published', now(), 'quantitative', 4) RETURNING id`, teacher, tag)
	var questions []string
	for i, diff := range []float64{0.45, 0.45, 0.60, 0.90} {
		questions = append(questions, id("question", `INSERT INTO questions (quiz_id, position, type, prompt, options, correct_answer, time_limit_seconds, difficulty)
			VALUES ($1, $2, 'multiple_choice', 'p', '["A","B"]', '"A"', 60, $3) RETURNING id`, quiz, i+1, diff))
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM question_responses WHERE question_id IN (SELECT id FROM questions WHERE quiz_id=$1)`, quiz)
		pool.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, quiz)
		pool.Exec(ctx, `DELETE FROM questions WHERE quiz_id=$1`, quiz)
		pool.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quiz)
		pool.Exec(ctx, `DELETE FROM enrollments WHERE institution_id=$1`, inst)
		pool.Exec(ctx, `DELETE FROM users WHERE full_name LIKE '%'||$1`, tag)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, inst)
	})
	// play answers every question, the first `right` correctly, each in ms.
	play := func(user string, right int, ms int) {
		attempt := id("attempt", `INSERT INTO quiz_attempts (quiz_id, user_id, status, score_pct, started_at, completed_at)
			VALUES ($1, $2, 'completed', 50, now() - interval '5 minutes', now()) RETURNING id`, quiz, user)
		for i, q := range questions {
			_, err := pool.Exec(ctx, `INSERT INTO question_responses (attempt_id, question_id, answer, is_correct, time_taken_ms)
				VALUES ($1, $2, '"A"', $3, $4)`, attempt, q, i < right, ms)
			must("response", err)
		}
	}
	enroll := func(user string) {
		_, err := pool.Exec(ctx, `INSERT INTO enrollments (institution_id, user_id, full_name, status, joined_at)
			VALUES ($1, $2, 'x', 'active', now())`, inst, user)
		must("enrollment", err)
	}

	me := newUser("me")
	enroll(me)
	play(me, 3, 6000) // 75%, 6s of a 60s limit: fast and accurate

	svc := NewService(pool)
	got, err := svc.GetPerformance(ctx, me, inst)
	if err != nil {
		t.Fatalf("GetPerformance: %v", err)
	}
	if got.Pace == nil || got.Pace.Label != "fast_accurate" || got.Pace.SecondsPerQuestion != 6 {
		t.Fatalf("pace = %+v; want fast_accurate at 6s", got.Pace)
	}
	tiers := map[string]DifficultyTier{}
	for _, d := range got.Difficulty {
		tiers[d.Tier] = d
	}
	if tiers["easy"].Questions != 2 || tiers["easy"].Correct != 2 || tiers["hard"].Correct != 0 {
		t.Fatalf("tiers = %+v", got.Difficulty)
	}
	if got.Week.ThisWeek != 4 || len(got.Week.Days) != 7 || got.Week.Days[6].Questions != 4 {
		t.Fatalf("week = %+v; want today's 4 answers", got.Week)
	}
	if got.Campus != nil {
		t.Fatalf("campus shown with no peers: %+v", got.Campus)
	}

	// Two peers is still under the floor; the third unlocks the comparison.
	for i := range 3 {
		peer := newUser(fmt.Sprintf("peer%d", i))
		enroll(peer)
		play(peer, 2, 30000) // 50%
		got, err = svc.GetPerformance(ctx, me, inst)
		if err != nil {
			t.Fatalf("GetPerformance: %v", err)
		}
		if i < 2 && got.Campus != nil {
			t.Fatalf("campus shown with %d peers", i+1)
		}
	}
	if got.Campus == nil || got.Campus.CampusAccuracy != 50 || got.Campus.Accuracy != 75 {
		t.Fatalf("campus = %+v; want 75 vs 50", got.Campus)
	}
	if len(got.Domains) != 1 || got.Domains[0].CampusAvgScore == nil || *got.Domains[0].CampusAvgScore != 50 {
		t.Fatalf("domains = %+v; want quantitative with campus 50", got.Domains)
	}
}
