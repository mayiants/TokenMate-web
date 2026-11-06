-- 0033: audit completeness + per-enterprise data-retention policy.
--
-- A. proxy_request_audits: the Proxy now also reports user agent, HTTP status
--    and failed requests. enterprise_id is resolved from api_keys at insert
--    time (nullable, no FK: audit rows must outlive enterprises/keys).
--    trace_id gets a NON-unique index: the table may already contain
--    duplicates, so idempotency is enforced by the insert (NOT EXISTS under an
--    advisory lock) rather than by a constraint that could fail this migration.
-- B. enterprise_audit_log: older writers filled change_type but left `action`
--    empty (222/286 rows in production); backfill so both columns agree.
-- C. enterprises: per-enterprise retention policy consumed by the Proxy via
--    /api/keys/validate, plus platform defaults for keys without an enterprise.
--
-- Safe to re-run: every statement is idempotent. internal/db/schema.go mirrors
-- these changes for fresh databases.

-- A. proxy_request_audits ----------------------------------------------------
ALTER TABLE proxy_request_audits ADD COLUMN IF NOT EXISTS user_agent TEXT;
ALTER TABLE proxy_request_audits ADD COLUMN IF NOT EXISTS http_status INTEGER;
ALTER TABLE proxy_request_audits ADD COLUMN IF NOT EXISTS enterprise_id UUID;

CREATE INDEX IF NOT EXISTS idx_proxy_request_audits_trace_id ON proxy_request_audits(trace_id);
CREATE INDEX IF NOT EXISTS idx_proxy_request_audits_created_at ON proxy_request_audits(created_at);
CREATE INDEX IF NOT EXISTS idx_proxy_request_audits_enterprise_created
    ON proxy_request_audits(enterprise_id, created_at) WHERE enterprise_id IS NOT NULL;

-- B. enterprise_audit_log: client IP / UA + action backfill --------------------
ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS ip_address INET;
ALTER TABLE enterprise_audit_log ADD COLUMN IF NOT EXISTS user_agent TEXT;

-- `action` is a hashed column once the tamper-evidence chain (0034) exists, and
-- chained rows are immutable by design. In the normal upgrade order 0033 runs
-- BEFORE 0034, so every row is still unchained and gets normalised here (and is
-- then hashed with its final value). If 0034 was somehow applied first, only the
-- still-unchained rows (row_hash IS NULL) may be rewritten; chained rows keep
-- their original, hash-covered value.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
                WHERE table_schema = current_schema()
                  AND table_name = 'enterprise_audit_log' AND column_name = 'row_hash') THEN
        PERFORM set_config('app.audit_chain_backfill', 'on', true);
        UPDATE enterprise_audit_log
           SET action = change_type
         WHERE (action IS NULL OR action = '')
           AND change_type IS NOT NULL AND change_type <> ''
           AND row_hash IS NULL;
        PERFORM set_config('app.audit_chain_backfill', 'off', true);
    ELSE
        UPDATE enterprise_audit_log
           SET action = change_type
         WHERE (action IS NULL OR action = '')
           AND change_type IS NOT NULL AND change_type <> '';
    END IF;
END $$;

-- C. enterprises retention policy --------------------------------------------
ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_mode VARCHAR(16) NOT NULL DEFAULT 'standard';
ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_days INTEGER NOT NULL DEFAULT 90;
ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_updated_at TIMESTAMPTZ;
ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_updated_by UUID;
ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_last_purge_at TIMESTAMPTZ;
ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_last_purge_by UUID;
ALTER TABLE enterprises ADD COLUMN IF NOT EXISTS retention_last_purge_status VARCHAR(16);

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_enterprises_retention_mode') THEN
        ALTER TABLE enterprises ADD CONSTRAINT chk_enterprises_retention_mode
            CHECK (retention_mode IN ('off', 'metadata', 'standard'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_enterprises_retention_days') THEN
        ALTER TABLE enterprises ADD CONSTRAINT chk_enterprises_retention_days
            CHECK (retention_days BETWEEN 1 AND 365);
    END IF;
END $$;

-- Platform defaults for keys that belong to no enterprise. Admin-edited rows
-- are never overwritten (DO NOTHING).
INSERT INTO system_settings (key, value, category, description, managed_by)
VALUES
    ('retention_default_mode', 'standard', 'compliance', 'Default payload retention mode for keys without an enterprise (off | metadata | standard)', 'seed'),
    ('retention_default_days', '30', 'compliance', 'Default payload retention days for keys without an enterprise (1-365)', 'seed')
ON CONFLICT (key) DO NOTHING;
