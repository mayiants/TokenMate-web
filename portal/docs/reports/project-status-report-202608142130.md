# Project Status Report — WOODGIT AI Platform

> **Status:** ACTIVE  
> **Date:** 2026-08-14 21:30 UTC  
> **Scope:** Full platform review covering ai-portal-go (control plane) and ai-proxy-go (data plane)

---

## Executive Summary

The WOODGIT AI Platform is a two-service AI API gateway consisting of a **Portal** (control plane) and a **Proxy** (data plane). Both services are in production, healthy, and serving traffic. The platform has reached feature maturity with 163 active upstream models (via TokenRouter), full media billing (image/video/audio), multi-payment gateway support (Stripe/PayPal/Alipay/WeChat), and enterprise multi-tenancy.

Key metrics at a glance:

| Metric | Portal | Proxy |
|--------|--------|-------|
| Total commits | 1,519 | 339 |
| Go source files | 309 | 152 |
| Lines of code (Go) | ~94,800 | ~48,500 |
| Test files | 147 | 71 |
| August 2026 commits | 50 | 8 |
| Latest tag | v0.11.2 | GA_1.0.0 |
| Go version | 1.25.13 | 1.25.12 |
| Production status | Healthy (:3001) | Healthy (:8080) |
| CI status | ✅ Green | ✅ Green |
| Uncommitted changes | None | None |

---

## 1. Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        End Users / SDKs                          │
└──────────────────────────────┬──────────────────────────────────┘
                               │ HTTPS
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│                  ai-proxy-go (Data Plane)                        │
│  172.31.43.214:8080                                             │
│  • Request authentication (Portal key validation, 5min cache)   │
│  • Rate limiting (RPM/TPM/concurrent per key_type)              │
│  • Provider routing (tier/priority, affinity, fallback)          │
│  • Format conversion (OpenAI ↔ Claude ↔ Gemini)                │
│  • Streaming relay, circuit breaking                            │
│  • Pre-consume billing + usage reporting                        │
│  • Media endpoints (images, video, audio — async task support)  │
└───────────┬─────────────────────────────────────┬───────────────┘
            │ Webhooks (usage, pre-consume)        │ Upstream relay
            ▼                                      ▼
┌──────────────────────────────┐    ┌─────────────────────────────┐
│  ai-portal-go (Control Plane)│    │  Upstream AI Providers       │
│  172.31.45.213:3001          │    │  • TokenRouter (120 models)  │
│  • Admin portal (HTMX/Alpine)│    │  • OpenAI, Claude, Gemini   │
│  • API key management        │    │  • AWS Bedrock               │
│  • Billing engine + pricing  │    │  • DeepSeek, ByteDance, etc.│
│  • Enterprise multi-tenancy  │    └─────────────────────────────┘
│  • Payments (Stripe/PayPal/  │
│    Alipay/WeChat)            │
│  • Provider/model management │
│  • Subscription plans        │
│  • Reconciliation            │
└──────────────────────────────┘
```

**Infrastructure:**
- Portal EC2 (172.31.45.213): Portal container + PostgreSQL 15 + Redis 7
- Proxy EC2 (172.31.43.214): Proxy container + Redis
- Code sync via rsync (`sync-to-proxy.sh`); shared secrets: `ENCRYPTION_KEY`, `PROXY_SYNC_SECRET`

---

## 2. ai-portal-go — Control Plane

### 2.1 Codebase Structure

```
cmd/server/          — Entry point, bootstrap, route registration
internal/
  config/            — Env-based configuration
  db/                — PostgreSQL (pgx/v5) + Redis; schema.go = DDL source of truth
  middleware/        — JWT auth, RBAC, request logging
  query/             — Dashboard/admin query helpers (upstream ops, health)
  routes/            — HTTP handlers (30+ route files covering admin, billing, enterprise, etc.)
  services/          — Billing engine, pricing (token/media/variant), exchange rates, quota, rewards
  aws/               — AWS Bedrock integration
  storage/, datax/   — Storage and data helpers
  web/               — Server-rendered pages (handlers, templates, i18n, static)
  testutil/          — Test utilities
pkg/
  audit/             — Audit logging
  crypto/            — Encryption (AES for provider keys)
  i18n/              — Bilingual internationalization (en/zh)
  jwt/               — Token generation/validation
  mail/              — Email sending
  metrics/           — Prometheus metrics
  payment/           — Multi-gateway payments (Stripe, PayPal, Alipay, WeChat)
  queue/             — Billing queue
  telemetry/         — OpenTelemetry integration
  validation/        — Input validation
web/
  templates/         — 73 HTML templates (layout, admin, enterprise, user pages)
  static/            — CSS/JS/fonts/images; Tailwind CSS + Alpine.js + HTMX
  i18n/              — en.json, zh.json
migrations/          — 8 ordered SQL migration files
e2e/                 — 15 Playwright spec files
tests/               — Integration, performance, load test harnesses
scripts/             — CI checks (schema drift, i18n audit, Tailwind rebuild, English lint)
docs/                — 200 design/plan/report documents
```

### 2.2 Key Capabilities

| Area | Details |
|------|---------|
| Authentication | JWT + cookie-based sessions, token_version for forced logout, RBAC (user/enterprise_admin/super_admin) |
| API Key Management | General + enterprise keys, group-based model access, lifecycle management |
| Billing Engine | Token-based (input/output/cache), media variants (image size, video duration), per-request fixed fees, micro-USD precision |
| Payments | Stripe, PayPal (webhook verified), Alipay, WeChat Pay; subscription + top-up |
| Enterprise | Multi-tenancy, self-registration, invitation system, per-enterprise billing, member management |
| Pricing | Rules engine with token/media/flat modes, variant matching (resolution, duration, pixel thresholds), upstream/downstream price management |
| Provider Management | Multi-provider support, health monitoring, model routing rules (tier/priority) |
| Reconciliation | Upstream cost reconciliation, invoice generation, dead letter queue for failed billing |
| Frontend | Server-rendered HTMX + Alpine.js + Tailwind; brand design system with gradient themes; responsive; bilingual (en/zh) |
| Observability | Audit logging, usage analytics, Prometheus metrics, OpenTelemetry |

### 2.3 August 2026 Development (50 commits)

Major work streams:

1. **Media Billing Implementation** (Aug 12)
   - New `billing_mode='media'` with `PriceVariant` structure
   - Variant matching by image size, resolution, pixel thresholds, video duration
   - TokenRouter model import script with variant pricing extraction
   - Audio model type support

2. **Design System Overhaul** (Aug 13)
   - Brand gradient variables (indigo → purple → violet)
   - Banner style templates (6 gradient + 4 solid options)
   - Increased base font (14→15px), button/topbar sizing
   - Empty state illustrations, card hover effects
   - Landing page hero redesign with model marquee

3. **Status Page Fixes** (Aug 14)
   - Provider-aware model metadata for health view
   - Media type detection via `supported_types` join
   - Go 1.25.13 security upgrade (7 stdlib vulnerabilities patched)
   - Keyboard accessibility for scrollable data tables (axe-core compliance)

4. **Navigation & UX Restructuring** (Aug 12)
   - Sidebar reorganized by functional groups
   - Invoices/Addresses merged into Billing Records (5-tab layout)
   - Plan & Group renamed to Subscription & Group

### 2.4 Release Tags

| Tag | Commit | Description |
|-----|--------|-------------|
| v0.11.2 | `876db76` | Provider-aware status page fix |
| v0.11.1 | `f2d3d03` | Go 1.25.13 security upgrade |
| v0.11.0 | `6c624ab` | Accessibility fixes (scrollable tables) |
| v0.10.0 | — | Design system + media billing |
| GA_1.0.0 | — | General availability baseline |

---

## 3. ai-proxy-go — Data Plane

### 3.1 Codebase Structure

```
cmd/server/          — Entry point
cmd/stress/          — Load testing tool
internal/
  server/            — HTTP handlers: chat, responses, embeddings, images, audio, video, models, status
  auth/              — Key validation against Portal + balance cache (Lua scripts)
  ratelimit/         — RPM/TPM/concurrent limits (per key_type: group vs subscription)
  config/            — Viper YAML config + env overrides
  middleware/        — Request middleware
  audit/             — Audit trail
  retention/         — Data retention policies
pkg/
  relay/             — Provider adapter interface + base adaptor
  convert/           — Format conversion: OpenAI ↔ Claude ↔ Gemini (including streaming)
  routing/           — Budget controller, scene classifier
  keypool/           — Upstream key pool management, health tracking, sync, selection, affinity
  billing/           — Pre-consume billing, usage record construction
  token/             — Tiktoken token counting
  cache/             — Redis caching layer
  circuitbreaker/    — Circuit breaker for upstream failures
  sse/               — Server-sent events streaming
  metrics/           — Prometheus metrics
  telemetry/         — OpenTelemetry
  geoip/             — Geo-IP based routing
  plugin/            — Plugin system
  pool/              — Connection pooling
  models/            — Model definitions
  fallbacklog/       — Fallback logging
dto/                 — OpenAI/Claude request/response types
types/               — Relay types
tests/               — E2E, integration, performance tests
docs/                — 44 design/analysis documents
```

### 3.2 Key Capabilities

| Area | Details |
|------|---------|
| API Endpoints | `/v1/chat/completions`, `/v1/responses`, `/v1/embeddings`, `/v1/images/generations`, `/v1/video/generations`, `/v1/audio/speech`, `/v1/models` |
| Format Conversion | OpenAI ↔ Claude ↔ Gemini bidirectional, streaming support |
| Routing | Multi-tier priority (high/normal/economy), model-level affinity, cross-provider fallback |
| Key Pool | Upstream key management with health tracking, model-level cooldown, sync from Portal |
| Rate Limiting | RPM, TPM, concurrent (per key type); hard limits (TPH/TPMonth); soft limits (RPD/TPD) |
| Billing | Pre-consume estimation, actual usage settlement, partial token billing on stream interrupts |
| Reliability | Circuit breaker, usage buffer with exponential retry, settle buffer, dead letter queue |
| Media Support | Async image/video generation with task polling, audio speech/generation |
| Security | Key validation caching (5min TTL), negative caching for disabled keys, pub/sub invalidation |

### 3.3 August 2026 Development (8 commits)

1. **Media Billing & Endpoints** (Aug 10-14)
   - Video handler: canonical model name rewrite, size metadata forwarding
   - Image/video `output_tokens` parsing from upstream response
   - Longer media timeouts, response-size based billing
   - Audio endpoints (`/v1/audio/speech`, `/v1/audio/generations`)
   - Image/video media metadata forwarding in usage records
   - Async generation endpoints for TokenRouter

2. **Stream Interrupt Billing** (Aug 4)
   - Bill partial tokens on Anthropic stream interruptions

### 3.4 Handler Coverage

| Handler | Endpoint | Status |
|---------|----------|--------|
| Chat | `/v1/chat/completions` | ✅ Production |
| Responses | `/v1/responses` | ✅ Production |
| Embeddings | `/v1/embeddings` | ✅ Production |
| Images | `/v1/images/generations` + task polling | ✅ Production |
| Video | `/v1/video/generations` + task polling | ✅ Production |
| Audio | `/v1/audio/speech`, `/v1/audio/generations` | ⚠️ Limited (upstream dependency) |
| Models | `/v1/models` | ✅ Production |
| Status | Health checks | ✅ Production |

---

## 4. Deployment Status

### 4.1 Current Production State

| Component | Host | Container | Health | Last Build |
|-----------|------|-----------|--------|------------|
| Portal | 172.31.45.213 | ai-portal-go | ✅ Healthy | 2026-08-14 17:17 UTC |
| PostgreSQL | 172.31.45.213 | ai-portal-postgres | ✅ Healthy | Up 2 days |
| Redis (Portal) | 172.31.45.213 | ai-portal-redis | ✅ Healthy | Up 2 days |
| Proxy | 172.31.43.214 | ai-proxy-go | ✅ Healthy | 2026-08-14 (same session) |
| Redis (Proxy) | 172.31.43.214 | ai-proxy-redis | ✅ Healthy | — |

### 4.2 CI/CD Pipeline

**Portal CI** (`.github/workflows/ci-portal.yml`):
- Fast-gate: build, vet, golangci-lint v2.5.0, govulncheck, gofmt, schema drift check, unit tests, i18n audit, Tailwind rebuild, English lint
- Regression: PostgreSQL 15 + Redis 7 services, DB-dependent tests, Playwright (console errors, accessibility, Lighthouse)
- Latest: `876db76` — fast-gate ✅, regression ✅

**Proxy CI** (`.github/workflows/ci-proxy.yml`):
- Build, vet, govulncheck, gofmt, golangci-lint v2.5.0, `go test -race ./...`, Docker build, transform E2E
- Latest: `6bbef54` — all jobs ✅

### 4.3 API Verification (Production, 2026-08-14)

| Model | Type | HTTP | Cost (µUSD) | Verification |
|-------|------|------|-------------|--------------|
| `deepseek-v4-flash` | text | 200 | 71 | input 88×$0.5/M + output 10×$2/M + reasoning 10×$0.75/M ✅ |
| `bytedance-seed/seedream-5.0-lite` (1920×1920) | image | 200 | 36,000 | variant $0.035 + fixed $0.001 ✅ |
| `kling-v3` (5s) | video | 200 (async) | 561,000 | fixed $0.561 ✅ |

Balance reconciliation verified: `78408043 - 71 - 36000 - 561000 = 77810972` µUSD — exact match.

---

## 5. Quality & Testing

### 5.1 Test Infrastructure

| Category | Portal | Proxy |
|----------|--------|-------|
| Unit/integration test files | 147 | 71 |
| E2E Playwright specs | 15 | — |
| Shell-based smoke tests | ✅ (`test-portal-quick.sh`) | ✅ (`e2e-internal-transform.sh`) |
| CI checks | 12 distinct steps | 8 distinct steps |
| Schema drift check | ✅ | N/A |
| i18n completeness audit | ✅ (strict mode) | N/A |
| Accessibility (axe-core/Lighthouse) | ✅ (CI gated) | N/A |
| Race detector | — | ✅ (`-race` flag) |

### 5.2 Code Quality

- **Zero TODOs/FIXMEs** in both codebases
- **Clean git status** on both repos (no uncommitted changes)
- **golangci-lint v2.5.0** enforced in CI for both
- **govulncheck** enforced — Go 1.25.13 upgrade resolved 7 stdlib vulnerabilities
- **gofmt** enforced — zero formatting drift

---

## 6. Recent Major Milestones (July–August 2026)

| Date | Milestone | Impact |
|------|-----------|--------|
| Jul 26 | Platform security review + 4-batch fix | Fixed billing edge cases, payment verification, auth vulnerabilities |
| Jul 27-29 | Pre-launch review (5 phases) | Permission system hardening, funds chain reliability, RBAC enforcement |
| Aug 4 | Enterprise key model access fix | Prevented model list exposure for ungrouped enterprise keys |
| Aug 12 | Media billing implementation | Image/video/audio pricing with variant support (11 models) |
| Aug 12 | TokenRouter model import | 120 models imported with variant pricing metadata |
| Aug 13 | Design system overhaul | Brand gradients, responsive layout, accessibility |
| Aug 14 | Go 1.25.13 security upgrade | 7 stdlib CVEs resolved |
| Aug 14 | Provider-aware status page | Correct media health thresholds per provider |

---

## 7. Model Coverage

- **Total active models:** 163 (149 unique by `api_model_name` after multi-provider dedup)
- **By type:** Text 104, Image 8, Video 8, Multi-type (text+image) 3+
- **Providers:** TokenRouter (primary, 120 models), plus OpenAI, Claude, Gemini, AWS Bedrock, DeepSeek, ByteDance, Kuaishou, MiniMax, etc.
- **Billing modes:** Token (input/output/cache per M), Media (variant-based: resolution/duration/pixel), Flat (per-request fixed)

---

## 8. Known Limitations & Open Items

### 8.1 Operational

| Item | Severity | Notes |
|------|----------|-------|
| Audio endpoints limited | Low | TokenRouter doesn't support TTS/audio generation for most models; `tts-1` has no upstream key |
| Proxy Go version 1.25.12 | Low | Portal upgraded to 1.25.13; proxy should follow |
| HANDOVER.md contains live PAT | Medium | GitHub PAT exposed in plaintext; rotation recommended |
| PayPal webhook_id not configured | Low | Webhook signature verification will fail without it |

### 8.2 Technical Debt

| Item | Location | Notes |
|------|----------|-------|
| Legacy `DB_*` env vars in proxy | ai-proxy-go config | Unused but still referenced in config struct |
| Proxy golangci-lint suppressions | `.golangci.yml` | errcheck/ineffassign/staticcheck/unused disabled for legacy code |
| Node.js dependency for full CI | Portal EC2 | Tailwind rebuild and Playwright require Node (only available in CI runner) |

### 8.3 Recommendations

1. **Upgrade Proxy to Go 1.25.13** — align with Portal for security parity
2. **Rotate GitHub PAT** in HANDOVER.md — exposed credential
3. **Configure PayPal webhook_id** — enable fail-closed webhook verification
4. **Enable golangci-lint checks in proxy** — reduce technical debt incrementally
5. **Audio endpoint completion** — depends on upstream provider availability

---

## 9. Commit Activity Trend

```
Month       Portal    Proxy    Combined
2026-04        —         6          6
2026-05      359       101        460
2026-06      608        66        674
2026-07      502       158        660
2026-08       50         8         58  (14 days)
─────────────────────────────────────────
Total      1,519       339      1,858
```

August pace reflects stabilization after intensive July security hardening and feature development.

---

## 10. Documentation

- **Portal docs:** 200 markdown files covering designs, plans, reviews, reports, and legal docs
- **Proxy docs:** 44 markdown files covering architecture, routing strategies, and integration guides
- **Key recent docs:**
  - `media-api-support-status-202608121200.md` — current image/video/audio API endpoint support
  - `ui-optimization-tokenrouter-inspired-202608120345.md` — UI redesign plan
  - `e2e-roundtrip-report-202608041327.md` — three-role E2E test report
  - `pre-launch-review-key-user-member-usage-202607270342.md` — comprehensive pre-launch security review

---

*Report generated automatically from codebase analysis on 2026-08-14 21:30 UTC.*
