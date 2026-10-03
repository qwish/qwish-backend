package enrollment

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type InstitutionHandler struct {
	svc *Service
	db  *pgxpool.Pool
}

func NewInstitutionHandler(svc *Service, db *pgxpool.Pool) *InstitutionHandler {
	return &InstitutionHandler{svc: svc, db: db}
}

// PATCH /api/v1/institution/enrollments/{enrollmentId}/status  {status, reason}
func (h *InstitutionHandler) SetStudentStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"` // active | suspended | graduated | transferred
		Reason string `json:"reason"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}

	enrollmentID := chi.URLParam(r, "enrollmentId")
	err := h.svc.SetStatus(r.Context(), middleware.GetInstitutionID(r), enrollmentID, req.Status)
	switch {
	case errors.Is(err, ErrNotFound):
		middleware.NotFound(w, "student")
		return
	case err != nil:
		log.Printf("SetStudentStatus: %v", err)
		middleware.BadRequest(w, err.Error())
		return
	}
	reason := "status → " + req.Status
	if req.Reason != "" {
		reason += ": " + req.Reason
	}
	h.logAudit(r, middleware.GetUserID(r), middleware.GetInstitutionID(r), "set_enrollment_status", "enrollment", enrollmentID, reason)
	middleware.JSON(w, http.StatusOK, map[string]string{"status": req.Status})
}

// that can't change is reported and skipped, the rest still apply.
func (h *InstitutionHandler) BulkSetStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EnrollmentIDs []string `json:"enrollment_ids"`
		Status        string   `json:"status"`
		Reason        string   `json:"reason"`
	}
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024)).Decode(&req); err != nil || len(req.EnrollmentIDs) == 0 {
		middleware.BadRequest(w, "enrollment_ids and status are required")
		return
	}
	if len(req.EnrollmentIDs) > 5000 {
		middleware.BadRequest(w, "at most 5000 enrollments per request")
		return
	}
	instID, adminID := middleware.GetInstitutionID(r), middleware.GetUserID(r)
	type skip struct {
		EnrollmentID string `json:"enrollment_id"`
		Name         string `json:"name"`
		Reason       string `json:"reason"`
	}
	updated := 0
	skipped := []skip{}
	for _, id := range req.EnrollmentIDs {
		if err := h.svc.SetStatus(r.Context(), instID, id, req.Status); err != nil {
			var name string
			h.db.QueryRow(r.Context(), `SELECT COALESCE(NULLIF(u.display_name,''), e.full_name) FROM enrollments e
				LEFT JOIN users u ON u.id=e.user_id WHERE e.id=$1 AND e.institution_id=$2`, id, instID).Scan(&name)
			reason := err.Error()
			if errors.Is(err, ErrNotFound) {
				reason = "not on your roster"
			}
			skipped = append(skipped, skip{EnrollmentID: id, Name: name, Reason: reason})
			continue
		}
		updated++
	}
	note := fmt.Sprintf("bulk: %d → %s, %d skipped", updated, req.Status, len(skipped))
	if req.Reason != "" {
		note += ": " + req.Reason
	}
	h.logAudit(r, adminID, instID, "set_enrollment_status", "enrollment", "", note)
	middleware.JSON(w, http.StatusOK, map[string]interface{}{"updated": updated, "skipped": skipped})
}

// logAudit mirrors the institution package's writer, so enrollment status
// changes land in the same audit log as everything else.
func (h *InstitutionHandler) logAudit(r *http.Request, adminID, instID, action, targetType, targetID, reason string) {
	var name, role string
	h.db.QueryRow(r.Context(), `SELECT display_name, role FROM users WHERE id=$1`, adminID).Scan(&name, &role)
	h.db.Exec(r.Context(),
		`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason, institution_id)
		 VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8)`,
		adminID, name, role, action, targetType, targetID, reason, instID)
}
