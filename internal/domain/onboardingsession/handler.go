package onboardingsession

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/qwish/backend/internal/middleware"
)

func sessionBearer(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return token, token != ""
}

func requireSessionBearer(w http.ResponseWriter, r *http.Request) (string, bool) {
	token, ok := sessionBearer(r)
	if !ok {
		middleware.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "onboarding session credential is required")
	}
	return token, ok
}

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

// GET /api/v1/onboarding/taxonomy
func (h *Handler) Taxonomy(w http.ResponseWriter, r *http.Request) {
	taxonomy, err := h.svc.Taxonomy(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, taxonomy)
}

// PATCH /api/v1/onboarding/session
func (h *Handler) UpdatePrefs(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := requireSessionBearer(w, r)
	if !ok {
		return
	}
	var req prefsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if err := h.svc.UpdatePrefs(r.Context(), sessionID, req.Language, req.Topics); err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/v1/onboarding/session/recommendations
func (h *Handler) Recommendations(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := requireSessionBearer(w, r)
	if !ok {
		return
	}
	quizzes, err := h.svc.Recommendations(r.Context(), sessionID)
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, quizzes)
}

// GET /api/v1/onboarding/session/quizzes/{quizId}
func (h *Handler) Questions(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := requireSessionBearer(w, r)
	if !ok {
		return
	}
	questions, err := h.svc.Questions(r.Context(), sessionID, chi.URLParam(r, "quizId"))
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, questions)
}

// POST /api/v1/onboarding/session/submit
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := requireSessionBearer(w, r)
	if !ok {
		return
	}
	var req struct {
		QuizID  string   `json:"quiz_id"`
		Answers []Answer `json:"answers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.QuizID == "" {
		middleware.BadRequest(w, "quiz_id and answers are required")
		return
	}
	result, err := h.svc.Submit(r.Context(), sessionID, req.QuizID, req.Answers)
	if err != nil {
		respond(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}
