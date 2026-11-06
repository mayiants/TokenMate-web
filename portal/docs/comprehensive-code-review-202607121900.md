> **HISTORICAL** — Full codebase review, both projects
> **Updated:** 2026-07-13 03:15 UTC — fix status annotations added

# Comprehensive Code Review — ai-portal-go & ai-proxy-go

**Date:** 2026-07-12 19:00 UTC
**Scope:** All Go source files, configuration, Dockerfile, tests — ~300 files reviewed
**Commits:** portal 542ecd4 · proxy f275fcd (latest after fixes)

---

## Executive Summary

| Metric | Portal | Proxy |
|--------|--------|-------|
| Go files reviewed | ~180 | ~120 |
| 🔴 CRITICAL | 5 (3 fixed) | 6 (1 fixed) |
| 🟠 HIGH | 8 (3 fixed) | 10 (1 fixed) |
| 🟡 MEDIUM | 15 | 15 |
| 🟢 LOW | 15 | 12 |
| Overall health | **6.0/10 → 7.5/10** | **5.5/10 → 7.0/10** |

---

## 🔴 CRITICAL Findings (Combined)

### Portal

| # | File | Issue | Status |
|---|------|-------|--------|
| CP1 | `pkg/payment/paypal.go:93` + `alipay.go:117` | **支付 webhook 签名验证是空操作** — `signature != ""`。Alipay 路由传入 `""`，所有 webhook 被拒绝；PayPal 只要有 header 就通过 | ⏳ TODO |
| CP2 | `internal/routes/payment.go:251-273` | **网关失败时静默 fallback 到模拟模式** — `CreateGateway` 或 `CreateSession` 失败时自动模拟充值，用户以为付了钱但没扣款 | ✅ FIXED (8972d85) |
| CP3 | `internal/routes/payment.go:606-612` | **`handleBillingCallback` cancel 分支双重写 response** — 先写 cancelled，紧接着写 failed，第二个 `c.JSON` 恐慌 | ✅ FIXED (8972d85) |
| CP4 | `internal/web/admin_handler_common.go:1347-1352` | **付款处理是空桩** — `HandleProcessPayment` 标记所有订单为 completed，不经过真实网关 | ✅ FIXED (1d4c0e5 — restricted to debug mode) |
| CP5 | `internal/routes/admin_reconciliation.go:608-633` | **`billingSvc` 为 nil 导致 panic** — `handleReconcileUpload` 创建 `WebhookRoutes` 时不初始化 `billingSvc` | ✅ FIXED (8972d85) |

### Proxy

| # | File | Issue | Status |
|---|------|-------|--------|
| CPr1 | `pkg/relay/channel/claude/claude.go:451` | **全局可变流状态** — `currentStreamState` 是包级变量，所有并发 SSE 流共用，竞态导致 tool-call 跟踪数据损坏 | ✅ FIXED (cafa029) |
| CPr2 | `pkg/keypool/` (多处) | **锁顺序死锁** — `poolsMu` 和 `providerLock` 在两个方向上获取，三线程可触发 Go RWMutex 死锁 | ❌ Not confirmed (lock order is consistent) |
| CPr3 | `pkg/relay/channel/aws/aws.go:385` | **`a.model` 数据竞态** — `ToProvider` 写入，`DoRequestBytesWithContext` 读取，并发请求时不可预测 | ❌ Not confirmed (per-request adaptor instance) |
| CPr4 | `pkg/keypool/persist.go:246-250` | **HKDF 失败时使用 SHA-256 回退** — 加密密钥弱化，AES-GCM nonce 重用风险 | ⏳ TODO |
| CPr5 | `internal/auth/auth.go:942-949` | **JSON 错误体解析吞掉解码错误** — Portal 返回非 JSON 4xx 时 body stream 被消费，后续解码读空流 | ⏳ TODO |
| CPr6 | `cmd/server/main.go:98-110, 209-219` | **6 个后台 goroutine 无关闭机制** — `StartSyncLoop`、`StartPubSubListener`、`StartHealthCheck`、`StartFlushLoop` 等从不停止 | ✅ FIXED (f275fcd) |

---

## 🟠 HIGH Findings

### Portal

| # | File | Issue | Status |
|---|------|-------|--------|
| HP1 | `internal/routes/webhook_common.go:51-54` | `PORTAL_SYNC_SECRET` 未设置时 webhook HMAC 验证是空操作 | ❌ Not confirmed (already rejects with 403) |
| HP2 | `internal/routes/webhook_preconsume.go:99-101` | 已删除/暂停用户 pre-consume 返回 success:true，允许免费使用 | ⏳ TODO |
| HP3 | `internal/routes/auth.go:282` | 审计日志记录登录失败的 email，导致邮箱枚举 | ⏳ TODO |
| HP4 | `internal/routes/admin_keypool.go:800-830` | 操作密码通过 query string 参数接受，暴露在日志和 Referer header 中 | ⏳ TODO |
| HP5 | `internal/web/handler.go:164` | `currentLocale` 竞态条件 — 并发请求共享同一字段 | ✅ FIXED (b568427, 542ecd4) |
| HP6 | `internal/routes/payment.go:340-382` | 所有 webhook handler 不验证回调来源（除了 WeChat） | ⏳ TODO |
| HP7 | `internal/services/pricing.go:118-119` | `PricingService` 缓存条目永不淘汰，内存无限增长 | ⏳ TODO |
| HP8 | `pkg/crypto/encryption.go:15` | `ErrInvalidKey` 定义但从不返回——死代码 | ⏳ TODO |
| HP9 | `internal/routes/enterprise_keys.go:128-132` | 加密失败时 API key 明文存储 | ✅ FIXED (8972d85) |

### Proxy

| # | File | Issue | Status |
|---|------|-------|--------|
| HPr1 | `pkg/billing/billing.go:35-84` vs `104-170` | **两份计费逻辑不一致** — `CalculateCost` 和 `CalculateCostWithCache` 对相同输入可产生不同计费结果 | ⏳ TODO |
| HPr2 | `pkg/convert/claude.go:107-124` | 图片 URL 作为 base64 数据直接传给 Anthropic API — 总是失败 | ✅ FIXED (4fcce0a, c5a98d1) |
| HPr3 | `pkg/relay/channel/gemini/gemini.go:394` | 图片内容静默替换为 `"[Image]"` 占位符 — 多模态请求被破坏 | ✅ FIXED (4fcce0a) |
| HPr4 | `pkg/relay/channel/openai/openai.go:164-170` | `randomString` 产生确定性"随机"ID — 相同毫秒时间戳的 ID 完全一致 | ✅ FIXED (4fcce0a) |
| HPr5 | `Dockerfile` | **无 `.dockerignore`** — `.git/`, `config.yaml`, `.env.*` 进入构建上下文 | ⏳ TODO |
| HPr6 | `Dockerfile:64` | Healthcheck 使用 `wget --spider` — BusyBox 不支持该选项 | ⏳ TODO |
| HPr7 | `.golangci.yml:13-16` | 4 个关键 linter 永久关闭 — ~220 个遗留问题 | ⏳ TODO (incremental) |
| HPr8 | `internal/auth/auth.go:239-252` | `concurrentLimit` 实现不正确/不启用？ | ⏳ TODO |
| HPr9 | `internal/audit/audit.go:173-175` | `go l.Flush()` 无节流，可产生无限并发的 goroutine | ⏳ TODO |
| HPr10 | `internal/server/usage_buffer.go:107-109` | `billing` 为 nil 时 drain 的数据包静默丢失 | ⏳ TODO |

---

## 🔵 MEDIUM Findings (Selected Highlights)

### Portal

- **SQL 动态拼接**: `billing.go:393` / `orders.go:101` / `apikeys.go:79` — 参数索引用 `fmt.Sprintf` 管理
- **CSRF bypass**: `web/middleware.go:65-67` — API 路由跳过 CSRF 验证
- **CSV 注入**: `admin_usage.go:310,446,577` — Excel 公式注入风险
- **日志正则泄露**: `auth.go:468-476` — 重置令牌在日志中明文记录
- **订阅日配额重置竞态**: `webhook_billing.go:250-256` — 多个 batch 可同时触发重置
- **key_hash → user_id 无缓存**: `webhook_billing.go:46-49` — 每次处理重复查询

### Proxy

- **Pre-consume fail-open**: `billing/preconsume.go:77-127` — Portal 不可用时所有请求放行
- **计费 key_hash 查找无缓存**: `auth.go:505-506` — 每次请求查 Portal
- **Retention Cleanup 从未被调用**: `internal/retention/disk_store.go:174` — 磁盘文件无限积累
- **数据库配置全部死代码**: `internal/config/config.go:81-90` — 8 个字段，0 个使用
- **集成测试无断言**: `tests/integration/integration_test.go:110-197` — 4 个测试零断言
- **Moonshot 调试日志**: `pkg/keypool/sync.go:309-314` — 生产路径保留供应商特定日志

---

## 🧪 测试覆盖评估

| 项目 | 好的 | 中等 | 差的 |
|------|------|------|------|
| **Portal** | `pkg/jwt/` (9/10), `pkg/crypto/` (8/10) | `pkg/mail/`, `pkg/validation/` | `pkg/queue/` (5/10), `pkg/payment/` (5/10), `internal/routes/payment_test.go` (只有 JSON binding 测试) |
| **Proxy** | `pkg/keypool/health_test.go`, `pkg/circuitbreaker/` | `pkg/relay/`, 各 adapter 有测试 | `pkg/audit/` (Flush recovery), `pkg/billing/` (preconsume), `tests/integration/` (零断言), e2e 接受 3 种 HTTP code |

**关键缺口:** 两个项目都没有支付 E2E 测试、没有优雅关闭测试、没有 pre-consume 全链路测试。

---

## 🏛️ 架构问题

### 1. 支付配置存储双系统
`payment_gateways` (JSONB, REST API) vs `payment_config` (KV, 表单 POST) — 两套配置可能不同步。

### 2. Proxy 数据库配置全部死代码
`config.yaml` 仍有完整的 `database` 节（host/port/user/password），但 CLAUDE.md 明确说 unused。8 个死字段误导运营。

### 3. 后台 goroutine 关闭黑洞
Proxy 有 6+ 个后台 goroutine 从不关闭。Portal 也有 exchange service、goroutine leak 等问题。**SIGTERM 后泄漏所有后台资源。**

### 4. Portal 前端支付按钮与网关状态无关
`billing.html` 和 `checkout.html` 固定显示 4 个支付方式，不管实际配置了哪个网关。

---

## ⚡ 快速修复建议 (按影响排序)

| 优先级 | 项目 | 修改 | 预计工作量 | Status |
|--------|------|------|-----------|--------|
| P0 | Portal | 修复 `payment.go:606` cancel 分支双重写 response | 1 行 | ✅ FIXED |
| P0 | Portal | `payment.go:251-273` 禁止生产环境静默 fallback 到模拟模式 | 2 行 | ✅ FIXED |
| P0 | Proxy | `claude.go:451` 消除全局 `currentStreamState`，改为 per-connection | 半天 | ✅ FIXED |
| P0 | Proxy | `main.go` 给所有后台 goroutine 加 `stopCh` | 1 小时 | ✅ FIXED |
| P1 | Portal | 修复 Alipay webhook 签名验证 | 半天 | ⏳ TODO |
| P1 | Portal | 修复 PayPal `QueryOrder` 传入正确的 Order ID | 半天 | ⏳ TODO |
| P1 | Proxy | 修复 `poolsMu` / `providerLock` 锁顺序 | 1 小时 | ❌ Not confirmed |
| P1 | Proxy | 修复 `openai.go:164` `randomString` 使用 crypto/rand | 1 行 | ✅ FIXED |
| P2 | Portal | 删除或修复 `admin_handler_common.go:1347` 付款桩 | 1 小时 | ✅ FIXED (debug-only) |
| P2 | Portal | 修复 `admin_reconciliation.go:608` nil billingSvc | 1 行 | ✅ FIXED |
| P2 | Proxy | 修复 `Dockerfile:64` healthcheck 命令 | 1 行 | ⏳ TODO |
| P2 | Proxy | 添加 `.dockerignore` | 5 分钟 | ⏳ TODO |
