-- Client adapter manifests distributed to PC agents.
-- Each row describes how the agent injects gateway credentials into one
-- client on one OS (config path, env var names, version constraints).
-- Content is not secret; agents fetch it at enrollment and on heartbeat.

CREATE TABLE IF NOT EXISTS agent_adapters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client VARCHAR(40) NOT NULL,              -- e.g. workbuddy
    os VARCHAR(20) NOT NULL DEFAULT 'windows',
    config_path TEXT NOT NULL DEFAULT '',     -- primary config file to inject into
    inject_method VARCHAR(10) NOT NULL DEFAULT 'file',  -- file | env
    env_base_url_var VARCHAR(64) NOT NULL DEFAULT '',
    env_api_key_var VARCHAR(64) NOT NULL DEFAULT '',
    manifest JSONB NOT NULL DEFAULT '{}'::jsonb,        -- adapter-specific details (backup names, JSON pointers, etc.)
    manifest_version INT NOT NULL DEFAULT 1,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (client, os)
);

-- Seed placeholder for workbuddy. config_path / env vars stay empty until the
-- P0 verification on a real Windows machine pins them down; the manifest
-- carries an explicit status so agents surface "pending verification"
-- instead of guessing.
INSERT INTO agent_adapters (client, os, manifest)
VALUES ('workbuddy', 'windows', '{"status":"pending_p0_verification"}'::jsonb)
ON CONFLICT (client, os) DO NOTHING;
