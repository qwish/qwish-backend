-- Allow an assessment to cover more than one curriculum unit (a published
-- curriculum chapter) without forcing every authored question to be mapped.
ALTER TABLE quizzes
  ADD COLUMN IF NOT EXISTS curriculum_question_mapping_enabled BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS quiz_curriculum_units (
  quiz_id UUID NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
  unit_id UUID NOT NULL REFERENCES curriculum_chapters(id) ON DELETE RESTRICT,
  position INT NOT NULL DEFAULT 1 CHECK (position > 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (quiz_id, unit_id)
);

CREATE INDEX IF NOT EXISTS quiz_curriculum_units_unit
  ON quiz_curriculum_units(unit_id);

ALTER TABLE quiz_curriculum_units ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON quiz_curriculum_units FROM anon, authenticated;

-- Preserve the unit context of quizzes created before multi-unit authoring.
INSERT INTO quiz_curriculum_units (quiz_id, unit_id, position)
SELECT q.id, ch.id, 1
FROM quizzes q
JOIN curriculum_concepts co ON co.id = q.curriculum_concept_id
JOIN curriculum_chapters ch ON ch.id = co.chapter_id
WHERE q.curriculum_concept_id IS NOT NULL
ON CONFLICT (quiz_id, unit_id) DO NOTHING;
