> **📋 HISTORICAL** - Handover report for reference. Covers implementation status, test results, and known gaps as of 2026-05-05.

# AI Proxy + Portal Go 重写项目 — 交接报告

**日期**: 2026-05-05 14:25  
**项目**: ai-proxy-go + ai-portal-go（Node.js → Go 重写）

---

## 一、项目总览

| | ai-proxy-go | ai-portal-go |
|---|---|---|
| **用途** | AI API 网关/代理 | 管理后台 & 用户门户 |
| **技术栈** | Go 1.23 + Gin + Redis | Go 1.21 + Gin + pgx/v5 + Redis |
| **代码量** | ~11,400 行 Go / ~2,300 行测试 | ~12,200 行 Go / ~4,200 行测试 |
| **状态** | 核心功能完成，5 个 bug 刚修复 | ~75% 功能覆盖，大量 admin 功能待补 |
| **部署** | Docker + `start.sh` | Docker + `start.sh` |
| **运行端口** | 8081 (nginx → 8080) | 3002 (nginx → 80) |

**两个项目的关系**：Portal 负责持久化（PostgreSQL）、用户管理、计费、key pool 配置；Proxy 只通过 Redis 和 HTTP Webhook 与 Portal 通信，不直接连数据库。

---

## 二、ai-proxy-go 现状

### 2.1 已实现的核心功能

| 模块 | 状态 | 说明 |
|---|---|---|
| **请求路由** | ✅ | 模型名 → 16 家 Provider 映射 |
| **格式转换** | ✅ | OpenAI ↔ Claude ↔ Gemini 适配器 |
| **Key Pool** | ✅ | 3 级优先级(high/normal/economy)、冷却/失效/恢复 |
| **流式响应** | ✅ | SSE chunk 转换 |
| **Token 估算** | ✅ | Tiktoken (OpenAI) + 字符比例 (Claude/Gemini) |
| **计费计算** | ✅ | Cache hit/miss 折扣、Reasoning tokens、图片生成 |
| **亲和缓存** | ✅ | Local sync.Map + Redis 双层 |
| **限流** | ✅ | Token Bucket O(1) + Redis 分布式后备 |
| **认证** | ✅ | Bearer Key → Portal API 验证，5min 内存缓存 |
| **审计上报** | ✅ | 批量 Webhook → Portal `/api/webhook/usage-batch` |
| **Metrics** | ✅ | Prometheus `/metrics` 端点（自定义 text format） |
| **模拟模式** | ✅ | `simulate.enabled: true` 时返回假响应（当前开启） |

### 2.2 最新提交（近 5 个）

```
cfbbcb2 feat(metrics): Prometheus metrics 端点
da1c419 fix: 5 个关键 bug 修复（见 docs/reports/bug-fixes-202605051000.md）
4bb815b fix(auth): portalHealthy 改用 atomic.Bool
9b1aea4 fix: AuditEntry 补全 APIKeyHash
aa6dcc7 fix: 多个关键 bug 修复
```

**B3-B12 Bug 修复要点**：
- B3: Key sync 时保留 Portal 的 `Enabled` 状态（之前被强制改 true）
- B4: 新增 `billing.StartPricingSyncLoop()` 从 Portal 同步定价
- B5: 亲和缓存修复（在解析 body 前设置 `request_body`）
- B9: `getTokenEncoder` 竞态条件修复（`LoadOrStore`）
- B12: `handleSyncKeys` 增加认证校验

### 2.3 已知问题

1. **4 个空适配器**：`ali/deepseek/moonshot/zhipu` 是 OpenAI-compatible stub，无自定义逻辑
2. **metrics.go `formatInt` 有 bug**：不能正确处理所有数字
3. **Admin 接口部分为 stub**：key history、audit search 是占位符
4. **Simulate 模式当前开启**：`config.yaml` 中 `simulate.enabled: true`，不会调用真实上游 API

---

## 三、ai-portal-go 现状

### 3.1 已实现的核心功能

| 模块 | 状态 | 说明 |
|---|---|---|
| **Auth** | ✅ | JWT (header+cookie)、注册、登录、密码重置、SMTP 发码 |
| **角色系统** | ✅ | super_admin / system_admin / enterprise_admin / user 四级 |
| **API Keys** | ✅ | CRUD、SHA-256 哈希存储、前缀预览、验证端点 |
| **Billing** | ✅ | 余额（分）、用量记录（按月分区）、按 key/按 model 统计 |
| **Orders & Invoices** | ⚠️ | 路由存在，部分表缺失，可能 500 |
| **Subscriptions** | ✅ | Plan CRUD、用户订阅、配额/日限、自动续费字段 |
| **Redemption** | ✅ | 批量生成（≤100）、兑换、禁用/启用、统计 |
| **Checkin** | ✅ | 连续签到逻辑、奖励计算 |
| **Upstream Key Pool** | ⚠️ | AES-256-GCM 加密、导入/导出、toggle、重复防呆 |
| **Providers** | ✅ | 18 家供应商初始化、CRUD、enable/disable |
| **Service Models** | ⚠️ | OpenRouter 同步、四项定价、EOL 流程、Pause/Resume |
| **Channel Health** | ✅ | HTTP 连通性测试、延迟跟踪、错误率计算 |
| **Audit Logs** | ✅ | 结构化日志、分类/分级、CSV 导出 |
| **System Settings** | ✅ | KV 设置、分类、默认种子 |
| **Groups** | ✅ | 用户组、定价倍率、模型权限 |
| **Sensitive Words** | ✅ | 内容检查、词表管理 |
| **Webhook** | ✅ | Redis 队列批量处理 Proxy 上报 |
| **Proxy Sync** | ✅ | `/api/admin/key-pool/sync`、`/api/admin/models/sync` |

### 3.2 最新提交（近 5 个）

```
9d4cc81 feat: improve admin models page UX
51d44b8 fix: replace all browser native alert/confirm with custom modals
a660dec feat: add EOL management with editable fields
8b746de feat: add complete model status management
9c99346 fix: implement getOpenRouterModels to return actual data from database
```

### 3.3 前端技术栈

**不是 React SPA。** Portal 使用 **Go server-side rendering**：
- `html/template` + `go:embed`
- HTMX 2.x（AJAX 交互）
- Alpine.js 3.x（轻量响应式）
- Tailwind CSS（purge 后嵌入）
- i18n：zh/en JSON 翻译文件

模板页：landing、login、dashboard、apikeys、billing、orders、invoices、profile、admin(10+子页)、enterprise、super-admin、checkout

### 3.4 数据库迁移（11 个）

```
001_core_tables.sql          → users, api_keys, upstream_keys, providers, service_models, audit_logs
002_usage_and_records.sql    → usage_records(按月分区), channel_health, channel_health_history
003_billing.sql              → orders, invoices, model_products, pricing_sources, model_pricing
004_subscriptions.sql        → subscription_plans, user_subscriptions（默认 4 个套餐）
005_redemption.sql           → redemption_codes, redemption_records, checkin_records
006_enterprise_groups.sql    → user_groups, service_model_approvals, sensitive_check_logs
007_system_settings.sql      → system_settings(20+默认), user_devices
008_v3_schema.sql            → 大规模 V3 扩展（2FA、订单扩展、企业成员、归档等）
009_seed_providers.sql       → 18 家供应商种子数据
20260505_add_pricing_fields.sql      → 四项定价 + EOL 字段
202605051330_add_keypool_fields.sql  → upstream_keys 唯一索引 + RPM/TPM/Quota/Validation
```

---

## 四、功能差距（Go vs Node.js）

**整体覆盖率：~75%**

### 🔴 P0 — 生产必备

| 差距 | 影响 | 位置 |
|---|---|---|
| **GDPR 端点缺失** | `/api/gdpr/export/delete/status` 完全不存在 | 法律合规 |
| **Device 管理缺失** | `/api/devices` 完全不存在 | 安全/风控 |
| **Pricing 管理缺失** | `/api/admin/pricing/*` 5 个端点全缺 | 成本跟踪 |
| **Enterprise 管理 stub** | 创建/更新/启用/禁用/成员管理全是 no-op | 企业功能 |
| **Dashboard 图表缺失** | consumption-trend, model-distribution, user-ranking 等 6 个端点缺数据 | 数据可视化 |
| **Webhook 余额扣减无事务** | 无 `FOR UPDATE` + `tx.Commit` | 并发安全 |
| **Sync 返回加密 Key** | Proxy sync 端点返回加密后的 key 给 Proxy（Proxy 无法使用） | 同步逻辑 |

### 🟡 P1 — 重要功能

| 差距 | 说明 |
|---|---|
| **Usage 导出** | CSV 导出缺失 |
| **Usage Detail** | `/api/billing/usage-records/:trace_id/detail` 缺失 |
| **Channel Health History** | 历史表存在但无写入/查询端点 |
| **Profile 增强** | 改邮箱/改手机/备用邮箱流程缺失 |
| **4 个空 Proxy 适配器** | Ali/DeepSeek/Moonshot/Zhipu 需补自定义逻辑 |
| **CamelCase 统一** | 部分 API 返回 snake_case，前端期望 camelCase |

### 🟢 P2 — 体验优化

| 差距 | 说明 |
|---|---|
| **Frontend V3 实施** | 文档完备（web-frontend-architecture-v3-202605040636.md），代码为 0 |
| **Payment 网关** | Stripe/PayPal/Alipay/WeChat 全是 no-op stub |
| **Admin Settings Web 处理器** | `admin_handlers.go` 大量 no-op（创建/更新/删除 setting） |
| **Super Admin 升降级** | no-op stub |

---

## 五、测试状态

### ai-proxy-go
- **单元测试**: `go test ./pkg/billing/... ./pkg/token/... ./pkg/keypool/... ./internal/auth/...` ✅ 通过
- **集成测试**: `tests/integration/integration_test.go` — Mock upstream HTTP 服务器，验证路由、计费、Token 估算
- **性能测试**: 76K RPM (vs Node.js 64K)，内存 -58%，P99 520ms

### ai-portal-go
- **单元测试**: 覆盖 auth、apikeys、billing、subscriptions、redemption、checkin、orders、invoices、groups、sensitive、webhook
- **集成测试**: `tests/*.sh` 脚本化测试
- **API 测试报告**: `docs/reports/api-test-report-202605032024.md` — 102 端点，99% 通过率 (101/102)

---

## 六、如何启动

### 6.1 快速启动（推荐）

```bash
# Portal + DB + Redis
cd ai-portal-go
./start.sh portal

# Proxy（连接远程 Portal）
cd ai-proxy-go
./start.sh proxy

# 全栈 MVP 模式
cd ai-portal-go
./start.sh mvp
```

### 6.2 环境变量（Portal）

```bash
PORT=3002
DB_HOST=localhost DB_PORT=5432 DB_NAME=ai_proxy_portal DB_USER=postgres DB_PASSWORD=...
REDIS_HOST=localhost REDIS_PORT=6379 REDIS_PASSWORD=...
JWT_SECRET="min-32-char-secret-here"
JWT_EXPIRES=24h
ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD=<ADMIN_PASSWORD> ADMIN_NAME=Admin
ENCRYPTION_KEY="32-byte-aes-key-here"
PROXY_SYNC_SECRET="sync-secret-between-portal-and-proxy"
SMTP_HOST=smtp.gmail.com SMTP_PORT=465 SMTP_USER=... SMTP_PASSWORD=... SMTP_FROM=...
```

### 6.3 当前部署状态（服务器）

| 服务 | 端口 | 状态 |
|---|---|---|
| Portal Go | :3002 | ✅ 运行中 |
| Proxy Go | :8081 | 需确认 |
| PostgreSQL | :5432 (Docker) | ✅ 运行中 |
| Redis | :6379 (Docker) | ✅ 运行中 |
| nginx | :80 → Portal, :8080 → Proxy | ✅ 运行中 |

---

## 七、关键文件速查

### ai-proxy-go
| 文件 | 用途 |
|---|---|
| `cmd/server/main.go` | 入口：初始化所有子系统 |
| `internal/server/handlers.go` | 核心代理 handler（chat/embeddings/images/audio） |
| `pkg/keypool/manager.go` | Key Pool 管理器 |
| `pkg/billing/billing.go` | 计费计算 |
| `pkg/relay/providers.go` | 16 家 Provider 配置 |
| `config.yaml` | 运行时配置 |
| `docs/reports/bug-fixes-202605051000.md` | 最新 Bug 修复报告 |

### ai-portal-go
| 文件 | 用途 |
|---|---|
| `cmd/server/main.go` | 入口：注册所有路由、运行迁移 |
| `internal/routes/` | 24 个路由文件 |
| `internal/middleware/auth.go` | JWT + 角色中间件 |
| `internal/db/migrate.go` | SQL 迁移运行器 |
| `pkg/crypto/crypto.go` | AES-256-GCM 加密 |
| `web/` | 服务端渲染模板 + HTMX |
| `docs/reports/nodejs-vs-go-portal-gap-analysis-202605051030.md` | 功能差距分析 |
| `docs/web-frontend-architecture-v3-202605040636.md` | 前端 V3 最终方案 |

---

## 八、下一步建议（按优先级）

### 立即（本周）
1. **修复 Sync 加密 Key Bug** — Proxy sync 端点返回明文 key（`pkg/crypto` 解密后再返回）
2. **补齐缺失的数据库表/字段** — 20 张表/字段缺失，导致多个 API 500
3. **Webhook 余额扣减加事务** — `FOR UPDATE` + `tx.Commit`
4. **修复 admin.go `Use()` 返回值错误**

### 短期（2 周）
5. **实现 GDPR 端点** — export/delete/status
6. **实现 Device 管理** — 表已有，补端点
7. **补齐 Dashboard 图表数据** — 6 个 chart 端点
8. **统一 JSON 字段命名** — 全部改为 camelCase

### 中期（1 个月）
9. **启动 Frontend V3 开发** — 文档已完备，可开工
10. **Enterprise 管理完整实现** — 创建/更新/成员/充值
11. **Payment 网关接入** — Stripe/Alipay/WeChat
12. **Proxy P99 优化** — 连接池/GC 调优

---

*报告由 AI Agent 自动生成，基于代码审计、文档阅读和现场测试。*
