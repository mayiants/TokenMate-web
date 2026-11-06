# AI Proxy Go — Code Review & Test/Fix Plan

> **HISTORICAL** — 2026-05-04
> Final architecture reference: `web-frontend-architecture-v3-202605040636.md`
> Confirmed answers: `v3-answers-confirmed-202605040805.md`

---

## 一、Review Scope

| 项目 | 文件 | 代码行数 | 测试文件 |
|------|------|----------|----------|
| ai-proxy-go | 35 .go 文件 | ~11,372 | 13 test files (~2,275+ 行) |

---

## 二、Architecture Assessment vs V3 Requirements

### 2.1 V3 Architecture 对 Proxy 的期望

| V3 需求 | Proxy 现状 | 差距 |
|---------|-----------|------|
| **3 种 Key 前缀识别** (sk-wgit-/sk-wgit-tp-/sk-wgit-en-) | `access_mode` 从 claims 获取, 无前缀路由 | 需在 auth middleware 增加前缀解析 |
| **economy/general/single_model/subscription/enterprise 五种 access_mode** | handlers.go:80-84 已有 `getUserPriority()` 硬编码映射 | ✅ 已覆盖 |
| **Token Plan: 扣 token 额度** | 无 subscription 查询 | ❌ 需新增 Portal API 调用 |
| **Code Plan: 扣 request 额度** | 无 request counting per key | ❌ 缺失 |
| **Cache Hit/Miss 计费分离** | billing.go 已实现 (input/output/cache/ reasoning 分离) | ✅ |
| **Upstream key 使用追踪 (对账)** | 无 | ❌ 缺失 |
| **Enterprise 高优先级 pool** | keypool 有 priority 机制 | ✅ |
| **Economy 低优先级 pool + 折扣** | keypool 有 economy priority tier | ✅ |

### 2.2 符合度: ~70%

Proxy 核心转发 + 计费 + 优先级 pool 已就位，最大缺口在 **subscription 额度校验** 和 **upstream 对账**。

---

## 三、Bug 清单

### P0 — Critical (Block Production)

| # | 文件:行 | 问题 | 影响 | 修复方案 |
|---|---------|------|------|----------|
| **B1** | `internal/auth/auth.go:57` | `portalHealthy` 读写无 sync, goroutine race | 并发场景下 Portal 健康检查结果不确定 | `atomic.Bool` 替换 |
| **B2** | `internal/auth/auth.go:197-237` | AdminHandler: 当 `signature=""` 且 `adminSecret=""` 时 fallback 到 `m.cfg.Portal.SyncSecret` 比较 — 但若 SyncSecret 也未配, 空 string==空 string 直接通过 | 未配置 secret 时 admin 端点无保护 | 要求 adminSecret 非空或 fail startup |
| **B3** | `pkg/keypool/manager.go:340` | `updatePoolsFromSync` 强制 `key.Enabled = true` | Portal 中 disabled 的 key 被 Proxy 重新激活 | 移除强制赋值, 保留 Portal sync 过来的 enabled 状态 |
| **B4** | `pkg/billing/billing.go:169` | `CalculateCost` 方法签名参数 `model, provider string` 但实际调用 `GetPricing(model, provider)` 返回的是 `DefaultPricing()` 的结果，未从 Portal 同步 | Proxy 计费使用硬编码默认价，与 Portal 配置可能不一致 | 增加从 Portal sync pricing 的机制 |
| **B5** | `pkg/cache/affinity.go:240` | `getRequestFingerprint` 依赖 `ctx.Get("request_body")` — 但 handler.go 中是否 set 了此值需验证 | 若未 set, fingerprint 始终为 "", 亲和性缓存完全失效 | 在 handler 中确保设置 `request_body`, 或改用 hash request path+headers |

### P1 — High (Fix Before Beta)

| # | 文件:行 | 问题 | 影响 | 修复方案 |
|---|---------|------|------|----------|
| **B6** | 4 个 channel 目录 (ali/deepseek/moonshot/zhipu) | 目录存在但空 — 无适配器实现 | 这 4 个 Provider 无法通过 Proxy 转发 | 实现适配器 (可复用 openai adaptor, 仅改 BaseURL) |
| **B7** | `internal/server/handlers.go:532,624` | embeddings/images 每次新建 `http.Client`, 无连接池 | 高并发场景连接泄漏 | 复用 `sharedHandlerClient` |
| **B8** | `internal/ratelimit/ratelimit.go:276-281` | Redis sync 失败时 fallback 到本地计数, 但代码中缺 explicit "fail open/closed" 逻辑 | 需确认行为一致性 | 明确 fail-closed 策略: Redis 不可用时使用本地计数 + 更保守的阈值 |
| **B9** | `pkg/token/counter.go:29-31` | ~~"tokenEncoderCache 非线程安全"~~ → **已修复** — `sync.Map` 本身并发安全。但 `getTokenEncoder` 中 Load→miss→New→Store 存在 TOCTOU race (两个 goroutine 同时 miss 会创建两个 encoder) — 无害但浪费 | 轻微性能浪费 | 使用 `LoadOrStore` 替代 |
| **B10** | `pkg/cache/affinity.go:171-186` | ~~"buildSimpleCacheKey 始终返回 0"~~ → **已验证** — 代码正确, sha1 hash 构造 int 无问题 | — | 无需修复 |

### P2 — Medium (Improvement)

| # | 文件:行 | 问题 | 修复方案 |
|---|---------|------|----------|
| **B11** | `dto/openai.go:9` | `PromptCacheHitTokens` 字段定义为 int (OpenAI) 但 Claude 也使用 | 统一字段命名 |
| **B12** | `internal/server/admin_handlers.go:443-501` | `handleSyncKeys` controller 注释 "allow all requests" — 生产必须验证 | 添加 `AdminHandler()` middleware |
| **B13** | `internal/audit/audit.go:34` | AuditEntry 有 `RequestCount` 字段但未在 webhook 上报中填充 | 修复上报逻辑 |

---

## 四、V3 Architecture Gaps (功能缺口)

### 4.1 Gap 1: Subscription 额度校验

**现状**: Proxy 做 key validation 时只查 Portal, 返回 `access_mode` 和 `allowed_models`, 但 **不在 Proxy 侧检查 subscription 额度**。

**需要**: Proxy 在每次请求前查询 (或缓存) 用户 subscription 的剩余 token/request 额度。

**建议实现**:
```go
// pkg/keypool/manager.go — 扩展 KeyInfo
type KeyInfo struct {
    // ... existing fields
    SubscriptionType  string  // "token" / "code" / ""
    QuotaRemaining    int64   // token 额度剩余
    RequestsRemaining int     // request 额度剩余
    DailyQuotaUsed    int64   // 今日已用
    DailyQuotaLimit   int64   // 今日限额
}
```

### 4.2 Gap 2: Upstream Key 使用追踪

**现状**: Proxy 记录了 user key → upstream API 的调用, 但未记录 "哪个 upstream key 被使用"。

**需要**: 在 `usage_records` 中增加 `upstream_key_id` 字段, 用于与上游供应商对账。

**建议实现**:
```go
// 在 usage webhook 上报时增加字段
type UsageRecord struct {
    // ... existing
    UpstreamKeyID    string `json:"upstreamKeyId"`
    UpstreamProvider string `json:"upstreamProvider"`
}
```

### 4.3 Gap 3: Code Plan Request 计数

**现状**: 每次 API 调用自动计为 1 request, 但未与 Code Plan 订阅关联。

**需要**: 在 Proxy 侧累加 request count 并报告给 Portal; Portal 侧 (`webhook.go`) 判断是否需要按 request 扣费。

**建议**: AuditEntry 已有 `RequestCount` 字段 — 确保持久化到 Portal 的 usage_records。

### 4.4 Gap 4: Economy group 配置同步

**现状**: Proxy 的 access_mode 映射 (`handlers.go:80`) 硬编码了 economy→low priority, 但 **未从 Portal 同步 economy 组的折扣率和模型列表**。

**需要**: Portal sync 接口返回 economy group 配置 (折扣率, 可用模型, RPM 限制)。

---

## 五、Test & Fix Plan (四 Agent)

### Agent 1: Fix (Bug修复)

| Phase | Tasks | Estimated |
|-------|-------|-----------|
| **F-P1** | Fix B1 (`portalHealthy` → `atomic.Bool`) | 0.5h |
| **F-P1** | Fix B2 (AdminHandler empty secret bypass) | 0.5h |
| **F-P1** | Fix B3 (don't force Enabled=true in sync) | 0.5h |
| **F-P1** | Fix B5 (ensure `request_body` is set for affinity cache) | 1h |
| **F-P1** | Fix B7 (reuse shared HTTP client for embeddings/images) | 0.5h |
| **F-P2** | Fix B4 (load pricing from Portal instead of DefaultPricing) | 2h |
| **F-P2** | Implement 4 empty channel adapters (ali/deepseek/moonshot/zhipu via openai-compat) | 3h |
| **F-P2** | Fix B8 (rate limiter explicit fail-closed) | 1h |
| **F-P3** | Fix B9 (TOCTOU → LoadOrStore) | 0.5h |
| **F-P3** | Fix B10 (admin_handlers sync auth) | 0.5h |
| **F-P3** | Fix B13 (audit webhook request count) | 0.5h |
| **Total** | | **~10.5h** |

### Agent 2: Unit Test (新增测试)

| Module | Test Content | Framework |
|--------|-------------|-----------|
| `pkg/billing/billing_test.go` | Verify calculateCost matches V3 formula (input+output+cache+reasoning) | Go testing |
| `internal/auth/auth_test.go` | Test AdminHandler bypass when empty secret | httptest |
| `pkg/keypool/manager_test.go` | Test sync preserves Enabled state | mock |
| `internal/ratelimit/ratelimit_test.go` | Test fail-closed when Redis down | miniredis |
| `pkg/cache/affinity_test.go` | Test fingerprint builds correctly with request_body set | httptest |
| `internal/server/handlers_test.go` | Test access_mode priority routing | httptest |
| **Integration** | Test full flow: Portal sync → Proxy auth → keypool assign → billing → webhook | httptest + miniredis |

### Agent 3: Stress Test (性能+业务验证)

| Test | Tool | Target |
|------|------|--------|
| Key pool sync (1000 keys) | Custom script | < 5s sync time |
| SSE streaming (100 concurrent) | k6 | 99.9% success |
| Rate limit (local + Redis) | k6 | Local < 1μs, Redis < 5ms |
| Billing accuracy (10k requests) | Script | Token count vs cost < 0.1% error |
| Cache hit rate (affinity cache) | k6 + script | Verify cache hit improvement |
| 4 new providers (ali/deepseek/moonshot/zhipu) | curl | Each provider returns valid response |

### Agent 4: Review (安全+代码审查)

| Category | Check Items |
|----------|------------|
| **Auth** | JWT expiry/refresh, AdminHandler bypass, HMAC signature validation |
| **Key Pool** | Plaintext key storage (Redis persist → should encrypt), sync auth |
| **Billing** | Pricing accuracy vs Portal config, rounding errors, economy discount |
| **Rate Limit** | Fail-open vs fail-closed, Redis sync race |
| **Affinity Cache** | Fingerprint determinism, cache invalidation, memory leak |
| **Dependencies** | govulncheck, tiktoken-go version |
| **Concurrency** | All shared state audit (maps, counters, caches) |

---

## 六、V3 需要的 Portal→Proxy 接口扩展

### 6.1 现有接口 (保持不变)

| 接口 | 方向 | 状态 |
|------|------|------|
| `GET /api/apikeys/validate` | Proxy→Portal | ✅ key验证 |
| `GET /api/admin/models/sync` | Proxy→Portal | ✅ 模型同步 |
| `GET /api/admin/key-pool/sync` | Proxy→Portal | ✅ keypool同步 |
| `POST /api/webhook/usage` | Proxy→Portal | ✅ 用量上报 |
| `POST /api/webhook/usage-batch` | Proxy→Portal | ✅ 批量上报 |
| `POST /api/webhook/heartbeat` | Proxy→Portal | ✅ 心跳 |

### 6.2 新增接口

| 接口 | 方向 | 用途 | 优先级 |
|------|------|------|--------|
| `GET /api/admin/pricing/sync` | Proxy→Portal | 同步模型定价到 Proxy (替代 DefaultPricing) | P1 |
| `GET /api/admin/economy-groups/sync` | Proxy→Portal | 同步 economy 组配置 (折扣率/模型/RPM) | P1 |
| `GET /api/subscriptions/validate` | Proxy→Portal | 验证 subscription 额度 (token剩余/request剩余) | P0 |
| `POST /api/webhook/upstream-usage` | Proxy→Portal | 上报 upstream key 使用情况 (对账用) | P1 |

---

## 七、执行优先级

```
Phase 1 (Day 1-2): P0 Bug Fix + Subscription 额度校验
  ├── B1: portalHealthy race
  ├── B2: AdminHandler bypass
  ├── B3: Enabled=false after sync
  ├── B5: affinity cache fingerprint
  ├── New: GET /api/subscriptions/validate
  └── Agent 2: 并行测试 B1-B5 fixes

Phase 2 (Day 3-4): P1 Fixes + 4 Provider Adapters
  ├── B6: 4 channel adapters (ali/deepseek/moonshot/zhipu)
  ├── B4: Pricing sync from Portal
  ├── B7: Shared HTTP client
  ├── B8: Rate limit fail-closed
  ├── New: GET /api/admin/pricing/sync
  └── Agent 3: 压力测试 + 支付沙箱

Phase 3 (Day 5-6): P2 Fixes + Integration
  ├── B9-B13: TOCTOU, sync auth, webhook count
  ├── New: GET /api/admin/economy-groups/sync
  ├── New: POST /api/webhook/upstream-usage
  └── Agent 4: 安全审查 + 代码审查

Phase 4 (Day 7): Regression + Release
  ├── Full integration test (Portal ↔ Proxy ↔ Frontend)
  ├── Performance benchmark vs baseline
  └── Release notes
```

---

## 八、Summary

| 类别 | 统计 |
|------|------|
| **P0 Bugs** | 5 (atomic race, auth bypass, force enable, pricing mismatch, affinity dead) |
| **P1 Bugs** | 5 (4 adapters, HTTP client, rate limit, TOCTOU, sync auth) |
| **P2 Bugs** | 3 (dto naming, audit fields) |
| **V3 功能缺口** | 4 (subscription 校验, upstream 对账, Code Plan 计数, economy 配置同步) |
| **新增 API** | 4 (pricing sync, economy sync, subscription validate, upstream usage) |
| **估时** | ~10.5h fix + ~8h test + ~6h stress + ~4h review = **~28h total** |

**Proxy 成熟度**: 核心转发链路完整, 计费公式与 Node.js 一致, 吞吐量 +19% vs Node.js。主要补齐: subscription 额度在线校验 + upstream 对账 + 4 个 Provider 适配器。

---

*Document Version: 1.0*
*Next: Phase 1 kickoff — P0 Bug Fix + Subscription 额度校验*
