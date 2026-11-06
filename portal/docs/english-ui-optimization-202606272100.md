# English Copy & UI/UX Optimization Report

**Date:** 2026-06-27 21:00 UTC  
**Scope:** `ai-portal-go` user-facing English (`web/i18n/en.json`, templates)  
**Context:** Project at ~92% feature-complete, preparing MVP deployment. Previous development used Chinese AI models for English copy, resulting in unnatural phrasing.

---

## 1. English Copy — Issues Found

### 1.1 Terminology (Chinese SaaS jargon leaked into English)

| Current Term | Problem | Recommended | Occurrences |
|---|---|---|---|
| **Recharge** | Direct translation of 充值; not used in English SaaS | **Top Up** / **Add Funds** | 31+ |
| **Supplier** | 供应商 translated literally; context is AI provider | **Provider** | 6+ |
| **Channel** | 渠道 — Chinese API proxy industry jargon | **Route** / **Routing Path** | scattered |
| **Deduction** | 扣款 — sounds like punishment | **Charge** / **Usage** | 5+ |
| **Intelligent Routing** | 智能路由 — overly formal | **Smart Routing** | 2 |
| **Quota Exhausted** | 额度耗尽 — dramatic | **Out of Credits** | varies |
| **Please input** | 请输入 — calque | **Enter** / **Type** | varies |

### 1.2 Sentence Pattern Issues

| Pattern | Example | Fix |
|---|---|---|
| Colon-terminated fragments | `Fetch failed: `, `Load failed: ` | Use full sentence: `Failed to load data` |
| Overly formal/wordy | "To protect your account security and recharge experience, you can recharge up to 30 times per month" | "You can top up up to 30 times per month. Unpaid orders close after 24 hours." |
| Chinese phone placeholder | `+86 138 0000 0000` | `+1 (555) 000-0000` for international audience |
| Double negatives / passive | "Not specified (use the default method)" | "Default (not specified)" |
| Run-on help text | Entire paragraphs as single i18n keys | Break into heading + body |

### 1.3 Inconsistencies

- `Are you sure you want to ` (trailing space) — incomplete key
- `Are you sure you want to add ` (trailing space) — incomplete key
- Duplicate keys with different casings: `Approval Failed` vs `Approval failed`
- Mixed `*` marker usage: `Key Name *` vs `Key type *`
- Some keys are dot-namespaced (`billing.recharge`, `mail.enterprise.alert.note`) while most are plain sentences

---

## 2. UI/UX Optimization Recommendations (No Feature Changes)

### 2.1 Accessibility Quick Wins

| Area | Issue | Fix |
|---|---|---|
| Color contrast | Some `text-muted` on light backgrounds may fail WCAG AA | Ensure minimum 4.5:1 contrast ratio |
| Focus indicators | Interactive elements need visible `focus-visible:ring-2` | Add Tailwind focus classes globally |
| ARIA labels | SVG icons in stat cards lack `aria-label` or `aria-hidden` | Add `aria-hidden="true"` to decorative SVGs |
| Form labels | Some inputs use placeholder-only labeling | Add visually-hidden `<label>` elements |
| Keyboard navigation | Tab order in modals and dropdowns | Ensure `tabindex` and focus trap |

### 2.2 Visual Polish (CSS/Tailwind only)

| Area | Recommendation |
|---|---|
| Loading states | Add `animate-pulse` skeleton screens for HTMX-loaded content |
| Transitions | Add `transition-colors duration-150` to buttons and links |
| Empty states | Replace bare "No data" text with SVG illustration + helpful message |
| Card spacing | Standardize on `p-4` (compact) or `p-6` (spacious) — not mixed |
| Error messages | Use a consistent toast component positioning (top-right) |
| Typography | Enforce hierarchy: `text-sm` labels, `text-base` body, `text-lg` headings |

### 2.3 Performance

| Area | Tool | Expected Impact |
|---|---|---|
| CSS bundle | Run `npx purgecss` or rely on Tailwind JIT | Reduce CSS from ~300KB to ~30KB |
| Image/SVG | Inline SVGs are fine; ensure `loading="lazy"` on any `<img>` | LCP improvement |
| JS bundle | HTMX+Alpine are already small; ensure `defer` on all scripts | Already done ✓ |

---

## 3. Recommended Tools

### English Copy Auditing

| Tool | Type | Best For |
|---|---|---|
| **Vale** (GitHub: errata-ai/vale) | CLI linter | Style guide enforcement (Google/Microsoft rules) |
| **write-good** (GitHub: btford/write-good) | CLI | Passive voice, weasel words |
| **LanguageTool** | API/self-hosted | Full grammar checking |
| **GPT-4/Claude batch** | API | Rewriting Chinese-model English to natural SaaS English |
| **Grammarly Business** | Commercial | Tone consistency at scale |

### UI/UX Auditing

| Tool | Type | Best For |
|---|---|---|
| **Lighthouse CI** | CLI/CI | Performance, accessibility, SEO scores |
| **axe-core** / **pa11y** | CLI | WCAG accessibility violations |
| **Playwright accessibility** | E2E test | Integrate into existing test suite |
| **CSS Stats** | Web tool | Spotting inconsistent spacing/colors |

---

## 4. Action Plan

### Phase 1: English Copy (2-4 hours)

1. Run automated scan on `en.json` for known patterns (recharge→top up, supplier→provider, etc.)
2. Fix terminology globally via search-replace
3. Rewrite awkward sentences (batch of ~50 longest strings)
4. Remove duplicate/trailing-space keys
5. Standardize error message format: `"Failed to {action}"` pattern

### Phase 2: Accessibility (1-2 hours)

1. Run Lighthouse on all major page types (dashboard, keys, billing, admin)
2. Run axe-core for WCAG AA violations
3. Fix critical findings (contrast, missing labels, focus indicators)

### Phase 3: Visual Polish (2-3 hours)

1. Add transition utilities to button/link classes
2. Implement skeleton loading states
3. Standardize card padding and empty states
4. Add visual regression snapshots to prevent regressions

---

## 5. Audit Results (2026-06-28)

### 5.1 Lighthouse Scores (Dashboard, authenticated)

| Category | Score |
|---|---|
| Accessibility | **95/100** |
| Performance | **56/100** (throttled local VM) |
| Best Practices | **77/100** |

#### Performance Breakdown
| Metric | Value | Notes |
|---|---|---|
| First Contentful Paint | 6.2s | Heavily affected by VM CPU throttling |
| Largest Contentful Paint | 7.2s | Same — production should be <2s |
| Total Blocking Time | 260ms | Acceptable |
| Cumulative Layout Shift | 0 | Perfect |

#### Performance Opportunities
- Reduce unused CSS: 69 KiB savings
- Reduce unused JavaScript: 256 KiB savings
- Minify CSS: 16 KiB savings
- Minify JavaScript: 12 KiB savings

### 5.2 axe-core Results (9 pages tested)

| Page | Violations | Serious/Critical | Passes |
|---|---|---|---|
| /dashboard | 1 | 1 | 31 |
| /billing | 2 | 2 | 36 |
| /admin/users | 3 | 3 | 38 |
| /admin/models | 3 | 3 | 40 |
| /admin/providers | 4 | 4 | 39 |
| /admin/settings | 2 | 2 | 33 |
| /admin/announcements | 2 | 2 | 37 |
| /login | 1 | 1 | 30 |
| /register | 1 | 1 | 30 |

**Total: 27 violations across 9 pages (21 serious/critical)**

### 5.3 Top Issues by Category

#### Critical: Form Labels (affects 5 pages)
```html
<!-- Problem: inputs without associated <label> -->
<input type="number" name="sort_order" class="form-input" value="0" min="0">
<select id="status-filter" class="form-select" onchange="...">
```
**Fix:** Add `<label for="...">` or `aria-label="..."` to all form inputs.

#### Serious: Color Contrast (affects 6 pages)
```html
<!-- Problem: insufficient contrast ratios -->
<span class="badge badge-success">Active</span>
<div class="stat-value text-orange">$0.000000</div>
<button class="amount-btn active">$50</button>
```
**Fix:** Darken `.text-orange`, `.badge-success`, `.amount-btn.active` colors to meet 4.5:1 ratio.

#### Serious: Link Distinguishability (affects 3 pages)
```html
<!-- Problem: links rely only on color -->
<a href="/register" class="text-primary">Sign Up</a>
```
**Fix:** Add `underline` or `font-weight: 600` to in-text links.

#### Serious: Logo Link Missing Name
```html
<a href="/dashboard" class="topbar-logo">...</a>
```
**Fix:** Add `aria-label="Home"` to the logo link.

---

## 6. Success Metrics

| Metric | Current | Target |
|---|---|---|
| Lighthouse Accessibility | 95/100 | 100 |
| Lighthouse Performance | 56 (local VM) | 85+ (production) |
| axe-core critical violations | 0 ✅ | 0 |
| axe-core serious violations | 0 ✅ | 0 |
| Unnatural English strings | 0 (fixed) | 0 ✅ |
| Inconsistent terminology | 0 (fixed) | 0 ✅ |

## 7. Priority Fix List

| # | Item | Status | Notes |
|---|------|--------|-------|
| 1 | **[P0] Form labels** | ✅ Done | Batch added `aria-label` to 97 `<select>` and 165 `<input>`; axe-core now reports 0 violations |
| 2 | **[P0] Color contrast** | ✅ Done | Updated `.trend-*`, `.badge-soft-*`, `.flash-*` to use `-text` color variables |
| 3 | **[P1] Link styles** | ✅ Done | `a.text-primary { text-decoration: underline; }` added to `app.css` |
| 4 | **[P1] Logo aria-label** | ✅ Done | `aria-label="Home"` added to topbar logo in `layout.html` |
| 5 | **[P1] Focus indicators** | ✅ Done | `focus-visible` outline added to form controls, buttons, sidebar items in `app.css` |
| 6 | **[P1] Decorative SVG aria-hidden** | ✅ Done | All decorative SVGs use `aria-hidden="true" focusable="false"` |
| 7 | **[P2] Unused CSS/JS** | ⏸️ Deferred | User decision: do not minify/bundle-slim for now; sizes are acceptable |
| 8 | **[P2] Minification** | ⏸️ Deferred | Same as above |

## 8. Status Update (2026-06-28)

### Completed

- English Copy Scheme A implemented and verified via `scripts/i18n_full_audit.py --strict`.
- Landing page hero visual removed; Quick Start connector line completed across all 3 steps.
- Empty state component standardized (`empty-state` / `empty-state-sm`) with unified "No data" copy across 27+ pages.
- Card padding standardized to 24px (`p-6`) for primary content cards.
- HTMX skeleton loading state added (`.htmx-request` pulse animation).
- Link transitions added (`transition-colors duration-150`).
- Tools/metrics wired into CI:
  - axe-core: **gate enabled** on 37 pages across all roles
  - Lighthouse: gate enabled on public pages (Landing, Login)
  - Vale English copy lint on `web/i18n/en.json` (report-only; migrated from write-good)

### Remaining

- Resolve Vale `write-good` warnings/suggestions in `web/i18n/en.json` if stricter gate is desired (currently report-only by design).
