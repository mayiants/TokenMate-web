# Design Proposal: Unify Redemption & Invitation into "Voucher"

**Date:** 2026-06-08  
**Status:** ✅ Implemented  
**Author:** Kimi Code (research agent)

---

## 1. Executive Summary

~~The portal currently maintains two independent code-based reward systems~~  
**【Status Update: 2026-06-08】Migration completed.**

All reward functionality has been unified under the `vouchers` + `voucher_uses` tables:

- **Redemption Codes** → Migrated to `vouchers(type='gift'/'promo')` 
- **Invitation Codes** → Migrated to `vouchers(type='referral')`
- **Welcome Bonus** → Migrated to `vouchers(type='welcome')`

Old tables (`redemption_codes`, `invitation_codes`, `redemption_records`, `invitation_records`) remain in the database schema but are no longer used by application code.

**Implementation approach taken:** Direct schema migration (not proxy layer). `RewardService.ApplyWelcomeBonus()` and `RewardService.ProcessInvitationCode()` now read from and write to `vouchers` + `voucher_uses` directly.

---

## 2. Current State Comparison

| Dimension | Redemption Codes | Invitation Codes |
|-----------|------------------|------------------|
| **Purpose** | Admin distributes gift/promo credit | Users recruit friends; both earn credit |
| **Creator** | Admin only | End user (self-service) |
| **Table** | `redemption_codes` | `invitation_codes` |
| **Record table** | `redemption_records` | `invitation_records` |
| **Code format** | `WG-XXXX-XXXX-XXXX` (auto) | `INV-XXXXXXXX` (auto) |
| **Amount field** | `amount` (single reward) | `reward_amount` + `invitee_reward_amount` (dual reward) |
| **Types** | `balance`, `promo`, `token_plan`, `code_plan`, `welcome` | `balance` (implicit) |
| **Max uses** | Configurable per code | Configurable per code (system default) |
| **User actions** | Redeem code, view history | Generate code, copy link, view history, transfer quota |
| **Reward destination** | `gift_balance` (+ `balance`) | Invitee → `gift_balance`; Inviter → `gift_balance` **or** `aff_quota` |
| **Admin actions** | CRUD codes, batch generate, stats, list records | List codes, disable/enable, list records, edit settings, manage welcome code |
| **Registration hook** | `welcome` type auto-applied by `RewardService.ApplyWelcomeBonus()` | Processed by `RewardService.ProcessInvitationCode()` |
| **Soft delete** | Yes (`deleted_at`) | Yes (`deleted_at`) |

### 2.1 Schema Detail

**`redemption_codes`**
```
id, code UNIQUE, name, type, amount BIGINT, max_uses, used_count,
status, expires_at, created_by → users(id), plan_id → subscription_plans(id),
description, reward_type, deleted_at, created_at, updated_at
```

**`redemption_records`**
```
id, user_id → users(id), code_id → redemption_codes(id), code,
amount, balance_before, balance_after, created_at
```

**`invitation_codes`**
```
id, code UNIQUE, inviter_id → users(id), max_uses, used_count,
status, expires_at, reward_amount BIGINT, reward_type, invitee_reward_amount,
deleted_at, created_at
```

**`invitation_records`**
```
id, inviter_id → users(id), invitee_id → users(id),
code_id / invitation_code_id → invitation_codes(id),
reward_granted, invitee_reward_granted, created_at
```

**`users` fields touched by invitation system**
```
aff_code, aff_quota, aff_history_quota, inviter_id
```

---

## 3. Can They Be Unified?

**Yes — conceptually.** Both are "vouchers": alphanumeric codes that deliver a reward when consumed within usage and time limits.

The differences that must be preserved in a unified model:

| Difference | Mitigation in unified model |
|------------|----------------------------|
| **Creator** | `created_by` (nullable for system-level welcome codes) + `owner_type` enum: `system`, `admin`, `user` |
| **Dual reward** | Add `reward_config` JSONB: `{ "inviter": { "amount": X, "destination": "gift_balance" }, "invitee": { "amount": Y, "destination": "gift_balance" } }` |
| **Reward types** | `reward_type` enum: `gift_balance`, `recharge_balance`, `plan_token`, `plan_code` |
| **Plan binding** | Keep `plan_id` (nullable) |
| **Welcome auto-apply** | Flag `auto_apply_on_register` + `apply_trigger = 'register'` |
| **Affiliate quota** | `aff_quota` remains on `users`; transfer becomes a generic "transfer voucher credit to balance" operation |

---

## 4. Proposed Unified Data Model (Phase 2 — Optional)

If the team decides to merge schemas later, the target design is:

### 4.1 `vouchers` (replaces `redemption_codes` + `invitation_codes`)

```sql
CREATE TABLE vouchers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(50) UNIQUE NOT NULL,
    name VARCHAR(255),

    -- Classification
    voucher_type VARCHAR(30) NOT NULL
        CHECK (voucher_type IN ('gift', 'promo', 'referral', 'welcome', 'plan_token', 'plan_code')),

    -- Ownership / creation
    owner_type VARCHAR(20) NOT NULL DEFAULT 'admin'
        CHECK (owner_type IN ('system', 'admin', 'user')),
    created_by UUID REFERENCES users(id),   -- admin or user who created it
    owner_id UUID REFERENCES users(id),     -- for user-generated referral codes

    -- Usage constraints
    max_uses INTEGER DEFAULT 1,
    used_count INTEGER DEFAULT 0,
    status VARCHAR(20) DEFAULT 'active',
    expires_at TIMESTAMP,
    deleted_at TIMESTAMP,

    -- Reward configuration (flexible)
    reward_amount BIGINT DEFAULT 0,              -- primary reward amount
    reward_type VARCHAR(20) DEFAULT 'gift_balance',
    plan_id UUID REFERENCES subscription_plans(id),

    -- Referral-specific (nullable for non-referral vouchers)
    referrer_reward_amount BIGINT DEFAULT 0,
    referrer_reward_type VARCHAR(20) DEFAULT 'gift_balance',
    invitee_reward_amount BIGINT DEFAULT 0,
    invitee_reward_type VARCHAR(20) DEFAULT 'gift_balance',

    -- Behaviour
    auto_apply_trigger VARCHAR(20),              -- e.g. 'register', NULL = manual
    description TEXT,

    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
```

### 4.2 `voucher_uses` (replaces `redemption_records` + `invitation_records`)

```sql
CREATE TABLE voucher_uses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    voucher_id UUID NOT NULL REFERENCES vouchers(id),

    -- Who consumed it
    consumer_id UUID NOT NULL REFERENCES users(id),

    -- For referral vouchers: who referred
    referrer_id UUID REFERENCES users(id),

    amount BIGINT NOT NULL,                      -- value granted to consumer
    balance_before BIGINT,
    balance_after BIGINT,

    -- For referral tracking
    referrer_reward_amount BIGINT,
    referrer_reward_granted BOOLEAN DEFAULT false,
    consumer_reward_granted BOOLEAN DEFAULT false,

    created_at TIMESTAMP DEFAULT NOW()
);
```

### 4.3 Migration of existing data

- `redemption_codes` → `vouchers` with `voucher_type = 'gift'/'promo'/'welcome'/'plan_token'/'plan_code'` and `owner_type = 'admin'`.
- `invitation_codes` → `vouchers` with `voucher_type = 'referral'`, `owner_type = 'user'`, `owner_id = inviter_id`.
- `redemption_records` → `voucher_uses` with `referrer_id = NULL`.
- `invitation_records` → `voucher_uses` with both `consumer_id = invitee_id` and `referrer_id = inviter_id`.

---

## 5. Proposed Unified User Page — `/voucher`

Instead of `/redemption` and `/invitation`, users see a single **Voucher** page with three tabs:

### Tab 1: "My Vouchers" (我的券码)
- **Generated referral codes** (formerly invitation codes)
  - Stats cards: active codes count, remaining reward quota, lifetime earned
  - Table: code, used/max, invitee reward, referrer reward, expiry, status, copy link
  - "Generate Code" button (respects `max_codes_per_user` limit)
- **Transfer to Balance** card (visible only if `aff_quota > 0`)

### Tab 2: "Redeem" (兑换)
- Prominent input + "Redeem" button
- Success animation showing amount added
- Recent redemption history (last 5) below the input

### Tab 3: "History" (记录)
- Unified chronological list of:
  - Redemptions (admin codes consumed)
  - Referrals (invitee sign-ups via my codes)
- Filters: date range, type (`redemption` | `referral`), status
- Columns: date, code, type, amount/credit granted, status

**Benefits:** Users no longer need to understand the distinction between "redemption" and "invitation"; they simply manage all their codes and credits in one place.

---

## 6. Proposed Unified Admin Page — `/admin/vouchers`

Merge `/admin/redemption` and `/admin/invitations` into one page with sections:

### Section A: Stats Bar
- Total vouchers | Active | Promo | Referral | Welcome | Exhausted
- Total redeemed amount | Total referral rewards granted

### Section B: Filters & List
- Filter by: `type` (all / gift / promo / referral / welcome / plan), `status`, `owner` (admin / user), date range
- Table columns: name/code, type, owner, amount/rewards, uses, status, expiry, actions
- Bulk actions: disable, enable, delete (soft)
- Create buttons:
  - "Create Gift Voucher" (old single redemption)
  - "Batch Generate" (old batch redemption)
  - "Edit Welcome Voucher" (old welcome code management)

### Section C: Global Settings (卡片)
- Referral settings: max codes per user, default max uses, default expiry, inviter reward, invitee reward, auto-transfer toggle

### Section D: Usage Records (sub-tab)
- All voucher uses (redemptions + referrals) with:
  - Consumer name/email
  - Referrer name/email (if applicable)
  - Code, amount, reward granted status, date

---

## 7. API Route Changes

### Phase 1 (UI unification, backward-compatible)
Keep existing tables and API routes, but add a thin aggregation layer:

```
GET  /api/voucher/my-codes        → merges /api/invitation/my-codes + /api/redemption/my-records
POST /api/voucher/redeem          → proxies to /api/redemption/redeem
POST /api/voucher/generate        → proxies to /api/invitation/generate
POST /api/voucher/transfer-quota  → proxies to /api/invitation/transfer-quota
GET  /api/voucher/records         → unified pagination across redemption_records + invitation_records

GET  /api/admin/vouchers          → unified list (query param ?type=...)
GET  /api/admin/vouchers/records  → unified records
GET  /api/admin/vouchers/stats    → unified stats
POST /api/admin/vouchers          → create (replaces single + batch)
PATCH /api/admin/vouchers/:id     → update
POST /api/admin/vouchers/:id/toggle → disable/enable
GET  /api/admin/vouchers/welcome  → welcome voucher config
POST /api/admin/vouchers/welcome  → welcome voucher upsert
```

### Phase 2 (Schema unification)
Replace the proxy layer with direct queries against `vouchers` + `voucher_uses`.

---

## 8. Migration Plan

### Step 1: Add new unified UI (backward compatible)
1. Create `web/templates/voucher.html` (user) and `web/templates/admin-vouchers.html` (admin).
2. Add new API route group `/api/voucher` and `/api/admin/vouchers` as aggregators.
3. Update sidebar navigation:
   - Replace `/redemption` and `/invitation` links with `/voucher`.
   - Replace `/admin/redemption` and `/admin/invitations` with `/admin/vouchers`.
4. Add i18n keys under `voucher.*` namespace.
5. Keep old pages and routes live but hidden from nav (soft deprecation).

### Step 2: Data migration scripts (Phase 2 — optional)
1. Create `vouchers` and `voucher_uses` tables.
2. Run idempotent backfill script:
   - Migrate `redemption_codes` → `vouchers`.
   - Migrate `invitation_codes` → `vouchers`.
   - Migrate records → `voucher_uses`.
3. Update `RewardService` to read/write `vouchers`.
4. Swap API implementation from proxy to direct table access.
5. Drop old tables in a subsequent release (after validation).

### Step 3: Cleanup ✅ Completed
1. ~~Remove deprecated templates~~ ✅ Deleted: `redemption.html`, `invitation.html`, `admin-redemption.html`, `admin-invitations.html`
2. ~~Remove old route files~~ ✅ Deleted: `internal/routes/redemption.go`, `internal/routes/invitations.go`
3. ~~Delete old i18n keys~~ ✅ Old nav keys removed from `layout.html`

---

## 9. i18n Key Inventory & New Keys Needed

### Existing keys to consolidate
| Old Key Context | Proposed Unified Key |
|-----------------|----------------------|
| `nav.redemption`, `nav.invitation` | `nav.voucher` |
| `nav.admin.redemption`, `nav.admin.invitations` | `nav.admin.vouchers` |
| `error.invalid_redemption_code` | `error.invalid_voucher_code` |
| `error.redemption_code_disabled` | `error.voucher_disabled` |
| `error.redemption_code_expired` | `error.voucher_expired` |
| `error.redemption_code_used` | `error.voucher_already_used` |
| `info.redeem_success` | `info.voucher_redeemed` |
| `ui_使用兑换码` | `action_redeem_voucher` |
| `title_我的兑换记录` | `title_my_voucher_history` |
| `title_邀请码` | `title_my_referral_codes` |
| `title_邀请记录_4` | `title_referral_history` |
| `title_奖励转余额` | `title_transfer_voucher_credit` |
| `stat_邀请码` | `stat_referral_codes` |
| `stat_邀请奖励余额` | `stat_voucher_credit` |
| `stat_累计邀请奖励` | `stat_lifetime_voucher_earnings` |
| `modal_邀请码生成成功` | `modal_referral_code_created` |
| `admin.redemption`, `admin.invitations` | `admin.vouchers` |

**Recommendation:** Create a new `voucher` namespace in the i18n dictionary and duplicate/consolidate strings there. Do not delete old keys until the old pages are fully retired.

---

## 10. Open Questions — Resolution Log

| # | Question | Resolution |
|---|----------|------------|
| 1 | **Naming** | "Voucher" 作为统一术语。前端保留子标签区分：gift/promo/referral/welcome/plan。 |
| 2 | **Plan binding** | Admin-only。用户生成的 referral code 不绑定 plan。 |
| 3 | **Welcome code placement** | `admin/vouchers` 页面顶部独立 Welcome Settings card + 统计卡片。 |
| 4 | **Affiliate system scope** | `aff_code` 保持为隐藏实现细节，不在 voucher 页面暴露。 |
| 5 | **Migration appetite** | 直接完成 Phase 2（schema 合并），跳过了 proxy 层。 |
| 6 | **Route deprecation policy** | 旧路由直接删除（`/redemption`、`/invitation` 及其 admin 版本），不保留重定向。 |
| 7 | **Enterprise invitations** | 确认无冲突，`enterprise_invitations` 保持独立。 |

---

## 11. Appendix: File Inventory

| File | Role | Status |
|------|------|--------|
| ~~`internal/routes/redemption.go`~~ | ~~Redemption API~~ | ✅ Deleted |
| ~~`internal/routes/invitations.go`~~ | ~~Invitation API~~ | ✅ Deleted |
| `internal/services/reward.go` | Welcome & invitation reward logic | ✅ Updated to use `vouchers` + `voucher_uses` |
| ~~`web/templates/redemption.html`~~ | ~~User redemption page~~ | ✅ Deleted |
| ~~`web/templates/invitation.html`~~ | ~~User invitation page~~ | ✅ Deleted |
| ~~`web/templates/admin-redemption.html`~~ | ~~Admin redemption page~~ | ✅ Deleted |
| ~~`web/templates/admin-invitations.html`~~ | ~~Admin invitation page~~ | ✅ Deleted |
| `web/templates/layout.html` | Sidebar nav | ✅ Cleaned up old nav keys |
| `internal/db/schema.go` | Schema init | ✅ Added `vouchers` + `voucher_uses` (already existed) |
| `internal/web/pages.go` | Page route registration | ✅ Removed old routes, kept `/voucher` + `/admin/vouchers` |
| `internal/web/admin_handlers.go` | Old admin form handlers | Keep for compat | Delete handlers |
