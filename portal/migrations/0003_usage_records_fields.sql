-- Usage logs enrichment fields: endpoint, streaming flag, client IP, status.
ALTER TABLE usage_records
  ADD COLUMN IF NOT EXISTS endpoint VARCHAR(128),
  ADD COLUMN IF NOT EXISTS is_streaming BOOLEAN DEFAULT false,
  ADD COLUMN IF NOT EXISTS client_ip VARCHAR(64),
  ADD COLUMN IF NOT EXISTS status VARCHAR(32) DEFAULT 'success';

CREATE INDEX IF NOT EXISTS idx_usage_records_endpoint ON usage_records(endpoint);
CREATE INDEX IF NOT EXISTS idx_usage_records_is_streaming ON usage_records(is_streaming);
CREATE INDEX IF NOT EXISTS idx_usage_records_status ON usage_records(status);
