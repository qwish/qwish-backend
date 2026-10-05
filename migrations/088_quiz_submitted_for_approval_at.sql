-- Approval age is measured from the moment a quiz entered pending_approval.
-- The trigger stamps every entry, whichever code path or console makes it.
-- No backfill: quizzes pending before this migration show "age unavailable"
-- rather than an age guessed from created_at or updated_at.
ALTER TABLE quizzes ADD COLUMN IF NOT EXISTS submitted_for_approval_at TIMESTAMPTZ NULL;

CREATE OR REPLACE FUNCTION stamp_quiz_submitted_for_approval() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'pending_approval'
     AND (TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM 'pending_approval') THEN
    NEW.submitted_for_approval_at := now();
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS quizzes_submitted_for_approval ON quizzes;
CREATE TRIGGER quizzes_submitted_for_approval
  BEFORE INSERT OR UPDATE OF status ON quizzes
  FOR EACH ROW EXECUTE FUNCTION stamp_quiz_submitted_for_approval();
