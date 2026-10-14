-- Share of a first-time answer's evidence the skill rating dropped (see
-- internal/domain/scoring/effort.go). NULL means 0: written only for
-- discounted answers, and read by BackfillRatings so a replay still matches
-- after the behaviour events that justified it expire.
ALTER TABLE question_responses ADD COLUMN IF NOT EXISTS rating_discount REAL
  CHECK (rating_discount IS NULL OR rating_discount BETWEEN 0 AND 1);
