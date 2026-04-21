-- Add onboarding supplementary columns to institutions table
-- These are filled during self-registration and are read-only after approval.

ALTER TABLE institutions
  ADD COLUMN IF NOT EXISTS onboarding_admin_name TEXT,
  ADD COLUMN IF NOT EXISTS onboarding_phone      TEXT,
  ADD COLUMN IF NOT EXISTS onboarding_website    TEXT,
  ADD COLUMN IF NOT EXISTS onboarding_city       TEXT,
  ADD COLUMN IF NOT EXISTS onboarding_state      TEXT,
  ADD COLUMN IF NOT EXISTS onboarding_country    TEXT;
