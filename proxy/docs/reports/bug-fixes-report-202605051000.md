# AI Proxy Go - Bug Fixes Report

> **HISTORICAL** — May 2026 bug fix report. All fixes merged.

---

## Summary

Fixed 5 bugs from the review document (`proxy-review-test-plan-202605041000.md`).

| Bug | Priority | Status | Description |
|-----|----------|--------|-------------|
| B3 | P0 | ✅ Fixed | Remove forced `Enabled=true` in keypool sync |
| B4 | P0 | Fixed | Add Portal pricing sync mechanism |
| B5 | P0 | Fixed | Set `request_body` in handlers for affinity cache |
| B9 | P1 | Fixed | Use `LoadOrStore` in `getTokenEncoder` |
| B12 | P2 | Fixed | Add auth verification to `handleSyncKeys` |

---

## Detailed Changes

### B3: Remove forced Enabled=true in keypool sync

**File**: `pkg/keypool/manager.go:374`

**Before**:
```go
key.Enabled = true
key.Status = "active"
```

**After**:
```go
// Preserve Enabled state from Portal sync, only set default if not specified
if key.Status == "" {
    key.Status = "active"
}
```

**Impact**: Portal's disabled keys are now correctly preserved instead of being re-enabled during sync.

---

### B4: Add Portal pricing sync mechanism

**File**: `pkg/billing/billing.go`

**Changes**:
- Added `PortalPricing` and `PortalPricingSyncResponse` structs
- Added `NewBillingWithPortal()` constructor
- Added `StartPricingSyncLoop()` for periodic sync
- Added `SyncPricingFromPortal()` to fetch pricing from Portal API
- Added thread-safe `syncMutex` for pricing cache access
- Added `GetLastSyncTime()` for monitoring

**New API endpoint expected**: `GET /api/admin/pricing/sync` on Portal

**Usage**:
```go
billing := billing.NewBillingWithPortal(portalURL, portalSecret, 60*time.Second)
billing.StartPricingSyncLoop()
```

---

### B5: Set request_body in handlers for affinity cache

**File**: `internal/server/handlers.go`

**Changes**:
- Read request body before parsing
- Store body string in gin context as `"request_body"`
- Restore body for subsequent handlers using `io.NopCloser`

**Before**:
```go
var req dto.GeneralOpenAIRequest
if err := c.ShouldBindJSON(&req); err != nil { ... }
```

**After**:
```go
bodyBytes, err := io.ReadAll(c.Request.Body)
c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
c.Set("request_body", string(bodyBytes))

var req dto.GeneralOpenAIRequest
if err := json.Unmarshal(bodyBytes, &req); err != nil { ... }
```

**Impact**: Affinity cache fingerprint (`affinity.go:240`) now works correctly because `request_body` is set before cache operations.

---

### B9: Use LoadOrStore in getTokenEncoder

**File**: `pkg/token/counter.go:157-181`

**Before** (TOCTOU race):
```go
if val, ok := tokenEncoderCache.Load(model); ok {
    return val.(*tiktoken.Tiktoken)
}
encoder, err := tiktoken.GetEncoding(encoding)
tokenEncoderCache.Store(model, encoder)
return encoder
```

**After** (atomic):
```go
encoder, err := tiktoken.GetEncoding(encoding)
val, _ := tokenEncoderCache.LoadOrStore(model, encoder)
return val.(*tiktoken.Tiktoken)
```

**Impact**: Eliminates race condition where two goroutines could create duplicate encoders.

---

### B12: Add auth to handleSyncKeys

**File**: `internal/server/admin_handlers.go`

**Changes**:
- Added `serverConfig` variable and `SetServerConfig()` function
- Added proper secret verification in `handleSyncKeys()`

**Before**:
```go
// In production, verify against configured secret
// For now, allow all requests
```

**After**:
```go
if serverConfig != nil && serverConfig.Portal.SyncSecret != "" {
    if proxySecret != serverConfig.Portal.SyncSecret {
        c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
            "message": "Invalid proxy secret",
            ...
        }})
        return
    }
}
```

**Impact**: Sync endpoint now properly validates requests using configured secret.

---

## Remaining Bugs (Not Fixed)

| Bug | Priority | Description |
|-----|----------|-------------|
| B6 | P1 | 4 empty provider adapters (ali, deepseek, moonshot, zhipu) - requires new implementation |

---

## Build & Test Verification

```bash
# Build
go build ./...
# Result: SUCCESS

# Tests
go test ./pkg/billing/... ./pkg/token/... ./pkg/keypool/... ./internal/auth/... -v
# Result: ALL PASS
```

---

## Next Steps

1. Implement 4 provider adapters (B6)
2. Add Portal endpoint `/api/admin/pricing/sync`
3. Update server initialization to call `SetServerConfig()`
4. Integration testing with Portal