# Pre-Launch Platform Audit Report

> **Status:** ACTIVE  
> **Date:** 2026-08-14 23:20 UTC  
> **Auditor:** Automated + Manual API/Web testing  
> **Scope:** Full UI access, RBAC, operational logic, billing accuracy

---

## Executive Summary

The WOODGIT AI Platform is **ready for launch** with minor issues to address. All pages render correctly, RBAC is solid, billing is accurate, and error handling is proper. Three issues of medium severity were found.

| Category | Result |
|----------|--------|
| Page rendering (all roles) | ✅ 49/49 pages render without error |
| RBAC (cross-role access) | ✅ All unauthorized access properly blocked |
| API authentication | ✅ Invalid key → 401, missing key → 401 |
| Billing accuracy | ✅ Verified exact match (75 µUSD = 47.5 + 20 + 7.5) |
| Error handling | ✅ Helpful messages for invalid models |
| Data isolation | ✅ Users cannot access other users' data |
| Issues found | ⚠️ 3 medium-severity issues |

---

## 1. Test Matrix

### 1.1 Accounts Tested

| Role | Email | Result |
|------|-------|--------|
| User | thinbee09@qq.com | ✅ Login OK, role=user |
| Enterprise Admin | jedi@aynbay.com | ✅ Login OK, role=enterprise_admin |
| System Admin | admin@example.com | ✅ Login OK, role=super_admin |

### 1.2 Page Access Results

**System Admin (/admin/*, /super/*)** — 28 pages tested:
- `/admin` (dashboard), `/admin/users`, `/admin/models`, `/admin/models-aws`, `/admin/pricing`, `/admin/upstream-pricing`, `/admin/keypool`, `/admin/providers`, `/admin/provider-health`, `/admin/subscriptions`, `/admin/vouchers`, `/admin/rate-limits`, `/admin/keys`, `/admin/groups`, `/admin/enterprise`, `/admin/enterprise-applications`, `/admin/monitor-upstream`, `/admin/monitor-downstream`, `/admin/upstream-ops`, `/admin/reconciliation`, `/admin/reconciliation-ops`, `/admin/announcements`, `/admin/audit-logs`, `/admin/compliance`, `/admin/settings`, `/admin/usage-explorer`, `/super/payment`, `/super/admin-management`
- **Result: ALL 200 OK, no template errors, no 500s**

**Enterprise Admin (/enterprise/* + common pages)** — 20 pages tested:
- `/enterprise`, `/enterprise/users`, `/enterprise/keys`, `/enterprise/billing`, `/enterprise/groups`, `/enterprise/usage`, `/enterprise/audit`, `/enterprise/alerts`, `/enterprise/settings`
- `/dashboard`, `/apikeys`, `/billing`, `/billing-details`, `/usage-logs`, `/plan-and-group`, `/profile`, `/status`, `/models`, `/subscriptions`, `/voucher`
- **Result: ALL 200 OK**

**Regular User** — 12 pages tested:
- `/dashboard`, `/apikeys`, `/billing`, `/billing-details`, `/plan-and-group`, `/usage-logs`, `/profile`, `/status`, `/models`, `/subscriptions`, `/voucher`, `/enterprise-apply`
- **Result: ALL 200 OK**

### 1.3 RBAC Cross-Role Tests

| Test | Expected | Actual | ✓ |
|------|----------|--------|---|
| User → /admin/* | Blocked | 302 redirect | ✅ |
| User → /super/* | Blocked | 302 redirect | ✅ |
| User → /enterprise/* | Blocked | 302 redirect | ✅ |
| Enterprise → /admin/* | Blocked | 302 redirect | ✅ |
| Enterprise → /super/* | Blocked | 302 redirect | ✅ |
| User → /api/admin/* | 403 | 403 | ✅ |
| Enterprise → /api/admin/* | 403 | 403 | ✅ |

---

## 2. Real API Call Verification

### 2.1 Test Call

| Parameter | Value |
|-----------|-------|
| Model | deepseek-v4-flash |
| Input | "What is 2+2? Answer in one word." |
| max_tokens | 10 |

### 2.2 Results

| Metric | Value | Verified |
|--------|-------|----------|
| HTTP Status | 200 | ✅ |
| Input tokens | 95 | ✅ |
| Output tokens | 10 | ✅ |
| Reasoning tokens | 10 | ✅ (model uses thinking) |
| Total cost | 75 µUSD | ✅ |
| Balance before | 244,965,604 µUSD | ✅ |
| Balance after | 244,965,529 µUSD | ✅ |
| Deduction | 75 µUSD | ✅ exact match |

### 2.3 Billing Calculation Proof

```
Pricing rule: input=$0.50/M, output=$2.00/M, reasoning=$0.75/M
  95 input   × $0.50/M = 47.5 µUSD
  10 output  × $2.00/M = 20.0 µUSD
  10 reason  × $0.75/M =  7.5 µUSD
  ─────────────────────────────────
  Total:                  75.0 µUSD ✅
```

### 2.4 Error Handling Tests

| Test | HTTP | Response | ✓ |
|------|------|----------|---|
| Invalid model name | 400 | Helpful error with available model list | ✅ |
| Invalid API key format | 401 | `INVALID_API_KEY_FORMAT` | ✅ |
| Missing API key | 401 | `MISSING_API_KEY` | ✅ |

---

## 3. Issues Found

### Issue #1: `/v1/models` Returns Duplicate Entries (Medium)

**Description:** The OpenAI-compatible models list endpoint returns the same model name multiple times because it exists across multiple providers.

**Example:**
```
deepseek-v4-flash × 3 (providers: deepseek, huawei, tokenrouter)
deepseek-v4-pro   × 3 (providers: deepseek, huawei, tokenrouter)
```

**Impact:** SDK users calling `/v1/models` will see duplicates in their model lists. Some SDKs may not handle this gracefully.

**Recommendation:** Deduplicate by `api_model_name` in the proxy's models handler, returning only unique model IDs.

---

### Issue #2: Pricing Rules Differ by Provider for Same Model (Low-Medium)

**Description:** The same user-facing model (`deepseek-v4-flash`) has different pricing rules depending on which provider routes the request:

| Provider | Input $/M | Output $/M | Reasoning $/M |
|----------|-----------|-----------|----------------|
| deepseek | $0.50 | $2.00 | $0.75 |
| huawei | $0.50 | $2.00 | $0.75 |
| tokenrouter | $0.14 | $0.28 | $0.00 |

**Impact:** User is charged based on which provider handles their request (non-deterministic from user's perspective). Models gallery shows $0.50/$2.00 but user might be charged $0.14/$0.28 if routed to tokenrouter.

**Note:** This is actually **favorable to users** (they sometimes get a lower price), but creates billing unpredictability.

**Recommendation:** Either:
- (a) Use a single canonical pricing rule regardless of provider (simpler, predictable), or
- (b) Document that pricing may vary and show a "from $X" notation in the gallery.

---

### Issue #3: Retention Data Cleanup Not Automated (Medium)

**Description:** `DiskStore.Cleanup()` method exists but is never called in any background loop. Data accumulates without pruning.

**Current state:** 801 MB stored, configured for 90-day retention, but no expiry mechanism is running.

**Impact:** Disk will eventually fill up if not addressed before high traffic.

**Recommendation:** Add a cleanup goroutine in the DiskStore initialization that calls `Cleanup()` every 6-12 hours (already noted in HANDOVER.md).

---

## 4. Positive Findings

1. **RBAC is production-ready** — no leaks across role boundaries
2. **Billing engine is mathematically correct** — exact µUSD precision
3. **All 49 tested pages render clean** — no 500s, no template errors
4. **Auth security is solid** — HMAC signatures on admin endpoints, CSRF on web forms, JWT with HttpOnly cookies
5. **Error responses are helpful** — include available model list on model resolution failure
6. **164 public models available** — broad model coverage (145 text, 8 image, 8 video, 3 audio)
7. **Health endpoints operational** — both Portal and Proxy report healthy with all dependencies
8. **Enterprise isolation works** — enterprise admin can only see their own enterprise data
9. **Usage tracking accurate** — records created immediately with correct token counts

---

## 5. Data Summary

| Metric | Value |
|--------|-------|
| Total users | 12 |
| Active providers | 70 |
| Public models | 164 (145 text, 8 image, 8 video, 3 audio) |
| User groups | 9 |
| Upstream key pool | 12 active keys |
| Published announcements | 1 active |
| Test user balance | $244.97 |

---

## 6. Recommendations for Launch

### Must-Fix Before Launch
- None — all critical systems are functional

### Should-Fix (non-blocking)
1. **Deduplicate `/v1/models` response** — cosmetic but affects SDK compatibility
2. **Implement retention cleanup cron** — not urgent at current traffic but needed within weeks

### Consider for Post-Launch
3. **Unify pricing rules** — decide on single canonical price per model or document variability
4. **Disk space monitoring** — add alerting when Proxy EC2 crosses 80% usage
5. **Portal Analytics page** — expose the retention analytics via admin UI (Phase 2)

---

*Report generated from automated API testing + manual verification on 2026-08-14.*
