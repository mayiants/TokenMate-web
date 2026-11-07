-- Browser-based device authorization (device authorization flow): the agent
-- creates a session, the user approves it in a logged-in browser, the agent
-- polls and exchanges the issued one-time session for a device-bound key.
-- The enroll session itself is reused from agent_enroll_sessions; this table
-- only bridges browser and agent.
CREATE TABLE IF NOT EXISTS agent_device_auth (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code_hash text NOT NULL UNIQUE,
    status varchar(20) NOT NULL DEFAULT 'pending', -- pending|approved|rejected|expired
    user_id uuid,
    device_name varchar(128) NOT NULL DEFAULT '',
    session_token_enc text NOT NULL DEFAULT '',    -- AES-GCM encrypted one-time session token
    created_at timestamptz NOT NULL DEFAULT NOW(),
    expires_at timestamptz NOT NULL,
    decided_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_agent_device_auth_status ON agent_device_auth (status, expires_at);
