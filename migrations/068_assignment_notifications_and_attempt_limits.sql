ALTER TABLE learning_assignment_recipients
  ADD COLUMN IF NOT EXISTS attempts_started INT NOT NULL DEFAULT 0
  CHECK (attempts_started >= 0);

UPDATE learning_assignment_recipients
SET attempts_started = 1
WHERE attempt_id IS NOT NULL AND attempts_started = 0;

ALTER TABLE notification_preferences
  ADD COLUMN IF NOT EXISTS push_assignments BOOLEAN NOT NULL DEFAULT true;

-- Assignment notification references include both the assignment and delivery
-- window. This is the idempotency boundary for immediate and scheduled sends.
CREATE UNIQUE INDEX IF NOT EXISTS user_notifications_assignment_reference
  ON user_notifications(user_id, reference)
  WHERE kind = 'assignment' AND reference IS NOT NULL;

