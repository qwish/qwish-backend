ALTER TABLE quizzes
  ADD COLUMN IF NOT EXISTS curriculum_concept_id UUID
  REFERENCES curriculum_concepts(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS quizzes_curriculum_concept
  ON quizzes(curriculum_concept_id)
  WHERE curriculum_concept_id IS NOT NULL;
