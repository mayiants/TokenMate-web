# CI Coverage Audit & Gate Upgrade — 2026-11-03

Status: ACTIVE

Audit of `.github/workflows/ci-portal.yml` (fast-gate + regression) for coverage
gaps that let real bugs through, triggered by the i18n "English value was Chinese"
bug that CI did not catch.

## What CI already does well
- fast-gate: `go build`, `go vet`, golangci-lint v2.14.0, schema drift check,
  govulncheck, gofmt, unit tests (middleware/web/pkg/performance), i18n audit
  (`--strict`), Tailwind rebuild, Vale English lint.
- regression: DB tests (`-p 1` db/routes/services), Playwright (ui-smoke +
  ui-all-pages), authenticated console-error scan (3 roles), authenticated
  axe-core a11y scan (critical/serious), public Lighthouse (/, /models, /login).

## Changes made in this pass (safe, verified)
1. **i18n_full_audit.py — new `chinese_en` check** (committed earlier `aef0f24`):
   en.json values containing Chinese now fail `--strict`. Closes the exact bug
   that shipped (hard gate, verified by self-test).
2. **check_on_conflict_constraints.py — fixed + wired into fast-gate**: the
   script missed `CREATE UNIQUE INDEX` (incl. partial `WHERE` indexes), producing
   false positives (user_devices, enterprise_invitations, enterprise_statements,
   notifications). Fixed to parse those; now passes (exit 0) and is a hard gate.
   Catches `ON CONFLICT (...)` without a matching unique constraint/index — a
   runtime-500 class that `check_schema_vs_code.sh` does not detect.
3. **i18n-completeness.spec.js — added to regression, report-only**: render-time
   DOM scan (EN must not show Chinese; ZH must not leak English) across all
   pages × 3 roles. Added with `continue-on-error: true` so it surfaces leaks in
   the CI log without blocking on legacy leaks. Promote to a hard gate (remove
   `continue-on-error`) after a confirmed clean run.

## Gaps found but NOT auto-fixed (need triage / could break CI)
- **scan-field-alignment.py (P0 intent, noisy)**: scans FE form fields vs BE
  request structs for silent field-drop (its docstring cites the real
  `pricing_ratio` drop bug). Currently exits 1 with ~15 findings, most of which
  are by-design (e.g. prices set via a separate pricing form, `sync_mode`/
  `model_permissions` backend-only). Needs an allow-list before it can gate.
- **Unused high-value e2e specs (P0/P1)**: `ui-admin-field-roundtrip.spec.js`
  (18 create/edit/delete→read-back tests catching silent field drops),
  `ui-crud`, `ui-admin-full-crud`, `ui-role-roundtrip`, `ui-subscriptions`,
  `ui-auth-flow`, `ui-mobile-*`, `ui-enterprise-apply`. All reuse the seeded E2E
  accounts and are CI-ready, but each should be validated green before wiring to
  avoid a red pipeline. Recommend adding `ui-admin-field-roundtrip` next (highest
  value) after a clean local/CI run.
- **ui-all-pages.spec.js uses hardcoded page arrays** (~51 paths) that drift from
  `pages.go` (~60 rendered templates). `:id` detail pages
  (`/enterprise/users/:id`, `/admin/*/:id/detail`, `/enterprise/alerts`,
  `/notifications`) are DB-query-heavy and unvisited. Recommend a route-manifest
  parity test + seeded-id detail-page visits.
- **golangci-lint uses defaults only (no .golangci.yml)**: high-value linters are
  OFF — `rowserrcheck`, `sqlclosecheck`, `bodyclose`, `gosec`, `errorlint`,
  `noctx`. The codebase has many manual `rows, _ := db.Query(...)` loops. Enabling
  these will surface pre-existing findings that must be fixed first, so do it as a
  dedicated cleanup PR, not inline.
- **internal/query tests are compile-guards** (`t.Skip("requires DB")`), and
  `internal/aws|storage|config|datax|query` are not in either CI `go test`
  package list (only compiled). Recommend adding them to the regression `go test`
  list and converting query skips to real DB-backed tests.
- **Lighthouse is public-only** (3 pages); authenticated pages have no perf/a11y
  budget.

## Recommended promotion order (each after a green run)
1. Promote i18n-completeness to a hard gate (remove continue-on-error).
2. Add `ui-admin-field-roundtrip.spec.js` to the Playwright step.
3. Add `.golangci.yml` enabling rowserrcheck/sqlclosecheck/bodyclose/gosec as a
   dedicated cleanup PR.
4. Add scan-field-alignment.py with an allow-list.
