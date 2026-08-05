-- The institution audit log was scoped by guessing from target_id: the row was
-- yours if the target WAS your institution, or was one of your users. Anything
-- targeting another entity you own — a teacher invite, a group — matched
-- neither branch and silently never appeared in your log. `invite_teacher` has
-- been written since day one and has never been visible to the institution that
-- performed it.
--
-- Owning institution is a fact about the entry, not something to re-derive from
-- the target's type on every read. Record it.

ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS institution_id UUID REFERENCES institutions(id);

-- Backfill the two shapes the old query could already see, so history keeps
-- reading the same after the query switches to the column.
UPDATE audit_log al
   SET institution_id = al.target_id
  FROM institutions i
 WHERE al.institution_id IS NULL
   AND al.target_id = i.id;

UPDATE audit_log al
   SET institution_id = u.institution_id
  FROM users u
 WHERE al.institution_id IS NULL
   AND al.target_id = u.id
   AND u.institution_id IS NOT NULL;

-- Entries that were invisible can be recovered where the target still exists.
UPDATE audit_log al
   SET institution_id = ti.institution_id
  FROM teacher_invites ti
 WHERE al.institution_id IS NULL
   AND al.target_id = ti.id;

UPDATE audit_log al
   SET institution_id = g.institution_id
  FROM groups g
 WHERE al.institution_id IS NULL
   AND al.target_id = g.id;

-- The institution log reads by institution, newest first.
CREATE INDEX IF NOT EXISTS audit_log_institution_timestamp_idx
  ON audit_log (institution_id, timestamp DESC)
  WHERE institution_id IS NOT NULL;
