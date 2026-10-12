-- Neutral age screen (Google Play Families policy, COPPA, India DPDP Act).
-- Asked once at sign-up and never editable, so it is only used to decide
-- whether a learner needs a parent's consent. NULL = signed up before the
-- age screen existed; treated as an adult.
ALTER TABLE users ADD COLUMN IF NOT EXISTS date_of_birth DATE;

-- A minor (under 18, the DPDP threshold, which also covers COPPA's under 13)
-- needs an active parent link before social features or a public profile.
-- A parent withdraws consent by revoking the link.
CREATE OR REPLACE FUNCTION parental_consent_required(uid UUID) RETURNS boolean
LANGUAGE sql STABLE AS $$
  SELECT COALESCE(
    (SELECT u.date_of_birth > current_date - interval '18 years' FROM users u WHERE u.id = uid),
    false)
  AND NOT EXISTS (
    SELECT 1 FROM parent_student_links l WHERE l.student_id = uid AND l.status = 'active')
$$;
