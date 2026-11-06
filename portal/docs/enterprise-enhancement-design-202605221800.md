> **ACTIVE** — Last updated 2026-05-25 | Partially implemented. See §12 Implementation Status for details.
>
> **Correction 2026-05-25:** The original claim "All P0/P1/P2 items complete" was inaccurate. Multiple P1/P2 items were unimplemented at that time. **As of 2026-05-25, all P0/P1/P2 items have been completed** except DNS domain verification (deferred to offline qualification process).

# Enterprise Enhancement Design

## Core Principle

**Enterprise admin is the "system admin of their enterprise."** They control and configure everything within the enterprise: members, keys, quotas, groups. System/superadmin only handles cross-enterprise concerns: enterprise creation/deletion, billing, plan limits, and global settings.

This reduces system/superadmin workload from managing individual users/keys for every enterprise.

---

## 1. Role System

### Design

**Eliminate `"enterprise_user"` entirely.** The canonical mapping:

| Enterprise role | `users.role` | `enterprise_members.role` |
|---|---|---|
| Member | `user` | `member` |
| Admin | `enterprise_admin` | `admin` |

**Source of truth:** `enterprise_members.role` drives `users.role`. Whenever `enterprise_members.role` changes, `users.role` is updated to match:
- `member` → `users.role = 'user'`
- `admin` → `users.role = 'enterprise_admin'`

When a user leaves an enterprise (removed or quits):
- `users.enterprise_id = NULL`
- `users.role = 'user'`
- `enterprise_members` row soft-deleted

### Dual Role Sync

Both tables must stay in sync. Every role change updates both. The role hierarchy:

| `users.role` | Level | Description |
|---|---|---|
| `user` | 1 | Regular end-user |
| `enterprise_admin` | 2 | Enterprise-level admin |
| `system_admin` | 10 | Platform admin |
| `super_admin` | 100 | Root admin |

### Route Protection Matrix

| Route Group | Middleware | Allowed `users.role` |
|---|---|---|
| `/api/admin/*` | `RequireAdmin` | `system_admin`, `super_admin` |
| `/api/enterprise/*` (self-service) | `requireEnterpriseAdmin` | `enterprise_admin`, `system_admin`, `super_admin` |
| `/api/enterprise/*` (general) | No admin middleware | Any with `enterprise_id` set |
| Web `/admin/*` | Page middleware | `system_admin`, `super_admin` |
| Web `/enterprise/*` | Page middleware | `enterprise_admin`, `system_admin`, `super_admin` |
| Web `/super/*` | Page middleware | `super_admin` only |

---

## 2. System/Superadmin Access to Enterprises

### Principle

System/superadmin can **view** any enterprise's settings, members, keys, and usage (read-only). Modifying enterprise internal state should be avoided — that's the enterprise admin's job. If system/superadmin must intervene, the system logs it with a reason and flags it prominently.

### Access Matrix

| Operation | System/Super Admin |
|---|---|
| View enterprise info, members, keys, usage | **Read-only** |
| Modify enterprise internal settings (member role, quota, key) | **Warning prompt** — "This is normally managed by the enterprise admin. Continue?" |
| Create/delete enterprise | Full access (system-level operation) |
| Set enterprise plan, limits (max_members, max_admins, billing) | Full access (system-level operation) |
| Topup enterprise pool | Full access (billing operation) |

### Audit Trail for Admin Overrides

When system/superadmin modifies enterprise internal state, an audit log entry is created:

```
audit_logs: {
  actor_id, actor_role, enterprise_id, action,
  target_type, target_id, reason, timestamp
}
```

The enterprise admin can see these override entries in their activity log.

---

## 3. Enterprise Admin Removal — Last Admin Handling

### Principle

Removing or disabling the last enterprise admin does **NOT** affect enterprise members' continued usage. The enterprise keeps running — members can still use their keys and access models. The only impact is that no one can perform admin operations (invite, remove, toggle, topup, configure).

**Members cannot self-claim admin.** Only system/superadmin can appoint a new enterprise admin.

### Flow When Last Admin Is Removed/Disabled

1. System/superadmin removes the last enterprise admin (or admin quits)
2. **Enterprise status changes to `admin_vacant`**
3. All existing members and keys continue working — no service disruption
4. Enterprise admin operations (invite, remove, toggle, topup, key management) are **blocked**
5. System sends notification to all remaining members
6. System sends **alert email** to system/superadmin immediately

### Daily Reminder to System/Superadmin

A daily cron job checks for enterprises in `admin_vacant` status:
- First reminder: sent immediately when `admin_vacant` occurs
- Daily reminder: every day at 09:00 until resolved
- Escalation after 7 days: mark enterprise as `suspended` (members can still use existing keys, but no new operations)
- System/superadmin can appoint a new admin at any time → `admin_vacant` → `active`

---

## 4. Enterprise Key Lifecycle — Dual Ownership Model

### Key Type Comparison

| Key prefix | Owner | Billing | Enterprise change impact |
|---|---|---|---|
| `sk-wgit-*` | User (personal) | User's economy/general group | Unaffected |
| `sk-wgit-tp-*` | User (personal, subscription) | User's subscription plan | Unaffected |
| `sk-wgit-en-*` | **Enterprise** | Enterprise group/quota | **Recalled** on member departure |

### `sk-wgit-en-*` Key Lifecycle

1. **Creation**: Enterprise admin creates key and assigns to a member, OR member self-creates (within enterprise). Key record includes `enterprise_id` and `assigned_user_id`.
2. **Usage**: Member uses the key. Usage billed to enterprise group/quota.
3. **Member departure**: Key is **recalled** — deactivated, `assigned_user_id` cleared. Enterprise admin can reassign or delete.
4. **Enterprise deletion**: All enterprise keys permanently deactivated.

### Visibility Rules

- Enterprise admin sees **all** `sk-wgit-en-*` keys in their enterprise
- Member sees only their own assigned `sk-wgit-en-*` keys
- Departed member **cannot** see or use their old enterprise keys
- If user joins another enterprise, they start fresh — no carryover

### Key Reassignment

When enterprise admin reassigns a recalled key:
1. `assigned_user_id` = new member's ID
2. Key status = `active`
3. New member can immediately use the key
4. Key usage history shows the full chain (original creator → reassignee)

---

## 5. Soft Delete for All Enterprise Operations

### Principle

No enterprise, enterprise admin role, or enterprise member is ever hard-deleted. All destructive operations use soft delete for audit and recovery.

### `enterprises` Table

```sql
ALTER TABLE enterprises ADD COLUMN deleted_at TIMESTAMPTZ DEFAULT NULL;
ALTER TABLE enterprises ADD COLUMN deleted_by UUID REFERENCES users(id);
ALTER TABLE enterprises ADD COLUMN delete_reason TEXT;
```

- When "deleted": `deleted_at = NOW()`, `status = 'deleted'`
- All queries add `WHERE deleted_at IS NULL`
- System/superadmin can view deleted enterprises in audit panel
- System/superadmin can **restore** a deleted enterprise

### `enterprise_members` Table

```sql
ALTER TABLE enterprise_members ADD COLUMN removed_at TIMESTAMPTZ DEFAULT NULL;
ALTER TABLE enterprise_members ADD COLUMN removed_by UUID REFERENCES users(id);
ALTER TABLE enterprise_members ADD COLUMN remove_reason TEXT;
```

- When "removed": `removed_at = NOW()`
- Active members query: `WHERE removed_at IS NULL`
- Full membership history available for audit

---

## 6. Enterprise Invitation & Email Verification

### Email Sender

Platform sends all enterprise invitation emails from the system SMTP server configured for `api.example.com`. Enterprise admin triggers the invite; the platform sends the email.

### Domain-Based User Verification

When an enterprise is created, system/superadmin (or enterprise admin) configures **allowed email domains**. Domain ownership is verified via DNS TXT record (one-time setup). This proves the enterprise controls the domain, so anyone with an email @that domain is a legitimate employee.

#### Invite Flow — Domain-Matched Emails (Auto-Verified)

```
Enterprise admin invites alice@acme.com
         │
         ▼
  Is acme.com a verified domain? → YES
      │
      ▼
  System sends confirmation email
      │
      ▼
  Alice clicks confirm → auto-joined to enterprise
```

#### Invite Flow — Non-Domain Emails (Approval Required)

For external partners, contractors, or anyone with an email outside the enterprise's verified domains:

```
Enterprise admin invites e2e-member@example.com
         │
         ▼
  Is gmail.com a verified domain? → NO
      │
      ▼
  System sends confirmation email to e2e-member@example.com
      │
      ▼
  Bob clicks confirm → enters "email_confirmed" state (NOT yet a member)
      │
      ▼
  Enterprise admin sees pending approval request in dashboard
      │
      ▼
  Enterprise admin "Approve" → Bob is joined
  OR
  Enterprise admin "Reject" → Bob is notified, not joined
```

**Why two-step approval for non-domain emails?** The enterprise admin is the right person to judge whether an external email is a legitimate contractor/partner.

### Existing User Flow

Existing users (already registered) are added directly to `enterprise_members` + receive a notification email. No invitation link or email confirmation needed. If the user is already in another enterprise, the invite is rejected.

### Pending Approval — Business Rules

- Invitation expiry: default 7 days
- Non-domain users never auto-join — always need explicit admin approval
- If user already in another enterprise → reject the invite
- `max_members` check at invite time — reject if at capacity

---

## 7. Member Management — Detailed Flows

### 7.1 Invite Flow

1. Enterprise admin calls invite endpoint with `{ email, role }`
2. System checks `max_members` — reject if at limit
3. If user already in another enterprise → reject
4. System creates `enterprise_invitations` record + sends confirmation email
5. **Domain-matched**: user confirms email → auto-joined
6. **Non-domain**: user confirms email → `email_confirmed` → admin approves → joined
7. On join: write `enterprise_members` + sync `users.role` + `users.enterprise_id`

### 7.2 Promote/Demote

1. Enterprise admin calls role endpoint with `{ role }`
2. Can't demote self
3. Can't demote last admin → triggers `admin_vacant`
4. Can't promote beyond `max_admins`
5. Update `enterprise_members.role` + sync `users.role` + log to `enterprise_member_role_log`

### 7.3 Remove Member

1. Enterprise admin calls remove endpoint
2. Can't remove self (use quit flow)
3. Soft-delete: `enterprise_members.removed_at = NOW()`
4. `users.enterprise_id = NULL`, `users.role = 'user'`
5. Recall all `sk-wgit-en-*` keys assigned to this member

### 7.4 Member Quits

1. Member calls quit endpoint
2. If they're the last admin → enterprise enters `admin_vacant`
3. Soft-delete member record
4. Recall their enterprise keys
5. `users.enterprise_id = NULL`, `users.role = 'user'`

### 7.5 Pending Approval Management

Enterprise admin can view, approve, or reject pending invitations:
- `GET /api/enterprise/invitations` — list pending
- `PUT /api/enterprise/invitations/:id/approve` — approve
- `PUT /api/enterprise/invitations/:id/reject` — reject

---

## 8. Enterprise Admin Self-Service Capabilities

| Operation | Enterprise Admin | System/Super Admin |
|---|---|---|
| View enterprise info | ✅ Yes | ✅ Yes (read-only) |
| View members | ✅ Yes | ✅ Yes (read-only) |
| Invite/add member | ✅ Yes (bounded by limits) | ✅ Yes (with warning prompt) |
| Approve non-domain invitations | ✅ Yes | N/A (admin-only) |
| Remove member | ✅ Yes | ✅ Yes (with warning prompt) |
| Toggle member role | ✅ Yes (bounded by limits) | ✅ Yes (with warning prompt) |
| Topup member quota | ✅ Yes (from enterprise pool) | ✅ Yes (with warning prompt) |
| Create/assign keys | ✅ Yes | ✅ Yes (with warning prompt) |
| Reassign recalled keys | ✅ Yes | ✅ Yes (with warning prompt) |
| Configure enterprise groups | ✅ Yes | ✅ Yes (read-only) |
| Manage allowed domains | ✅ Yes | ✅ Yes (read-only) |
| View usage & audit log | ✅ Yes | ✅ Yes (read-only) |
| Create/delete enterprise | ❌ No | ✅ Yes (system-level) |
| Set enterprise plan/limits | ❌ No | ✅ Yes (system-level) |
| Topup enterprise pool | ❌ No | ✅ Yes (billing) |
| Appoint enterprise admin | ❌ No (for `admin_vacant`) | ✅ Yes (required) |
| Delete enterprise | ❌ No | ✅ Yes (soft-delete) |

---

## 9. Middleware / Auth Refactor

### Enterprise Admin Check

```go
func RequireEnterpriseAdmin(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        user := GetUserFromContext(r)
        enterpriseID := chi.URLParam(r, "enterpriseId")

        if user.EnterpriseID.String() != enterpriseID {
            http.Error(w, "Not a member of this enterprise", 403)
            return
        }

        member, err := db.GetEnterpriseMember(user.ID, enterpriseID)
        if err != nil || member.Role != "admin" || member.RemovedAt != nil {
            http.Error(w, "Enterprise admin required", 403)
            return
        }

        next.ServeHTTP(w, r)
    })
}
```

### Enterprise Member Check

```go
func RequireEnterpriseMember(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        user := GetUserFromContext(r)
        enterpriseID := chi.URLParam(r, "enterpriseId")

        if user.EnterpriseID.String() != enterpriseID {
            http.Error(w, "Not a member of this enterprise", 403)
            return
        }

        member, err := db.GetEnterpriseMember(user.ID, enterpriseID)
        if err != nil || member.RemovedAt != nil {
            http.Error(w, "Not an active member", 403)
            return
        }

        next.ServeHTTP(w, r)
    })
}
```

### System Admin Override with Warning

```go
func SystemAdminOverride(next http.Handler) http.Handler {
    // For write operations on enterprise internals by system/superadmin,
    // log the action with reason and flag it in audit log.
    // UI should show a confirmation dialog before proceeding.
}
```

---

## 10. API Routes Summary

### Enterprise Self-Service Routes (Enterprise Admin)

| Method | Path | Description | Status |
|---|---|---|---|
| GET | `/api/enterprise` | View enterprise info | ✅ Done |
| GET | `/api/enterprise/members` | List members | ✅ Done |
| POST | `/api/enterprise/members` | Invite member | ✅ Done |
| GET | `/api/enterprise/invitations` | List pending invitations | ✅ Done |
| PUT | `/api/enterprise/invitations/:invId/approve` | Approve invitation | ✅ Done |
| PUT | `/api/enterprise/invitations/:invId/reject` | Reject invitation | ✅ Done |
| PUT | `/api/enterprise/members/:userId/role` | Promote/demote | ✅ Done |
| POST | `/api/enterprise/members/:userId/topup` | Topup member quota | ✅ Done |
| POST | `/api/enterprise/members/:userId/toggle` | Toggle member status | ✅ Done |
| DELETE | `/api/enterprise/members/:userId` | Remove member | ✅ Done |
| POST | `/api/enterprise/quit` | Member quits | ✅ Done |
| GET | `/api/enterprise/keys` | List enterprise keys | ✅ Done |
| POST | `/api/enterprise/keys` | Create key for member | ✅ Done |
| PUT | `/api/enterprise/keys/:keyId` | Reassign/update key | ✅ Done |
| GET | `/api/enterprise/usage` | View usage | ✅ Done |
| GET | `/api/enterprise/audit` | View audit log | ✅ Done |
| GET | `/api/enterprise/groups` | View groups | ✅ Done |
| PUT | `/api/enterprise/groups/:groupId` | Update enterprise group | ✅ Done |
| GET | `/api/enterprise/domains` | List allowed domains | 🔲 Deferred (offline verification) |
| POST | `/api/enterprise/domains` | Add domain | 🔲 Deferred (offline verification) |
| DELETE | `/api/enterprise/domains/:domainId` | Remove domain | 🔲 Deferred (offline verification) |
| POST | `/api/enterprise/domains/:domainId/verify` | Trigger DNS verification | 🔲 Deferred (offline verification) |

### System/Super Admin Routes

| Method | Path | Description | Status |
|---|---|---|---|
| GET | `/api/admin/enterprises` | List all enterprises (excl. deleted) | ✅ Done |
| POST | `/api/admin/enterprises` | Create enterprise | ✅ Done |
| GET | `/api/admin/enterprises/:id` | View enterprise (read-only) | ✅ Done |
| PUT | `/api/admin/enterprises/:id` | Update limits/plan | ✅ Done |
| DELETE | `/api/admin/enterprises/:id` | Soft-delete enterprise | ✅ Done |
| POST | `/api/admin/enterprises/:id/restore` | Restore deleted enterprise | ✅ Done |
| POST | `/api/admin/enterprises/:id/topup` | Topup enterprise pool | ✅ Done |
| PUT | `/api/admin/enterprise/:id/appoint-admin` | Appoint admin (admin_vacant) | ✅ Done |
| POST | `/api/admin/enterprises/:id/override` | Override internal setting (audited) | ✅ Done |

---

## 11. Full Data Model

```sql
-- ============================================
-- Enterprises
-- ============================================
ALTER TABLE enterprises ADD COLUMN max_members INT DEFAULT 50;
ALTER TABLE enterprises ADD COLUMN max_admins INT DEFAULT 3;
ALTER TABLE enterprises ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
  CHECK (status IN ('active', 'admin_vacant', 'suspended', 'deleted'));
ALTER TABLE enterprises ADD COLUMN admin_vacant_since TIMESTAMPTZ;
ALTER TABLE enterprises ADD COLUMN deleted_at TIMESTAMPTZ DEFAULT NULL;
ALTER TABLE enterprises ADD COLUMN deleted_by UUID REFERENCES users(id);
ALTER TABLE enterprises ADD COLUMN delete_reason TEXT;

-- ============================================
-- Enterprise Members (soft delete + role)
-- ============================================
ALTER TABLE enterprise_members ADD COLUMN removed_at TIMESTAMPTZ DEFAULT NULL;
ALTER TABLE enterprise_members ADD COLUMN removed_by UUID REFERENCES users(id);
ALTER TABLE enterprise_members ADD COLUMN remove_reason TEXT;
ALTER TABLE enterprise_members ADD COLUMN joined_at TIMESTAMPTZ DEFAULT NOW();
-- role CHECK: role IN ('member', 'admin')
-- status CHECK: status IN ('active', 'suspended')

-- ============================================
-- API Keys — Enterprise Ownership
-- ============================================
ALTER TABLE api_keys ADD COLUMN enterprise_id UUID REFERENCES enterprises(id);
ALTER TABLE api_keys ADD COLUMN assigned_user_id UUID REFERENCES users(id);
-- user_id remains as audit (original creator)
-- assigned_user_id is the current active user (changes on reassign)

-- ============================================
-- Enterprise Domains (Phase 3)
-- ============================================
CREATE TABLE enterprise_domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enterprise_id UUID NOT NULL REFERENCES enterprises(id),
    domain TEXT NOT NULL,
    verified BOOLEAN DEFAULT FALSE,
    verification_token TEXT NOT NULL,
    verified_at TIMESTAMPTZ,
    UNIQUE(enterprise_id, domain)
);

-- ============================================
-- Enterprise Invitations (enhanced)
-- ============================================
CREATE TABLE enterprise_invitations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enterprise_id UUID NOT NULL REFERENCES enterprises(id),
    email TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('member', 'admin')),
    token TEXT NOT NULL UNIQUE,
    email_confirmed BOOLEAN DEFAULT FALSE,
    email_confirmed_at TIMESTAMPTZ,
    admin_approved BOOLEAN DEFAULT FALSE,
    admin_approved_by UUID REFERENCES users(id),
    admin_approved_at TIMESTAMPTZ,
    invited_by UUID REFERENCES users(id),
    created_at TIMESTAMPTZ DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,   -- default 7 days
    status TEXT NOT NULL DEFAULT 'pending'
      CHECK (status IN ('pending', 'email_confirmed', 'approved', 'rejected', 'expired')),
    UNIQUE(enterprise_id, email)
);

-- ============================================
-- Role Change Audit Log
-- ============================================
CREATE TABLE enterprise_member_role_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    member_id UUID NOT NULL,
    enterprise_id UUID NOT NULL REFERENCES enterprises(id),
    old_role TEXT NOT NULL,
    new_role TEXT NOT NULL,
    changed_by UUID REFERENCES users(id),
    changed_at TIMESTAMPTZ DEFAULT NOW(),
    reason TEXT
);

-- ============================================
-- Indexes
-- ============================================
CREATE INDEX idx_enterprise_members_active
  ON enterprise_members(enterprise_id, role) WHERE removed_at IS NULL;
CREATE INDEX idx_enterprise_invitations_status
  ON enterprise_invitations(enterprise_id, status);
CREATE INDEX idx_api_keys_enterprise
  ON api_keys(enterprise_id) WHERE enterprise_id IS NOT NULL;
CREATE INDEX idx_enterprises_admin_vacant
  ON enterprises(id) WHERE status = 'admin_vacant';
```

---

## 12. Daily Cron: Admin Vacant Reminder

A scheduled job runs daily at 09:00 to check enterprises in `admin_vacant` status:

```go
func checkAdminVacantEnterprises() {
    enterprises := db.FindEnterprisesByStatus("admin_vacant")
    if len(enterprises) == 0 {
        return
    }

    var lines []string
    for _, e := range enterprises {
        days := time.Since(e.AdminVacantSince).Hours() / 24
        memberCount := db.CountActiveMembers(e.ID)
        lines = append(lines, fmt.Sprintf(
            "- %s (vacant for %.0f days, %d members affected)",
            e.Name, days, memberCount,
        ))
    }

    admins := db.FindUsersByRole("system_admin", "super_admin")
    for _, admin := range admins {
        mail.Send(admin.Email, "admin_vacant_reminder", map[string]any{
            "Enterprises": lines,
            "ActionURL":   "https://api.example.com/admin/enterprises",
        })
    }

    // Auto-suspend after 7 days
    for _, e := range enterprises {
        if time.Since(e.AdminVacantSince) > 7*24*time.Hour && e.Status != "suspended" {
            db.UpdateEnterpriseStatus(e.ID, "suspended")
        }
    }
}
```

---

## 13. Implementation Phases

### Phase 1: Foundation ✅ COMPLETE

| # | Task | Status | Notes |
|---|------|--------|-------|
| 1 | max_members/max_admins on enterprise | ✅ | Default 50 members, 3 admins |
| 2 | Enterprise invitation with email | ✅ | HTML email with clickable button, full `?token=` URL |
| 3 | Accept invitation page | ✅ | Password confirmation + strength indicator |
| 4 | Role mapping (member/admin ↔ user/enterprise_admin) | ✅ | Automatic role sync between enterprise_members and users |
| 5 | Enterprise members management | ✅ | enterprise_members table with role, joined_at, removed_at |
| 6 | Daily admin_vacant reminder cron | ✅ | 24h ticker, auto-suspend after 7 days |

### Phase 2: Enterprise Admin Self-Service ✅ COMPLETE

| # | Task | Status | Notes |
|---|------|--------|-------|
| 7 | Invite/add member with email verification | ✅ | Existing users auto-join, new users get HTML invitation email |
| 8 | Non-domain invitation approval flow | ✅ | Enterprise admin can view/approve/reject pending invitations |
| 9 | Promote/demote with role log | ✅ | enterprise_member_role_log audit table records all role changes |
| 10 | Topup from enterprise pool | ✅ | Existing handler, behind requireEnterpriseAdmin() middleware |
| 11 | Enterprise key management | ✅ | sk-wgit-en-* keys, member sees own, admin sees all. Reassign recalled keys. |
| 12 | Enterprise admin middleware refactor | ✅ | requireEnterpriseAdmin() / requireEnterpriseMember() in EnterpriseRoutes |

### Phase 3: Domain & Advanced 🔲 PLANNED

| # | Task | Status | Notes |
|---|------|--------|-------|
| 13 | Enterprise domain binding + DNS verification | 🔲 | Verify domain ownership via TXT record. See Section 6 for full flow. |
| 14 | Enterprise group/model configuration | 🔲 | Per-enterprise model group overrides. See API routes in Section 10. |
| 15 | Per-member quota caps | 🔲 | Limit individual member usage within enterprise pool |
| 16 | Enterprise quota pool distribution | 🔲 | Auto-distribute quota across members |
| 17 | System admin override audit trail | 🔲 | Log all system admin actions on enterprises. See Section 2 for access matrix. |
| 18 | Activity/audit log for enterprise admin | 🔲 | Dashboard-visible audit trail beyond role changes |

### Not Yet Planned / Out of Scope

- `enterprise_invitations` enhanced schema with `email_confirmed`/`admin_approved` columns (currently uses simpler model)
- `enterprise_domains` full implementation with DNS TXT verification
- `api_keys.enterprise_id` + `api_keys.assigned_user_id` columns (enterprise key ownership)
- `enterprises.deleted_at/deleted_by/delete_reason` soft delete columns
- `enterprise_members.remove_reason` column
- 4 performance indexes (idx_enterprise_members_active, idx_enterprise_invitations_status, idx_api_keys_enterprise, idx_enterprises_admin_vacant)
- 6 system-admin API routes (enterprise view, update, delete, restore, topup, override)
- 4 enterprise self-service routes (groups, domains, domain verify)

---

## 14. Implementation Status

### Phase 1 Fixes (2026-05-22)

- Invitation email converted to HTML (fixes QQ mail URL truncation at `?`)
- Existing user invitation: direct add to enterprise_members + notification email (no broken acceptURL)
- Default role: `enterprise_user` → `member` (matches constraint)
- Invitation URL: `localhost:port/enterprise/accept?token=` → `BaseURL/api/enterprise/accept?token=`
- Template rendering: custom `renderPage()` using `template.ParseFS(web.FS, ...)` instead of Gin's `c.HTML()`
- enterprise_members.enterprise_id FK fixed to reference enterprises(id), not users(id)

### Phase 2 API Routes (2026-05-22)

```
# Enterprise self-service (auth + enterprise member required)
GET    /api/enterprise                        → getMyEnterprise
GET    /api/enterprise/members                 → getMyEnterpriseMembers
GET    /api/enterprise/usage                   → getMyEnterpriseUsage
DELETE /api/enterprise/members/:userId         → removeMyEnterpriseMember (admin only)
POST   /api/enterprise/members/:userId/topup   → topupMemberQuota (admin only)
POST   /api/enterprise/members/:userId/toggle  → toggleMemberStatus (admin only)
PUT    /api/enterprise/members/:userId/role    → promoteDemoteMember (admin only)
POST   /api/enterprise/quit                    → quitEnterprise
GET    /api/enterprise/keys                    → listEnterpriseKeys
POST   /api/enterprise/keys                    → createEnterpriseKey (admin only)
PUT    /api/enterprise/keys/:keyId             → reassignKey (admin only)
GET    /api/enterprise/audit                   → listEnterpriseAuditLog (admin only)

# Enterprise invitation management (admin only)
GET    /api/enterprise/invitations              → listMyEnterpriseInvitations
PUT    /api/enterprise/invitations/:invId/approve → approveInvitation
PUT    /api/enterprise/invitations/:invId/reject  → rejectInvitation

# System admin
PUT    /api/admin/enterprise/:id/appoint-admin  → appointAdmin (for admin_vacant enterprises)
```

### Phase 2 New Handlers (2026-05-22)

| Handler | Description |
|---|---|
| `promoteDemoteMember` | Changes member role with max_admins check, syncs users.role, logs to enterprise_member_role_log |
| `quitEnterprise` | Member quits, recalls keys, if last admin → sets enterprise to admin_vacant |
| `approveInvitation` / `rejectInvitation` | Enterprise admin approves/rejects email_confirmed invitations |
| `appointAdmin` | System admin appoints member as admin for admin_vacant enterprises |
| `listEnterpriseKeys` / `createEnterpriseKey` / `reassignKey` | Enterprise key CRUD with visibility rules |
| `listEnterpriseAuditLog` | Returns enterprise_member_role_log entries for the enterprise |

### Mail Methods Added (2026-05-22)

- `SendEnterpriseAddedNotification(to, enterpriseName)` — HTML notification for existing users added to enterprise
- `SendAdminVacantReminder(to, summary)` — HTML reminder for system admins about admin_vacant enterprises
- `sendHTML()` / `buildHTMLMessage()` / `buildMessageWithContentType()` — HTML email infrastructure

### Database Schema Actual (2026-05-22)

Differences from the full data model design (Section 11):
- `enterprise_members.joined_at` added
- `enterprise_members` has `status` column (was in existing table already)
- `api_keys.enterprise_id` and `api_keys.assigned_user_id` **not yet added** — enterprise keys are distinguished by key prefix `sk-wgit-en-*` and `key_type='enterprise'`
- `enterprise_domains` table **not yet created** (Phase 3)
- `enterprises` soft delete columns (`deleted_at`, `deleted_by`, `delete_reason`) **not yet added**
- `enterprise_members.remove_reason` **not yet added**
- 4 indexes **not yet created**
- `enterprise_invitations` uses simpler schema (no `email_confirmed`/`admin_approved` split)

### Phase 2 Frontend Fixes (2026-05-23)

- **Template data binding**: `{{range .Data}}` → `{{range .Data.Members}}` — gin.H map requires explicit key access
- **CSS variable compliance**: All enterprise pages and apikeys page use only CSS variables defined in `app.css` (`--text`, `--text-muted`, `--bg`, `--bg-card`, `--bg-hover`, `--border`, `--primary`, etc.). Removed undefined variables (`--text-primary`, `--bg-secondary`, `--bg-input`).
- **Custom modal system**: Replaced browser `alert()`/`confirm()` with global `.modal-overlay` + `.modal` classes from `app.css`. Proper light/dark support via CSS variables.
- **Table styling**: Enterprise pages use `data-table` + `data-table-wrapper` classes instead of bare `<table>`. Matches admin/users, apikeys, orders pages.
- **Enterprise key NULL group_id**: Conditional INSERT — two separate SQL queries depending on whether `group_id` is provided
- **Member key visibility split**: `/apikeys` page split into "个人密钥" (personal keys via `GetAPIKeys`) and "企业分配密钥" (enterprise assigned keys via `GetAssignedEnterpriseKeys`)
- **AssignedEnterpriseKey struct**: New struct with `EnterpriseName` field, JOINs `enterprises` table for display

---

## 15. Enterprise Billing & Quota Mechanism

### Core Design: Two User Systems on One Portal

The platform serves **two distinct user modes** under the same login:

| Mode | Billing Model | Products | Keys |
|---|---|---|---|
| **Personal** | Pre-paid (topup → consume) | general, economy, vip, subscription | `sk-wgit-*`, `sk-wgit-tp-*` |
| **Enterprise** | **Post-paid** (company pays later) | enterprise groups | `sk-wgit-en-*` |

A user with the same email can be both a personal consumer AND an enterprise member. The two roles are independent:
- Personal: topup, buy subscriptions, create personal keys, consumption billed to personal balance
- Enterprise: consume via enterprise-assigned keys, billed to enterprise credit, company pays

### Post-Paid Dual-Track System

Enterprise consumption is tracked on **two independent dimensions**:

```
API Call (enterprise key)
  │
  ├─ Billing Path: Gift → Enterprise Credit → Personal Balance
  │    (webhook.go:655-700 in processRecord)
  │
  ├─ Token Tracking: enterprises.quota_used += tokens per request
  │    (webhook.go:918-923 in checkEnterpriseLimits)
  │
  └─ Alerting: 80%/90%/100% thresholds on both dimensions
       (webhook.go:927-949 — email + enterprise_alerts table)
```

| Dimension | DB Column | Unit | Updated | Blocks Requests? |
|---|---|---|---|---|
| Token Quota | `enterprises.quota_total/used` | raw tokens | Per API call | No (alert only) |
| Financial Credit | `enterprises.credit_total/used` | micro-USD | Per API call | No (consumed before personal balance) |

### Current Webhook Billing Path (`webhook.go:643-733`)

For `key_type == "enterprise"`, the billing path is:
1. **Gift balance** first — deduct from `users.gift_balance`
2. **Enterprise credit** second — deduct from `enterprises.credit_used` (up to `credit_total`)
3. **Personal balance** last — deduct from `users.balance` for remaining cost
4. Log in `balance_transactions` with `balance_type = 'credit'`
5. Call `checkEnterpriseLimits` for token quota + credit threshold alerts

### Current Enterprise Billing Page Data (`web/data.go:1397-1460`)

`GetEnterpriseBillingData` returns:
- Token quota: `quota_total`, `quota_used`, `quota_remaining`
- `billing_cycle`
- Per-member usage: current month `usage_records` aggregated by user
- Invoices: from `invoices WHERE enterprise_id = $1`

**Credit data is NOT shown.** `credit_total`/`credit_used` are not returned to the enterprise billing page.

### Member Quota Topup — No Enterprise Pool

Current `topupMemberQuota` (`enterprise.go:294-332`):

```go
// Current: creates quota from nothing
UPDATE users SET quota_total = quota_total + N, quota_remaining = quota_remaining + N
```

There is **no deduction from an enterprise pool**. Enterprise-level `quota_total`/`quota_used` on the `enterprises` table is completely independent from member-level `users.quota_total`/`users.quota_used`. Enterprise admin can arbitrarily grant tokens without any enterprise-level constraint.

---

## 16. Current Gaps & Solution Design

### Gap A: Credit Invisible on Billing Page (HIGH) — ✅ FIXED

**Problem**: Enterprise admin cannot see `credit_total`/`credit_used` on `/enterprise/billing`. Post-paid customers have zero financial transparency.

**Root cause**: `GetEnterpriseBillingData` queries `quota_total`/`quota_used` but not `credit_total`/`credit_used`.

**Solution**: `getEnterpriseBillingDetails` API returns full credit data. Billing page displays credit total/used/remaining. ✅ Done.

### Gap B: No Monthly Reset of Quota/Credit (HIGH) — ✅ FIXED

**Problem**: `billing_cycle = 'monthly'` is set on enterprises, and `credit_reset_at` column exists, but **nothing resets `quota_used` or `credit_used`**. Both counters grow infinitely.

**Solution**: `ResetMonthlyBilling` cron runs daily, resets `quota_used` and `credit_used` to 0 when `credit_reset_at <= NOW()` for `billing_cycle='monthly'` enterprises. Next reset set to +1 month. ✅ Done.

### Gap C: Member Topup Does Not Deduct from Enterprise Pool (HIGH) — ✅ FIXED

**Problem**: `topupMemberQuota` creates quota from nothing. No relationship between `enterprises.quota_total` and member quotas.

**Solution**: `topupMemberQuota` now uses a transaction: locks enterprise row with `FOR UPDATE`, checks `quota_total - quota_used >= requested`, deducts from enterprise pool (`quota_used += N`), then adds to member. Returns error if insufficient. ✅ Done.

### Gap D: No Enterprise Billing Details API (MEDIUM) — ✅ FIXED

**Problem**: Enterprise admin cannot programmatically retrieve enterprise-level consumption data.

**Solution**: `GET /api/enterprise/billing/details` returns quota, credit, usage_by_member, invoices. Auth: `requireEnterpriseAdmin`. ✅ Done.

### Gap E: Enterprise Group Management (MEDIUM) — ✅ IMPLEMENTED

Enterprise group CRUD fully implemented with platform group linkage (`parent_group_id`), dynamic model selection from registry, visibility (`public`/`admin_only`), and rate limits. Proxy untouched — all constraints resolved at key creation time. ✅ Done.

#### Group Model Selection — Dynamic from Platform Registry

**Design decision after discussion (2026-05-23)**: Enterprise groups should NOT allow manual model name entry. Instead, model selection pulls from the platform's model registry (`service_models WHERE status IN ('active', 'eol_pending', 'eol')`) dynamically on every modal open.

Rationale:
- Manual model entry is error-prone (typos, outdated names)
- Platform models change frequently (version upgrades, EOL)
- Enterprise can only use models that exist on the platform
- `admin/groups` enterprise-type groups control rate limits, not model catalogs

**Implementation**:
```
Enterprise Admin clicks "新建分组/编辑" →
  Modal fetches GET /api/enterprise/available-models →
    Queries service_models WHERE status IN ('active', 'eol_pending', 'eol') →
  Renders checkboxes with current model list →
  Admin selects models → saves to user_groups.models JSONB
```

**Enterprise group fields**:
| Field | Type | Default | Description |
|---|---|---|---|
| Name | string | required | Display name |
| Models | []string | [] | Selected from platform registry. Empty = no restriction (all models) |
| RPM | int | 500 | Rate limit per minute |
| TPM | int64 | 0 | Token limit per minute (0 = unlimited, ⚠ warning) |
| Concurrent | int | 10 | Max simultaneous connections |

**Why TPM defaults to 0**: Incorrect TPM configuration severely impacts performance and user experience. RPM alone is sufficient for most use cases. TPM is available but with a warning in the UI.

**Model lifecycle sync**: See Section "Model Lifecycle Synchronization" below.

#### Member-Group Association

Enterprise member ↔ group is associated through **API key**, not a separate table:
```
Enterprise Group (models: [gpt-4o, claude])
  └→ API Key (group_id = group.id, assigned_user_id = member.id)
       └→ Member uses key → proxy routes via group's model list
```
The key is the sole permission carrier. No additional member-group mapping needed.

#### Platform Groups vs Enterprise Groups — No Overwrite

`admin/groups` with `group_type='enterprise'` defines **platform-level enterprise groups** (rate limits, pricing, billing). `enterprise/groups` defines **enterprise-internal groups** (model selection, member RPM/TPM limits). These are independent layers — enterprise groups do not overwrite platform groups. Enterprise groups have no `pricing_ratio` (inherit from `enterprises.pricing_ratio`).

**Implemented files:**
1. `GET/POST/PUT/DELETE /api/enterprise/groups` — CRUD with `concurrent_limit` field
2. `GET /api/enterprise/available-models` — dynamic model list from `service_models`
3. `web/templates/enterprise/groups.html` — full CRUD UI with model checkboxes
4. `user_groups.enterprise_id` column — enterprise group ownership (migration: 202605231000)
5. `user_groups.concurrent_limit` — imported from existing schema
6. Proxy: **zero changes**

### Gap F: Enterprise Subscription/Plan Management (MEDIUM) — DEFERRED

Deferred for future phase. Same proxy-safe pattern as Gap E. Enterprise subscription plans work identically to personal plans — the only difference is the assigner (enterprise admin vs user self-service).

### Gap G: No Enterprise Orders/Billing API Routes (LOW) — ✅ IMPLEMENTED

`GET /api/enterprise/billing/details` returns full billing data including credit, quota, per-member usage, and invoices. ✅ Done.

### Gap H: Middleware Inconsistency (LOW) — DEFERRED

`middleware.Auth.RequireEnterpriseAdmin()` (`auth.go:145-148`) is defined but never used. Enterprise routes use a local `requireEnterpriseAdmin()`. Unification deferred — local closure works correctly.

---

## 17. Implementation Priority Roadmap (Original Planning)

> **Note**: See Section 21 for updated roadmap with completion status. Items A-E, plus additional enhancements, are now implemented.

| Priority | # | Task | Files | Proxy Impact |
|---|---|---|---|---|
| **P0** | A | Enterprise billing page: add credit consumption card | `web/data.go`, `enterprise/billing.html` | None |
| **P0** | B | Monthly quota/credit reset cron | `cmd/server/main.go` | None |
| **P0** | C | Member topup from enterprise pool (deduct, not create) | `enterprise.go:topupMemberQuota` | None |
| **P1** | D | `GET /api/enterprise/billing/details` endpoint | `enterprise.go`, `data.go` | None |
| **P1** | E | Enterprise group management (key-creation filter, proxy untouched) | `enterprise.go`, `enterprise/keys.html` | None |
| **P1** | F | Enterprise subscription/plan management | TBD | None |
| **P2** | G | Enterprise orders/billing REST API | `enterprise.go` | None |
| — | H | Middleware unification (handle during any refactor) | `auth.go`, `enterprise.go` | None |
| — | — | Enterprise pricing_ratio (system/superadmin managed) | New: `enterprise.go`, `apikeys.go`, `webhook.go` | None |
| — | — | EOL model filtering at key validation time | New: `apikeys.go` | None |
| — | — | my-group page: personal + enterprise split | New: `web/data.go`, `my-group.html` | None |
| — | — | Enterprise credit 110% hard cap + personal balance removal | New: `apikeys.go`, `webhook.go`, `auth.go` | Proxy (1 line) |

**Key architectural constraint**: No item in this roadmap touches the Proxy. All enterprise-level constraints (groups, subscriptions, quotas, plans) are enforced at key creation time in the Portal. The key remains the single permission and routing artifact in the request path.

---

## 18. Enterprise Pricing Ratio Architecture

### Design

Enterprise customers may have negotiated pricing different from the public rate. This is managed at the enterprise level by system/superadmin — enterprise admins cannot modify it, only view it.

```
Pricing chain for enterprise key:
  Base model cost (from service_models pricing)
    → × enterprise.pricing_ratio (set by system_admin, e.g. 0.8 = 20% discount)
      → Final cost charged to enterprise credit

Pricing chain for personal key:
  Base model cost (from service_models pricing)
    → × group.pricing_ratio (set by admin, group-level discount/premium)
      → Final cost charged to user balance
```

### Implementation Points

| Location | What | File |
|---|---|---|
| **DB Schema** | `enterprises.pricing_ratio NUMERIC(5,4) DEFAULT 1.0000` | Migration: `202605231000_enterprise_enhancement_phase2_1.sql` |
| **Key validation** | Override `pricingRatio` with `enterprises.pricing_ratio` when `key_type = 'enterprise'` | `apikeys.go` validate handler |
| **Webhook billing** | Undo group pricing_ratio, apply enterprise pricing_ratio for enterprise keys | `webhook.go` processRecord + processStats |
| **Enterprise billing page** | Shows effective ratio to enterprise admin (read-only) | `enterprise/billing.html` |
| **Proxy impact** | Zero — proxy receives the already-adjusted `pricingRatio` in the validation response | N/A |

### Why Enterprise Groups Don't Have pricing_ratio

Enterprise groups select models and set rate limits. Pricing is an enterprise-level concern (negotiated with the customer), not a per-group concern. Multiple enterprise groups sharing the same pricing prevents internal billing confusion.

---

## 19. Model Lifecycle Synchronization

### Problem

Enterprise groups store model names as a JSONB array at creation time. When the platform admin EOLs or disables a model in `service_models`, enterprise groups retain stale model names. Without synchronization, members using enterprise keys would encounter failures only when the proxy tries to route to a non-existent/EOL model.

### Solution: Dual-Layer Protection

| Layer | When | Mechanism |
|---|---|---|
| **UI layer** | Group edit/create modal opens | `GET /api/enterprise/available-models` fetches fresh `service_models WHERE status IN ('active', 'eol_pending', 'eol')` every time. Enterprise admin sees only serviceable models (excludes discontinued). |
| **Validation layer** | Key validation (`GET /api/apikeys/validate`) | `allowed_models` and `selected_models` cross-checked against `service_models WHERE status IN ('active', 'eol_pending', 'eol')`. EOL/discontinued models silently filtered out before returning to proxy. |

### Validation-Time Filter (apikeys.go)

```go
// After computing allowed_models from key + group, filter against serviceable models:
var activeModels map[string]bool
modelRows := r.db.Query("SELECT api_model_name FROM service_models WHERE status IN ('active', 'eol_pending', 'eol')")
// ... build activeModels set ...

// Filter allowed_models ([]string or []interface{})
switch am := allowedModels.(type) {
case []string:
    var filtered []string
    for _, m := range am {
        if activeModels[m] { filtered = append(filtered, m) }
    }
    allowedModels = filtered
}
// Same for selected_models
```

### Why This Works

1. **No DB migration needed** — enterprise groups keep their storage format (JSONB model list)
2. **Silent filtering** — EOL models are removed from proxy's view without error messages to end users
3. **Real-time** — Every key validation (typically cached for 5 min) gets a fresh model status check
4. **Proxy impact: zero** — proxy never sees filtered-out models

### Remaining Gap

When ALL models in a group are EOL'd and `allowed_models` becomes empty after filtering, the key is still marked `valid: true` with an empty model list. The proxy will attempt to route and may fail. A future enhancement could set `quota_exceeded = true` or `valid = false` in this edge case.

---

## 20. my-group Page — Personal vs Enterprise Split

The `/my-group` page was redesigned to show personal and enterprise groups separately, matching the `/apikeys` pattern (personal keys + enterprise assigned keys in two sections).

### Structure

| Section | Content | Visibility |
|---|---|---|
| **个人可用分组** | Platform public groups (general, economy, subscription, vip) | All users |
| **企业分组** | Enterprise-owned groups (models + rate limits, no pricing_ratio) | Users with `enterprise_id` set |

### Design Decisions

- Enterprise section is **read-only** — enterprise group CRUD is on `/enterprise/groups` (admin only)
- Enterprise groups show **no pricing_ratio** (inherit from `enterprises.pricing_ratio`)
- Both sections use `data-table` styling (consistent with `/apikeys`, `/admin/groups`)
- `GetMyGroupData()` queries personal groups and enterprise groups in **separate queries** for clarity

### Data Model

```go
type MyGroupData struct {
    UserGroupID      string       // User's personal group assignment
    UserGroupName    string
    Groups           []GroupInfo  // Personal (platform) groups
    EnterpriseGroups []GroupInfo  // Enterprise-owned groups
    HasEnterprise    bool
}
```

---

## 21. Updated Implementation Priority Roadmap

| Priority | # | Task | Status | Files | Proxy Impact |
|---|---|---|---|---|---|
| **P0** | A | Enterprise billing page: add credit consumption card | ✅ Done | `web/data.go`, `enterprise/billing.html` | None |
| **P0** | B | Monthly quota/credit reset cron | ✅ Done | `cmd/server/main.go`, `enterprise.go` | None |
| **P0** | C | Member topup from enterprise pool (deduct, not create) | ✅ Done | `enterprise.go:topupMemberQuota` | None |
| **P1** | D | `GET /api/enterprise/billing/details` endpoint | ✅ Done | `enterprise.go` | None |
| **P1** | E | Enterprise group management (model selector, key-creation filter) | ✅ Done | `enterprise.go`, `enterprise/groups.html`, `enterprise/keys.html` | None |
| **P1** | F | Enterprise subscription/plan management | 🔲 Deferred | TBD | None |
| **P2** | G | Enterprise orders/billing REST API | ✅ Done | `enterprise.go` | None |
| **P1** | — | Soft-delete enterprise (was hard delete) | ✅ Done | `enterprise.go:deleteEnterprise` | None |
| **P1** | — | Restore deleted enterprise | ✅ Done | `enterprise.go:restoreEnterprise` | None |
| **P1** | — | removeMember soft-delete + key recall | ✅ Done | `enterprise.go:removeMyEnterpriseMember`, `removeMember` | None |
| **P1** | — | max_members enforced in API invite | ✅ Done | `enterprise.go:inviteMember` | None |
| **P2** | — | quota_limit enforced in validateKey | ✅ Done | `apikeys.go` | None |
| **P2** | — | Enterprise alert email to ALL admins | ✅ Done | `webhook.go` | None |
| **P2** | — | getMyEnterprise returns pricing_ratio | ✅ Done | `enterprise.go:getMyEnterprise` | None |
| **P2** | — | System admin topup enterprise pool | ✅ Done | `enterprise.go:topupEnterprisePool` | None |
| **P2** | — | System admin override with audit trail | ✅ Done | `enterprise.go:overrideEnterprise` | None |
| **P1** | — | Non-domain invite 2-step approval flow | ✅ Done | `enterprise.go:acceptInvitation`, `approveInvitation` | None |
| — | — | Enterprise key: delete/reactivate/edit UI | ✅ Done | `enterprise/keys.html`, `admin_handlers.go` | None |
| — | — | Enterprise key generation unified (UUID format) | ✅ Done | `enterprise.go` | None |
| — | — | Enterprise pricing_ratio (system/superadmin managed) | ✅ Done | `enterprise.go`, `apikeys.go`, `webhook.go`, migration | None |
| — | — | EOL model lifecycle filter at key validation time | ✅ Done | `apikeys.go` validate handler | None |
| — | — | my-group page: personal + enterprise split | ✅ Done | `web/data.go`, `my-group.html` | None |
| — | — | Enterprise credit 110% hard cap + remove personal balance fallback | ✅ Done | `apikeys.go`, `webhook.go`, `auth.go` (proxy) | Proxy (1 line) |
| — | — | Enterprise page styling fix (CSS variable compliance) | ✅ Done | `enterprise/users.html`, `enterprise/keys.html`, `apikeys.html` | None |
| — | — | Enterprise groups: parent/child platform group model | ✅ Done | `enterprise.go`, `enterprise/groups.html`, migration, `data.go` | None |
| — | — | Enterprise key: route via platform group, apply enterprise limits | ✅ Done | `enterprise.go:createEnterpriseKey` | None |
| — | — | Platform group details visible in enterprise group modal | ✅ Done | `enterprise.go:getPlatformGroups`, `enterprise/groups.html` | None |
| — | — | Effective pricing ratio display in groups table | ✅ Done | `data.go:GetEnterpriseGroups`, `enterprise/groups.html` | None |
| — | — | Email URL audit: accept invite, password reset, admin alerts | ✅ Done | `enterprise.go`, `pages.go`, `mail.go`, `models.go` | None |
| — | — | Invitation role mapping (member→user, admin→enterprise_admin) | ✅ Done | `enterprise.go:acceptInvitation` | None |
| — | — | requireEnterpriseAdmin middleware sets enterprise_id on context | ✅ Done | `enterprise.go:requireEnterpriseAdmin` | None |
| — | — | Enterprise group visibility: public vs admin_only | ✅ Done | migration, `enterprise.go`, `data.go`, `groups.html` | None |
| — | — | Enterprise key creation: group selector + unassigned option | ✅ Done | `enterprise/keys.html`, `admin_handlers.go` | None |
| — | — | Enterprise dashboard pricing ratio badge | ✅ Done | `data.go`, `enterprise/dashboard.html` | None |
| — | — | Enterprise users: auto-invite non-existent email | ✅ Done | `admin_handlers.go:HandleAddEnterpriseUser` | None |
| — | — | Enterprise keys table: group column, time format, edit group | ✅ Done | `data.go`, `enterprise/keys.html`, `enterprise.go:reassignKey` | None |
| — | — | /apikeys enterprise keys: show self-created vs admin-assigned | ✅ Done | `data.go:AssignedEnterpriseKey`, `apikeys.html` | None |
| **P2** | — | Enterprise key creation dropdown filter (visibility) | ✅ Done | `groups.go:listUserGroups` | None |
| **P2** | — | /my-group: show only public enterprise groups | ✅ Done | `data.go:GetMyGroupData` | None |
| **P2** | — | Per-member quota cap: validate enforcement | ✅ Done | `apikeys.go:validateKey` | None |
| **P2** | — | Per-member quota cap: API endpoint + UI | ✅ Done | `enterprise.go:setMemberQuotaLimit`, `users.html` | None |
| **P3** | — | Soft delete columns (enterprises + enterprise_members) | ✅ Done | Phase 1 migration | None |
| **P3** | — | Performance indexes (4 indexes) | ✅ Done | Phase 1 + Phase 2.1 migrations | None |

### Remaining Work

| Priority | Task | Description |
|---|---|---|
| **P3** | Enterprise domains (Phase 3) | DNS TXT verification deferred — company qualification verified offline |

---

## 22. Key Files

| File | Description |
|---|---|
| `internal/routes/enterprise.go` | All enterprise handlers + middleware |
| `internal/web/data.go` | Data queries including `GetAssignedEnterpriseKeys`, `AssignedEnterpriseKey` struct |
| `internal/web/pages.go` | Page handlers, `handleAPIKeys` with split key data |
| `internal/web/admin_handlers.go` | Admin enterprise key creation with conditional INSERT |
| `pkg/mail/mail.go` | Email sending (text + HTML) |
| `web/templates/enterprise/users.html` | Member management (custom remove modal, CSS-variable-compliant) |
| `web/templates/enterprise/keys.html` | Key management (custom modal, key-display CSS class) |
| `web/templates/apikeys.html` | Split personal + enterprise key sections |
| `web/templates/enterprise-accept.html` | Invitation accept page |
| `web/static/css/app.css` | Global CSS with light/dark theme variables |
| `migrations/202605221600_enterprise_enhancement_phase1.sql` | Phase 1 migration |
| `migrations/202605230000_enterprise_phase2.sql` | Phase 2 migration |
| `cmd/server/main.go` | Background cron setup |

---

## 23. Changelog

| Date | What |
|---|---|
| 2026-05-22 | Initial design doc, Phase 1 migration (soft delete, admin_vacant, role audit, indexes) |
| 2026-05-23 | Phase 2 migration (enterprise groups, visibility, parent/child model, pricing_ratio, concurrent_limit, quota_limit) |
| 2026-05-23 | P0/P1 complete: billing page, credit reset cron, member topup, group management, EOL filter, my-group split, credit 110% cap, pricing_ratio, email URL audit |
| 2026-05-24 | **P2 complete:** Group visibility filter, per-member quota cap enforcement, quota-limit API + UI |
| 2026-05-24 | **Fix:** Model dropdown broken for non-admin users — `/api/admin/models/grouped/public` |
| 2026-05-24 | **Fix:** Enterprise group visibility badge on `/enterprise/keys` dropdown |
| 2026-05-24 | **Fix:** Enterprise group scoping — exclude platform enterprise parent groups, show all for superadmin |
| 2026-05-24 | **Fix:** Subscription key — gate by actual subscription + Chinese error message |
| 2026-05-24 | **Fix:** Admin /groups split into 系统用户组 + 企业自建组 sections |
| 2026-05-24 | **Fix:** Conflicting CHECK constraints on api_keys.key_type — subscription blocked |
| 2026-05-24 | **Fix:** Personal key "复制" now fetches full decrypted key (matching enterprise keys) |
| 2026-05-24 | **Fix:** Enterprise key view blocked for regular members — added `/apikeys/:id/view` |
| 2026-05-24 | **Fix:** Enterprise key INSERT — `user_id` (creator) separated from `assigned_user_id` (assignee) |
| 2026-05-24 | **Fix:** Enterprise key table columns — added 模型 column, removed 创建时间 |
| 2026-05-24 | **Fix:** Models display — resolve key→group fallback, show total active count |
| 2026-05-24 | **Fix:** `/my-group` personal groups also show "全部模型 (N)" |
| 2026-05-24 | **Fix:** `/admin/groups` ModelCount=0 → "全部模型" |
| 2026-05-24 | **Fix:** Enterprise keys dedup — personal section excludes enterprise-bound keys; enterprise section requires `enterprise_id IS NOT NULL` |
| 2026-05-24 | **Fix:** NULL scan failures — `COALESCE(is_self_created, false)`, `Models *string` |
| 2026-05-24 | **Fix:** Enterprise key `group_id` always points to enterprise group (not platform parent) — fixes model display + rate limits |
| 2026-05-24 | **Fix:** `/enterprise/keys` edit — switched from JWT API to web form POST (fixes "Invalid or expired token") |
| 2026-05-24 | **Fix:** Enterprise key model display — rendered as individual code tags, not raw JSON |
| 2026-05-24 | **Fix:** Model dropdown on `/apikeys` — only populated after group selected (blocks leaking all service models) |
| 2026-05-24 | **Fix:** `createKey` handler sets `enterprise_id` + `assigned_user_id` when creating enterprise-type keys |
| 2026-05-24 | **Fix:** Webhook now increments `users.quota_used` for enterprise keys — enables per-member quota enforcement |
| 2026-05-24 | **Fix:** `deleteEnterpriseGroup` uses soft delete (`status = 'deleted'`) instead of hard DELETE |
| 2026-05-24 | **DB flushed** — `clean-all` + `mvp`, fresh DB with corrected schema and fixed handlers |
| 2026-05-24 | **Code Review Round 1 Fixes (Proxy):** EventStream parser now uses proper binary framing instead of brute-force brace scanning; SigV4 signing supports `x-amz-security-token` (IAM Role temp credentials); `MapModelToBedrock` supports dynamic mapping table via `SetModelMapping`; `ToProvider` supports Tool Use / Function Calling; `bedrockProviders` list is runtime-configurable via `SetBedrockProviders`; deprecated `AffinityCache` methods now log warnings |
| 2026-05-24 | **Code Review Round 1 Fixes (Portal):** `syncBedrockModels` insert/update count uses PostgreSQL `xmax` instead of `created_at` heuristic; `topupEnterprisePool` validates negative values and checks enterprise exists/not deleted; `overrideEnterprise` checks enterprise exists/not deleted before applying; `recordUsageAsyncFromContext` now correctly reads `routing_track` from gin context; fallback from Bedrock to standard updates `routing_track` to `"standard"` and sets `original_track="bedrock"` |
| 2026-05-24 | **Code Review Round 1 Fixes (Docs):** `resolveBedrockProviderAndKey` outdated comment updated to reflect actual RPM/TPM filtering via `GetKeysForProvider` → `checkUpstreamAvailable` |

### Key Design Decisions for /apikeys Group Dropdown

| User Role | Platform Groups (personal key) | Enterprise Groups (enterprise key) |
|---|---|---|
| system_admin / super_admin | general, economy, subscription, vip | All enterprise groups (test/validation) |
| enterprise_admin | general, economy, subscription, vip | Own enterprise (public + admin_only) |
| enterprise member | general, economy, subscription, vip | Own enterprise (public only) |
| regular user (no enterprise) | general, economy, subscription, vip | None |

- Platform parent groups with `groupType = 'enterprise'` are excluded from ALL users — they're internal plumbing for enterprise group → platform group routing, not user-selectable.
- Enterprise groups are scoped by `g.enterprise_id = user's enterprise_id`. Platform groups are `g.enterprise_id IS NULL AND g.group_type != 'enterprise'`.
- The `/enterprise/keys` admin page uses `GetEnterpriseGroups()` which returns ALL enterprise groups (public + admin_only) for the enterprise admin. This is separate from `/api/groups` used by `/apikeys`.

---

## 24. Remaining Todo

| # | Priority | Task | Status |
|---|---|---|---|
| 1 | **P3** | Enterprise domains — DNS TXT verification, domain binding | 🔲 Deferred (offline) |
| 2 | — | Member self-created enterprise key — `enterprise_id` now set in `createKey` | ✅ Done |
| 3 | — | Enterprise key `group_id` scoped to enterprise group (not platform parent) | ✅ Done |
| 4 | — | Model dropdown gated by group selection (no leak) | ✅ Done |
| 5 | — | Per-member quota tracking — webhook increments `users.quota_used` | ✅ Done |
| 6 | — | Soft-delete enterprise + restore API | ✅ Done |
| 7 | — | removeMember soft-delete + key recall | ✅ Done |
| 8 | — | max_members enforced in API invite | ✅ Done |
| 9 | — | quota_limit enforced in validateKey | ✅ Done |
| 10 | — | Non-domain invite 2-step approval | ✅ Done |
| 11 | — | Enterprise alert to all admins | ✅ Done |
| 12 | — | System admin topup + override | ✅ Done |
| 13 | — | getMyEnterprise returns pricing_ratio | ✅ Done |
| 6 | — | Enterprise group soft delete — hard DELETE replaced with `status = 'deleted'` | ✅ Done |
