// Package leadership implements scoped institution leadership roles —
// Director, Principal, Vice Principal, Dean and Head of Department — from
// plans/institution-hierarchy-and-access-control.md.
//
// A role is held through an assignment scoped to the whole institution or to
// one department. Access is evaluated per assignment: a permission is granted
// only when one active assignment carries both the permission and a scope
// covering the target. Titles never grant access, and seniority does not
// expose individual responses or evidence.
package leadership

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const (
	PermDepartmentsRead   = "departments.read"
	PermStudentsRead      = "students.read"
	PermStaffRead         = "staff.read"
	PermReportsRead       = "reports.read"
	PermActivitiesPublish = "activities.publish"
)

// Role is one default template. Permission keys are stable; Label is display only.
type Role struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Scope       string   `json:"scope"` // institution | department | either
	Permissions []string `json:"permissions"`
	Summary     string   `json:"summary"`
}

var reads = []string{PermDepartmentsRead, PermStudentsRead, PermStaffRead, PermReportsRead}

// Roles is the catalogue, ordered by seniority for display.
var Roles = []Role{
	{Key: "director", Label: "Director", Scope: "institution",
		Permissions: append(append([]string{}, reads...), PermActivitiesPublish),
		Summary:     "Institution-wide oversight: departments, students, staff and summary reports; can publish forms and polls to the whole institution. Cannot read individual responses or manage roles."},
	{Key: "principal", Label: "Principal", Scope: "institution",
		Permissions: append(append([]string{}, reads...), PermActivitiesPublish),
		Summary:     "Head of institution: departments, students, staff and summary reports across the institution; can publish forms and polls to the whole institution. Cannot read individual responses or manage roles."},
	{Key: "vice_principal", Label: "Vice Principal", Scope: "either",
		Permissions: reads,
		Summary:     "Read access to departments, students, staff and summary reports for the institution or the named departments. Does not copy the Principal's publishing access."},
	{Key: "dean", Label: "Dean", Scope: "department",
		Permissions: reads,
		Summary:     "Academic monitoring of the assigned departments: students, staff and summary reports. Cannot publish or manage roles."},
	{Key: "hod", Label: "Head of Department", Scope: "department",
		Permissions: append(append([]string{}, reads...), PermActivitiesPublish),
		Summary:     "The assigned department's students, staff and summary reports; can publish forms and polls to that department's classes."},
}

func role(key string) (Role, bool) {
	for _, r := range Roles {
		if r.Key == key {
			return r, true
		}
	}
	return Role{}, false
}

// ValidScope checks a role/department pairing (department "" = institution).
func ValidScope(roleKey, departmentID string) error {
	r, ok := role(roleKey)
	if !ok {
		return fmt.Errorf("role must be one of director, principal, vice_principal, dean, hod")
	}
	switch {
	case r.Scope == "institution" && departmentID != "":
		return fmt.Errorf("%s is an institution-wide role; omit department_id", r.Label)
	case r.Scope == "department" && departmentID == "":
		return fmt.Errorf("%s needs a department_id", r.Label)
	}
	return nil
}

// Grant is one active assignment. DepartmentID "" means the whole institution.
type Grant struct {
	Role         string
	DepartmentID string
}

type Grants []Grant

func (g Grant) has(perm string) bool {
	r, ok := role(g.Role)
	if !ok {
		return false
	}
	for _, p := range r.Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// Can reports whether one grant carries perm with a scope covering dept
// ("" = a class in no department, reachable only by institution scope).
func (gs Grants) Can(perm, dept string) bool {
	for _, g := range gs {
		if g.has(perm) && (g.DepartmentID == "" || g.DepartmentID == dept) {
			return true
		}
	}
	return false
}

// Scope is the reach of perm: the whole institution, or these departments.
func (gs Grants) Scope(perm string) (all bool, departments []string) {
	seen := map[string]bool{}
	for _, g := range gs {
		if !g.has(perm) {
			continue
		}
		if g.DepartmentID == "" {
			return true, nil
		}
		if !seen[g.DepartmentID] {
			seen[g.DepartmentID] = true
			departments = append(departments, g.DepartmentID)
		}
	}
	return false, departments
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Load reads the user's active grants. It runs on every request, so ending or
// revoking an assignment takes effect on the next request.
func Load(ctx context.Context, q querier, userID, institutionID string) (Grants, error) {
	rows, err := q.Query(ctx, `SELECT a.role, COALESCE(a.department_id::text,'')
		FROM staff_role_assignments a
		JOIN users u ON u.id=a.user_id AND u.institution_id=a.institution_id AND u.deleted_at IS NULL AND COALESCE(u.status,'active')='active'
		LEFT JOIN departments d ON d.id=a.department_id
		WHERE a.user_id=$1 AND a.institution_id=$2 AND a.revoked_at IS NULL
		  AND a.starts_at<=now() AND (a.ends_at IS NULL OR a.ends_at>now())
		  AND (a.department_id IS NULL OR d.archived_at IS NULL)`, userID, institutionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var gs Grants
	for rows.Next() {
		var g Grant
		if err := rows.Scan(&g.Role, &g.DepartmentID); err != nil {
			return nil, err
		}
		gs = append(gs, g)
	}
	return gs, rows.Err()
}
