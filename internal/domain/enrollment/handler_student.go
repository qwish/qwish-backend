package enrollment

import (
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type StudentHandler struct{ svc *Service }

func NewStudentHandler(svc *Service) *StudentHandler { return &StudentHandler{svc: svc} }

// POST /api/v1/students/join-class  {invite_code}
func (h *StudentHandler) JoinClass(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InviteCode string `json:"invite_code"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.InviteCode == "" {
		middleware.BadRequest(w, "invite_code is required")
		return
	}

	e, err := h.svc.JoinByClassCode(r.Context(), middleware.GetUserID(r), req.InviteCode)
	if err != nil {
		joinError(w, err)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}

// GET /api/v1/users/me/enrollment
//
// Returns null for a student with no institution. numpie keys its shell off
// this: null hides institution navigation and shows the join prompt.
func (h *StudentHandler) Mine(w http.ResponseWriter, r *http.Request) {
	e, err := h.svc.ActiveByUser(r.Context(), middleware.GetUserID(r))
	if err != nil {
		log.Printf("Mine: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, e)
}

// GET /api/v1/users/me/enrollments — every live enrollment, active first.
func (h *StudentHandler) ListMine(w http.ResponseWriter, r *http.Request) {
	out, err := h.svc.ListMine(r.Context(), middleware.GetUserID(r))
	if err != nil {
		log.Printf("ListMine: %v", err)
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

// PUT /api/v1/users/me/active-institution {institution_id}
func (h *StudentHandler) SetActive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InstitutionID string `json:"institution_id"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil || req.InstitutionID == "" {
		middleware.BadRequest(w, "institution_id is required")
		return
	}
	if err := h.svc.SetActive(r.Context(), middleware.GetUserID(r), req.InstitutionID); err != nil {
		if errors.Is(err, ErrNotFound) {
			middleware.NotFound(w, "enrollment")
			return
		}
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "active institute changed"})
}

// POST /api/v1/users/me/enrollments/{enrollmentId}/leave
func (h *StudentHandler) Leave(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Leave(r.Context(), middleware.GetUserID(r), chi.URLParam(r, "enrollmentId")); err != nil {
		if errors.Is(err, ErrNotFound) {
			middleware.NotFound(w, "enrollment")
			return
		}
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "left institute"})
}
