-- Admission requests grant no access. A transfer retains its source membership
-- until the student explicitly accepts the destination's approval.
CREATE TABLE admission_policies (
 institution_id UUID PRIMARY KEY REFERENCES institutions(id),
 policy JSONB NOT NULL
);
ALTER TABLE admission_policies ENABLE ROW LEVEL SECURITY;
CREATE TABLE admission_requests (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
 user_id UUID NOT NULL REFERENCES users(id),
 institution_id UUID NOT NULL REFERENCES institutions(id),
 source_institution_id UUID REFERENCES institutions(id),
 status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','approved','joined','declined','cancelled')),
 reason TEXT NOT NULL DEFAULT '',
 reviewed_by UUID REFERENCES users(id),
 reviewed_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX admission_one_open_per_user ON admission_requests(user_id) WHERE status IN ('pending','approved');
CREATE INDEX admission_institution_queue ON admission_requests(institution_id,status,created_at);
CREATE TABLE admission_targets (
 request_id UUID NOT NULL REFERENCES admission_requests(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('class','claim','institution')),
 target_id UUID NOT NULL,
 code TEXT NOT NULL,
 name TEXT NOT NULL,
 outcome TEXT NOT NULL DEFAULT 'pending' CHECK(outcome IN ('pending','joined','unavailable')),
 PRIMARY KEY(request_id,kind,target_id)
);
ALTER TABLE admission_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE admission_targets ENABLE ROW LEVEL SECURITY;
-- API-only access: policies/allowlists and request details must never leak via
-- Supabase's direct table API. Institutions already have RLS in place.
