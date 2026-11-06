# Operational Reliability / Data Durability / Failure-Mode Audit

> **Status:** ACTIVE
> Generated (UTC): 2026-10-20 10:03
> Scope: operational reliability, data durability, failure modes of the WOODGIT AI gateway (limited commercial trial). Two Go services at `/opt/ai-platform`: `ai-portal-go` (control plane) + `ai-proxy-go` (data plane).
> Method: read-only source review focused on code added/changed since the 2026-09-02 production review (`git log --since=2026-09-02`), plus live host inspection (disk/cron/mem). No code modified.
> Prior review read first: `ai-portal-go/docs/reports/production-review-report-202609021205.md`. This report does **not** re-litigate the credential leaks (S-H1 AccessAPI key, S-H2 GitHub PAT) or the media-billing findings already filed there; it covers the six reliability domains requested.

---

## 1. Executive Summary

The funds pipeline design (freeze → settle, local balance cache with Redis Lua, retry buffers, fallback NDJSON log) is sound in intent, and several durability affordances exist (settle retry buffer, usage fallback writer, graceful-degradation auth cache). However, the **durability guarantees are not actually wired end-to-end**, and the **infrastructure has no offsite durability and no alerting**. The dominant risks are not code bugs but unclosed reliability loops:

- **No offsite backup + backups colocated with the database on one 80%-full disk** (D-1, P1). A single EBS/instance loss destroys the DB *and* every backup at once. RPO is effectively "everything since the last local dump that you can still reach," RTO is "rebuild from scratch."
- **`DiskStore.Cleanup()` and `fallbacklog.Writer.Cleanup()` are implemented but never called** (R-1, P1). Retention + fallback data grows unbounded on the already-80% disk. A full disk breaks Postgres writes/WAL on the Portal host.
- **The usage fallback log is write-only — nothing ever replays it** (B-1, P1). When the Portal is unreachable past retries, billed usage is written to NDJSON and then *silently expires*; there is no reconciliation sweep. That is direct unbilled revenue.
- **The chat settle "charge" path is still fire-and-forget `go Billing.Settle(...)`**, bypassing the reliable `SettleBuffer` (B-2, P1). On any transient Portal error or proxy shutdown these charges are dropped and the matching Redis freeze expires uncollected.
- **Key revocation/suspension does not propagate** — the Portal never publishes `key_revoked`/`user_banned`, so a revoked/suspended/deleted key keeps spending until the in-process cache expires: up to 5 min normally, **up to 30 min under graceful degradation** (A-1, P1 at scale / P2 for trial).
- Balance-cache fail-open and single-instance rate limiting are acceptable *for the trial* but are hard scaling blockers (A-2, RL-1).
- **No monitoring/alerting of any kind** beyond a daily backup cron (M-1, P1). Nothing pages a human when the disk fills, the Portal dies, backups stop, or usage-send failures accumulate.

Trial-acceptable vs must-fix-before-scaling split is in §9.

---

## 2. Live Host State (verified 2026-10-20)

| Fact | Value | Source |
|---|---|---|
| Root FS | `/dev/root` 38G, **30G used, 80%** | `df -h /` |
| Postgres data volume | `/var/lib/docker/volumes/ai-portal-go_postgres-data/_data` — **same `/dev/root` disk** | `docker inspect ai-portal-postgres` |
| Backups | `/opt/ai-platform/backups/*.sql.gz`, 7 files, ~1 MB each, **same `/dev/root` disk** | `ls -lah backups/` |
| Backup cron | `0 3 * * * /opt/ai-platform/backup-db.sh` — **the only app cron** | `crontab -l` |
| S3 / offsite upload | **none** — `backup-db.sh` has no upload step at all | read `backup-db.sh` |
| Health/monitoring cron | **none** (only system `certbot`/`sysstat`/`e2scrub`) | `/etc/cron.d/*` |
| Memory / swap | 3.7Gi RAM, 4.0Gi swap (181Mi free, buff/cache 2.3Gi) | `free -h` |

`backup-db.sh` in full:
```bash
docker exec ai-portal-postgres pg_dump -U postgres ai_proxy_portal | gzip > "$BACKUP_DIR/$FILENAME"
find "$BACKUP_DIR" -name "*.sql.gz" -mtime +7 -delete
```
No encryption, no offsite copy, no verification/restore test. DB + WAL + backups all share one 38G disk at 80%.

---

## 3. Domain 1 — Usage / Billing / Settle Buffers: loss & double-settle

### B-1 (P1, must-fix-before-scaling) — Usage fallback log is write-only; unbilled usage silently expires
**Files:** `internal/server/usage_buffer.go:166-199` (`sendBatch` → `writeFallback`), `pkg/fallbacklog/writer.go:37-59` (`WriteRecord`), `writer.go:71-90` (`Cleanup`).

**Verified behavior:** After `maxRetries` (3) failed sends to the Portal, `sendBatch` calls `writeFallback`, which appends each `UsageRecord` to `usage-fallback-YYYYMMDD-HH0000.ndjson`. A repo-wide search for any reader (`ReadRecord`/`ReplayRecords`/`LoadRecords`) returns **nothing** — the only reader in the whole proxy is `DiskStore.readRecord` for retention, unrelated to these files. The `Writer.Cleanup()` that deletes files older than 7 days is **never called** outside tests (grep: references only in `writer_test.go`).

**Failure scenario / blast radius:** Portal unreachable for > ~1 minute (deploy, OOM, network partition) → every usage batch for that window lands in NDJSON. These records represent *actual consumed tokens the platform paid the upstream for*. No process ever re-sends them, so the Portal ledger never learns about them → **permanent unbilled revenue**. Because `Cleanup()` is never wired, the files also accumulate forever on the 80% disk (data-durability + disk-growth double hit). Note the `failedCount` only logs every 100th failure (`usage_buffer.go:191`) with no alert — operators won't notice.

**Fix:** (a) Add a startup + periodic replay job that reads `usage-fallback-*.ndjson`, re-POSTs via `SendUsageStatsBatchToPortal`, and deletes lines on success; (b) wire `Writer.Cleanup()` to a ticker so files are bounded; (c) emit a metric/alert when `failedCount` increases.

### B-2 (P1, must-fix-before-scaling) — Chat settle "charge" path bypasses the reliable SettleBuffer (fire-and-forget)
**Files:** `internal/server/handlers_chat.go` — ~15 call sites use raw `go s.handlerDeps.Billing.Settle(context.Background(), …)`: lines 442, 511, 554, 602, 649, 769, 784, 826, 891, 924, 972, 1015, 1138, 1357 (and the Anthropic path ~1700s). The reliable wrapper `HandlerService.Settle` → `SettleBuffer.AddSettle` exists at `handler_service.go:137-142` and `settle_buffer.go:50-66` but is **not used** by these sites.

**Verified behavior:** `SettleBuffer.processWithRetry` (`settle_buffer.go:81-110`) retries 3× with backoff and `StopAndWait`/`drain` flushes on shutdown (`main.go` calls `settleBuf.StopAndWait(5s)`). The raw `go Billing.Settle(...)` calls get **none** of this: a single transient Portal error (`Settle` returns `{Success:false}` on any HTTP/decode error, `preconsume.go:233-247`) is not retried, and these goroutines are untracked, so `srv.Shutdown` + `cancel()` on SIGTERM drops any in-flight ones.

**Blast radius / revenue impact:** These specific call sites are the `timeout` / `usage_missing` / embedded-error branches — i.e. the **"charge the frozen estimate"** cases (see `DetermineSettleAction`, `preconsume.go:258-280`). When the charge settle is dropped, the Portal freeze (`billing:freeze:` key, 5-min TTL, `webhook_preconsume.go:26`) simply **expires and releases** — the platform never collects for an upstream call it already paid for. During a Portal blip or a proxy redeploy, every timed-out/zero-usage request in flight becomes free. This was flagged LOW ("currently invisible") in the 2026-09-02 review; under commercial traffic it is a direct, silent revenue leak and should be P1.

**Fix:** Replace the raw `go s.handlerDeps.Billing.Settle(...)` sites with `s.Settle(ctx, req)` so they route through `SettleBuffer` (retry + shutdown drain). Pass a non-`context.Background()` or ensure drain covers them.

### B-3 (P2) — In-memory buffers lose data on crash (no WAL); double-settle window is small but real
**Files:** `usage_buffer.go:62-80` (records held in a slice, flushed every 3s or at batch 100), `upstream_usage_buffer.go` (30s flush), `settle_buffer.go` channel size 100.

**Verified behavior:** A hard crash (SIGKILL/OOM, not SIGTERM) loses: up to ~3s of buffered usage records, up to 30s of upstream-usage reconciliation rows, and anything sitting in the settle channel. Graceful SIGTERM is handled well (`main.go` drains in order). **Double-settle:** `Settle` itself is not idempotent on the proxy side; the Portal's `getAndDeleteFreeze` provides the real guard (freeze is deleted on first settle), so a duplicate charge requires a concurrent settle before the freeze is deleted — the prior Portal reviews already track this as a GET+DEL non-atomic window (`full-code-review-202607200416.md` item 8). Not a new regression; noting for completeness.

**Fix (scaling):** Accept small crash-loss window for trial; before scaling, persist the usage buffer to the fallback NDJSON on a crash-safe path and make the Portal settle idempotent by `requestId` (GETDEL / dedup table).

### In-flight pre-consume holds (answering the brief)
Freeze is a Redis hold with a 5-min TTL (`webhook_preconsume.go:26`), it never debits the balance. On proxy crash the hold auto-expires and the user is *not* charged for the input estimate (favor-user), and the balance was never reduced — so **no stuck funds**, but **no collection** for genuinely-consumed input on timeout (same leak as B-2). There is **no dead-letter / reconciliation sweep** for freezes that should have been charged but whose settle was lost.

---

## 4. Domain 2 — Local Balance Cache (proxy auth): fail-open & phantom balance

### A-2 (P2 trial / P1 scaling) — Balance check is fail-open on Redis/Portal outage
**Files:** `internal/auth/auth.go:306-348` (balance pre-check), `auth.go:588-610` (`getBalance` returns `-1` on miss), `PreConsumeLocal` `auth.go:716-760` (returns `handled=false` → fail-open on Redis error), `pkg/billing/preconsume.go:84-171` (`PreConsume` returns `Success:true` on every error path → fail-open).

**Verified behavior:** On Redis error `PreConsumeLocal` returns `(0,true,false)` → caller falls back to Portal `PreConsume`, which itself fail-opens on any transport/5xx error. If both Redis and Portal are degraded, requests proceed **with no balance enforcement**. The balance pre-check in `Handler()` only blocks when it has an authoritative cached value ≤ 0; a cache miss with `claims.Balance > 0` (from the 5-min auth cache) passes. Phantom-balance is explicitly guarded: `SettleLocal`'s Lua only adjusts an *existing* key (`auth.go:773-789`) to avoid recreating a balance from a bare INCRBY — good.

**Blast radius:** During Redis/Portal degradation a user can overspend their balance (bounded by request volume in the outage window and the daily budget controller if configured). This is an intentional availability-over-strictness choice documented in `pricing-architecture-design-202607021800.md:364`. **Acceptable for trial** (small balances, low volume, reconciliation catches most). Before scaling, add a fail-closed mode or a hard cap on cumulative fail-open spend per key.

---

## 5. Domain 3 — Rate-limit enforcement (single-instance only)

### RL-1 (P2, document as scaling constraint) — Local counters, async sync, silent drop on full channel
**Files:** `internal/ratelimit/ratelimit.go:30-66` (per-process `lockFreeBucket`), `:135-137` (`syncChan` size 10000), async sync goroutine, `:174-177` cleanup loops; counters evicted after `maxLocalCounterIdleAge = 24h` (`:22`).

**Verified behavior:** Token buckets are **per-process in-memory**. The Redis sync is async via a 10k channel; the prior review confirms a channel-full silent drop. On restart all counters reset to full. With a single proxy instance this is correct. With **N instances**, effective limits become ~N× the configured value (no shared enforcement on the hot path), and a restart grants everyone a fresh full bucket.

**Blast radius:** Abusive/compromised key can exceed its RPM/TPM by the instance-count factor and across restarts, increasing upstream spend and the chance of tripping upstream provider limits. **Acceptable for the current single-instance trial.** Must-fix before horizontal scaling: move enforcement to a shared Redis token bucket (Lua) on the request path, or accept documented N× slack.

---

## 6. Domain 4 — Key-validation cache TTL: revocation propagation delay

### A-1 (P1, must-fix-before-scaling; P2 for trial) — Revoked/suspended keys keep spending for up to 5 min (30 min degraded)
**Files:** proxy `internal/auth/auth.go:30` (`defaultCacheTTL = 5*time.Minute`), `:34` (`gracefulTTL = 30*time.Minute`), `getCachedClaims` `auth.go:886-911` (serves expired entries up to `gracefulTTL` when `portalHealthy=false`), pub/sub consumer `auth.go:792-870` (handles `key_revoked`/`user_banned`). Proxy has `PublishKeyRevoked`/`PublishUserBanned` (`auth.go:650-679`) that are **never called** (grep: definitions only). Portal publishes **only** `balance_update` — `PublishBalanceUpdate` at `internal/db/db.go:592-611`, invoked from `webhook_usage.go:850` and `webhook_billing.go:647`. A repo-wide search shows **no Portal code path publishes `key_revoked` or `user_banned`** (the `key_revoked` strings in `enterprise_keys.go:486` / `management_tokens_consumers.go:182` are *audit-log* `ChangeType` values, not Redis publishes).

**Verified behavior:** When an admin/user revokes, suspends, deletes, or bans a key, the proxy's 5-min positive auth cache keeps returning the old claims. Nothing invalidates it until it expires. Only a *balance drop to ≤0* propagates instantly (via `balance_update`). So a revoked-but-still-funded key keeps authenticating and spending for **up to 5 minutes**, and **up to 30 minutes** if the Portal is simultaneously unhealthy (graceful degradation serves the stale entry). The Portal-side pre-launch review already identified this exact gap (`pre-launch-review-key-user-member-usage-202607270342.md:205-207`): "撤销通道：pub/sub 只有 balance_update，无 key revoke/user ban 消息…禁用 key 只能等缓存过期."

**Blast radius / impact:** Abuse/fraud/compromised-key response is delayed by the full cache window; a leaked key cannot be hard-stopped faster than 5 min. The wiring already exists on the proxy consumer side — only the Portal publisher and the proxy's own self-publish are missing. **Acceptable for trial** (small blast radius, admins can tolerate 5 min); **must fix before scaling** because incident response SLA depends on it.

**Fix:** On key revoke/suspend/delete and user ban in the Portal routes, call `db.RedisPublish(ctx, RedisBalanceChannel, {type:"key_revoked",key_hash:…})` / `{type:"user_banned",user_id:…}` (and HTTPS callback for cross-VPC, mirroring `PublishBalanceUpdate`). The proxy consumer already handles both message types.

---

## 7. Domain 5 — Retention disk growth + cleanup gap → disk-full → Postgres/WAL

### R-1 (P1, must-fix-before-scaling) — Retention and fallback cleanup never wired; growth unbounded on 80% disk
**Files:** retention store `internal/retention/disk_store.go:160-181` (`Cleanup()` deletes files older than `MaxAge`), wired in `cmd/server/main.go:215-247` which starts the `DiskStore` and the analytics scheduler but **never schedules `Cleanup()`**. Grep confirms the only `.Cleanup()` callers are tests (`disk_store_test.go:100`) and the ratelimit/auth `cleanupLoop`s (unrelated). The fallback writer `Cleanup()` is likewise unwired (§B-1).

**Verified growth math (data-plane, proxy host):** `DiskStore.Record` writes one gzip JSON file per request under `retention/<y>/<m>/<d>/<h>/<trace_id>.json.gz`. The prior review measured 804 MB / 5,605 files at ~trial volume. At `MaxAge=30d` default with no cleanup, steady-state size = (files never deleted) × avg size → **monotonic growth**; at even 2,000 req/day that is ~60k files/month accumulating indefinitely. The analytics scheduler also scans the entire 90-day tree every 6h, so unbounded growth also degrades that job (prior review P-M3). Note retention is opt-in (`cfg.Retention.Enabled`); if enabled in production the growth is real.

**Disk-full consequences (Portal host):** The Portal's Postgres data volume, WAL, backups, and container logs **all share the single `/dev/root` 38G disk, currently at 80%**. When the disk fills: Postgres cannot write WAL → transactions fail → the control plane (key validation `/api/apikeys/validate`, billing webhooks, admin) returns errors → the proxy loses its auth/billing backend. A full WAL disk can also prevent Postgres from starting cleanly after a restart. Even without retention (which runs on the proxy host), the Portal disk is squeezed by Docker logs + daily backups with no rotation guard beyond the 7-day `find -delete`.

**Blast radius:** Disk-full is a full control-plane outage with a messy recovery (free space, possibly `pg_resetwal` risk). **Must-fix before scaling.** For the trial, the immediate concern is the 80% starting point.

**Fix:** (a) Wire `DiskStore.Cleanup()` and `fallbacklog.Writer.Cleanup()` to tickers (hourly/daily). (b) Add a disk-usage alarm at 85%/90% (see M-1). (c) Put Postgres data and/or backups on a separate volume; add log rotation / Docker `max-size` log opts. (d) Lower retention `MaxAge` or cap total bytes.

---

## 8. Domain 6 — Monitoring/alerting, SPOF, RTO/RPO

### M-1 (P1, must-fix-before-scaling) — No alerting; failures are silent
**Verified:** The only app cron is the 03:00 pg_dump; no health-check cron, no CloudWatch alarms, no uptime probe, no log-based alerting (`crontab -l`, `/etc/cron.d/*`). `/api/health` and `/health` exist but nothing polls them. Usage-send failures only log every 100th occurrence (`usage_buffer.go:191`); backup failures only append to `backups/backup.log` which no one watches. There is no notification when the Portal container dies, the disk fills, backups stop, or unbilled-usage fallback accumulates.

**Blast radius:** Any of the failure modes above (disk-full, Portal crash, Redis loss, backup-cron failure) goes unnoticed until a user complains. MTTR is unbounded.

**Fix:** CloudWatch agent + alarms for disk ≥85%, memory, instance status, and a canary hitting `/api/health` + `/health`; alert on backup-cron non-zero exit and on proxy `usage_send_failures` metric; page on container restart.

### D-1 (P1, must-fix-before-scaling) — No offsite backup; DB and backups share one disk/instance
**Verified (§2):** Single Docker Postgres, no RDS/Multi-AZ; `backup-db.sh` keeps 7 daily local dumps with **no S3/offsite step**; backups live on the same `/dev/root` as the DB.

**RPO/RTO reality:**
- **RPO:** best case ~24h (last nightly dump); but if the disk/instance is lost, **all 7 local dumps are lost with it** → RPO = "last dump you manually copied off the box," realistically ∞ for anything not exfiltrated. The prior review already records a real incident of 638 audit rows deleted with no recoverable backup (`S-M2`).
- **RTO:** rebuild EC2 + Docker + restore dump + reconfigure both hosts = hours, manual, undocumented runbook for a cold start.
- **SPOF:** both EC2 instances are single points; Portal Postgres/Redis single-container; proxy Redis single-container. Any instance loss is a full outage for that plane.

**Fix:** Ship dumps to S3 (versioned, lifecycle, cross-region) immediately — the single highest-value change. Medium term: managed Postgres (RDS) with PITR + automated snapshots, and a documented restore runbook with a periodic restore test.

---

## 9. Trial-Acceptable vs Must-Fix-Before-Scaling

| ID | Finding | Severity | Trial-acceptable? | Must-fix before scaling? |
|---|---|---|---|---|
| D-1 | No offsite backup; backups colocated on 80% disk | P1 | ❌ (one disk loss = total data loss) — **fix now** | Yes |
| R-1 | Retention/fallback `Cleanup()` never wired → disk-full → PG/WAL | P1 | ⚠ risky at 80% — **wire cleanup + alarm now** | Yes |
| B-1 | Usage fallback log write-only, never replayed → unbilled revenue | P1 | ⚠ low volume now, but silent | Yes |
| B-2 | Chat "charge" settle fire-and-forget, bypasses SettleBuffer | P1 | ⚠ small leak now | Yes |
| M-1 | No monitoring/alerting at all | P1 | ⚠ tolerable while watched manually | Yes |
| A-1 | Revocation not propagated (5-min, 30-min degraded) | P1 | ✅ small blast radius | Yes |
| A-2 | Balance check fail-open on Redis/Portal outage | P2 | ✅ intentional, low volume | Add cap/fail-closed option |
| RL-1 | Rate limit single-instance only; silent drop; reset on restart | P2 | ✅ single instance | Yes (shared Redis limiter) |
| B-3 | In-memory buffer crash-loss window; settle not idempotent | P2 | ✅ small window | Harden before scaling |

Already-tracked and intentionally out of scope here: fail-open pre-consume by design, GET+DEL freeze non-atomic double-charge window, credential leaks (S-H1/S-H2 — still require rotation per the 2026-09-02 review).

---

## 10. Top Recommendations (ordered)

1. **(D-1)** Add S3 offsite upload to `backup-db.sh` today; verify one restore. Highest value, lowest effort.
2. **(R-1)** Wire `DiskStore.Cleanup()` + `fallbacklog.Writer.Cleanup()` to tickers; add disk ≥85% alarm; move backups off the DB disk.
3. **(B-1 + B-2)** Replay the usage fallback NDJSON on startup + periodically; switch chat "charge" settles to `s.Settle()` (SettleBuffer). Closes the two silent revenue leaks.
4. **(A-1)** Publish `key_revoked`/`user_banned` from the Portal on revoke/suspend/delete/ban (proxy consumer already handles them).
5. **(M-1)** CloudWatch agent + alarms (disk, mem, instance status, health canary, backup-cron exit, usage-send-failure metric).
6. **(RL-1, A-2, B-3)** Scaling-gated: shared Redis rate limiter, fail-open spend cap, idempotent settle + crash-safe buffer persistence.
