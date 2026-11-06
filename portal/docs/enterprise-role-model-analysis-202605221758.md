> **⚠️ DRAFT** — Enterprise role model as of 2026-05-22

# Enterprise Role Model Analysis

## Overview

The system has **two separate role systems** that intersect for enterprise users:

| Scope | Table | Column | Allowed Values |
|---|---|---|---|
| System-wide | `users` | `role` | `user`, `enterprise_admin`, `system_admin`, `super_admin` |
| Enterprise-local | `enterprise_members` | `role` | `member`, `admin` |

These two role systems are independent — a user's `users.role` and
`enterprise_members.role` are not cross-referenced or validated against each
other.

---

## 1. System-wide Roles (`users.role`)

Defined in `migrations/001_core_tables.sql` with a CHECK constraint:

```sql
role VARCHAR(20) DEFAULT 'user'
  CHECK (role IN ('user', 'enterprise_admin', 'system_admin', 'super_admin'))
```

### Numeric Hierarchy

`internal/middleware/auth.go:173-178`:

| Role | Level | Description |
|---|---|---|
| `user` | 1 | Regular end-user |
| `enterprise_admin` | 2 | Enterprise-level admin (system-wide role) |
| `system_admin` | 10 | Platform admin |
| `super_admin` | 100 | Root admin |

Used by `RequireEnterpriseAdmin()` (level >= 2) and `RequireAdmin()` (exact
match: `system_admin` or `super_admin` only).

### Route Protection Matrix

| Route Group | Middleware | Allowed `users.role` |
|---|---|---|
| `/api/admin/*` | `RequireAdmin` | `system_admin`, `super_admin` |
| `/api/enterprise/*` (self-service) | Custom `requireEnterpriseAdmin` | `enterprise_admin`, `system_admin`, `super_admin` |
| `/api/enterprise/*` (general) | No admin middleware | Any with `enterprise_id` set |
| Web `/admin/*` | Page middleware | `system_admin`, `super_admin` |
| Web `/enterprise/*` | Page middleware | `enterprise_admin`, `system_admin`, `super_admin` |
| Web `/super/*` | Page middleware | `super_admin` only |

**Key point:** `enterprise_admin` (level 2) can access enterprise self-service
routes (`/api/enterprise/*`) but **cannot** access system admin routes
(`/api/admin/*`).

---

## 2. Enterprise-local Roles (`enterprise_members.role`)

Defined in `migrations/008_v3_schema.sql`:

```sql
CREATE TABLE IF NOT EXISTS enterprise_members (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enterprise_id UUID NOT NULL REFERENCES enterprises(id) ON DELETE CASCADE,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role          VARCHAR(20) DEFAULT 'member'
                    CHECK (role IN ('member', 'admin')),
    status        VARCHAR(20) DEFAULT 'active'
                    CHECK (status IN ('active', 'inactive')),
    created_by    UUID REFERENCES users(id),
    created_at    TIMESTAMP DEFAULT NOW(),
    updated_at    TIMESTAMP DEFAULT NOW(),
    UNIQUE(enterprise_id, user_id)
);
```

### Role Meanings

| `enterprise_members.role` | Permissions |
|---|---|
| `member` | Default. Can use enterprise resources. Cannot manage members. |
| `admin` | Can manage members (invite/remove/toggle/topup) via `/api/enterprise/members/*` endpoints. |

---

## 3. Enterprise Invitations (`enterprise_invitations`)

Defined in `migrations/202605071200_phase3_enterprise_payment.sql`:

```sql
CREATE TABLE IF NOT EXISTS enterprise_invitations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enterprise_id UUID NOT NULL REFERENCES enterprises(id),
    email        VARCHAR(255) NOT NULL,
    token        VARCHAR(64) NOT NULL UNIQUE,
    role         VARCHAR(20) DEFAULT 'enterprise_admin',  -- NO CHECK constraint!
    status       VARCHAR(20) DEFAULT 'pending'
                    CHECK (status IN ('pending', 'accepted', 'expired', 'cancelled')),
    invited_by   UUID REFERENCES users(id),
    expires_at   TIMESTAMP NOT NULL,
    accepted_at  TIMESTAMP,
    created_at   TIMESTAMP DEFAULT NOW()
);
```

**Notable:** The `role` column has NO CHECK constraint, so any string is accepted
at the DB level.

---

## 4. Invite Flow

`internal/routes/enterprise.go` `inviteMember` handler (`POST /api/admin/enterprise/:id/invite`):

```
Request Body: { "email": "...", "role": "..." }

1. Validate admin (caller must be system_admin or super_admin)
2. Check if email already registered:
   a. YES → UPDATE users SET enterprise_id = :id, role = req.role
            (direct add, no email sent — BUG: no notification)
   b. NO  → INSERT into enterprise_invitations, send invitation email
3. Log audit event
```

### Known Bug in Invite Flow

The `inviteMember` handler defaults `role` to `"enterprise_user"` when empty:

```go
if req.Role == "" {
    req.Role = "enterprise_user"
}
```

But `"enterprise_user"` is NOT one of the allowed values in the `users.role`
CHECK constraint. Directly adding an existing user with this role will cause a
DB constraint violation.

### Existing User Path: No Email Notification (Fixed)

When an existing user is added directly (step 2a above), previously **no email
was sent**. This was fixed in commit `6c6275c` to call
`SendEnterpriseInvitation`.

---

## 5. Admin Panel: Web Form Add Member

`internal/web/admin_handlers.go` `HandleAddEnterpriseUser` (`POST /admin/enterprise/users`):

```
Form fields: email, role

1. Lookup user by email (must exist, else "用户不存在")
2. UPDATE users SET enterprise_id = :id, role = 'enterprise_user' (HARDCODED!)
3. INSERT INTO enterprise_members (..., role = form.role) with ON CONFLICT UPDATE
4. Send email notification (added in 20260522 fix)
```

**Bug:** Step 2 hardcodes `role = 'enterprise_user'` which does not match the
`users.role` CHECK constraint. Step 3 uses the form-provided role for
`enterprise_members.role`, which could be any string.

---

## 6. Unenforced Limits

Several limits exist in the database/config but are **never checked in code**:

| Limit | Location | Default | Enforced? |
|---|---|---|---|
| `max_members` | `enterprises.max_members` column | 50 | No |
| `enterprise_max_admins` | `system_settings` table | 3 | No |
| `MAX_SYSTEM_ADMINS` | Config env | 10 | No |
| `MAX_SUPER_ADMINS` | Config env | 3 | No |
| `MAX_ADMINS_MVP` | Config env | 5 | No |

---

## 7. Enterprise Admin Email Alerts

When quota thresholds are crossed (`internal/routes/webhook.go:996-1004`), the
system queries the **first** user with `role = 'enterprise_admin'` in the
enterprise:

```sql
SELECT u.email
FROM enterprises e
JOIN users u ON u.enterprise_id = e.id AND u.role = 'enterprise_admin'
WHERE e.id = $1::uuid LIMIT 1
```

If multiple enterprise admins exist, only one is picked arbitrarily.

---

## 8. Summary of Issues

| # | Issue | Severity | File |
|---|---|---|---|
| 1 | Invite default role `"enterprise_user"` violates CHECK constraint | High | `routes/enterprise.go` |
| 2 | Admin panel hardcodes `role = 'enterprise_user'` in UPDATE | High | `admin_handlers.go` |
| 3 | No role validation on invite/create endpoints | Medium | `routes/enterprise.go`, `admin_handlers.go` |
| 4 | `max_members` not enforced | Medium | Nowhere |
| 5 | `enterprise_max_admins` not enforced | Low | Nowhere |
| 6 | No `owner` role in enterprise hierarchy | Low | N/A |
| 7 | Invite modal offers `"enterprise_user"` as default | Medium | Frontend |
| 8 | `contact_email` on enterprises table is independent of any user | Info | N/A |
