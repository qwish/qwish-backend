-- Roster import matches live enrollments by roll number or by email.
-- enrollments_roll_unique(institution_id, roll_number) already covers the roll
-- number branch; the email branch had nothing, so every emailed row fell back
-- to scanning the institution's enrollments.
CREATE INDEX IF NOT EXISTS enrollments_institution_email
  ON enrollments(institution_id, email)
  WHERE email IS NOT NULL AND ended_at IS NULL;
