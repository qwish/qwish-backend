-- ============================================================================
-- Migration 110: Optional authoring metadata on questions
-- ============================================================================
-- Teachers (and the question-writer import) can attach a Bloom's taxonomy
-- level, the answer explanation, why each other option is wrong, the key
-- concepts and the reasoning behind the difficulty. All optional; NULL means
-- "not written". The authored difficulty reuses questions.difficulty, which
-- seeds the learner rating (scoring.SeedDifficulty) and is refined nightly.
-- None of these columns are part of the question's revision: editing them does
-- not change what a learner answered. Idempotent; safe to re-run.
-- ============================================================================

ALTER TABLE questions
  ADD COLUMN IF NOT EXISTS bloom_level TEXT
    CHECK (bloom_level IN ('remember','understand','apply','analyse','evaluate','create')),
  ADD COLUMN IF NOT EXISTS answer_explanation TEXT CHECK (length(answer_explanation) <= 4000),
  ADD COLUMN IF NOT EXISTS why_not_others TEXT CHECK (length(why_not_others) <= 4000),
  ADD COLUMN IF NOT EXISTS key_concepts TEXT CHECK (length(key_concepts) <= 4000),
  ADD COLUMN IF NOT EXISTS difficulty_rationale TEXT CHECK (length(difficulty_rationale) <= 4000);
