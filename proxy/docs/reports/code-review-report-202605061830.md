> **HISTORICAL** — May 2026 code review. All findings addressed.

# Code Review Summary - ai-proxy-go & ai-portal-go

**Date:** 2026-05-06
**Reviewer:** Claude Code Review

---

## Critical Issues

### 1. SSE Stream Chunk Handling Bug
**Location:** `ai-proxy-go/internal/server/handlers.go:472-485`

```go
if adaptor.GetRelayFormat() != "openai" {
    transformed := adaptor.TransformStreamChunk([]byte(data), chunkIndex)
    if transformed != nil {
        if streamResp, ok := transformed.(*dto.ChatCompletionsStreamResponse); ok {
            chunkData, _ = json.Marshal(streamResp)
        }
    }
} else {
    chunkData = []byte(data)
}
// BUG: If transform returned nil or type assertion failed, chunkData is empty!
```

**Impact:** When transformation fails, `chunkData` remains empty but code still extracts usage at lines 488-494. Token counting becomes incorrect, leading to billing errors.

**Fix:** Add `else` branch to handle nil/assertion failure, or skip chunk processing.

---

### 2. HTTP Status Code Inconsistency
**Location:** `ai-proxy-go/internal/server/handlers.go:320-341`

Upstream 4xx errors return `StatusBadGateway (400)` but should preserve original upstream status:
- 4xx client errors → `400 Bad Request`
- 5xx server errors → `502 Bad Gateway`

**Fix:** Check `resp.StatusCode` range and return appropriate status.

---

### 3. Balance Check Logic Flaw
**Location:** `ai-portal-go/internal/routes/apikeys.go:396-407`

```go
if balance <= 0 {
    c.JSON(http.StatusOK, gin.H{"valid": false, ...})
    return  // Returns HTTP 200 with valid=false
}
```

Auth middleware only recognizes non-200 status codes. Zero balance returns HTTP 200, so Proxy allows key to pass validation. Balance check fails only later during chat completions.

**Fix:** Return non-200 status when balance is exhausted (e.g., `402 Payment Required`).

---

## Design Issues

### 4. No-Config Fallback Bypasses Security
**Location:** `ai-proxy-go/internal/auth/auth.go:448-461`

```go
if m.cfg.Portal.URL == "" {
    claims := &Claims{
        UserID:     "test-user",
        AccessMode: "auto_balance",  // bypasses all limits!
    }
}
```

Dev mode gives all API keys unlimited access with `auto_balance` mode. Balance/quota checks become meaningless.

**Fix:** Require explicit config flag for dev mode, or use restricted test claims.

---

### 5. Nonce Replay Prevention is Optional
**Location:** `ai-proxy-go/internal/auth/auth.go:278-283`

Replay attack prevention only works when `signature` header is provided. Without it, nonce check is skipped entirely.

**Fix:** Enforce nonce presence for admin endpoints.

---

### 6. Cache Write Cost Edge Case
**Location:** `ai-proxy-go/pkg/billing/billing.go:127-130`

```go
if cacheCreation5m > 0 && pricing.CacheCreation5mRatio > 0 {
    cacheWriteCost = float64(cacheCreation5m)/1e6*pricing.ModelRatio*pricing.CacheCreation5mRatio +
        float64(cacheCreation1h)/1e6*pricing.ModelRatio*pricing.CacheCreation1hRatio
}
```

If `CacheCreation1hRatio` is 0 but `cacheCreation1h > 0`, that portion is free. May be intentional but can cause billing discrepancies.

**Fix:** Warn or default to `CacheCreationRatio` when specific ratio is missing.

---

## Minor Issues

### 7. listKeys Silently Ignores Scan Errors
**Location:** `ai-portal-go/internal/routes/apikeys.go:58-70`

```go
for rows.Next() {
    _ = rows.Scan(...)  // Error ignored!
}
```

Incomplete key data returned with zero values when Scan fails.

**Fix:** Log or aggregate scan errors.

---

### 8. Request Body Duplicated in Memory
**Location:** `ai-proxy-go/internal/server/handlers.go:131-134`

```go
c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
c.Set("request_body", string(bodyBytes))  // Stored twice
```

Increases memory usage for large requests.

**Fix:** Use one source of truth.

---

### 9. Cleanup Goroutine Has No Shutdown
**Location:** `ai-proxy-go/internal/auth/auth.go:85-87`

```go
go m.cleanupLoop()  // No stop channel, runs forever
```

Minor goroutine leak on shutdown.

**Fix:** Add stop channel to cleanupLoop.

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 3 |
| Design   | 3 |
| Minor    | 3 |

**Recommendation:** Fix critical issues before deployment. Issue #1 (stream chunk handling) can cause silent billing failures. Issue #3 (balance check) allows zero-balance keys to pass validation temporarily.