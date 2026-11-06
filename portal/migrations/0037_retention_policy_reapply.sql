-- 0037: re-apply the retention-policy part of 0033.
--
-- 0033 was split across two commits during the 2026-11-06 hardening work, and
-- production applied the first (audit-only) version, so section C never ran
-- there. The enterprise columns/constraints are also created by
-- internal/db/schema.go at startup; this migration makes the constraints and
-- the seeded system_settings defaults explicit in migration history.
--
-- Fully idempotent: a no-op on databases that ran the complete 0033.

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
