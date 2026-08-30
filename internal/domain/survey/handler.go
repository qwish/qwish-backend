package survey

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ db *pgxpool.Pool }

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

type questionInput struct {
	Type     string   `json:"type"`
	Prompt   string   `json:"prompt"`
	Options  []string `json:"options"`
	Required bool     `json:"required"`
}
type createInput struct {
	Title       string          `json:"title"`
	Description *string         `json:"description"`
	Questions   []questionInput `json:"questions"`
}
type answerInput struct {
	QuestionID string `json:"question_id"`
	Value      any    `json:"value"`
}

func validQuestion(q questionInput) bool {
	q.Prompt = strings.TrimSpace(q.Prompt)
	if q.Prompt == "" || len(q.Prompt) > 2000 {
		return false
	}
	switch q.Type {
	case "single_choice", "multiple_choice":
		if len(q.Options) < 2 || len(q.Options) > 20 {
			return false
		}
		seen := map[string]bool{}
		for _, option := range q.Options {
			option = strings.TrimSpace(option)
			if option == "" || len(option) > 500 || seen[option] {
				return false
			}
			seen[option] = true
		}
		return true
	case "rating", "free_text":
		return len(q.Options) == 0
	default:
		return false
	}
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var in createInput
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || strings.TrimSpace(in.Title) == "" || len(in.Title) > 160 || len(in.Questions) == 0 || len(in.Questions) > 100 {
		middleware.BadRequest(w, "invalid survey payload")
		return
	}
	if in.Description != nil {
		trimmed := strings.TrimSpace(*in.Description)
		if len(trimmed) > 4000 {
			middleware.BadRequest(w, "description is too long")
			return
		}
		in.Description = &trimmed
	}
	for i := range in.Questions {
		in.Questions[i].Prompt = strings.TrimSpace(in.Questions[i].Prompt)
		for n := range in.Questions[i].Options {
			in.Questions[i].Options[n] = strings.TrimSpace(in.Questions[i].Options[n])
		}
		if !validQuestion(in.Questions[i]) {
			middleware.BadRequest(w, "invalid survey question")
			return
		}
	}
	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())
	id, slug := uuid.NewString(), uuid.NewString()[:12]
	var createdBy any
	if adminID := middleware.GetAdminID(r); adminID != "" {
		createdBy = adminID
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO anonymous_surveys(id,slug,title,description,status,created_by) VALUES($1,$2,$3,$4,'published',$5)`, id, slug, strings.TrimSpace(in.Title), in.Description, createdBy)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	for i, q := range in.Questions {
		options, _ := json.Marshal(q.Options)
		if _, err = tx.Exec(r.Context(), `INSERT INTO anonymous_survey_questions(survey_id,position,type,prompt,options,required) VALUES($1,$2,$3,$4,$5,$6)`, id, i+1, q.Type, strings.TrimSpace(q.Prompt), options, q.Required); err != nil {
			middleware.InternalError(w)
			return
		}
	}
	if tx.Commit(r.Context()) != nil {
		middleware.InternalError(w)
		return
	}
	h.audit(r.Context(), middleware.GetAdminID(r), "create_anonymous_survey", id)
	middleware.JSON(w, http.StatusCreated, map[string]any{"id": id, "slug": slug, "status": "published"})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.Query(r.Context(), `SELECT s.id,s.slug,s.title,s.description,s.status,s.created_at,COUNT(r.id) FROM anonymous_surveys s LEFT JOIN anonymous_survey_responses r ON r.survey_id=s.id GROUP BY s.id ORDER BY s.created_at DESC`)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, slug, title, status string
		var desc *string
		var created time.Time
		var count int
		if rows.Scan(&id, &slug, &title, &desc, &status, &created, &count) == nil {
			out = append(out, map[string]any{"id": id, "slug": slug, "title": title, "description": desc, "status": status, "created_at": created, "response_count": count})
		}
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *Handler) PublicGet(w http.ResponseWriter, r *http.Request) {
	var id, title, status string
	var desc *string
	err := h.db.QueryRow(r.Context(), `SELECT id,title,description,status FROM anonymous_surveys WHERE slug=$1`, chi.URLParam(r, "slug")).Scan(&id, &title, &desc, &status)
	if errors.Is(err, pgx.ErrNoRows) || status != "published" {
		middleware.NotFound(w, "survey")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT id,type,prompt,options,required FROM anonymous_survey_questions WHERE survey_id=$1 ORDER BY position`, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	qs := []map[string]any{}
	for rows.Next() {
		var qid, t, p string
		var options json.RawMessage
		var required bool
		if rows.Scan(&qid, &t, &p, &options, &required) == nil {
			qs = append(qs, map[string]any{"id": qid, "type": t, "prompt": p, "options": options, "required": required})
		}
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"id": id, "title": title, "description": desc, "questions": qs})
}

func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var in struct {
		Receipt string        `json:"receipt"`
		Answers []answerInput `json:"answers"`
	}
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&in) != nil || len(in.Receipt) < 16 || len(in.Receipt) > 200 {
		middleware.BadRequest(w, "invalid response")
		return
	}
	var surveyID string
	if h.db.QueryRow(r.Context(), `SELECT id FROM anonymous_surveys WHERE slug=$1 AND status='published'`, chi.URLParam(r, "slug")).Scan(&surveyID) != nil {
		middleware.NotFound(w, "survey")
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT id,type,options,required FROM anonymous_survey_questions WHERE survey_id=$1`, surveyID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	expected := map[string]struct {
		typ      string
		options  []string
		required bool
	}{}
	for rows.Next() {
		var id, t string
		var raw []byte
		var req bool
		rows.Scan(&id, &t, &raw, &req)
		var opts []string
		json.Unmarshal(raw, &opts)
		expected[id] = struct {
			typ      string
			options  []string
			required bool
		}{t, opts, req}
	}
	seen := map[string]bool{}
	for _, a := range in.Answers {
		q, ok := expected[a.QuestionID]
		if !ok || seen[a.QuestionID] || !validAnswer(q.typ, q.options, a.Value) || (q.required && blankAnswer(q.typ, a.Value)) {
			middleware.BadRequest(w, "invalid answer")
			return
		}
		seen[a.QuestionID] = true
	}
	for id, q := range expected {
		if q.required && !seen[id] {
			middleware.BadRequest(w, "required answer missing")
			return
		}
	}
	answers, _ := json.Marshal(in.Answers)
	hash := receiptHash(surveyID, in.Receipt)
	_, err = h.db.Exec(r.Context(), `INSERT INTO anonymous_survey_responses(survey_id,receipt_hash,answers) VALUES($1,$2,$3)`, surveyID, hash[:], answers)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			middleware.Error(w, http.StatusConflict, "ALREADY_SUBMITTED", "This browser has already submitted this survey")
			return
		}
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]bool{"submitted": true})
}

func receiptHash(surveyID, receipt string) [sha256.Size]byte {
	return sha256.Sum256([]byte(surveyID + ":" + receipt))
}

func blankAnswer(typ string, value any) bool {
	if typ == "free_text" {
		s, _ := value.(string)
		return strings.TrimSpace(s) == ""
	}
	if typ == "multiple_choice" {
		values, _ := value.([]any)
		return len(values) == 0
	}
	return false
}

func validAnswer(typ string, options []string, value any) bool {
	switch typ {
	case "free_text":
		s, ok := value.(string)
		return ok && len(strings.TrimSpace(s)) <= 4000
	case "rating":
		n, ok := value.(float64)
		return ok && n >= 1 && n <= 5 && n == float64(int(n))
	case "single_choice":
		s, ok := value.(string)
		if !ok {
			return false
		}
		for _, o := range options {
			if s == o {
				return true
			}
		}
		return false
	case "multiple_choice":
		xs, ok := value.([]any)
		if !ok || len(xs) > len(options) {
			return false
		}
		seen := map[string]bool{}
		for _, x := range xs {
			if !validAnswer("single_choice", options, x) {
				return false
			}
			s := x.(string)
			if seen[s] {
				return false
			}
			seen[s] = true
		}
		return true
	}
	return false
}

func (h *Handler) Results(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "surveyId")
	questionRows, err := h.db.Query(r.Context(), `SELECT id,prompt,type FROM anonymous_survey_questions WHERE survey_id=$1 ORDER BY position`, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	questions := []map[string]string{}
	for questionRows.Next() {
		var qid, prompt, typ string
		if questionRows.Scan(&qid, &prompt, &typ) == nil {
			questions = append(questions, map[string]string{"id": qid, "prompt": prompt, "type": typ})
		}
	}
	questionRows.Close()
	rows, err := h.db.Query(r.Context(), `SELECT answers,created_at FROM anonymous_survey_responses WHERE survey_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var answers json.RawMessage
		var created time.Time
		if rows.Scan(&answers, &created) == nil {
			out = append(out, map[string]any{"answers": answers, "created_at": created})
		}
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"questions": questions, "responses": out})
}

func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&in) != nil || (in.Status != "published" && in.Status != "closed") {
		middleware.BadRequest(w, "status must be published or closed")
		return
	}
	tag, err := h.db.Exec(r.Context(), `UPDATE anonymous_surveys SET status=$1,updated_at=now() WHERE id=$2`, in.Status, chi.URLParam(r, "surveyId"))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "survey")
		return
	}
	h.audit(r.Context(), middleware.GetAdminID(r), "set_anonymous_survey_status", chi.URLParam(r, "surveyId"))
	middleware.JSON(w, http.StatusOK, map[string]string{"status": in.Status})
}

func (h *Handler) audit(ctx context.Context, adminID, action, surveyID string) {
	if adminID == "" {
		return
	}
	var name, role string
	if h.db.QueryRow(ctx, `SELECT name,role FROM admin_accounts WHERE id=$1`, adminID).Scan(&name, &role) != nil {
		return
	}
	_, _ = h.db.Exec(ctx, `INSERT INTO audit_log(admin_id,admin_name,admin_role,action_type,target_type,target_id) VALUES($1,$2,$3,$4,'anonymous_survey',$5)`, adminID, name, role, action, surveyID)
}
