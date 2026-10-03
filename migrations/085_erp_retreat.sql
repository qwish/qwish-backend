-- ERP retreat (spec 2026-10-03, plan 2). Destructive: snapshot first.

-- Data step, kept as a function so tests can exercise it on seeded rows.
CREATE OR REPLACE FUNCTION retire_roster_rows() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
  -- Unclaimed rows on a verified institute domain become institute-level invites.
  INSERT INTO student_invites (institution_id, group_id, email, status)
  SELECT DISTINCT e.institution_id, NULL::uuid, lower(btrim(e.email)), 'pending'
    FROM enrollments e
    JOIN institution_domains d ON d.institution_id = e.institution_id
                              AND d.verified_at IS NOT NULL
                              AND d.domain = split_part(lower(btrim(e.email)), '@', 2)
   WHERE e.status = 'pending_claim' AND e.user_id IS NULL AND e.email IS NOT NULL
  ON CONFLICT DO NOTHING;

  -- Unclaimed rows carry no learning history; drop them and their references.
  DELETE FROM promotion_batch_students pbs USING enrollments e
   WHERE pbs.enrollment_id = e.id AND e.status = 'pending_claim';
  DELETE FROM enrollments WHERE status = 'pending_claim';
END $$;

SELECT retire_roster_rows();

DROP TABLE IF EXISTS student_edit_requests;

-- The admin-search version trigger listed roll_number; recreate it without.
DROP TRIGGER IF EXISTS trg_enrollments_student_search_version ON enrollments;
CREATE TRIGGER trg_enrollments_student_search_version
AFTER INSERT OR DELETE OR UPDATE OF user_id, status ON enrollments
FOR EACH STATEMENT EXECUTE FUNCTION bump_admin_student_search_version();
DROP INDEX IF EXISTS enrollments_roll_unique;

ALTER TABLE enrollments
  DROP COLUMN IF EXISTS roll_number,
  DROP COLUMN IF EXISTS admission_date,
  DROP COLUMN IF EXISTS claim_code,
  DROP COLUMN IF EXISTS import_phone,
  DROP COLUMN IF EXISTS import_guardian_name,
  DROP COLUMN IF EXISTS import_guardian_phone,
  DROP COLUMN IF EXISTS import_guardian_email;

ALTER TABLE enrollments DROP CONSTRAINT IF EXISTS enrollments_status_check;
ALTER TABLE enrollments ADD CONSTRAINT enrollments_status_check
  CHECK (status IN ('active','suspended','graduated','transferred','left'));
-- The old default was 'pending_claim', which the check above now forbids.
ALTER TABLE enrollments ALTER COLUMN status SET DEFAULT 'active';

-- Activity responses stored a respondent snapshot that included roll_number.
UPDATE activity_responses SET respondent = respondent - 'roll_number' WHERE respondent ? 'roll_number';

ALTER TABLE users
  DROP COLUMN IF EXISTS date_of_birth,
  DROP COLUMN IF EXISTS gender,
  DROP COLUMN IF EXISTS phone,
  DROP COLUMN IF EXISTS address,
  DROP COLUMN IF EXISTS guardian_name,
  DROP COLUMN IF EXISTS guardian_phone,
  DROP COLUMN IF EXISTS guardian_email,
  DROP COLUMN IF EXISTS highest_qualification;

-- guard_student_enrollment and guard_super_admin_membership still list
-- 'pending_claim'; harmless, but keep them accurate.
CREATE OR REPLACE FUNCTION guard_student_enrollment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE account_role text;
BEGIN
 IF NEW.user_id IS NOT NULL AND NEW.status IN ('active','suspended') THEN
  SELECT role INTO account_role FROM users WHERE id=NEW.user_id FOR UPDATE;
  IF account_role IS DISTINCT FROM 'student' THEN
   RAISE EXCEPTION 'Only students can hold institute enrollments' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
