-- Add agent_tools to usage_records for agent-tool identification.
-- The proxy reports the raw tool names offered in the request (deduped,
-- comma-separated); the UI renders an agent badge with the tool list, or '—'
-- for plain chat requests without tools.
ALTER TABLE usage_records ADD COLUMN IF NOT EXISTS agent_tools TEXT;
