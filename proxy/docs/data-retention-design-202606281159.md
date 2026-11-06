# AI Proxy Data Retention Design

**Version:** 1.0  
**Date:** 2026-06-28  
**Status:** Phase 1-2 COMPLETE (local disk capture + Proxy admin API + analytics). Phase 3 (Portal metadata ingestion) deferred.  
**Applies to:** `ai-proxy-go` (Proxy) + `ai-portal-go` (Portal)

---

## 1. Executive Summary

Upstream model providers—Anthropic, OpenAI, and AWS among them—now require partners to retain end-user request/response data and identity metadata for 30–90 days for trust-and-safety audits, abuse investigations, and legal compliance. Our existing token proxy deliberately minimized user-data persistence; this proposal adds a scoped, time-bounded retention layer without turning the proxy into a long-term data store.

**Recommendation:** Proxy writes full request/response payloads to S3-compatible object storage (preferably AWS S3) and sends lightweight, queryable metadata to `ai-portal-go`. Portal stores the metadata in a new `retention_records` table and re-uses its existing `audit_logs`, GDPR routes, admin UI, and retention-cleanup jobs for governance. Because Proxy and Portal may run on separate hosts, object-level administration (view, delete, legal-hold objects) is exposed through a dedicated admin API on `ai-proxy-go`; Portal only needs to query and display metadata, exactly like the existing audit-log UI.

---

## 2. Background and Upstream Requirements

### 2.1 Why retention is now required

| Provider | Default API retention | Audit/disclosure expectation | Source |
|----------|----------------------|------------------------------|--------|
| **Anthropic** | 30 days for trust-and-safety logs; 30 days for API inputs/outputs by default | Partners must be able to produce raw prompts/responses for flagged requests | Anthropic Commercial Terms, Mythos-class model policy (June 2026) |
| **OpenAI** | 30 days for abuse monitoring; eligible enterprise customers can request ZDR | Partners must retain request/response pairs for the same abuse-monitoring window | OpenAI Enterprise Privacy, Platform Data Controls |
| **Azure OpenAI** | 30 days abuse-monitoring data store; Modified Abuse Monitoring/ZDR available | Customer data is processed within the customer-specified geography; logs are auditable | Azure OpenAI data privacy documentation |
| **AWS Bedrock** | Invocation logging disabled by default; when enabled, logs go to S3/CloudWatch with customer-configured lifecycle | Full input/output bodies, identity, modelId, token counts | AWS Bedrock Model Invocation Logging |

Key takeaway: **we do not need to retain data forever**. The upstream window is short (30 days default, up to 90 days for our own safety margin). Legal holds may require up to one year, so the system supports a configurable maximum of 365 days, but we must be able to locate, export, and—when legally permissible—delete individual records on demand.

### 2.2 What must be retainable

1. **Request content and response content** — the full HTTP bodies exchanged with the model provider.
2. **Identity and attribution** — user ID, API key hash, client IP, upstream key ID, provider, model, region.
3. **Usage patterns and statistics** — input/output tokens, cached tokens, latency, status code, routing track, fallback flag.
4. **Proof of purpose** — trace ID, timestamp, request path, and any policy/contract flags (legal hold, ZDR opt-out, etc.).

---

## 3. Current System Context

### 3.1 `ai-proxy-go` (Proxy)

- Sends **usage metadata** (no request/response bodies) to Portal via `POST /api/webhook/usage-batch`.
- Request bodies are read into `bodyBytes` and response bodies into `rawBody` inside the handlers; the content is already in memory at request time.
- Has no local database connection today.
- Design documentation mentions a 7-year billing-audit tiered store, but it is not implemented.

### 3.2 `ai-portal-go` (Portal)

- `users.data_retention_days` exists (default 365) but is currently unused for inference logs.
- `audit_logs` table already supports 7-year retention, soft-delete, export, and admin/super-admin RBAC.
- `usage_records` stores billing/usage metadata; `/api/gdpr/delete` already hard-deletes these.
- `db/retention.go` runs nightly cleanup jobs driven by `PORTAL_RETENTION_DAYS`.
- `/api/gdpr/export` and `/api/gdpr/delete` already implement data-subject rights.
- Redis queue + webhook handlers already process usage batches asynchronously.

**Important:** Portal already has most of the governance machinery we need. The new work is primarily (a) capture and store raw payloads, and (b) wire them into existing workflows.

---

## 4. Design Principles

1. **Minimize scope** — retain full payloads only for the shortest period that satisfies upstream obligations; default 30 days, admin-configurable up to 90 days, legal-hold maximum 365 days.
2. **Separate hot metadata from cold payloads** — metadata stays queryable in Portal PostgreSQL; payloads go to cheap object storage.
3. **Re-use Portal governance** — audit logs, GDPR routes, admin RBAC, retention cleanup, and export already exist there.
4. **Keep Proxy stateless** — Proxy uploads payloads and sends metadata; it does not need a local database.
5. **Immutable, verifiable deletions** — every deletion is logged, produces a receipt, and cascades to object storage.
6. **Data minimization per contract** — customers with ZDR or shorter contractual terms skip full payload retention unless legally required.
7. **Balance user rights with legal obligations** — GDPR/CCPA deletion requests are not absolute; legal holds, cooling-off periods, and manual review protect the platform when upstream audit or legal claims require retention.

---

## 5. Recommended Architecture

### 5.1 High-level flow

```
                              Payload path
┌─────────────────┐     request/response body      ┌──────────────────────────┐
│   ai-proxy-go   │ ─────────────────────────────▶ │  AWS S3 / Aliyun / R2    │
│    (Proxy)      │  gzip + encrypt + upload       │    Object Storage        │
│                 │◀───────────────────────────────│  (30–365 day lifecycle)  │
│  /admin/retention/objects  (object admin API)    │                          │
└────────┬────────┘                                └──────────────────────────┘
         │
         │ send lightweight metadata record
         │ (trace_id, user_id, object_key, tokens, ...)
         ▼
┌─────────────────┐     queue/worker              ┌──────────────────────────┐
│  ai-portal-go   │ ────────────────────────────▶ │  retention_records       │
│    (Portal)     │                               │  (Portal PostgreSQL)     │
│                 │                               │  metadata + audit trail  │
│  /admin/retention/query  (metadata query UI)     │                          │
└────────┬────────┘                               └──────────────────────────┘
         │
         │ re-uses existing components
         ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│  audit_logs  │  /api/gdpr/*  │  db/retention cleanup  │  admin UI            │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 Why this architecture

| Concern | If we used only Portal DB | If we used only object storage | Recommended hybrid |
|---------|---------------------------|--------------------------------|--------------------|
| Cost at scale | High — PostgreSQL storage is expensive for raw payloads | Low | Low |
| Query speed | Fast for metadata and content | Slow for user-range searches | Fast for metadata, acceptable for payload lookup |
| Deletion speed | Fast single DELETE | Requires listing objects | Fast metadata delete + async object purge |
| GDPR export | Simple SQL | Requires object downloads | SQL metadata + signed object URLs |
| Admin interfaces | Single system | Object-only CLI | Metadata in Portal, object ops in Proxy; can be unified by Portal calling Proxy |

---

## 6. Data Scope and Retention Matrix

### 6.1 What is retained

| Category | Fields | Where | Retention |
|----------|--------|-------|-----------|
| **Payload** | Request body, response body, selected headers (`Content-Type`, `User-Agent`) | Object storage | 30 days default; admin-configurable 90 days; legal hold up to 365 days |
| **Identity metadata** | `user_id`, `api_key_hash`, `client_ip`, `upstream_key_id` | `retention_records` | Same as payload |
| **Request metadata** | `trace_id`, `provider`, `model`, `region`, `status_code`, `latency_ms`, `is_stream`, `routing_track`, `is_fallback` | `retention_records` | Same as payload |
| **Usage metadata** | `input_tokens`, `output_tokens`, `cached_tokens` | `retention_records` + `usage_records` | Billing retention (7 years) separate from payload retention |
| **Governance metadata** | `retention_days`, `retention_until`, `legal_hold`, `deleted_at`, `deleted_by`, `delete_reason` | `retention_records` | Until hard-delete after retention window |

### 6.2 What is NOT retained

- Upstream API keys or decrypted credentials.
- System prompts or billing headers that are stripped before upstreaming.
- Payloads for customers/contracts with Zero-Data-Retention (ZDR) terms, unless legally required.

### 6.3 Retention classes

| Class | Days | Who sets it | Use case |
|-------|------|-------------|----------|
| `default` | 30 | System default | Standard upstream abuse-monitoring window |
| `extended` | 90 | Admin per customer/enterprise | Longer internal audit or contractual requirement |
| `legal_hold` | Up to 365 | Super admin | Active investigation or litigation hold; capped to control cost |
| `zdr_exempt` | 0 | System/customer contract | Skip payload retention; keep minimal metadata only |

---

## 7. Reference Architectures from Other Vendors

### 7.1 AWS Bedrock Model Invocation Logging

- Logs every `InvokeModel`, `Converse`, and streaming call.
- Each record includes `timestamp`, `accountId`, `identity.arn`, `modelId`, `operation`, `inputBodyJson`, `outputBodyJson`, token counts.
- Destinations: CloudWatch Logs (short-term query) and/or S3 (long-term retention).
- S3 lifecycle policies control retention and transition to Glacier.
- **Takeaway:** separate queryable short-term logs from cheap long-term object storage.

### 7.2 OpenAI / Anthropic partner expectations

- Default 30-day abuse-monitoring retention.
- Partners must be able to correlate upstream request IDs with their own internal records.
- Enterprise customers can negotiate ZDR, but the partner still needs metadata to prove traffic volume and model usage.
- **Takeaway:** even with ZDR customers, keep metadata; only full payloads need to be conditional.

### 7.3 LLM gateways (Bifrost, LiteLLM, Portkey)

- Keep searchable metadata in a logs database.
- Offload payloads to S3/GCS via configurable prefix.
- Bucket lifecycle policies handle object expiration; the application does not delete time-expired objects itself.
- **Takeaway:** delegate TTL cleanup to object storage; keep the application responsible for access control and on-demand deletion.

---

## 8. Detailed Component Design

### 8.1 Object storage backend and format

**Backend preference:**
1. **AWS S3** (primary) — chosen because `ai-proxy-go` is expected to run on AWS; S3 lifecycle, IAM, and KMS integration are simplest.
2. **Aliyun OSS** (fallback) — for deployments in mainland China.
3. **Cloudflare R2** (fallback) — for lower egress cost if Proxy moves to a non-AWS edge.

The implementation should use an S3-compatible SDK (AWS SDK v2 or MinIO client) so switching backends is a configuration change.

**Key pattern:**
```
s3://<bucket>/proxy-retention/<env>/retention-<days>/year=2026/month=06/day=28/hour=07/<trace_id>.jsonl.gz
```

- `retention-<days>` is embedded in the prefix (`retention-30`, `retention-90`, `retention-365`) so separate S3 lifecycle rules can apply to each retention class.
- This also makes bulk operations (e.g., "delete all 30-day objects older than 30 days") efficient.

**File content (single gzip-compressed JSON object):**
```json
{
  "schema_version": "1.0",
  "trace_id": "550e8400-e29b-41d4-a716-446655440000",
  "received_at": "2026-06-28T07:12:30Z",
  "metadata": {
    "user_id": "...",
    "api_key_hash": "sha256:abc...",
    "client_ip": "203.0.113.42",
    "provider": "anthropic",
    "model": "claude-sonnet-4",
    "region": "us-east-1",
    "status_code": 200,
    "latency_ms": 1234,
    "input_tokens": 150,
    "output_tokens": 320,
    "is_stream": false,
    "routing_track": "standard",
    "is_fallback": false
  },
  "request": {
    "method": "POST",
    "path": "/v1/chat/completions",
    "headers": {
      "content-type": "application/json",
      "user-agent": "..."
    },
    "body": "<base64-encoded original request body>"
  },
  "response": {
    "status_code": 200,
    "headers": { "content-type": "application/json" },
    "body": "<base64-encoded original response body>"
  }
}
```

- Bodies are base64-encoded to preserve binary/multimodal content.
- Files are gzip-compressed before upload.
- Max payload size: 10 MB per request; larger requests are truncated and flagged.
- Encryption: SSE-S3 or SSE-KMS at rest; TLS in transit.
- Lifecycle rules expire objects after 30, 90, or 365 days based on prefix.

### 8.1.1 Per-request storage and user-level aggregation

Each request is stored as **one independent object** keyed by `trace_id`. We do **not** pre-aggregate a user's requests into a single file, because:

- It preserves immutability and audit integrity.
- It allows precise per-request deletion (e.g., GDPR deletion of specific trace IDs).
- It avoids concurrent-append complexity while traffic is ongoing.

**How user-level queries work:**

- The Portal metadata table (`retention_records`) indexes every request by `user_id`, `created_at`, `trace_id`, and `object_key`.
- A query such as "all requests by user X in June" is a fast SQL lookup on Portal:
  ```sql
  SELECT trace_id, object_key, created_at
  FROM retention_records
  WHERE user_id = '...' AND created_at BETWEEN '2026-06-01' AND '2026-06-30'
  ORDER BY created_at;
  ```

**How user-level export works (e.g., legal/compliance review):**

1. Portal runs the SQL query above to get the list of `trace_id`s and object keys.
2. Portal calls Proxy `GET /admin/retention/objects/:trace_id` for each object.
3. An export worker (in Portal or a separate service) sorts the retrieved JSON objects by time and assembles them into a single deliverable — typically a `.jsonl` file or a `.zip` archive.
4. Metadata-only exports (CSV) can be aggregated directly in SQL with `GROUP BY`, because the metadata lives in PostgreSQL.

In short: **S3 stores raw per-request objects; Portal metadata enables fast user-level search; aggregation and packaging happen at export time on the application side.**

### 8.2 Portal metadata table

```sql
CREATE TABLE IF NOT EXISTS retention_records (
    id BIGSERIAL,
    trace_id VARCHAR(64) NOT NULL,
    user_id UUID REFERENCES users(id),
    api_key_hash VARCHAR(255),
    client_ip INET,
    provider VARCHAR(50),
    model VARCHAR(100),
    region VARCHAR(50),
    object_key TEXT NOT NULL,
    object_size BIGINT,
    input_tokens INT DEFAULT 0,
    output_tokens INT DEFAULT 0,
    cached_tokens INT DEFAULT 0,
    latency_ms INT,
    status_code INT,
    is_stream BOOLEAN DEFAULT FALSE,
    routing_track VARCHAR(50),
    is_fallback BOOLEAN DEFAULT FALSE,
    retention_days INT DEFAULT 30,
    retention_until TIMESTAMP WITH TIME ZONE NOT NULL,
    legal_hold BOOLEAN DEFAULT FALSE,
    deleted_at TIMESTAMP WITH TIME ZONE,
    deleted_by UUID REFERENCES users(id),
    delete_reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);

CREATE UNIQUE INDEX idx_retention_trace ON retention_records(trace_id);
CREATE INDEX idx_retention_user_created ON retention_records(user_id, created_at);
CREATE INDEX idx_retention_apikey_created ON retention_records(api_key_hash, created_at);
CREATE INDEX idx_retention_retention_until ON retention_records(retention_until) WHERE deleted_at IS NULL;
```

Partitioning follows the existing pattern used by `usage_records` and other large tables in Portal.

**Deletion governance table:**

```sql
CREATE TABLE IF NOT EXISTS retention_deletion_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID REFERENCES users(id),
    requested_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    cooling_until TIMESTAMP WITH TIME ZONE,
    status VARCHAR(20) DEFAULT 'pending'
        CHECK (status IN ('pending', 'legal_hold', 'review_required', 'executed', 'rejected')),
    legal_hold_reason TEXT,
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMP WITH TIME ZONE,
    executed_at TIMESTAMP WITH TIME ZONE,
    receipt_id UUID,
    ip_address INET,
    user_agent TEXT
);

CREATE INDEX idx_retention_deletion_user ON retention_deletion_requests(user_id, requested_at);
CREATE INDEX idx_retention_deletion_status ON retention_deletion_requests(status, cooling_until);
```

This table records every user-initiated deletion request, its cooling-off window, legal-hold status, and final disposition. It provides the audit trail needed to demonstrate that the platform applied GDPR/CCPA exceptions consistently and defensibly.

### 8.3 Proxy retention writer and object admin API

New package: `ai-proxy-go/internal/retention`

**Writer responsibilities:**
- Accept `RetentionRecord` structs from handlers.
- Compress and upload payload to object storage.
- Enqueue metadata record for Portal webhook.
- Flush in batches (configurable: default 100 records or 3 seconds).
- Retry object uploads and webhook posts with exponential backoff.
- Drop records silently for `zdr_exempt` customers (configurable).

**Object admin API responsibilities (on Proxy, not Portal):**
- `GET /admin/retention/objects/:trace_id` — retrieve a payload object.
- `POST /admin/retention/objects/delete` — delete one or more objects by `trace_id`.
- `GET /admin/retention/objects/list` — list objects by prefix (for operational inspection).
- `POST /admin/retention/objects/legal-hold` — tag objects as under legal hold (prevents deletion).

Because Proxy and Portal may run on separate hosts, the Proxy admin API is the only service that needs direct S3 credentials. Portal can call this API internally when an admin action requires object access, but the interface itself lives on Proxy.

**Critical behavior:**
- If object upload fails, discard the metadata record to avoid a dangling index.
- If webhook fails, retry; metadata is idempotent on `trace_id`.
- Streaming: accumulate chunks in a bounded buffer (max size + max duration), then flush at stream end. If the client disconnects, flush whatever was received.

Pseudo-code for the handler integration:
```go
retention.Record(ctx, &retention.Event{
    TraceID:       traceID,
    UserID:        userID,
    APIKeyHash:    keyHash,
    ClientIP:      c.ClientIP(),
    Provider:      provider,
    Model:         logicalModel,
    Region:        region,
    RequestBody:   bodyBytes,
    ResponseBody:  rawBody,
    InputTokens:   usage.PromptTokens,
    OutputTokens:  usage.CompletionTokens,
    CachedTokens:  usage.PromptCacheHitTokens,
    LatencyMs:     latency,
    StatusCode:    statusCode,
    IsStream:      isStream,
    RoutingTrack:  track,
    IsFallback:    isFallback,
})
```

### 8.4 Portal ingestion webhook and worker

New endpoint: `POST /api/webhook/retention-batch`

- Authenticated with existing `X-Signature` / `X-Timestamp` HMAC mechanism.
- Validates batch and pushes each record to Redis queue `webhook:retention:queue`.
- Worker (`processRetentionQueue`) inserts into `retention_records` with:
  ```sql
  ON CONFLICT (trace_id) DO NOTHING
  ```
- Failed inserts are re-queued with a dead-letter threshold.

### 8.5 Retention cleanup

Extend `ai-portal-go/internal/db/retention.go`:

```go
{name: "retention_records", days: 0, sql: `
    DELETE FROM retention_records
    WHERE legal_hold = FALSE
      AND retention_until < NOW()
      AND deleted_at IS NOT NULL
`},
```

- DB hard-delete only applies to rows already soft-deleted or past retention.
- **Orphan prevention:** on-demand deletions (GDPR, admin, upstream) delete S3 objects before soft-deleting metadata. The object lifecycle rules act as a safety net in case any object escapes on-demand deletion.
- Object storage lifecycle rules are the primary TTL enforcers, one per retention prefix:
  ```json
  {
    "Rules": [
      {
        "ID": "proxy-retention-30d",
        "Status": "Enabled",
        "Filter": { "Prefix": "proxy-retention/prod/retention-30/" },
        "Expiration": { "Days": 30 }
      },
      {
        "ID": "proxy-retention-90d",
        "Status": "Enabled",
        "Filter": { "Prefix": "proxy-retention/prod/retention-90/" },
        "Expiration": { "Days": 90 }
      },
      {
        "ID": "proxy-retention-365d",
        "Status": "Enabled",
        "Filter": { "Prefix": "proxy-retention/prod/retention-365/" },
        "Expiration": { "Days": 365 }
      }
    ]
  }
  ```

---

## 9. Admin, Export, and Audit Interfaces

Admin functions are split by host responsibility:

- **`ai-portal-go`** owns the **metadata query UI/API** and the audit trail. This is the only interface administrators interact with directly.
- **`ai-proxy-go`** owns the **object-level admin API** because the payloads live in object storage attached to Proxy. Proxy does **not** implement a separate admin web UI; it only exposes REST endpoints that Portal calls server-side.

For a unified admin experience, Portal renders the UI and calls Proxy's object admin API internally using a shared service key. Administrators never access Proxy directly in normal workflows. The interface contract and S3 credentials live on Proxy, while authentication, authorization, and the user interface live on Portal.

### 9.1 Portal — Query metadata

```
GET /admin/retention/query
?user_id=uuid
&api_key_hash=...
&trace_id=...
&model=...
&provider=...
&from=2026-06-01
&to=2026-06-28
&limit=50
&offset=0
```

Response:
```json
{
  "records": [
    {
      "trace_id": "...",
      "user_id": "...",
      "provider": "anthropic",
      "model": "claude-sonnet-4",
      "status_code": 200,
      "input_tokens": 150,
      "output_tokens": 320,
      "retention_until": "2026-07-28T07:12:30Z",
      "object_size": 2048,
      "created_at": "2026-06-28T07:12:30Z"
    }
  ],
  "total": 1204,
  "limit": 50,
  "offset": 0
}
```

### 9.2 Portal — Export metadata and payload packages

```
GET /admin/retention/export
?user_id=uuid
&from=...
&to=...
&format=csv|jsonl|zip
```

- **`format=csv`**: metadata-only export, aggregated directly from PostgreSQL. Suitable for spreadsheets and summary review.
- **`format=jsonl`**: returns one metadata record per line; payload content is not included.
- **`format=zip`**: full payload export for legal/compliance review.
  1. Portal queries `retention_records` for the user's `trace_id`s and object keys.
  2. Portal calls Proxy `GET /admin/retention/objects/:trace_id` for each object.
  3. An export worker sorts the retrieved JSON objects by `created_at` and packs them into a zip archive.
  4. For large exports (e.g., >10,000 requests), the worker runs asynchronously and notifies the admin when ready.

All exports are logged to `audit_logs` (category `retention`, action `exported`).

### 9.3 Portal — Soft-delete and legal-hold metadata

```
POST /admin/retention/delete
{
  "trace_ids": ["..."],
  "reason": "User GDPR erasure request #12345"
}
```

Behavior:
1. Verify no `legal_hold` is active on the records.
2. Call Proxy `POST /admin/retention/objects/delete` to delete the actual objects and receive per-trace confirmation.
3. Only after object deletion is confirmed, soft-delete metadata rows (`deleted_at`, `deleted_by`, `delete_reason`).
4. Write `audit_logs` entry.
5. Return deletion receipt with affected trace IDs and object-deletion confirmation.

If the Proxy object deletion fails (network error, S3 throttling, etc.), Portal leaves metadata intact and returns an error so the operation can be retried. This prevents metadata from being removed while payload objects still exist in S3.

```
POST /admin/retention/legal-hold
{
  "trace_ids": ["..."],
  "hold": true,
  "reason": "Law enforcement request ref ABC-2026-06-28"
}
```

Only `super_admin` can create or clear legal holds. Portal updates `retention_records.legal_hold` and calls Proxy to tag the corresponding objects.

### 9.4 Proxy — Object admin API (API only, no UI)

Proxy exposes these endpoints on its existing Gin server under `/admin/retention/objects/*`. They are protected by the existing admin-secret middleware. There is no separate admin web interface on Proxy; these endpoints are consumed by Portal's UI and by operational CLI scripts.

```
GET /admin/retention/objects/:trace_id
```
- Returns the gzip-compressed payload object for the given trace ID.
- Used by Portal UI when an admin clicks "view payload" and by upstream audit requests.

```
POST /admin/retention/objects/delete
{
  "trace_ids": ["..."]
}
```
- Deletes the listed objects from S3.
- Returns confirmation per trace ID.
- Objects under legal hold are rejected unless the hold is cleared first.

```
GET /admin/retention/objects/list
?prefix=proxy-retention/prod/retention-30/year=2026/month=06/day=28/
&limit=1000
```
- Lists object keys by prefix for operational inspection.

```
POST /admin/retention/objects/legal-hold
{
  "trace_ids": ["..."],
  "hold": true
}
```
- Tags objects with a legal-hold marker so lifecycle rules and delete requests skip them.

**Operational access:** In rare troubleshooting scenarios, operators can call these endpoints directly with `curl` or a small CLI script using the admin secret. This is not a primary workflow and does not require a dedicated client application.

### 9.5 Audit logging

Every retention admin action writes to Portal's `audit_logs`:

| Category | Action | Example |
|----------|--------|---------|
| `retention` | `query` | Admin queried retention records for user X |
| `retention` | `exported` | Admin exported metadata for user Y |
| `retention` | `payload_viewed` | Admin viewed payload for trace_id Z |
| `retention` | `deleted` | Admin deleted 10 records (GDPR #12345) |
| `retention` | `legal_hold_set` | Super admin placed hold on trace IDs |
| `retention` | `legal_hold_cleared` | Super admin cleared hold |

---

## 10. GDPR / CCPA Integration

### 10.1 Data export (`/api/gdpr/export`)

Extend the existing export to include:
- `retention_records` metadata for the user (last 100 records by default).
- Optionally, short-lived signed URLs to payload objects if the user explicitly requests full content.

The export itself is already logged to `audit_logs`.

### 10.2 Data deletion (`/api/gdpr/delete`)

Extend the existing deletion flow to:
1. Check deletion eligibility flags (`legal_hold`, `deletion_review_required`, `deletion_cooling_until`).
2. If the account is under legal hold, reject the request and inform the user.
3. Insert a record into `retention_deletion_requests` with status `pending` and a configurable cooling-off period (default 7 days).
4. During the cooling-off period, the request is visible in the admin review queue; super admins can approve, reject, or escalate to `legal_hold`.
5. If the cooling-off period expires without escalation, the request is automatically approved and executed.
6. On execution: query all `trace_id`s for the user from `retention_records`.
7. Call Proxy `POST /admin/retention/objects/delete` to delete the corresponding payload objects and receive confirmation.
8. Only after object deletion succeeds, soft-delete all `retention_records` for the user in Portal.
9. Update `retention_deletion_requests` status to `executed` and keep the existing `audit_logs` GDPR entry.

**Why object deletion must be confirmed first:** Portal metadata and S3 objects are two separate systems. If Portal deleted metadata first, the remaining S3 objects would become unreferenced "orphans" that could no longer be tied to a user but would still exist. Reversing the order guarantees that when the user is told "your data is deleted", both metadata and payloads are gone (subject to legal holds).

**Protecting against abuse of deletion rights:** A user could request deletion to evade an upstream trust-and-safety investigation. The minimum viable safeguards are:

| Safeguard | Priority | Implementation |
|-----------|----------|----------------|
| **Cooling-off period** | Required | Deletion requests are queued for 7 days (configurable); super admins can apply a legal hold during this window. |
| **Legal hold override** | Required | Accounts under active legal hold cannot self-delete retention records until the hold is cleared. |
| **Deletion request review** | Required | All deletion requests appear in an admin review queue; super admins can escalate to legal hold or approve execution. |
| **Deletion request audit** | Required | Every request is logged with timestamp, IP, user agent, and final disposition in `retention_deletion_requests`. |
| **Terms of service** | Required | User agreement reserves the platform's right to delay or decline deletion where required by law, contract, or legal claim. |
| **Risk-based auto-flagging** | Optional/Future | Automatically flag accounts for review based on upstream abuse signals or anomaly classifiers (can be added later without changing the core flow). |
| **Upstream notification** | Optional/Future | Notify contracted upstream providers when a flagged account requests deletion (depends on legal/commercial agreements and integration effort). |

GDPR Article 17(3) and similar CCPA provisions already contain exceptions for legal compliance and establishment/exercise/defense of legal claims; the required controls above give the platform evidence that it applied those exceptions consistently.

Because payloads may be under legal hold, the endpoint returns:
```json
{
  "ok": true,
  "message": "Account deactivated. Some retention records may be retained due to active legal holds."
}
```

### 10.3 Per-user retention override

`users.data_retention_days` can be used to set a **shorter** retention window for a specific user or enterprise (e.g., 7 days). It cannot exceed the system legal maximum (365 days). The Proxy reads this value during key validation and tags records with the appropriate `retention-<days>` prefix.

---

## 11. Dispute and Upstream Request Handling

### 11.1 Request channels

| Source | Typical request | Response SLA target |
|--------|-----------------|---------------------|
| Anthropic / OpenAI trust-and-safety | Provide payload for `trace_id` X | 24–48 hours |
| Law enforcement | Provide all records for user Y in date range | Per legal counsel |
| End user (GDPR/CCPA) | Delete my data / export my data | 30 days (GDPR) / 45 days (CCPA) |
| Enterprise customer admin | Export all usage by my organization | Contractual |

### 11.2 Upstream audit webhook

Because payloads live in object storage managed by Proxy, the payload-retrieval endpoint should be exposed on `ai-proxy-go`:

```
POST /api/webhook/upstream-audit-request
```

Supported actions:
- `trace_lookup` — return payload for one or more `trace_id`s (Proxy reads directly from S3).
- `user_lookup` — return metadata list for a user/time range (Proxy can query Portal internally or require the caller to supply trace IDs).
- `user_delete` — cascade delete for a user subject to legal hold checks.

Authentication: existing `X-Signature` HMAC plus a provider-specific key.

If upstream providers cannot be configured to call Proxy directly, Portal can receive the request and forward payload lookups to Proxy's admin API.

### 11.3 Manual review queue

Fuzzy requests (e.g., "all requests from IP 1.2.3.3 last Tuesday") cannot be automated safely. These go to a super-admin review queue where a human approves before export/deletion.

### 11.4 Deletion receipts

Every deletion produces an immutable receipt:
```json
{
  "receipt_id": "...",
  "trace_ids": ["..."],
  "requested_by": "user|admin|upstream",
  "deleted_by": "uuid",
  "reason": "...",
  "object_deletion_confirmed": true,
  "metadata_deletion_confirmed": true,
  "legal_hold_skipped": [],
  "created_at": "2026-06-28T07:12:30Z"
}
```

Receipts are stored in `audit_logs.details` and can be provided to regulators or upstream providers. The `object_deletion_confirmed` flag is only set after Proxy reports successful S3 deletion.

---

## 12. Security and Compliance Checklist

| Control | Implementation |
|---------|----------------|
| Encryption at rest | SSE-S3 or SSE-KMS on object storage; PostgreSQL encrypted volumes |
| Encryption in transit | TLS 1.2+ for all Proxy↔Storage and Proxy↔Portal traffic |
| Access control | Proxy holds S3 credentials (write/read/delete); Portal has no direct bucket access; no human direct access |
| IP retention | Store raw `client_ip` in metadata; restrict access to `super_admin` and legal-process requests |
| Audit trail | All admin queries/exports/deletions logged to `audit_logs` |
| Deletion governance | Cooling-off period, legal-hold override, risk-based review, and deletion-request audit trail |
| Legal basis for retention | Document GDPR Article 17(3) / CCPA exceptions for legal compliance and legal claims |
| Data minimization | Skip payload retention for ZDR customers; truncate payloads > 10 MB |
| Retention enforcement | Object lifecycle (primary) + nightly DB cleanup (secondary) |
| Legal hold | Super-admin flag blocks deletion until cleared |
| DPA / contracts | Update Data Processing Addendum to list object storage subprocessor and retention windows |
| Incident response | Document runbook for data breach: identify affected `trace_id`s, revoke access, notify upstream providers |

---

## 13. Cost Estimate

Assumptions: 1,000 requests/second, average request+response body = 2 KB.

| Item | Calculation | Monthly cost (90-day retention) |
|------|-------------|--------------------------------|
| Object storage | 1k RPS × 2 KB × 86,400 s × 30 d = 5.18 TB/month; 90-day peak ≈ 15.5 TB; $0.023/GB | ~$360 |
| Object egress (exports) | 1% of payloads exported = 52 GB/month; $0.09/GB | ~$5 |
| PostgreSQL metadata | ~150 bytes/row × 2.6B rows/month ≈ 390 GB; already budgeted in existing Portal DB | Negligible incremental |
| Compute (Proxy) | Async upload adds ~1–2 ms; no extra instances at this volume | Negligible |
| **Total** | | **~$365/month** |

For 30-day retention, divide storage cost by 3. For legal-hold 365-day retention, multiply by ~4, so a full 365-day peak would be roughly **$1,460/month** at this volume.

---

## 13.5 Cost Optimization and Archival Strategy

If data volume grows beyond the estimate above, the object storage bill becomes the dominant cost. Several options are available, but the key point is that **S3 does not impose a fixed data structure** — the key naming convention and the gzip-compressed JSON payload format are defined by us. As long as that schema is preserved, objects can be moved to cheaper storage or even exported to non-AWS systems without losing interpretability.

### 13.5.1 S3 storage-class lifecycle transitions

The recommended first step is to use **S3 Lifecycle rules** to move older objects to cheaper storage classes instead of leaving everything in S3 Standard.

| Retention class | S3 Standard window | Transition target | Rationale |
|-----------------|-------------------|-------------------|-----------|
| `retention-30` | 0–30 days | None (stay Standard) | Short window; transition fees would outweigh savings |
| `retention-90` | 0–30 days | S3 Standard-IA or Intelligent-Tiering | Infrequent access; modest savings |
| `retention-90` | 30–90 days | S3 Glacier Instant Retrieval | Rarely accessed after 30 days |
| `retention-365` / legal hold | 30 days | S3 Glacier Deep Archive | Very rare access; lowest cost |

**Important caveats:**
- Glacier Instant Retrieval has a **90-day minimum storage duration** charge.
- Glacier Deep Archive has a **180-day minimum storage duration** charge.
- If an object is deleted before the minimum period, AWS bills for the remainder.
- Retrieval incurs per-GB fees; frequent audit exports from Glacier can erase the savings.

Because our key prefix already encodes the retention class (`retention-30/`, `retention-90/`, `retention-365/`), each prefix can have its own lifecycle policy.

### 13.5.2 Export to alternative storage

For very large volumes or regulatory data-residency requirements, objects can be exported in bulk while preserving the key structure:

| Destination | When to consider | Notes |
|-------------|------------------|-------|
| **MinIO (self-hosted)** | Egress from AWS is expensive; existing private infrastructure | Fully S3-compatible; key schema and JSON format remain identical |
| **Aliyun OSS** | Deployment in mainland China | S3-compatible API; adjust endpoint and credentials |
| **Cloudflare R2** | High egress volume from S2 | No egress fees; may be cheaper for read-heavy audit workloads |
| **Tape / cold storage** | Legal-hold data rarely accessed | Export gzip objects by prefix; keep a manifest mapping trace_id → offline location |
| **Parquet data lake** | Analytical queries on metadata only | Convert metadata to Parquet; keep raw payloads in object storage |

### 13.5.3 Practical recommendation

- **Start with S3 Standard** for all objects. The implementation and operational simplicity outweigh savings at moderate scale.
- **Add lifecycle rules for `retention-365/`** after 30 days → Glacier Deep Archive. This is where cost reduction is easiest and least risky.
- **Evaluate Glacier for `retention-90/`** only if monthly storage exceeds ~50 TB and retrieval frequency is very low.
- **Do not move `retention-30/` to Glacier**; the 90/180-day minimum charges make it more expensive than Standard.
- **Preserve the key schema and JSON format** in any export so archived data can be re-imported or queried without rewriting tooling.

---

## 14. Implementation Phases

### Phase 0 — Policy and configuration (1 week)
- Finalize retention default (30 days), admin-configurable option (90 days), and legal-hold maximum (365 days).
- Confirm object storage backend (AWS S3 primary; Aliyun/R2 fallback documented).
- Update DPA and privacy policy.
- Add Proxy config flags (`RETENTION_ENABLED`, `RETENTION_S3_BUCKET`, `RETENTION_DEFAULT_DAYS`, `RETENTION_S3_ENDPOINT` for Aliyun/R2).
- Add `retention_records` schema to Portal.

### Phase 1 — Proxy foundation (1–2 weeks)
- Add S3-compatible client (AWS SDK v2 with MinIO-compatible endpoint support).
- Create `internal/retention` package with async batch writer.
- Implement gzip + upload + webhook enqueue.
- Add Proxy object admin API (`/admin/retention/objects/*`).
- Add unit tests for retry and failure paths.

### Phase 2 — Handler integration (1–2 weeks)
- Wire retention recording into:
  - `handleChatCompletions`
  - `handleClaudeMessages`
  - `handleResponses`
  - `handleEmbeddings`
  - `handleImageGenerations`
  - `handleAudioTranscriptions` / `handleAudioTranslations`
- Handle streaming accumulation.
- Add payload size limits and truncation flags.

### Phase 3 — Portal ingestion and metadata queries (1 week)
- Add `POST /api/webhook/retention-batch`.
- Add Redis queue worker.
- Add `retention_records` query package and `/admin/retention/query` / `/admin/retention/export`.
- Configure S3 lifecycle rules for 30/90/365-day prefixes.

### Phase 4 — Admin and GDPR UI/API (1–2 weeks)
- Wire Portal soft-delete and legal-hold to call Proxy object admin API.
- Implement deletion governance: cooling-off queue, legal-hold override, risk-based review, `retention_deletion_requests` table.
- Extend `/api/gdpr/export` and `/api/gdpr/delete`.
- Add admin UI page (can reuse `admin-audit-logs.html` patterns).
- Add export streaming.

### Phase 5 — Compliance hardening (1 week)
- Legal-hold enforcement across Proxy and Portal.
- Deletion receipts.
- Upstream audit webhook on Proxy.
- Security review and load test.

**Estimated total duration:** 6–9 weeks with one backend engineer and one Portal engineer.

---

## 15. Open Decisions

| Decision | Resolution |
|----------|------------|
| Default retention | **30 days** default; admin-configurable **90 days**; legal-hold maximum **365 days** |
| Object storage backend | **AWS S3 primary**; Aliyun OSS and Cloudflare R2 documented as fallbacks |
| Object admin interface | Lives on **Proxy** (`/admin/retention/objects/*`); Portal queries metadata only and calls Proxy for object operations |
| IP storage | **Raw IP retained** in `retention_records`; access restricted to super-admin and legal process |
| Metadata DB location | Start on same Portal DB; move to read replica if query load justifies it |
| Per-customer overrides | Shorter allowed; cannot exceed system legal maximum (365 days) |
| Multimodal/image payloads | Include as base64 up to 10 MB per record; exclude files uploaded by reference |

---

## 16. Appendix A: Example Object Lifecycle Rules

```json
{
  "Rules": [
    {
      "ID": "ai-proxy-retention-30d",
      "Status": "Enabled",
      "Filter": { "Prefix": "proxy-retention/prod/retention-30/" },
      "Expiration": { "Days": 30 }
    },
    {
      "ID": "ai-proxy-retention-90d",
      "Status": "Enabled",
      "Filter": { "Prefix": "proxy-retention/prod/retention-90/" },
      "Expiration": { "Days": 90 }
    },
    {
      "ID": "ai-proxy-retention-365d",
      "Status": "Enabled",
      "Filter": { "Prefix": "proxy-retention/prod/retention-365/" },
      "Expiration": { "Days": 365 }
    }
  ]
}
```

---

## 17. Appendix B: Related Internal Documents

- `ai-proxy-go/docs/ai-proxy-go-design-202604291600.md` — Original Proxy architecture (7-year audit vision not yet implemented).
- `ai-portal-go/docs/usage-audit-separation-design-202606211557.md` — Decoupling usage stats from audit logs.
- `ai-portal-go/docs/gdpr-audit-log-gap-analysis-202605281700.md` — Existing GDPR audit log analysis.
- `ai-portal-go/internal/db/retention.go` — Existing retention cleanup framework.
- `ai-portal-go/internal/routes/audit.go` — Existing audit log routes and RBAC.
- `ai-portal-go/internal/routes/gdpr.go` — Existing GDPR export/delete routes.

---

*End of document.*
