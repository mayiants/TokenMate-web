# Group Rate Limit Design - Dual Limit Mechanism

> **ACTIVE** - 2026-05-12

## Overview

This document describes the dual rate limit mechanism that enforces rate limiting based on both **subscription limits** and **group limits**. The system uses the **stricter (more restrictive)** of the two limits to ensure proper resource allocation.

## Design Philosophy

### Two Types of Limits

| Type | Source | Purpose |
|------|--------|---------|
| **Subscription Limits** | `user_subscriptions` table | What the user paid for (product capability) |
| **Group Limits** | `user_groups` table | Billing rate and model capabilities |

### Why Both?

Users can choose different groups for different billing rates:

- **High subscription + Low-tier group**: User pays for high limits but wants slower billing (cheaper group)
- **Example**: Subscription allows 100 RPM, but user selects a group with 60 RPM → effective limit is 60 RPM

This design provides flexibility:
1. Subscription defines the **maximum capability** the user paid for
2. Group defines the **billing rate and working constraints**
3. System enforces the **stricter** (smaller) non-zero limit

## Rate Limit Calculation Logic

### Effective Rate Limit Formula

```
effective_limit = min(subscription_limit, group_limit)
```

**Special cases:**
- If both are 0: No limit (unlimited)
- If one is 0: Use the non-zero value
- If both are non-zero: Use the smaller value

### Implementation (Proxy)

```go
// Calculate effective RPM: use the stricter (smaller) non-zero limit
effectiveRPM := keyRPM  // Default from API key

if subRPM > 0 && (effectiveRPM <= 0 || subRPM < effectiveRPM) {
    effectiveRPM = subRPM
}
if groupRPM > 0 && (effectiveRPM <= 0 || groupRPM < effectiveRPM) {
    effectiveRPM = groupRPM
}
```

### Examples

| Scenario | Subscription RPM | Group RPM | Effective RPM |
|----------|------------------|-----------|---------------|
| No limits | 0 | 0 | Default (60) |
| Only subscription | 100 | 0 | 100 |
| Only group | 0 | 60 | 60 |
| Both set | 100 | 60 | 60 (stricter) |
| Both set (reverse) | 60 | 100 | 60 (stricter) |

## Data Flow

### 1. Portal: Save Group Rate Limits

**API**: `PUT /api/admin/groups/:id`

**Request Body:**
```json
{
  "name": "package - 2",
  "rpm": 60,
  "rph": 1200,
  "rp5h": 3000,
  "rpw": 16000,
  "rpmonth": 50000,
  "tpm": 0,
  "tph": 0,
  "tpd": 0,
  "tpw": 0,
  "tpmonth": 0
}
```

**Database**: `user_groups` table

### 2. Portal: Validate API Key

**API**: `GET /api/apikeys/validate`

**Response includes:**
```json
{
  "valid": true,
  "user_id": "...",
  "rate_limit_rpm": 60,
  "sub_rpm_limit": 100,
  "group_rpm": 60,
  "group_rph": 1200,
  "group_rp5h": 3000,
  "group_rpw": 16000,
  "group_rpmonth": 50000,
  "group_tpm": 0,
  "group_tph": 0,
  "group_tpd": 0,
  "group_tpw": 0,
  "group_tpmonth": 0
}
```

### 3. Proxy: Enforce Rate Limits

**Auth Middleware** (`internal/auth/auth.go`):
- Receives group rate limits from Portal validation response
- Stores in JWT claims and gin context

**Rate Limit Middleware** (`internal/ratelimit/ratelimit.go`):
- Reads both subscription and group limits from context
- Calculates effective limits using `min()` logic
- Enforces the stricter limit

## Database Schema

### user_groups Table

```sql
-- Rate limit fields
rpm         INTEGER DEFAULT 0  -- Requests per minute
rph         BIGINT DEFAULT 0   -- Requests per hour
rp5h        BIGINT DEFAULT 0   -- Requests per 5 hours
rpw         BIGINT DEFAULT 0   -- Requests per week
rpmonth     BIGINT DEFAULT 0   -- Requests per month
tpm         BIGINT DEFAULT 0   -- Tokens per minute
tph         BIGINT DEFAULT 0   -- Tokens per hour
tpd         BIGINT DEFAULT 0   -- Tokens per day
tpw         BIGINT DEFAULT 0   -- Tokens per week
tpmonth     BIGINT DEFAULT 0   -- Tokens per month
```

### user_subscriptions Table

```sql
-- Rate limit fields (same structure)
rpm_limit   INTEGER DEFAULT 0
rph_limit   INTEGER DEFAULT 0
rp5h_limit  INTEGER DEFAULT 0
rpw_limit   INTEGER DEFAULT 0
tpm_limit   INTEGER DEFAULT 0
tph_limit   INTEGER DEFAULT 0
tpd_limit   BIGINT DEFAULT 0
tpw_limit   BIGINT DEFAULT 0
```

## Admin Configuration

### Group Rate Limit Configuration

**Location**: Admin Panel → Groups → Edit Group

**UI Fields:**
- RPM (Requests Per Minute)
- RPH (Requests Per Hour)
- RP5H (Requests Per 5 Hours)
- RPW (Requests Per Week)
- RP/月 (Requests Per Month)
- TPM (Tokens Per Minute)
- TPH (Tokens Per Hour)
- TPD (Tokens Per Day)
- TPW (Tokens Per Week)
- TP/月 (Tokens Per Month)

**Note**: Leave empty or 0 for unlimited

### Subscription Rate Limit Configuration

**Location**: Admin Panel → Subscriptions → Edit Plan/User Subscription

**Same fields as group rate limits**

## Implementation Files

### Portal (ai-portal-go)

| File | Description |
|------|-------------|
| `internal/routes/groups.go` | Group CRUD with rate limit fields |
| `internal/routes/apikeys.go` | API key validation with group limits |
| `web/templates/admin-group-edit.html` | Admin UI for group rate limits |
| `web/templates/my-group.html` | User-facing group info display |

### Proxy (ai-proxy-go)

| File | Description |
|------|-------------|
| `internal/auth/auth.go` | JWT claims with group rate limits |
| `internal/ratelimit/ratelimit.go` | Dual limit enforcement logic |

## Testing

### Test Scenarios

1. **Only group limits set**
   - Set group RPM = 60, subscription RPM = 0
   - Expected: Effective RPM = 60

2. **Only subscription limits set**
   - Set group RPM = 0, subscription RPM = 100
   - Expected: Effective RPM = 100

3. **Both limits set (group stricter)**
   - Set group RPM = 60, subscription RPM = 100
   - Expected: Effective RPM = 60

4. **Both limits set (subscription stricter)**
   - Set group RPM = 100, subscription RPM = 60
   - Expected: Effective RPM = 60

5. **Both limits set to 0**
   - Set group RPM = 0, subscription RPM = 0
   - Expected: Effective RPM = default (60)

## Related Documents

- [Rate Limit Design](rate-limit-design-202605072130.md) - Original rate limit architecture
- [Subscription Plans Analysis](subscription-plans-analysis-202605072120.md) - Plan tier definitions
