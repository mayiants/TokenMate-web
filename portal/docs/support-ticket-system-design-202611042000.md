# Support Ticket System — Design

Status: DRAFT
Date: 2026-11-04
Scope: ai-portal-go (control plane only; no proxy / data-plane changes)

A built-in support ticketing module: users (and enterprise admins) open tickets,
exchange messages, attach files; system admins triage, reply, and resolve.
Admins get notified on new tickets and replies; progress is tracked in the
backend. Inspired by TokenRouter's "Ticket Creation" release.

---

## Reuse existing infra (no new subsystems needed)

- **AuthN/Z**: `middleware.Auth` — `RequireAuth()` for the user surface,
  `RequireAdmin()` (system_admin/super_admin) for the admin surface. Enterprise
  context already available for org-scoped tickets.
- **Notifications**: `internal/services/notification.go` —
  `NotifyUser`, `NotifySystemAdmins`, `NotifyEnterpriseAdmins(InApp)`. Use these
  for in-app bell notifications (new ticket → sysadmins; admin reply → requester).
- **Email**: `pkg/mail` `Sender` (templated, locale-aware). Add ticket
  templates (new-ticket ack, new-reply). Mail is optional → degrade to in-app
  only when unconfigured (existing posture).
- **Attachments**: `internal/storage` `Provider` (`Upload`/local, pluggable) —
  store files out of the DB; keep only metadata rows.
- **Audit**: `pkg/audit` — log status changes, assignments, admin replies.
- **i18n**: all user-facing strings via `web/i18n/{en,zh}.json`.

---

## Data model

```sql
CREATE TABLE support_tickets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  ticket_no       BIGSERIAL,                              -- human-friendly #, unique
  user_id         UUID NOT NULL REFERENCES users(id),     -- requester
  enterprise_id   UUID REFERENCES enterprises(id),        -- set if opened in enterprise context
  subject         VARCHAR(255) NOT NULL,
  category        VARCHAR(40)  NOT NULL DEFAULT 'general', -- general|billing|technical|abuse|feature
  priority        VARCHAR(20)  NOT NULL DEFAULT 'normal',  -- low|normal|high|urgent
  status          VARCHAR(20)  NOT NULL DEFAULT 'open',    -- open|pending|answered|resolved|closed
  assignee_id     UUID REFERENCES users(id),              -- admin handling it (nullable)
  last_reply_role VARCHAR(10),                            -- 'user'|'admin' (drives "needs reply" views)
  last_reply_at   TIMESTAMPTZ,
  created_at      TIMESTAMPTZ DEFAULT NOW(),
  updated_at      TIMESTAMPTZ DEFAULT NOW(),
  deleted_at      TIMESTAMPTZ
);
CREATE INDEX idx_support_tickets_user   ON support_tickets(user_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_support_tickets_status ON support_tickets(status)  WHERE deleted_at IS NULL;
CREATE INDEX idx_support_tickets_ent    ON support_tickets(enterprise_id) WHERE deleted_at IS NULL;

CREATE TABLE support_ticket_messages (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  ticket_id     UUID NOT NULL REFERENCES support_tickets(id) ON DELETE CASCADE,
  author_id     UUID NOT NULL REFERENCES users(id),
  author_role   VARCHAR(10) NOT NULL,                     -- 'user' | 'admin'
  body          TEXT NOT NULL,
  internal_note BOOLEAN DEFAULT FALSE,                    -- admin-only note, hidden from requester
  created_at    TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_ticket_messages_ticket ON support_ticket_messages(ticket_id, created_at);

CREATE TABLE support_ticket_attachments (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  message_id  UUID NOT NULL REFERENCES support_ticket_messages(id) ON DELETE CASCADE,
  storage_key TEXT NOT NULL,                              -- key in storage.Provider
  filename    VARCHAR(255) NOT NULL,
  size_bytes  BIGINT,
  mime_type   VARCHAR(100),
  created_at  TIMESTAMPTZ DEFAULT NOW()
);
```
Add the `CREATE TABLE`s to `internal/db/schema.go` (source of truth) AND a
numbered `migrations/NNNN_support_tickets.sql`, per the repo convention.

### Status lifecycle
`open` → (admin replies) `answered` → (user replies) `pending` → … → `resolved`
→ `closed`. Derive "needs admin attention" from `last_reply_role='user' AND
status IN (open,pending)`. Auto-close `resolved` tickets after N days of silence
(reuse the background-job pattern in `cmd/server/background.go`).

---

## API (routes, `internal/routes/tickets.go`)

### User surface — `RequireAuth()`, group `/api/tickets`
- `POST   /api/tickets`                      create (subject, category, priority, first message, optional attachments)
- `GET    /api/tickets`                      list own tickets (status filter, pagination)
- `GET    /api/tickets/:id`                  detail + messages (own only; exclude internal_note)
- `POST   /api/tickets/:id/messages`         reply (reopens if resolved)
- `POST   /api/tickets/:id/close`            requester closes their own ticket
- `POST   /api/tickets/:id/attachments`      upload (multipart → storage.Provider)
- `GET    /api/tickets/:id/attachments/:aid` download (ownership-checked, streamed)

Ownership rule: a user sees their own tickets; an enterprise admin may also see
tickets with a matching `enterprise_id` (opt-in, mirrors enterprise member
visibility).

### Admin surface — `RequireAdmin()`, group `/api/admin/tickets`
- `GET    /api/admin/tickets`                list/filter (status, category, priority, assignee, search)
- `GET    /api/admin/tickets/:id`            detail incl. internal notes
- `POST   /api/admin/tickets/:id/messages`   reply (or `internal_note=true`)
- `PATCH  /api/admin/tickets/:id`            set status / priority / category / assignee
- `GET    /api/admin/tickets/stats`          counts by status for the dashboard badge

All writes audited (`pkg/audit`): status change, assignment, admin reply.

### Validation / limits
- Subject ≤ 255; body ≤ e.g. 10 000 chars; category/priority from fixed enums.
- Rate-limit ticket creation per user (reuse rate-limit middleware) to prevent spam.
- Attachments: max size (e.g. 10 MB) + MIME allowlist; store via `storage.Provider`;
  never trust client filename for the storage key (use `ticket_id/uuid`).

---

## Notifications & email

- **New ticket** → `NotifySystemAdmins(...)` (in-app) + optional email to a
  support inbox; link to `/admin/tickets/:id`.
- **Admin reply** → `NotifyUser(requester, ...)` + email "你的工单有新回复"
  (new ticket templates in `pkg/mail/templates.go`).
- **User reply** → `NotifySystemAdmins` / notify the `assignee_id` if set.
- Reuse the notification `refType='ticket'`, `refID=ticket_id` so the bell links
  back correctly (and dedup via the existing `uq_notification_dedup`).

Email is best-effort (optional, like other flows); in-app notification is the
source of truth.

---

## UI (server-rendered + HTMX/Alpine, consistent with existing pages)

- **User**: `/support` — "My Tickets" list + "New Ticket" drawer + a ticket
  detail/conversation view (message thread, reply box, attachment upload).
- **Admin**: `/admin/tickets` — queue with filters (status/priority/assignee),
  a detail view with reply + internal-note + status/assignee controls, and a
  header badge (open-ticket count from `/stats`).
- Register both pages in `internal/web/pages.go` **and** add them to the template
  smoke test (unregistered pages 404 per repo rules).
- i18n: add en/zh keys for all labels/statuses/categories; `i18n_full_audit
  --strict` must pass (and English values must be English — the new
  `chinese_en` guard).

---

## Testing

- **DB tests** (`-p 1`, Postgres): create → reply → status transitions →
  ownership isolation (user A cannot read user B's ticket) → internal_note
  hidden from requester → enterprise-admin visibility scope.
- **Attachments**: upload→download round-trip via `storage.Provider`; size/MIME
  rejection.
- **Notifications**: new ticket notifies sysadmins; admin reply notifies
  requester (assert rows via the notification service).
- **E2E (Playwright)**: user opens a ticket; admin sees it, replies; user sees
  the reply — a new `e2e/ui-tickets.spec.js` wired into the regression job once
  green (reuse seeded E2E accounts).
- **Gates**: build/vet/gofmt, golangci-lint, schema drift (new tables in
  schema.go), i18n audit, ON CONFLICT check.

---

## Rollout
1. Migration + schema.go tables + models/queries.
2. User API + admin API + audit + notifications.
3. UI (user `/support`, admin `/admin/tickets`) + i18n.
4. Attachments (optional in a second pass; ship text-only first if needed).
5. Auto-close background job + admin stats badge.

### Effort
**M–L.** No proxy/data-plane changes. Largest parts are the two UIs and the
attachment path; the data model and API are straightforward CRUD on top of
existing auth/notification/mail/storage services.

### Out of scope (future)
- Inbound email → ticket (parse replies from a support mailbox).
- SLA timers / CSAT surveys / canned responses / tags.
- Public (unauthenticated) ticket submission.
