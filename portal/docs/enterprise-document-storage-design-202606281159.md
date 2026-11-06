# Enterprise Document Storage Design

**Version:** 1.0  
**Date:** 2026-06-28  
**Status:** NOT IMPLEMENTED — design only. No database table or code exists. Feature deferred indefinitely (non-core for launch).  
**Applies to:** `ai-proxy-go` (Proxy) + `ai-portal-go` (Portal)

---

## 1. Goal

Define how enterprise customers' certification documents (business licenses, contracts, ID cards, etc.) are stored in object storage, retained for 7 years per GDPR requirements, and versioned so that updates do not destroy historical files.

This document is a companion to `data-retention-design-20260628.md`, which covers request/response payload retention. Enterprise documents share the same S3 infrastructure but have different metadata, lifecycle, and access patterns.

---

## 2. Architecture Constraints

The following facts drive the design:

| Resource | Access pattern | Implication |
|----------|----------------|-------------|
| **PostgreSQL (Portal DB)** | Only `ai-portal-go` can access | All metadata, audit logs, and document indexes must live in Portal |
| **S3 object storage** | Both `ai-portal-go` and `ai-proxy-go` can access | File uploads/downloads can be handled by either service, but metadata stays in Portal |
| **Redis** | Currently shared with Portal | If Proxy is deployed independently, Proxy may need its own Redis for queues/caching; document upload queues can stay in Portal |

**Design consequence:** Enterprise documents are stored as **objects in S3** and indexed by **metadata rows in Portal PostgreSQL**. The two systems are kept consistent by writing the S3 object first, then inserting the metadata row.

---

## 3. Object Storage Key Design

### 3.1 Key pattern

```
s3://<bucket>/enterprise-documents/<env>/<enterprise_id>/<document_id>/<version>/<filename>
```

Example:
```
s3://ai-proxy-retention/prod/enterprise-documents/550e8400-e29b-41d4-a716-446655440000/
  doc-abc123/
  v1/
  business-license.pdf
```

### 3.2 Versioning strategy

We do **not** rely on S3 bucket versioning. Instead, we store each upload as a new version folder:

- `v1/` — first upload
- `v2/` — updated upload
- `v3/` — latest upload

**Why explicit versioning instead of S3 versioning:**

- Easier to list all versions of a document for audit or legal discovery.
- Simpler lifecycle rules: we can expire old versions independently.
- Avoids accidental overwrites by admin tools.

The latest version is tracked in the Portal metadata table (`enterprise_documents.current_version`).

---

## 4. Portal Metadata Schema

```sql
CREATE TABLE IF NOT EXISTS enterprise_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enterprise_id UUID NOT NULL REFERENCES enterprises(id),
    uploaded_by UUID REFERENCES users(id),
    document_type VARCHAR(50) NOT NULL,
    -- examples: business_license, organization_code, tax_certificate,
    --           legal_id_card, authorization_letter, contract, other

    file_name VARCHAR(255) NOT NULL,
    original_name VARCHAR(255),
    mime_type VARCHAR(100),
    object_key TEXT NOT NULL,
    object_size BIGINT,

    version INT NOT NULL DEFAULT 1,
    is_current BOOLEAN DEFAULT TRUE,

    status VARCHAR(20) DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'rejected', 'expired')),
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMP WITH TIME ZONE,
    review_notes TEXT,

    retention_until TIMESTAMP WITH TIME ZONE NOT NULL,
    deleted_at TIMESTAMP WITH TIME ZONE,
    deleted_by UUID REFERENCES users(id),
    delete_reason TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_enterprise_docs_enterprise ON enterprise_documents(enterprise_id, document_type);
CREATE INDEX idx_enterprise_docs_current ON enterprise_documents(enterprise_id, document_type, is_current);
CREATE INDEX idx_enterprise_docs_retention ON enterprise_documents(retention_until) WHERE deleted_at IS NULL;
```

### 4.1 Notes on retention

- `retention_until` is set to `created_at + INTERVAL '7 years'`.
- Even after a document is replaced by a newer version, the old version row remains until its own 7-year retention expires.
- Soft-delete is used for GDPR/account-deletion scenarios; hard-delete happens only after retention expires.

---

## 5. Upload Flow

### 5.1 Portal-mediated upload (recommended)

```
┌─────────┐   1. select file   ┌──────────┐   2. presigned URL / direct upload   ┌─────┐
│  Admin  │ ─────────────────▶ │  Portal  │ ───────────────────────────────────▶ │ S3  │
│   UI    │ ◀───────────────── │          │ ◀─────────────────────────────────── │     │
└─────────┘   5. return URL    └──────────┘   3. write metadata                   └─────┘
                                                 4. return doc_id
```

Steps:
1. Admin selects file in Portal UI.
2. Portal generates a presigned PUT URL or streams the file to S3.
3. Portal inserts a row into `enterprise_documents` with `version = 1`, `is_current = TRUE`.
4. If a previous version exists, Portal sets its `is_current = FALSE`.
5. Portal returns the document ID and download URL to the UI.

### 5.2 Proxy-mediated upload (alternative)

If the file passes through `ai-proxy-go` (for example, a public upload endpoint fronted by Proxy):

1. Proxy receives the file.
2. Proxy uploads to S3.
3. Proxy sends metadata (enterprise_id, document_type, object_key, version) to Portal via webhook.
4. Portal inserts the metadata row.

**Recommendation:** Use Portal-mediated upload for enterprise documents. Enterprise onboarding is a Portal workflow; Proxy should only handle AI inference traffic.

---

## 6. Update / Replace Flow

When an enterprise submits an updated document:

1. Portal uploads the new file to a new version folder:
   ```
   enterprise-documents/<enterprise_id>/<doc_id>/v2/<filename>
   ```
2. Portal inserts a new row with `version = 2`, `is_current = TRUE`.
3. Portal sets the previous version's `is_current = FALSE`.
4. The old S3 object is **not deleted**.

**Result:** All historical versions remain available for 7 years from their original upload date.

### 6.1 API example

```
POST /api/enterprise/:enterprise_id/documents
{
  "document_type": "business_license",
  "file": <multipart upload>
}
```

Response:
```json
{
  "id": "doc-abc123",
  "version": 2,
  "is_current": true,
  "object_key": "enterprise-documents/prod/<enterprise_id>/doc-abc123/v2/business-license.pdf",
  "download_url": "https://..."
}
```

---

## 7. Download and View

### 7.1 Presigned URL approach

1. UI requests download: `GET /api/enterprise/:enterprise_id/documents/:doc_id`
2. Portal verifies the caller has permission.
3. Portal generates a short-lived S3 presigned URL (e.g., 5 minutes).
4. Browser downloads directly from S3.

### 7.2 Proxy-proxied approach

If presigned URLs are undesirable (e.g., strict egress controls):

1. UI requests download through Portal.
2. Portal calls Proxy `GET /admin/enterprise-documents/:doc_id`.
3. Proxy reads the object from S3 and streams it back.
4. Portal forwards to UI.

**Recommendation:** Use presigned URLs for simplicity. Proxy-proxied downloads add latency and are only needed if S3 cannot be exposed to browsers.

---

## 8. Retention and Deletion

### 8.1 7-year retention

- Each `enterprise_documents` row has `retention_until = created_at + 7 years`.
- S3 lifecycle rules expire objects based on prefix age:
  ```json
  {
    "Rules": [{
      "ID": "enterprise-documents-7yr",
      "Status": "Enabled",
      "Filter": { "Prefix": "enterprise-documents/" },
      "Expiration": { "Days": 2555 }
    }]
  }
  ```
- Portal's nightly retention cleanup job hard-deletes rows whose `retention_until` has passed and whose S3 object has expired.

### 8.2 Account deletion / GDPR

When an enterprise or user requests deletion:

1. Portal soft-deletes metadata rows (`deleted_at`, `deleted_by`, `delete_reason`).
2. Portal calls S3 to delete the corresponding objects.
3. Deletion receipts are written to `audit_logs`.

Because enterprise documents may be required for legal or tax purposes beyond a user's deletion request, the platform can apply the same legal-hold and review mechanism described in `data-retention-design-20260628.md`.

### 8.3 Version cleanup

Old versions are not deleted early. Each version expires independently based on its own upload date. If storage costs become significant, a policy could be added to move versions older than 1 year to Glacier Deep Archive while keeping the latest version in Standard.

---

## 9. Access Control and Audit

### 9.1 RBAC

| Role | Permissions |
|------|-------------|
| `enterprise_admin` | View/download own enterprise's documents |
| `system_admin` | View/download all documents; approve/reject submissions |
| `super_admin` | Full control including deletion and legal hold |

### 9.2 Audit logging

Every action writes to Portal's `audit_logs`:

| Category | Action | Example |
|----------|--------|---------|
| `enterprise_document` | `uploaded` | Enterprise admin uploaded business license v2 |
| `enterprise_document` | `viewed` | Admin viewed document doc-abc123 |
| `enterprise_document` | `downloaded` | Admin downloaded document doc-abc123 |
| `enterprise_document` | `approved` | System admin approved business license |
| `enterprise_document` | `deleted` | GDPR deletion removed enterprise documents |

---

## 10. Redis and Deployment Considerations

### 10.1 Current deployment

- Redis is currently shared with Portal.
- Document upload queues, background thumbnail generation, or virus-scan tasks can use Portal's Redis.

### 10.2 Independent Proxy deployment

If `ai-proxy-go` is deployed on separate hosts:

- Proxy does **not** need access to Portal Redis for enterprise document handling.
- Document uploads/downloads go through Portal or directly to S3.
- If Proxy ever needs to queue document-related tasks, it should use its own Redis instance or a shared message broker agreed upon by both services.

**Recommendation:** Keep enterprise document workflows in Portal. Do not add document upload logic to Proxy unless there is a specific architectural reason.

---

## 11. S3 Bucket and IAM Design

### 11.1 Bucket structure

Enterprise documents can share the same bucket as request/response payloads or use a separate bucket:

| Approach | Pros | Cons |
|----------|------|------|
| **Same bucket, separate prefix** | Simpler IAM and lifecycle management | Mixed usage patterns in one bucket |
| **Separate bucket** | Cleaner cost attribution and access policies | One more bucket to manage |

**Recommendation:** Use a **separate bucket** if cost attribution and compliance boundaries matter; otherwise, a separate prefix in the same bucket is sufficient.

### 11.2 IAM policy example (Portal role)

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:PutObject",
        "s3:GetObject",
        "s3:DeleteObject"
      ],
      "Resource": "arn:aws:s3:::ai-proxy-enterprise-documents/*"
    },
    {
      "Effect": "Allow",
      "Action": "s3:ListBucket",
      "Resource": "arn:aws:s3:::ai-proxy-enterprise-documents",
      "Condition": {
        "StringLike": {
          "s3:prefix": "enterprise-documents/*"
        }
      }
    }
  ]
}
```

Proxy does not need access to the enterprise-documents bucket unless the Proxy-mediated upload path is chosen.

---

## 12. Implementation Phases

### Phase 0 — Schema and policy (3 days)
- Create `enterprise_documents` table.
- Define document types and retention policy.
- Create S3 bucket and IAM roles.
- Update terms of service / privacy policy.

### Phase 1 — Upload and version management (1 week)
- Implement Portal upload endpoint.
- Implement versioned S3 key generation.
- Insert/update metadata rows on upload.

### Phase 2 — Download and admin UI (1 week)
- Presigned URL generation.
- Enterprise admin document list page.
- System admin review/approve/reject workflow.

### Phase 3 — Retention and audit (3–4 days)
- 7-year S3 lifecycle rule.
- Nightly cleanup job in `db/retention.go`.
- Audit logging for all document actions.

### Phase 4 — GDPR deletion integration (2–3 days)
- Wire enterprise document deletion into existing GDPR delete flow.
- Add legal-hold support if required.

---

## 13. Open Decisions

| Decision | Options | Recommendation |
|----------|---------|----------------|
| Separate bucket vs shared bucket | Separate / shared | Separate for cleaner compliance and cost tracking |
| Download method | Presigned URL / Proxy proxy | Presigned URL unless egress is restricted |
| Upload path | Portal only / via Proxy | Portal only |
| Old version archive | Keep in Standard / move to Glacier after 1 year | Keep in Standard initially; move to Glacier later if cost justifies |
| Virus scanning | On upload / on demand / none | On upload for security; can be Phase 2+ |

---

## 14. Related Documents

- `data-retention-design-20260628.md` — Request/response payload retention design.
- `ai-portal-go/docs/enterprise-self-registration-design-202606231945.md` — Original enterprise self-registration design.
- `ai-portal-go/internal/db/retention.go` — Existing retention cleanup framework.

---

*End of document.*
