-- Accelerate the substring searches used across admin, institution, teacher,
-- recruiter, and quiz APIs. GIN trigram indexes support ILIKE '%term%'.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS users_display_name_trgm
  ON users USING gin (display_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS users_email_trgm
  ON users USING gin (email gin_trgm_ops);
CREATE INDEX IF NOT EXISTS enrollments_full_name_trgm
  ON enrollments USING gin (full_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS enrollments_email_trgm
  ON enrollments USING gin (email gin_trgm_ops);
CREATE INDEX IF NOT EXISTS enrollments_roll_number_trgm
  ON enrollments USING gin (roll_number gin_trgm_ops);
CREATE INDEX IF NOT EXISTS quizzes_title_trgm
  ON quizzes USING gin (title gin_trgm_ops);
CREATE INDEX IF NOT EXISTS quizzes_description_trgm
  ON quizzes USING gin (description gin_trgm_ops);
CREATE INDEX IF NOT EXISTS institutions_name_trgm
  ON institutions USING gin (name gin_trgm_ops);
