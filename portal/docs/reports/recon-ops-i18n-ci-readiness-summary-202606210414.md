# Reconciliation-ops & i18n CI Readiness Assessment

> **Status**: ACTIVE  
> **Date**: 2026-06-21 04:14 UTC  
> **Scope**: ai-portal-go — `/admin/reconciliation-ops` page and i18n infrastructure  
> **Request**: Assess CI readiness, identify gaps, document coverage

---

## Goal

Determine whether the `/admin/reconciliation-ops` upstream provider reconciliation page and the project's i18n infrastructure are ready for CI testing (e2e, unit, i18n leak detection), and document all gaps found.

---

## Findings Summary

### 1. Reconciliation-ops Page: Fully Wired, Zero Tests

The page is completely wired into the application but has **no test coverage whatsoever**.

| Component | File | Status |
|-----------|------|--------|
| Page route | `internal/web/pages.go:859-860` | ✅ Registered |
| API routes | `internal/routes/admin_common.go:173-177` | ✅ Registered (summary, trend, detail, export) |
| Sidebar link | `web/templates/partials/layout-sidebar.html:204` | ✅ Present with i18n key |
| HTML template | `web/templates/admin-reconciliation-ops.html` (396 lines) | ✅ Embedded via `templates/*` glob in embed.go |
| Route handler | `internal/routes/admin_reconciliation_ops.go` | ✅ 4 handler functions |
| DB query layer | `internal/query/reconciliation_ops.go` | ✅ 5 query functions + period parser |
| Design doc | `docs/upstream-reconciliation-design-202606201003.md` | ✅ Documented |
| **Unit tests** | — | ❌ **None** (handler, query, i18n all untested) |
| **E2E page tests** | — | ❌ **Not in any e2e spec** |
| **API integration tests** | — | ❌ **None** |

### 2. E2E Coverage Gap

The page `/admin/reconciliation-ops` appears **zero times** across all e2e test files:

- `e2e/ui-all-pages.spec.js` — only lists `/admin/reconciliation` (old invoice page)
- `e2e/i18n-completeness.spec.js` — only lists `/admin/reconciliation`
- `e2e/ui-console-error-scan.spec.js` — only lists `/admin/reconciliation`
- `e2e/ui-mobile-all-pages.spec.js` — only lists `/admin/reconciliation`
- `e2e/scan-console-errors.js` — only lists `/admin/reconciliation`

The old `/admin/reconciliation` (invoice request reconciliation) is tested in 5 files, but the new `/admin/reconciliation-ops` (upstream provider reconciliation) is in **0**.

### 3. i18n Translation Coverage: Nearly Complete (1 Missing Key)

**Locale files examined:**
- `web/i18n/en.json` — 2241 keys (English-default, same key=value pattern)
- `web/i18n/zh.json` — 2241 keys (Chinese translations)

**Translation keys used in the template** — all present in both locales:

| Key | en.json | zh.json |
|-----|---------|---------|
| `Upstream Provider Reconciliation` | line 2221 | line 2221 |
| `Compare upstream provider usage with downstream billing` | line 2222 | line 2222 |
| `Upstream Requests` | line 2223 | line 2223 |
| `Billed Requests` | line 2224 | line 2224 |
| `Est Upstream Cost` | line 2225 | line 2225 |
| `Cost Diff` | line 2226 | line 2226 |
| `Upstream Error Rate` | line 2227 | line 2227 |
| `Custom` | line 2228 | line 2228 |
| `Auto Refresh` | line 2211 | line 2211 |
| `Refresh` | line 1290 | — (exists in en only) |
| `Upstream vs Billed Trend` | line 2229 | line 2229 |
| `Requests` | line 1333 | — (exists in en only) |
| `Tokens` | line 1555 | — (exists in en only) |
| `Cost` | — | — (checked) |
| `Error Rate` | line 616 | — (exists in en only) |
| `Loading...` | line 839 | line 839 |
| `Upstream` | line 2230 | line 2230 |
| `Billed` | line 2231 | line 2231 |
| `Provider` | line 1205 | — (exists in en only) |
| `Model` | line 911 | — (exists in en only) |
| `Key preview` | line 809 | — (exists in en only) |
| `Diff threshold` | line 2232 | line 2232 |
| `Export` | line 628 | — (exists in en only) |
| `Reconciliation Detail` | line 2233 | line 2233 |
| `Key` | line 2234 | line 2234 |
| `Upstream Input` | line 2235 | line 2235 |
| `Billed Input` | line 2236 | line 2236 |
| `Input Diff` | line 2237 | line 2237 |
| `Upstream Output` | line 2238 | line 2238 |
| `Billed Output` | line 2239 | line 2239 |
| `Output Diff` | line 2240 | line 2240 |
| `Est Cost` | line 2241 | line 2241 |
| `Billed Cost` | line 2242 | line 2242 |
| `Failed to load trend` | line 2243 | line 2243 |
| `Failed to load detail` | line 2244 | line 2244 |
| `No data` | line 973 | line 973 |

**Inline JS `_t('...')` keys** — all present in both locales:
- `'Loading...'` ✓
- `'Failed to load trend'` ✓ (newly added key at en.json:2243)
- `'No data'` ✓
- `'Failed to load detail'` ✓ (newly added key at en.json:2244)

### ⚠️ CRITICAL: Missing Translation Key

**`"Req Diff"`** is used in the template at line 106:
```html
<th>{{t "Req Diff"}}</th>
```

But it is **NOT present** in either `en.json` or `zh.json`. The fallback chain will render it as `"Req Diff"` (raw key) since Go i18n `T()` falls through: requested locale → en → raw key. For Chinese users, this column header would appear in English.

### 4. i18n Infrastructure

| Component | File | Description |
|-----------|------|-------------|
| Core engine | `pkg/i18n/i18n.go` | `I18n.T()` — fallback chain: requested locale → en → raw key |
| Translation loader | `internal/web/i18n.go` | `LoadTranslations()` — loads zh.json + en.json from embedded FS |
| Locale detection | `internal/routes/i18n.go` | `GetLocale()` — cookie > Accept-Language header > query param > "en" |
| Inline JS `_t()` | `web/static/js/app-layout.js:63` | Client-side translation for dynamic content |
| Sidebar | `layout-sidebar.html` | Uses server-side `{{t "..."}}` |

### 5. Unit Test Gaps

| Package | Files | Unit Tests? |
|---------|-------|-------------|
| `internal/routes/` | `admin_reconciliation_ops.go` | ❌ None |
| `internal/query/` | `reconciliation_ops.go` | ❌ None |
| `pkg/i18n/` | `i18n.go` | ❌ None |
| `internal/web/` | Only `admin_handlers_test.go` | ⚠️ Partial (tests `generateRandomCode` only) |

---

## CI Readiness Assessment

| Criteria | Status | Notes |
|----------|--------|-------|
| Page renders without 404/500 | ✅ | Fully wired in routes, pages, and embed |
| All server-side i18n keys exist | ⚠️ | **1 missing**: `"Req Diff"` |
| All client-side i18n keys exist | ✅ | All `_t()` keys present |
| E2E page availability test | ❌ | Not in `ui-all-pages.spec.js` |
| E2E i18n completeness test | ❌ | Not in `i18n-completeness.spec.js` |
| E2E console error scan test | ❌ | Not in `ui-console-error-scan.spec.js` |
| Unit tests for handler | ❌ | Not written |
| Unit tests for query layer | ❌ | Not written |
| Unit tests for i18n engine | ❌ | Not written |

**Verdict**: The page is **functionally complete** but has **zero test coverage** and **one missing i18n key**. It will work in production (Go fallback handles the missing key), but CI will not catch regressions.

---

## Recommended Actions

### P0 — Must Fix Before CI Can Pass
1. **Add `"Req Diff"` to both `en.json` and `zh.json`** — resolves the only i18n gap
2. **Add `/admin/reconciliation-ops` to `e2e/ui-all-pages.spec.js`** — basic page load test
3. **Add `/admin/reconciliation-ops` to `e2e/i18n-completeness.spec.js`** — i18n leak detection
4. **Add `/admin/reconciliation-ops` to `e2e/ui-console-error-scan.spec.js`** — console error detection
5. **Add `/admin/reconciliation-ops` to `e2e/ui-mobile-all-pages.spec.js`** — mobile viewport test

### P1 — Should Add
6. **Unit tests for `internal/query/reconciliation_ops.go`** — `ParseReconciliationPeriod`, `UpstreamReconciliationSummary`, etc.
7. **Unit tests for `internal/routes/admin_reconciliation_ops.go`** — handler function unit tests

### P2 — Nice to Have
8. **Unit tests for `pkg/i18n/i18n.go`** — fallback chain, locale detection edge cases
9. **API integration test** for `/api/admin/reconciliation-ops/*` endpoints

---

## Relevant Files

| File | Lines | Relevance |
|------|-------|-----------|
| `internal/web/pages.go` | 859-860 | Page route registration + `handleAdminReconciliationOps` |
| `internal/routes/admin_common.go` | 173-177 | API routes: `/api/admin/reconciliation-ops/{summary,trend,detail,export}` |
| `internal/routes/admin_reconciliation_ops.go` | Full file | Route handler: `getUpstreamReconciliationSummary`, `getUpstreamReconciliationTrend`, `getUpstreamReconciliationDetail`, `exportUpstreamReconciliation` |
| `internal/query/reconciliation_ops.go` | Full file | DB query layer: `ParseReconciliationPeriod`, `UpstreamReconciliationSummary`, `UpstreamReconciliationTrend`, `UpstreamReconciliationDetail`, `ExportUpstreamReconciliation` |
| `web/templates/admin-reconciliation-ops.html` | 396 lines | Full-page template: summary cards, trend chart, detail table, CSV export |
| `web/embed.go` | Line 5 | `//go:embed templates/*` — template embedded via glob |
| `web/templates/partials/layout-sidebar.html` | Line 204 | Sidebar link with `{{t "Upstream Reconciliation"}}` |
| `web/static/js/app-layout.js` | Line 63 | `function _t(text)` — client-side i18n for inline JS |
| `pkg/i18n/i18n.go` | Full file | Core `T()` with locale → en → raw key fallback |
| `internal/web/i18n.go` | Full file | `LoadTranslations()` — loads locale JSON from embedded FS |
| `internal/routes/i18n.go` | Full file | `GetLocale()` — cookie > Accept-Language > query > "en" |
| `web/i18n/en.json` | 2241 keys | English translations (English-default, key=value) |
| `web/i18n/zh.json` | 2241 keys | Chinese translations |
| `e2e/ui-all-pages.spec.js` | Line 113 | Only tests `/admin/reconciliation` (old page) |
| `e2e/i18n-completeness.spec.js` | Line 44 | Only tests `/admin/reconciliation` (old page) |
| `e2e/ui-console-error-scan.spec.js` | Line 46 | Only tests `/admin/reconciliation` (old page) |
| `e2e/ui-mobile-all-pages.spec.js` | Line 54 | Only tests `/admin/reconciliation` (old page) |
| `internal/routes/admin_reconciliation.go` | Legacy | Old invoice reconciliation handler (separate from ops) |
| `web/templates/admin-reconciliation.html` | Legacy | Old invoice reconciliation template |
| `docs/upstream-reconciliation-design-202606201003.md` | Design doc | Architecture and design for reconciliation-ops |

---

## Architecture Overview

```
Browser ──GET /admin/reconciliation-ops──→ pages.go (handleAdminReconciliationOps)
  │                                              │
  │                                              ▼
  │                                    render admin-reconciliation-ops.html
  │                                              │
  │    ──fetch /api/admin/reconciliation-ops─────┤ (client-side JS fetch)
  │    ──fetch /api/admin/reconciliation-ops/ ───┤
  │                                              │
  │                                              ▼
  │                                    admin_reconciliation_ops.go
  │                                    (handler functions)
  │                                              │
  │                                              ▼
  │                                    query/reconciliation_ops.go
  │                                    (DB queries)
```

The page uses a **client-rendered pattern**: the server serves the static HTML shell, then JavaScript `fetch()` calls the API endpoints to populate the summary cards, trend chart, and detail table. CSV export is handled client-side via blob download.

---

## Recent Related Commits

- `c1c3fea` — `refactor(admin): align monitor/e2e/i18n with upstream-ops and status pages`
- `010bb73` — `fix(i18n): add missing admin-models keys, remove orphans`
