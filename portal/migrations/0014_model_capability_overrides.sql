-- Model capability override master data.
--
-- One row per model name (api_model_name, the name clients send). Resolution
-- precedence when serving model metadata:
--   1. this table, source='manual'  (admin-edited, never auto-overwritten)
--   2. this table, source='probe'   (written by capability probes, phase 2)
--   3. this table, source='upstream' (derived from upstream provider data)
--   4. service_models.capabilities->'supported_types' (legacy fallback)
--   5. default ['text']
--
-- An empty supported_types array means "fall back to the next source" and is
-- equivalent to deleting the row; the API deletes instead of storing empty.

CREATE TABLE IF NOT EXISTS model_capability_overrides (
    model_name      TEXT PRIMARY KEY,
    supported_types JSONB NOT NULL DEFAULT '[]'::jsonb,
    source          TEXT NOT NULL DEFAULT 'upstream'
                    CHECK (source IN ('manual', 'probe', 'upstream')),
    note            TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_model_capability_overrides_source
    ON model_capability_overrides (source);
