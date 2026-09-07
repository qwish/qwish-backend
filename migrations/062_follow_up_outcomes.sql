ALTER TABLE learning_assignments
  ADD COLUMN IF NOT EXISTS source_concept_id UUID REFERENCES curriculum_concepts(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS learning_assignments_follow_up_concept
  ON learning_assignments (institution_id, group_id, created_at DESC)
  WHERE purpose = 'follow_up' AND source_concept_id IS NOT NULL;
