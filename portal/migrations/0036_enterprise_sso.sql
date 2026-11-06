-- 0036: per-enterprise OpenID Connect single sign-on (authorization code + PKCE).
--
-- enterprise_sso_configs   one row per enterprise (PK = enterprise_id).
--   enabled                SSO login is offered/accepted only when true.
--   issuer                 OIDC issuer URL; discovery is fetched from
--                          <issuer>/.well-known/openid-configuration.
--   client_secret_enc      AES-GCM ciphertext (pkg/crypto, ENCRYPTION_KEY). The
--                          plaintext is never returned by any API.
--   allowed_email_domains  lower-case domains whose users may sign in through this
--                          IdP; also used for "Sign in with SSO" discovery.
--   default_role           role given to just-in-time provisioned users. Only the
--                          non-admin member role is accepted by the API.
--   jit_provisioning       create unknown users on first SSO login.
--   enforce_sso            non-admin members must use SSO; enterprise_admin and
--                          super_admin keep password login as break-glass.
-- user_identities          (issuer, subject) -> local user. The pair, not the
--                          e-mail address, identifies a person on later logins.
--
-- Safe to re-run: every statement is idempotent. internal/db/schema.go mirrors
-- these objects for fresh databases and the schema-vs-code checker. Nothing is
-- enabled by default, so deploying this migration changes no login behaviour.

CREATE TABLE IF NOT EXISTS enterprise_sso_configs (
    enterprise_id         UUID PRIMARY KEY REFERENCES enterprises(id) ON DELETE CASCADE,
    enabled               BOOLEAN NOT NULL DEFAULT false,
    issuer                TEXT,
    client_id             TEXT,
    client_secret_enc     TEXT,
    scopes                TEXT NOT NULL DEFAULT 'openid email profile',
    allowed_email_domains TEXT[] NOT NULL DEFAULT '{}',
    default_role          VARCHAR(32) NOT NULL DEFAULT 'member',
    jit_provisioning      BOOLEAN NOT NULL DEFAULT true,
    enforce_sso           BOOLEAN NOT NULL DEFAULT false,
    created_at            TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_by            UUID REFERENCES users(id) ON DELETE SET NULL
);

CREATE TABLE IF NOT EXISTS user_identities (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    enterprise_id UUID NOT NULL REFERENCES enterprises(id) ON DELETE CASCADE,
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    email         VARCHAR(255),
    created_at    TIMESTAMP NOT NULL DEFAULT NOW(),
    last_login_at TIMESTAMP,
    UNIQUE (issuer, subject)
);

CREATE INDEX IF NOT EXISTS idx_user_identities_user ON user_identities(user_id);
CREATE INDEX IF NOT EXISTS idx_user_identities_enterprise ON user_identities(enterprise_id);
