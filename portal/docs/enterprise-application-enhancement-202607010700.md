# Enterprise Application Enhancement: Document Requirements & Anti-Abuse

**Date:** 2026-07-01  
**Scope:** `/admin/enterprise-applications` and `/enterprise-apply` pages  
**Status:** Design proposal

---

## Current State

The enterprise registration flow currently:
- Accepts free-text fields (name, country, reg number, address, bank info)
- Allows generic file uploads (PDF/JPG/PNG, max 10MB each, 50MB total) with **no document type classification**
- Has a unique constraint preventing duplicate active applications per user
- Checks that user isn't already in an enterprise
- Manual admin review (approve/reject/request-more-info)

### Problems Identified

1. **No document type specification** — admin must guess what each uploaded file is (business license? ID card? bank statement?)
2. **No structured validation requirements** — users don't know what documents are required
3. **Open to abuse** — any registered user can spam enterprise applications; no rate limiting, no account maturity checks

---

## Concern 1: Attachment Types & Requirements

### Recommended Document Categories (based on Stripe KYB / PayPal Business Verification)

| Category | Document Types | Required? | Purpose |
|----------|---------------|-----------|---------|
| **Business Registration** | Business license, Certificate of Incorporation, Commercial Register extract, ACRA BizFile (SG), 营业执照 (CN) | ✅ Required | Proves entity legally exists |
| **Tax ID / Registration Number** | Tax registration certificate, VAT certificate, EIN letter (US), GST certificate (SG/IN) | Recommended | Links entity to tax authority |
| **Proof of Address** | Utility bill (<3 months), bank statement header, government correspondence | Optional | Confirms operating address |
| **Authorized Representative** | Board resolution, Power of Attorney, appointment letter, company secretary certificate | ✅ Required | Proves applicant can act for the company |
| **Representative ID** | Passport, national ID card, driver's license | ✅ Required | Verifies identity of the person applying |
| **Bank Account Proof** | Bank statement (redacted), voided check, bank letter confirming account holder | Optional | For billing/invoicing purposes |

### Proposed UI Changes

**Step 2 (Upload Documents)** should show a structured checklist:

```
┌──────────────────────────────────────────────────────┐
│ Required Documents                                    │
│                                                       │
│ ☐ Business Registration Certificate                  │
│   (Business license, Certificate of Incorporation)   │
│   [Choose File] [Upload]                             │
│                                                       │
│ ☐ Authorized Representative Proof                    │
│   (Board resolution, Power of Attorney, or           │
│    appointment letter)                                │
│   [Choose File] [Upload]                             │
│                                                       │
│ ☐ Representative ID                                  │
│   (Passport, national ID, or driver's license)       │
│   [Choose File] [Upload]                             │
│                                                       │
│ Optional Documents                                    │
│                                                       │
│ ○ Tax Registration Certificate                       │
│ ○ Proof of Business Address                          │
│ ○ Bank Account Verification                          │
│   [Choose File] [Upload]                             │
└──────────────────────────────────────────────────────┘
```

### Database Changes

Add a `document_type` column to `enterprise_application_documents`:

```sql
ALTER TABLE enterprise_application_documents
  ADD COLUMN IF NOT EXISTS document_type VARCHAR(50) DEFAULT 'other';

-- Allowed values:
-- 'business_registration' — business license, incorporation cert
-- 'tax_certificate'       — tax/VAT/GST registration
-- 'proof_of_address'      — utility bill, bank statement header
-- 'representative_auth'   — board resolution, PoA, appointment letter
-- 'representative_id'     — passport, national ID, driver's license
-- 'bank_proof'            — bank statement, voided check
-- 'other'                 — catch-all for admin-requested docs
```

### API Changes

Upload endpoint should accept `document_type` as a form field:

```go
// In uploadEnterpriseApplicationDocument:
docType := c.PostForm("document_type")
if docType == "" {
    docType = "other"
}
validTypes := map[string]bool{
    "business_registration": true,
    "tax_certificate": true,
    "proof_of_address": true,
    "representative_auth": true,
    "representative_id": true,
    "bank_proof": true,
    "other": true,
}
if !validTypes[docType] {
    docType = "other"
}
```

### Admin Review Enhancement

In the review modal, show documents grouped by type with status indicators:

```
✅ Business Registration: company_license.pdf (2.1 MB)
✅ Representative ID: passport_scan.jpg (1.3 MB)
⚠️ Representative Auth: NOT UPLOADED
```

This makes it immediately clear to admin what's missing when deciding to "Request Info."

### Validation Rules (by country, optional future enhancement)

| Country | Business Reg Format | Tax ID Format |
|---------|-------------------|---------------|
| Singapore | UEN: 9-10 chars (e.g. 202012345A) | GST: M-prefixed |
| China | 统一社会信用代码: 18 chars | Same as reg number |
| USA | EIN: XX-XXXXXXX | Same |
| UK | Company Number: 8 digits | VAT: GB + 9-12 digits |
| EU (general) | Varies by state | VAT: CC + 8-12 digits |

For MVP: keep as free text but add format hint in placeholder. Post-MVP: add regex validation per country selection.

---

## Concern 2: Anti-Abuse / Spam Prevention

### Current Vulnerabilities

1. **Any user can apply** — even brand new accounts (0 usage, 0 balance)
2. **No cooldown after rejection** — user can immediately reapply
3. **No IP/device rate limiting** on application submissions
4. **Bulk user registration** → bulk enterprise applications is possible
5. **No account maturity requirement** — could register + apply in 1 minute

### Recommended Multi-Layer Defense

#### Layer 1: Account Maturity Requirements

```go
// Before allowing enterprise application:
// 1. Account must be at least N days old
// 2. Account must have verified email
// 3. Account must have made at least 1 successful API call OR 1 top-up

const MinAccountAgeDays = 7
const MinAPICallsForApplication = 1  // or MinTopUpAmount > 0

// Check:
var accountAge int
var totalCalls int
db.QueryRow(ctx, `
    SELECT EXTRACT(DAY FROM NOW() - created_at)::int,
           COALESCE((SELECT COUNT(*) FROM usage_records WHERE user_id = $1), 0)
    FROM users WHERE id = $1`, userID).Scan(&accountAge, &totalCalls)

if accountAge < MinAccountAgeDays {
    return error("Account must be at least 7 days old to apply for enterprise")
}
if totalCalls < MinAPICallsForApplication {
    return error("Account must have at least 1 API usage record before applying")
}
```

#### Layer 2: Application Cooldown

```go
// After rejection: 7-day cooldown before re-application
var lastRejected *time.Time
db.QueryRow(ctx, `
    SELECT reviewed_at FROM enterprise_applications
    WHERE applicant_user_id = $1 AND status = 'rejected'
    ORDER BY reviewed_at DESC LIMIT 1`, userID).Scan(&lastRejected)

if lastRejected != nil && time.Since(*lastRejected) < 7*24*time.Hour {
    return error("Please wait 7 days after rejection before reapplying")
}
```

#### Layer 3: Rate Limiting

```go
// Per-IP: max 3 applications per 24 hours
// Per-user: max 1 active application (already exists via unique index)
// Global: max 20 new applications per hour (circuit breaker for mass attacks)

// Add to existing auth_brute pattern:
key := fmt.Sprintf("enterprise_apply:%s", clientIP)
count := redis.Incr(ctx, key)
if count == 1 { redis.Expire(ctx, key, 24*time.Hour) }
if count > 3 {
    return error("Too many applications from this address. Try again tomorrow.")
}
```

#### Layer 4: Enterprise Name Deduplication (soft check)

```go
// Warn (don't block) if similar enterprise name already exists
var similarCount int
db.QueryRow(ctx, `
    SELECT COUNT(*) FROM enterprises
    WHERE LOWER(name) = LOWER($1) AND deleted_at IS NULL`,
    enterpriseName).Scan(&similarCount)

// Also check pending applications
db.QueryRow(ctx, `
    SELECT COUNT(*) FROM enterprise_applications
    WHERE LOWER(enterprise_name) = LOWER($1) AND status IN ('pending_review','approved')
    AND applicant_user_id != $2`,
    enterpriseName, userID).Scan(&pendingDupCount)

// If duplicates found: flag for admin attention, add warning badge in admin panel
```

#### Layer 5: Admin Dashboard Indicators

In the admin enterprise applications list, add risk indicators:

| Indicator | Meaning | Trigger |
|-----------|---------|---------|
| 🆕 | New account | Account age < 30 days |
| ⚠️ | No usage history | 0 API calls, 0 top-ups |
| 🔄 | Repeated applicant | Previously rejected |
| 👥 | Duplicate name | Similar enterprise already exists |
| 🚨 | Same IP cluster | Multiple applications from same IP range |

### Configuration (admin settings)

Add these to system settings (admin-configurable):

```
enterprise_apply_min_account_age_days = 7
enterprise_apply_min_usage_records = 1
enterprise_apply_cooldown_after_reject_days = 7
enterprise_apply_max_per_ip_per_day = 3
enterprise_apply_require_email_verified = true
```

---

## Implementation Priority

### Phase 1 (MVP — before deploy)

1. ✅ Add `document_type` column and upload UI with categorized slots
2. ✅ Add account maturity check (7-day minimum + email verified)
3. ✅ Add rejection cooldown (7 days)
4. ✅ Add per-IP rate limit on `/api/enterprise-application` POST

### Phase 2 (Post-MVP — first week)

5. Admin risk indicators (account age, usage, duplicates)
6. Enterprise name similarity warning
7. Country-specific registration number format validation

### Phase 3 (Future)

8. Automated business registry lookups (Singapore ACRA API, etc.)
9. Webhook notification to admin when new application submitted
10. Application expiry automation (already exists: 7-day `needs_more_info` timeout)

---

## Reference: Industry Standards

### Stripe KYB (Know Your Business)

- Business name + registration number (required)
- Business address (required)
- Ownership structure (>25% beneficial owners)
- Representative identity verification (passport/ID required)
- Tax ID (required in most jurisdictions)
- Bank account verification via micro-deposits

### PayPal Business Verification

- Government-issued ID of account holder
- Business registration documents
- Proof of address (utility bill <3 months)
- Bank statement showing business name
- Tax identification number

### Key Takeaway

Both Stripe and PayPal require **at minimum**:
1. Proof the business exists (registration document)
2. Proof the person is authorized (ID + authorization document)
3. Proof of address (optional but recommended)

Our platform should mirror this with the 3 required document types: Business Registration + Representative Auth + Representative ID.
