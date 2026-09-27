package institution

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// auditActionGroups are the families the audit log can be filtered by. An
// action missing here still appears under "all actions".
var auditActionGroups = map[string][]string{
	"membership": {
		"suspend_student", "reactivate_student", "set_enrollment_status",
		"suspend_teacher", "reactivate_teacher", "verify_teacher", "remove_teacher", "invite_teacher",
		"add_student_to_group", "remove_student_from_group", "add_teacher_to_group", "remove_teacher_from_group",
	},
	"admissions": {"update_admission_policy", "admission_approve", "admission_decline"},
	"academics": {
		"create_group", "update_group", "archive_group",
		"create_academic_year", "update_academic_year",
		"create_curriculum_version", "update_curriculum_draft", "publish_curriculum_version",
		"assign_class_curriculum", "end_class_curriculum", "promote_students", "revert_promotion",
		"review_edit_request",
	},
	"settings": {"update_settings", "update_point_rules", "request_referral_code_reset", "reset_referral_codes"},
}

// auditTargetLabel resolves a readable name for an entry's target at read
// time, so older entries get names too. Unknown types fall back to NULL.
const auditTargetLabel = `CASE al.target_type
	WHEN 'student' THEN (SELECT COALESCE(NULLIF(u.display_name,''), u.full_name) FROM users u WHERE u.id=al.target_id)
	WHEN 'teacher' THEN (SELECT COALESCE(NULLIF(u.display_name,''), u.full_name) FROM users u WHERE u.id=al.target_id)
	WHEN 'user' THEN (SELECT COALESCE(NULLIF(u.display_name,''), u.full_name) FROM users u WHERE u.id=al.target_id)
	WHEN 'enrollment' THEN (SELECT COALESCE(NULLIF(u.display_name,''), e.full_name) FROM enrollments e LEFT JOIN users u ON u.id=e.user_id WHERE e.id=al.target_id)
	WHEN 'group' THEN (SELECT g.name FROM groups g WHERE g.id=al.target_id)
	WHEN 'quiz' THEN (SELECT q.title FROM quizzes q WHERE q.id=al.target_id)
	WHEN 'institution' THEN (SELECT i.name FROM institutions i WHERE i.id=al.target_id)
	WHEN 'curriculum' THEN COALESCE(
		(SELECT v.subject || ' · Grade ' || v.grade || ' · ' || v.label FROM curriculum_versions v WHERE v.id=al.target_id),
		(SELECT y.name FROM academic_years y WHERE y.id=al.target_id))
	WHEN 'admission' THEN (SELECT COALESCE(NULLIF(u.display_name,''), u.full_name) FROM admission_requests ar JOIN users u ON u.id=ar.user_id WHERE ar.id=al.target_id)
	END`

// auditChange is one field's before and after.
type auditChange struct {
	Field  string      `json:"field"`
	Before interface{} `json:"before"`
	After  interface{} `json:"after"`
}

// auditChanges diffs old_value against new_value, field by field.
func auditChanges(oldRaw, newRaw []byte) []auditChange {
	if len(oldRaw) == 0 && len(newRaw) == 0 {
		return nil
	}
	before, after := map[string]interface{}{}, map[string]interface{}{}
	_ = json.Unmarshal(oldRaw, &before)
	_ = json.Unmarshal(newRaw, &after)
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	out := []auditChange{}
	for _, k := range names {
		b, bOK := before[k]
		a, aOK := after[k]
		if bOK && aOK && fmt.Sprint(b) == fmt.Sprint(a) {
			continue
		}
		out = append(out, auditChange{Field: k, Before: b, After: a})
	}
	return out
}

// logAuditChange records an action with the fields it changed. Only fields
// whose values differ are stored, so the log reads as a diff.
func logAuditChange(ctx context.Context, db *pgxpool.Pool, adminID, institutionID, action, targetType, targetID, reason string, before, after map[string]interface{}) {
	oldV, newV := map[string]interface{}{}, map[string]interface{}{}
	for k, a := range after {
		if b, ok := before[k]; !ok || fmt.Sprint(b) != fmt.Sprint(a) {
			oldV[k] = before[k]
			newV[k] = a
		}
	}
	var adminName, adminRole string
	db.QueryRow(ctx, `SELECT display_name, role FROM users WHERE id=$1`, adminID).Scan(&adminName, &adminRole)
	oldJSON, _ := json.Marshal(oldV)
	newJSON, _ := json.Marshal(newV)
	db.Exec(ctx,
		`INSERT INTO audit_log (admin_id, admin_name, admin_role, action_type, target_type, target_id, reason, institution_id, old_value, new_value)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid,$9,$10)`,
		adminID, adminName, adminRole, action, targetType, targetID, reason, institutionID, oldJSON, newJSON)
}
