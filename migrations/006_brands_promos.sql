-- Brands and sponsorship requests

CREATE TABLE IF NOT EXISTS brands (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name           TEXT NOT NULL,
  industry       TEXT,
  contact_email  TEXT,
  website        TEXT,
  reward_pool    NUMERIC(12,2) NOT NULL DEFAULT 0,
  status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','active','suspended')),
  created_by     UUID REFERENCES admin_accounts(id),
  approved_by    UUID REFERENCES admin_accounts(id),
  approved_at    TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sponsorship_requests (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  brand_id     UUID NOT NULL REFERENCES brands(id) ON DELETE CASCADE,
  quiz_id      UUID REFERENCES quizzes(id) ON DELETE SET NULL,
  status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending','approved','rejected')),
  reason       TEXT,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  reviewed_by  UUID REFERENCES admin_accounts(id),
  reviewed_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_brands_status ON brands(status);
CREATE INDEX IF NOT EXISTS idx_sponsorship_requests_brand ON sponsorship_requests(brand_id);
CREATE INDEX IF NOT EXISTS idx_sponsorship_requests_status ON sponsorship_requests(status);
