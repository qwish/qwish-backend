-- Sections a learner hides from their public profile (standing, strengths,
-- activity, portfolio). Empty means everything is shown. The server blanks
-- hidden sections before responding, so hidden data never leaves the backend.
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS hidden_profile_sections TEXT[] NOT NULL DEFAULT '{}';
