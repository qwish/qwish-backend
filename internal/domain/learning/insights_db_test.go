package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/domain/attempt"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/streak"
	"github.com/qwish/backend/internal/middleware"
)

// Run the actual migrations in a private schema, never against application rows.
// TEST_DATABASE_URL must identify a disposable PostgreSQL database with the
// Supabase auth.uid() stub and anon/authenticated/service_role roles installed.
func insightTestDB(t *testing.T, through string) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set — database integration tests skipped")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	schema := "insight_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		admin.Close()
		if err != nil {
			t.Error(err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	_, file, _, _ := runtime.Caller(0)
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(file), "../../../migrations/*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if through != "" && filepath.Base(path)[:3] > through {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sql := strings.NewReplacer("public.", schema+".", "search_path = public", "search_path = "+schema, "search_path=public", "search_path="+schema).Replace(string(raw))
		if _, err = pool.Exec(ctx, sql); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	return pool
}

type insightFixture struct {
	t                                              *testing.T
	db                                             *pgxpool.Pool
	h                                              *Handler
	svc                                            *attempt.Service
	inst, teacher, student, group, concept, m1, m2 string
}

func (f *insightFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(context.Background(), sql, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *insightFixture) id(sql string, args ...any) string {
	f.t.Helper()
	var id string
	if err := f.db.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}
func newInsightFixture(t *testing.T, db *pgxpool.Pool) *insightFixture {
	t.Helper()
	f := &insightFixture{t: t, db: db, h: NewHandler(db, nil), svc: attempt.NewService(db, quiz.NewService(db), streak.NewService(db))}
	tag := uuid.NewString()
	f.inst = f.id(`INSERT INTO institutions(name,type,contact_email,status,student_referral_code,teacher_referral_code) VALUES('School','school',$1,'verified',$2,$3) RETURNING id`, tag+"@school.test", tag+"s", tag+"t")
	f.teacher = f.id(`INSERT INTO users(supabase_uid,full_name,display_name,email,role,institution_id) VALUES(gen_random_uuid(),'Teacher','Teacher',$1,'teacher',$2) RETURNING id`, tag+"t@school.test", f.inst)
	f.student = f.id(`INSERT INTO users(supabase_uid,full_name,display_name,email,role,institution_id) VALUES(gen_random_uuid(),'Student','Student',$1,'student',$2) RETURNING id`, tag+"s@school.test", f.inst)
	f.exec(`INSERT INTO enrollments(institution_id,user_id,full_name,status,joined_at) VALUES($1,$2,'Student','active',now())`, f.inst, f.student)
	f.group = f.id(`INSERT INTO groups(institution_id,name,invite_code) VALUES($1,'Class',$2) RETURNING id`, f.inst, tag)
	f.exec(`INSERT INTO group_teachers(group_id,user_id) VALUES($1,$2)`, f.group, f.teacher)
	f.exec(`INSERT INTO group_students(group_id,user_id) VALUES($1,$2)`, f.group, f.student)
	year := f.id(`INSERT INTO academic_years(institution_id,name,starts_on,ends_on) VALUES($1,'Year','2026-01-01','2026-12-31') RETURNING id`, f.inst)
	curriculum := f.id(`INSERT INTO curricula(institution_id,name) VALUES($1,'Math') RETURNING id`, f.inst)
	version := f.id(`INSERT INTO curriculum_versions(curriculum_id,institution_id,label,subject,grade) VALUES($1,$2,'v1','Math','10') RETURNING id`, curriculum, f.inst)
	chapter := f.id(`INSERT INTO curriculum_chapters(version_id,title,position) VALUES($1,'Fractions',1) RETURNING id`, version)
	f.concept = f.id(`INSERT INTO curriculum_concepts(chapter_id,code,title,position) VALUES($1,'F','Fractions',1) RETURNING id`, chapter)
	f.exec(`UPDATE curriculum_versions SET status='published',published_at=now() WHERE id=$1`, version)
	f.exec(`INSERT INTO class_curricula(institution_id,group_id,academic_year_id,curriculum_id,version_id) VALUES($1,$2,$3,$4,$5)`, f.inst, f.group, year, curriculum, version)
	f.m1 = f.id(`INSERT INTO misconceptions(institution_id,concept_id,code,title,created_by) VALUES($1,$2,'M1','Misconception one',$3) RETURNING id`, f.inst, f.concept, f.teacher)
	f.m2 = f.id(`INSERT INTO misconceptions(institution_id,concept_id,code,title,created_by) VALUES($1,$2,'M2','Misconception two',$3) RETURNING id`, f.inst, f.concept, f.teacher)
	return f
}
func (f *insightFixture) quiz(count int) (string, []string) {
	f.t.Helper()
	id := f.id(`INSERT INTO quizzes(created_by,institution_id,group_id,title,type,visibility,status,curriculum_question_mapping_enabled) VALUES($1,$2,$3,'Quiz','knowledge_check','public','draft',true) RETURNING id`, f.teacher, f.inst, f.group)
	qs := []string{}
	for i := 1; i <= count; i++ {
		q := f.id(`INSERT INTO questions(quiz_id,position,type,prompt,options,correct_answer,time_limit_seconds,clues) VALUES($1,$2,'multiple_choice',$3,'["A","B","C"]','"A"',0,'["Original clue"]') RETURNING id`, id, i, fmt.Sprintf("Question %d %s", i, id))
		f.exec(`INSERT INTO question_concepts(question_id,concept_id,mapped_by) VALUES($1,$2,$3)`, q, f.concept, f.teacher)
		for _, m := range []string{f.m1, f.m2} {
			f.exec(`INSERT INTO question_misconception_options(question_id,option_id,misconception_id,reviewed_by) SELECT $1,id,$2,$3 FROM question_options WHERE question_id=$1 AND label='B'`, q, m, f.teacher)
		}
		qs = append(qs, q)
	}
	return id, qs
}
func (f *insightFixture) start(quizID string) *attempt.StartAttemptResp {
	f.t.Helper()
	f.exec(`UPDATE quizzes SET status='published',published_at=now() WHERE id=$1`, quizID)
	st, err := f.svc.Start(context.Background(), f.student, quizID, "")
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}
func (f *insightFixture) answer(st *attempt.StartAttemptResp, q, label string, option *string) *attempt.AnswerResp {
	f.t.Helper()
	raw, _ := json.Marshal(label)
	resp, err := f.svc.SubmitAnswer(context.Background(), f.student, st.AttemptID, attempt.AnswerReq{QuestionID: q, Answer: raw, OptionID: option, ConfidenceLevel: "very_confident"})
	if err != nil {
		f.t.Fatal(err)
	}
	return resp
}
func (f *insightFixture) call(handler http.HandlerFunc, path string, body any, params map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, f.teacher)
	ctx = context.WithValue(ctx, middleware.ContextKeyInstID, f.inst)
	rc := chi.NewRouteContext()
	for k, v := range params {
		rc.URLParams.Add(k, v)
	}
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rc)
	w := httptest.NewRecorder()
	handler(w, req.WithContext(ctx))
	return w
}
func (f *insightFixture) insights(quizID, query string) []map[string]any {
	f.t.Helper()
	w := f.call(f.h.QuizInsights, "/insights"+query, nil, map[string]string{"quizId": quizID})
	if w.Code != 200 {
		f.t.Fatalf("insights %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		f.t.Fatal(err)
	}
	return body.Data
}
func diagnostic(t *testing.T, rows []map[string]any, id string) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row["misconception_id"] == id {
			return row
		}
	}
	t.Fatalf("missing misconception %s in %+v", id, rows)
	return nil
}

func TestMisconceptionPipelineRegression(t *testing.T) {
	db := insightTestDB(t, "")
	t.Run("cross quiz evidence correct contradictions and multiple tags", func(t *testing.T) {
		f := newInsightFixture(t, db)
		q1, a := f.quiz(1)
		q2, b := f.quiz(2)
		f.answer(f.start(q1), a[0], "B", nil)
		st := f.start(q2)
		f.answer(st, b[0], "B", nil)
		f.answer(st, b[1], "A", nil)
		rows := f.insights(q1, "")
		for _, id := range []string{f.m1, f.m2} {
			row := diagnostic(t, rows, id)
			if row["status"] != "possible_misconception" || row["distinct_questions"] != float64(2) || row["contradictory_correct"] != float64(1) {
				t.Fatalf("incorrect diagnostic %+v", row)
			}
		}
		var count, tags int
		if err := db.QueryRow(context.Background(), `SELECT count(*) FROM learning_evidence WHERE user_id=$1`, f.student).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(context.Background(), `SELECT count(*) FROM learning_evidence_misconceptions em JOIN learning_evidence e ON e.id=em.evidence_id WHERE e.user_id=$1`, f.student).Scan(&tags); err != nil {
			t.Fatal(err)
		}
		if count != 3 || tags != 4 {
			t.Fatalf("expected 3 observations and 4 tags, got %d / %d", count, tags)
		}
	})
	t.Run("reviews override automatic status and return notes", func(t *testing.T) {
		f := newInsightFixture(t, db)
		q, qs := f.quiz(2)
		st := f.start(q)
		for _, id := range qs {
			f.answer(st, id, "B", nil)
		}
		for _, status := range []string{"dismissed", "confirmed"} {
			w := f.call(f.h.Review, "/review", map[string]string{"status": status, "reason": "Teacher checked the work"}, map[string]string{"studentId": f.student, "misconceptionId": f.m1})
			if w.Code != 200 {
				t.Fatalf("review %d: %s", w.Code, w.Body.String())
			}
			row := diagnostic(t, f.insights(q, ""), f.m1)
			if row["status"] != status || row["automatic_status"] != "possible_misconception" || row["review_reason"] != "Teacher checked the work" || row["reviewed_by"] != f.teacher || row["reviewed_at"] == nil {
				t.Fatalf("review not reflected %+v", row)
			}
		}
	})
	t.Run("delivered versions options mappings and clues survive edits", func(t *testing.T) {
		f := newInsightFixture(t, db)
		q, qs := f.quiz(2)
		st := f.start(q)
		wrong := f.id(`SELECT id FROM question_options WHERE question_id=$1 AND label='B'`, qs[0])
		for _, id := range qs {
			f.exec(`UPDATE questions SET correct_answer='"C"',options='["A","C","D"]',time_limit_seconds=1,clues='["Changed clue"]' WHERE id=$1`, id)
		}
		f.exec(`DELETE FROM question_misconception_options WHERE question_id=ANY($1::uuid[])`, qs)
		f.exec(`DELETE FROM question_concepts WHERE question_id=ANY($1::uuid[])`, qs)
		resumed, err := f.svc.ResumeAttempt(context.Background(), f.student, st.AttemptID)
		if err != nil {
			t.Fatal(err)
		}
		for _, question := range resumed.Questions {
			if !strings.Contains(strings.ReplaceAll(string(question.OptionChoices), " ", ""), `"label":"B"`) || !question.CollectConfidence {
				t.Fatalf("resume lost context: %+v", question)
			}
		}
		clue, err := f.svc.RevealClue(context.Background(), f.student, st.AttemptID, qs[0])
		if err != nil || string(clue.Clue) != `"Original clue"` {
			t.Fatalf("clue changed %+v %v", clue, err)
		}
		f.answer(st, qs[0], "ignored", &wrong)
		if got := f.answer(st, qs[1], "A", nil); !got.IsCorrect || string(got.CorrectAnswer) != `"A"` {
			t.Fatalf("graded new answer key: %+v", got)
		}
		row := diagnostic(t, f.insights(q, ""), f.m1)
		if row["error_count"] != float64(1) || row["contradictory_correct"] != float64(1) {
			t.Fatalf("snapshot mappings lost: %+v", row)
		}
	})
	t.Run("timing does not become a knowledge error", func(t *testing.T) {
		f := newInsightFixture(t, db)
		q, qs := f.quiz(2)
		f.exec(`UPDATE questions SET time_limit_seconds=1 WHERE quiz_id=$1`, q)
		st := f.start(q)
		for i, label := range []string{"A", "B"} {
			f.exec(`UPDATE quiz_attempts SET last_answer_at=now()-interval '10 seconds' WHERE id=$1`, st.AttemptID)
			resp := f.answer(st, qs[i], label, nil)
			if !resp.TimedOut || resp.IsCorrect || resp.PointsEarned != 0 {
				t.Fatalf("timeout scoring changed %+v", resp)
			}
		}
		if rows := f.insights(q, ""); len(rows) != 0 {
			t.Fatalf("timeouts became diagnoses %+v", rows)
		}
		var correct, timed int
		if err := db.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE is_correct),count(*) FILTER(WHERE timed_out) FROM learning_evidence WHERE user_id=$1`, f.student).Scan(&correct, &timed); err != nil || correct != 1 || timed != 2 {
			t.Fatalf("correctness lost %d / %d: %v", correct, timed, err)
		}
	})
	t.Run("configurable bounded window", func(t *testing.T) {
		f := newInsightFixture(t, db)
		q, qs := f.quiz(2)
		st := f.start(q)
		for _, id := range qs {
			f.answer(st, id, "B", nil)
		}
		f.exec(`UPDATE learning_evidence SET occurred_at=now()-interval '45 days' WHERE user_id=$1 AND question_id=$2`, f.student, qs[0])
		if row := diagnostic(t, f.insights(q, ""), f.m1); row["status"] != "review_flag" {
			t.Fatalf("old error counted %+v", row)
		}
		if row := diagnostic(t, f.insights(q, "?window_days=60"), f.m1); row["status"] != "possible_misconception" {
			t.Fatalf("window ignored %+v", row)
		}
		f.exec(`UPDATE learning_evidence SET occurred_at=now()-interval '400 days' WHERE user_id=$1`, f.student)
		if rows := f.insights(q, ""); len(rows) != 0 {
			t.Fatalf("expired diagnoses %+v", rows)
		}
		w := f.call(f.h.QuizInsights, "/insights?window_days=0", nil, map[string]string{"quizId": q})
		if w.Code != 400 {
			t.Fatalf("invalid window accepted %d", w.Code)
		}
	})
	t.Run("learning map roundtrip and correct option rejection", func(t *testing.T) {
		f := newInsightFixture(t, db)
		_, qs := f.quiz(1)
		w := f.call(f.h.GetQuestionMap, "/map", nil, map[string]string{"questionId": qs[0]})
		var body struct {
			Data struct {
				Mappings []struct {
					OptionID        string `json:"option_id"`
					MisconceptionID string `json:"misconception_id"`
				} `json:"option_mappings"`
			} `json:"data"`
		}
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Data.Mappings) != 2 {
			t.Fatalf("missing saved maps: %s", w.Body.String())
		}
		w = f.call(f.h.MapQuestion, "/map", map[string]any{"concept_id": f.concept, "weight": 1, "options": body.Data.Mappings}, map[string]string{"questionId": qs[0]})
		if w.Code != 200 {
			t.Fatalf("roundtrip %d: %s", w.Code, w.Body.String())
		}
		correct := f.id(`SELECT id FROM question_options WHERE question_id=$1 AND label='A'`, qs[0])
		w = f.call(f.h.MapQuestion, "/map", map[string]any{"concept_id": f.concept, "options": []map[string]string{{"option_id": correct, "misconception_id": f.m1}}}, map[string]string{"questionId": qs[0]})
		if w.Code != 400 {
			t.Fatalf("correct option accepted %d", w.Code)
		}
		var n int
		if err := db.QueryRow(context.Background(), `SELECT count(*) FROM question_misconception_options WHERE question_id=$1`, qs[0]).Scan(&n); err != nil || n != 2 {
			t.Fatalf("failed map erased existing maps: %d %v", n, err)
		}
	})
	t.Run("cross teacher institute and class scope", func(t *testing.T) {
		f := newInsightFixture(t, db)
		other := newInsightFixture(t, db)
		q, qs := f.quiz(1)
		f.answer(f.start(q), qs[0], "B", nil)
		oq, oqs := other.quiz(1)
		other.answer(other.start(oq), oqs[0], "B", nil)
		if rows := f.insights(oq, ""); len(rows) != 0 {
			t.Fatalf("other institute leaked %+v", rows)
		}
		if rows := f.insights(q, "?class_id="+other.group); len(rows) != 0 {
			t.Fatalf("other class leaked %+v", rows)
		}
		// A different teacher's quiz must not contribute evidence even for our student.
		oq2, oqs2 := f.quiz(1)
		f.answer(f.start(oq2), oqs2[0], "B", nil)
		f.exec(`UPDATE quizzes SET created_by=$1 WHERE id=$2`, other.teacher, oq2)
		if row := diagnostic(t, f.insights(q, ""), f.m1); row["distinct_questions"] != float64(1) {
			t.Fatalf("unauthorized evidence contributed %+v", row)
		}
	})
}

func TestMisconceptionMigrationPreservesAndRepairsLegacyEvidence(t *testing.T) {
	db := insightTestDB(t, "074")
	f := newInsightFixture(t, db)
	q, qs := f.quiz(2)
	f.exec(`UPDATE questions SET time_limit_seconds=1 WHERE quiz_id=$1`, q)
	st := f.start(q)
	for i, answer := range []string{`"A"`, `"B"`} {
		elapsed := 100
		if i == 0 {
			elapsed = 10000
		}
		response := f.id(`INSERT INTO question_responses(attempt_id,question_id,answer,is_correct,time_taken_ms,clues_used,confidence_level,combo_level,points_earned)
   VALUES($1,$2,$3,false,$4,0,'very_confident',0,0) RETURNING id`, st.AttemptID, qs[i], answer, elapsed)
		f.exec(`INSERT INTO learning_evidence(response_id,institution_id,user_id,attempt_id,question_id,question_revision,question_version_id,concept_id,misconception_id,is_correct,confidence_level)
   SELECT $1,$2,$3,$4,$5,aq.question_revision,aq.question_version_id,$6,$7,false,'very_confident' FROM quiz_attempt_questions aq WHERE aq.attempt_id=$4 AND aq.question_id=$5`, response, f.inst, f.student, st.AttemptID, qs[i], f.concept, f.m1)
	}
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../migrations/075_misconception_evidence_integrity.sql"))
	if err != nil {
		t.Fatal(err)
	}
	f.exec(string(raw))
	var correct, timed, tags int
	if err = db.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE is_correct),count(*) FILTER(WHERE timed_out) FROM learning_evidence WHERE user_id=$1`, f.student).Scan(&correct, &timed); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(context.Background(), `SELECT count(*) FROM learning_evidence_misconceptions em JOIN learning_evidence e ON e.id=em.evidence_id WHERE e.user_id=$1`, f.student).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	if correct != 1 || timed != 1 || tags != 1 {
		t.Fatalf("bad backfill: correct=%d timed=%d tags=%d", correct, timed, tags)
	}
	var originalCorrect bool
	if err = db.QueryRow(context.Background(), `SELECT is_correct FROM question_responses WHERE attempt_id=$1 AND question_id=$2`, st.AttemptID, qs[0]).Scan(&originalCorrect); err != nil || originalCorrect {
		t.Fatalf("migration changed historical game score: %v %v", originalCorrect, err)
	}
	var guessed bool
	if err = db.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM quiz_attempt_questions WHERE attempt_id=$1 AND learning_map<>'[]'::jsonb)`, st.AttemptID).Scan(&guessed); err != nil || guessed {
		t.Fatalf("migration invented mapping history: %v %v", guessed, err)
	}
	row := diagnostic(t, f.insights(q, ""), f.m1)
	if row["error_count"] != float64(1) {
		t.Fatalf("backfilled insight %+v", row)
	}
}
