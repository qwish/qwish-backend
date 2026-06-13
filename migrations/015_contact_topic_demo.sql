-- Migration 015: Add 'demo' to the contact_submissions topic set.
-- Institutions/teams can request a product demo from the contact form.

ALTER TABLE contact_submissions DROP CONSTRAINT IF EXISTS contact_submissions_topic_check;
ALTER TABLE contact_submissions ADD  CONSTRAINT contact_submissions_topic_check
  CHECK (topic IN (
    'institution', 'partnership', 'recruiter',
    'calibration', 'press', 'support', 'demo', 'other'
  ));
