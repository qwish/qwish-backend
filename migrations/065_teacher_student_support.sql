CREATE TABLE IF NOT EXISTS teacher_student_support (
  teacher_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  note TEXT NOT NULL DEFAULT '',
  plan TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'monitoring' CHECK (status IN ('monitoring','supporting','resolved')),
  review_on DATE,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (teacher_id, student_id)
);

ALTER TABLE teacher_student_support ENABLE ROW LEVEL SECURITY;
CREATE INDEX IF NOT EXISTS teacher_student_support_teacher_updated ON teacher_student_support(teacher_id, updated_at DESC);
CREATE POLICY teacher_student_support_select ON teacher_student_support FOR SELECT TO authenticated USING (teacher_id = auth_user_id());
CREATE POLICY teacher_student_support_insert ON teacher_student_support FOR INSERT TO authenticated WITH CHECK (teacher_id = auth_user_id());
CREATE POLICY teacher_student_support_update ON teacher_student_support FOR UPDATE TO authenticated USING (teacher_id = auth_user_id()) WITH CHECK (teacher_id = auth_user_id());
CREATE POLICY teacher_student_support_delete ON teacher_student_support FOR DELETE TO authenticated USING (teacher_id = auth_user_id());
