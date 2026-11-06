> **DRAFT** - Debug issues found during Go Portal rewrite

# Go Portal Debug Issues - 20260503

## Overview

This document summarizes the issues found during the Go Portal rewrite debugging session.

---

## Issue 1: Frontend Field Name / Case / Format Mismatches

### Problem Description

The frontend (React) expects camelCase field names, but Go backend returns snake_case. This causes API integration failures.

### Critical Field Mismatches

| Frontend Expects | Go Portal Returns | Location | Impact |
|-----------------|-------------------|----------|--------|
| `keyPrefix` | `key_prefix` | KeyPoolPage:375 | Key details display fails |
| `keyPreview` | `key_preview` | admin.go:439 | - |
| `accessMode` | `access_mode` | ApiKeysPage:108 | Create Key fails |
| `rateLimitRpm` | `rate_limit_rpm` | ApiKeysPage:108 | Create Key fails |
| `allowedModels` | `allowed_models` | ApiKeysPage:113 | Model restriction fails |
| `totalRequests` | `total_requests` | Dashboard | Stats display wrong |
| `totalTokens` | `total_tokens` | Dashboard | Stats display wrong |
| `createdAt` | `created_at` | Multiple | Time display wrong |
| `apiModelName` | `api_model_name` | models | Model name display wrong |
| `showInStore` | `show_in_store` | models | Store display wrong |
| `inputPrice` | `input_price` | models | Price display wrong |
| `outputPrice` | `output_price` | models | Price display wrong |

### Frontend API Call Example

```javascript
// ApiKeysPage.jsx:108 - Frontend sends camelCase
const payload = {
  accessMode: newKeyAccessMode,      // Backend expects access_mode
  rateLimitRpm: newKeyRateLimit,     // Backend expects rate_limit_rpm
  allowedModels: newKeyModels,       // Backend expects allowed_models
};

// Go backend apikeys.go:82 expects snake_case
var req struct {
  AccessMode   string   `json:"access_mode"`     // Mismatch!
  Models       []string `json:"allowed_models"`  // Mismatch!
  RateLimitRPM int      `json:"rate_limit_rpm"`  // Mismatch!
}
```

### Fix Options

**Option A: Backend supports both formats (Recommended)**

```go
// apikeys.go - Add dual JSON tag support
var req struct {
    AccessMode   string   `json:"access_mode,accessMode"`
    Models       []string `json:"allowed_models,allowedModels"`
    RateLimitRPM int      `json:"rate_limit_rpm,rateLimitRpm"`
}
```

**Option B: Use camelCase consistently (frontend style)**

```go
// All structs use camelCase
var req struct {
    AccessMode   string   `json:"accessMode"`
    Models       []string `json:"allowedModels"`
    RateLimitRPM int      `json:"rateLimitRpm"`
}
```

### Affected Files

- `internal/routes/apikeys.go`
- `internal/routes/admin.go`
- `internal/routes/dashboard.go`
- `internal/routes/models.go`
- `internal/routes/auth.go`
- All route handlers that return JSON

---

## Issue 2: Routing + Endpoint Connection Problems

### Problem Description

Some frontend API calls don't match backend routes, or endpoints are missing entirely.

### Route Registration Issue

```go
// admin.go:33 - Incorrect router group usage
authGroup := rg.Use(r.auth.RequireAuth())  // Use() returns void, not RouterGroup!
authGroup.GET("/users", r.auth.RequireAdmin(), r.listUsers)

// Correct approach:
rg.Use(r.auth.RequireAuth())
rg.GET("/users", r.auth.RequireAdmin(), r.listUsers)
```

### Missing API Endpoints

| Frontend Call | Backend Status | Impact |
|--------------|----------------|--------|
| `/apikeys/:id/usage` | Missing | ApiKeysPage usage view fails |
| `/billing/balance` | Needs verification | Dashboard balance display |
| `/profile` | Needs verification | Settings page |
| `/admin/users/:id/status` | Missing | User management fails |

### Routes to Verify

1. `/api/admin/key-pool` - KeyPoolPage:28
2. `/api/admin/dashboard/stats` - Dashboard stats
3. `/api/admin/analytics` - Analytics page
4. `/api/billing/balance` - Balance display
5. `/api/profile` - User profile

### Fix Required

1. Fix router group registration in `admin.go`
2. Add missing endpoints
3. Verify all frontend API calls have corresponding backend routes

---

## Issue 3: Proxy and Portal Data Synchronization

### Problem Description

Upstream keys are not properly synchronized between Portal and Proxy.

### Root Cause: Encrypted Keys Not Decrypted

```go
// sync.go:121 - Key not decrypted before sending to Proxy
keys = append(keys, SyncKeyResponse{
    Key: keyEncrypted,  // WRONG: Sending encrypted key!
    // Should decrypt first
})
```

### Synchronization Flow Breakdown

```
Portal DB (key_encrypted)
    ↓
Portal syncKeyPool() - Returns encrypted key WRONG
    ↓
Proxy SyncFromPortal() - Receives encrypted key
    ↓
Proxy uses encrypted key to call upstream API FAILS!
```

### Proxy Expects Decrypted Keys

```go
// keypool/manager.go:273
keys[i] = Key{
    Key: sk.Key,  // Expects this to be the actual decrypted key
}
```

### Fix Required

```go
// sync.go - Add decryption logic
func (r *SyncRoutes) syncKeyPool(c *gin.Context) {
    // ... query keys ...

    for rows.Next() {
        var keyEncrypted string
        // ... scan ...

        // Decrypt key
        actualKey, err := decryptKey(keyEncrypted, r.cfg.EncryptionKey)
        if err != nil {
            r.logger.Warn("Failed to decrypt key", zap.Error(err))
            continue  // Skip keys that can't be decrypted
        }

        keys = append(keys, SyncKeyResponse{
            Key: actualKey,  // Return decrypted actual key
            // ...
        })
    }
}
```

### Sync Timing Issue

```go
// Proxy main.go:66 - Only syncs at startup and every 60 seconds
if cfg.Portal.URL != "" && cfg.Portal.SyncSecret != "" {
    keyPoolMgr.StartSyncLoop()  // 60 second interval
}
```

Portal doesn't notify Proxy when keys are added/modified. Must wait for next sync cycle.

### Recommended: Add Push Notification

```go
// Portal admin.go - Notify Proxy after adding key
func (r *AdminRoutes) addKeyPoolKey(c *gin.Context) {
    // ... create key ...

    // Notify all Proxies to re-sync
    go r.notifyProxiesToSync()
}

func (r *AdminRoutes) notifyProxiesToSync() {
    proxies := []string{"proxy1", "proxy2"}  // From config
    for _, proxy := range proxies {
        url := proxy + "/api/admin/keys/reload"
        req, _ := http.NewRequest("POST", url, nil)
        req.Header.Set("X-Admin-Secret", r.cfg.Portal.SyncSecret)
        http.DefaultClient.Do(req)
    }
}
```

---

## Priority Fix List

### P0 - Critical (Fix Immediately)

1. `sync.go:121` - Add key decryption logic
2. `admin.go:33` - Fix router group registration
3. `apikeys.go:82` - Support camelCase JSON fields

### P1 - High (Fix Soon)

4. Add `/apikeys/:id/usage` endpoint
5. Unify all API response field naming style
6. Add Proxy push notification for sync

### P2 - Medium (Fix Later)

7. Add API response DTO type definitions
8. Align frontend API types with backend DTOs
9. Add E2E tests for API compatibility

---

## Related Files

### Portal (ai-portal-go)

- `internal/routes/sync.go` - Key sync endpoint
- `internal/routes/admin.go` - Admin routes, key pool management
- `internal/routes/apikeys.go` - API key CRUD
- `internal/routes/dashboard.go` - Dashboard stats
- `internal/middleware/auth.go` - Authentication middleware

### Proxy (ai-proxy-go)

- `pkg/keypool/manager.go` - Key pool manager, sync logic
- `internal/server/handlers.go` - Request handlers
- `internal/server/server.go` - Route setup

### Frontend (ai-proxy-main/ai-proxy-portal)

- `src/api/index.js` - API client
- `src/pages/KeyPoolPage.jsx` - Key pool page
- `src/pages/ApiKeysPage.jsx` - API keys page
- `src/pages/DashboardPage.jsx` - Dashboard page

---

## Notes

- Node.js Portal used camelCase by default (JavaScript convention)
- Go convention is snake_case for JSON
- Need to decide on consistent naming strategy
- Consider using a translation layer if keeping snake_case in Go
