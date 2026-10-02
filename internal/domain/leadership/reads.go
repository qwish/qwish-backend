package leadership

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/qwish/backend/internal/middleware"
)

// Routes mounts the read API for leadership role holders. Mount it behind
// RequireRole("teacher","institution_admin"); every handler re-derives scope
// from the caller's active assignments.
func (h *Handler) Routes(r chi.Router) {
	r.Get("/access", h.withGrants("", h.Access))
	r.Get("/departments", h.withGrants(PermDepartmentsRead, h.Departments))
	r.Get("/departments/{departmentId}/summary", h.withGrants(PermReportsRead, h.DepartmentSummary))
	r.Get("/students", h.withGrants(PermStudentsRead, h.Students))
	r.Get("/staff", h.withGrants(PermStaffRead, h.Staff))
}

type scoped func(w http.ResponseWriter, r *http.Request, gs Grants)

// withGrants loads active assignments; without one carrying perm, 403.
func (h *Handler) withGrants(perm string, next scoped) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		gs, err := Load(r.Context(), h.db, middleware.GetUserID(r), middleware.GetInstitutionID(r))
		if err != nil {
			middleware.InternalError(w)
			return
		}
		if perm != "" {
			if all, depts := gs.Scope(perm); !all && len(depts) == 0 {
				middleware.Forbidden(w)
				return
			}
		}
		next(w, r, gs)
	}
}

// requestedScope narrows perm's scope to ?department_id when given. ok=false
// means that department is outside the caller's scope.
func requestedScope(r *http.Request, gs Grants, perm string) (all bool, depts []string, ok bool) {
	all, depts = gs.Scope(perm)
	if d := strings.TrimSpace(r.URL.Query().Get("department_id")); d != "" {
		return false, []string{d}, gs.Can(perm, d)
	}
	if depts == nil {
		depts = []string{}
	}
	return all, depts, true
}

func (h *Handler) Access(w http.ResponseWriter, r *http.Request, gs Grants) {
	rows, err := h.db.Query(r.Context(), `SELECT `+assignmentCols+`
		WHERE a.user_id=$1 AND a.institution_id=$2 AND a.revoked_at IS NULL AND (a.ends_at IS NULL OR a.ends_at>now())
		ORDER BY a.starts_at`, middleware.GetUserID(r), middleware.GetInstitutionID(r))
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	list := []assignment{}
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			middleware.InternalError(w)
			return
		}
		list = append(list, a)
	}
	type reach struct {
		Institution   bool     `json:"institution"`
		DepartmentIDs []string `json:"department_ids"`
	}
	perms := map[string]reach{}
	for _, p := range []string{PermDepartmentsRead, PermStudentsRead, PermStaffRead, PermReportsRead, PermActivitiesPublish} {
		if all, depts := gs.Scope(p); all || len(depts) > 0 {
			if depts == nil {
				depts = []string{}
			}
			perms[p] = reach{all, depts}
		}
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"assignments": list, "permissions": perms})
}

func (h *Handler) Departments(w http.ResponseWriter, r *http.Request, gs Grants) {
	all, depts := gs.Scope(PermDepartmentsRead)
	out, err := h.listDepartments(r.Context(), middleware.GetInstitutionID(r), all, depts)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}

// DepartmentSummary: per-class aggregates for the last 30 days. No individual
// answers or responses are exposed here.
func (h *Handler) DepartmentSummary(w http.ResponseWriter, r *http.Request, gs Grants) {
	dept := chi.URLParam(r, "departmentId")
	if !gs.Can(PermReportsRead, dept) {
		middleware.NotFound(w, "department")
		return
	}
	inst := middleware.GetInstitutionID(r)
	var name string
	if h.db.QueryRow(r.Context(), `SELECT name FROM departments WHERE id::text=$1 AND institution_id=$2 AND archived_at IS NULL`, dept, inst).Scan(&name) != nil {
		middleware.NotFound(w, "department")
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT g.id, g.name,
		(SELECT count(*) FROM group_students gs WHERE gs.group_id=g.id),
		COALESCE((SELECT array_agg(COALESCE(NULLIF(u.display_name,''),u.full_name,'') ORDER BY u.full_name) FROM group_teachers gt JOIN users u ON u.id=gt.user_id WHERE gt.group_id=g.id),'{}'),
		(SELECT count(*) FROM quiz_attempts qa JOIN group_students gs ON gs.user_id=qa.user_id AND gs.group_id=g.id
			WHERE qa.status='completed' AND qa.completed_at>now()-interval '30 days'),
		(SELECT AVG(qa.score_pct)::float8 FROM quiz_attempts qa JOIN group_students gs ON gs.user_id=qa.user_id AND gs.group_id=g.id
			WHERE qa.status='completed' AND qa.completed_at>now()-interval '30 days')
		FROM groups g WHERE g.department_id::text=$1 AND g.institution_id=$2 AND g.archived_at IS NULL ORDER BY g.name`, dept, inst)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type class struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Students    int      `json:"student_count"`
		Teachers    []string `json:"teachers"`
		Attempts30d int      `json:"completed_attempts_30d"`
		AvgScore30d *float64 `json:"average_score_pct_30d"`
	}
	classes := []class{}
	for rows.Next() {
		var c class
		if rows.Scan(&c.ID, &c.Name, &c.Students, &c.Teachers, &c.Attempts30d, &c.AvgScore30d) != nil {
			middleware.InternalError(w)
			return
		}
		classes = append(classes, c)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"id": dept, "name": name, "classes": classes, "as_of": time.Now().UTC()})
}

func page(r *http.Request) (limit, offset int) {
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	return limit, max(offset, 0)
}

// Students lists students in scope. Institution scope includes students in no
// department; department scope covers students of that department's classes.
func (h *Handler) Students(w http.ResponseWriter, r *http.Request, gs Grants) {
	all, depts, ok := requestedScope(r, gs, PermStudentsRead)
	if !ok {
		middleware.NotFound(w, "department")
		return
	}
	limit, offset := page(r)
	inst := middleware.GetInstitutionID(r)
	rows, err := h.db.Query(r.Context(), `SELECT u.id, COALESCE(NULLIF(u.display_name,''),u.full_name,''),
		(SELECT e.roll_number FROM enrollments e WHERE e.user_id=u.id AND e.institution_id=$1 AND e.ended_at IS NULL LIMIT 1),
		COALESCE((SELECT array_agg(g.name ORDER BY g.name) FROM group_students gs JOIN groups g ON g.id=gs.group_id
			WHERE gs.user_id=u.id AND g.institution_id=$1 AND g.archived_at IS NULL AND ($2 OR g.department_id::text = ANY($3))),'{}'),
		count(*) OVER ()
		FROM users u
		WHERE u.institution_id=$1 AND u.role='student' AND u.deleted_at IS NULL
		  AND ($2 OR EXISTS(SELECT 1 FROM group_students gs JOIN groups g ON g.id=gs.group_id
			WHERE gs.user_id=u.id AND g.archived_at IS NULL AND g.department_id::text = ANY($3)))
		  AND ($4='' OR u.full_name ILIKE '%'||$4||'%' OR u.display_name ILIKE '%'||$4||'%')
		ORDER BY u.full_name, u.id LIMIT $5 OFFSET $6`, inst, all, depts, strings.TrimSpace(r.URL.Query().Get("q")), limit, offset)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type student struct {
		ID         string   `json:"id"`
		Name       string   `json:"name"`
		RollNumber *string  `json:"roll_number"`
		Classes    []string `json:"classes"`
	}
	out := []student{}
	total := 0
	for rows.Next() {
		var s student
		if rows.Scan(&s.ID, &s.Name, &s.RollNumber, &s.Classes, &total) != nil {
			middleware.InternalError(w)
			return
		}
		out = append(out, s)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, map[string]any{"students": out, "total": total})
}

// Staff lists teachers of classes in scope and leadership holders in scope.
func (h *Handler) Staff(w http.ResponseWriter, r *http.Request, gs Grants) {
	all, depts, ok := requestedScope(r, gs, PermStaffRead)
	if !ok {
		middleware.NotFound(w, "department")
		return
	}
	rows, err := h.db.Query(r.Context(), `SELECT u.id, COALESCE(NULLIF(u.display_name,''),u.full_name,''),
		COALESCE((SELECT array_agg(g.name ORDER BY g.name) FROM group_teachers gt JOIN groups g ON g.id=gt.group_id
			WHERE gt.user_id=u.id AND g.institution_id=$1 AND g.archived_at IS NULL AND ($2 OR g.department_id::text = ANY($3))),'{}'),
		COALESCE((SELECT array_agg(a.role ORDER BY a.role) FROM staff_role_assignments a
			WHERE a.user_id=u.id AND a.institution_id=$1 AND a.revoked_at IS NULL AND a.starts_at<=now() AND (a.ends_at IS NULL OR a.ends_at>now())
			  AND ($2 OR a.department_id::text = ANY($3))),'{}')
		FROM users u
		WHERE u.institution_id=$1 AND u.role IN ('teacher','institution_admin') AND u.deleted_at IS NULL
		  AND (($2 AND u.role='teacher')
		    OR EXISTS(SELECT 1 FROM group_teachers gt JOIN groups g ON g.id=gt.group_id
				WHERE gt.user_id=u.id AND g.archived_at IS NULL AND ($2 OR g.department_id::text = ANY($3)))
		    OR EXISTS(SELECT 1 FROM staff_role_assignments a WHERE a.user_id=u.id AND a.institution_id=$1 AND a.revoked_at IS NULL
				AND a.starts_at<=now() AND (a.ends_at IS NULL OR a.ends_at>now()) AND ($2 OR a.department_id::text = ANY($3))))
		ORDER BY u.full_name, u.id LIMIT 500`, middleware.GetInstitutionID(r), all, depts)
	if err != nil {
		middleware.InternalError(w)
		return
	}
	defer rows.Close()
	type member struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Classes []string `json:"classes"`
		Roles   []string `json:"roles"`
	}
	out := []member{}
	for rows.Next() {
		var m member
		if rows.Scan(&m.ID, &m.Name, &m.Classes, &m.Roles) != nil {
			middleware.InternalError(w)
			return
		}
		out = append(out, m)
	}
	if rows.Err() != nil {
		middleware.InternalError(w)
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}
