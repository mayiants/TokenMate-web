-- Agent enrollment for the enterprise AI access platform (Phase 1).
-- One-time, short-lived, device-bound sessions that let the PC agent exchange
-- an OTP-verified identity for an enterprise sub-key.
-- See ai-gateway-go/WOODGIT企业级AI接入平台-首阶段实施方案-v1.0.md

CREATE TABLE IF NOT EXISTS agent_enroll_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash TEXT NOT NULL UNIQUE,          -- sha256 of the one-time token; plaintext never stored
    user_id UUID NOT NULL REFERENCES users(id),
    enterprise_id UUID REFERENCES enterprises(id),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agent_enroll_sessions_user ON agent_enroll_sessions(user_id);

-- Devices enrolled via the PC agent. One row per (user, device fingerprint);
-- each device holds its own sub-key so it can be revoked independently.
CREATE TABLE IF NOT EXISTS agent_devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    enterprise_id UUID REFERENCES enterprises(id),
    api_key_id UUID REFERENCES api_keys(id),
    fingerprint_hash TEXT NOT NULL,           -- sha256 of the agent-generated device fingerprint
    hostname VARCHAR(64) NOT NULL DEFAULT '',
    os_version VARCHAR(64) NOT NULL DEFAULT '',
    agent_version VARCHAR(32) NOT NULL DEFAULT '',
    client_version VARCHAR(64) NOT NULL DEFAULT '',  -- detected workbuddy version, refreshed on heartbeat
    last_seen_at TIMESTAMPTZ,
    status VARCHAR(20) NOT NULL DEFAULT 'active',    -- active | revoked
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, fingerprint_hash)
);
CREATE INDEX IF NOT EXISTS idx_agent_devices_user ON agent_devices(user_id);
CREATE INDEX IF NOT EXISTS idx_agent_devices_enterprise ON agent_devices(enterprise_id);
CREATE INDEX IF NOT EXISTS idx_agent_devices_key ON agent_devices(api_key_id);
