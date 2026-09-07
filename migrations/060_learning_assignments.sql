CREATE TABLE IF NOT EXISTS learning_assignments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  group_id UUID NOT NULL,
  quiz_id UUID NOT NULL REFERENCES quizzes(id) ON DELETE RESTRICT,
  purpose TEXT NOT NULL CHECK (purpose IN ('baseline','practice','follow_up','diagnostic')),
  status TEXT NOT NULL DEFAULT 'published' CHECK (status IN ('draft','published','closed')),
  due_at TIMESTAMPTZ,
  created_by UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  FOREIGN KEY (group_id, institution_id) REFERENCES groups(id, institution_id)
);
CREATE TABLE IF NOT EXISTS learning_assignment_recipients (
  assignment_id UUID NOT NULL REFERENCES learning_assignments(id) ON DELETE CASCADE,
  student_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'assigned' CHECK (status IN ('assigned','started','submitted','overdue','excused')),
  attempt_id UUID REFERENCES quiz_attempts(id) ON DELETE SET NULL,
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  submitted_at TIMESTAMPTZ,
  PRIMARY KEY (assignment_id, student_id)
);
CREATE INDEX IF NOT EXISTS learning_assignment_recipient_inbox ON learning_assignment_recipients(student_id,status);
ALTER TABLE learning_assignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE learning_assignment_recipients ENABLE ROW LEVEL SECURITY;
