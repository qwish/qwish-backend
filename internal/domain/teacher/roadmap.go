package teacher

// Teacher-panel roadmap endpoints:
//   R1  GET  /teacher/support-reviews
//   R7  GET/PUT /teacher/notification-preferences
//   R9  GET  /teacher/question-bank
//   R10 GET  /teacher/students/{userId}/attempts/{attemptId}
//   R14 POST /teacher/students/{userId}/parent-summaries, GET /public/parent-summaries/{token}
//   R16 GET/PUT /teacher/preferences
// R11 (question generation) lives in generate.go.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	qdb "github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

// ─── R1 — support reviews due ────────────────────────────────────────────────

func (h *Handler) SupportReviews(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.URL.Query().Get("within_days"))
	if err != nil || days < 0 || days > 60 {
		days = 7
	}
	rows, err := h.db.Query(r.Context(), `
		SELECT s.student_id, COALESCE(NULLIF(u.display_name,''),u.full_name,''), s.status, to_char(s.review_on,'YYYY-MM-DD'),
		       (SELECT g.name FROM group_students gs JOIN groups g ON g.id=gs.group_id
		          JOIN group_teachers gt ON gt.group_id=g.id AND gt.user_id=$1
		         WHERE gs.user_id=s.student_id AND g.archived_at IS NULL ORDER BY g.name LIMIT 1)
		  FROM teacher_student_support s
		  JOIN users u ON u.id=s.student_id AND u.deleted_at IS NULL AND `+qdb.LiveMemberSQL("u.id", "$2")+`
		 WHERE s.teacher_id=$1 AND s.status<>'resolved' AND s.review_on IS NOT NULL
		   AND s.review_on <= current_date + $3::int
		 ORDER BY s.review_on, 2`, middleware.GetUserID(r), middleware.GetInstitutionID(r), days)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type review struct {
		StudentID   string  `json:"student_id"`
		StudentName string  `json:"student_name"`
		Status      string  `json:"status"`
		ReviewOn    string  `json:"review_on"`
		ClassName   *string `json:"class_name"`
	}
	out := []review{}
	for rows.Next() {
		var v review
		if rows.Scan(&v.StudentID, &v.StudentName, &v.Status, &v.ReviewOn, &v.ClassName) != nil {
			middleware.InternalError(w)
			return
		}
		out = append(out, v)
	}
	middleware.JSON(w, http.StatusOK, out)
}

// ─── R10 — one attempt, question by question ─────────────────────────────────

type attemptNeighbour struct {
	StudentID   string `json:"student_id"`
	StudentName string `json:"student_name"`
	AttemptID   string `json:"attempt_id"`
}

func (h *Handler) StudentAttempt(w http.ResponseWriter, r *http.Request) {
	teacherID, instID := middleware.GetUserID(r), middleware.GetInstitutionID(r)
	studentID, attemptID := chi.URLParam(r, "userId"), chi.URLParam(r, "attemptId")
	classID := strings.TrimSpace(r.URL.Query().Get("class_id"))
	if !h.canSeeStudent(r, teacherID, instID, studentID) {
		middleware.NotFound(w, "attempt")
		return
	}
	var out struct {
		AttemptID       string            `json:"attempt_id"`
		StudentID       string            `json:"student_id"`
		StudentName     string            `json:"student_name"`
		QuizID          string            `json:"quiz_id"`
		QuizTitle       string            `json:"quiz_title"`
		ClassID         *string           `json:"class_id"`
		ClassName       *string           `json:"class_name"`
		CompletedAt     *time.Time        `json:"completed_at"`
		ScorePct        float64           `json:"score_pct"`
		CorrectCount    int               `json:"correct_count"`
		QuestionCount   int               `json:"question_count"`
		DurationSeconds *int              `json:"duration_seconds"`
		PointsDelta     int64             `json:"points_delta"`
		ClassAverage    *float64          `json:"class_average_pct"`
		Prev            *attemptNeighbour `json:"prev"`
		Next            *attemptNeighbour `json:"next"`
		Responses       []map[string]any  `json:"responses"`
	}
	// The quiz must be the caller's own.
	err := h.db.QueryRow(r.Context(), `
		SELECT qa.id, u.id, COALESCE(NULLIF(u.display_name,''),u.full_name,''), q.id, q.title, qa.completed_at,
		       COALESCE(qa.score_pct,0), COALESCE(qa.total_correct,0), COALESCE(qa.total_questions,0),
		       CASE WHEN qa.completed_at IS NULL THEN NULL ELSE EXTRACT(EPOCH FROM qa.completed_at-qa.started_at)::int END,
		       COALESCE(qa.points_delta,0)
		  FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id JOIN users u ON u.id=qa.user_id
		 WHERE qa.id::text=$1 AND qa.user_id::text=$2 AND q.created_by=$3`, attemptID, studentID, teacherID,
	).Scan(&out.AttemptID, &out.StudentID, &out.StudentName, &out.QuizID, &out.QuizTitle, &out.CompletedAt,
		&out.ScorePct, &out.CorrectCount, &out.QuestionCount, &out.DurationSeconds, &out.PointsDelta)
	if err != nil {
		middleware.NotFound(w, "attempt")
		return
	}

	// Class context: the requested class if the student is in it, else the first shared class.
	_ = h.db.QueryRow(r.Context(), `SELECT g.id::text, g.name FROM groups g
		JOIN group_students gs ON gs.group_id=g.id AND gs.user_id=$1
		JOIN group_teachers gt ON gt.group_id=g.id AND gt.user_id=$2
		WHERE ($3='' OR g.id::text=$3) ORDER BY g.name LIMIT 1`, studentID, teacherID, classID).Scan(&out.ClassID, &out.ClassName)

	if out.ClassID != nil {
		_ = h.db.QueryRow(r.Context(), `SELECT AVG(qa.score_pct)::float8 FROM quiz_attempts qa
			JOIN group_students gs ON gs.user_id=qa.user_id AND gs.group_id::text=$2
			WHERE qa.quiz_id=$1 AND qa.status='completed'`, out.QuizID, *out.ClassID).Scan(&out.ClassAverage)
		// Neighbours: latest completed attempt per classmate, alphabetical.
		nrows, nerr := h.db.Query(r.Context(), `SELECT DISTINCT ON (u.id) u.id, COALESCE(NULLIF(u.display_name,''),u.full_name,''), qa.id
			FROM quiz_attempts qa JOIN users u ON u.id=qa.user_id
			JOIN group_students gs ON gs.user_id=u.id AND gs.group_id::text=$2
			WHERE qa.quiz_id=$1 AND qa.status='completed'
			ORDER BY u.id, qa.completed_at DESC`, out.QuizID, *out.ClassID)
		if nerr == nil {
			list := []attemptNeighbour{}
			for nrows.Next() {
				var n attemptNeighbour
				if nrows.Scan(&n.StudentID, &n.StudentName, &n.AttemptID) == nil {
					list = append(list, n)
				}
			}
			nrows.Close()
			sortNeighbours(list)
			for i, n := range list {
				if n.StudentID == studentID {
					if i > 0 {
						p := list[i-1]
						out.Prev = &p
					}
					if i < len(list)-1 {
						nx := list[i+1]
						out.Next = &nx
					}
				}
			}
		}
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT q.position, q.prompt, q.type,
		       (SELECT c.code FROM question_concepts qc JOIN curriculum_concepts c ON c.id=qc.concept_id WHERE qc.question_id=q.id ORDER BY qc.weight DESC LIMIT 1),
		       qr.answer, q.correct_answer, COALESCE(qr.is_correct,false), qr.id IS NULL OR qr.answer IS NULL OR qr.answer='null'::jsonb,
		       qr.confidence_level, qr.time_taken_ms
		  FROM questions q
		  LEFT JOIN LATERAL (SELECT * FROM question_responses x WHERE x.attempt_id=$1 AND x.question_id=q.id ORDER BY x.submitted_at DESC LIMIT 1) qr ON true
		 WHERE q.quiz_id=$2
		 ORDER BY q.position`, out.AttemptID, out.QuizID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	out.Responses = []map[string]any{}
	for rows.Next() {
		var pos int
		var prompt, qtype string
		var code, confidence *string
		var answer, correct json.RawMessage
		var isCorrect, omitted bool
		var ms *int
		if rows.Scan(&pos, &prompt, &qtype, &code, &answer, &correct, &isCorrect, &omitted, &confidence, &ms) != nil {
			middleware.InternalError(w)
			return
		}
		var ans any
		if !omitted {
			_ = json.Unmarshal(answer, &ans)
		}
		var corr any
		_ = json.Unmarshal(correct, &corr)
		out.Responses = append(out.Responses, map[string]any{
			"position": pos + 1, "prompt": prompt, "type": qtype, "concept_code": code,
			"answer": ans, "correct_answer": corr, "is_correct": isCorrect && !omitted, "omitted": omitted,
			"confidence": confidence, "time_ms": ms,
		})
	}
	middleware.JSON(w, http.StatusOK, out)
}

func sortNeighbours(list []attemptNeighbour) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && strings.ToLower(list[j].StudentName) < strings.ToLower(list[j-1].StudentName); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

// ─── R9 — question bank ──────────────────────────────────────────────────────

func (h *Handler) QuestionBank(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit < 1 || limit > 500 {
		limit = 200
	}
	teacherID := middleware.GetUserID(r)
	args := []any{teacherID, strings.TrimSpace(q.Get("search")), q.Get("type"), q.Get("concept_id"), q.Get("quiz_id"),
		q.Get("aligned") == "true", q.Get("has_distractors") == "true", q.Get("exclude_quiz_id"), limit}
	rows, err := h.db.Query(r.Context(), `
		WITH mine AS (
		  SELECT qu.*, z.title AS quiz_title FROM questions qu JOIN quizzes z ON z.id=qu.quiz_id
		   WHERE z.created_by=$1 AND z.deleted_at IS NULL
		), stats AS (
		  SELECT qr.question_id, COUNT(*) AS responses,
		         AVG(CASE WHEN qr.is_correct THEN 100.0 ELSE 0 END) AS acc,
		         COUNT(*) FILTER (WHERE qr.is_correct=false AND qr.answer IS NOT NULL) > 0 AS wrong
		    FROM question_responses qr WHERE qr.question_id IN (SELECT id FROM mine) GROUP BY qr.question_id
		), concept AS (
		  SELECT DISTINCT ON (qc.question_id) qc.question_id, c.id AS concept_id, c.code
		    FROM question_concepts qc JOIN curriculum_concepts c ON c.id=qc.concept_id
		   WHERE qc.question_id IN (SELECT id FROM mine) ORDER BY qc.question_id, qc.weight DESC
		)
		SELECT m.id, m.quiz_id, m.position, m.type, m.prompt, m.media_url, m.options, m.correct_answer, m.time_limit_seconds, m.clues,
		       m.quiz_title, c.concept_id::text, c.code, s.acc::float8, COALESCE(s.responses,0), COALESCE(s.wrong,false),
		       (SELECT COUNT(DISTINCT o.quiz_id) FROM mine o WHERE o.type=m.type AND lower(o.prompt)=lower(m.prompt))
		  FROM mine m LEFT JOIN stats s ON s.question_id=m.id LEFT JOIN concept c ON c.question_id=m.id
		 WHERE ($2='' OR m.prompt ILIKE '%'||$2||'%')
		   AND ($3='' OR m.type=$3)
		   AND ($4='' OR c.concept_id::text=$4)
		   AND ($5='' OR m.quiz_id::text=$5)
		   AND (NOT $6 OR c.concept_id IS NOT NULL)
		   AND (NOT $7 OR COALESCE(s.wrong,false))
		   AND ($8='' OR m.quiz_id::text<>$8)
		 ORDER BY m.created_at DESC LIMIT $9`, args...)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	quizzes := map[string]bool{}
	for rows.Next() {
		var id, quizID, qtype, prompt, quizTitle string
		var pos, tl, responses, usedIn int
		var media, conceptID, code *string
		var options, correct, clues json.RawMessage
		var acc *float64
		var wrong bool
		if err := rows.Scan(&id, &quizID, &pos, &qtype, &prompt, &media, &options, &correct, &tl, &clues, &quizTitle, &conceptID, &code, &acc, &responses, &wrong, &usedIn); err != nil {
			middleware.InternalError(w)
			return
		}
		quizzes[quizID] = true
		var clueList any
		if len(clues) > 0 {
			clueList = clues
		}
		items = append(items, map[string]any{
			"question": map[string]any{"id": id, "quiz_id": quizID, "position": pos, "type": qtype, "prompt": prompt, "media_url": media,
				"options": options, "correct_answer": correct, "time_limit_seconds": tl, "clues": clueList},
			"quiz_title": quizTitle, "concept_id": conceptID, "concept_code": code, "accuracy_pct": acc,
			"responses": responses, "used_in": usedIn, "has_distractor_data": wrong,
		})
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items), "assessments": len(quizzes)})
}

// ─── R14 — parent summaries ──────────────────────────────────────────────────

type summaryInclude struct {
	Participation bool `json:"participation"`
	Concepts      bool `json:"concepts"`
	AverageScore  bool `json:"average_score"`
	Message       bool `json:"message"`
}

func (h *Handler) CreateParentSummary(teacherURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		teacherID, instID, studentID := middleware.GetUserID(r), middleware.GetInstitutionID(r), chi.URLParam(r, "userId")
		var in struct {
			Period  string         `json:"period"`
			Include summaryInclude `json:"include"`
			Message string         `json:"message"`
		}
		decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil || len(in.Message) > 1000 || !validPeriod(in.Period) {
			middleware.BadRequest(w, "period (YYYY-MM or term) and a message up to 1000 characters are required")
			return
		}
		if !h.canSeeStudent(r, teacherID, instID, studentID) {
			middleware.NotFound(w, "student")
			return
		}
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			middleware.InternalError(w)
			return
		}
		token := base64.RawURLEncoding.EncodeToString(buf)
		include, _ := json.Marshal(in.Include)
		msg := strings.TrimSpace(in.Message)
		if !in.Include.Message {
			msg = ""
		}
		var id string
		var expires time.Time
		err := h.db.QueryRow(r.Context(), `INSERT INTO parent_summaries(student_id,teacher_id,institution_id,period,include,message,token,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,now()+interval '30 days') RETURNING id, expires_at`,
			studentID, teacherID, instID, in.Period, include, msg, token).Scan(&id, &expires)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		middleware.JSON(w, http.StatusCreated, map[string]any{"id": id, "url": strings.TrimRight(teacherURL, "/") + "/p?token=" + token, "expires_at": expires})
	}
}

func validPeriod(p string) bool {
	if p == "term" {
		return true
	}
	_, err := time.Parse("2006-01", p)
	return err == nil
}

func periodWindow(p string, now time.Time) (time.Time, time.Time) {
	if p == "term" {
		from := time.Date(now.Year(), now.Month()-3, 1, 0, 0, 0, 0, time.UTC)
		return from, time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
	from, _ := time.Parse("2006-01", p)
	return from, from.AddDate(0, 1, 0)
}

// PublicParentSummary is unauthenticated: the token is the capability. It
// applies the evidence rules server-side and never exposes teacher notes,
// support plans or misconception signals.
func (h *Handler) PublicParentSummary(w http.ResponseWriter, r *http.Request) {
	var studentID, teacherID, period, message, studentName, teacherName string
	var includeRaw []byte
	err := h.db.QueryRow(r.Context(), `SELECT p.student_id, p.teacher_id, p.period, p.include, p.message,
		COALESCE(NULLIF(s.display_name,''),s.full_name,''), COALESCE(NULLIF(t.display_name,''),t.full_name,'')
		FROM parent_summaries p JOIN users s ON s.id=p.student_id JOIN users t ON t.id=p.teacher_id
		WHERE p.token=$1 AND p.expires_at>now()`, chi.URLParam(r, "token")).
		Scan(&studentID, &teacherID, &period, &includeRaw, &message, &studentName, &teacherName)
	if err != nil {
		middleware.NotFound(w, "summary")
		return
	}
	var inc summaryInclude
	_ = json.Unmarshal(includeRaw, &inc)
	from, to := periodWindow(period, time.Now().UTC())
	out := map[string]any{"student_name": studentName, "teacher_name": teacherName, "period": period,
		"from": from.Format("2006-01-02"), "to": to.AddDate(0, 0, -1).Format("2006-01-02")}
	var className *string
	_ = h.db.QueryRow(r.Context(), `SELECT g.name FROM groups g JOIN group_students gs ON gs.group_id=g.id AND gs.user_id=$1
		JOIN group_teachers gt ON gt.group_id=g.id AND gt.user_id=$2 ORDER BY g.name LIMIT 1`, studentID, teacherID).Scan(&className)
	out["class_name"] = className
	if inc.Participation || inc.AverageScore {
		var n int
		var avg *float64
		_ = h.db.QueryRow(r.Context(), `SELECT COUNT(*), AVG(qa.score_pct)::float8 FROM quiz_attempts qa JOIN quizzes q ON q.id=qa.quiz_id
			WHERE qa.user_id=$1 AND q.created_by=$2 AND qa.status='completed' AND qa.completed_at>=$3 AND qa.completed_at<$4`,
			studentID, teacherID, from, to).Scan(&n, &avg)
		if inc.Participation {
			out["assessments_completed"] = n
		}
		if inc.AverageScore && avg != nil {
			out["average_score"] = int(*avg + 0.5)
		}
	}
	if inc.Concepts {
		rows, err := h.db.Query(r.Context(), `SELECT c.title, COUNT(*) FILTER(WHERE le.is_correct), COUNT(*) FILTER(WHERE NOT le.is_correct), COUNT(DISTINCT le.question_id)
			FROM learning_evidence le JOIN curriculum_concepts c ON c.id=le.concept_id
			WHERE le.user_id=$1 AND NOT le.timed_out AND le.occurred_at>=$2 AND le.occurred_at<$3 GROUP BY c.title ORDER BY c.title`, studentID, from, to)
		if err == nil {
			well, practising := []string{}, []string{}
			gathering := 0
			for rows.Next() {
				var title string
				var correct, wrong, qs int
				if rows.Scan(&title, &correct, &wrong, &qs) != nil {
					continue
				}
				switch {
				case qs < 2:
					gathering++
				case correct >= wrong:
					well = append(well, title)
				default:
					practising = append(practising, title)
				}
			}
			rows.Close()
			out["going_well"], out["practising"], out["still_gathering"] = well, practising, gathering
		}
	}
	if inc.Message && message != "" {
		out["message"] = message
	}
	middleware.JSON(w, http.StatusOK, out)
}

// ─── R16 — synced UI preferences ─────────────────────────────────────────────

func (h *Handler) GetPreferences(w http.ResponseWriter, r *http.Request) {
	var raw []byte
	var updated *time.Time
	err := h.db.QueryRow(r.Context(), `SELECT prefs, updated_at FROM user_preferences WHERE user_id=$1`, middleware.GetUserID(r)).Scan(&raw, &updated)
	if err != nil {
		middleware.JSON(w, http.StatusOK, map[string]any{"prefs": map[string]string{}, "updated_at": nil})
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"prefs": json.RawMessage(raw), "updated_at": updated})
}

func (h *Handler) PutPreferences(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prefs map[string]string `json:"prefs"`
	}
	decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || in.Prefs == nil || len(in.Prefs) > 500 {
		middleware.BadRequest(w, "prefs must be an object of strings, 64 KB at most")
		return
	}
	for k := range in.Prefs {
		if !strings.HasPrefix(k, "qwish") || len(k) > 120 {
			middleware.BadRequest(w, "unexpected preference key")
			return
		}
	}
	raw, _ := json.Marshal(in.Prefs)
	var updated time.Time
	if err := h.db.QueryRow(r.Context(), `INSERT INTO user_preferences(user_id,prefs) VALUES($1,$2)
		ON CONFLICT(user_id) DO UPDATE SET prefs=EXCLUDED.prefs, updated_at=now() RETURNING updated_at`,
		middleware.GetUserID(r), raw).Scan(&updated); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"prefs": in.Prefs, "updated_at": updated})
}

// ─── R7 — teacher notification preferences ───────────────────────────────────

// NotificationTopics lists the channels a teacher controls, with defaults.
var NotificationTopics = map[string]struct{ InApp, Email bool }{
	"overdue_work":         {true, true},
	"follow_up_evidence":   {true, true},
	"suggestion_decisions": {true, false},
	"assessment_decisions": {true, true},
}

type channel struct {
	InApp bool `json:"in_app"`
	Email bool `json:"email"`
}

func (h *Handler) loadNotificationPrefs(r *http.Request, userID string) map[string]channel {
	out := map[string]channel{}
	for k, d := range NotificationTopics {
		out[k] = channel{d.InApp, d.Email}
	}
	var raw []byte
	if h.db.QueryRow(r.Context(), `SELECT prefs FROM teacher_notification_preferences WHERE user_id=$1`, userID).Scan(&raw) == nil {
		stored := map[string]channel{}
		if json.Unmarshal(raw, &stored) == nil {
			for k, v := range stored {
				if _, ok := NotificationTopics[k]; ok {
					out[k] = v
				}
			}
		}
	}
	return out
}

func (h *Handler) GetNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	middleware.JSON(w, http.StatusOK, h.loadNotificationPrefs(r, middleware.GetUserID(r)))
}

func (h *Handler) PutNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	in := map[string]channel{}
	decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	if decoder.Decode(&in) != nil {
		middleware.BadRequest(w, "invalid preferences")
		return
	}
	for k := range in {
		if _, ok := NotificationTopics[k]; !ok {
			middleware.BadRequest(w, "unknown topic "+k)
			return
		}
	}
	raw, _ := json.Marshal(in)
	if _, err := h.db.Exec(r.Context(), `INSERT INTO teacher_notification_preferences(user_id,prefs) VALUES($1,$2)
		ON CONFLICT(user_id) DO UPDATE SET prefs=EXCLUDED.prefs, updated_at=now()`, middleware.GetUserID(r), raw); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, h.loadNotificationPrefs(r, middleware.GetUserID(r)))
}
