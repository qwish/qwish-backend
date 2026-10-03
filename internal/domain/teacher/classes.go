package teacher

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	qdb "github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/middleware"
)

// POST /api/v1/teacher/classes/{classId}/end — "promote": the class ends,
// members keep it as a past class and rejoin their next one by code.
func (h *Handler) EndClass(w http.ResponseWriter, r *http.Request) {
	classID := chi.URLParam(r, "classId")
	tag, err := h.db.Exec(r.Context(), `UPDATE groups g SET archived_at=now()
		WHERE g.id=$1 AND g.archived_at IS NULL AND g.institution_id=NULLIF($3,'')::uuid
		  AND EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=g.id AND gt.user_id=$2)`,
		classID, middleware.GetUserID(r), middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	if tag.RowsAffected() == 0 {
		middleware.NotFound(w, "class")
		return
	}
	if h.onClassEnded != nil {
		h.onClassEnded(r.Context(), classID)
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "class ended"})
}

// POST /api/v1/teacher/classes/{classId}/reopen — within 90 days of ending.
func (h *Handler) ReopenClass(w http.ResponseWriter, r *http.Request) {
	status, code := qdb.ReopenClass(r.Context(), h.db, chi.URLParam(r, "classId"),
		"institution_id=NULLIF($3,'')::uuid AND EXISTS(SELECT 1 FROM group_teachers gt WHERE gt.group_id=groups.id AND gt.user_id=$2)",
		middleware.GetUserID(r), middleware.GetInstitutionID(r))
	if status != http.StatusOK {
		msg := "This class can't be reopened."
		if code == "CLASS_REOPEN_EXPIRED" {
			msg = "This class ended more than 90 days ago. Ask your institute to create a new class."
		}
		middleware.Error(w, status, code, msg)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"message": "class reopened"})
}
