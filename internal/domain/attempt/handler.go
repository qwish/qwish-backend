package attempt

import (
	"encoding/json"
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

// POST /api/v1/quizzes/:quizId/attempts
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	userID := middleware.GetUserID(r)

	resp, err := h.svc.Start(r.Context(), userID, quizID)
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusCreated, resp)
}

// POST /api/v1/attempts/:attemptId/answers
func (h *Handler) SubmitAnswer(w http.ResponseWriter, r *http.Request) {
	var req AnswerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.QuestionID == "" {
		middleware.BadRequest(w, "question_id and answer are required")
		return
	}

	resp, err := h.svc.SubmitAnswer(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "attemptId"), req)
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, resp)
}

// POST /api/v1/attempts/:attemptId/behavior
func (h *Handler) RecordBehavior(w http.ResponseWriter, r *http.Request) {
	var req BehaviorBatch
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid behavior event payload")
		return
	}
	inserted, err := h.svc.RecordBehavior(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "attemptId"), req)
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusAccepted, map[string]int{"accepted": inserted})
}

// POST /api/v1/attempts/:attemptId/questions/:questionId/clue
func (h *Handler) RevealClue(w http.ResponseWriter, r *http.Request) {
	resp, err := h.svc.RevealClue(r.Context(), middleware.GetUserID(r),
		chi.URLParam(r, "attemptId"), chi.URLParam(r, "questionId"))
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, resp)
}

// POST /api/v1/attempts/:attemptId/complete
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	resp, err := h.svc.Complete(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "attemptId"))
	if err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, resp)
}

// GET /api/v1/attempts/:attemptId
func (h *Handler) GetResult(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.GetResult(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "attemptId"))
	if err != nil {
		middleware.NotFound(w, "attempt")
		return
	}
	middleware.JSON(w, http.StatusOK, result)
}

// GET /api/v1/admin/quizzes/:quizId/behavior
func (h *Handler) BehaviorSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := h.svc.BehaviorSummary(r.Context(), chi.URLParam(r, "quizId"))
	if err != nil {
		middleware.NotFound(w, "quiz behavior")
		return
	}
	middleware.JSON(w, http.StatusOK, summary)
}
