package enrollment

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/qwish/backend/internal/middleware"
)

// POST /api/v1/institution/promotions
//
// One class, the students chosen out of it, and where they go. The admin may
// name an existing target class or ask for a new one in the same step, so the
// flow never bounces them to the Groups page half way through.
func (h *InstitutionHandler) CreatePromotion(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)
	adminID := middleware.GetUserID(r)

	var req struct {
		SourceGroupID string `json:"source_group_id"`
		TargetGroupID string `json:"target_group_id"`
		// Creates the target class when target_group_id is absent.
		NewGroupName string   `json:"new_group_name"`
		ToGrade      string   `json:"to_grade"`
		ToSection    string   `json:"to_section"`
		Promote      []string `json:"promote_enrollment_ids"`
		Retained     []struct {
			EnrollmentID string `json:"enrollment_id"`
			Reason       string `json:"reason"`
		} `json:"retained"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return
	}
	if req.ToGrade == "" {
		middleware.BadRequest(w, "to_grade is required")
		return
	}
	if req.TargetGroupID == "" && req.NewGroupName == "" {
		middleware.BadRequest(w, "choose a target class or name a new one")
		return
	}

	// Both ends must belong to the caller's institution. Without this a crafted
	// group id would move students into another school's class.
	for _, id := range []string{req.SourceGroupID, req.TargetGroupID} {
		if id == "" {
			continue
		}
		var owned bool
		h.db.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM groups WHERE id=$1 AND institution_id=$2)`, id, instID,
		).Scan(&owned)
		if !owned {
			middleware.NotFound(w, "class")
			return
		}
	}

	targetGroupID := req.TargetGroupID
	if targetGroupID == "" {
		if err := h.db.QueryRow(r.Context(),
			`INSERT INTO groups (institution_id, name, invite_code)
			 VALUES ($1,$2,upper(substr(md5(random()::text),1,8))) RETURNING id`,
			instID, req.NewGroupName,
		).Scan(&targetGroupID); err != nil {
			middleware.InternalError(w)
			return
		}
	}

	retained := make([]RetainedStudent, 0, len(req.Retained))
	for _, r2 := range req.Retained {
		retained = append(retained, RetainedStudent{EnrollmentID: r2.EnrollmentID, Reason: r2.Reason})
	}

	res, err := h.svc.PromoteBatch(r.Context(), instID, adminID, PromotionRequest{
		SourceGroupID: req.SourceGroupID,
		TargetGroupID: targetGroupID,
		ToGrade:       req.ToGrade,
		ToSection:     req.ToSection,
		Promote:       req.Promote,
		Retained:      retained,
	})
	if err != nil {
		if errors.Is(err, ErrNoStudentsChosen) {
			middleware.BadRequest(w, err.Error())
			return
		}
		middleware.InternalError(w)
		return
	}
	h.logAudit(r, adminID, instID, "promote_students", targetGroupID,
		fmt.Sprintf("%d promoted, %d retained, to grade %s", res.Promoted, res.Retained, req.ToGrade))
	middleware.JSON(w, http.StatusCreated, res)
}

// logAudit mirrors the institution package's writer. Promotion moves students
// between classes and changes their grade; an admin action that large belongs in
// the log with everything else.
func (h *InstitutionHandler) logAudit(r *http.Request, adminID, instID, action, targetID, reason string) {
	var name, role string
	h.db.QueryRow(r.Context(), `SELECT display_name, role FROM users WHERE id=$1`, adminID).Scan(&name, &role)
	h.db.Exec(r.Context(),
		`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason, institution_id)
		 VALUES ($1,$2,$3,$4,'group',NULLIF($5,'')::uuid,$6,$7)`,
		adminID, name, role, action, targetID, reason, instID)
}

// GET /api/v1/institution/promotions
//
// Recent promotions, newest first, each carrying whether it can still be undone.
func (h *InstitutionHandler) ListPromotions(w http.ResponseWriter, r *http.Request) {
	instID := middleware.GetInstitutionID(r)

	rows, err := h.db.Query(r.Context(),
		`SELECT pb.id, pb.created_at, u.display_name,
		        COALESCE(sg.name,''), COALESCE(tg.name,''),
		        pb.to_grade, pb.to_section,
		        pb.promoted_count, pb.retained_count,
		        pb.revertible_until, pb.reverted_at, pb.reverted_skipped
		   FROM promotion_batches pb
		   JOIN users u ON u.id = pb.performed_by
		   LEFT JOIN groups sg ON sg.id = pb.source_group_id
		   LEFT JOIN groups tg ON tg.id = pb.target_group_id
		  WHERE pb.institution_id=$1
		  ORDER BY pb.created_at DESC LIMIT 25`, instID)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type batch struct {
		ID              string     `json:"id"`
		CreatedAt       time.Time  `json:"created_at"`
		PerformedBy     string     `json:"performed_by"`
		SourceClass     string     `json:"source_class"`
		TargetClass     string     `json:"target_class"`
		ToGrade         string     `json:"to_grade"`
		ToSection       *string    `json:"to_section,omitempty"`
		Promoted        int        `json:"promoted"`
		Retained        int        `json:"retained"`
		RevertibleUntil time.Time  `json:"revertible_until"`
		RevertedAt      *time.Time `json:"reverted_at,omitempty"`
		RevertedSkipped *int       `json:"reverted_skipped,omitempty"`
		// Precomputed so the UI never has to reason about the window itself.
		Revertible bool `json:"revertible"`
	}
	out := []batch{}
	for rows.Next() {
		var b batch
		if err := rows.Scan(&b.ID, &b.CreatedAt, &b.PerformedBy, &b.SourceClass, &b.TargetClass,
			&b.ToGrade, &b.ToSection, &b.Promoted, &b.Retained,
			&b.RevertibleUntil, &b.RevertedAt, &b.RevertedSkipped); err != nil {
			middleware.InternalError(w)
			return
		}
		b.Revertible = b.RevertedAt == nil && time.Now().Before(b.RevertibleUntil)
		out = append(out, b)
	}
	middleware.JSON(w, http.StatusOK, out)
}

// POST /api/v1/institution/promotions/{batchId}/revert
func (h *InstitutionHandler) RevertPromotion(w http.ResponseWriter, r *http.Request) {
	res, err := h.svc.RevertBatch(r.Context(),
		middleware.GetInstitutionID(r), middleware.GetUserID(r), chi.URLParam(r, "batchId"))
	switch {
	case errors.Is(err, ErrBatchNotFound):
		middleware.NotFound(w, "promotion")
	case errors.Is(err, ErrBatchAlreadyReverted), errors.Is(err, ErrBatchNotRevertible):
		middleware.Error(w, http.StatusConflict, "NOT_REVERTIBLE", err.Error())
	case err != nil:
		middleware.InternalError(w)
	default:
		h.logAudit(r, middleware.GetUserID(r), middleware.GetInstitutionID(r),
			"revert_promotion", "",
			fmt.Sprintf("%d reverted, %d skipped", res.Reverted, res.Skipped))
		middleware.JSON(w, http.StatusOK, res)
	}
}
