-- One email address belongs to exactly one person on exactly one surface.
--
-- Identity lives in two tables: `users` (student, teacher, institution_admin,
-- parent) and `admin_accounts` (super_admin, moderator, support_agent). Each had
-- its own UNIQUE(email), so nothing stopped one address from being a student in
-- the app AND a super admin in the console — two accounts, two sessions, two
-- audit trails, one human.
--
-- Two enforcement gaps, both closed here:
--   1. The constraints were per-table, so they never saw each other.
--   2. UNIQUE on a TEXT column is case-sensitive in Postgres, so Priya@x.com and
--      priya@x.com were already two rows — while the teacher-invite check
--      compares with EqualFold and treats them as one person.
--
-- Enforced with a trigger rather than a shared unique structure on purpose:
-- migrations run at boot behind log.Fatalf, so a constraint that rejects
-- pre-existing data would not fail a deploy, it would fail to start the API.
-- The trigger judges only rows being written. Legacy collisions stay readable in
-- the email_identity_collisions view below and are resolved by hand.

-- ─── 1. Normalise what can be normalised ────────────────────────────────────
--
-- Lowercase every address whose lowercase form is not already claimed by a
-- different row. Rows that would collide are deliberately left untouched: an
-- automatic merge here would silently pick a winner between two real accounts.

UPDATE users u
   SET email = lower(btrim(u.email))
 WHERE u.email <> lower(btrim(u.email))
   AND NOT EXISTS (
     SELECT 1 FROM users o
      WHERE o.id <> u.id
        AND lower(btrim(o.email)) = lower(btrim(u.email))
   );

UPDATE admin_accounts a
   SET email = lower(btrim(a.email))
 WHERE a.email <> lower(btrim(a.email))
   AND NOT EXISTS (
     SELECT 1 FROM admin_accounts o
      WHERE o.id <> a.id
        AND lower(btrim(o.email)) = lower(btrim(a.email))
   );

-- ─── 2. The rule ────────────────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION assert_one_identity_per_email()
RETURNS TRIGGER
LANGUAGE plpgsql
SET search_path = public
AS $$
DECLARE
  holder TEXT;
BEGIN
  -- Normalising here rather than in each caller means every write path gets it,
  -- including ones written after this migration.
  NEW.email := lower(btrim(NEW.email));

  IF NEW.email = '' THEN
    RAISE EXCEPTION 'email is required'
      USING ERRCODE = '23514';
  END IF;

  SELECT 'the Qwish app or a dashboard, as ' || u.role
    INTO holder
    FROM users u
   WHERE lower(btrim(u.email)) = NEW.email
     AND u.deleted_at IS NULL
     AND NOT (TG_TABLE_NAME = 'users' AND u.id = NEW.id)
   LIMIT 1;

  IF holder IS NULL THEN
    SELECT 'the Qwish admin console, as ' || a.role
      INTO holder
      FROM admin_accounts a
     WHERE lower(btrim(a.email)) = NEW.email
       AND a.deleted_at IS NULL
       AND NOT (TG_TABLE_NAME = 'admin_accounts' AND a.id = NEW.id)
     LIMIT 1;
  END IF;

  IF holder IS NOT NULL THEN
    -- 23505 so existing duplicate-key handling still recognises it; the named
    -- constraint is what lets the API tell this apart from an ordinary
    -- unique violation and explain which surface already holds the address.
    RAISE EXCEPTION 'email % is already registered on %', NEW.email, holder
      USING ERRCODE = '23505', CONSTRAINT = 'one_identity_per_email';
  END IF;

  RETURN NEW;
END;
$$;

-- UPDATE OF email, not UPDATE: this must not run on every points or streak write.
DROP TRIGGER IF EXISTS one_identity_per_email ON users;
CREATE TRIGGER one_identity_per_email
  BEFORE INSERT OR UPDATE OF email ON users
  FOR EACH ROW EXECUTE FUNCTION assert_one_identity_per_email();

DROP TRIGGER IF EXISTS one_identity_per_email ON admin_accounts;
CREATE TRIGGER one_identity_per_email
  BEFORE INSERT OR UPDATE OF email ON admin_accounts
  FOR EACH ROW EXECUTE FUNCTION assert_one_identity_per_email();

-- ─── 3. Make the legacy collisions findable ─────────────────────────────────
--
-- Anything the normalisation pass could not fix, plus any address that already
-- spanned both tables before this migration. Expected to be empty; if it is not,
-- each row is one human holding two accounts and needs a human decision.

CREATE OR REPLACE VIEW email_identity_collisions
WITH (security_invoker = true) AS
WITH identities AS (
  SELECT lower(btrim(email)) AS email, 'users' AS source, id::text AS id, role, deleted_at
    FROM users
  UNION ALL
  SELECT lower(btrim(email)), 'admin_accounts', id::text, role, deleted_at
    FROM admin_accounts
)
SELECT email,
       count(*)                                   AS identity_count,
       array_agg(source || ':' || role ORDER BY source, role) AS held_as,
       array_agg(id ORDER BY id)                  AS identity_ids
  FROM identities
 WHERE deleted_at IS NULL
 GROUP BY email
HAVING count(*) > 1;

COMMENT ON VIEW email_identity_collisions IS
  'Addresses holding more than one identity. Pre-existing only — migration 038 blocks new ones.';

-- Supporting the trigger's two lookups. Case-insensitive, matching the rule.
CREATE INDEX IF NOT EXISTS users_lower_email_idx
  ON users (lower(btrim(email))) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS admin_accounts_lower_email_idx
  ON admin_accounts (lower(btrim(email))) WHERE deleted_at IS NULL;
