-- Migration 008: Contact Form Submissions
-- Stores brand-website contact form submissions, categorised by topic.

CREATE TABLE IF NOT EXISTS contact_submissions (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  topic       TEXT        NOT NULL CHECK (topic IN (
                'general', 'partnership', 'support', 'feedback',
                'press', 'institution_onboarding', 'careers'
              )),
  name        TEXT        NOT NULL,
  email       TEXT        NOT NULL,
  phone       TEXT,
  message     TEXT        NOT NULL,
  metadata    JSONB,                -- optional topic-specific extra fields
  status      TEXT        NOT NULL DEFAULT 'new'
                CHECK (status IN ('new', 'in_progress', 'resolved', 'spam')),
  resolved_by UUID        REFERENCES admin_accounts(id),
  resolved_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_contact_topic   ON contact_submissions(topic);
CREATE INDEX IF NOT EXISTS idx_contact_status  ON contact_submissions(status);
CREATE INDEX IF NOT EXISTS idx_contact_created ON contact_submissions(created_at DESC);
