-- Add user_agent to usage_records for client-app identification.
-- The proxy reports the raw client User-Agent; the UI maps it to a friendly
-- app name (Claude Code / Cherry Studio / ...) or shows the raw UA fragment.
ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS user_agent TEXT;
