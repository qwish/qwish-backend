-- Migration 009: Teacher Invites + Notification Log

-- ── teacher_invites ─────────────────────────────────────────────────────────
-- Stores pending email invitations sent by institution admins to prospective
-- teachers. The invite token is embedded in the sign-up link sent over email.
CREATE TABLE IF NOT EXISTS teacher_invites (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    institution_id UUID        NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
    invited_by     UUID        NOT NULL REFERENCES users(id),
    email          TEXT        NOT NULL,
    name           TEXT,
    token          TEXT        NOT NULL UNIQUE,   -- cryptographically random hex token
    status         TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'accepted', 'expired', 'revoked')),
    expires_at     TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '7 days',
    accepted_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_teacher_invites_institution ON teacher_invites(institution_id);
CREATE INDEX IF NOT EXISTS idx_teacher_invites_email       ON teacher_invites(email);
CREATE INDEX IF NOT EXISTS idx_teacher_invites_token       ON teacher_invites(token);
CREATE INDEX IF NOT EXISTS idx_teacher_invites_status      ON teacher_invites(status);

-- ── notification_log ─────────────────────────────────────────────────────────
-- Records every outbound email send attempt (success or failure) so that
-- admins can audit delivery and debug issues without relying on external
-- provider dashboards.
CREATE TABLE IF NOT EXISTS notification_log (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    to_email    TEXT        NOT NULL,
    subject     TEXT        NOT NULL,
    status      TEXT        NOT NULL DEFAULT 'sent'
                                CHECK (status IN ('sent', 'failed')),
    error       TEXT,                   -- provider error message when status='failed'
    reference   TEXT,                   -- free-form context, e.g. 'teacher_invite:<uuid>'
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notification_log_created ON notification_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notification_log_to      ON notification_log(to_email);
CREATE INDEX IF NOT EXISTS idx_notification_log_status  ON notification_log(status);
