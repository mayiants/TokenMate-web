# Platform Security Enhancements — Design

Status: DRAFT
Date: 2026-11-04
Scope: ai-portal-go (control plane) + ai-proxy-go (data plane)

Three independent security features, inspired by TokenRouter's recent releases,
designed to fit the existing WOODGIT architecture:

1. **API Key IP Whitelist** — restrict each API key to allowed source IPs/CIDRs.
2. **WIF Trust (Workload Identity Federation)** — obtain a short-lived API key by
   proving an external identity (OIDC) instead of holding a long-lived key.
3. **Login step-up verification** — email code (and/or interactive challenge) on
   risky logins (new device / long inactivity / risk signal).

Each can ship on its own; they are ordered by effort (1 < 3 < 2).

---

## Current architecture (facts this design builds on)

- **Key validation**: proxy extracts the Bearer key (`ai-proxy-go` auth
  `extractAPIKey`, `Authorization: Bearer` / `X-API-Key`) and calls the portal
  `POST /api/keys/validate`, which returns a struct (group/limits/models/…).
  The proxy **caches** the validation result for `defaultCacheTTL = 5m`
  (`gracefulTTL = 30m` when the portal is down).
- **Client IP** is already available in the proxy request path via
  `c.ClientIP()` (`handlers_common.go`), and is recorded on usage records.
- **Login**: `AuthRoutes.login` (`internal/routes/auth.go`) already sends email
  codes via `r.mail.SendVerificationCode(...)`, backed by the
  `verification_codes` table (has `type`, `value`, `expires_at`) and the
  `pkg/mail` package.
- **Devices**: `user_devices` (with a `(user_id, fingerprint)` unique index) +
  `internal/routes/devices.go` already record/list devices, but **login is not
  gated** on device trust today.
- **Service credentials precedent**: `management_tokens` (prefix `mgmt-`) already
  mints non-interactive tokens — the closest existing pattern to WIF output.

---

## 1. API Key IP Whitelist

### Goal
Let a user restrict an API key to a set of source IPs / CIDR ranges. When set,
the key is rejected from any other source.

### Data model (portal)
Add to `api_keys` (via `schema.go` + a migration):
```sql
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS ip_whitelist JSONB; -- array of CIDR/IP strings, NULL/[] = no restriction
```
Store normalized CIDRs (a bare IP becomes `/32` or `/128`). Validate on write
(reject malformed entries; cap the list length, e.g. ≤ 50).

### Enforcement (where)
Two layers, defense in depth:

- **Primary — proxy, per request (authoritative, not cacheable per-IP).**
  The portal `validate` response gains `ip_whitelist []string`. The proxy, which
  already has `c.ClientIP()`, checks membership AFTER a successful validate and
  BEFORE forwarding. On miss → `403 ip_not_allowed`. Because the 5-min validate
  cache is keyed by key-hash (not IP), the IP check must run **on every request
  against the cached whitelist**, not be baked into the cache decision.
- **Secondary — portal validate.** The portal cannot see the true client IP
  (the proxy is the client), so the portal does NOT enforce IP; it only returns
  the list. (If a direct-to-portal path ever exists, enforce there too using
  `X-Forwarded-For` with a trusted-proxy allowlist.)

### Proxy matching
Parse each entry with `net.ParseCIDR` (fallback `net.ParseIP` → /32,/128). Match
`c.ClientIP()` against the set. Treat an unparseable stored entry as non-matching
and log a warning (fail closed for that entry, not the whole key).

### UI (portal)
- API key create/edit drawer: a textarea "Allowed source IPs (one per line,
  CIDR supported)". Empty = unrestricted (show a hint).
- Surface the current caller IP on the page to help users whitelist themselves.

### Edge cases / risks
- **Lockout**: a user can whitelist a wrong IP and lock the key out. Mitigate by
  showing their current egress IP and a confirmation when the list is non-empty.
- **IPv6 + proxy chains**: honor the proxy's existing trusted `ClientIP()`
  resolution; document that corporate NAT/egress IPs are what matters.
- **Caching**: whitelist changes take up to 5 min to propagate (validate cache).
  Acceptable; note it in the UI. For immediate effect, reuse the existing
  `InvalidateKeyHash` cache-busting already used on key updates.

### Testing
- Unit: CIDR parse/normalize + match (v4/v6, bare IP, malformed).
- Proxy: request from allowed vs disallowed IP → 200 vs 403.
- Portal: validate returns the list; key update invalidates cache.

### Effort: **S** (1 column + 1 proxy check + UI). No new services.

---

## 3. Login Step-Up Verification (risk-based)

### Goal
On a **risky** login, after a correct password, require a second factor before
issuing the session: an emailed 6-digit code (reusing existing infra) and/or an
interactive human challenge (slider/jigsaw CAPTCHA, like TR).

### Risk signals (any → step up)
- **New/untrusted device**: the login's device fingerprint is not in
  `user_devices` for this user (fingerprint already computed in `devices.go`).
- **Long inactivity**: `user_devices.last_active_at` older than N days (e.g. 30),
  or no trusted device at all.
- **Risk heuristics**: new country/ASN vs recent logins (from usage/audit IP
  history), many recent `login_failed` audit rows, or an admin "force step-up"
  flag.

### Flow (fits current `AuthRoutes.login`)
1. Verify email+password as today.
2. Compute device fingerprint (reuse `fingerprintHash`) + evaluate risk signals.
3. **Low risk** (known trusted device, recently active): issue session as today.
4. **High risk**: do NOT issue the session yet. Create a `verification_codes`
   row (`type='login_step_up'`, value=code, short TTL ~10 min), email it via
   `r.mail.SendVerificationCode`, and return a `step_up_required` response with a
   short-lived challenge token (signed, carries user_id + fingerprint + expiry).
5. Client submits code (+ challenge token) to a new `POST /api/auth/verify-step-up`.
   On success: mark the device trusted (`user_devices` upsert, `last_active_at=now`),
   issue the session, audit `login_step_up_passed`.

### Interactive challenge (slider/jigsaw) — optional, layered
- Purpose: bot/credential-stuffing mitigation on the password step itself,
  independent of the email step-up.
- Recommended: integrate a proven provider (e.g. hCaptcha/Cloudflare Turnstile,
  or a self-hosted slider-CAPTCHA) rather than building crypto-grade puzzle
  verification from scratch. Server verifies the challenge token server-side
  before counting a password attempt. Gate it behind a config flag so it can be
  enabled per-environment.
- If a bespoke slider is required, keep the verdict **server-side** (client sends
  trajectory + a signed nonce; server scores + rate-limits), and treat it as a
  friction/bot filter, not a strong auth factor — the email code remains the
  real second factor.

### Data / config
- Reuse `verification_codes` (`type='login_step_up'`). No new table required for
  the email path.
- `user_devices`: add `trusted_at TIMESTAMPTZ` (when the device cleared step-up).
- Config flags: `AUTH_STEP_UP_ENABLED`, `AUTH_STEP_UP_INACTIVITY_DAYS`,
  `AUTH_CAPTCHA_PROVIDER` / keys.

### Risks
- **Email deliverability** is the single point of failure for account access —
  provide admin "reset trusted devices" and keep recovery codes in scope later.
- **Don't lock out** when mail is unconfigured: if `mail` is not configured, log
  and fall back to password-only (same posture as other mail-optional flows).

### Testing
- Unit: risk evaluation (new device, stale device, failed-login burst).
- Flow: known device → no step-up; new device → step-up → verify → trusted.
- Negative: wrong/expired code; challenge token reuse/expiry.

### Effort: **M** (email step-up is M-small reusing infra; interactive CAPTCHA is
a separate M if a provider is used, L if bespoke).

---

## 2. WIF Trust — Workload Identity Federation

### Goal
Let a customer workload obtain a **short-lived API key** by proving an external
identity (OIDC token from e.g. GitHub Actions, GCP, AWS, an enterprise IdP)
instead of storing a long-lived key — a token-exchange / "trust" model.

### Concept
Customer registers a **trust** (issuer + audience + subject/claim constraints).
At runtime their workload presents an OIDC JWT; the portal verifies it against
the trust's JWKS and, if the claims match, **mints a short-lived API key**
(or a short-lived bearer the proxy accepts) scoped to a chosen group/models.

### Data model (portal)
```sql
CREATE TABLE wif_trusts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id),
  enterprise_id UUID REFERENCES enterprises(id),       -- optional, for org-scoped trusts
  name VARCHAR(255) NOT NULL,
  issuer TEXT NOT NULL,                                 -- e.g. https://token.actions.githubusercontent.com
  audience TEXT NOT NULL,                               -- expected aud
  subject_match TEXT,                                   -- exact/glob on sub (e.g. repo:org/*:ref:refs/heads/main)
  claim_constraints JSONB,                              -- additional required claim=value pairs
  jwks_uri TEXT,                                        -- discovered from issuer if null (/.well-known)
  group_id UUID REFERENCES user_groups(id),             -- scope minted key to this group
  allowed_models JSONB,
  key_ttl_seconds INT DEFAULT 900,                      -- minted key lifetime
  status VARCHAR(20) DEFAULT 'active',
  created_at TIMESTAMPTZ DEFAULT NOW()
);
```
Minted credentials reuse the existing `api_keys` table with a short
`expires_at` and a marker (`origin='wif'`, `wif_trust_id`), so the whole existing
validate/billing/rate-limit path works unchanged. (This mirrors how
`management_tokens` already mints non-interactive credentials.)

### Token exchange endpoint (portal)
`POST /api/wif/token` (public, no session) — RFC 8693-style exchange:
1. Read the OIDC JWT from the request (`subject_token`).
2. Parse issuer+kid, fetch/caches the issuer JWKS (`jwks_uri` or OIDC discovery),
   verify signature, `exp/nbf/iat`, `aud`, `iss`.
3. Find a matching `wif_trusts` row (issuer+audience) and check
   `subject_match` + `claim_constraints` against the JWT claims.
4. On match: mint an `api_keys` row (random key, hash stored, `expires_at = now +
   key_ttl_seconds`, group/models from the trust, `origin='wif'`), audit it, and
   return `{ api_key, expires_at }`.
5. Rate-limit per trust; cache JWKS; reject unknown issuers.

### Enforcement (proxy)
No proxy change needed for the happy path: the minted key is a normal API key
with an `expires_at`, which the existing validate path already honors (key
expiry is already supported). A background sweep (or validate-time check) drops
expired WIF keys.

### Why mint a key (vs accept the OIDC JWT directly at the proxy)
Minting keeps the proxy's hot path unchanged (no per-request JWKS verification,
no issuer trust config in the data plane) and reuses all existing
group/model/rate-limit/billing wiring. The verification cost is paid once per
token exchange, not per request.

### Security considerations
- **Pin issuer + audience + subject**; never trust `sub` alone. Require at least
  one of subject_match / claim_constraints to be non-empty (no "any identity
  from this issuer" trusts).
- **JWKS caching with rotation**: cache by `kid`, refresh on unknown kid, cap
  refresh rate.
- **Short TTLs** (default 15 min); the minted key is the blast radius.
- **Audit** every exchange (issuer, sub, trust id, minted key id) and every
  rejection.
- **Enterprise scoping**: org-level trusts gated by enterprise admin.

### UI
Admin/user "WIF Trusts" page: create trust (issuer/audience/subject/claims →
group/models/TTL), list, disable. Show a copy-paste exchange snippet.

### Testing
- Unit: JWT verify (good/bad sig, expired, wrong aud/iss), subject/claim match.
- Flow: valid OIDC → minted key works at proxy → expires after TTL.
- Negative: unknown issuer, subject mismatch, replay after expiry.

### Effort: **L** (new table + OIDC/JWKS verification + token-exchange endpoint +
UI). Highest effort; do last.

---

## Rollout order & flags
1. **IP whitelist** (S) — isolated, high value, 1 column + 1 proxy check.
2. **Login step-up, email path** (M-small) — reuses verification_codes + mail +
   device fingerprint; behind `AUTH_STEP_UP_ENABLED`.
3. **Interactive CAPTCHA** (M) — provider-based, behind a config flag.
4. **WIF Trust** (L) — new subsystem; ship behind a feature flag and start with
   a single issuer (e.g. GitHub Actions) to validate the exchange flow.

All four are config-gated and independently revertible. None change the proxy
hot path except the IP check (a cheap in-memory membership test) and honoring
existing key expiry for WIF.
