ALTER TABLE announcements ADD COLUMN delivery_state text NOT NULL DEFAULT 'idle'
 CHECK(delivery_state IN ('idle','dispatching','completed','failed'));
CREATE INDEX announcements_dispatching ON announcements(id) WHERE delivery_state='dispatching';
