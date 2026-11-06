-- Add called_tools to usage_records for response-side tool-call tracking.
-- The proxy reports the tools the upstream response actually invoked as
-- "name:count" pairs (count desc, name asc); the UI renders them with the
-- request-side agent_tools column. Empty/NULL for plain chat responses.
ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS called_tools TEXT;
