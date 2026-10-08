-- Read-only. Run before migrations 095/096 and retain the output with the
-- recoverable database snapshot. These rows require review, not automatic deletion.
BEGIN TRANSACTION READ ONLY;
-- Invalid active eligibility and retained ended-class assignments are separate
-- findings; an ended-class relationship is history, not proof of bad data.
SELECT gt.group_id,gt.user_id,g.institution_id,
 u.institution_id AS staff_institution,u.role,u.status,u.deleted_at,g.archived_at,
 CASE WHEN g.institution_id IS DISTINCT FROM u.institution_id THEN 'cross_institute'
 WHEN u.role<>'teacher' THEN 'wrong_role' WHEN u.deleted_at IS NOT NULL THEN 'deleted_user'
 WHEN g.archived_at IS NOT NULL THEN 'retained_ended_class'
 ELSE 'inactive_staff' END AS finding
FROM group_teachers gt JOIN groups g ON g.id=gt.group_id JOIN users u ON u.id=gt.user_id
WHERE g.institution_id IS DISTINCT FROM u.institution_id OR u.role<>'teacher'
 OR u.deleted_at IS NOT NULL OR u.status<>'active' OR g.archived_at IS NOT NULL
ORDER BY g.institution_id,gt.group_id,gt.user_id;

SELECT gs.group_id,gs.user_id,g.institution_id,gs.joined_at
FROM group_students gs JOIN groups g ON g.id=gs.group_id
WHERE g.archived_at IS NULL AND NOT EXISTS(SELECT 1 FROM enrollments e
 WHERE e.user_id=gs.user_id AND e.institution_id=g.institution_id AND e.status IN ('active','suspended'))
ORDER BY g.institution_id,gs.group_id,gs.user_id;

SELECT e.id AS enrollment_id,e.user_id,e.institution_id,e.grade AS old_grade,e.section AS old_section,
 jsonb_agg(DISTINCT jsonb_build_object('grade',g.grade,'section',g.section)) AS combinations
FROM enrollments e JOIN group_students gs ON gs.user_id=e.user_id
JOIN groups g ON g.id=gs.group_id AND g.institution_id=e.institution_id
WHERE e.status IN ('active','suspended') AND g.kind='class' AND g.archived_at IS NULL
 AND (g.grade IS NOT NULL OR g.section IS NOT NULL)
GROUP BY e.id HAVING count(DISTINCT (g.grade,g.section))>1 ORDER BY e.id;

SELECT a.institution_id,a.id,a.name,a.starts_on,a.ends_on,b.id AS overlaps_id,b.name AS overlaps_name
FROM academic_years a JOIN academic_years b ON b.institution_id=a.institution_id AND a.id<b.id
AND a.starts_on<=b.ends_on AND b.starts_on<=a.ends_on ORDER BY a.institution_id,a.id,b.id;

SELECT ar.attempt_id,count(*) AS links FROM learning_assignment_recipients ar
WHERE ar.attempt_id IS NOT NULL GROUP BY ar.attempt_id HAVING count(*)>1 ORDER BY ar.attempt_id;
COMMIT;
