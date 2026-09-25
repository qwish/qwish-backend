-- Qwish Score right after each completed attempt, so the Insights trend plots
-- the rating itself. Existing rows are filled by scoring.BackfillRatings.
ALTER TABLE quiz_attempts ADD COLUMN IF NOT EXISTS qwish_score_after DOUBLE PRECISION;
