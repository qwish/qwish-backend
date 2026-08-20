package onboardingsession

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

type prefsReq struct {
	Language string   `json:"language"`
	Topics   []string `json:"topics"`
}

// respond maps the package's sentinel errors onto status codes. A missing and
// an expired session are both 404: the caller is anonymous and telling them
// apart only helps someone guessing ids.
func respond(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrBadLanguage):
		middleware.BadRequest(w, "unsupported language")
	case errors.Is(err, ErrBadTopic):
		middleware.BadRequest(w, "unknown topic")
	case errors.Is(err, ErrSession):
		middleware.NotFound(w, "session")
	case errors.Is(err, ErrQuizNotEligible):
		middleware.NotFound(w, "quiz")
	case errors.Is(err, ErrAlreadySubmitted):
		middleware.BadRequest(w, "this calibration was already submitted")
	default:
		middleware.InternalError(w)
	}
}

// POST /api/v1/onboarding/session
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var req prefsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	id, err := h.svc.Create(r.Context(), req.Language, req.Topics)
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"session_id": id})
}

// PATCH /api/v1/onboarding/session/{sessionId}
func (h *Handler) UpdatePrefs(w http.ResponseWriter, r *http.Request) {
	var req prefsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if err := h.svc.UpdatePrefs(r.Context(), chi.URLParam(r, "sessionId"), req.Language, req.Topics); err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/v1/onboarding/session/{sessionId}/recommendations
func (h *Handler) Recommendations(w http.ResponseWriter, r *http.Request) {
	quizzes, err := h.svc.Recommendations(r.Context(), chi.URLParam(r, "sessionId"))
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, quizzes)
}

// GET /api/v1/onboarding/session/{sessionId}/quizzes/{quizId}
func (h *Handler) Questions(w http.ResponseWriter, r *http.Request) {
	questions, err := h.svc.Questions(r.Context(), chi.URLParam(r, "sessionId"), chi.URLParam(r, "quizId"))
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, questions)
}

// POST /api/v1/onboarding/session/{sessionId}/submit
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		QuizID  string   `json:"quiz_id"`
		Answers []Answer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.QuizID == "" {
		middleware.BadRequest(w, "quiz_id and answers are required")
		return
	}
	result, err := h.svc.Submit(r.Context(), chi.URLParam(r, "sessionId"), req.QuizID, req.Answers)
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}
