-- Interim compatibility labels, not canonical academic placement.
ALTER TABLE enrollments ADD COLUMN IF NOT EXISTS class_label_state TEXT NOT NULL DEFAULT 'unset'
 CHECK (class_label_state IN ('unset','derived','ambiguous'));
CREATE TABLE IF NOT EXISTS academic_label_review (
 enrollment_id UUID PRIMARY KEY, recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 old_grade TEXT, old_section TEXT, combinations JSONB NOT NULL
);
ALTER TABLE academic_label_review ENABLE ROW LEVEL SECURITY;
INSERT INTO academic_label_review(enrollment_id,old_grade,old_section,combinations)
SELECT e.id,e.grade,e.section,jsonb_agg(DISTINCT jsonb_build_object('grade',g.grade,'section',g.section))
FROM enrollments e JOIN group_students gs ON gs.user_id=e.user_id
JOIN groups g ON g.id=gs.group_id AND g.institution_id=e.institution_id
WHERE e.status IN ('active','suspended') AND g.archived_at IS NULL AND g.kind='class' AND (g.grade IS NOT NULL OR g.section IS NOT NULL)
GROUP BY e.id HAVING count(DISTINCT (g.grade,g.section))>1
ON CONFLICT DO NOTHING;

-- Label-only updates must not take account locks: joins lock the account
-- while enforcing the institute cap. Membership guards still run whenever
-- enrollment identity/status changes, including insert/delete synchronization.
DROP TRIGGER IF EXISTS guard_student_enrollment ON enrollments;
CREATE TRIGGER guard_student_enrollment BEFORE INSERT OR UPDATE OF user_id,status,institution_id ON enrollments
FOR EACH ROW EXECUTE FUNCTION guard_student_enrollment();
DROP TRIGGER IF EXISTS sync_student_institute ON enrollments;
CREATE TRIGGER sync_student_institute AFTER INSERT OR DELETE OR UPDATE OF user_id,status,institution_id,joined_at ON enrollments
FOR EACH ROW EXECUTE FUNCTION sync_student_institute();

CREATE OR REPLACE FUNCTION recompute_class_labels(student UUID, institute UUID) RETURNS void LANGUAGE plpgsql AS $$
DECLARE n INT; gr TEXT; sec TEXT;
BEGIN
 -- Lock the live enrollment before reading memberships; concurrent derivations
 -- take a fresh READ COMMITTED snapshot after this lock is acquired.
 PERFORM id FROM enrollments WHERE user_id=student AND institution_id=institute
 AND status IN ('active','suspended') ORDER BY id FOR UPDATE;
 SELECT count(*),min(grade),min(section) INTO n,gr,sec FROM (
 SELECT DISTINCT g.grade,g.section FROM groups g JOIN group_students gs ON gs.group_id=g.id
 WHERE gs.user_id=student AND g.institution_id=institute AND g.kind='class' AND g.archived_at IS NULL AND (g.grade IS NOT NULL OR g.section IS NOT NULL)
 ) labels;
 UPDATE enrollments SET grade=CASE WHEN n=1 THEN gr END,section=CASE WHEN n=1 THEN sec END,
 class_label_state=CASE WHEN n>1 THEN 'ambiguous' WHEN n=1 AND (gr IS NOT NULL OR sec IS NOT NULL) THEN 'derived' ELSE 'unset' END,
 updated_at=now() WHERE user_id=student AND institution_id=institute AND status IN ('active','suspended');
END $$;
CREATE OR REPLACE FUNCTION copy_class_grade() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE gid UUID; uid UUID; inst UUID;
BEGIN
 IF TG_OP='DELETE' THEN gid:=OLD.group_id; uid:=OLD.user_id; ELSE gid:=NEW.group_id; uid:=NEW.user_id; END IF;
 SELECT institution_id INTO inst FROM groups WHERE id=gid AND kind='class';
 IF inst IS NOT NULL THEN PERFORM recompute_class_labels(uid,inst); END IF;
 RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS copy_class_grade ON group_students;
CREATE TRIGGER copy_class_grade AFTER INSERT OR DELETE ON group_students FOR EACH ROW EXECUTE FUNCTION copy_class_grade();
CREATE OR REPLACE FUNCTION refresh_group_labels() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE uid UUID;
BEGIN
 FOR uid IN SELECT user_id FROM group_students WHERE group_id=NEW.id ORDER BY user_id LOOP
 PERFORM recompute_class_labels(uid,NEW.institution_id);
 END LOOP;
 RETURN NULL;
END $$;
CREATE TRIGGER refresh_group_labels AFTER UPDATE OF grade,section,kind,archived_at ON groups
FOR EACH ROW EXECUTE FUNCTION refresh_group_labels();
CREATE OR REPLACE FUNCTION refresh_enrollment_labels() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.user_id IS NOT NULL AND NEW.status IN ('active','suspended') THEN
 PERFORM recompute_class_labels(NEW.user_id,NEW.institution_id);
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER refresh_enrollment_labels AFTER INSERT OR UPDATE OF status,user_id ON enrollments
FOR EACH ROW EXECUTE FUNCTION refresh_enrollment_labels();
DO $$ DECLARE r RECORD; BEGIN
 FOR r IN SELECT DISTINCT user_id,institution_id FROM enrollments WHERE user_id IS NOT NULL AND status IN ('active','suspended') ORDER BY user_id,institution_id LOOP
 PERFORM recompute_class_labels(r.user_id,r.institution_id);
 END LOOP;
END $$;
