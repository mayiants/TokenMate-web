# Weekly Code Review & Platform Status — 2026-09-07 → 2026-09-14

> Status: ACTIVE
> Scope: `ai-portal-go` (38 commits) + `ai-proxy-go` (6 commits) since 2026-09-07, deployment state, CI health.
> Date: 2026-09-14 12:00 UTC

---

## 1. Executive Summary

The week was dominated by **enterprise feature hardening** (management tokens, self-service keys, audit-log gap fixes), **billing precision work** (tiered_context mode, media/flat unit price display), **upstream operability** (Bedrock ABSK keys, model-list auto-sync, liveness probe), and a **public-site SEO/anti-abuse pass**. Code velocity is healthy and portal CI is green at head (`ffe0e1a`).

Two systemic issues surfaced during this review and were fixed on 2026-09-14:

1. **ai-proxy-go CI had been red since 2025-08-12** (~1 month, 6+ merges on red). Root cause: `.golangci.yml` used the v1-only key `issues.exclude-rules`, so `golangci-lint config verify` failed before any linting ran. Fixed in `16e180b` (v2 schema `linters.exclusions`, then matched the AGENTS.md linter set). Correcting the config exposed **72 pre-existing lint findings** (58 errcheck, 14 staticcheck) that had never been enforced — documented as debt in the config comment; errcheck/staticcheck/unused/ineffassign stay disabled until cleaned up.
2. **HANDOVER.md was not updated for 09-10 → 09-13 sessions** (management tokens, enterprise audit fixes, SEO rollout, ABSK keypool support are missing from the handover log). Added in this session's HANDOVER increment.

## 2. Commit Review by Theme

### ai-portal-go (38 commits)

**Billing & pricing** — the most technically significant work:
- `b4cf2a6` `tiered_context` billing mode: whole-request tier chosen by context length; rule carries `tiers[]` with per-tier price sets (`docs/context-tier-billing-design-202609090545.md`). First applied to laozhang `grok-4.6` / `gpt-6-astra`. Follow-up `cbb6a3b` wired the mode into the pricing filter + rule editor UI.
- `4738ca4` group pricing drawer now shows media/flat unit prices (fixes empty-price display reported for group-image/group-video).
- `07f102c` upstream-pricing Billing Type select: was 2-option and would silently rewrite `flat` → `per_token` on save; now 5 modes with exact matching.

**Enterprise & access control**:
- `425bd06` controlled self-service enterprise keys for members; `f9b3738`/`b6effa9`/`b073629` programmatic management tokens (P1 UI, P2 keys API, P3 members API — plan in `docs/management-token-feature-plan-202609111530.md`).
- `738fa9a`/`781c26f` closed enterprise_audit_log gaps (key deletion was unlogged).
- `46c8f56` **security-relevant**: stopped platform-catalog leak in enterprise group model selection + fixed select-all/clear. Verified the fix keeps backend model filtering intact (no auth bypass).

**Upstream operability**:
- `5e0e1e8` auto-sync upstream key model lists (additive merge) — directly addresses the "upstream model list goes stale" gap.
- `c441508` upstream model liveness probe + first report (`docs/reports/upstream-model-liveness-report-202609081530.md`).
- `db8e549`/`2665f11`/`9f2ad54` Bedrock API-key (ABSK) support in keypool add-form, provider acceptance, and re-validate.
- `ecf41f6` group deletion blocked only by *active* keys (was: any key).

**Key/group model pickers UX**: vendor chips + Select All/Clear across apikeys create/edit, enterprise keys create/edit (`1006396`, `2d641d6`, `668d8e0`), group edit drawer type chips (`5253679`), round-robin/priority ordering panel for enterprise keys (`b3eaa2c`).

**SEO & anti-abuse** (09-13): `d20686a` AI-discoverability (llms.txt, structured data) + narrowed bot blocklist to named scrapers; `5d0e192`/`7b8e13a`/`ffe0e1a` CI/Lighthouse follow-ups (SEO gate skip for noindex pages, /models added to scan set). Three transient CI failures on 09-13 were all fixed same-day; last two runs green.

### ai-proxy-go (6 commits)

- `31be2dc`/`fb3a503` media models rejected on chat-completions (503 → explicit MEDIA_MODEL_WRONG_ENDPOINT with real endpoint paths) + embedded upstream stream-error detection. Revenue-relevant: prevents misrouted media calls from being billed as chat.
- `1f340c7` async-billing guard poisoning fix (a failed async charge could disable the async path).
- `d6ee56b` ABSK bearer support on the Bedrock runtime track (matches portal keypool changes).
- `72b4c2b` admin endpoint to reset key runtime penalty state.
- `16e180b` (this review) CI config schema fix.

All proxy commits are live on the proxy host (`ai-proxy-go` container Up 4 days, healthy).

## 3. Platform State (2026-09-14)

| Item | State |
|---|---|
| Portal | `ai-portal-go` Up 25h (healthy); last deploy includes `ffe0e1a` |
| Proxy | `ai-proxy-go` Up 4d (healthy), synced through `d6ee56b` + `16e180b` |
| CI portal | green at `ffe0e1a` |
| CI proxy | red ~1 month → fix pushed `16e180b`, run in progress at review time |
| Models | 411 active / 476 total (growth from additive auto-sync) |
| Users / keys | 18 users; 15 active keys (small封闭测试) |
| Upstream keys | 48 across 8 providers (incl. amazon-mantle-us-west-2) |
| Usage (7d) | 51 usage records — still pilot-scale |
| LZ claude | 12 models paused — reseller group 503 persists (re-tested 09-09, see HANDOVER) |

## 4. Findings & Recommendations

**P1 — proxy lint debt (newly visible).** 58 errcheck + 14 staticcheck findings in non-test code, concentrated in `pkg/convert`, `pkg/relay/channel/aws`, `pkg/keypool`, `internal/server`. None are behavior-verified bugs, but unchecked error paths around upstream I/O and billing guards deserve a dedicated cleanup PR before re-enabling the linters. List reproducible via `golangci-lint run ./...` with the four linters re-enabled.

**P2 — CI-as-gate discipline.** Proxy merged 6 commits over red fast-gate in one week; nobody noticed because lint failed at config validation, not at findings. Now that config verifies, the gate is meaningful again. Suggest: treat fast-gate red as blocking (already configured) + a weekly CI-health glance at both repos.

**P2 — HANDOVER discipline.** 09-10→09-13 work never reached HANDOVER.md. The deployment-relevant bits (management tokens, SEO middleware/blocklist, ABSK keypool) are operationally significant if a rollback or incident occurs mid-session. Added in this session.

**P3 — pricing rule coverage for paused models.** 40 non-active catalog rows have no pricing rule (incl. all 14 zimaocloud). Today harmless; on any unpause (e.g. laozhang claude when its reseller recovers) billing falls through to the Global fallback `*` rule. Add "rule exists" to the unpause checklist.

**P3 — upstream-provider concentration.** 2 laozhang keys + 30 TR keys carry most text-model traffic; LZ claude outage (reseller 503 since 09-08) shows single-group-of-reseller fragility. The liveness probe (runs via `scripts/probe-upstream-models`) should be scheduled (cron) rather than run ad hoc.

## 5. Verification Performed

- `golangci-lint v2.5.0 config verify` + `run ./...` locally on proxy (0 issues under corrected config; 72 issues with full linter set — recorded).
- `go build ./...` proxy OK.
- GitHub Actions API: portal last 5 runs (3 fail → 2 success on 09-13); proxy run history (all fail since 08-12) + failing-step log retrieved (`golangci-lint config verify`).
- Docker/container state on both hosts; DB counts via psql.
