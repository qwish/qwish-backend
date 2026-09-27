-- Operational state the Institute dashboard redesign surfaces.

-- Which students a promotion revert moved back and which it left alone, so
-- the batch history can name the skipped ones.
ALTER TABLE promotion_batch_students
  ADD COLUMN IF NOT EXISTS revert_outcome TEXT CHECK (revert_outcome IN ('reverted','skipped'));

-- When an institution admin verified a teacher. Null for teachers verified
-- before this column existed.
ALTER TABLE users ADD COLUMN IF NOT EXISTS verified_at TIMESTAMPTZ;

-- Successful sign-ins, so an account holder can check "was that me?".
CREATE TABLE IF NOT EXISTS user_sign_ins (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  method TEXT NOT NULL CHECK (method IN ('email_code','passkey')),
  ip TEXT,
  user_agent TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_user_sign_ins_user ON user_sign_ins (user_id, created_at DESC);
ALTER TABLE user_sign_ins ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON user_sign_ins FROM anon, authenticated;

-- Action centre ownership: who in the institution has picked an item up.
-- item_id is text because some items are synthetic (an unclaimed-records
-- group is keyed by grade and section, not by a row).
CREATE TABLE IF NOT EXISTS action_item_owners (
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  item_type TEXT NOT NULL CHECK (item_type IN ('admission','teacher_verification','edit_request','unclaimed_records','topic_request')),
  item_id TEXT NOT NULL,
  owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (institution_id, item_type, item_id)
);
ALTER TABLE action_item_owners ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON action_item_owners FROM anon, authenticated;
