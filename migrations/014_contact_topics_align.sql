-- Migration 014: Align contact_submissions.topic with the brand-website form.
-- The website's contact form is the only producer of these rows; its topic set
-- is the source of truth. The original 008 enum diverged from the form values
-- (which forced a lossy client-side mapping), so collapse them to the form set.

-- 1. Remap any existing rows from the old enum to the new values.
UPDATE contact_submissions SET topic = 'institution' WHERE topic = 'institution_onboarding';
UPDATE contact_submissions SET topic = 'other'       WHERE topic IN ('general', 'feedback', 'careers');

-- 2. Swap the CHECK constraint to the website's topic set.
ALTER TABLE contact_submissions DROP CONSTRAINT IF EXISTS contact_submissions_topic_check;
ALTER TABLE contact_submissions ADD  CONSTRAINT contact_submissions_topic_check
  CHECK (topic IN (
    'institution', 'partnership', 'recruiter',
    'calibration', 'press', 'support', 'other'
  ));
