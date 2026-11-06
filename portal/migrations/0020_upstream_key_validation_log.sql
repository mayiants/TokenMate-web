-- Persist per-key upstream validation outcomes (background cron, batch, manual).
-- The upstream_keys row only keeps the *current* status, so transient probe
-- failures and recovery patterns were invisible after the fact — rebuildable
-- only from container logs, which are lost on recreate. This log makes the
-- 2026-10-27 class of incident ("which keys failed, when, and did they
-- recover") answerable from SQL.

CREATE TABLE IF NOT EXISTS upstream_key_validation_log (
    id BIGSERIAL PRIMARY KEY,
    upstream_key_id UUID,
    provider VARCHAR(50),
    key_preview VARCHAR(20),
    trigger_type VARCHAR(20) NOT NULL DEFAULT 'background',
    result VARCHAR(30) NOT NULL,
    error_message TEXT,
    models_fetched INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ukvl_created_at ON upstream_key_validation_log (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ukvl_key ON upstream_key_validation_log (upstream_key_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ukvl_provider_time ON upstream_key_validation_log (provider, created_at DESC);
