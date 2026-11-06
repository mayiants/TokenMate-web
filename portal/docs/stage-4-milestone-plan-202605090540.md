# Stage 4: First Release Milestone — Cross-Project TODO List

> **✅ COMPLETED** — All P0/P1 items resolved. Superseded by Stage 5/6.
> **Goal:** Resolve all P0 production blockers and high-priority P1 gaps to ship the first release candidate for both `ai-proxy-go` and `ai-portal-go`.
> **Last Updated:** 2026-05-09

---

## 🔴 P0 — Production Blockers (Must Fix Before Release)

| # | Project | Task | Status | Effort |
|---|---------|------|--------|--------|
| 1 | ai-proxy-go | Fix `loadFromRedis` error handling in `GetKeyByPriority` — missing `return nil` on Redis error causes nil-pool panic | ✅ Done | Small |
| 2 | ai-proxy-go | Enforce `allowed_models` restriction in all proxy handlers before forwarding to upstream | ✅ Done | Small |
| 3 | ai-proxy-go | Verify `pkg/models/validator.go` (`ModelValidator`) is wired and functional — **already implemented**, confirm no gaps | ✅ Verified | — |
| 4 | ai-portal-go | Add `/api/billing/callback` endpoint for payment provider return-redirect flows (complements existing async webhooks) | ✅ Done | Small |
| 5 | ai-portal-go | Verify GDPR endpoints completeness (`export`, `delete`, `status`) — **already implemented**, check for edge cases | ✅ Verified | — |

### P0 Summary
- **ai-proxy-go:** Core proxy is solid. Two code-level fixes needed (error handling + model enforcement). Validator already exists.
- **ai-portal-go:** Payment webhooks (Stripe/Alipay/WeChat) are complete. Need a generic return-redirect callback. GDPR endpoints exist and are functional.

---

## 🟠 P1 — High Priority (Feature Parity)

| # | Project | Task | Status | Effort |
|---|---------|------|--------|--------|
| 6 | ai-proxy-go | Populate `pkg/convert/` with format conversion helpers (Claude↔OpenAI, Gemini↔OpenAI) | ✅ Done | Medium |
| 7 | ai-proxy-go | Add server handler unit tests (`internal/server/*_test.go`) | ✅ Done | Medium |
| 8 | ai-proxy-go | Add rate limiter unit tests (`internal/ratelimit/*_test.go`) | ✅ Done | Small |
| 9 | ai-portal-go | Enterprise managed users: self-service endpoints (GET /enterprise, members, usage, topup, toggle) | ✅ Done | Medium |
| 10 | ai-portal-go | Dashboard analytics: consumption trend, model/provider distribution, user ranking | ✅ Verified (already implemented) | — |
| 11 | ai-portal-go | Usage export (CSV) and trace detail endpoints | ✅ Verified (already implemented) | — |

---

## 🟡 P2 — Medium Priority (Post-Release / Stretch)

| # | Project | Task | Status |
|---|---------|------|--------|
| 12 | ai-proxy-go | Multi-time-window rate limits (RP5H, RPW) | ✅ Done |
| 13 | ai-proxy-go | AWS streaming support | ✅ Done |
| 14 | ai-portal-go | Profile enhancements (change email, change phone, backup email) | ✅ Done (already implemented) |
| 15 | ai-portal-go | Service model approvals, pricing, aliases | ✅ Done |
| 16 | ai-portal-go | Channel health history | ✅ Done (already implemented) |
| 17 | ai-portal-go | Device management | ✅ Done |

---

## 📊 Overall Progress

| Project | Completion | P0 Done | P1 Done | P2 Done | Blockers |
|---------|-----------|---------|---------|---------|----------|
| ai-proxy-go | ~97% | 3/3 | 3/3 | 2/2 | None |
| ai-portal-go | ~95% | 2/2 | 3/3 | 4/4 | Portal model sync SQL error (non-blocking) |

**Path to Release Candidate:** All P0 blockers resolved, all P1 features complete, all P2 stretch goals done. Full test suite passing (ai-proxy-go: 16 packages, ai-portal-go: routes + crypto).
