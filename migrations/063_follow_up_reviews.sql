ALTER TABLE learning_assignments
  ADD COLUMN IF NOT EXISTS follow_up_review_status TEXT NOT NULL DEFAULT 'unreviewed'
    CHECK (follow_up_review_status IN ('unreviewed', 'continue_support', 'resolved')),
  ADD COLUMN IF NOT EXISTS follow_up_note TEXT
    CHECK (follow_up_note IS NULL OR char_length(follow_up_note) <= 2000),
  ADD COLUMN IF NOT EXISTS follow_up_reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS follow_up_reviewed_at TIMESTAMPTZ;

ALTER TABLE learning_assignments
  ADD CONSTRAINT learning_assignments_follow_up_review_consistency CHECK (
    (follow_up_review_status = 'unreviewed' AND follow_up_reviewed_at IS NULL)
    OR
    (follow_up_review_status <> 'unreviewed' AND follow_up_reviewed_at IS NOT NULL)
  );
