-- /teacher/attention finds overdue work by the teacher's classes
-- (learning_assignments.group_id = ANY(...) AND status='published'); no index
-- led with group_id. The queue's other filters are already covered by
-- existing indexes: group_students PK, enrollments_one_live_per_institute,
-- teacher_student_support PK, learning_evidence_student_concept_time,
-- idx_attempts_user and idx_qresponses_attempt.
CREATE INDEX IF NOT EXISTS learning_assignments_group_published
  ON learning_assignments (group_id, due_at) WHERE status = 'published';
