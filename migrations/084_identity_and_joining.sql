-- Learning-layer identity and joining (spec 2026-10-03, plan 1).
-- A student owns one account, may verify extra emails, joins classes by code,
-- and may hold live enrollments at up to two institutes. users.institution_id
-- becomes the student's *active* institute. Admissions are retired.

-- Secondary emails. The login email stays on users.email.
CREATE TABLE IF NOT EXISTS user_emails (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  email           TEXT NOT NULL CHECK (email = lower(btrim(email)) AND position('@' IN email) > 1),
  verified_at     TIMESTAMPTZ,
  code_hash       TEXT,
  code_expires_at TIMESTAMPTZ,
  attempts        INT NOT NULL DEFAULT 0,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, email)
);
CREATE UNIQUE INDEX IF NOT EXISTS user_emails_verified_unique ON user_emails(email) WHERE verified_at IS NOT NULL;
ALTER TABLE user_emails ENABLE ROW LEVEL SECURITY;

-- Institute email domains, verified by a super-admin.
CREATE TABLE IF NOT EXISTS institution_domains (
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  domain         TEXT NOT NULL CHECK (domain = lower(btrim(domain)) AND position('.' IN domain) > 1 AND position('@' IN domain) = 0),
  verified_at    TIMESTAMPTZ,
  verified_by    UUID REFERENCES admin_accounts(id),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (institution_id, domain)
);
CREATE UNIQUE INDEX IF NOT EXISTS institution_domains_verified_unique ON institution_domains(domain) WHERE verified_at IS NOT NULL;
ALTER TABLE institution_domains ENABLE ROW LEVEL SECURITY;

-- Per-class joining switch.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS joining_enabled BOOLEAN NOT NULL DEFAULT true;

-- Invites to institute-domain emails. group_id NULL = institute-level (migration only).
CREATE TABLE IF NOT EXISTS student_invites (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  group_id       UUID REFERENCES groups(id) ON DELETE CASCADE,
  email          TEXT NOT NULL CHECK (email = lower(btrim(email))),
  invited_by     UUID REFERENCES users(id) ON DELETE SET NULL,
  status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','revoked')),
  accepted_by    UUID REFERENCES users(id) ON DELETE SET NULL,
  accepted_at    TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS student_invites_one_pending
  ON student_invites(institution_id, COALESCE(group_id, '00000000-0000-0000-0000-000000000000'::uuid), email)
  WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS student_invites_email_pending ON student_invites(email) WHERE status = 'pending';
ALTER TABLE student_invites ENABLE ROW LEVEL SECURITY;

-- Enrollment lifecycle additions.
ALTER TABLE enrollments ADD COLUMN IF NOT EXISTS join_route TEXT CHECK (join_route IN ('domain','invite','code'));
ALTER TABLE enrollments ADD COLUMN IF NOT EXISTS ended_by TEXT CHECK (ended_by IN ('student','system','institution'));
ALTER TABLE enrollments ADD COLUMN IF NOT EXISTS end_warned_at TIMESTAMPTZ;
ALTER TABLE enrollments DROP CONSTRAINT IF EXISTS enrollments_status_check;
ALTER TABLE enrollments ADD CONSTRAINT enrollments_status_check
  CHECK (status IN ('pending_claim','active','suspended','graduated','transferred','left'));

-- One live enrollment per institute, not per student. The cap of two is
-- enforced in the join transaction (a count, which an index cannot express).
DROP INDEX IF EXISTS enrollments_one_active_per_user;
CREATE UNIQUE INDEX IF NOT EXISTS enrollments_one_live_per_institute
  ON enrollments(user_id, institution_id)
  WHERE user_id IS NOT NULL AND status IN ('active','suspended');

-- users.institution_id is now the student's active institute: keep it if it is
-- still live, otherwise move to the most recently joined live one, else NULL.
CREATE OR REPLACE FUNCTION sync_student_institute() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE target uuid;
BEGIN
 FOR target IN SELECT DISTINCT id FROM unnest(ARRAY[
  CASE WHEN TG_OP<>'INSERT' THEN OLD.user_id END,
  CASE WHEN TG_OP<>'DELETE' THEN NEW.user_id END]) AS ids(id) WHERE id IS NOT NULL
 LOOP
  UPDATE users u SET institution_id = COALESCE(
     (SELECT e.institution_id FROM enrollments e
       WHERE e.user_id=target AND e.institution_id=u.institution_id AND e.status IN ('active','suspended')),
     (SELECT e.institution_id FROM enrollments e
       WHERE e.user_id=target AND e.status IN ('active','suspended')
       ORDER BY COALESCE(e.joined_at, e.created_at) DESC LIMIT 1)),
    updated_at=now()
  WHERE u.id=target AND u.role='student';
 END LOOP;
 RETURN NULL;
END $$;

-- Resolve open admission requests before retiring admissions: every class is
-- joinable right now (joining_enabled defaults true), so open requests at a
-- verified institute are approved; the rest are declined. Students are told.
CREATE TEMP TABLE open_admissions ON COMMIT DROP AS
SELECT r.id, r.user_id, r.institution_id, i.status = 'verified' AS approvable, i.name AS institution_name
  FROM admission_requests r
  JOIN institutions i ON i.id = r.institution_id
  JOIN users u ON u.id = r.user_id AND u.role = 'student' AND u.deleted_at IS NULL
 WHERE r.status IN ('pending','approved');

-- Claim targets link their roster row.
UPDATE enrollments e SET user_id=o.user_id, status='active', joined_at=now(), claim_code=NULL,
       join_route='invite', updated_at=now()
  FROM open_admissions o JOIN admission_targets t ON t.request_id=o.id AND t.kind='claim'
 WHERE o.approvable AND e.id=t.target_id AND e.status='pending_claim'
   AND NOT EXISTS (SELECT 1 FROM enrollments x WHERE x.user_id=o.user_id AND x.institution_id=o.institution_id AND x.status IN ('active','suspended'));

-- Everything else gets a fresh enrollment.
INSERT INTO enrollments (institution_id, user_id, full_name, email, status, joined_at, join_route)
SELECT o.institution_id, u.id, COALESCE(NULLIF(u.full_name,''), u.display_name), u.email, 'active', now(), 'code'
  FROM open_admissions o JOIN users u ON u.id=o.user_id
 WHERE o.approvable
   AND NOT EXISTS (SELECT 1 FROM enrollments x WHERE x.user_id=o.user_id AND x.institution_id=o.institution_id AND x.status IN ('active','suspended'));

-- Class targets get their membership.
INSERT INTO group_students (group_id, user_id)
SELECT g.id, o.user_id
  FROM open_admissions o JOIN admission_targets t ON t.request_id=o.id AND t.kind='class'
  JOIN groups g ON g.id=t.target_id AND g.archived_at IS NULL AND g.institution_id=o.institution_id
 WHERE o.approvable
ON CONFLICT DO NOTHING;

INSERT INTO user_notifications (user_id, kind, title, body, icon, reference)
SELECT o.user_id, 'system',
       CASE WHEN o.approvable THEN 'You joined ' || o.institution_name ELSE 'Your request to join ' || o.institution_name || ' was closed' END,
       CASE WHEN o.approvable THEN 'Your join request was approved.' ELSE 'This institute is not accepting students right now.' END,
       'school', 'admission:' || o.id
  FROM open_admissions o;

-- super-admin guard referenced admission_requests; redefine without it.
CREATE OR REPLACE FUNCTION guard_super_admin_membership() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.role='super_admin' AND (
  EXISTS(SELECT 1 FROM enrollments WHERE user_id=NEW.id AND status IN ('pending_claim','active','suspended'))
  OR EXISTS(SELECT 1 FROM group_students WHERE user_id=NEW.id)
 ) THEN
  RAISE EXCEPTION 'End institute membership before promoting a super admin' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;

DROP TABLE IF EXISTS admission_targets;
DROP TABLE IF EXISTS admission_requests;
DROP TABLE IF EXISTS admission_policies;
