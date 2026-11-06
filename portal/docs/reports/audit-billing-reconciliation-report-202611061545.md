# Audit / Billing / Reconciliation Run — trace_id Linkage

Status: ACTIVE
Date: 2026-11-06
Scope: Production Portal DB `ai_proxy_portal` (read-only queries) + Proxy retention volume on the Proxy EC2 (file names and metadata only; no prompt bodies read). Plan item 9 in `docs/commercial-hardening-plan-202611061517.md`.

Nothing was written to the production DB in this run. The forward fix (Proxy commit `bd8d8c8`, Portal `c24e553`) is committed but **not yet deployed**, so all numbers below describe the pre-fix state.

---

## 1. Summary

| Check | Result |
|---|---|
| usage_records ↔ retention files by trace_id (Oct–Nov) | **16,197 / 16,256 usage rows (99.6%) have a retention file with the same trace_id** → retention is a usable proof-of-request source |
| proxy_request_audits ↔ usage_records by trace_id | **0 / 44,612** (audit logger generated its own UUID — fixed in `bd8d8c8`) |
| proxy_request_audits ↔ usage_records heuristic (key + model + provider + tokens + ±5 s, unique both ways) | only 5,587 linkable; 34,643 audit rows reference keys that were **hard-deleted** (load-test keys) |
| Enterprise Aynbay (credit) | **Reconciles exactly**: Oct record-level credit deductions $95.45 = Oct statement `credit_used` $95.45; Nov $64.18 = current `credit_used` $64.18 |
| Enterprise Intel (prepaid) | **Nov reconciles**: Nov usage $48.80 ≈ Δprepaid_balance $48.81 (statement snapshot $4,160.05 → $4,111.24). **Oct cannot be reconciled**: no `prepaid_topup` record exists for the initial wallet funding |
| Personal ledger (`balance_transactions`) vs record deductions | Aug–Nov gift reconciles exactly; July gift differs by **$36.29**; balance ledger $132.31 vs records $128.01 (+$4.30); credit ledger $200.15 vs records $206.91 (−$6.76, of which $6.90 is the Aynbay manual adjustment) |
| **Unbilled usage under load (new P0)** | **11-02 load test: 26,330 successful streaming requests with tokens have retention + audit rows but no usage_records row** (≈75% of that day) |

## 2. Linkage method

1. Retention files are named `<trace_id>.json.gz`; that trace_id is the same as `usage_records.trace_id` (RequestID middleware). Listed all Oct–Nov file names (42,571 files) and joined with usage trace_ids (16,256 rows since 10-01).
2. For proxy_request_audits there is no shared key before the fix. Tried a heuristic join on `api_key_hash → api_keys.id`, model, provider, input/output tokens and ±5 s; kept only pairs unique in both directions.
3. Enterprise reconciliation used `enterprise_statements` (monthly snapshot before reset), current enterprise counters, and record-level `*_deduction` columns.

Per-day comparison retention vs usage (only days with differences):

| Day | Retention files | Retention without usage | Usage rows |
|---|---|---|---|
| 2026-10-22 | 924 | 42 | 901 |
| 2026-10-25 | 614 | 2 | 612 |
| 2026-11-02 | 34,957 | **26,330** | 8,627 |

- 10-22 / 10-25: sampled all 44 files — they are 200 responses with **0 tokens** (claude-opus-5 via openrouter returned no usage) or 401/402 rejections. Not billable, not a loss — but the zero-token 200s mean the upstream did not report usage; worth an upstream check because the customer did receive answers.
- 11-02: sampled 300 of 26,330 — all `200`, `chat_completions_stream`, input tokens > 0, mostly `claude-haiku-4.5` on Bedrock and OpenRouter (some with fallback), load-test keys `202e2516 / 5a2a79c7 / 18b7b22e` and also `06f361d2`, `6c4fa8f5`.
- Only 59 usage rows in Oct–Nov have no retention file (written while capture was skipped/failed).

## 3. Findings

### F1 (P0) Usage records are lost under concurrency
On 11-02 (SLA test, c100–c200) audit rows sum to 299.2 M input tokens (289.7 M cached) vs 120.9 M in usage_records. Roughly 178 M input tokens (mostly cache reads) were served and never billed. At that day's mix ($123.48 on 120.9 M input) the unbilled amount is on the order of **$150–$200** (estimate; the test traffic was internal, but the same path bills customers).

Root cause **not yet confirmed**: Proxy and Portal container logs from 11-02 have rotated (containers restarted since). Candidates to check, in order:
1. Proxy `UsageBuffer` capacity / drop-on-full under burst (the usage path is separate from the audit logger, which did keep all rows);
2. Portal `webhook:usage-stats:queue` worker failing for keys that were banned mid-test ("balance exhausted → auto-ban" in the SLA report) — records for a banned/deleted key may be discarded instead of stored with `pricing_error`;
3. fallback-log replay not covering the 11-02 window.

Next step: reproduce in staging with c200 against a key whose balance runs out, count `retention files == usage rows`, and add a metric/alert `usage_records_per_min / retention_files_per_min < 0.99`.

### F2 (P1) Prepaid top-ups are not recorded
Intel's prepaid wallet ($4,111 now) has no `prepaid_topup` entry in `enterprise_audit_log` and no `balance_transactions` row; the reconciliation code (`admin_reconciliation.go`) expects one. The wallet was very likely funded by direct DB edit. Funding must go through the API (audit + ledger), and the Oct opening balance needs to be confirmed with finance and back-filled as an `admin_adjustment` with a reason.

### F3 (P1) Personal ledger is batched, not per request
`balance_transactions.reference_id` is `batch-YYYYMM` for most usage rows (only 2,422 / 6,962 reference a trace_id). Per-request traceability from ledger to usage is therefore impossible for those rows. Recommend writing one ledger row per settled trace (or keep batches but store `trace_count` + a digest of trace_ids).

### F4 (P2) Historical gaps (July)
July gift deductions on records ($121.25) exceed the gift ledger ($84.96) by $36.29; July is the early-trial period before the billing fixes and includes hard-deleted data. Recommend accepting and documenting as a pre-launch variance rather than restating.

### F5 Audit linkage before the fix
proxy_request_audits cannot be linked retroactively in a trustworthy way (5,587 heuristic matches, most remaining rows point to hard-deleted keys). Decision proposal: **do not back-fill** trace_ids into old audit rows (a back-fill would itself be a mutation of audit data); keep them as-is, mark the cut-over time after deployment, and rely on retention ↔ usage (99.6%) for the historical period.

## 4. Re-run after deployment

```sql
-- audit ↔ usage linkage (expect ≥ 99% of audits with tokens)
select count(*) filter (where u.trace_id is not null)::float / nullif(count(*),0)
from proxy_request_audits a left join usage_records u on u.trace_id = a.trace_id
where a.created_at > now() - interval '1 day' and a.input_tokens > 0;

-- enterprise credit reconciliation for a month
select e.name, s.period, s.credit_used_micro/1e6 as statement,
  (select sum(credit_deduction)/1e6 from usage_records r join users u on u.id=r.user_id
    where u.enterprise_id=e.id and to_char(r.created_at,'YYYY-MM')=s.period) as records
from enterprise_statements s join enterprises e on e.id=s.enterprise_id;
```
Retention ↔ usage: list `ent/*/*d/YYYY/MM/DD` file names on the Proxy host and join with `usage_records.trace_id` as in §2.
