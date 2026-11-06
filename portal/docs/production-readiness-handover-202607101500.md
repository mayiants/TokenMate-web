# Production Readiness Handover

> Date: 2026-07-10 15:00 UTC
> Scope: `ai-portal-go` / `ai-proxy-go` production (`./start.sh mvp`) readiness
> Status: P0/P1 from the MVP readiness review are fixed in code; P2 hardening is partially landed and documented below.

## 1. Fixed in this pass

| Area | Fix | Location |
|---|---|---|
| P0 default admin password | `mvp|prod|production` now refuses empty/short/known-default `ADMIN_PASSWORD`; direct Portal boot has the same blocklist as defense in depth | `ai-portal-go/start.sh`, `ai-portal-go/cmd/server/bootstrap.go` |
| P1 email HTML injection | Enterprise status email escapes `enterpriseName`/`reason` and strips CRLF from subject | `ai-portal-go/pkg/mail/mail.go` |
| P1 duplicate email race | `INSERT users` maps PostgreSQL `23505 unique_violation` to `409 Email already registered` | `ai-portal-go/internal/routes/auth.go` |
| P1 referral over-redemption | Voucher row is locked with `SELECT ... FOR UPDATE` and revalidated inside the transaction before `used_count` increments | `ai-portal-go/internal/services/reward.go` |
| P2 JWT | Default `JWT_EXPIRES` is now 7 days; tokens carry `iss=ai-portal` / `aud=ai-portal`; verification enforces them when present and still accepts legacy tokens | `ai-portal-go/internal/config/config.go`, `ai-portal-go/pkg/jwt/jwt.go` |

### 1.1 Super admin password: where to set it

The first super admin is bootstrapped from environment variables in
`cmd/server/bootstrap.go` (`ADMIN_EMAIL`, `ADMIN_PASSWORD`, `ADMIN_NAME`; role is
hard-coded to `super_admin`). It is created only when no active user with that
email exists.

- Existing install: edit `ai-portal-go/.env.go-platform` and set a strong
  `ADMIN_PASSWORD` (≥12 chars, not `Admin@123456!`), then run `./start.sh mvp`
  or `./start.sh portal --app-base-url=https://api.woodgit.com`.
- First run before `.env.go-platform` exists: set `ADMIN_PASSWORD` in
  `ai-portal-go/.env` or export it in the shell; `start.sh` writes it into
  `.env.go-platform`. Once `.env.go-platform` exists, that file is authoritative.
- Changing `ADMIN_PASSWORD` later does **not** change an existing admin's
  password, because bootstrap skips creation when the admin already exists. Use
  the admin UI / another super admin to rotate passwords; emergency recovery is
  to soft-delete the admin row and restart to recreate it, or update the bcrypt
  `password_hash` directly (not recommended).

### 1.2 Soft-deleted email can be registered again

User deletion is soft delete (`UPDATE users SET deleted_at = NOW()`). The old
hard `users_email_key` constraint is dropped, and the current uniqueness rule is
partial: `idx_users_email_active ON users(email) WHERE deleted_at IS NULL`.
Registration also checks only active users and maps an active-row unique
violation (`23505`) to `409 Email already registered`. Therefore a previously
soft-deleted email can register again; only an email with an active user is
blocked, which is the intended behavior.

### 1.3 Production admin-count recommendation

Current defaults are `MAX_SUPER_ADMINS=3`, `MAX_SYSTEM_ADMINS=10`,
`MAX_ADMINS=5` (`internal/config/config.go`). For initial production, use a
smaller, named-admin ceiling:

```bash
# ai-portal-go/.env.go-platform
MAX_SUPER_ADMINS=2      # primary + break-glass/second approver; avoid 1
MAX_SYSTEM_ADMINS=3     # raise to 5 only for a real 24/7 rotation; avoid 10 initially
```

Rationale: in `ADMIN_MODE=production`, sensitive operations require
`super_admin` (`internal/middleware/auth.go: CanPerformSensitiveOps`), so a
single super admin is an operational risk. Two super admins cover break-glass
and the existing demote/delete flow that needs another super admin. Keep system
admins named and minimal; add capacity only as on-call coverage requires.

Note: these `MAX_*` values are currently exposed to the admin UI as limits, but
the promote/demote API paths do not hard-enforce them yet. Treat them as an
operational policy until server-side enforcement is added.

## 2. Documentation updated

- `ai-portal-go/migrations/README.md` now describes the real startup path: `InitSchema` baseline + `Migrate()`, current `0001`/`0002` files, naming rules, and rollback via snapshot/restore.
- `ai-proxy-go/README.md` and `ai-proxy-go/CLAUDE.md` now state that the Proxy runtime uses Redis + Portal HTTP and does not connect to PostgreSQL; `AI_PROXY_DATABASE_*` / `DB_*` are marked legacy/unused.
- `ai-portal-go/.env.template` now carries a `CHANGE_ME` admin-password placeholder (blocked by the production blocklist) and the recommended `MAX_SUPER_ADMINS=2` / `MAX_SYSTEM_ADMINS=3` / `MAX_ADMINS=5` values for new-server setup.

## 3. Remaining risks to schedule

- **Refresh token**: iss/aud and 7-day expiry are in, but `/api/auth/refresh` is still a follow-up if shorter access tokens are required.
- **Multi-instance rate limit**: Portal IP rate limiting is in-memory per instance; move to Redis before running multiple Portal instances.
- **Multi-proxy balance cache**: local Redis-backed balance cache has a documented overdraft limitation across multiple Proxy instances; monitor/reconcile before horizontal Proxy scaling.
- **Provider queue growth**: provider queues are lazily created and not TTL-evicted; watch long-running instances with many provider names.
- **Pagination UX**: `admin-reconciliation-ops` uses numeric pagination while other admin pages use Previous/info/Next. Cosmetic only, not a release blocker.

## 4. Pre-deploy env checklist

- `ADMIN_PASSWORD`: strong, ≥12 chars, not the default `Admin@123456!`.
- `MAX_SUPER_ADMINS=2`, `MAX_SYSTEM_ADMINS=3` for initial production (see §1.3).
- `JWT_SECRET`, `ENCRYPTION_KEY`, `PROXY_SYNC_SECRET`, `ADMIN_SECRET`: ≥32 chars, unique, not defaults.
- `GIN_MODE=release`, `ADMIN_MODE=production` (set by `start.sh` generated compose).
- `ALLOWED_ORIGINS`: set to the real domain(s); do not leave empty outside reverse-proxy/same-site setups.
- Aurora/external DB: `DB_SSLMODE=require` minimum, prefer `verify-full` with `DB_SSL_ROOT_CERT`.
- Redis: enable TLS in production; keep Proxy Redis separate from Portal Redis.
- Cloudflare split: origin security groups allow only Cloudflare IP ranges; Portal allows Proxy internal IP for sync.

## 5. Operational commands

```bash
# Start full platform (Portal + Proxy), production mode
./start.sh mvp --app-base-url=https://api.woodgit.com

# Release safety net
./start.sh snapshot
./start.sh restore backups/snapshot-keep-<UTC>.dump portal_check

# Health / logs
./start.sh status
./start.sh logs portal
./start.sh logs proxy

# Master-data export/import (core entities only, not large audit/usage tables)
./portal data export --entities users,enterprises,api_keys,subscriptions,vouchers,providers,models,routing,system_settings --out ./backup-YYYYMMDD
./portal data import --in ./backup-YYYYMMDD --mode upsert --dry-run
./portal data import --in ./backup-YYYYMMDD --mode upsert
```

## 6. Validation performed for this pass

- `go build ./...` for both repositories.
- Targeted tests: `go test ./pkg/jwt ./pkg/mail ./internal/config ./internal/middleware ./internal/routes` in `ai-portal-go`.
- Full E2E/load tests are not part of this pass; run them with the Docker/Postgres/Redis stack before release.
