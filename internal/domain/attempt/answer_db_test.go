package attempt

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/streak"
)

// Pins what one submitted answer writes: the response, learning evidence and
// misconception tags for an enrolled learner, and the combo/clock advance —
// and that a duplicate or invalid answer writes none of it.
func TestSubmitAnswerWritesResponseEvidenceAndCombo(t *testing.T) {
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
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	var inst, teacher, student, other, quizID string
	must("institution", p.QueryRow(ctx, `INSERT INTO institutions(name,type,contact_email,status,student_referral_code,teacher_referral_code) VALUES($1,'school',$1||'@example.test','verified',$1||'s',$1||'t') RETURNING id`, "inst"+tag).Scan(&inst))
	newUser := func(role, label string) string {
		var id string
		must(label, p.QueryRow(ctx, `INSERT INTO users(supabase_uid,full_name,display_name,email,role) VALUES(gen_random_uuid(),$1,$1,$1||'@example.test',$2) RETURNING id`, label+tag, role).Scan(&id))
		return id
	}
	teacher = newUser("teacher", "teacher")
	student = newUser("student", "student")
	other = newUser("student", "other")
	_, err = p.Exec(ctx, `INSERT INTO enrollments(user_id,institution_id,full_name,status) VALUES($1,$2,'Student','active')`, student, inst)
	must("enrollment", err)

	var curriculum, version, chapter, concept, misconception string
	must("curriculum", p.QueryRow(ctx, `INSERT INTO curricula(institution_id,name) VALUES($1,'Maths') RETURNING id`, inst).Scan(&curriculum))
	must("version", p.QueryRow(ctx, `INSERT INTO curriculum_versions(curriculum_id,institution_id,label,subject,grade) VALUES($1,$2,'v1','Maths','8') RETURNING id`, curriculum, inst).Scan(&version))
	must("chapter", p.QueryRow(ctx, `INSERT INTO curriculum_chapters(version_id,title,position) VALUES($1,'Fractions',1) RETURNING id`, version).Scan(&chapter))
	must("concept", p.QueryRow(ctx, `INSERT INTO curriculum_concepts(chapter_id,code,title,position) VALUES($1,'F1','Adding fractions',1) RETURNING id`, chapter).Scan(&concept))
	must("misconception", p.QueryRow(ctx, `INSERT INTO misconceptions(institution_id,concept_id,code,title) VALUES($1,$2,'M1','Adds denominators') RETURNING id`, inst, concept).Scan(&misconception))

	must("quiz", p.QueryRow(ctx, `INSERT INTO quizzes(created_by,title,type,visibility,status) VALUES($1,$2,'knowledge_check','public','published') RETURNING id`, teacher, "answers "+tag).Scan(&quizID))
	var mapped, plain string
	must("q1", p.QueryRow(ctx, `INSERT INTO questions(quiz_id,position,type,prompt,options,correct_answer,time_limit_seconds) VALUES($1,1,'multiple_choice','Q1','["A","B","C"]','"A"',30) RETURNING id`, quizID).Scan(&mapped))
	must("q2", p.QueryRow(ctx, `INSERT INTO questions(quiz_id,position,type,prompt,options,correct_answer,time_limit_seconds) VALUES($1,2,'multiple_choice','Q2','["A","B","C"]','"A"',30) RETURNING id`, quizID).Scan(&plain))
	_, err = p.Exec(ctx, `INSERT INTO question_concepts(question_id,concept_id) VALUES($1,$2)`, mapped, concept)
	must("question concept", err)
	_, err = p.Exec(ctx, `INSERT INTO question_misconception_options(question_id,option_id,misconception_id,reviewed_by)
		SELECT $1, id, $2, $3 FROM question_options WHERE question_id=$1 AND label='B'`, mapped, misconception, teacher)
	must("misconception option", err)
	t.Cleanup(func() {
		ctx := context.Background()
		p.Exec(ctx, `DELETE FROM learning_evidence_misconceptions WHERE evidence_id IN (SELECT id FROM learning_evidence WHERE user_id=$1)`, student)
		p.Exec(ctx, `DELETE FROM learning_evidence WHERE user_id=$1`, student)
		p.Exec(ctx, `DELETE FROM quiz_attempts WHERE quiz_id=$1`, quizID)
		p.Exec(ctx, `DELETE FROM quizzes WHERE id=$1`, quizID)
		p.Exec(ctx, `DELETE FROM misconceptions WHERE id=$1`, misconception)
		p.Exec(ctx, `DELETE FROM curriculum_concepts WHERE id=$1`, concept)
		p.Exec(ctx, `DELETE FROM curriculum_chapters WHERE id=$1`, chapter)
		p.Exec(ctx, `DELETE FROM curriculum_versions WHERE id=$1`, version)
		p.Exec(ctx, `DELETE FROM curricula WHERE id=$1`, curriculum)
		p.Exec(ctx, `DELETE FROM enrollments WHERE user_id=$1`, student)
		p.Exec(ctx, `DELETE FROM users WHERE full_name LIKE '%'||$1`, tag)
		p.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, inst)
	})

	svc := NewService(p, quiz.NewService(p), streak.NewService(p))
	start, err := svc.Start(ctx, student, quizID, "")
	must("start", err)
	option := func(questionID, label string) *string {
		for _, q := range start.Questions {
			if q.ID != questionID {
				continue
			}
			var choices []struct{ ID, Label string }
			must("choices", json.Unmarshal(q.OptionChoices, &choices))
			for _, c := range choices {
				if c.Label == label {
					return &c.ID
				}
			}
		}
		t.Fatalf("no option %s on %s", label, questionID)
		return nil
	}
	answer := func(user, questionID, label string) (*AnswerResp, error) {
		return svc.SubmitAnswer(ctx, user, start.AttemptID, AnswerReq{
			QuestionID: questionID, Answer: json.RawMessage(`"` + label + `"`), OptionID: option(questionID, label)})
	}
	type state struct{ responses, evidence, tags, combo int }
	read := func() state {
		var s state
		must("state", p.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM question_responses WHERE attempt_id=$1),
			(SELECT count(*) FROM learning_evidence WHERE attempt_id=$1),
			(SELECT count(*) FROM learning_evidence_misconceptions m JOIN learning_evidence e ON e.id=m.evidence_id WHERE e.attempt_id=$1),
			(SELECT combo_level FROM quiz_attempts WHERE id=$1)`, start.AttemptID).Scan(&s.responses, &s.evidence, &s.tags, &s.combo))
		return s
	}

	// Correct answer on the unmapped question: response and combo, no evidence.
	got, err := answer(student, plain, "A")
	must("correct answer", err)
	if !got.IsCorrect || got.PointsEarned <= 0 || got.ComboLevel != 1 || string(got.CorrectAnswer) != `"A"` {
		t.Errorf("correct answer: %+v", got)
	}
	if s := read(); s != (state{1, 0, 0, 1}) {
		t.Errorf("after correct answer: %+v", s)
	}

	// The misconception option on the mapped question: evidence plus its tag,
	// and the combo resets.
	got, err = answer(student, mapped, "B")
	must("wrong answer", err)
	if got.IsCorrect || got.PointsEarned != 0 || got.ComboLevel != 0 {
		t.Errorf("wrong answer: %+v", got)
	}
	if s := read(); s != (state{2, 1, 1, 0}) {
		t.Errorf("after wrong answer: %+v", s)
	}

	// Rejected answers change nothing.
	for _, tc := range []struct {
		name, user, question, label, want string
	}{
		{"duplicate", student, mapped, "A", "question already answered"},
		{"someone else's attempt", other, plain, "B", "attempt not found or not in progress"},
	} {
		if _, err := answer(tc.user, tc.question, tc.label); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v, want %q", tc.name, err, tc.want)
		}
	}
	if _, err := svc.SubmitAnswer(ctx, student, start.AttemptID, AnswerReq{QuestionID: uuid.NewString(), Answer: json.RawMessage(`"A"`)}); err == nil || !strings.Contains(err.Error(), "question snapshot not found") {
		t.Errorf("unknown question: err=%v", err)
	}
	if s := read(); s != (state{2, 1, 1, 0}) {
		t.Errorf("rejected answers wrote data: %+v", s)
	}
}
