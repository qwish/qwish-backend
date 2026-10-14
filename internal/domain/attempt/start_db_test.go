package attempt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/streak"
)

// Start snapshots the delivered set in one statement. Each stored row must
// keep the delivered position, the question's version, and the option order
// the learner was shown, since answers and resume are checked against them.
func TestStartSnapshotsDeliveredSetInOrder(t *testing.T) {
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
	// Shuffled questions exercise a delivered order that differs from position.
	if err = p.QueryRow(ctx, `INSERT INTO quizzes(created_by,title,type,visibility,status,shuffle_questions) VALUES($1,'start snapshot','knowledge_check','public','published',true) RETURNING id`, teacher).Scan(&qid); err != nil {
		t.Fatal(err)
	}
	defer func() {
		p.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, qid)
		p.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, qid)
		p.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, teacher, student)
	}()
	for i := 1; i <= 6; i++ {
		if _, err = p.Exec(ctx, `INSERT INTO questions(quiz_id,position,type,prompt,options,correct_answer) VALUES($1,$2,'multiple_choice',$3,'["A","B","C","D"]','"A"')`,
			qid, i, fmt.Sprintf("Q%d", i)); err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(p, quiz.NewService(p), streak.NewService(p))
	start, err := svc.Start(ctx, student, qid, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(start.Questions) != 6 {
		t.Fatalf("delivered %d questions, want 6", len(start.Questions))
	}
	rows, err := p.Query(ctx, `SELECT aq.question_id, aq.position, aq.question_version_id = q.version_id, aq.option_order, jsonb_array_length(aq.option_choices)
		FROM quiz_attempt_questions aq
		JOIN LATERAL (SELECT qv.id AS version_id FROM question_versions qv WHERE qv.question_id=aq.question_id AND qv.revision=aq.question_revision) q ON true
		WHERE aq.attempt_id=$1 ORDER BY aq.position`, start.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var questionID string
		var position, choices int
		var versionMatches bool
		var order json.RawMessage
		if err := rows.Scan(&questionID, &position, &versionMatches, &order, &choices); err != nil {
			t.Fatal(err)
		}
		want := start.Questions[i]
		if questionID != want.ID || position != i+1 {
			t.Errorf("row %d: question %s at %d, want %s at %d", i, questionID, position, want.ID, i+1)
		}
		if !versionMatches {
			t.Errorf("row %d: question_version_id does not match the delivered revision", i)
		}
		var got, delivered []int
		if err := json.Unmarshal(order, &got); err != nil {
			t.Fatalf("row %d: option_order %s: %v", i, order, err)
		}
		if err := json.Unmarshal(want.OptionOrder, &delivered); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(delivered) {
			t.Errorf("row %d: option_order %v, want %v", i, got, delivered)
		}
		if choices != 4 {
			t.Errorf("row %d: %d option choices frozen, want 4", i, choices)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if i != 6 {
		t.Fatalf("stored %d rows, want 6", i)
	}
}
