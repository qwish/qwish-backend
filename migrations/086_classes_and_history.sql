-- Classes carry grade/section/kind; membership history replaces promotion
-- batches (spec 2026-10-03, plan 3).

ALTER TABLE groups
  ADD COLUMN IF NOT EXISTS grade   TEXT CHECK (grade IS NULL OR char_length(grade) <= 40),
  ADD COLUMN IF NOT EXISTS section TEXT CHECK (section IS NULL OR char_length(section) <= 40),
  ADD COLUMN IF NOT EXISTS kind    TEXT NOT NULL DEFAULT 'class' CHECK (kind IN ('class','remedial'));

CREATE TABLE IF NOT EXISTS group_student_history (
  id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  group_id  UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
  user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  joined_at TIMESTAMPTZ NOT NULL,
  left_at   TIMESTAMPTZ NOT NULL,
  -- Snapshot of the class at the time; promotion-converted rows predate class grades.
  grade     TEXT,
  section   TEXT
);
CREATE INDEX IF NOT EXISTS group_student_history_user ON group_student_history(user_id, left_at DESC);
ALTER TABLE group_student_history ENABLE ROW LEVEL SECURITY;

-- A main class with a grade defines the student's grade at that institute.
CREATE OR REPLACE FUNCTION copy_class_grade() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE enrollments e SET grade=g.grade, section=COALESCE(g.section, e.section), updated_at=now()
    FROM groups g
   WHERE g.id=NEW.group_id AND g.kind='class' AND g.grade IS NOT NULL
     AND e.user_id=NEW.user_id AND e.institution_id=g.institution_id AND e.status IN ('active','suspended');
  RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS copy_class_grade ON group_students;
CREATE TRIGGER copy_class_grade AFTER INSERT ON group_students
  FOR EACH ROW EXECUTE FUNCTION copy_class_grade();

-- Removing a membership keeps it as history, so past classes survive leaving.
CREATE OR REPLACE FUNCTION record_membership_history() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  -- A cascade from deleting the user or the group leaves nothing to record.
  INSERT INTO group_student_history (group_id, user_id, joined_at, left_at, grade, section)
  SELECT OLD.group_id, OLD.user_id, OLD.joined_at, now(), g.grade, g.section
    FROM groups g WHERE g.id=OLD.group_id
     AND EXISTS (SELECT 1 FROM users u WHERE u.id=OLD.user_id);
  RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS record_membership_history ON group_students;
CREATE TRIGGER record_membership_history AFTER DELETE ON group_students
  FOR EACH ROW EXECUTE FUNCTION record_membership_history();

-- Promotion batches become history: each unreverted promotion closed the
-- student's stay in its source class.
CREATE OR REPLACE FUNCTION convert_promotions_to_history() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO group_student_history (group_id, user_id, joined_at, left_at, grade, section)
  SELECT pbs.prior_group_id, e.user_id, COALESCE(e.joined_at, e.created_at), b.created_at, pbs.prior_grade, pbs.prior_section
    FROM promotion_batch_students pbs
    JOIN promotion_batches b ON b.id=pbs.batch_id
    JOIN enrollments e ON e.id=pbs.enrollment_id
   WHERE pbs.outcome='promoted' AND b.reverted_at IS NULL
     AND pbs.prior_group_id IS NOT NULL AND e.user_id IS NOT NULL;
END $$;

SELECT convert_promotions_to_history();
DROP TABLE IF EXISTS promotion_batch_students;
DROP TABLE IF EXISTS promotion_batches;
