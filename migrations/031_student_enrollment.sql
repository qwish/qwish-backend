-- Student management: separate the person (users) from their relationship with
-- an institution (enrollments). Institution-owned academic fields live here and
-- have no student-facing write path — the table boundary is the permission model.

CREATE TABLE IF NOT EXISTS enrollments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id  UUID NOT NULL REFERENCES institutions(id),
  user_id         UUID REFERENCES users(id),
  full_name       TEXT NOT NULL,
  email           TEXT,
  -- Staging for import-supplied personal values. Copied onto the users row at
  -- claim time (only into fields the student left blank), then never read again.
  import_phone          TEXT,
  import_guardian_name  TEXT,
  import_guardian_phone TEXT,
  import_guardian_email TEXT,
  roll_number     TEXT,
  grade           TEXT,
  section         TEXT,
  admission_date  DATE,
  claim_code      TEXT UNIQUE,
  status          TEXT NOT NULL DEFAULT 'pending_claim'
                    CHECK (status IN ('pending_claim','active','suspended','graduated','transferred')),
  joined_at       TIMESTAMPTZ,
  ended_at        TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Roll numbers are unique per institution among live enrollments only, so a
-- number is reusable once a student graduates or transfers out.
CREATE UNIQUE INDEX IF NOT EXISTS enrollments_roll_unique
  ON enrollments(institution_id, roll_number)
  WHERE roll_number IS NOT NULL AND ended_at IS NULL;

-- A student holds at most one live enrollment. pending_claim rows have
-- user_id NULL and are exempt, so an institution may pre-provision a student
-- who is currently enrolled elsewhere; the code will not redeem until they leave.
CREATE UNIQUE INDEX IF NOT EXISTS enrollments_one_active_per_user
  ON enrollments(user_id)
  WHERE user_id IS NOT NULL AND status IN ('active','suspended');

CREATE INDEX IF NOT EXISTS enrollments_institution_status ON enrollments(institution_id, status);
CREATE INDEX IF NOT EXISTS enrollments_user ON enrollments(user_id) WHERE user_id IS NOT NULL;

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS date_of_birth         DATE,
  ADD COLUMN IF NOT EXISTS gender                TEXT,
  ADD COLUMN IF NOT EXISTS phone                 TEXT,
  ADD COLUMN IF NOT EXISTS address               TEXT,
  ADD COLUMN IF NOT EXISTS guardian_name         TEXT,
  ADD COLUMN IF NOT EXISTS guardian_phone        TEXT,
  ADD COLUMN IF NOT EXISTS guardian_email        TEXT,
  ADD COLUMN IF NOT EXISTS highest_qualification TEXT;

CREATE TABLE IF NOT EXISTS user_profile_entries (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL CHECK (kind IN ('experience','certification','achievement','course')),
  title       TEXT NOT NULL,
  org         TEXT,
  start_date  DATE,
  end_date    DATE,
  description TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS user_profile_entries_user_kind ON user_profile_entries(user_id, kind);

CREATE TABLE IF NOT EXISTS student_edit_requests (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  enrollment_id  UUID NOT NULL REFERENCES enrollments(id) ON DELETE CASCADE,
  requested_by   UUID NOT NULL REFERENCES users(id),
  field          TEXT NOT NULL CHECK (field IN ('roll_number','grade','section','admission_date')),
  current_value  TEXT,
  proposed_value TEXT NOT NULL,
  note           TEXT,
  status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
  reviewed_by    UUID REFERENCES users(id),
  reviewed_at    TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS student_edit_requests_pending
  ON student_edit_requests(enrollment_id) WHERE status = 'pending';

-- RLS: all writes go through the Go backend (service_role bypasses RLS).
-- These policies restrict SELECT for the `authenticated` Supabase role.
ALTER TABLE enrollments ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_profile_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE student_edit_requests ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS enrollments_select ON enrollments;
CREATE POLICY enrollments_select ON enrollments FOR SELECT USING (
  user_id = auth_user_id()
  OR (auth_institution_id() IS NOT NULL AND institution_id = auth_institution_id())
);

DROP POLICY IF EXISTS user_profile_entries_select ON user_profile_entries;
CREATE POLICY user_profile_entries_select ON user_profile_entries FOR SELECT USING (
  user_id = auth_user_id()
);

DROP POLICY IF EXISTS student_edit_requests_select ON student_edit_requests;
CREATE POLICY student_edit_requests_select ON student_edit_requests FOR SELECT USING (
  requested_by = auth_user_id()
  OR EXISTS (SELECT 1 FROM enrollments e WHERE e.id = enrollment_id
             AND e.institution_id = auth_institution_id())
);
