package leadership

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qwish/backend/internal/jsonx"
	"github.com/qwish/backend/internal/middleware"
)

type Handler struct {
	db *pgxpool.Pool
}

func NewHandler(db *pgxpool.Pool) *Handler { return &Handler{db: db} }

// AdminRoutes mounts department and role management. Mount under the
// institution_admin route: the institution admin is the access administrator.
func (h *Handler) AdminRoutes(r chi.Router) {
	r.Get("/departments", h.ListDepartments)
	r.Post("/departments", h.CreateDepartment)
	r.Patch("/departments/{departmentId}", h.UpdateDepartment)
	r.Delete("/departments/{departmentId}", h.ArchiveDepartment)
	r.Put("/groups/{groupId}/department", h.SetGroupDepartment)
	r.Get("/staff-role-templates", h.Templates)
	r.Get("/staff-roles", h.ListAssignments)
	r.Post("/staff-roles", h.CreateAssignment)
	r.Delete("/staff-roles/{assignmentId}", h.RevokeAssignment)
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	d := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		middleware.BadRequest(w, "invalid request body")
		return false
	}
	return true
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// auditTx writes to the institution's audit log inside the change's transaction.
func auditTx(ctx context.Context, tx pgx.Tx, r *http.Request, action, targetType, targetID, reason string, before, after map[string]any) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason, institution_id, old_value, new_value)
		SELECT u.id, COALESCE(NULLIF(u.display_name,''),u.full_name,''), u.role, $2, $3, $4, NULLIF($5,''), $6, $7, $8 FROM users u WHERE u.id=$1`,
		middleware.GetUserID(r), action, targetType, targetID, reason, middleware.GetInstitutionID(r), before, after)
	return err
}

// ─── Departments ─────────────────────────────────────────────────────────────

type department struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Code         *string  `json:"code"`
	ClassCount   int      `json:"class_count"`
	StudentCount int      `json:"student_count"`
	Heads        []string `json:"heads"`
}

// listDepartments returns active departments; with all=false, only those in ids.
func (h *Handler) listDepartments(ctx context.Context, inst string, all bool, ids []string) ([]department, error) {
	rows, err := h.db.Query(ctx, `SELECT d.id, d.name, d.code,
		(SELECT count(*) FROM groups g WHERE g.department_id=d.id AND g.archived_at IS NULL),
		(SELECT count(DISTINCT gs.user_id) FROM groups g JOIN group_students gs ON gs.group_id=g.id WHERE g.department_id=d.id AND g.archived_at IS NULL),
		COALESCE((SELECT array_agg(COALESCE(NULLIF(u.display_name,''),u.full_name,'') ORDER BY u.full_name) FROM staff_role_assignments a JOIN users u ON u.id=a.user_id
			WHERE a.department_id=d.id AND a.role='hod' AND a.revoked_at IS NULL AND a.starts_at<=now() AND (a.ends_at IS NULL OR a.ends_at>now())),'{}')
		FROM departments d WHERE d.institution_id=$1 AND d.archived_at IS NULL AND ($2 OR d.id::text = ANY($3))
		ORDER BY d.name`, inst, all, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []department{}
	for rows.Next() {
		var d department
		if err := rows.Scan(&d.ID, &d.Name, &d.Code, &d.ClassCount, &d.StudentCount, &d.Heads); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (h *Handler) ListDepartments(w http.ResponseWriter, r *http.Request) {
	out, err := h.listDepartments(r.Context(), middleware.GetInstitutionID(r), true, nil)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

type departmentInput struct {
	Name string  `json:"name"`
	Code *string `json:"code"`
}

func (in *departmentInput) clean() bool {
	in.Name = strings.TrimSpace(in.Name)
	if in.Code != nil {
		c := strings.TrimSpace(*in.Code)
		in.Code = &c
		if c == "" {
			in.Code = nil
		}
	}
	return in.Name != "" && len([]rune(in.Name)) <= 120 && (in.Code == nil || len([]rune(*in.Code)) <= 20)
}

func (h *Handler) CreateDepartment(w http.ResponseWriter, r *http.Request) {
	var in departmentInput
	if !decode(w, r, &in) {
		return
	}
	if !in.clean() {
		middleware.BadRequest(w, "name (1–120 characters) is required; code is at most 20")
		return
	}
	ctx := r.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO departments(institution_id,name,code) VALUES ($1,$2,$3) RETURNING id`,
		middleware.GetInstitutionID(r), in.Name, in.Code).Scan(&id)
	if isUnique(err) {
		middleware.Error(w, http.StatusConflict, "CONFLICT", "a department with this name already exists")
		return
	}
	if err != nil || auditTx(ctx, tx, r, "create_department", "department", id, "", nil, map[string]any{"name": in.Name, "code": in.Code}) != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, map[string]string{"id": id})
}

func (h *Handler) UpdateDepartment(w http.ResponseWriter, r *http.Request) {
	var in departmentInput
	if !decode(w, r, &in) {
		return
	}
	if !in.clean() {
		middleware.BadRequest(w, "name (1–120 characters) is required; code is at most 20")
		return
	}
	ctx := r.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)
	var oldName string
	var oldCode *string
	id := chi.URLParam(r, "departmentId")
	err = tx.QueryRow(ctx, `SELECT name, code FROM departments WHERE id::text=$1 AND institution_id=$2 AND archived_at IS NULL FOR UPDATE`,
		id, middleware.GetInstitutionID(r)).Scan(&oldName, &oldCode)
	if err != nil {
		middleware.NotFound(w, "department")
		return
	}
	_, err = tx.Exec(ctx, `UPDATE departments SET name=$2, code=$3 WHERE id=$1`, id, in.Name, in.Code)
	if isUnique(err) {
		middleware.Error(w, http.StatusConflict, "CONFLICT", "a department with this name already exists")
		return
	}
	if err != nil || auditTx(ctx, tx, r, "update_department", "department", id, "",
		map[string]any{"name": oldName, "code": oldCode}, map[string]any{"name": in.Name, "code": in.Code}) != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"id": id})
}

// ArchiveDepartment refuses while classes or active roles still point at it,
// so archiving never silently strips an HOD's access or orphans a class.
func (h *Handler) ArchiveDepartment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "departmentId")
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM institutions WHERE id=$1 FOR UPDATE`, middleware.GetInstitutionID(r)); err != nil {
		middleware.InternalError(w)
		return
	}

	var classes, roles, programmes int
	err = tx.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM groups g WHERE g.department_id=d.id AND g.archived_at IS NULL),
		(SELECT count(*) FROM staff_role_assignments a WHERE a.department_id=d.id AND a.revoked_at IS NULL AND (a.ends_at IS NULL OR a.ends_at>now())),
		(SELECT count(*) FROM programmes p WHERE p.department_id=d.id AND p.archived_at IS NULL)
		FROM departments d WHERE d.id::text=$1 AND d.institution_id=$2 AND d.archived_at IS NULL FOR UPDATE OF d`,
		id, middleware.GetInstitutionID(r)).Scan(&classes, &roles, &programmes)
	if err != nil {
		middleware.NotFound(w, "department")
		return
	}
	if classes > 0 || roles > 0 || programmes > 0 {
		middleware.Error(w, http.StatusConflict, "CONFLICT", "move this department's classes and programmes and end its role assignments before archiving it")
		return
	}
	_, err = tx.Exec(ctx, `UPDATE departments SET archived_at=now() WHERE id=$1`, id)
	if err != nil || auditTx(ctx, tx, r, "archive_department", "department", id, "", nil, nil) != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]string{"id": id})
}

func (h *Handler) SetGroupDepartment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DepartmentID *string `json:"department_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	ctx := r.Context()
	inst := middleware.GetInstitutionID(r)
	groupID := chi.URLParam(r, "groupId")
	dept := ""
	if in.DepartmentID != nil {
		dept = strings.TrimSpace(*in.DepartmentID)
	}
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM institutions WHERE id=$1 FOR UPDATE`, middleware.GetInstitutionID(r)); err != nil {
		middleware.InternalError(w)
		return
	}

	var old *string
	if err := tx.QueryRow(ctx, `SELECT department_id::text FROM groups WHERE id::text=$1 AND institution_id=$2 AND archived_at IS NULL FOR UPDATE`,
		groupID, inst).Scan(&old); err != nil {
		middleware.NotFound(w, "class")
		return
	}
	if dept != "" {
		var ok bool
		tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM departments WHERE id::text=$1 AND institution_id=$2 AND archived_at IS NULL)`, dept, inst).Scan(&ok)
		if !ok {
			middleware.BadRequest(w, "department_id is not an active department of this institution")
			return
		}
	}
	_, err = tx.Exec(ctx, `UPDATE groups SET department_id=NULLIF($2,'')::uuid WHERE id::text=$1`, groupID, dept)
	if err != nil || auditTx(ctx, tx, r, "set_group_department", "group", groupID, "",
		map[string]any{"department_id": old}, map[string]any{"department_id": in.DepartmentID}) != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"id": groupID, "department_id": in.DepartmentID})
}

// ─── Role assignments ────────────────────────────────────────────────────────

func (h *Handler) Templates(w http.ResponseWriter, r *http.Request) {
	middleware.JSON(w, http.StatusOK, Roles)
}

type assignment struct {
	ID             string     `json:"id"`
	UserID         string     `json:"user_id"`
	UserName       string     `json:"user_name"`
	Role           string     `json:"role"`
	RoleLabel      string     `json:"role_label"`
	Title          *string    `json:"title"`
	DepartmentID   *string    `json:"department_id"`
	DepartmentName *string    `json:"department_name"`
	StartsAt       time.Time  `json:"starts_at"`
	EndsAt         *time.Time `json:"ends_at"`
	State          string     `json:"state"` // active | scheduled | ended | revoked
	Reason         string     `json:"reason"`
	GrantedBy      *string    `json:"granted_by_name"`
	RevokedAt      *time.Time `json:"revoked_at"`
	RevokeReason   *string    `json:"revoke_reason"`
	Summary        string     `json:"access_summary"`
}

const assignmentCols = `a.id, a.user_id, COALESCE(NULLIF(u.display_name,''),u.full_name,''), a.role, a.title, a.department_id::text, d.name,
	a.starts_at, a.ends_at,
	CASE WHEN a.revoked_at IS NOT NULL THEN 'revoked' WHEN a.ends_at IS NOT NULL AND a.ends_at<=now() THEN 'ended'
	     WHEN a.starts_at>now() THEN 'scheduled' ELSE 'active' END,
	a.reason, (SELECT COALESCE(NULLIF(g.display_name,''),g.full_name,'') FROM users g WHERE g.id=a.granted_by), a.revoked_at, a.revoke_reason
	FROM staff_role_assignments a JOIN users u ON u.id=a.user_id LEFT JOIN departments d ON d.id=a.department_id`

func scanAssignment(row pgx.Row) (assignment, error) {
	var a assignment
	err := row.Scan(&a.ID, &a.UserID, &a.UserName, &a.Role, &a.Title, &a.DepartmentID, &a.DepartmentName,
		&a.StartsAt, &a.EndsAt, &a.State, &a.Reason, &a.GrantedBy, &a.RevokedAt, &a.RevokeReason)
	if rl, ok := role(a.Role); ok {
		a.RoleLabel = rl.Label
		a.Summary = rl.Summary
		if a.DepartmentName != nil {
			a.Summary = "Scope: " + *a.DepartmentName + ". " + a.Summary
		}
	}
	return a, err
}

func (h *Handler) ListAssignments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := h.db.Query(r.Context(), `SELECT `+assignmentCols+`
		WHERE a.institution_id=$1 AND ($2='' OR a.user_id::text=$2) AND ($3='' OR a.role=$3) AND ($4='' OR a.department_id::text=$4)
		  AND ($5 OR (a.revoked_at IS NULL AND (a.ends_at IS NULL OR a.ends_at>now())))
		ORDER BY a.created_at DESC LIMIT 500`,
		middleware.GetInstitutionID(r), q.Get("user_id"), q.Get("role"), q.Get("department_id"), q.Get("include_ended") == "true")
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	out := []assignment{}
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		out = append(out, a)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

func (h *Handler) CreateAssignment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		UserID       string     `json:"user_id"`
		Role         string     `json:"role"`
		DepartmentID string     `json:"department_id"`
		Title        *string    `json:"title"`
		StartsAt     *time.Time `json:"starts_at"`
		EndsAt       *time.Time `json:"ends_at"`
		Reason       string     `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.DepartmentID = strings.TrimSpace(in.DepartmentID)
	in.Reason = strings.TrimSpace(in.Reason)
	if err := ValidScope(in.Role, in.DepartmentID); err != nil {
		middleware.BadRequest(w, err.Error())
		return
	}
	if in.Reason == "" || len([]rune(in.Reason)) > 500 || (in.Title != nil && len([]rune(*in.Title)) > 120) {
		middleware.BadRequest(w, "reason (1–500 characters) is required; title is at most 120")
		return
	}
	starts := time.Now()
	if in.StartsAt != nil {
		starts = *in.StartsAt
	}
	if in.EndsAt != nil && (!in.EndsAt.After(starts) || !in.EndsAt.After(time.Now())) {
		middleware.BadRequest(w, "ends_at must be in the future and after starts_at")
		return
	}
	ctx := r.Context()
	inst := middleware.GetInstitutionID(r)
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM institutions WHERE id=$1 FOR UPDATE`, inst); err != nil {
		middleware.InternalError(w)
		return
	}

	// The holder must be active staff of this institution; students never hold roles.
	var staff bool
	tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id::text=$1 AND institution_id=$2 AND role IN ('teacher','institution_admin')
		AND deleted_at IS NULL AND status='active')`, in.UserID, inst).Scan(&staff)
	if !staff {
		middleware.BadRequest(w, "user_id must be an active teacher or administrator of this institution")
		return
	}
	if in.DepartmentID != "" {
		var ok bool
		tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM departments WHERE id::text=$1 AND institution_id=$2 AND archived_at IS NULL)`, in.DepartmentID, inst).Scan(&ok)
		if !ok {
			middleware.BadRequest(w, "department_id is not an active department of this institution")
			return
		}
	}
	// An assignment that ran out still occupies the uniqueness slot; close it out.
	if _, err := tx.Exec(ctx, `UPDATE staff_role_assignments SET revoked_at=now(), revoke_reason='expired'
		WHERE user_id::text=$1 AND role=$2 AND department_id IS NOT DISTINCT FROM NULLIF($3,'')::uuid
		  AND revoked_at IS NULL AND ends_at IS NOT NULL AND ends_at<=now()`, in.UserID, in.Role, in.DepartmentID); err != nil {
		middleware.InternalError(w)
		return
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO staff_role_assignments(institution_id,user_id,role,department_id,title,starts_at,ends_at,reason,granted_by)
		VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9) RETURNING id`,
		inst, in.UserID, in.Role, in.DepartmentID, in.Title, starts, in.EndsAt, in.Reason, middleware.GetUserID(r)).Scan(&id)
	if isUnique(err) {
		middleware.Error(w, http.StatusConflict, "CONFLICT", "this person already holds this role for this scope")
		return
	}
	if err != nil || auditTx(ctx, tx, r, "grant_staff_role", "user", in.UserID, in.Reason, nil, map[string]any{
		"assignment_id": id, "role": in.Role, "department_id": in.DepartmentID, "title": in.Title, "starts_at": starts, "ends_at": in.EndsAt,
	}) != nil {
		middleware.InternalError(w)
		return
	}
	a, err := scanAssignment(tx.QueryRow(ctx, `SELECT `+assignmentCols+` WHERE a.id=$1`, id))
	if err != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusCreated, a)
}

// RevokeAssignment ends an assignment now. Access stops on the next request.
func (h *Handler) RevokeAssignment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" || len([]rune(in.Reason)) > 500 {
		middleware.BadRequest(w, "reason (1–500 characters) is required")
		return
	}
	ctx := r.Context()
	tx, err := h.db.Begin(ctx)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer tx.Rollback(ctx)
	a, err := scanAssignment(tx.QueryRow(ctx, `SELECT `+assignmentCols+` WHERE a.id::text=$1 AND a.institution_id=$2 FOR UPDATE OF a`,
		chi.URLParam(r, "assignmentId"), middleware.GetInstitutionID(r)))
	if err != nil {
		middleware.NotFound(w, "assignment")
		return
	}
	if a.State == "revoked" {
		middleware.JSON(w, http.StatusOK, a)
		return
	}
	_, err = tx.Exec(ctx, `UPDATE staff_role_assignments SET revoked_at=now(), revoked_by=$2, revoke_reason=$3 WHERE id=$1`,
		a.ID, middleware.GetUserID(r), in.Reason)
	if err != nil || auditTx(ctx, tx, r, "revoke_staff_role", "user", a.UserID, in.Reason,
		map[string]any{"assignment_id": a.ID, "role": a.Role, "department_id": a.DepartmentID, "state": a.State}, map[string]any{"state": "revoked"}) != nil {
		middleware.InternalError(w)
		return
	}
	a, err = scanAssignment(tx.QueryRow(ctx, `SELECT `+assignmentCols+` WHERE a.id=$1`, a.ID))
	if err != nil || tx.Commit(ctx) != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, a)
}
