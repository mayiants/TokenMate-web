-- S-P3: Persist proxy audit-logger batches instead of discarding them.
--
-- POST /webhook/usage/batch (handleUsageBatch) previously acknowledged and
-- discarded the batch. This table stores those records so failed upstream
-- requests become quantifiable — usage_records only covers the billing path.
-- Plain inserts (no unique constraint): dedup is not required for an audit trail.

CREATE TABLE IF NOT EXISTS proxy_request_audits (
    id BIGSERIAL PRIMARY KEY,
    trace_id UUID,
    api_key_hash TEXT,
    model TEXT,
    provider TEXT,
    region TEXT,
    upstream_key_id TEXT,
    endpoint TEXT,
    status TEXT,
    error TEXT,
    error_code TEXT,
    input_tokens BIGINT DEFAULT 0,
    output_tokens BIGINT DEFAULT 0,
    cached_tokens BIGINT DEFAULT 0,
    latency_ms BIGINT DEFAULT 0,
    request_count INTEGER DEFAULT 1,
    is_streaming BOOLEAN DEFAULT false,
    client_ip TEXT,
    pricing_ratio NUMERIC(7,4),
    routing_track TEXT,
    server_id TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_proxy_request_audits_created_at ON proxy_request_audits(created_at);
CREATE INDEX IF NOT EXISTS idx_proxy_request_audits_model_created_at ON proxy_request_audits(model, created_at);
