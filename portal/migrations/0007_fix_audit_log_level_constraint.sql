-- Fix audit_logs level constraint: the application uses 'warning' everywhere,
-- but the schema check constraint only allowed 'warn'. This caused every audit
-- log written with level='warning' to fail batch flush.

-- Backfill any existing rows that somehow used the old 'warn' value.
UPDATE audit_logs SET level = 'warning' WHERE level = 'warn';

-- Drop the old check constraint. Postgres auto-names inline column CHECK
-- constraints as <table>_<column>_check; use IF EXISTS to be idempotent.
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS audit_logs_level_check;

-- Add the corrected constraint that matches application usage.
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_level_check
    CHECK (level IN ('debug', 'info', 'warning', 'error'));
