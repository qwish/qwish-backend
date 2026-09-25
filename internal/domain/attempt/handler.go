package attempt

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
	"github.com/qwish/backend/internal/playintegrity"
)

type Handler struct {
	svc       *Service
	integrity *playintegrity.Verifier
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) SetIntegrityVerifier(v *playintegrity.Verifier) { h.integrity = v }

// POST /api/v1/quizzes/:quizId/attempts
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	quizID := chi.URLParam(r, "quizId")
	userID := middleware.GetUserID(r)
	var input struct {
		AssignmentID string `json:"assignment_id"`
	}
	// A body is optional for ordinary catalogue quizzes. When present, reject
	// malformed JSON rather than silently dropping assignment context.
	if r.Body != nil && r.ContentLength != 0 {
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			middleware.BadRequest(w, "invalid attempt context")
			return
		}
	}

	resp, err := h.svc.Start(r.Context(), userID, quizID, input.AssignmentID)
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
	attemptID := chi.URLParam(r, "attemptId")
	if h.integrity != nil && h.integrity.Mode() != playintegrity.Off {
		var input struct {
			IntegrityToken string `json:"integrity_token"`
		}
		if r.Body != nil && r.ContentLength != 0 {
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				middleware.BadRequest(w, "invalid completion payload")
				return
			}
		}
		if input.IntegrityToken != "" {
			result, err := h.integrity.Verify(r.Context(), attemptID, input.IntegrityToken)
			if err != nil {
				log.Printf("[play-integrity] completion verification failed: %v", err)
				if h.integrity.Mode() == playintegrity.Enforce {
					middleware.Error(w, http.StatusForbidden, "INTEGRITY_FAILED", "This quiz completion could not be verified. Please retry from the Play Store app.")
					return
				}
			} else {
				log.Printf("[play-integrity] trusted=%t app=%s license=%s device=%v sdk=%d activity=%s protect=%s access=%v risk=%v", result.Trusted, result.App, result.License, result.Device, result.SDKVersion, result.Activity, result.PlayProtect, result.AppAccess, result.RiskFlags)
				if !result.Trusted && h.integrity.Mode() == playintegrity.Enforce {
					middleware.Error(w, http.StatusForbidden, "INTEGRITY_FAILED", "This quiz completion could not be verified. Please use the Play Store app on a certified device.")
					return
				}
			}
		} else if h.integrity.Mode() == playintegrity.Enforce {
			middleware.Error(w, http.StatusForbidden, "INTEGRITY_REQUIRED", "Update the app to complete this quiz.")
			return
		} else {
			log.Printf("[play-integrity] completion has no token")
		}
	}
	resp, err := h.svc.Complete(r.Context(), middleware.GetUserID(r), attemptID)
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

// GET /api/v1/attempts/:attemptId/session
func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	result, err := h.svc.ResumeAttempt(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "attemptId"))
	if err != nil {
		middleware.NotFound(w, "active attempt")
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
