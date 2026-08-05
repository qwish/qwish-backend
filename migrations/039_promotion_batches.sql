-- Promotion used to be a blind bulk UPDATE keyed on grade and section: no
-- record of who ran it, who moved, or what they were before. Rolling one back
-- meant editing students by hand from memory.
--
-- A promotion is now a batch. The class is the unit an admin thinks in, the
-- students inside it are chosen explicitly, and everyone's prior position is
-- kept so the whole thing can be undone.

CREATE TABLE IF NOT EXISTS promotion_batches (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id  UUID NOT NULL REFERENCES institutions(id),
  performed_by    UUID NOT NULL REFERENCES users(id),
  source_group_id UUID REFERENCES groups(id),
  target_group_id UUID REFERENCES groups(id),
  -- The grade and section the admin confirmed on the review step. Kept even
  -- though the target group knows its own name: the two are set together and a
  -- later rename of the class must not rewrite history.
  to_grade        TEXT NOT NULL,
  to_section      TEXT,
  promoted_count  INT NOT NULL DEFAULT 0,
  retained_count  INT NOT NULL DEFAULT 0,
  -- 30 days, per the agreed window. A batch past this is history, not an undo.
  revertible_until TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '30 days',
  reverted_at     TIMESTAMPTZ,
  reverted_by     UUID REFERENCES users(id),
  -- How many the revert declined to touch because someone had since moved them.
  reverted_skipped INT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per student the promotion considered — moved or held back.
--
-- Retained students are recorded deliberately. Filtering a class by score means
-- some students do not go up, and "why is my child still in Grade 9" deserves an
-- answer better than the absence of a record.
CREATE TABLE IF NOT EXISTS promotion_batch_students (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id      UUID NOT NULL REFERENCES promotion_batches(id) ON DELETE CASCADE,
  enrollment_id UUID NOT NULL REFERENCES enrollments(id),
  outcome       TEXT NOT NULL CHECK (outcome IN ('promoted','retained')),
  -- Prior position, for the undo. Null section and null group are real states.
  prior_group_id UUID REFERENCES groups(id),
  prior_grade    TEXT,
  prior_section  TEXT,
  -- Why a retained student stayed: the filter that excluded them, in words.
  retained_reason TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- A student appears once per batch.
CREATE UNIQUE INDEX IF NOT EXISTS promotion_batch_students_unique
  ON promotion_batch_students (batch_id, enrollment_id);

-- The batch list reads by institution, newest first.
CREATE INDEX IF NOT EXISTS promotion_batches_institution_idx
  ON promotion_batches (institution_id, created_at DESC);

-- The revert and the student profile both read a batch's rows by batch.
CREATE INDEX IF NOT EXISTS promotion_batch_students_batch_idx
  ON promotion_batch_students (batch_id);

-- "Has this student ever been held back" reads by enrollment.
CREATE INDEX IF NOT EXISTS promotion_batch_students_enrollment_idx
  ON promotion_batch_students (enrollment_id);

-- FK covering indexes, per 035.
CREATE INDEX IF NOT EXISTS promotion_batches_performed_by_idx ON promotion_batches (performed_by);
CREATE INDEX IF NOT EXISTS promotion_batches_source_group_idx ON promotion_batches (source_group_id);
CREATE INDEX IF NOT EXISTS promotion_batches_target_group_idx ON promotion_batches (target_group_id);

-- Same model as 033: every read and write goes through the Go backend as the
-- table owner, so RLS on with no policy means no PostgREST client sees anything.
ALTER TABLE promotion_batches        ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotion_batch_students ENABLE ROW LEVEL SECURITY;
