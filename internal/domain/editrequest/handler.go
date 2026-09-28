package editrequest

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	svc      *Service
	notif    *notification.Service
	panelURL string
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// SetNotifier tells the proposing teacher when an admin decides (R7).
func (h *Handler) SetNotifier(n *notification.Service, teacherPanelURL string) {
	h.notif, h.panelURL = n, teacherPanelURL
}

// POST /api/v1/teacher/enrollments/{enrollmentId}/edit-requests
//
//	{field, proposed_value, note}
func (h *Handler) Propose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Field         string `json:"field"`
		ProposedValue string `json:"proposed_value"`
		Note          string `json:"note"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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

// GET /api/v1/institution/edit-requests/counts
func (h *Handler) CountsForReview(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.CountForInstitution(r.Context(), middleware.GetInstitutionID(r))
	if err != nil {
		log.Printf("CountsForReview: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, c)
}

// PATCH /api/v1/institution/edit-requests/{requestId}  {decision}
func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Decision string `json:"decision"` // approved | rejected
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
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
	if h.notif != nil {
		requestID := chi.URLParam(r, "requestId")
		var teacherID, student, field string
		if h.svc.db.QueryRow(r.Context(), `SELECT r.requested_by, COALESCE(su.display_name, e.full_name), r.field
			FROM student_edit_requests r JOIN enrollments e ON e.id=r.enrollment_id LEFT JOIN users su ON su.id=e.user_id
			WHERE r.id=$1`, requestID).Scan(&teacherID, &student, &field) == nil {
			label := map[string]string{"roll_number": "roll-number", "grade": "grade", "section": "section", "admission_date": "admission-date"}[field]
			h.notif.EmitTeacher(r.Context(), teacherID, notification.TopicSuggestionDecisions, "edit_request", "Suggestion",
				"Your "+label+" correction for "+student+" was "+req.Decision+".", "edit_request:"+requestID, h.panelURL+"/inbox/suggestions")
		}
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": req.Decision})
}
