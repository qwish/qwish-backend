package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/qwish/backend/internal/middleware"
)

// GET /api/v1/admin/institutions/:institutionId/academics
//
// Read-only snapshot of an institution's academic setup for the console:
// departments, leadership roles, years and terms, programmes and cohorts,
// curricula, course offerings and classes. Individual students are not
// listed here (the roster covers them). Archived records are included and
// flagged so support can see history.
func (h *Handler) InstitutionAcademics(w http.ResponseWriter, r *http.Request) {
	instID := chi.URLParam(r, "institutionId")
	if _, err := uuid.Parse(instID); err != nil {
		middleware.BadRequest(w, "institutionId must be a UUID")
		return
	}
	// ponytail: one statement of json_agg subqueries; paginate classes if an
	// institute ever has thousands of them.
	var out json.RawMessage
	err := h.db.QueryRow(r.Context(), `SELECT json_build_object(
	'departments', COALESCE((SELECT json_agg(json_build_object(
		'id', d.id, 'name', d.name, 'code', d.code, 'archived_at', d.archived_at,
		'class_count', (SELECT count(*) FROM groups g WHERE g.department_id=d.id AND g.archived_at IS NULL),
		'programme_count', (SELECT count(*) FROM programmes p WHERE p.department_id=d.id AND p.archived_at IS NULL)
	) ORDER BY d.archived_at NULLS FIRST, d.name) FROM departments d WHERE d.institution_id=i.id), '[]'),

	'roles', COALESCE((SELECT json_agg(json_build_object(
		'id', a.id, 'role', a.role, 'title', a.title,
		'user_name', COALESCE(NULLIF(u.display_name,''), u.full_name, ''), 'user_email', u.email,
		'department_name', d.name, 'starts_at', a.starts_at, 'ends_at', a.ends_at, 'revoked_at', a.revoked_at,
		'state', CASE WHEN a.revoked_at IS NOT NULL THEN 'revoked' WHEN a.starts_at > now() THEN 'scheduled'
		              WHEN a.ends_at IS NOT NULL AND a.ends_at <= now() THEN 'ended' ELSE 'active' END
	) ORDER BY a.revoked_at NULLS FIRST, a.role, u.full_name)
		FROM staff_role_assignments a JOIN users u ON u.id=a.user_id LEFT JOIN departments d ON d.id=a.department_id
		WHERE a.institution_id=i.id), '[]'),

	'years', COALESCE((SELECT json_agg(json_build_object(
		'id', y.id, 'name', y.name, 'starts_on', y.starts_on, 'ends_on', y.ends_on,
		'terms', COALESCE((SELECT json_agg(json_build_object('id', t.id, 'name', t.name, 'sequence', t.sequence,
			'starts_on', t.starts_on, 'ends_on', t.ends_on) ORDER BY t.sequence) FROM academic_terms t WHERE t.academic_year_id=y.id), '[]'),
		'assignment_count', (SELECT count(*) FROM class_curricula cc WHERE cc.academic_year_id=y.id AND cc.ended_at IS NULL)
	) ORDER BY y.starts_on DESC) FROM academic_years y WHERE y.institution_id=i.id), '[]'),

	'programmes', COALESCE((SELECT json_agg(json_build_object(
		'id', p.id, 'code', p.code, 'name', p.name, 'award', p.award, 'duration_terms', p.duration_terms,
		'department_name', d.name, 'archived_at', p.archived_at,
		'cohorts', COALESCE((SELECT json_agg(json_build_object('id', c.id, 'admission_year', c.admission_year,
			'completion_year', c.completion_year, 'archived_at', c.archived_at,
			'division_count', (SELECT count(*) FROM groups g WHERE g.cohort_id=c.id AND g.archived_at IS NULL))
			ORDER BY c.admission_year DESC) FROM cohorts c WHERE c.programme_id=p.id), '[]')
	) ORDER BY p.archived_at NULLS FIRST, p.code) FROM programmes p JOIN departments d ON d.id=p.department_id WHERE p.institution_id=i.id), '[]'),

	'curricula', COALESCE((SELECT json_agg(json_build_object(
		'id', v.id, 'name', c.name, 'label', v.label, 'subject', v.subject, 'grade', v.grade,
		'status', v.status, 'revision', v.revision, 'published_at', v.published_at,
		'chapter_count', (SELECT count(*) FROM curriculum_chapters ch WHERE ch.version_id=v.id),
		'concept_count', (SELECT count(*) FROM curriculum_chapters ch JOIN curriculum_concepts cn ON cn.chapter_id=ch.id WHERE ch.version_id=v.id),
		'class_count', (SELECT count(DISTINCT cc.group_id) FROM class_curricula cc WHERE cc.version_id=v.id AND cc.ended_at IS NULL)
	) ORDER BY c.name, v.created_at DESC) FROM curriculum_versions v JOIN curricula c ON c.id=v.curriculum_id WHERE v.institution_id=i.id), '[]'),

	'offerings', COALESCE((SELECT json_agg(json_build_object(
		'id', o.id, 'code', o.code, 'title', o.title, 'term_name', t.name, 'year_name', y.name, 'department_name', d.name,
		'groups', COALESCE((SELECT json_agg(json_build_object('name', g.name, 'component', og.component) ORDER BY og.component, g.name)
			FROM offering_groups og JOIN groups g ON g.id=og.group_id WHERE og.offering_id=o.id), '[]')
	) ORDER BY y.starts_on DESC, t.sequence, o.code)
		FROM course_offerings o JOIN academic_terms t ON t.id=o.term_id JOIN academic_years y ON y.id=t.academic_year_id
		LEFT JOIN departments d ON d.id=o.department_id WHERE o.institution_id=i.id), '[]'),

	'classes', COALESCE((SELECT json_agg(json_build_object(
		'id', g.id, 'name', g.name, 'kind', g.kind, 'grade', g.grade, 'section', g.section,
		'invite_code', g.invite_code, 'joining_enabled', g.joining_enabled,
		'created_at', g.created_at, 'archived_at', g.archived_at,
		'department_name', d.name,
		'cohort', CASE WHEN c.id IS NULL THEN NULL ELSE json_build_object('programme_code', p.code,
			'admission_year', c.admission_year, 'completion_year', c.completion_year) END,
		'term_name', t.name,
		'student_count', (SELECT count(*) FROM group_students gs WHERE gs.group_id=g.id),
		'teachers', COALESCE((SELECT json_agg(COALESCE(NULLIF(u.display_name,''), u.full_name) ORDER BY u.full_name)
			FROM group_teachers gt JOIN users u ON u.id=gt.user_id WHERE gt.group_id=g.id AND u.deleted_at IS NULL), '[]'),
		'curricula', COALESCE((SELECT json_agg(json_build_object('name', cu.name, 'label', v.label, 'subject', v.subject, 'year_name', y.name)
			ORDER BY v.subject, cu.name) FROM class_curricula cc JOIN curriculum_versions v ON v.id=cc.version_id
			JOIN curricula cu ON cu.id=v.curriculum_id JOIN academic_years y ON y.id=cc.academic_year_id
			WHERE cc.group_id=g.id AND cc.ended_at IS NULL), '[]')
	) ORDER BY g.archived_at NULLS FIRST, g.name)
		FROM groups g LEFT JOIN departments d ON d.id=g.department_id LEFT JOIN cohorts c ON c.id=g.cohort_id
		LEFT JOIN programmes p ON p.id=c.programme_id LEFT JOIN academic_terms t ON t.id=g.term_id
		WHERE g.institution_id=i.id), '[]')
	) FROM institutions i WHERE i.id=$1 AND i.deleted_at IS NULL`, instID).Scan(&out)
	if err != nil {
		middleware.NotFound(w, "institution")
		return
	}
	middleware.JSON(w, http.StatusOK, out)
}
