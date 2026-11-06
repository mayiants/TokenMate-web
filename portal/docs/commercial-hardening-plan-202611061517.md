# Commercial Hardening Plan (post trial-month review)

Status: ACTIVE
Date: 2026-11-06
Source review: `docs/reports/commercial-readiness-review-report-202611061240.md`
Owner workflow rule (from user, 2026-11-06): **one feature per commit; build + test after each commit; deploy one feature at a time** so regressions can be located. Update the status table below whenever an item changes, so work can resume the next day from this file.

---

## 1. Status board

| # | Item | Repo | Migration | Code | Commit | Deployed | Prod verified |
|---|---|---|---|---|---|---|---|
| 0 | Remove PAT from HANDOVER (use `workspace/github/ghp.txt` temporarily; user deletes after testing) | root | – | ✅ HANDOVER has 0 PATs; AGENTS.md updated | n/a (no root repo) | n/a | ✅ |
| 1 | Alert webhook in `monitor.sh` (feishu/dingtalk/slack format, backup-freshness check, proxy-disk check, config file `/opt/ai-platform/.monitor.env`) | root | – | ✅ | n/a (no root repo) | ✅ cron already runs it | ⏸ user will provide the webhook URL later |
| 2 | Proxy: unified trace_id in audit, client IP / UA, `X-Trace-Id` header, failed-request audit middleware (dedupe + anonymous flood limit) | ai-proxy-go | – | ✅ | ✅ `bd8d8c8` | ⏳ | ⏳ |
| 3 | Proxy: per-enterprise retention (mode off/metadata/standard + days from Portal validate), tenant layout `ent/<id>/<days>d/...`, hourly `Cleanup()` wired, disk guard, admin list/purge, access log | ai-proxy-go | – | ✅ | ✅ `c748bf7` | ⏳ | ⏳ |
| 4 | Portal: audit fields (proxy_request_audits user_agent/http_status/enterprise_id, idempotent insert, retention rule; audit_logs IP/UA via context; enterprise_audit_log action backfill) | ai-portal-go | 0033 (part) | ✅ | ✅ `c24e553` | ⏳ | ⏳ |
| 5 | Portal: per-enterprise retention policy (enterprises.retention_mode/days, validate response fields, settings UI, purge → Proxy) | ai-portal-go | 0033 (part) | ✅ | ✅ `35f26b0` | ⏳ | ⏳ |
| 6 | Portal: tamper evidence (hash chain on audit_logs / enterprise_audit_log, update/delete/truncate guards, daily HMAC anchors, verify API + CLI + UI button, soft-delete reason, ledger delete guard, api_key_tombstones) | ai-portal-go | 0034 | ✅ | ✅ `6c30917` | ⏳ | ⏳ |
| 7 | Portal: TOTP MFA (single session chokepoint, recovery codes, lockout, enterprise require_mfa, admin reset) | ai-portal-go | 0035 | ✅ | ✅ `8ade42f` | ⏳ | ⏳ |
| 8 | Portal: OIDC SSO per enterprise (PKCE, SSRF guard, JIT, enforce_sso) | ai-portal-go | 0036 | ✅ | ✅ `dc0722e` | ⏳ | ⏳ |
| 9 | Historical reconciliation run (read-only): `docs/reports/audit-billing-reconciliation-report-202611061545.md`. Decision proposed: no back-fill of old audit trace_ids | prod DB (read-only) | – | ✅ | docs commit | – | ✅ |
| 12 | **P0 new**: usage_records lost under concurrency (11-02: 26,330 served streaming requests with no usage row). Reproduce in staging, find root cause, add usage-vs-retention ratio alert | ai-proxy-go / ai-portal-go | – | ⏳ | ⏳ | ⏳ | ⏳ |
| 13 | Prepaid top-up must go through API (audit + ledger). Intel opening balance **confirmed by user: $4,587.00 deposited 2026-09-30** — reconciles exactly (4,587.00 − Oct 426.95 = 4,160.05 = Oct statement end; − Nov 48.80 = 4,111.25 vs balance 4,111.24, sub-cent rounding). Back-fill a `prepaid_topup` record (enterprise_audit_log + statement `prepaid_topup_micro` for 2026-10) **via the API after item 6 (0034) is deployed**, so the record enters the hash chain; no balance change | ai-portal-go + prod data | – | ⏳ | ⏳ | ⏳ | ⏳ |
| 10 | Proxy retention data cleanup (keep Oct only per user) | proxy host | – | ✅ Jul/Aug/Sep deleted (2026-11-06, 811 MB). Nov kept (user confirmed 2026-11-06: keep Oct + Nov) | – | – | ✅ |
| 11 | Compliance (DPA, sub-processor list, SOC2 gap analysis) | – | – | ⏸ separate task per user | – | – | – |

Legend: ✅ done, ⏳ pending, ⏸ deferred.

> Note: items 2–8 were developed in parallel on 2026-11-06 before the one-feature-per-commit rule was given; they were then split into one commit per row, and **each commit was built and tested in isolation** (fresh scratch DB per commit). Not pushed. From now on: one item at a time — implement → commit → test → deploy → verify.

## 2. Commit / test procedure (per item)

1. Stage only the files/hunks of that item; `git diff --cached --stat` to review.
2. In a clean worktree of the staged state: `go build`, `go vet`, `gofmt -l`, unit tests, and for Portal the DB tests against the **scratch** DB:
   ```bash
   # scratch instances (never the prod container ai-portal-postgres)
   docker run -d --name scratch-pg -e POSTGRES_PASSWORD=scratchpw -p 127.0.0.1:55432:5432 postgres:15-alpine
   docker run -d --name scratch-redis -p 127.0.0.1:56379:6379 redis:7-alpine
   export DB_CONTAINER=scratch-pg DB_HOST=127.0.0.1 DB_PORT=55432 DB_USER=postgres \
          DB_PASSWORD=scratchpw DB_NAME=ai_proxy_portal_test REDIS_ADDR=127.0.0.1:56379
   go test -p 1 ./internal/db/... ./internal/routes/... ./internal/services/... ./cmd/...
   bash scripts/check_schema_vs_code.sh && python3 scripts/i18n_full_audit.py --strict
   ```
   `DB_CONTAINER` matters: the test snapshot/restore helpers `docker exec` into that container (default is the prod container, which only holds `ai_proxy_portal_test` but must not be used).
3. Commit with a Conventional Commit message; do not push until the user approves.
4. Deploy one item, verify in prod (SQL/HTTP checks listed in §3), then move to the next.

Not available on this host: Node (Tailwind rebuild, English lint, Playwright) and golangci-lint → only verifiable via GitHub CI.

## 3. Deployment order and prod checks

Order matters because of the validate-response contract and migrations:

1. **Portal item 4** (0033 audit part) — **must ship together with `da93ae6`** (INET scan fix; without it the admin audit-log list shows blank rows once IPs are recorded). Build from a deploy branch `c24e553` + cherry-pick `da93ae6` (verified: applies cleanly and builds). Check: new proxy_request_audits columns exist; `audit_logs` new rows have `ip_address`; `/api/admin/audit-logs` shows IP and correct time.
2. **Proxy item 2** → after ~10 min:
   ```sql
   select count(*) filter (where u.trace_id is not null) * 100.0 / count(*)
   from proxy_request_audits a left join usage_records u on u.trace_id = a.trace_id
   where a.created_at > now() - interval '10 minutes';            -- expect ≥ 99%
   select count(*) filter (where client_ip <> '') from proxy_request_audits
   where created_at > now() - interval '10 minutes';              -- expect > 0
   select status, count(*) from proxy_request_audits
   where created_at > now() - interval '1 day' group by 1;        -- errors now appear
   ```
   Also `curl -i` a request and confirm `X-Trace-Id`.
3. **Portal item 5** (retention policy) → validate response contains `enterprise_id/retention_mode/retention_days`.
4. **Proxy item 3** → new files under `ent/<id>/<days>d/`; cleanup log line within the first hour; admin purge on a test enterprise.
5. **Portal item 6** (0034) → `GET /api/admin/audit/verify` ok; `DELETE FROM audit_logs` rejected; first anchor next day.
6. **Portal item 7** (0035) → nobody is forced into MFA by default; enrol a test admin and log in.
7. **Portal item 8** (0036) → set `APP_BASE_URL`; configure a test IdP; enforce_sso off by default.

Proxy validate cache is ~5 min: restart Proxy or wait after Portal auth changes.

Deploy mechanics (per step): `git worktree add /tmp/deploy-N <commit>` (+ needed fix cherry-picks), build the image from that tree, run the checks, then move to the next step. CI fixes `d9d6dd7` (SSO) ships with item 8, `bbe6458` (test-only) and `d6b96b0` (CLI errcheck) with item 6; proxy `089d02d` (imports only) with item 2.

## 4. Open questions for the user

- ~~Retention Nov~~ keep (answered). ~~Intel opening balance~~ $4,587 on 2026-09-30 (answered).
- Alert webhook URL and type (deferred by user) (feishu / dingtalk / slack). Put it in `/opt/ai-platform/.monitor.env` (chmod 600):
  `ALERT_WEBHOOK_URL=...` and `ALERT_WEBHOOK_FORMAT=feishu|dingtalk|slack`.
- Retention: keep November payloads (live) or delete? Default retention for enterprises is `standard / 90 days`; should the platform default be `metadata` instead?
- `$480` of usage rows without a record-level deduction (Oct) – confirm reconciliation approach (item 9).
- Remaining review items not in this round: offsite backup (S3), staging environment, DB role separation, key IP allowlist, key "reveal" removal, encryption-key KDF/rotation, SAML/SCIM.

## 5. Change log

- 2026-11-06 15:17 UTC — plan created; items 0, 1 (script), 10 done; items 2–8 implemented and passing tests in the working tree, not yet committed.
- 2026-11-06 15:50 UTC — items 2–8 split into per-feature commits (proxy `bd8d8c8`, `c748bf7`; portal `c24e553`, `35f26b0`, `6c30917`, `8ade42f`, `dc0722e`), each verified in isolation. Item 9 report written; new items 12 (P0 usage loss under load) and 13 (prepaid top-up ledger) added. **Next session starts at: push for CI (needs approval) → deploy item 4 → verify → item 2 → …** (§3 order). Nothing deployed yet.
- 2026-11-06 16:00 UTC — pushed to GitHub main (portal `196f8a3..8fe6371`, proxy `363da2d..c748bf7`). CI results not yet checked. **Next session: check CI for both repos → fix any failures (one commit per fix) → deploy in §3 order.**
- 2026-11-06 16:15 UTC — user answers: webhook later; keep Nov retention; Intel prepaid $4,587 on 09-30 → Intel prepaid reconciles exactly (item 13 updated). Reconciliation report finding F2 is therefore a *missing record*, not a money discrepancy.
- 2026-11-06 17:40 UTC — CI after first push: both repos failed golangci-lint only (portal 11, proxy 1). Fixed one commit per feature: proxy `089d02d`; portal `d9d6dd7` (SSO: sentence-case problem strings, EC JWK via `ecdsa.ParseUncompressedPublicKey` + new test), `bbe6458` (MFA test: unused field; chain verify scoped to own rows — other tests' cleanups use the purge GUC), `d6b96b0` (CLI errcheck). Second CI: proxy all green; portal fast-gate green, **Playwright role round-trip failed → real bug**: INET `ip_address` scanned into `*string`, error ignored → blank audit rows. Fixed in `da93ae6` with regression test (fails on old code). Local: golangci-lint v2.14.0 installed at /tmp/gl (0 issues), full DB regression on fresh scratch DB green.
- Ops note: Portal host root disk hit 92% (Go build cache 12 GB) → `go clean -cache` → 62%. Proxy retention tests fail when host disk > `RETENTION_MAX_DISK_PERCENT` (90) — follow-up: pin the threshold in tests.
- 2026-11-06 19:35 UTC — session takeover after SSH drop. Portal deployed through step7 (items 4–8; migrations 0033–0037 applied, `latest` = step-step7, rollback tags `rollback-pre-step*`). Health/login 200; no enterprise has SSO enforced. Remaining: item 12 (P0 usage loss), item 13 (prepaid top-up via API), verify Proxy deploy of items 2–3 (proxy host git is still at 363da2d, container ~23 min old), CI watch for pushed commits, webhook URL, cleanup of scratch containers and /tmp worktrees.
- 2026-11-06 20:50 UTC — items 2–8 all deployed and verified with real traffic (personal + enterprise key, glm-5.3/kimi-k3). Found and fixed proxy `a2803fe` (http_status NULL on successful chat requests; deployed, verified 200 on non-stream + stream). Portal `9d8cb5d` and proxy `a2803fe` pushed; CI watchers write to `workspace/ci-status/`. Open: 3001 SG/key-encryption check (user), items 12 and 13, webhook URL, cleanup. Handover updated.
