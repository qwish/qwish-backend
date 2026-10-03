package db

// LiveMemberSQL is the one definition of "this student belongs to this
// institute": a live enrollment. users.institution_id is only the student's
// active institute and must never be used for membership.
func LiveMemberSQL(userCol, instParam string) string {
	return "EXISTS (SELECT 1 FROM enrollments m WHERE m.user_id=" + userCol +
		" AND m.institution_id=" + instParam + " AND m.status IN ('active','suspended'))"
}
