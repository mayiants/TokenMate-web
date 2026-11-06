# Design Verification: Enterprise Document Storage & Data Retention

**Date:** 2026-06-28 14:00 UTC  
**Status:** ACTIVE — analysis for decision-making  
**Reviews:**
- `enterprise-document-storage-design-202606281159.md`
- `data-retention-design-202606281159.md`

---

## Executive Summary

Both designs are structurally sound — the hybrid architecture (PostgreSQL metadata + S3 objects) is the industry standard used by LLM Gateway, Portkey, LiteLLM, and AWS Bedrock's own invocation logging. However, **the data retention design's premise needs adjustment** based on current upstream provider policies (several of which have shifted to shorter defaults since the document was drafted). The enterprise document design is clean and ready for Phase 0 approval with minor clarifications.

---

## 1. Upstream Provider Policy Reality Check (June 2026)

The data retention design's Table 2.1 states all providers default to 30-day retention. Web research reveals a more nuanced picture:

| Provider | Design Doc Claims | Current Actual (June 2026) | Source |
|----------|------------------|---------------------------|--------|
| **Anthropic API** | 30 days default | **7 days** (reduced Sept 2025); ZDR available for enterprise | Anthropic API docs, Anarlog Mar 2026 |
| **Anthropic Mythos-class** (Fable 5, Mythos 5) | (not separately called out) | **30 days mandatory** via `provider_data_share` on Bedrock; no opt-out | AWS Bedrock docs, Lushbinary Jun 2026 |
| **OpenAI API** | 30 days for abuse monitoring | **30 days** for abuse monitoring; ZDR available for eligible customers | OpenAI Enterprise Privacy |
| **AWS Bedrock (default)** | Logging disabled by default | Disabled by default; configurable per-project; ZDR (`none`) available per-model | AWS Bedrock data retention docs |

**Key finding:** The document's premise that "all providers require 30-90 days" is accurate for **abuse monitoring on the provider's side**, but the implication that **we must retain full payloads for 30 days on our side** is weaker than stated. The actual requirement is:

1. **Be able to produce payloads when requested** (within the provider's retention window)
2. **For Mythos-class models**: mandatory 30-day retention with data shared to provider
3. **For standard API traffic**: no affirmative obligation to retain; only need to respond to upstream audit requests within their window

This shifts the design's cost-benefit calculus significantly.

---

## 2. Data Retention Design — Assessment

### 2.1 Strengths

| Aspect | Assessment |
|--------|-----------|
| **Architecture** | Metadata in PG + payloads in S3 is correct. LLM Gateway, Portkey, and Coder's AI Gateway all follow this pattern |
| **Retention classes** | 30/90/365 day classes with S3 lifecycle rules are well-designed |
| **Deletion governance** | Cooling-off period + legal hold + admin review queue is thoughtful and defensible |
| **Object admin API on Proxy** | Correct for cross-host deployment — Proxy owns S3 credentials, Portal queries metadata |
| **Cost estimate** | $365/mo @ 1k RPS for 90-day retention is realistic (S3 Standard pricing) |
| **GDPR integration** | Extending existing `/api/gdpr/*` routes is pragmatic; reuses proven machinery |

### 2.2 Concerns

| # | Concern | Severity | Recommendation |
|---|---------|----------|---------------|
| 1 | **Default 30-day retention for ALL traffic is over-engineered** | High | Default should be **metadata-only** for most traffic. Full payload retention should be opt-in per customer/enterprise, or triggered only when an upstream provider flags a trace_id. The 30-day blanket default assumes a compliance burden that doesn't exist for standard API traffic |
| 2 | **Per-request S3 PUT at scale** | Medium | 1k RPS = 86M objects/day = 2.6B objects/month. S3 handles this, but listing, billing granularity (per-object costs), and potential throttling at prefix level need consideration. Consider batching multiple records into single S3 objects (e.g., hourly batches per provider) rather than one object per request |
| 3 | **No PII redaction before storage** | Medium | Storing raw request/response bodies without scanning for PII (emails, phone numbers, addresses) creates GDPR data minimization risk. Consider adding a PII redaction step before writing to S3, or at minimum document that this is a known gap |
| 4 | **7-day cooling-off period for GDPR deletion may conflict with Article 17** | Medium | GDPR Article 17 requires erasure "without undue delay." A 7-day mandatory cooling-off period could be challenged. Possible mitigation: make the cooling-off period configurable (default 0 for non-legal-hold accounts) and only apply it when a legal hold flag is active |
| 5 | **Per-request cost at 10k+ RPS** | Medium | The cost estimate is linear. At 10k RPS with 90-day retention: ~$3,650/mo. At 50k RPS: ~$18,250/mo. This should be called out as a scaling consideration, with a recommendation to re-evaluate at higher traffic volumes |
| 6 | **No ephemeral storage tier** | Low | For most debugging/audit use cases, 7-day retention is sufficient. Adding a "fast" tier (7 days in local SSD, then purge) would cover 90% of actual audit requests at near-zero S3 cost |
| 7 | **No mapping of retention requirements per upstream model** | Low | Mythos-class models (mandatory 30d) vs standard models (7d default) vs ZDR customers (0d) should be tracked per upstream configuration, not as a global default |

### 2.3 Alternative Approaches

| Option | Description | Pros | Cons | Effort |
|--------|-------------|------|------|--------|
| **A — Metadata-only default, on-demand payload capture** (Recommended) | Don't store any payloads by default. When an upstream provider flags a trace_id (or when abuse monitoring flags a request), capture and store that specific payload. Uses a "mark-and-capture" pattern: requests are marked in Redis for capture, and only marked trace_ids get written to S3 | Lowest cost; most privacy-preserving; meets actual compliance need (respond to upstream requests) | Requires a mechanism to retroactively capture payloads; can't capture after the response has been streamed | 4-6 weeks |
| **B — Shorter default (7 days), 30-day opt-in** | Default retention is 7 days (matching Anthropic's actual API policy). Customers/enterprises can opt in to 30-day retention. Mythos-class traffic automatically gets 30 days | Aligns with actual provider defaults; significantly reduces S3 cost (~80% less storage); matches user privacy expectations | Requires per-customer/enterprise configuration; adds a small complexity to the routing layer | 5-7 weeks |
| **C — Batch per hour, not per request** | Instead of one S3 object per trace_id, batch records by hour per provider into a single .jsonl.gz file (e.g., `hour=14/anthropic.jsonl.gz`). Each object contains 3.6M records (at 1k RPS) | ~3,600x fewer S3 objects; much lower S3 PUT costs; simpler listing and lifecycle | Harder to delete individual trace_ids (must rewrite the batch or use marker tombstones); complicates GDPR deletion | 6-8 weeks |
| **D — PostgreSQL BYTEA for MVP, S3 later** (Pragmatic) | For the first 3-6 months, store payloads in PostgreSQL as a `payload BYTEA` column in `retention_records`. At 2KB per request, 1M requests/day = 2GB/day = 60GB/month, which PostgreSQL handles fine. Migrate to S3 when scale demands it | Zero new infrastructure; simplest implementation; single backup/restore | PG backup grows larger; not suitable for very high traffic; doesn't solve for cross-host deployment | 2-3 weeks |

#### Recommendation: Hybrid of A + B

```
Default:                     metadata-only, no payloads stored
When upstream flags trace:   capture on next matching request (mark-and-capture)
Mythos-class models:         automatic 30-day payload retention
Enterprise customers:        opt-in to 7/30/90 day retention via admin panel
GDPR deletion:               immediate (no cooling period) unless legal hold active
```

This approach:
- Costs ~10-20% of the original design at scale
- Meets actual compliance requirements (not hypothetical ones)
- Is more privacy-preserving (data minimization by default)
- Is easier to explain to customers and regulators

---

## 3. Enterprise Document Storage Design — Assessment

### 3.1 Strengths

| Aspect | Assessment |
|--------|-----------|
| **Architecture** | Correct separation of metadata (PG) and files (S3) |
| **Versioning** | Explicit version folders (v1/v2/v3) instead of S3 versioning — pragmatic choice, avoids accidental overwrites |
| **Upload flow** | Portal-mediated upload is correct; Proxy should not handle enterprise document uploads |
| **RBAC** | enterprise_admin / system_admin / super_admin roles are appropriately scoped |
| **Retention** | 7-year retention aligns with GDPR/legal requirements for KYC documents |
| **Presigned URL downloads** | Standard and correct approach |

### 3.2 Concerns

| # | Concern | Severity | Recommendation |
|---|---------|----------|---------------|
| 1 | **No virus/malware scanning before upload** | Medium | Enterprise documents (especially uploaded by untrusted users during self-registration) should be scanned before they reach admins. Add a ClamAV scan step or cloud scanning API call before marking the document as `status = 'pending'` |
| 2 | **7-year retention may be aggressive for some document types** | Low | Check specific local regulations. Some jurisdictions require shorter retention for certain document types (e.g., ID cards vs contracts). Consider making retention period configurable per document_type |
| 3 | **No document expiration notification to enterprises** | Low | When a document is about to expire (e.g., business license renewal), the system should notify the enterprise admin. This can be Phase 4+ |
| 4 | **Local file system MVP may never be migrated to S3** | Low | Common anti-pattern: "start with local, switch to S3 later" often stays local forever. If the team plans to deploy on AWS, start with S3 from Phase 1. If on-prem, start with local + bind mount |

### 3.3 Alternative Approaches

| Option | Description | Pros | Cons |
|--------|-------------|------|------|
| **A — S3 from Phase 1** (Recommended if cloud) | Implement S3 (or MinIO in Docker) from the start; skip local filesystem entirely | No migration later; production-ready from day 1; lifecycle rules work immediately | Requires AWS SDK dependency; slightly more complex local dev |
| **B — Local + bind mount for MVP** (Recommended if on-prem) | Use local filesystem with Docker bind mount to host path; clearly document the migration path | Zero new dependencies; simple backup (rsync the host dir) | Must plan for migration to S3 when scaling |
| **C — PostgreSQL BYTEA** | Store document binary directly in PostgreSQL | Simplest possible; single backup; works everywhere | PG bloat; not suitable for large files; backup size grows |

#### Recommendation: Start with S3 (or MinIO in Docker Compose)

Given that the data retention design also needs S3, it's worth setting up MinIO in the Docker Compose environment now. Both designs share the S3 infrastructure. This avoids the "local → S3 migration" trap.

---

## 4. Cross-Cutting Concerns (Both Designs)

| Concern | Enterprise Documents | Data Retention |
|---------|---------------------|----------------|
| **S3 bucket strategy** | Separate bucket recommended | Same bucket, separate prefixes, or different bucket? Need alignment |
| **IAM credential management** | Portal needs S3 write+read | Proxy needs S3 write+read+delete, Portal needs read-only (via Proxy admin API) |
| **Encryption** | SSE-S3 or SSE-KMS? | Same question — should be consistent |
| **Data residency** | Must match enterprise's jurisdiction | Must match upstream provider's jurisdiction |
| **Backup strategy** | PG metadata + S3 objects must be backed up together | Same requirement |
| **Disaster recovery** | Restore PG → S3 keys still valid? | Same question — needs runbook |
| **Audit logging** | Every document action should be logged | Every retention action should be logged (design doc covers this) |

---

## 5. Updated Implementation Recommendation

```
Week 1-2: Alignment & Infrastructure
  [ ] Align S3 bucket strategy between both designs (same bucket? separate buckets?)
  [ ] Set up MinIO in Docker Compose for local dev
  [ ] Define IAM policy templates for Portal vs Proxy S3 access

Week 2-4: Enterprise Document Storage (Phase 0-1)
  [x] Create enterprise_documents table (schema + document_type added, 2026-07-01)
  [ ] Implement S3 upload/download with presigned URLs
  [ ] Add ClamAV scanning step (or document as known gap)
  [ ] Build admin review UI

Week 3-6: Data Retention (Phase 0-1 — Revised)
  [ ] Default to metadata-only; no payload retention for standard traffic
  [ ] Implement per-customer retention policy configuration
  [ ] Implement mark-and-capture for flagged trace_ids
  [ ] Add Mythos-class model detection → automatic 30-day retention
  [ ] Wire Proxy upload to S3 for payload retention when active
  [ ] Create retention_records table and ingestion webhook

Week 6-8: Admin & Compliance UI
  [ ] Portal admin query UI for retention records
  [ ] GDPR export/delete integration
  [ ] Legal hold interface
  [ ] Admin review queue (for upstream audit requests)
```

---

## 6. References

| Source | URL / Path |
|--------|-----------|
| Anthropic API data retention | `platform.claude.com/docs/en/manage-claude/api-and-data-retention` |
| Anthropic policy analysis (Mar 2026) | `anarlog.so/blog/anthropic-data-retention-policy` |
| OpenAI enterprise privacy | `openai.com/enterprise-privacy/` |
| AWS Bedrock data retention | `docs.aws.amazon.com/bedrock/latest/userguide/data-retention.html` |
| Claude Fable 5 compliance (Jun 2026) | `lushbinary.com/blog/claude-fable-5-enterprise-data-retention-compliance-guide` |
| LLM Gateway data retention | `docs.llmgateway.io/features/data-retention` |
| GDPR + AI Act retention strategy | `techgdpr.com/blog/reconciling-the-regulatory-clock` |
| Startup AI data retention (Apr 2026) | `routine.co/blog/posts/ai-data-retention-startups` |
| Zero-retention AI processing | `grepture.com/guides/zero-retention-ai-processing` |

---

*End of document. Update status checkboxes as decisions are made.*
