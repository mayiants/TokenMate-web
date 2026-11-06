# Code Review Report: ai-portal-go & ai-proxy-go

> **Status:** ACTIVE
> **Created:** 2026-05-03
> **Review Date:** 2026-05-03
> **Reviewers:** Engineering, QA, Security

---

## 1. Executive Summary

This report documents the comprehensive code review of two Go projects: **ai-portal-go** (Admin Portal) and **ai-proxy-go** (AI API Gateway). The review covered engineering quality, testability, and security across both codebases.

### Findings Overview

| Category | Critical | Major | Minor |
|----------|----------|-------|-------|
| Engineering | 2 | 5 | 4 |
| QA | 1 | 6 | 4 |
| Security | 6 | 7 | 4 |
| **Total** | **9** | **18** | **12** |

---

## 2. Engineering Review

### 2.1 Critical Issues

#### Issue E-1: HTTP Client Created Per Request (No Pooling)
**Severity:** Critical
**File:** `ai-proxy-go/pkg/relay/base/adaptor.go:101-103`

```go
func (a *BaseAdaptor) DoRequest(...) (*http.Response, error) {
    client := &http.Client{Timeout: 120 * time.Second}  // New client per request!
    resp, err := client.Do(req)
}
```

**Impact:** Creates new TCP connection for every upstream request. No connection pooling utilization.

**Recommendation:** Inject shared `*http.Client` via `AdaptorConfig` and reuse.

---

#### Issue E-2: Hardcoded Admin Credentials
**Severity:** Critical
**File:** `ai-portal-go/cmd/server/main.go:353-354`

```go
if adminPassword == "" {
    adminPassword = "<ADMIN_PASSWORD>"
}
```

**Impact:** If env vars not set, default credentials are knowable by anyone.

**Recommendation:** Require admin password via env or fail startup.

---

### 2.2 Major Issues

| ID | Issue | File | Line |
|----|-------|------|------|
| E-3 | Admin secret default "admin-secret" | `ai-proxy-go/internal/config/config.go` | 292 |
| E-4 | Redis client not closed on shutdown | `ai-proxy-go/cmd/server/main.go` | 81-91 |
| E-5 | Silent error ignores (io.ReadAll) | `ai-proxy-go/internal/server/handlers.go` | 543 |
| E-6 | API keys stored unencrypted in DB | `ai-portal-go/internal/routes/admin.go` | 205-206 |
| E-7 | No request body validation for affinity cache | `ai-proxy-go/internal/server/handlers.go` | - |

---

### 2.3 Minor Issues

| ID | Issue | File |
|----|-------|------|
| E-8 | Global mutable state via `SetHandlerDependencies()` pattern | `ai-proxy-go/internal/server/handlers.go` |
| E-9 | Cleanup goroutines never stop | `ai-proxy-go/internal/auth/auth.go:302` |
| E-10 | SQL DDL scattered in main.go instead of migrations | `ai-portal-go/cmd/server/main.go` |
| E-11 | No request ID/correlation ID tracking | Both projects |

---

## 3. QA Review

### 3.1 Critical Issues

#### Issue Q-1: Zero Test Coverage (ai-portal-go)
**Severity:** Critical
**Location:** `ai-portal-go/` - No `*_test.go` files found

**Impact:** No unit or integration tests exist for Portal codebase.

**Recommendation:** Add comprehensive test suite covering routes, middleware, and database operations.

---

### 3.2 Major Issues

| ID | Issue | File |
|----|-------|------|
| Q-2 | Global state blocks testing | `ai-proxy-go/internal/server/handlers.go` |
| Q-3 | No interfaces for external services | Throughout both projects |
| Q-4 | Model not validated per-user | `ai-proxy-go/internal/server/handlers.go:104` |
| Q-5 | No metrics endpoint | `ai-portal-go` |
| Q-6 | No brute force protection on login | `ai-portal-go/internal/routes/auth.go` |
| Q-7 | Auth middleware has static coupling | `ai-portal-go/internal/middleware/auth.go` |

---

### 3.3 Observability Status

| Endpoint | ai-proxy-go | ai-portal-go |
|----------|-------------|--------------|
| Health Check | `/health` | `/status` |
| Metrics | Returns dummy data | Not implemented |
| Tracing | Not implemented | Not implemented |

---

## 4. Security Review

### 4.1 Critical Issues

#### Issue S-1: Hardcoded Credentials in Environment File
**Severity:** Critical
**File:** `ai-portal-go/.env.go-platform`

```bash
DB_PASSWORD=<DB_PASSWORD_HASH>
REDIS_PASSWORD=<REDIS_PASSWORD_HASH>
JWT_SECRET=<JWT_SECRET>
ADMIN_PASSWORD=<ADMIN_PASSWORD>
PROXY_SYNC_SECRET=<ENCRYPTION_KEY>
```

**Impact:** All secrets committed to source control in plaintext.

**Recommendation:** Use environment variables or secrets manager (Vault, AWS Secrets Manager).

---

#### Issue S-2: Default Admin Secret Hardcoded
**Severity:** Critical
**File:** `ai-proxy-go/internal/config/config.go:292`

```go
v.SetDefault("admin.secret", "admin-secret")
```

**Impact:** If admin secret not configured, default "admin-secret" allows trivial admin access.

**Recommendation:** Require strong secret configuration, fail startup if not set.

---

#### Issue S-3: ✅ RESOLVED — AES-256-GCM Encryption Implemented
**Severity:** Critical (resolved)
**File:** `ai-portal-go/internal/routes/admin.go` → now `internal/web/admin_handlers.go:248-270`

**Original issue:** Upstream API keys stored in plaintext. Any database compromise exposes all provider keys.

**Resolution:** AES-256-GCM encryption implemented in `pkg/crypto/encryption.go`. Keys are encrypted before storage and decrypted on sync to Proxy. Production mode (`ADMIN_MODE=production`) requires `ENCRYPTION_KEY` (32+ chars) and refuses to store keys without it.

---

### 4.2 Major Issues

| ID | Issue | File | Line |
|----|-------|------|------|
| S-4 | AdminHandler signature bypass (empty header) | `ai-proxy-go/internal/auth/auth.go` | 196-242 |
| S-5 | API key exposed in URL query parameter | `ai-proxy-go/internal/auth/auth.go` | 390-394 |
| S-6 | Insecure HTTP default (no TLS) | `ai-proxy-go/internal/config/config.go` | 262 |
| S-7 | No TLS verification in HTTP clients | `ai-proxy-go/pkg/keypool/manager.go` | 248-249 |
| S-8 | Local rate limiting bypass via scaling | `ai-proxy-go/internal/ratelimit/ratelimit.go` | 185-201 |
| S-9 | Redis sync failure "fail open" | `ai-proxy-go/internal/ratelimit/ratelimit.go` | 276-281 |
| S-10 | No rate limiting on auth endpoints | `ai-portal-go/internal/routes/auth.go` | 30-40 |

---

### 4.3 Minor Issues

| ID | Issue | File |
|----|-------|------|
| S-11 | No email format validation on registration | `ai-portal-go/internal/routes/auth.go` |
| S-12 | Weak verification code (6-char UUID) | `ai-portal-go/internal/routes/auth.go:275-277` |
| S-13 | sync.Map usage without atomic check | `ai-proxy-go/internal/ratelimit/ratelimit.go` |
| S-14 | Global state for handler dependencies | `ai-proxy-go/internal/server/handlers.go` |

---

## 5. Priority Recommendations

### Immediate (Must Fix)

1. Remove hardcoded secrets from `.env.go-platform`
2. Require admin secret configuration (fail if not set)
3. Implement AES-256-GCM encryption for stored upstream keys
4. Fix AdminHandler empty signature bypass
5. Fix HTTP client pooling in `pkg/relay/base/adaptor.go`

### High Priority

6. Remove API key support from query parameters
7. Implement Redis-authoritative rate limiting (fail closed)
8. Add rate limiting to Portal auth endpoints
9. Add test coverage to ai-portal-go
10. Enforce HTTPS/TLS for all network communication

### Medium Priority

11. Upgrade Gin to latest version (DoS fixes)
12. Add Prometheus metrics endpoint
13. Replace UUID-based verification codes with crypto/rand
14. Implement OpenTelemetry tracing
15. Refactor global state to injectable dependencies

---

## 6. Files Requiring Attention

| File | Issues |
|------|--------|
| `ai-portal-go/.env.go-platform` | S-1 |
| `ai-proxy-go/internal/config/config.go` | S-2, E-3 |
| `ai-proxy-go/internal/auth/auth.go` | S-4, S-5 |
| `ai-proxy-go/pkg/relay/base/adaptor.go` | E-1 |
| `ai-portal-go/internal/routes/admin.go` | S-3, E-6 |
| `ai-proxy-go/internal/ratelimit/ratelimit.go` | S-8, S-9 |
| `ai-portal-go/internal/routes/auth.go` | S-10, S-11, S-12 |
| `ai-portal-go/cmd/server/main.go` | E-2 |
| `ai-proxy-go/cmd/server/main.go` | E-4 |

---

## 7. Dependency Status

| Dependency | Version | Status |
|------------|---------|--------|
| github.com/gin-gonic/gin | v1.9.1 | Consider upgrading (DoS fixes) |
| github.com/golang-jwt/jwt/v5 | v5.2.1 | Current |
| github.com/redis/go-redis/v9 | v9.5.1 | Current |
| github.com/jackc/pgx/v5 | v5.5.0 | Current |

---

*Report generated: 2026-05-03*