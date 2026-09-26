-- Enrollment is the source of truth for student membership. Preserve ended
-- relationships; never resurrect a graduate/transfer from a stale users pointer.
UPDATE enrollments e SET status='transferred',ended_at=COALESCE(ended_at,now()),updated_at=now()
FROM users u WHERE u.id=e.user_id AND u.role='super_admin'
  AND e.status IN ('pending_claim','active','suspended');
DELETE FROM group_students gs USING users u WHERE gs.user_id=u.id AND u.role='super_admin';
UPDATE admission_requests r SET status='cancelled',updated_at=now()
FROM users u WHERE u.id=r.user_id AND u.role='super_admin' AND r.status IN ('pending','approved');
UPDATE users SET institution_id=NULL,updated_at=now() WHERE role='super_admin' AND institution_id IS NOT NULL;

WITH matches AS (
 SELECT u.id AS user_id,min(e.id::text)::uuid AS enrollment_id
 FROM users u JOIN enrollments e ON e.institution_id=u.institution_id
  AND lower(btrim(e.email))=lower(btrim(u.email)) AND e.user_id IS NULL AND e.status='pending_claim'
 WHERE u.role='student' AND u.deleted_at IS NULL AND u.status IN ('active','suspended')
 AND NOT EXISTS(SELECT 1 FROM enrollments linked WHERE linked.user_id=u.id)
 AND NOT EXISTS(SELECT 1 FROM admission_requests r WHERE r.user_id=u.id AND r.status IN ('pending','approved'))
 GROUP BY u.id HAVING count(*)=1
)
UPDATE enrollments e SET user_id=m.user_id,status=CASE WHEN u.status='suspended' THEN 'suspended' ELSE 'active' END,
 claim_code=NULL,joined_at=u.member_since,updated_at=now()
FROM matches m JOIN users u ON u.id=m.user_id WHERE e.id=m.enrollment_id;

-- Repair old referral signups whose account was attached without a roster row.
-- Leave existing enrollment history intact and don't approve pending admissions.
INSERT INTO enrollments(institution_id,user_id,full_name,email,status,joined_at)
SELECT u.institution_id,u.id,COALESCE(NULLIF(u.full_name,''),u.display_name),u.email,
       CASE WHEN u.status='suspended' THEN 'suspended' ELSE 'active' END,u.member_since
FROM users u WHERE u.role='student' AND u.deleted_at IS NULL
 AND u.status IN ('active','suspended') AND u.institution_id IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM enrollments e WHERE e.user_id=u.id)
 AND NOT EXISTS(SELECT 1 FROM admission_requests r WHERE r.user_id=u.id AND r.status IN ('pending','approved'));

UPDATE users u SET institution_id=e.institution_id,updated_at=now()
FROM enrollments e WHERE e.user_id=u.id AND u.role='student'
 AND e.status IN ('active','suspended') AND u.institution_id IS DISTINCT FROM e.institution_id;
UPDATE users u SET institution_id=NULL,updated_at=now()
WHERE u.role='student' AND u.institution_id IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM enrollments e WHERE e.user_id=u.id AND e.status IN ('active','suspended'));

ALTER TABLE users ADD CONSTRAINT super_admin_has_no_institute
 CHECK (role<>'super_admin' OR institution_id IS NULL);

CREATE FUNCTION guard_student_enrollment() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE account_role text;
BEGIN
 IF NEW.user_id IS NOT NULL AND NEW.status IN ('pending_claim','active','suspended') THEN
  SELECT role INTO account_role FROM users WHERE id=NEW.user_id FOR UPDATE;
  IF account_role IS DISTINCT FROM 'student' THEN
   RAISE EXCEPTION 'Only students can hold institute enrollments' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER guard_student_enrollment BEFORE INSERT OR UPDATE ON enrollments
 FOR EACH ROW EXECUTE FUNCTION guard_student_enrollment();

CREATE FUNCTION guard_super_admin_membership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.role='super_admin' AND (
  EXISTS(SELECT 1 FROM enrollments WHERE user_id=NEW.id AND status IN ('pending_claim','active','suspended'))
  OR EXISTS(SELECT 1 FROM group_students WHERE user_id=NEW.id)
  OR EXISTS(SELECT 1 FROM admission_requests WHERE user_id=NEW.id AND status IN ('pending','approved'))
 ) THEN
  RAISE EXCEPTION 'End institute membership before promoting a super admin' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER guard_super_admin_membership BEFORE UPDATE OF role ON users
 FOR EACH ROW EXECUTE FUNCTION guard_super_admin_membership();

-- Keep the denormalized account pointer aligned after joins, transfers, merges,
-- graduation and deletion. Services still own enrollment status and approvals.
CREATE FUNCTION sync_student_institute() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target uuid;
BEGIN
 FOR target IN SELECT DISTINCT id FROM unnest(ARRAY[
  CASE WHEN TG_OP<>'INSERT' THEN OLD.user_id END,
  CASE WHEN TG_OP<>'DELETE' THEN NEW.user_id END]) AS ids(id) WHERE id IS NOT NULL
 LOOP
  UPDATE users u SET institution_id=(SELECT e.institution_id FROM enrollments e
   WHERE e.user_id=target AND e.status IN ('active','suspended')),updated_at=now()
  WHERE u.id=target AND u.role='student';
 END LOOP;
 RETURN NULL;
END $$;
CREATE TRIGGER sync_student_institute AFTER INSERT OR UPDATE OR DELETE ON enrollments
 FOR EACH ROW EXECUTE FUNCTION sync_student_institute();

CREATE FUNCTION guard_super_admin_class() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE account_role text;
BEGIN
 SELECT role INTO account_role FROM users WHERE id=NEW.user_id FOR UPDATE;
 IF account_role='super_admin' THEN
  RAISE EXCEPTION 'Super admins cannot join institute classes' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER guard_super_admin_class BEFORE INSERT OR UPDATE ON group_students
 FOR EACH ROW EXECUTE FUNCTION guard_super_admin_class();
