package teacher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/qwish/backend/internal/middleware"
)

// asTeacher runs one request through a chi router with the auth context set.
func asTeacher(t *testing.T, f teacherFixture, userID, method, pattern, path, body string, h http.HandlerFunc) (int, map[string]any, []any) {
	t.Helper()
	r := chi.NewRouter()
	r.Method(method, pattern, h)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyUserID, userID)
	ctx = context.WithValue(ctx, middleware.ContextKeyInstID, f.InstitutionID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	var obj map[string]any
	var arr []any
	if json.Unmarshal(env.Data, &obj) != nil {
		_ = json.Unmarshal(env.Data, &arr)
	}
	return rec.Code, obj, arr
}

func TestRoadmapEndpoints(t *testing.T) {
	pool := openTestDB(t)
	f := seedTeacherFixture(t, pool)
	h := NewHandler(pool)
	ctx := context.Background()

	// R1 — a plan due today shows up; resolved ones don't.
	if _, err := pool.Exec(ctx, `INSERT INTO teacher_student_support(teacher_id,student_id,institution_id,status,review_on)
		VALUES($1,$2,$3,'supporting',current_date)`, f.TeacherID, f.StudentID, f.InstitutionID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM teacher_student_support WHERE teacher_id=$1`, f.TeacherID) })
	code, _, list := asTeacher(t, f, f.TeacherID, "GET", "/r", "/r?within_days=7", "", h.SupportReviews)
	if code != 200 || len(list) != 1 || list[0].(map[string]any)["class_name"] != "Fixture Class" {
		t.Fatalf("support reviews: %d %v", code, list)
	}

	// R10 — attempt drill-down with one answered and one omitted question.
	var q1, q2, attemptID string
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO questions(quiz_id,position,type,prompt,options,correct_answer) VALUES($1,0,'multiple_choice','Q one','["a","b"]','"a"') RETURNING id`, f.QuizID).Scan(&q1))
	must(pool.QueryRow(ctx, `INSERT INTO questions(quiz_id,position,type,prompt,options,correct_answer) VALUES($1,1,'multiple_choice','Q two','["c","d"]','"d"') RETURNING id`, f.QuizID).Scan(&q2))
	must(pool.QueryRow(ctx, `INSERT INTO quiz_attempts(quiz_id,user_id,status,score_pct,total_correct,total_questions,completed_at) VALUES($1,$2,'completed',50,1,2,now()) RETURNING id`, f.QuizID, f.StudentID).Scan(&attemptID))
	_, err := pool.Exec(ctx, `INSERT INTO question_responses(attempt_id,question_id,answer,is_correct,confidence_level,time_taken_ms) VALUES($1,$2,'"a"',true,'very_confident',4000)`, attemptID, q1)
	must(err)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM question_responses WHERE attempt_id=$1`, attemptID) })
	code, obj, _ := asTeacher(t, f, f.TeacherID, "GET", "/s/{userId}/a/{attemptId}", "/s/"+f.StudentID+"/a/"+attemptID, "", h.StudentAttempt)
	if code != 200 {
		t.Fatalf("attempt: %d", code)
	}
	resp := obj["responses"].([]any)
	if len(resp) != 2 || resp[0].(map[string]any)["is_correct"] != true || resp[1].(map[string]any)["omitted"] != true {
		t.Fatalf("attempt responses: %v", resp)
	}
	if obj["class_name"] != "Fixture Class" {
		t.Fatalf("attempt class: %v", obj["class_name"])
	}
	// Another teacher's quiz is invisible.
	code, _, _ = asTeacher(t, f, f.LonerTeacherID, "GET", "/s/{userId}/a/{attemptId}", "/s/"+f.StudentID+"/a/"+attemptID, "", h.StudentAttempt)
	if code != 404 {
		t.Fatalf("attempt for other teacher: %d", code)
	}

	// R9 — bank lists own questions with stats and honours exclude_quiz_id.
	code, obj, _ = asTeacher(t, f, f.TeacherID, "GET", "/b", "/b?search=Q", "", h.QuestionBank)
	if code != 200 || len(obj["items"].([]any)) != 2 {
		t.Fatalf("bank: %d %v", code, obj)
	}
	code, obj, _ = asTeacher(t, f, f.TeacherID, "GET", "/b", "/b?exclude_quiz_id="+f.QuizID, "", h.QuestionBank)
	if code != 200 || len(obj["items"].([]any)) != 0 {
		t.Fatalf("bank exclude: %v", obj)
	}

	// R14 — create, then read publicly; private notes never appear.
	code, obj, _ = asTeacher(t, f, f.TeacherID, "POST", "/s/{userId}/p", "/s/"+f.StudentID+"/p",
		`{"period":"term","include":{"participation":true,"concepts":true,"average_score":true,"message":true},"message":"Practise at home"}`,
		h.CreateParentSummary("https://teacher.example"))
	if code != 201 || !strings.Contains(obj["url"].(string), "https://teacher.example/p?token=") {
		t.Fatalf("parent summary create: %d %v", code, obj)
	}
	token := strings.SplitN(obj["url"].(string), "token=", 2)[1]
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM parent_summaries WHERE student_id=$1`, f.StudentID) })
	code, obj, _ = asTeacher(t, f, "", "GET", "/p/{token}", "/p/"+token, "", h.PublicParentSummary)
	if code != 200 || obj["assessments_completed"] != float64(1) || obj["average_score"] != float64(50) || obj["message"] != "Practise at home" {
		t.Fatalf("public summary: %d %v", code, obj)
	}
	code, _, _ = asTeacher(t, f, "", "GET", "/p/{token}", "/p/nope", "", h.PublicParentSummary)
	if code != 404 {
		t.Fatalf("bad token: %d", code)
	}

	// R16 — round trip, and non-qwish keys are refused.
	code, _, _ = asTeacher(t, f, f.TeacherID, "PUT", "/pr", "/pr", `{"prefs":{"qwish-theme":"dark"}}`, h.PutPreferences)
	if code != 200 {
		t.Fatalf("put prefs: %d", code)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM user_preferences WHERE user_id=$1`, f.TeacherID) })
	code, obj, _ = asTeacher(t, f, f.TeacherID, "GET", "/pr", "/pr", "", h.GetPreferences)
	if code != 200 || obj["prefs"].(map[string]any)["qwish-theme"] != "dark" || obj["updated_at"] == nil {
		t.Fatalf("get prefs: %v", obj)
	}
	code, _, _ = asTeacher(t, f, f.TeacherID, "PUT", "/pr", "/pr", `{"prefs":{"access_token":"x"}}`, h.PutPreferences)
	if code != 400 {
		t.Fatalf("token key must be refused: %d", code)
	}

	// R7 — defaults, then a saved override.
	code, obj, _ = asTeacher(t, f, f.TeacherID, "GET", "/n", "/n", "", h.GetNotificationPrefs)
	if code != 200 || obj["suggestion_decisions"].(map[string]any)["email"] != false {
		t.Fatalf("notif defaults: %v", obj)
	}
	code, obj, _ = asTeacher(t, f, f.TeacherID, "PUT", "/n", "/n", `{"suggestion_decisions":{"in_app":false,"email":true}}`, h.PutNotificationPrefs)
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM teacher_notification_preferences WHERE user_id=$1`, f.TeacherID) })
	if code != 200 || obj["suggestion_decisions"].(map[string]any)["in_app"] != false || obj["overdue_work"].(map[string]any)["in_app"] != true {
		t.Fatalf("notif save: %v", obj)
	}
}

func TestGenerateNormalise(t *testing.T) {
	byCode := map[string]concept{"SCI.1": {ID: "c1", Code: "SCI.1"}}
	code := "SCI.1"
	ok := generatedQuestion{Type: "multiple_choice", Prompt: "p", Options: []string{"a", "b"}, CorrectAnswer: json.RawMessage(`"a"`), TimeLimitSeconds: 30, ConceptCode: &code,
		OptionMisconceptions: []map[string]string{{"option": "b", "title": "m"}, {"option": "a", "title": "correct one is dropped"}}}
	item, valid := normalise(ok, byCode, true)
	if !valid || *item["concept_id"].(*string) != "c1" || len(item["option_misconceptions"].([]map[string]string)) != 1 {
		t.Fatalf("valid row: %v %v", valid, item)
	}
	bad := generatedQuestion{Type: "multiple_choice", Prompt: "p", Options: []string{"a", "b"}, CorrectAnswer: json.RawMessage(`"z"`)}
	if _, valid := normalise(bad, byCode, false); valid {
		t.Fatal("correct answer not in options must be dropped")
	}
	order := generatedQuestion{Type: "arrange_order", Prompt: "p", Options: []string{"x", "y"}, CorrectAnswer: json.RawMessage(`["y","x"]`)}
	if _, valid := normalise(order, byCode, false); !valid {
		t.Fatal("arrange_order permutation must pass")
	}
	clue := generatedQuestion{Type: "clue_reveal", Prompt: "p", CorrectAnswer: json.RawMessage(`"ans"`)}
	if _, valid := normalise(clue, byCode, false); valid {
		t.Fatal("clue_reveal without clues must be dropped")
	}
}
