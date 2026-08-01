package enrollment

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

type TeacherHandler struct{ svc *Service }

func NewTeacherHandler(svc *Service) *TeacherHandler { return &TeacherHandler{svc: svc} }

// writeScopeError reports whether err was handled, so callers can return early.
func writeScopeError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, ErrNotYourClass):
		middleware.Error(w, http.StatusForbidden, "NOT_IN_YOUR_CLASS",
			"you are not assigned to this class")
		return true
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "student")
		return true
	case err != nil:
		log.Printf("teacher class membership: %v", err)
		middleware.InternalError(w)
		return true
	}
	return false
}

// POST /api/v1/teacher/classes/{classId}/students  {user_id}
func (h *TeacherHandler) AddStudent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		middleware.BadRequest(w, "user_id is required")
		return
	}
	err := h.svc.AddStudentToClass(r.Context(), middleware.GetUserID(r),
		chi.URLParam(r, "classId"), req.UserID)
	if writeScopeError(w, err) {
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "added"})
}

// DELETE /api/v1/teacher/classes/{classId}/students/{userId}
func (h *TeacherHandler) RemoveStudent(w http.ResponseWriter, r *http.Request) {
	err := h.svc.RemoveStudentFromClass(r.Context(), middleware.GetUserID(r),
		chi.URLParam(r, "classId"), chi.URLParam(r, "userId"))
	if writeScopeError(w, err) {
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "removed"})
}
