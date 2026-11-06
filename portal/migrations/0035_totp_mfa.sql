-- 0035: TOTP-based multi-factor authentication (RFC 6238).
--
-- users.totp_secret_enc      AES-GCM ciphertext (pkg/crypto, ENCRYPTION_KEY) of the
--                            base32 secret. Written at setup time; the secret only
--                            becomes active once totp_enabled flips to true.
-- users.totp_enabled         second factor is required at login when true.
-- users.totp_enabled_at      when enrolment was confirmed.
-- users.totp_last_step       last accepted 30s time step; a code whose step is <=
--                            this value is rejected (replay protection).
-- user_recovery_codes        sha256 hex of one-time recovery codes (shown once).
-- enterprises.require_mfa    enterprise-wide policy switch (default off).
-- system_settings
--   mfa_required_roles       comma separated roles that must enrol. Empty (the
--                            default) forces nobody, so deploying this migration
--                            cannot lock anybody out.
--
-- Safe to re-run: every statement is idempotent. internal/db/schema.go mirrors
-- these changes for fresh databases and the schema-vs-code checker.

ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret_enc TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled_at TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_step BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS user_recovery_codes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  VARCHAR(64) NOT NULL,
    used_at    TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_user_recovery_codes_user ON user_recovery_codes(user_id);
-- A hash is unique per user so one code can never be consumed twice by racing
-- requests selecting different rows.
CREATE UNIQUE INDEX IF NOT EXISTS uq_user_recovery_codes_hash ON user_recovery_codes(user_id, code_hash);

ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS require_mfa BOOLEAN NOT NULL DEFAULT false;

INSERT INTO system_settings (key, value, category, description, managed_by)
VALUES ('mfa_required_roles', '', 'security',
        'Comma separated roles that must enrol in MFA (empty = nobody is forced)', 'seed')
ON CONFLICT (key) DO NOTHING;
