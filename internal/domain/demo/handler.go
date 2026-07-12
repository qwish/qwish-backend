package demo

import (
	"encoding/json"
	"errors"
	"net/http"

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
