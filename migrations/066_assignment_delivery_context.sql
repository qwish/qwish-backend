ALTER TABLE learning_assignments
  ADD COLUMN IF NOT EXISTS instructions TEXT,
  ADD COLUMN IF NOT EXISTS available_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS attempt_limit INT NOT NULL DEFAULT 1 CHECK (attempt_limit > 0);

ALTER TABLE learning_assignment_recipients
  DROP CONSTRAINT IF EXISTS learning_assignment_recipients_status_check;
ALTER TABLE learning_assignment_recipients
  ADD CONSTRAINT learning_assignment_recipients_status_check
  CHECK (status IN ('assigned','started','submitted','overdue','excused','withdrawn'));
