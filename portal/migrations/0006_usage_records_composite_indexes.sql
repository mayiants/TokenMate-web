-- S-U14: Composite indexes for high-frequency query dimensions on usage_records.
-- These cover the most common filtered queries that currently degrade to
-- partition-wide sequential scans:
--   (user_id, created_at)     — user billing pages, admin user drill-down
--   (user_api_key_id, created_at) — key usage pages
--   (group_id, created_at)    — enterprise group usage, admin group drill-down
--   (model, created_at)       — model-level analytics
--
-- These run inside a migration transaction so CONCURRENTLY is not used here.
-- For zero-downtime production deployments, run these manually with CONCURRENTLY
-- or use schema.go which runs at startup before traffic begins.

CREATE INDEX IF NOT EXISTS idx_usage_records_user_id_created_at ON usage_records(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_records_api_key_id_created_at ON usage_records(user_api_key_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_records_group_id_created_at ON usage_records(group_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_records_model_created_at ON usage_records(model, created_at);
