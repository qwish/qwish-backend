-- Teachers who self-join an institution via its referral code sit in a 'pending'
-- state until the institution verifies them. Widen the users.status CHECK to
-- allow 'pending' (append-only; the original inline constraint from 001 is named
-- users_status_check by Postgres).
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE users ADD CONSTRAINT users_status_check
  CHECK (status IN ('active','pending','suspended','deleted'));
