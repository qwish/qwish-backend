-- Version the fields represented by the in-memory student-search Bloom filter.
-- Every API instance checks this cheap counter before trusting a negative.
CREATE TABLE IF NOT EXISTS internal_search_versions (
  key     TEXT PRIMARY KEY,
  version BIGINT NOT NULL DEFAULT 1
);
ALTER TABLE internal_search_versions ENABLE ROW LEVEL SECURITY;

INSERT INTO internal_search_versions (key, version)
VALUES ('admin_students', 1)
ON CONFLICT (key) DO NOTHING;

CREATE OR REPLACE FUNCTION bump_admin_student_search_version()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public
AS $$
BEGIN
  UPDATE internal_search_versions
     SET version = version + 1
   WHERE key = 'admin_students';
  RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_users_student_search_version ON users;
CREATE TRIGGER trg_users_student_search_version
AFTER INSERT OR DELETE OR UPDATE OF display_name, email, role ON users
FOR EACH STATEMENT EXECUTE FUNCTION bump_admin_student_search_version();

DROP TRIGGER IF EXISTS trg_enrollments_student_search_version ON enrollments;
CREATE TRIGGER trg_enrollments_student_search_version
AFTER INSERT OR DELETE OR UPDATE OF user_id, roll_number, status ON enrollments
FOR EACH STATEMENT EXECUTE FUNCTION bump_admin_student_search_version();
