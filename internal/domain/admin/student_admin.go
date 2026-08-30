package admin

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qwish/backend/internal/middleware"
)

// logAuditTx writes an audit entry on the caller's transaction, so the entry
// lands only if the action it describes commits.
//
// The pool-based logAudit above cannot do that: an audit row written outside
// the transaction survives a rollback and claims something happened that did
// not. Column set matches the audit_log table as migration 001 created it —
// admin_id is NOT NULL, and admin_name/admin_role record who the actor was at
// the time, since admin_accounts rows can be renamed or deleted later.
func logAuditTx(ctx context.Context, tx pgx.Tx, adminID, action, targetType, targetID, reason string) error {
	// No admin_accounts row means no id to attribute the entry to (e.g. a
	// super_admin acting from the users table). Skip rather than fail the
	// action the entry describes.
	if adminID == "" {
		return nil
	}
	var adminName, adminRole string
	tx.QueryRow(ctx, `SELECT name, role FROM admin_accounts WHERE id=$1`, adminID).
		Scan(&adminName, &adminRole)
	_, err := tx.Exec(ctx,
		`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		adminID, adminName, adminRole, action, targetType, targetID, reason)
	return err
}

// MergeStudents folds mergeID into keepID: one human, two records, usually a
// self-signup plus a roster row that was later claimed under a second account.
func MergeStudents(ctx context.Context, db *pgxpool.Pool, keepID, mergeID, actorID string) error {
	if keepID == mergeID {
		return errors.New("cannot merge a user into itself")
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	for _, q := range []string{
		`UPDATE quiz_attempts SET user_id=$1 WHERE user_id=$2`,
		`UPDATE points_ledger SET user_id=$1 WHERE user_id=$2`,
		`UPDATE user_profile_entries SET user_id=$1 WHERE user_id=$2`,
	} {
		if _, err := tx.Exec(ctx, q, keepID, mergeID); err != nil {
			return err
		}
	}

	// Enrollments move only if they would not violate the one-live-enrollment
	// index; ended ones are always safe to move.
	if _, err := tx.Exec(ctx,
		`UPDATE enrollments SET user_id=$1
		  WHERE user_id=$2
		    AND (status NOT IN ('active','suspended')
		         OR NOT EXISTS (SELECT 1 FROM enrollments k
		                         WHERE k.user_id=$1 AND k.status IN ('active','suspended')))`,
		keepID, mergeID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE users SET total_points = total_points +
			COALESCE((SELECT total_points FROM users WHERE id=$2), 0),
			updated_at = now()
		WHERE id=$1`, keepID, mergeID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET deleted_at=now(), status='deleted', updated_at=now() WHERE id=$1`,
		mergeID); err != nil {
		return err
	}

	if err := logAuditTx(ctx, tx, actorID, "merge_students", "user", keepID,
		"merged "+mergeID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type StudentAdminHandler struct {
	db          *pgxpool.Pool
	searchBloom *studentSearchBloom
}

func NewStudentAdminHandler(db *pgxpool.Pool) *StudentAdminHandler {
	return &StudentAdminHandler{db: db, searchBloom: newStudentSearchBloom()}
}

// GET /api/v1/admin/students/search?q=<email|roll|name>
func (h *StudentAdminHandler) Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 3 {
		middleware.BadRequest(w, "q must be at least 3 characters")
		return
	}

	// The Bloom filter is only a negative prefilter. A positive still goes to
	// PostgreSQL, which remains authoritative and applies the actual ILIKE
	// substring match. If refresh fails, search proceeds normally.
	if h.searchBloom != nil {
		fresh, err := h.searchBloom.refresh(r.Context(), h.db)
		if err != nil {
			log.Printf("student search bloom refresh: %v", err)
		} else if fresh && !h.searchBloom.mightContain(q) {
			middleware.JSON(w, http.StatusOK, []any{})
			return
		}
	}

	rows, err := h.db.Query(r.Context(), `
		SELECT u.id, u.display_name, u.email, u.status, u.deleted_at IS NOT NULL,
		       e.id, e.roll_number, i.name
		  FROM users u
		  LEFT JOIN enrollments e ON e.user_id = u.id AND e.status IN ('active','suspended')
		  LEFT JOIN institutions i ON i.id = e.institution_id
		 WHERE u.role='student'
		   AND (u.email ILIKE $1 OR u.display_name ILIKE $1 OR e.roll_number ILIKE $1)
		 ORDER BY u.display_name LIMIT 50`, "%"+q+"%")
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()

	type hit struct {
		ID              string  `json:"id"`
		DisplayName     string  `json:"display_name"`
		Email           string  `json:"email"`
		Status          string  `json:"status"`
		Deleted         bool    `json:"deleted"`
		EnrollmentID    *string `json:"enrollment_id,omitempty"`
		RollNumber      *string `json:"roll_number,omitempty"`
		InstitutionName *string `json:"institution_name,omitempty"`
	}
	out := []hit{}
	for rows.Next() {
		var x hit
		rows.Scan(&x.ID, &x.DisplayName, &x.Email, &x.Status, &x.Deleted,
			&x.EnrollmentID, &x.RollNumber, &x.InstitutionName)
		out = append(out, x)
	}
	middleware.JSON(w, http.StatusOK, out)
}

// POST /api/v1/admin/students/merge  {keep_user_id, merge_user_id}
func (h *StudentAdminHandler) Merge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeepUserID  string `json:"keep_user_id"`
		MergeUserID string `json:"merge_user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil ||
		req.KeepUserID == "" || req.MergeUserID == "" {
		middleware.BadRequest(w, "keep_user_id and merge_user_id are required")
		return
	}

	if err := MergeStudents(r.Context(), h.db, req.KeepUserID, req.MergeUserID,
		middleware.GetAdminID(r)); err != nil {
		log.Printf("Merge: %v", err)
		middleware.BadRequest(w, err.Error())
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "merged"})
}

// DELETE /api/v1/admin/students/{userId}/purge
//
// Permanent erasure beyond the soft deleted_at. Enrollments are detached
// rather than deleted so the institution keeps its historical roster count.
func (h *StudentAdminHandler) Purge(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")

	tx, err := h.db.Begin(r.Context())
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(r.Context())

	for _, q := range []string{
		`UPDATE enrollments SET user_id=NULL, status='transferred', ended_at=COALESCE(ended_at, now()) WHERE user_id=$1`,
		`DELETE FROM quiz_attempts WHERE user_id=$1`,
		`DELETE FROM points_ledger WHERE user_id=$1`,
		`DELETE FROM user_profile_entries WHERE user_id=$1`,
		`DELETE FROM group_students WHERE user_id=$1`,
		`DELETE FROM parent_student_links WHERE student_id=$1 OR parent_id=$1`,
	} {
		if _, err := tx.Exec(r.Context(), q, userID); err != nil {
			log.Printf("Purge %q: %v", q, err)
			middleware.InternalError(w)
			return
		}
	}

	if err := logAuditTx(r.Context(), tx, middleware.GetAdminID(r), "purge_student",
		"user", userID, "permanent erasure"); err != nil {
		log.Printf("Purge audit: %v", err)
		middleware.InternalError(w)
		return
	}
	if _, err := tx.Exec(r.Context(), `DELETE FROM users WHERE id=$1`, userID); err != nil {
		log.Printf("Purge delete user: %v", err)
		middleware.InternalError(w)
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"status": "purged"})
}
