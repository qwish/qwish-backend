-- Referral code resets stay a super-admin power (a code is an institution's
-- only join mechanism, so self-service resets are an abuse surface). The
-- institution's path is to file a request that a super admin actions, which is
-- what this table records. The PRD promised the admin this button; until now
-- the dashboard only told them to email support.

CREATE TABLE IF NOT EXISTS referral_code_reset_requests (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id  UUID NOT NULL REFERENCES institutions(id),
  requested_by    UUID NOT NULL REFERENCES users(id),
  -- 'both' is its own value rather than two rows: an admin who suspects a leak
  -- usually wants the pair rotated together, and a super admin actioning half a
  -- request is the failure mode.
  code_type       TEXT NOT NULL CHECK (code_type IN ('student','teacher','both')),
  reason          TEXT NOT NULL,
  status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','completed','declined')),
  resolution_note TEXT,
  resolved_by     UUID REFERENCES users(id),
  resolved_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One open request per institution. A second click while the first is still
-- pending must not queue a duplicate for the super admin to triage.
CREATE UNIQUE INDEX IF NOT EXISTS referral_reset_one_open_per_institution
  ON referral_code_reset_requests (institution_id)
  WHERE status = 'pending';

-- The super-admin queue reads by status, newest first.
CREATE INDEX IF NOT EXISTS referral_reset_status_created_idx
  ON referral_code_reset_requests (status, created_at DESC);

-- FK covering index, per 035.
CREATE INDEX IF NOT EXISTS referral_reset_requested_by_idx
  ON referral_code_reset_requests (requested_by);

-- Same model as 033: every read and write goes through the Go backend as the
-- table owner, so RLS on with no policy means no PostgREST client sees anything.
ALTER TABLE referral_code_reset_requests ENABLE ROW LEVEL SECURITY;
