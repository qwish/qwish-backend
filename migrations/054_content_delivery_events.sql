-- Learner-side delivery telemetry for admin-authored announcements and promos.
-- One row per user/content/event makes retries idempotent and gives the admin
-- console honest unique reach instead of client-controlled counters.
CREATE TABLE IF NOT EXISTS content_delivery_events (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  content_kind TEXT NOT NULL CHECK (content_kind IN ('announcement', 'promo')),
  content_id UUID NOT NULL,
  event_type TEXT NOT NULL CHECK (event_type IN ('impression', 'click', 'dismiss')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, content_kind, content_id, event_type)
);

CREATE INDEX IF NOT EXISTS idx_content_delivery_events_content
  ON content_delivery_events(content_kind, content_id, event_type);

ALTER TABLE content_delivery_events ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS content_delivery_events_self ON content_delivery_events;
CREATE POLICY content_delivery_events_self ON content_delivery_events
  FOR ALL TO authenticated
  USING (user_id = auth.uid())
  WITH CHECK (user_id = auth.uid());
