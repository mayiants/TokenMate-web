# Node.js vs Go Portal Gap Analysis Report

**Date**: 2026-05-05 10:30  
**Status**: ACTIVE  
**Author**: Gap Analysis Tool

---

## 1. Executive Summary

This report compares the Node.js Portal implementation with the Go Portal implementation to identify gaps and missing features. The Go implementation has made significant progress but is missing several key features present in the Node.js version.

**Overall Coverage**: ~75% of Node.js features implemented in Go

### Critical Missing Features
1. **GDPR endpoints** - Not implemented in Go
2. **Devices management** - Not implemented in Go
3. **Pricing management** - Partially implemented (missing sources, comparison, history)
4. **Channel health history** - Missing history table and detailed tracking
5. **Key pool sync endpoint** - Missing proxy key sync functionality

---

## 2. Missing API Endpoints

### 2.1 Completely Missing Routes

| Route | Node.js Path | Go Status | Priority |
|-------|-------------|-----------|----------|
| GDPR Export | `/api/gdpr/export` | ❌ Missing | High |
| GDPR Delete | `/api/gdpr/delete` | ❌ Missing | High |
| GDPR Status | `/api/gdpr/status` | ❌ Missing | High |
| Devices List | `/api/devices` | ❌ Missing | Medium |
| Devices Record | `/api/devices` (POST) | ❌ Missing | Medium |
| Devices Delete | `/api/devices/:id` | ❌ Missing | Medium |
| Key Pool Sync | `/api/admin/key-pool/sync` | ❌ Missing | High |
| Key Pool Assign | `/api/admin/key-pool/:keyId/assign` | ❌ Missing | Medium |
| Proxies List | `/api/admin/proxies` | ❌ Missing | Low |

### 2.2 Partially Implemented Routes

| Route | Node.js Path | Go Status | Missing Features |
|-------|-------------|-----------|------------------|
| Pricing Sources | `/api/admin/pricing/sources` | ❌ Missing | Full endpoint missing |
| Pricing Compare | `/api/admin/pricing/compare` | ❌ Missing | Full endpoint missing |
| Pricing Confirm | `/api/admin/pricing/confirm` | ❌ Missing | Full endpoint missing |
| Pricing History | `/api/admin/pricing/:model_id/history` | ❌ Missing | Full endpoint missing |
| Pricing Fetch | `/api/admin/pricing/fetch` | ❌ Missing | Full endpoint missing |
| Usage Export | `/api/billing/usage-records/export` | ❌ Missing | Export functionality |
| Usage Detail | `/api/billing/usage-records/:trace_id/detail` | ❌ Missing | Detailed breakdown |
| Profile Change Email | `/api/profile/change-email` | ❌ Missing | Email change flow |
| Profile Change Phone | `/api/profile/change-phone` | ❌ Missing | Phone change flow |
| Profile Send Change Code | `/api/profile/send-change-code` | ❌ Missing | Verification code |
| Profile Backup Email | `/api/profile/send-backup-email-code` | ❌ Missing | Backup email flow |

### 2.3 Admin Analytics Differences

| Feature | Node.js | Go | Gap |
|---------|---------|-----|-----|
| `/api/admin/analytics` | ✅ Full stats | ⚠️ Basic | Missing detailed stats |
| `/api/admin/stats` | ✅ Implemented | ✅ Implemented | - |
| `/api/admin/models/status` | ✅ Implemented | ✅ Implemented | - |
| Usage by user-keys | ✅ `/api/admin/usage/user-keys` | ✅ Implemented | - |
| Usage by upstream-keys | ✅ `/api/admin/usage/upstream-keys` | ⚠️ Partial | Missing some fields |

---

## 3. Missing Database Tables/Fields

### 3.1 Missing Tables

| Table | Node.js | Go | Purpose |
|-------|---------|-----|---------|
| `user_devices` | ✅ | ❌ | Device management |
| `pricing_sources` | ✅ | ❌ | Pricing data collection |
| `model_pricing` | ✅ | ❌ | Confirmed pricing |
| `pricing_history` | ✅ | ❌ | Price change tracking |
| `channel_health_history` | ✅ | ❌ | Channel health tracking |
| `sensitive_check_logs` | ✅ | ❌ | Sensitive word detection logs |
| `openrouter_models` | ✅ | ✅ | OpenRouter sync data |

### 3.2 Missing Columns in Existing Tables

#### `users` table
| Column | Node.js | Go | Purpose |
|--------|---------|-----|---------|
| `shipping_address` | ✅ JSONB | ❌ | Shipping info |
| `invoice_info` | ✅ JSONB | ❌ | Invoice details |
| `backup_email` | ✅ VARCHAR | ❌ | Backup email |
| `managed_by` | ✅ UUID | ❌ | Enterprise admin relation |
| `enterprise_config` | ✅ JSONB | ❌ | Enterprise configuration |
| `group_id` | ✅ UUID | ❌ | User group relation |
| `privacy_region` | ✅ VARCHAR | ❌ | GDPR region |
| `data_retention_days` | ✅ INTEGER | ❌ | Data retention |
| `consent_analytics` | ✅ BOOLEAN | ❌ | Analytics consent |
| `consent_model_training` | ✅ BOOLEAN | ❌ | Training consent |
| `gdpr_data_exported_at` | ✅ TIMESTAMP | ❌ | GDPR export tracking |
| `gdpr_erasure_requested_at` | ✅ TIMESTAMP | ❌ | GDPR deletion tracking |

#### `api_keys` table
| Column | Node.js | Go | Purpose |
|--------|---------|-----|---------|
| `model_binding` | ✅ VARCHAR | ❌ | Model binding |
| `total_requests` | ✅ INTEGER | ✅ | Request count |
| `total_tokens` | ✅ BIGINT | ✅ | Token count |

#### `upstream_keys` table
| Column | Node.js | Go | Purpose |
|--------|---------|-----|---------|
| `metadata` | ✅ JSONB | ❌ | Additional metadata |
| `allowed_regions` | ✅ JSONB | ❌ | Region restrictions |
| `last_synced_at` | ✅ TIMESTAMP | ❌ | Last sync time |

#### `service_models` table
| Column | Node.js | Go | Purpose |
|--------|---------|-----|---------|
| `aimodels_input_price` | ✅ DECIMAL | ❌ | Original price tracking |
| `aimodels_output_price` | ✅ DECIMAL | ❌ | Original price tracking |
| `aimodels_data` | ✅ JSONB | ✅ | Model metadata |
| `aimodels_synced_at` | ✅ TIMESTAMP | ❌ | Sync timestamp |
| `has_changes` | ✅ BOOLEAN | ❌ | Change detection |
| `change_details` | ✅ JSONB | ❌ | Change details |
| `provider_eol_at` | ✅ TIMESTAMP | ❌ | Provider EOL |
| `service_eol_at` | ✅ TIMESTAMP | ❌ | Service EOL |
| `purge_at` | ✅ TIMESTAMP | ❌ | Purge schedule |
| `discontinued_at` | ✅ TIMESTAMP | ❌ | Discontinue time |
| `discontinued_reason` | ✅ VARCHAR | ❌ | Discontinue reason |
| `discontinued_notes` | ✅ TEXT | ❌ | Discontinue notes |
| `ignored_at` | ✅ TIMESTAMP | ❌ | Ignore time |
| `ignored_reason` | ✅ VARCHAR | ❌ | Ignore reason |
| `paused_at` | ✅ TIMESTAMP | ❌ | Pause time |
| `paused_reason` | ✅ VARCHAR | ❌ | Pause reason |
| `paused_notes` | ✅ TEXT | ❌ | Pause notes |
| `confirmed_by` | ✅ UUID | ❌ | Who confirmed |
| `confirmed_at` | ✅ TIMESTAMP | ❌ | Confirm time |
| `eol_confirmed_by` | ✅ UUID | ❌ | EOL confirmer |
| `eol_confirmed_at` | ✅ TIMESTAMP | ❌ | EOL confirm time |
| `eol_notes` | ✅ TEXT | ❌ | EOL notes |
| `provider_aliases` | ✅ JSONB | ❌ | Provider aliases |
| `input_price_cache_hit` | ✅ DECIMAL | ❌ | Cache hit pricing |
| `input_price_cache_miss` | ✅ DECIMAL | ❌ | Cache miss pricing |
| `validation_status` | ✅ VARCHAR | ❌ | Validation status |
| `validation_result` | ✅ JSONB | ❌ | Validation result |
| `validated_at` | ✅ TIMESTAMP | ❌ | Validation time |
| `suggested_api_name` | ✅ VARCHAR | ❌ | Suggested name |

---

## 4. Missing Middleware Features

### 4.1 Auth Middleware Differences

| Feature | Node.js | Go | Gap |
|---------|---------|-----|-----|
| `requireAuth` | ✅ | ✅ | - |
| `requireAdmin` | ✅ | ✅ | - |
| `requireSuperAdmin` | ✅ | ✅ | - |
| `requireSensitiveOps` | ✅ | ❌ | Missing in Go |
| `requireEnterpriseAdmin` | ✅ | ❌ | Missing in Go |
| `requireRoleLevel` | ✅ | ❌ | Missing in Go |
| `optionalAuth` | ✅ | ❌ | Missing in Go |
| `canPerformSensitiveOps` | ✅ | ✅ | - |
| `isMvpMode` | ✅ | ✅ | - |
| Admin limit functions | ✅ | ✅ | - |
| `ROLE_HIERARCHY` | ✅ | ❌ | Not exported |

---

## 5. Missing Business Logic Features

### 5.1 Billing Features

| Feature | Node.js | Go | Priority |
|---------|---------|-----|----------|
| Balance retrieval | ✅ | ✅ | - |
| Recharge creation | ✅ | ✅ | - |
| Usage details | ✅ | ✅ | - |
| Usage summary | ✅ | ✅ | - |
| Usage by key | ✅ | ✅ | - |
| Usage by model | ✅ | ✅ | - |
| Usage records with pagination | ✅ | ✅ | - |
| Usage records export (CSV) | ✅ | ❌ | Medium |
| Usage detail breakdown | ✅ | ❌ | Medium |
| Payment callback | ✅ | ❌ | High |

### 5.2 Subscription Features

| Feature | Node.js | Go | Priority |
|---------|---------|-----|----------|
| List plans | ✅ | ✅ | - |
| Create plan | ✅ | ✅ | - |
| Update plan | ✅ | ✅ | - |
| Delete plan | ✅ | ✅ | - |
| List user subscriptions | ✅ | ✅ | - |
| Stats | ✅ | ✅ | - |
| Subscription deduction in webhook | ✅ | ✅ | - |
| Daily quota reset | ✅ | ✅ | - |

### 5.3 Redemption Features

| Feature | Node.js | Go | Priority |
|---------|---------|-----|----------|
| Redeem code | ✅ | ✅ | - |
| My records | ✅ | ✅ | - |
| Admin list codes | ✅ | ✅ | - |
| Admin create codes | ✅ | ✅ | - |
| Admin update code | ✅ | ✅ | - |
| Admin delete code | ✅ | ✅ | - |
| Admin records | ✅ | ✅ | - |
| Admin stats | ✅ | ✅ | - |
| Welcome bonus on registration | ✅ | ✅ | - |

### 5.4 Enterprise Features

| Feature | Node.js | Go | Priority |
|---------|---------|-----|----------|
| List enterprise admins | ✅ | ⚠️ Basic | Missing pagination |
| Create enterprise admin | ✅ | ❌ | High |
| Update enterprise admin config | ✅ | ❌ | High |
| Toggle enterprise admin | ✅ | ❌ | High |
| Enterprise admin self info | ✅ | ❌ | Medium |
| Managed users list | ✅ | ❌ | High |
| Create managed user | ✅ | ❌ | High |
| Topup managed user | ✅ | ❌ | High |
| Toggle managed user | ✅ | ❌ | High |
| Usage stats for group | ✅ | ❌ | Medium |
| Usage ranking for group | ✅ | ❌ | Medium |

### 5.5 Groups Features

| Feature | Node.js | Go | Priority |
|---------|---------|-----|----------|
| List groups | ✅ | ✅ | - |
| Get group | ✅ | ⚠️ Partial | Missing members list |
| Create group | ✅ | ✅ | - |
| Update group | ✅ | ✅ | - |
| Delete group | ✅ | ✅ | - |
| Add members | ✅ | ❌ | High |
| Remove member | ✅ | ❌ | High |

### 5.6 Dashboard Features

| Feature | Node.js | Go | Priority |
|---------|---------|-----|----------|
| Stats cards | ✅ | ✅ | - |
| Consumption trend | ✅ | ❌ | Medium |
| Model distribution | ✅ | ❌ | Medium |
| Provider distribution | ✅ | ❌ | Medium |
| User ranking | ✅ | ❌ | Medium |
| User trend | ✅ | ❌ | Medium |
| Quick actions | ✅ | ❌ | Low |
| Redis caching | ✅ | ❌ | Medium |

### 5.7 Checkin Features

| Feature | Node.js | Go | Priority |
|---------|---------|-----|----------|
| Daily checkin | ✅ | ✅ | - |
| Checkin status | ✅ | ✅ | - |
| Checkin records | ✅ | ✅ | - |
| Admin stats | ✅ | ✅ | - |
| Consecutive days bonus | ✅ | ⚠️ Basic | Missing multiplier |

### 5.8 Sensitive Word Features

| Feature | Node.js | Go | Priority |
|---------|---------|-----|----------|
| Check content | ✅ | ✅ | - |
| Get words list | ✅ | ✅ | - |
| Update words list | ✅ | ✅ | - |
| Detection logs | ✅ | ❌ | Low |

### 5.9 Channel Health Features

| Feature | Node.js | Go | Priority |
|---------|---------|-----|----------|
| List channels | ✅ | ✅ | - |
| Test all channels | ✅ | ✅ | - |
| Get channel detail | ✅ | ✅ | - |
| Test single channel | ✅ | ✅ | - |
| History tracking | ✅ | ❌ | Medium |
| Usage stats per channel | ✅ | ✅ | - |
| Auto disable/enable | ✅ | ❌ | Medium |

---

## 6. Configuration Differences

### 6.1 Environment Variables

| Variable | Node.js | Go | Notes |
|----------|---------|-----|-------|
| `PORT` | ✅ | ✅ | - |
| `DB_HOST/PORT/NAME/USER/PASSWORD` | ✅ | ✅ | - |
| `REDIS_HOST/PORT/PASSWORD` | ✅ | ✅ | - |
| `JWT_SECRET` | ✅ | ✅ | - |
| `JWT_EXPIRES` | ✅ | ✅ | - |
| `ADMIN_MODE` | ✅ | ✅ | - |
| `ADMIN_EMAIL/PASSWORD/NAME` | ✅ | ✅ | - |
| `MAX_SYSTEM_ADMINS` | ✅ | ✅ | - |
| `MAX_SUPER_ADMINS` | ✅ | ✅ | - |
| `MAX_ADMINS` (MVP) | ✅ | ✅ | - |
| `KEY_ENCRYPTION_KEY` | ✅ | ✅ | - |
| `WEBHOOK_SECRET` | ✅ | ❌ | Missing in Go |
| `PROXY_URL` | ✅ | ❌ | Missing in Go |
| `PROXY_SYNC_SECRET` | ✅ | ❌ | Missing in Go |
| `CORS_ORIGIN` | ✅ | ❌ | Missing in Go |
| `PORTAL_URL` | ✅ | ❌ | Missing in Go |
| `AI_MODELS_PATH` | ❌ | ✅ | Go-specific |

---

## 7. Implementation Differences

### 7.1 Key Pool Management

**Node.js**:
- Encrypts keys with AES-256-CBC
- Supports key assignment to specific proxies
- Supports region-based key distribution
- Has key sync endpoint for proxy to pull keys
- Tracks last_synced_at

**Go**:
- Encrypts keys with AES-256-GCM (better)
- Missing key assignment features
- Missing region-based distribution
- Missing sync endpoint

### 7.2 Webhook Processing

**Node.js**:
- Redis queue buffering
- Batch processing with row locks
- Subscription deduction with daily quota reset
- Balance deduction with FOR UPDATE lock

**Go**:
- Redis queue buffering
- Batch processing
- Subscription deduction
- Balance deduction
- Similar implementation

### 7.3 Model Management

**Node.js**:
- Full EOL workflow with confirmation
- Pause/resume with reasons
- Discontinue with reasons
- Change detection and acceptance
- Validation integration with proxy
- Provider aliases support
- Cache-aware pricing

**Go**:
- Basic CRUD
- Pause/resume (no reasons)
- EOL/discontinue (basic)
- Missing change detection
- Missing validation integration

### 7.4 User Management

**Node.js**:
- Full role hierarchy support
- Sensitive operations protection
- Admin limit enforcement
- Enterprise admin features
- User groups

**Go**:
- Basic role support
- Missing enterprise admin management
- Missing user group membership

---

## 8. Priority Recommendations

### High Priority (Critical for Production)

1. **Implement GDPR Endpoints**
   - `/api/gdpr/export` - Data export
   - `/api/gdpr/delete` - Data deletion
   - `/api/gdpr/status` - GDPR status
   - Required for compliance

2. **Implement Key Pool Sync Endpoint**
   - `/api/admin/key-pool/sync` - For proxy to pull keys
   - Essential for multi-proxy deployment

3. **Add Missing Database Columns**
   - `users.shipping_address`, `invoice_info`, `backup_email`
   - `service_models` EOL and validation columns
   - `upstream_keys.metadata`, `allowed_regions`

4. **Implement Enterprise Admin Management**
   - Create/update/toggle enterprise admins
   - Managed users CRUD
   - Group usage stats

### Medium Priority (Important Features)

5. **Implement Pricing Management**
   - Pricing sources table
   - Pricing comparison endpoint
   - Pricing history tracking

6. **Implement Device Management**
   - Device tracking table
   - Device CRUD endpoints

7. **Add Usage Export**
   - CSV export for usage records
   - Detailed usage breakdown

8. **Implement Channel Health History**
   - History table
   - Historical tracking

### Low Priority (Nice to Have)

9. **Dashboard Charts**
   - Consumption trend
   - Model/provider distribution
   - User ranking

10. **Profile Enhancements**
    - Email/phone change flows
    - Backup email verification

---

## 9. Detailed Route Comparison

### Node.js Routes (Complete List)

```
/api/status
/api/auth/password-requirements
/api/auth/send-code
/api/auth/register
/api/auth/login
/api/auth/logout
/api/auth/me
/api/auth/forgot-password
/api/auth/reset-password
/api/billing/balance
/api/billing/recharge/create
/api/billing/callback
/api/billing/usage-details
/api/billing/usage-summary
/api/billing/usage-by-key
/api/billing/usage-by-model
/api/billing/usage-records
/api/billing/usage-records/:trace_id/detail
/api/billing/usage-records/export
/api/webhook/usage
/api/webhook/usage-batch
/api/webhook/key-pool-status
/api/webhook/heartbeat
/api/apikeys/validate
/api/apikeys
/api/apikeys/:id
/api/apikeys/:id/usage
/api/devices
/api/devices/:id
/api/profile
/api/profile/password
/api/profile/account
/api/profile/change-email
/api/profile/change-phone
/api/profile/send-change-code
/api/profile/send-backup-email-code
/api/orders
/api/orders/:id
/api/orders/create
/api/orders/:id/cancel
/api/gdpr/export
/api/gdpr/delete
/api/gdpr/status
/api/invoices
/api/invoices/:id
/api/invoices/admin/all
/api/invoices/admin/:id
/api/admin/providers
/api/admin/admin-mode
/api/admin/users
/api/admin/users/:id
/api/admin/analytics
/api/admin/key-pool
/api/admin/key-pool/:provider/:keyId/history
/api/admin/key-pool/sync
/api/admin/key-pool/:keyId/assign
/api/admin/proxies
/api/admin/models
/api/admin/models/status
/api/admin/sync-status
/api/admin/sync-openrouter
/api/admin/audit-logs
/api/admin/audit-logs/export
/api/admin/stats
/api/admin/usage/user-keys
/api/admin/usage/upstream-keys
/api/admin/pricing/sources
/api/admin/pricing/compare
/api/admin/pricing
/api/admin/pricing/:model_id
/api/admin/pricing/confirm
/api/admin/pricing/:model_id/history
/api/admin/pricing/fetch
/api/admin/service-models
/api/admin/service-models/sync-from-aimodels
/api/admin/service-models/sync-eol
/api/admin/service-models/providers/list
/api/admin/service-models/approvals/pending
/api/admin/service-models/approvals/:id/review
/api/admin/service-models/:id
/api/admin/service-models/:id/confirm-eol
/api/admin/service-models/:id/discontinue
/api/admin/service-models/:id/pause
/api/admin/service-models/:id/resume
/api/admin/service-models/:id/confirm
/api/admin/service-models/:id/ignore
/api/admin/service-models/:id/accept-changes
/api/admin/service-models/:id/validate
/api/admin/service-models/batch-validate
/api/admin/service-models/:id/validate
/api/admin/service-models/:id/auto-confirm
/api/admin/service-models/:id/pricing
/api/admin/service-models/:id/aliases
/api/admin/providers/list
/api/admin/providers
/api/admin/providers/:id
/api/admin/dashboard/stats
/api/admin/dashboard/charts/consumption-trend
/api/admin/dashboard/charts/model-distribution
/api/admin/dashboard/charts/provider-distribution
/api/admin/dashboard/charts/user-ranking
/api/admin/dashboard/charts/user-trend
/api/admin/dashboard/quick-actions
/api/admin/settings
/api/admin/settings/:key
/api/admin/settings/reset
/api/admin/enterprise/list
/api/admin/enterprise/create
/api/admin/enterprise/:id/config
/api/admin/enterprise/:id/toggle
/api/admin/enterprise/me
/api/admin/enterprise/users
/api/admin/enterprise/users
/api/admin/enterprise/users/:userId/topup
/api/admin/enterprise/users/:userId/toggle
/api/admin/enterprise/usage/stats
/api/admin/enterprise/usage/ranking
/api/admin/groups
/api/admin/groups/:id
/api/admin/groups/:id/members
/api/admin/groups/:id/members/:userId
/api/admin/channel-health
/api/admin/channel-health/test-all
/api/admin/channel-health/:providerId
/api/admin/channel-health/:providerId/test
/api/admin/subscriptions/plans
/api/admin/subscriptions/users
/api/admin/subscriptions/stats
/api/sensitive/check
/api/sensitive/words
/api/sensitive/logs
/api/checkin
/api/checkin/status
/api/checkin/records
/api/checkin/admin/stats
/api/redemption/redeem
/api/redemption/my-records
/api/redemption/admin/codes
/api/redemption/admin/codes
/api/redemption/admin/codes/:id
/api/redemption/admin/records
/api/redemption/admin/stats
/api/service-models/public
/api/usage/admin/usage/upstream-keys
/api/usage/admin/usage/user-keys
/api/usage/admin/usage/summary
```

### Go Routes (Complete List)

```
/api/health
/api/auth/password-requirements
/api/auth/send-code
/api/auth/register
/api/auth/login
/api/auth/logout
/api/auth/me
/api/auth/change-password
/api/auth/forgot-password
/api/auth/reset-password
/api/webhook/usage
/api/webhook/usage-batch
/api/webhook/key-pool-status
/api/webhook/heartbeat
/api/apikeys
/api/apikeys/:id
/api/apikeys/:id/usage
/api/apikeys/validate
/api/status
/api/dashboard/stats
/api/usage/admin/usage/upstream-keys
/api/usage/admin/usage/user-keys
/api/usage/admin/usage/summary
/api/admin/users
/api/admin/users/:id
/api/admin/admin-mode
/api/admin/upstream-keys
/api/admin/providers
/api/admin/providers/list
/api/admin/providers/:id
/api/admin/service-models
/api/admin/service-models/batch-validate
/api/admin/models
/api/admin/models/status
/api/admin/sync-status
/api/admin/sync-openrouter
/api/admin/sync-openrouter-live
/api/admin/validate-openrouter
/api/admin/openrouter-models
/api/admin/key-pool
/api/admin/key-pool/:provider/:keyId/history
/api/admin/key-pool/:provider/:keyId
/api/admin/key-pool/import
/api/admin/key-pool/export
/api/profile
/api/profile/password
/api/profile/account
/api/billing/balance
/api/billing/usage-details
/api/billing/usage-summary
/api/billing/usage-records
/api/billing/usage-by-key
/api/billing/usage-by-model
/api/billing/recharge/create
/api/orders
/api/orders/:id
/api/orders/create
/api/orders/:id/cancel
/api/invoices
/api/invoices/:id
/api/invoices/admin/all
/api/invoices/admin/:id
/api/admin/settings
/api/admin/settings/:key
/api/admin/settings/reset
/api/redemption/redeem
/api/redemption/my-records
/api/redemption/admin/codes
/api/redemption/admin/codes/:id
/api/redemption/admin/records
/api/redemption/admin/stats
/api/admin/subscriptions/plans
/api/admin/subscriptions/plans/:id
/api/admin/subscriptions/users
/api/admin/subscriptions/stats
/api/checkin
/api/checkin/status
/api/checkin/records
/api/checkin/admin/stats
/api/sensitive/check
/api/sensitive/words
/api/sensitive/logs
/api/admin/audit-logs
/api/admin/groups
/api/admin/groups/:id
/api/admin/enterprise
/api/admin/enterprise/:id
/api/admin/downstream-keys
/api/models/public
/api/admin/service-models
/api/admin/service-models/:id
/api/admin/service-models/:id/confirm-eol
/api/admin/service-models/:id/discontinue
/api/admin/service-models/:id/pause
/api/admin/service-models/:id/resume
/api/admin/service-models/:id/confirm
/api/admin/service-models/:id/ignore
/api/admin/service-models/:id/accept-changes
/api/admin/service-models/sync-from-aimodels
/api/admin/channel-health
/api/admin/channel-health/test-all
/api/admin/channel-health/:providerId
/api/admin/channel-health/:providerId/test
```

---

## 10. Conclusion

The Go Portal implementation has achieved approximately 75% feature parity with the Node.js version. The most critical gaps are:

1. **GDPR compliance** - Required for production
2. **Key pool sync** - Required for multi-proxy deployment
3. **Enterprise admin management** - Required for enterprise features
4. **Pricing management** - Required for cost tracking

The Go implementation has some advantages:
- Better encryption (AES-256-GCM vs AES-256-CBC)
- Cleaner route organization
- Better type safety
- Faster execution

Recommend focusing on the High Priority items first to achieve production readiness.
