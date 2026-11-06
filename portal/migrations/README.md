# Database Schema & Migration Strategy

Status: **ACTIVE** — versioned migrations are enabled on `main`.

## What runs on startup

`cmd/server/bootstrap.go` initializes the schema in two stages:

1. `database.InitSchema(ctx, logger)` — idempotent baseline from `internal/db/schema.go`
   (`CREATE TABLE IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS`). This is still the
   source of truth for **fresh databases** and for the static checker
   (`scripts/check_schema_vs_code.sh`).
2. `database.Migrate(ctx, logger, migrationsDir)` — applies ordered SQL files from
   this directory. `migrationsDir` defaults to `./migrations` and can be overridden
   with `MIGRATIONS_DIR`.

`Migrate()` is safe when the directory is missing or empty: it creates
`schema_migrations` and returns without applying anything.

## Current files

| File | Purpose |
|---|---|
| `0001_baseline.sql` | Marker only. Records version `0001` in `schema_migrations`; no schema change. |
| `0002_seed_managed_by.sql` | Adds `managed_by` to `subscription_plans` and `system_settings` so future seed runs update only `managed_by='seed'` rows and never overwrite admin-edited rows. |

| `0034_audit_tamper_evidence.sql` | Hash chain + append-only guards for `audit_logs` / `enterprise_audit_log`, signed `audit_anchors`, delete guards on `usage_records` / `balance_transactions` / `orders`, `api_key_tombstones`. Also embedded (`migrations/embed.go`) and executed by `InitSchema`. |
| `0035_totp_mfa.sql` | TOTP multi-factor authentication: `users.totp_secret_enc/totp_enabled/totp_enabled_at/totp_last_step`, `user_recovery_codes` (sha256 hashes, one-time), `enterprises.require_mfa`, and the `mfa_required_roles` system setting. All defaults force MFA on nobody. Mirrored in `schema.go`. |
| `0036_enterprise_sso.sql` | Per-enterprise OpenID Connect SSO: `enterprise_sso_configs` (issuer, client id, AES-GCM `client_secret_enc`, allowed e-mail domains, JIT and enforce switches; everything off by default) and `user_identities` (`UNIQUE(issuer, subject)`). Mirrored in `schema.go`. |
| `0037_retention_policy_reapply.sql` | Idempotent re-run of the retention-policy part (section C) of `0033` for databases that applied the earlier audit-only `0033` (production, 2026-11-06): enterprise retention constraints and seeded `retention_default_mode/days`. No-op elsewhere. |

### Operating the 0034 guards (DBAs)

* Audit tables reject `DELETE`/`TRUNCATE` unless the transaction sets
  `SET LOCAL app.audit_purge = 'on'`. Only the retention job does this; run manual
  purges the same way, inside a transaction, and only remove a **prefix** of the
  chain (oldest `chain_seq` first) so the remaining rows still link up.
* Ledger tables reject `DELETE` unless `SET LOCAL app.allow_ledger_delete = 'on'`.
* `ALTER TABLE ... DISABLE TRIGGER` bypasses all of this by design (table owners
  can always do it); the daily HMAC-signed anchors and `portal verify-audit`
  (exit code 1 on failure, suitable for cron) are what make such edits detectable.
* Never use plain `SET` (session scope) for these GUCs on a pooled connection.

## Adding a new migration

1. Create `migrations/NNNN_short_description.sql` with the next numeric prefix
   (for example `0003_add_invoice_status.sql`). The prefix before the first `_` is
   the migration version; files are applied in lexical order.
2. Make the SQL idempotent where possible (`IF NOT EXISTS`, `ON CONFLICT`, guarded
   backfills).
3. Include data backfills in the same file when needed (for example
   `INSERT ... SELECT`), so `migrate up` carries existing data forward.
4. Keep `internal/db/schema.go` in sync for fresh-database initialization and the
   static checker, but do **not** use `schema.go` as the production migration
   mechanism.
5. Review for version-prefix collisions before merging; `loadMigrations` fails on
   duplicate versions.

## Rollback

There is no automatic down migration. Roll back with the operational safety net:

```bash
./start.sh snapshot            # before release
./start.sh restore backups/snapshot-keep-<UTC>.dump portal_check
```

Verify the restored database, then switch traffic to it. Never hand-run
`ALTER`/`UPDATE` against production to "fix" a migration.
