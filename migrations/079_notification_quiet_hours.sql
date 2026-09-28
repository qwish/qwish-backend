-- Push categories and a device-local quiet window. Existing users keep the
-- current delivery behaviour until they enable quiet hours in the app.
ALTER TABLE notification_preferences
  ADD COLUMN IF NOT EXISTS push_championships BOOLEAN NOT NULL DEFAULT true,
  ADD COLUMN IF NOT EXISTS quiet_hours_enabled BOOLEAN NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS quiet_from_minute SMALLINT NOT NULL DEFAULT 1350,
  ADD COLUMN IF NOT EXISTS quiet_until_minute SMALLINT NOT NULL DEFAULT 420,
  ADD COLUMN IF NOT EXISTS quiet_utc_offset_minutes SMALLINT NOT NULL DEFAULT 330;
