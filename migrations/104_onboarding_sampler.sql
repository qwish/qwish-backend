-- Migration 104: Mixed-topic calibration sampler.
--
-- The first-run quiz draws one question from each of a few subjects instead of
-- playing a single quiz. The drawn set is stored on the session so the guest
-- gets the same questions back after a restart, and so submit grades only the
-- questions that were actually handed out. The interests inferred from the
-- result are written to the existing topics column, which signup already
-- copies into users.interest_domains.
--
-- Each element: {"question_id": uuid, "quiz_id": uuid, "domain": text}.

ALTER TABLE onboarding_sessions ADD COLUMN IF NOT EXISTS sampler JSONB;
