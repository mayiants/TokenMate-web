-- One-time migration: copy flat prices from service_models into model_pricing_rules.
--
-- Mapping:
--   service_models.input_price_cache_miss -> prices.input
--   service_models.input_price            -> fallback for input
--   service_models.output_price           -> prices.output
--   service_models.input_price_cache_hit  -> prices.cache_hit
--   service_models.reasoning_price        -> prices.reasoning
--   service_models.fixed_price            -> prices.fixed_request
--   cache_write is left as 0 because legacy service_models did not track it.
--
-- The script is idempotent: it skips rows for which a flat rule already exists
-- for the same provider/region/api_model_name.
--
-- Run with: psql $DATABASE_URL -f scripts/migrate_service_models_to_pricing_rules.sql

INSERT INTO model_pricing_rules (
    provider,
    region,
    product_id,
    api_model_name,
    billing_mode,
    prices,
    timezone,
    currency,
    effective_from,
    priority,
    status,
    rule_name,
    note
)
SELECT
    sm.provider,
    '*' AS region,
    sm.product_id,
    COALESCE(sm.api_model_name, sm.product_id) AS api_model_name,
    'flat' AS billing_mode,
    jsonb_build_object(
        'input',        COALESCE(sm.input_price_cache_miss, sm.input_price, 0),
        'output',       COALESCE(sm.output_price, 0),
        'cache_hit',    COALESCE(sm.input_price_cache_hit, 0),
        'cache_write',  0,
        'reasoning',    COALESCE(sm.reasoning_price, 0),
        'fixed_request', COALESCE(sm.fixed_price, 0)
    ) AS prices,
    'UTC' AS timezone,
    COALESCE(sm.currency, 'USD') AS currency,
    NOW() AS effective_from,
    0 AS priority,
    'active' AS status,
    'Migrated from service_models: ' || sm.provider || '/' || COALESCE(sm.api_model_name, sm.product_id) AS rule_name,
    'Auto-generated flat pricing rule from legacy service_models prices.' AS note
FROM service_models sm
WHERE sm.deleted_at IS NULL
  AND sm.status = 'active'
  AND (
      COALESCE(sm.input_price_cache_miss, sm.input_price, 0) > 0
      OR COALESCE(sm.output_price, 0) > 0
      OR COALESCE(sm.input_price_cache_hit, 0) > 0
      OR COALESCE(sm.reasoning_price, 0) > 0
      OR COALESCE(sm.fixed_price, 0) > 0
  )
  AND NOT EXISTS (
      SELECT 1
      FROM model_pricing_rules r
      WHERE r.provider = sm.provider
        AND r.region = '*'
        AND r.product_id = sm.product_id
        AND r.api_model_name = COALESCE(sm.api_model_name, sm.product_id)
        AND r.billing_mode = 'flat'
  );
