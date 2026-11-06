-- Management tokens: long-lived, revocable, scoped credentials for programmatic
-- account management (distinct from sk- inference keys and JWT sessions).
-- P1: enterprise-scoped, scopes read:usage / read:pricing. user_id retained so
-- the model can later be extended to personal (non-enterprise) tokens.

CREATE TABLE IF NOT EXISTS management_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token_hash VARCHAR(255) NOT NULL UNIQUE,
    token_prefix VARCHAR(20) NOT NULL,
    name VARCHAR(255) NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id),
    enterprise_id UUID REFERENCES enterprises(id),
    scopes JSONB NOT NULL DEFAULT '[]',
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'revoked')),
    last_used_at TIMESTAMP,
    expires_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW(),
    deleted_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_management_tokens_hash
    ON management_tokens(token_hash) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_management_tokens_enterprise
    ON management_tokens(enterprise_id, status) WHERE deleted_at IS NULL;
