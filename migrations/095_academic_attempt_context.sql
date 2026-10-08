-- Preserve assignment context across retakes and roster transfers.
ALTER TABLE quiz_attempts ADD COLUMN IF NOT EXISTS assignment_id UUID REFERENCES learning_assignments(id) ON DELETE RESTRICT;
-- Only backfill uniquely linked attempts. Ambiguous evidence remains untouched.
UPDATE quiz_attempts qa SET assignment_id=x.assignment_id
FROM (SELECT ar.attempt_id,(array_agg(ar.assignment_id))[1] assignment_id
 FROM learning_assignment_recipients ar JOIN learning_assignments a ON a.id=ar.assignment_id
 JOIN quiz_attempts qa ON qa.id=ar.attempt_id AND qa.user_id=ar.student_id AND qa.quiz_id=a.quiz_id
 GROUP BY ar.attempt_id HAVING count(*)=1) x
WHERE qa.id=x.attempt_id AND qa.assignment_id IS NULL;
CREATE INDEX IF NOT EXISTS quiz_attempts_assignment_completed ON quiz_attempts(assignment_id,completed_at) WHERE status='completed';

-- Freeze the provable legacy context at rollout. Later quiz edits/assignments
-- must not move historical evidence to another class.
ALTER TABLE quiz_attempts ADD COLUMN IF NOT EXISTS legacy_group_id UUID REFERENCES groups(id) ON DELETE RESTRICT;
UPDATE quiz_attempts qa SET legacy_group_id=q.group_id FROM quizzes q JOIN groups g ON g.id=q.group_id AND g.institution_id=q.institution_id
WHERE qa.quiz_id=q.id AND qa.assignment_id IS NULL AND q.visibility<>'public'
AND NOT EXISTS(SELECT 1 FROM learning_assignments a WHERE a.quiz_id=q.id)
AND NOT EXISTS(SELECT 1 FROM learning_assignment_recipients ar WHERE ar.attempt_id=qa.id);

CREATE OR REPLACE FUNCTION preserve_attempt_class_context() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.assignment_id IS DISTINCT FROM OLD.assignment_id OR NEW.legacy_group_id IS DISTINCT FROM OLD.legacy_group_id THEN
  RAISE EXCEPTION 'attempt class context is immutable' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER preserve_attempt_class_context BEFORE UPDATE OF assignment_id,legacy_group_id ON quiz_attempts
FOR EACH ROW EXECUTE FUNCTION preserve_attempt_class_context();
