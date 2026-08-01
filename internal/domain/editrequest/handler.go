package editrequest

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// POST /api/v1/teacher/enrollments/{enrollmentId}/edit-requests
//
//	{field, proposed_value, note}
func (h *Handler) Propose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Field         string `json:"field"`
		ProposedValue string `json:"proposed_value"`
		Note          string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	id, err := h.svc.Propose(r.Context(), middleware.GetUserID(r),
		chi.URLParam(r, "enrollmentId"), req.Field, req.ProposedValue, req.Note)
	switch {
	case errors.Is(err, ErrNotYourClass):
		middleware.Error(w, http.StatusForbidden, "NOT_IN_YOUR_CLASS",
			"this student is not in one of your classes")
		return
	case errors.Is(err, ErrInvalidField):
		middleware.BadRequest(w, "field must be one of roll_number, grade, section, admission_date")
		return
	case err != nil:
		log.Printf("Propose: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

// GET /api/v1/teacher/edit-requests
//
// A teacher's own proposals and where they landed.
func (h *Handler) ListMine(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListForTeacher(r.Context(), middleware.GetUserID(r))
	if err != nil {
		log.Printf("ListMine: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// GET /api/v1/institution/edit-requests?status=pending
func (h *Handler) ListForReview(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ListForInstitution(r.Context(),
		middleware.GetInstitutionID(r), r.URL.Query().Get("status"))
	if err != nil {
		log.Printf("ListForReview: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, list)
}

// PATCH /api/v1/institution/edit-requests/{requestId}  {decision}
func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Decision string `json:"decision"` // approved | rejected
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	err := h.svc.Review(r.Context(), middleware.GetInstitutionID(r), middleware.GetUserID(r),
		chi.URLParam(r, "requestId"), req.Decision)
	switch {
	case errors.Is(err, ErrAlreadyResolved):
		middleware.Error(w, http.StatusConflict, "EDIT_REQUEST_RESOLVED",
			"this request has already been decided")
		return
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "edit request")
		return
	case err != nil:
		log.Printf("Review: %v", err)
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": req.Decision})
}
