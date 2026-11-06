-- S-P2: Lookup indexes missed by the production review (2026-09-02).
--
-- 1. (upstream_key_id, created_at) on usage_records: upstream key heat metrics
--    (internal/query ListUpstreamKeyMetrics and related LATERAL joins) filter
--    usage_records per upstream key; without this index each key degrades to a
--    partition-wide sequential scan as the table grows.
-- 2. Single-column indexes on service_models(product_id) and
--    service_models(api_model_name): per-request model resolution joins with
--    `product_id = $1 OR api_model_name = $1`, which the UNIQUE(provider,
--    product_id) constraint cannot serve. Postgres can now bitmap-or the two
--    indexes instead of scanning the table.
--
-- These run inside a migration transaction so CONCURRENTLY is not used here.
-- For zero-downtime production deployments, run the same statements manually
-- with CONCURRENTLY during a low-traffic window.

CREATE INDEX IF NOT EXISTS idx_usage_records_upstream_key_id_created_at ON usage_records(upstream_key_id, created_at);
CREATE INDEX IF NOT EXISTS idx_service_models_product_id ON service_models(product_id);
CREATE INDEX IF NOT EXISTS idx_service_models_api_model_name ON service_models(api_model_name);
