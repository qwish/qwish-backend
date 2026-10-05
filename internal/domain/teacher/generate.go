package teacher

// R11 — POST /teacher/questions/generate. Drafts questions with Claude via a
// forced tool call (structured output), validates every row with the same
// rules as the CSV importer, and returns them. Nothing is saved: the teacher
// reviews the rows and imports through the normal question endpoint.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/qwish/backend/internal/jobs"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type Generator struct {
	APIKey string
	Model  string
	Client *http.Client
}

type generatedQuestion struct {
	Type                 string              `json:"type"`
	Prompt               string              `json:"prompt"`
	Options              []string            `json:"options"`
	CorrectAnswer        json.RawMessage     `json:"correct_answer"`
	Clues                []string            `json:"clues"`
	TimeLimitSeconds     int                 `json:"time_limit_seconds"`
	ConceptCode          *string             `json:"concept_code"`
	ConceptID            *string             `json:"concept_id"`
	OptionMisconceptions []map[string]string `json:"option_misconceptions"`
}

var choiceTypes = map[string]bool{"multiple_choice": true, "confidence_based": true, "eliminate_wrong": true, "puzzle": true, "speed_chain": true}

var generateTool = map[string]any{
	"name":        "submit_questions",
	"description": "Submit the drafted quiz questions.",
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"questions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []string{"type", "prompt", "options", "correct_answer", "time_limit_seconds"},
					"properties": map[string]any{
						"type":               map[string]any{"type": "string", "enum": []string{"multiple_choice", "confidence_based", "eliminate_wrong", "puzzle", "speed_chain", "arrange_order", "clue_reveal"}},
						"prompt":             map[string]any{"type": "string"},
						"options":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Answer options; for arrange_order the items; empty for clue_reveal."},
						"correct_answer":     map[string]any{"description": "Exact text of the correct option; for arrange_order an array in the correct order; for clue_reveal the answer text."},
						"clues":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "clue_reveal only, vaguest first."},
						"time_limit_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 120},
						"concept_code":       map[string]any{"type": "string", "description": "One of the provided curriculum concept codes, if any."},
						"option_misconceptions": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "object", "required": []string{"option", "title"}, "properties": map[string]any{"option": map[string]any{"type": "string"}, "title": map[string]any{"type": "string"}}},
						},
					},
				},
			},
		},
		"required": []string{"questions"},
	},
}

type concept struct{ ID, Code, Title, Outcome string }

func (h *Handler) GenerateQuestions(g *Generator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if g == nil || g.APIKey == "" {
			middleware.NotFound(w, "question generation")
			return
		}
		var in struct {
			Topic                 string `json:"topic"`
			Subtopics             string `json:"subtopics"`
			Difficulty            string `json:"difficulty"`
			Count                 int    `json:"count"`
			QuizID                string `json:"quiz_id"`
			SuggestMisconceptions bool   `json:"suggest_misconceptions"`
		}
		decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil || strings.TrimSpace(in.Topic) == "" || len(in.Topic) > 200 || len(in.Subtopics) > 500 || in.Count < 1 || in.Count > 30 {
			middleware.BadRequest(w, "topic and a count of 1–30 are required")
			return
		}
		switch in.Difficulty {
		case "easy", "medium", "hard", "mixed":
		default:
			in.Difficulty = "mixed"
		}

		concepts := []concept{}
		if in.QuizID != "" {
			var owned bool
			if err := h.db.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM quizzes WHERE id::text=$1 AND created_by=$2 AND deleted_at IS NULL)`, in.QuizID, middleware.GetUserID(r)).Scan(&owned); err != nil {
				middleware.InternalError(w)
				return
			}
			if !owned {
				middleware.NotFound(w, "quiz")
				return
			}
			rows, err := h.db.Query(r.Context(), `SELECT c.id::text, c.code, c.title, c.learning_outcome
				FROM quizzes q JOIN quiz_curriculum_units u ON u.quiz_id=q.id JOIN curriculum_concepts c ON c.chapter_id=u.unit_id
				WHERE q.id::text=$1 AND q.created_by=$2 ORDER BY c.code LIMIT 80`, in.QuizID, middleware.GetUserID(r))
			if err == nil {
				for rows.Next() {
					var c concept
					if rows.Scan(&c.ID, &c.Code, &c.Title, &c.Outcome) == nil {
						concepts = append(concepts, c)
					}
				}
				rows.Close()
			}
		}

		prompt := buildGeneratePrompt(in.Topic, in.Subtopics, in.Difficulty, in.Count, in.SuggestMisconceptions, concepts)
		payload := generationPayload{prompt, concepts, in.Count, in.SuggestMisconceptions, in.QuizID, middleware.GetInstitutionID(r)}
		if r.URL.Query().Get("async") == "true" || strings.Contains(r.Header.Get("Prefer"), "respond-async") {
			key := uuid.NewString()
			if supplied := r.Header.Get("Idempotency-Key"); supplied != "" {
				if len(supplied) > 128 {
					middleware.BadRequest(w, "idempotency key is too long")
					return
				}
				key = middleware.GetUserID(r) + ":" + supplied
			}
			err := jobs.Enqueue(r.Context(), h.db, "question_generation", key, middleware.GetUserID(r), payload)
			if err != nil {
				middleware.InternalError(w)
				return
			}
			var id string
			var existingState string
			rawPayload, _ := json.Marshal(payload)
			if err = h.db.QueryRow(r.Context(), `SELECT id,state FROM background_jobs WHERE kind='question_generation' AND dedupe_key=$1 AND owner_id=$2 AND payload=$3::jsonb`, key, middleware.GetUserID(r), rawPayload).Scan(&id, &existingState); err != nil {
				middleware.Error(w, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "this key was used for a different generation request")
				return
			}
			w.Header().Set("Location", "/api/v1/teacher/questions/generation-jobs/"+id)
			middleware.JSON(w, http.StatusAccepted, map[string]any{"job_id": id, "status": existingState})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		out, err := runGeneration(ctx, g, payload)
		if err != nil {
			middleware.Error(w, http.StatusBadGateway, "GENERATION_FAILED", "Question generation is unavailable right now. Try again, or use “Copy a prompt”.")
			return
		}

		middleware.JSON(w, http.StatusOK, map[string]any{"questions": out})
	}
}

func buildGeneratePrompt(topic, subtopics, difficulty string, count int, misconceptions bool, concepts []concept) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Draft %d quiz questions for school students on the topic %q.", count, topic)
	if subtopics != "" {
		fmt.Fprintf(&b, " Cover these subtopics: %s.", subtopics)
	}
	fmt.Fprintf(&b, " Difficulty: %s.", difficulty)
	b.WriteString(" Mix question types: multiple_choice, confidence_based, eliminate_wrong, puzzle, speed_chain, arrange_order, clue_reveal. Mostly multiple_choice.")
	b.WriteString(" For choice types give 3–4 options and set correct_answer to the exact text of one option. For arrange_order, options are the items and correct_answer is the array in the correct order. For clue_reveal, options is empty, correct_answer is a short answer and clues has 2–4 clues from vaguest to most obvious.")
	b.WriteString(" Use time_limit_seconds of 10, 15, 30 or 60. Be factually accurate and age-appropriate.")
	if misconceptions {
		b.WriteString(" For each wrong option that reflects a common misconception, add option_misconceptions entries naming the misconception in under 10 words.")
	}
	if len(concepts) > 0 {
		b.WriteString("\nAlign every question to exactly one of these curriculum concepts and set concept_code:\n")
		for _, c := range concepts {
			fmt.Fprintf(&b, "- %s: %s. %s\n", c.Code, c.Title, c.Outcome)
		}
	}
	b.WriteString("\nSubmit the questions with the submit_questions tool.")
	return b.String()
}

func (g *Generator) call(ctx context.Context, prompt string) ([]generatedQuestion, error) {
	body, _ := json.Marshal(map[string]any{
		"model":       g.Model,
		"max_tokens":  8000,
		"system":      "You write clear, accurate classroom assessment questions for teachers.",
		"messages":    []map[string]any{{"role": "user", "content": prompt}},
		"tools":       []any{generateTool},
		"tool_choice": map[string]any{"type": "tool", "name": "submit_questions"},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", g.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("anthropic: %d %s", res.StatusCode, strings.TrimSpace(string(data[:min(len(data), 300)])))
	}
	var parsed struct {
		Content []struct {
			Type  string          `json:"type"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	for _, c := range parsed.Content {
		if c.Type == "tool_use" {
			var out struct {
				Questions []generatedQuestion `json:"questions"`
			}
			if err := json.Unmarshal(c.Input, &out); err != nil {
				return nil, err
			}
			return out.Questions, nil
		}
	}
	return nil, fmt.Errorf("anthropic: no tool_use block")
}

// normalise applies the importer's rules; invalid rows are dropped.
func normalise(q generatedQuestion, byCode map[string]concept, withMisconceptions bool) (map[string]any, bool) {
	q.Prompt = strings.TrimSpace(q.Prompt)
	if q.Prompt == "" || len(q.Prompt) > 1000 {
		return nil, false
	}
	if q.TimeLimitSeconds < 0 || q.TimeLimitSeconds > 600 {
		q.TimeLimitSeconds = 30
	}
	opts := []string{}
	for _, o := range q.Options {
		if o = strings.TrimSpace(o); o != "" {
			opts = append(opts, o)
		}
	}
	var correct any
	clues := []string{}
	switch {
	case choiceTypes[q.Type]:
		var c string
		if json.Unmarshal(q.CorrectAnswer, &c) != nil || len(opts) < 2 || !contains(opts, strings.TrimSpace(c)) {
			return nil, false
		}
		correct = strings.TrimSpace(c)
	case q.Type == "arrange_order":
		var order []string
		if json.Unmarshal(q.CorrectAnswer, &order) != nil || len(order) < 2 || len(order) != len(opts) {
			return nil, false
		}
		for _, o := range order {
			if !contains(opts, strings.TrimSpace(o)) {
				return nil, false
			}
		}
		correct = order
	case q.Type == "clue_reveal":
		var c string
		if json.Unmarshal(q.CorrectAnswer, &c) != nil || strings.TrimSpace(c) == "" {
			return nil, false
		}
		for _, cl := range q.Clues {
			if cl = strings.TrimSpace(cl); cl != "" {
				clues = append(clues, cl)
			}
		}
		if len(clues) == 0 {
			return nil, false
		}
		opts, correct = []string{}, strings.TrimSpace(c)
	default:
		return nil, false
	}
	var conceptID, conceptCode *string
	if q.ConceptCode != nil {
		if c, ok := byCode[*q.ConceptCode]; ok {
			id, code := c.ID, c.Code
			conceptID, conceptCode = &id, &code
		}
	}
	mis := []map[string]string{}
	if withMisconceptions && choiceTypes[q.Type] {
		for _, m := range q.OptionMisconceptions {
			opt, title := strings.TrimSpace(m["option"]), strings.TrimSpace(m["title"])
			if opt != "" && title != "" && opt != correct && contains(opts, opt) {
				mis = append(mis, map[string]string{"option": opt, "title": title})
			}
		}
	}
	return map[string]any{
		"type": q.Type, "prompt": q.Prompt, "options": opts, "correct_answer": correct, "clues": clues,
		"time_limit_seconds": q.TimeLimitSeconds, "concept_id": conceptID, "concept_code": conceptCode,
		"option_misconceptions": mis,
	}, true
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

type generationPayload struct {
	Prompt         string
	Concepts       []concept
	Count          int
	Misconceptions bool
	QuizID         string
	InstitutionID  string
}

func runGeneration(ctx context.Context, g *Generator, p generationPayload) ([]map[string]any, error) {
	raw, err := g.call(ctx, p.Prompt)
	if err != nil {
		return nil, err
	}
	byCode := map[string]concept{}
	for _, c := range p.Concepts {
		byCode[c.Code] = c
	}
	out := []map[string]any{}
	for _, q := range raw {
		if item, ok := normalise(q, byCode, p.Misconceptions); ok {
			out = append(out, item)
		}
		if len(out) >= p.Count {
			break
		}
	}
	return out, nil
}
func (h *Handler) RegisterGenerationJobs(q *jobs.Queue, g *Generator) {
	q.Register("question_generation", func(ctx context.Context, j jobs.Job) (any, error) {
		var p generationPayload
		if err := json.Unmarshal(j.Payload, &p); err != nil {
			return nil, err
		}
		var allowed bool
		err := h.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u LEFT JOIN institutions i ON i.id=u.institution_id
   WHERE u.id::text=$1 AND u.status='active' AND u.deleted_at IS NULL AND u.role='teacher'
   AND COALESCE(u.institution_id::text,'')=$2 AND (i.id IS NULL OR i.status='verified')
   AND ($3='' OR EXISTS(SELECT 1 FROM quizzes WHERE id::text=$3 AND created_by=u.id AND deleted_at IS NULL)))`, j.Owner, p.InstitutionID, p.QuizID).Scan(&allowed)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, fmt.Errorf("generation access revoked")
		}
		out, err := runGeneration(ctx, g, p)
		if err != nil {
			return nil, err
		}
		return map[string]any{"questions": out}, nil
	})
}
func (h *Handler) GenerationJob(w http.ResponseWriter, r *http.Request) {
	var id, state string
	var result json.RawMessage
	var attempts int
	err := h.db.QueryRow(r.Context(), `SELECT id::text,state,result,attempts FROM background_jobs WHERE id::text=$1 AND kind='question_generation' AND owner_id=$2`, chi.URLParam(r, "jobId"), middleware.GetUserID(r)).Scan(&id, &state, &result, &attempts)
	if err == pgx.ErrNoRows {
		middleware.NotFound(w, "generation job")
		return
	}
	if err != nil {
		middleware.InternalError(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	middleware.JSON(w, 200, map[string]any{"job_id": id, "status": state, "result": result, "attempts": attempts})
}
