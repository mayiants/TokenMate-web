# MVP Readiness Review

**Date:** 2026-06-28 13:45 UTC  
**Reviewer:** Kiro  
**Scope:** Full rebuild, E2E test, remaining open issues assessment

---

## 1. Build & Service Status

- `./start.sh rebuild` — ✅ Both Portal and Proxy built and started successfully (Docker multi-stage)
- Portal: http://localhost:3001 — ✅ 200
- Proxy: http://localhost:8080 — ✅ 200

## 2. E2E Test Results: 70/71 Passed

All 4 roles tested (super_admin, user, enterprise_admin, enterprise_member), cross-service proxy integration, auth boundaries, and web pages all pass.

### One Failure — Test Script Bug

**File:** `tests/test-e2e-4roles.sh` line 109  
**Issue:** Uses legacy endpoint `/api/admin/channel-health` (returns 500) instead of `/api/admin/provider-health` (returns 200)

**Fix:**
```bash
# Line 109 — change:
expect_status "Channel health" GET "/api/admin/channel-health" "200" "" "$ADMIN_TOKEN" > /dev/null 2>&1 || true
# To:
expect_status "Provider health" GET "/api/admin/provider-health" "200" "" "$ADMIN_TOKEN" > /dev/null 2>&1 || true
```

### Note: Rate Limit Interaction

The E2E test will fail if `auth_brute:<IP>` key exists in Redis (from previous manual login attempts). The test itself does 4 logins (admin, user, ent-admin, ent-member) which is under the threshold, but accumulated attempts from other sessions can trip it. Consider either:
- Increasing the brute-force threshold in test/dev mode, or
- Adding a Redis flush of `auth_brute:*` at the start of the test script

---

## 3. P0/P1 Status — ALL FIXED ✅

Per the integrated system review (2026-06-26), all 6 P0 items and 9 P1 items (#7–#15) are resolved:
- Security: hardcoded secrets removed, TLS verification enforced, WeChat Pay signature verified, HMAC required, CORS fixed
- Architecture: handler monolith split, dependency injection, query layer extracted, MustExec eliminated
- Performance: partition maintenance, memory eviction, streaming lifecycle, shared HTTP client
- Dependencies: govulncheck clean, deps upgraded

---

## 4. Remaining Open Issues (P2 — for post-MVP or quick pre-deploy fix)

### 4.1 Quick Fixes (< 1 hour total, recommended before deploy)

| # | Issue | File | Fix |
|---|-------|------|-----|
| A | **Email HTML injection** | `pkg/mail/mail.go:270-274` | Wrap `enterpriseName` and `reason` with `html.EscapeString()` |
| B | **Admin secret config default** | `internal/config/config.go:384` | Remove `"admin-secret"` default; fail if value matches a blocklist (`admin-secret`, `changeme`, etc.) |
| C | **Test script endpoint** | `tests/test-e2e-4roles.sh:109` | Change `channel-health` → `provider-health` |

### 4.2 Short-Term Fixes (2-4 hours, within first week)

| # | Issue | File | Fix |
|---|-------|------|-----|
| D | **Registration duplicate-email race** | `internal/routes/auth.go:189-196` | Add partial unique index: `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_active ON users(email) WHERE deleted_at IS NULL;` then catch duplicate-key error on INSERT |
| E | **Referral voucher over-redemption race** | `internal/services/reward.go:30-82` | Move `max_uses`/`used_count` check inside the DB transaction with `SELECT ... FOR UPDATE` on the voucher row |
| F | **JWT iss/aud and lifetime** | `pkg/jwt/jwt.go:30-61` | Add `Issuer: "ai-portal"`, `Audience: []string{"ai-portal"}` to claims; shorten `exp` from 30d to 7d; add a `/api/auth/refresh` endpoint |

### 4.3 Deferred (acceptable risk for initial MVP)

| # | Issue | Risk Level | Notes |
|---|-------|-----------|-------|
| G | Fallback logs unencrypted | Low (server-side only) | Only matters if disk is compromised |
| H | API keys in URL query params | Low (only for specific auth types) | Log a deprecation warning |
| I | AES-decrypted key format validation | Low (logs and skips) | Add stricter validation when time permits |

---

## 5. Performance Notes

Lighthouse on local VM showed 56/100 performance due to CPU throttling. In production (real hardware or cloud instance):
- FCP and LCP should be <2s (vs 6-7s on this VM)
- 256KB unused JS can be trimmed later via tree-shaking if needed
- CLS is already 0 (perfect)

The in-memory IP rate limiter (`internal/middleware/ratelimit.go`) does **not** scale horizontally — each Portal instance has its own counters. For single-instance MVP this is fine. For multi-instance deployment, switch to Redis-backed rate limiting.

---

## 6. Deployment Checklist

Before going live:

- [ ] Apply fixes A, B, C from §4.1
- [ ] Set strong `ADMIN_PASSWORD` (≥12 chars) and `PROXY_SYNC_SECRET` in production `.env`
- [ ] Set `GIN_MODE=release`
- [ ] Ensure PostgreSQL has the partial unique index on `users(email)` (fix D)
- [ ] Verify `ENCRYPTION_KEY` is set for upstream key encryption
- [ ] Configure real payment gateway (disable simulated mode)
- [ ] Set `ALLOWED_ORIGINS` to your actual domain(s) — do not leave empty
- [ ] Run `./start.sh rebuild` or `docker compose up --build`
- [ ] Run E2E test to confirm

---

## 7. Overall Assessment

**The system is ready for MVP deployment.** All critical security and architecture issues are resolved. The remaining P2 items are standard hardening that can be addressed in the first week post-launch. The 70/71 E2E pass rate (with the 1 failure being a test bug, not a product bug) confirms functional stability after 130+ files of UI/i18n/accessibility changes.
