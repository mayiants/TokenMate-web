# AI Proxy Portal: Node.js → Go Transition Plan

> **ACTIVE** — Updated: 2026-05-03  
> Working Dir: `/home/ubuntu/workspace/ai-portal-go`

---

## 1. Current State Analysis

### 1.1 Node.js Portal (Source of Truth)

**Backend**: ~10,668 LOC across 31 files

| Module | File | LOC | Status in Go |
|--------|------|-----|-------------|
| `database.js` | init + pool | 696 | ⚠️ Partial — 10 tables created, missing partitions |
| `auth.js` | login/register/reset | ~260 | ✅ Done — register, login, me, logout, change/reset password, send-code |
| `apikeys.js` | CRUD + validate | ~350 | ✅ Done — list, create, get, update, delete, validate |
| `webhook.js` | usage + heartbeat | 463 | ⚠️ Partial — single usage, batch queue, heartbeat. Missing balance deduction, subscription check |
| `admin.js` | users/analytics/keypool | 1,326 | ⚠️ Partial — list users, update user, analytics, upstream keys CR, providers CRU, service models RU, batch validate stub |
| `billing.js` | recharge/usage/cost | 499 | ❌ Missing — no billing, orders, invoices, cost calculator |
| `redemption.js` | codes/redeem | 502 | ❌ Missing |
| `subscriptions.js` | plans/subscribe | ~400 | ❌ Missing |
| `enterprise.js` | enterprise admin | 528 | ❌ Missing |
| `profile.js` | user profile | ~300 | ❌ Missing |
| `orders.js` | order management | ~200 | ❌ Missing |
| `invoices.js` | invoice apply | ~250 | ❌ Missing |
| `gdpr.js` | export/delete | ~150 | ❌ Missing |
| `devices.js` | device tracking | ~180 | ❌ Missing |
| `checkin.js` | daily check-in | ~200 | ❌ Missing |
| `sensitive.js` | word filtering | ~150 | ❌ Missing |
| `channel-health.js` | provider health | ~250 | ❌ Missing |
| `groups.js` | user groups | ~300 | ❌ Missing |
| `pricing.js` | model pricing | ~400 | ❌ Missing |
| `dashboard.js` | admin charts | 403 | ⚠️ Partial — basic stats only, no charts endpoints |
| `settings.js` | system settings | ~350 | ❌ Missing |
| `service-models.js` | model lifecycle | 1,080 | ⚠️ Partial — list, update, batch validate stub. Missing sync, EOL, approval, pause/resume |

**Frontend**: React SPA, ~76 files, served as static files

### 1.2 Current Go Portal (`ai-portal-go`)

**Already Implemented** (~1,800 LOC):

| File | Lines | What's Done |
|------|-------|-------------|
| `cmd/server/main.go` | 410 | Bootstrap, DB init, 10 default providers, default admin |
| `internal/config/config.go` | 130 | Env-based config (Server, DB, Redis, Auth, Admin, SMTP, Portal) |
| `internal/db/db.go` | 83 | pgxpool + go-redis wrapper |
| `internal/middleware/auth.go` | 165 | JWT auth, RequireAuth, OptionalAuth, RequireAdmin, RequireSuperAdmin, RequireEnterpriseAdmin, role hierarchy, CanPerformSensitiveOps |
| `pkg/jwt/jwt.go` | 61 | Generate + Verify with golang-jwt/v5 |
| `pkg/audit/audit.go` | 47 | Audit log insert with JSON details |
| `internal/routes/auth.go` | 277 | Register, login, me, logout, change-password, forgot-password, reset-password, send-code, password-requirements |
| `internal/routes/apikeys.go` | 299 | List, create, get, update, delete, validate (with balance + subscription check) |
| `internal/routes/webhook.go` | 183 | Usage single, usage-batch (Redis queue), heartbeat, queue processor |
| `internal/routes/admin.go` | 373 | List users, update user, analytics, admin-mode, upstream keys CR, providers CRU, service models RU, batch validate stub |
| `internal/routes/dashboard.go` | 98 | Basic dashboard stats (admin vs user scoped) |
| `internal/routes/status.go` | 80 | Health, status (DB + Redis + proxy), providers list |
| `internal/routes/sync.go` | 252 | Key-pool sync (for Proxy), models sync (for Proxy), X-Admin-Secret auth |
| `internal/routes/usage.go` | 94 | Usage records list, usage summary (today + month) |

**Total Go backend**: ~14 files, ~2,552 LOC

---

## 2. Missing Modules (Gap Analysis)

### 2.1 Critical — Must Have for Alpha

| # | Module | Node.js LOC | Complexity | Priority |
|---|--------|-------------|------------|----------|
| 1 | **Billing & Balance** | 499 | ⭐⭐⭐⭐⭐ | P0 |
| 2 | **Profile Management** | ~300 | ⭐⭐⭐ | P0 |
| 3 | **Orders** | ~200 | ⭐⭐⭐ | P0 |
| 4 | **Email Verification** | (in auth.js) | ⭐⭐⭐ | P0 — **Email-only register, no phone** |
| 5 | **Webhook Balance Deduction** | (in webhook.js) | ⭐⭐⭐⭐⭐ | P0 — Missing `FOR UPDATE` balance deduction |

### 2.2 Important — Needed for Beta

| # | Module | Node.js LOC | Complexity | Priority |
|---|--------|-------------|------------|----------|
| 6 | **Redemption Codes** | 502 | ⭐⭐⭐⭐ | P1 |
| 7 | **Subscriptions** | ~400 | ⭐⭐⭐⭐ | P1 |
| 8 | **Invoices** | ~250 | ⭐⭐⭐ | P1 |
| 9 | **Channel Health** | ~250 | ⭐⭐⭐ | P1 |
| 10 | **Sensitive Words** | ~150 | ⭐⭐⭐ | P1 |
| 11 | **System Settings** | ~350 | ⭐⭐⭐ | P1 |

### 2.3 Nice to Have — Post-Beta

| # | Module | Node.js LOC | Complexity | Priority |
|---|--------|-------------|------------|----------|
| 12 | **Enterprise / Groups** | 828 | ⭐⭐⭐⭐ | P2 |
| 13 | **GDPR Export/Erase** | ~150 | ⭐⭐⭐ | P2 |
| 14 | **Device Tracking** | ~180 | ⭐⭐ | P2 |
| 15 | **Daily Check-in** | ~200 | ⭐⭐⭐ | P2 |
| 16 | **Pricing Management** | ~400 | ⭐⭐⭐⭐ | P2 |
| 17 | **Service Models Full** | 1,080 | ⭐⭐⭐⭐⭐ | P2 |
| 18 | **Dashboard Charts** | 403 | ⭐⭐⭐ | P2 |

### 2.4 Database Schema Gaps

Tables missing in Go init (exist in Node.js but not created by Go):

| Table | Status |
|-------|--------|
| `orders` | ❌ Missing |
| `invoices` | ❌ Missing |
| `subscription_plans` | ❌ Missing |
| `user_subscriptions` | ❌ Missing |
| `redemption_codes` | ❌ Missing |
| `redemption_records` | ❌ Missing |
| `checkin_records` | ❌ Missing |
| `user_groups` | ❌ Missing |
| `model_products` | ❌ Missing |
| `pricing_sources` | ❌ Missing |
| `model_pricing` | ❌ Missing |
| `pricing_history` | ❌ Missing |
| `service_model_approvals` | ❌ Missing |
| `channel_health` | ❌ Missing |
| `channel_health_history` | ❌ Missing |
| `key_pool_status` | ❌ Missing |
| `system_settings` | ❌ Missing |
| `user_devices` | ❌ Missing |
| `usage_records` partitioning | ❌ Missing — plain table, no partitions |
| `usage_summary_hourly/daily` | ❌ Missing |

---

## 3. Frontend Requirements (New)

### 3.1 Design Reference

- **Kimi** (kimi.moonshot.cn) — Clean, minimal, card-based UI
- **Xiaomi Mimo** (https://mimo.mi.com/) — Modern dashboard, left sidebar

### 3.2 Theme System

| Mode | Behavior |
|------|----------|
| **Light** | White bg, dark text, subtle borders |
| **Dark** | Dark bg (slate-900), light text, muted accents |
| **Follow System** | Auto-switch based on `prefers-color-scheme` |

- Theme toggle in header/navbar
- CSS variables for all color tokens
- Persist preference in `localStorage`

### 3.3 Layout

```
┌─────────────────────────────────────────────┐
│  Header (logo, theme toggle, user menu)     │
├──────────┬──────────────────────────────────┤
│          │                                  │
│ Sidebar  │        Main Content Area         │
│ (nav)    │        (pages, cards, tables)    │
│          │                                  │
│          │                                  │
└──────────┴──────────────────────────────────┘
```

- Left sidebar: collapsible, icon + label
- Right: page content with consistent padding
- Responsive: sidebar → hamburger on mobile

### 3.4 Visual Style

| Element | Spec |
|---------|------|
| **Gradients** | Subtle, used sparingly (hero sections, CTAs). Skip if background too complex |
| **Icons** | [Lucide](https://lucide.dev/) — same as current Node.js Portal |
| **Fonts** | **No copyrighted fonts** — Use system fonts or open-source (Inter, Noto Sans SC) |
| **Cards** | Rounded corners (12-16px), subtle shadow, hover lift |
| **Tables** | Striped rows, hover highlight, sticky header |
| **Buttons** | Rounded, primary accent color, loading states |
| **Forms** | Floating labels or clear placeholders, validation feedback |

### 3.5 Pages Required

**Public**:
- `/` — Landing / product showcase
- `/login` — Email + password, "remember me"
- `/register` — Email (with verification), password, name
- `/forgot-password` — Email verification code + new password

**User (post-login)**:
- `/dashboard` — Usage overview, key stats, recent activity
- `/apikeys` — API key list, create, revoke
- `/billing` — Usage records, cost breakdown
- `/recharge` — Balance top-up
- `/orders` — Order history
- `/invoices` — Invoice applications
- `/settings` — Profile, password change

**Admin**:
- `/admin/dashboard` — System overview, charts
- `/admin/users` — User management
- `/admin/upstream-keys` — Provider key pool
- `/admin/service-models` — Model configuration
- `/admin/providers` — Provider registry
- `/admin/settings` — System settings
- `/admin/audit-logs` — Activity logs
- `/admin/billing` — Orders, redemption codes
- `/admin/subscriptions` — Plan management

### 3.6 Tech Stack for Frontend

| Layer | Choice | Reason |
|-------|--------|--------|
| Framework | **React 18** | Same as Node.js, no learning curve |
| Build | **Vite** | Fast HMR, same as current |
| Styling | **Tailwind CSS** | Same as current, utility-first |
| Icons | **lucide-react** | Same icon set |
| Charts | **Recharts** | Same as current admin dashboard |
| HTTP | **Axios** | Same as current, interceptors for JWT |
| Router | **React Router v6** | Same as current |
| State | **React Context + useReducer** | Lightweight, no Redux needed |

---

## 4. Registration Flow (Email-Only)

```
┌─────────────┐    ┌──────────────┐    ┌──────────────┐    ┌─────────────┐
│  Enter Email │ → │ Send Code    │ → │ Enter Code   │ → │ Set Password │
│  + Name      │    │ (Redis 10min)│    │ + Password   │    │ + Create     │
└─────────────┘    └──────────────┘    └──────────────┘    └─────────────┘
                                                          │
                                                          ↓
                                                    ┌─────────────┐
                                                    │ Welcome     │
                                                    │ Bonus Auto  │
                                                    │ Applied     │
                                                    └─────────────┘
```

**Requirements**:
- ✅ Email is **required** and **unique** (registration identifier)
- ✅ Email verification code **required** before account creation
- ❌ Phone **not required** (optional field, can be empty)
- ✅ Password: min 8 chars, upper + lower + number
- ✅ Welcome bonus: auto-apply `type='welcome'` redemption code on first register
- ✅ Rate limit: max 3 code requests per email per hour

---

## 5. Implementation Roadmap

### Phase 1: Foundation (Week 1)

| Task | Effort | Status |
|------|--------|--------|
| Add missing DB tables to `initDatabase()` | 1d | — |
| Add `orders`, `invoices`, `subscription_plans`, `user_subscriptions`, `redemption_codes`, `redemption_records`, `system_settings`, `channel_health` | 2d | — |
| Add usage_records partitioning logic | 1d | — |
| Add SMTP mail service (`pkg/mail/`) | 0.5d | — |
| Implement email verification in register flow | 1d | — |

### Phase 2: Core User Features (Week 2)

| Task | Effort | Status |
|------|--------|--------|
| Profile routes (`/api/profile/*`) | 1d | — |
| Billing routes (`/api/billing/*`) — balance, usage details, summary | 1.5d | — |
| Orders routes (`/api/orders/*`) | 1d | — |
| Invoices routes (`/api/invoices/*`) | 1d | — |
| Webhook balance deduction (`FOR UPDATE` transaction) | 1d | — |

### Phase 3: Admin & Business Logic (Week 3)

| Task | Effort | Status |
|------|--------|--------|
| Redemption codes (`/api/redemption/*`) | 1.5d | — |
| Subscriptions (`/api/admin/subscriptions/*`) | 1.5d | — |
| System settings (`/api/admin/settings/*`) | 1d | — |
| Channel health (`/api/admin/channel-health/*`) | 1d | — |
| Sensitive words (`/api/sensitive/*`) | 0.5d | — |

### Phase 4: Frontend (Week 4-5)

| Task | Effort | Status |
|------|--------|--------|
| Setup React + Vite + Tailwind project | 0.5d | — |
| Theme system (light/dark/follow-system) | 1d | — |
| Layout components (sidebar, header, page shell) | 1d | — |
| Auth pages (login, register, forgot-password) | 1.5d | — |
| User dashboard + API keys | 1.5d | — |
| Billing + orders + invoices | 1.5d | — |
| Admin pages (users, keys, models, settings) | 2d | — |
| Responsive design | 1d | — |

### Phase 5: Integration & Polish (Week 6)

| Task | Effort | Status |
|------|--------|--------|
| API compatibility testing (vs Node.js Portal) | 2d | — |
| Frontend-backend integration | 2d | — |
| Go Proxy integration test | 1d | — |
| Bug fixes, edge cases | 1d | — |

---

## 6. Effort Summary

| Phase | Duration | Modules | Est. LOC |
|-------|----------|---------|----------|
| Phase 1: Foundation | 1 week | DB schema, email verify, SMTP | ~500 |
| Phase 2: Core User | 1 week | Profile, billing, orders, invoices, webhook fix | ~1,500 |
| Phase 3: Admin/Business | 1 week | Redemption, subscriptions, settings, health | ~1,500 |
| Phase 4: Frontend | 2 weeks | React SPA, theme, all pages | ~3,000 |
| Phase 5: Integration | 1 week | Testing, bug fixes, polish | — |
| **Total** | **6 weeks** | | **~6,500** |

**Current Go progress**: ~2,552 LOC (≈ 28% of backend)
**Remaining backend**: ~6,500 - 2,552 ≈ **3,948 LOC**
**Frontend (new)**: ~3,000 LOC

---

## 7. Key Technical Decisions

### 7.1 Keep from Current Go Code

| Decision | Why |
|----------|-----|
| **Gin** | Already working, fast, middleware ecosystem |
| **pgx/v5** | Already working, native JSONB, pool management |
| **go-redis/v9** | Already working, queue for webhook batch |
| **golang-jwt/v5** | Already working, standard |
| **bcrypt (cost 10)** | Matches Node.js Portal behavior |
| **No ORM** | Matches Node.js, more control, easier to port SQL |
| **Raw SQL + parameterized queries** | Direct port from Node.js |

### 7.2 Add/Change

| Decision | Why |
|----------|-----|
| **Email verification required** | User requirement — email is sole identity |
| **Phone optional** | User requirement |
| **SMTP mail service** | Needed for verification codes, invoices |
| **React Context for state** | Simpler than Redux, sufficient for this app |
| **CSS variables for theming** | Clean dark/light toggle |
| **Usage records partitioning** | Critical for scale — same as Node.js |

### 7.3 Frontend Compatibility Notes

The new React frontend will call the **same API endpoints** as the Node.js frontend. This means:

1. **Go backend must match Node.js API contract exactly**
2. **Response JSON shape must be identical**
3. **Error format: `{ error: { message: "..." } }`**
4. **JWT in `Authorization: Bearer <token>` header**
5. **Role-based nav built from `/api/auth/me` response**

---

## 8. Risk Assessment

| Risk | Probability | Impact | Mitigation |
|------|-------------|--------|------------|
| API incompatibility with Node.js frontend | Medium | High | Maintain API contract doc, test every endpoint |
| DB schema drift | Low | High | Use same migration scripts, test against same DB |
| Email deliverability | Medium | Medium | Use reliable SMTP, add retry logic |
| Frontend build size | Medium | Low | Code splitting, lazy load admin pages |
| Missing edge cases in billing | Medium | High | Port billingCalculator.js exactly, add unit tests |

---

## 9. Success Criteria

1. ✅ All P0 backend modules implemented and tested
2. ✅ Frontend renders correctly in light/dark/follow-system modes
3. ✅ Email registration with verification works end-to-end
4. ✅ API responses match Node.js Portal exactly
5. ✅ Go Proxy integration works unchanged
6. ✅ Billing/balance deduction works correctly
7. ✅ Admin dashboard functional with all core pages
8. ✅ Responsive on mobile/tablet/desktop

---

## 10. File Structure (Target)

```
ai-portal-go/
├── cmd/server/main.go
├── internal/
│   ├── config/config.go
│   ├── db/db.go
│   ├── middleware/
│   │   ├── auth.go          # JWT + roles
│   │   ├── cors.go          # CORS middleware
│   │   └── ratelimit.go     # Rate limiting
│   ├── routes/
│   │   ├── auth.go          # ✅ Done
│   │   ├── apikeys.go       # ✅ Done
│   │   ├── webhook.go       # ⚠️ Needs balance deduction
│   │   ├── admin.go         # ⚠️ Partial
│   │   ├── dashboard.go     # ⚠️ Partial
│   │   ├── usage.go         # ✅ Done
│   │   ├── status.go        # ✅ Done
│   │   ├── sync.go          # ✅ Done
│   │   ├── profile.go       # ❌ New
│   │   ├── billing.go       # ❌ New
│   │   ├── orders.go        # ❌ New
│   │   ├── invoices.go      # ❌ New
│   │   ├── redemption.go    # ❌ New
│   │   ├── subscriptions.go # ❌ New
│   │   ├── channel_health.go# ❌ New
│   │   ├── sensitive.go     # ❌ New
│   │   ├── settings.go      # ❌ New
│   │   └── enterprise.go    # ❌ New
│   └── services/
│       ├── billing.go       # Cost calculator
│       ├── mail.go          # Email sender
│       └── sync.go          # Model sync service
├── pkg/
│   ├── jwt/jwt.go           # ✅ Done
│   ├── audit/audit.go       # ✅ Done
│   └── mail/mail.go         # ❌ New
├── migrations/
│   ├── 001_init.sql         # ✅ Partial
│   ├── 002_billing.sql      # ❌ New
│   ├── 003_subscriptions.sql# ❌ New
│   └── 004_partitions.sql   # ❌ New
├── web/                     # ❌ New — React Frontend
│   ├── index.html
│   ├── src/
│   │   ├── main.jsx
│   │   ├── App.jsx
│   │   ├── index.css        # Theme CSS variables
│   │   ├── context/
│   │   │   └── AuthContext.jsx
│   │   ├── components/
│   │   │   ├── Layout.jsx
│   │   │   ├── Sidebar.jsx
│   │   │   ├── Header.jsx
│   │   │   ├── ThemeToggle.jsx
│   │   │   └── ...
│   │   ├── pages/
│   │   │   ├── Login.jsx
│   │   │   ├── Register.jsx
│   │   │   ├── Dashboard.jsx
│   │   │   ├── APIKeys.jsx
│   │   │   ├── Billing.jsx
│   │   │   └── admin/
│   │   │       ├── AdminDashboard.jsx
│   │   │       ├── Users.jsx
│   │   │       └── ...
│   │   └── api/
│   │       └── index.js     # Axios instance
│   └── package.json
├── docs/
│   └── portal-transition-plan-202605031500.md  # This doc
├── Dockerfile
├── go.mod
└── go.sum
```

---

*Document Version: 1.0*  
*Last Updated: 2026-05-03*  
*Next Review: Before Phase 1 kickoff*
