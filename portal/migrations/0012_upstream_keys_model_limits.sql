-- Add per-(key, model) rate limits to upstream_keys.
--
-- Some upstream providers (e.g. TokenRouter) enforce RPM/TPM per (key, model)
-- rather than per key. The key-level rpm_limit/tpm_limit columns cannot
-- express that, so proxies need a model-keyed limit map:
--   {"*": {"rpm": 14}}                 -> default limit for every model
--   {"gpt-4o": {"rpm": 10, "tpm": 100000}} -> per-model override
-- Proxies fall back to key-level rpm_limit/tpm_limit when no entry matches.

ALTER TABLE upstream_keys ADD COLUMN IF NOT EXISTS model_limits JSONB;
