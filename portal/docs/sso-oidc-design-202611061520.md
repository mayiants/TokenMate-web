> **ACTIVE** - per-enterprise OpenID Connect SSO (authorization code + PKCE). SAML is out of scope (follow-up).

# Enterprise OIDC SSO

## Flow

1. `POST /api/auth/sso/discover {email}` -> `{"sso": bool, "start_url": string}` (same shape for every input).
2. `GET /auth/sso/start?enterprise=<id>`: discovery (SSRF-guarded), state + nonce + PKCE verifier stored in Redis
   (10 min, single use), browser bound by a signed `HttpOnly; SameSite=Lax` cookie (`sso_login`), redirect to the IdP.
   The redirect URI is `APP_BASE_URL + /auth/sso/callback`, never derived from request headers.
3. `GET /auth/sso/callback`: consume state (GETDEL) + cookie check, code exchange (client secret, `code_verifier`),
   ID-token verification (RS256/ES256 only; iss, aud, azp, exp/nbf/iat +-2 min, nonce, verified e-mail), account mapping,
   session through `session.Issuer.Issue(..., MethodSSO, ...)` (skips local TOTP; the IdP authenticated the user).

## Account mapping (internal/routes/sso_login.go)

1. `(issuer, subject)` identity of this enterprise.
2. Existing user with the same verified e-mail who is already an active member of *this* enterprise -> identity linked.
   Users of other enterprises, standalone users, `system_admin` and `super_admin` are never taken over.
3. Just-in-time provisioning (if enabled): plain member role only, e-mail verified, random unusable password, seat limit
   (members + open invitations, `FOR UPDATE` on the enterprise row) and the platform-approved `enterprises.allowed_domains`
   must both allow it.

The e-mail domain must always be in `allowed_email_domains`. Suspended and erasure-pending users are rejected.

## enforce_sso

Password login (API and web) is refused for plain members of an enterprise with `enabled AND enforce_sso`
(`ErrSSORequired`, checked in `AuthenticateUser` and again in `FinishPasswordLogin`). `enterprise_admin` (and any admin
membership), `super_admin`, `system_admin` and users outside the enterprise keep password login (break-glass).
Password reset only changes the hash, so it cannot bypass this.

## Management API (enterprise_admin of the enterprise, or super_admin with `enterprise_id`)

`GET/PUT/DELETE /api/enterprise/sso`, `POST /api/enterprise/sso/test` (discovery + JWKS only, stable error codes).
The client secret is never returned (`client_secret_set`), is stored with `pkg/crypto` + `ENCRYPTION_KEY` and never
written to audit logs. An enabled domain can belong to only one enterprise; public mail providers are rejected.

## Audit events

`sso_login`, `sso_failed`, `sso_user_provisioned`, `sso_identity_linked` (category `auth`);
`sso_config_updated`, `sso_config_deleted`, `sso_config_tested` (category `security`).

## Operations

* Migration `0036` (also in `schema.go`); nothing is enabled by default.
* `APP_BASE_URL` must be the public https URL; register `<APP_BASE_URL>/auth/sso/callback` at the IdP.
* `SSO_ALLOW_INSECURE_HTTP=true` only relaxes the scheme check for development; address filtering always applies.
* Redis is required (state store); SSO fails closed without it.

## Follow-ups

* SAML 2.0.
* DNS-TXT proof of domain ownership (today: first enabled claim wins + free-mail blocklist).
* Enterprises with `require_mfa` still require local TOTP enrolment for SSO users (the JWT has no auth-method claim);
  trusting the IdP's MFA (`amr`) is not implemented.
* SCIM / group-to-role mapping, back-channel logout.
