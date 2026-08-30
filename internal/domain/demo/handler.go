package demo

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// GET /api/v1/demo/quizzes
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	quizzes, err := h.svc.List(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, quizzes)
}

// GET /api/v1/demo/quizzes/{quizId} — questions without answers
func (h *Handler) Questions(w http.ResponseWriter, r *http.Request) {
	questions, err := h.svc.Questions(r.Context(), chi.URLParam(r, "quizId"))
	if err != nil {
		if errors.Is(err, ErrNotDemo) {
			middleware.NotFound(w, "quiz")
			return
		}
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, questions)
}

// POST /api/v1/demo/quizzes/{quizId}/score
func (h *Handler) Score(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Answers []Answer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "answers are required")
		return
	}
	result, err := h.svc.Score(r.Context(), chi.URLParam(r, "quizId"), req.Answers)
	if err != nil {
		if errors.Is(err, ErrNotDemo) {
			middleware.NotFound(w, "quiz")
			return
		}
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}

// ── Admin (super_admin) endpoints ────────────────────────────────────────────

// GET /api/v1/admin/demo/quizzes — list demo quizzes with play stats
func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	quizzes, err := h.svc.ListAdmin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, quizzes)
}

// POST /api/v1/admin/demo/quizzes — author a demo quiz
func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req CreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Title == "" || len(req.Questions) == 0 {
		middleware.BadRequest(w, "title and at least one question are required")
		return
	}
	for _, question := range req.Questions {
		if strings.TrimSpace(question.Prompt) == "" || question.TimeLimitSeconds < 0 || question.TimeLimitSeconds > 600 {
			middleware.BadRequest(w, "each question needs a prompt and a time limit from 0 to 600 seconds")
			return
		}
	}
	id, err := h.svc.Create(r.Context(), req)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

// DELETE /api/v1/admin/demo/quizzes/{quizId}
func (h *Handler) AdminDelete(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Delete(r.Context(), chi.URLParam(r, "quizId")); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]bool{"deleted": true})
}

// GET /api/v1/admin/demo/quizzes/{quizId}/analytics
func (h *Handler) AdminAnalytics(w http.ResponseWriter, r *http.Request) {
	a, err := h.svc.Analytics(r.Context(), chi.URLParam(r, "quizId"))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, a)
}
