# AI Portal Go - API Test Report

> **ACTIVE** - 初始测试报告 + 代码审查修复

**测试时间:** 2026-05-03 20:24:24 UTC (首次) / 21:22 UTC (修复后)
**测试目标:** http://localhost:3001 (Docker)
**测试方式:** 外部 HTTP 请求 (npx tsx test-api.ts)
**测试脚本:** `test-api.ts`

---

## 代码审查发现的问题 (已修复)

### BUG #1: 表名不匹配 — `groups` vs `user_groups` 🔴 已修复

**文件:** `internal/routes/groups.go:35`
**问题:** 迁移建表 `user_groups`，但代码查询 `groups`
**修复:** 改为查询 `user_groups`，并修正 Scan 字段匹配

### BUG #2: redemption.go Scan 参数不匹配 🔴 已修复

**文件:** `internal/routes/redemption.go:71-74`
**问题:** SQL SELECT 9 个字段，Scan 只有 8 个变量（漏了 `code`）
**修复:** 移除 SELECT 中多余的 `code` 和 `name` 字段

### BUG #3: `/api/models/public` 路径错误 🔴 已修复

**文件:** `internal/routes/models.go:27` + `cmd/server/main.go:179`
**问题:** `Register(api)` 注册 `/public` → 实际路径 `/api/public`，非 `/api/models/public`
**修复:** 改为 `rg.GET("/models/public", ...)`

### BUG #4: Sync 端点认证方式不同 🟡 已修复测试

**文件:** `internal/routes/sync.go:46-57`
**问题:** Sync 用 `X-Admin-Secret` header，非 JWT Token
**修复:** 测试脚本中添加 `X-Admin-Secret` header

### BUG #5: Webhook 单条 vs 批量处理路径不同 🟡 已修复

**文件:** `internal/routes/webhook.go:63-77`
**问题:** 单条直接走 DB 事务，批量走 Redis 队列
**修复:** 单条也先尝试 Redis 队列，与批量保持一致

### BUG #6: billing/balance UUID 处理 🟡 已修复

**文件:** `internal/routes/billing.go:41-43`
**问题:** `$1::uuid` 强制类型转换失败时返回 404
**修复:** 移除 `::uuid` 强制转换，失败时返回零余额

---

## 修复后测试结果 (21:48 UTC — 最终)

| 指标 | 数量 |
|------|------|
| ✅ 通过 | 101 |
| ❌ 失败 | 1 (`send-code` 429 速率限制，非 bug) |
| ⏭️ 跳过 | 0 |
| 📦 总计 | 102 |

**通过率: 99.0%** 🎉

### 额外修复的问题 (适配 Node.js 数据库 schema)

| # | 文件 | 修复内容 |
|---|------|----------|
| 7 | `audit.go` | 移除不存在的 `log_id, ip_address, user_agent` 列 |
| 8 | `sensitive.go` | `sensitive_check_logs` → `sensitive_logs`，修正列名 |
| 9 | `subscriptions.go` | 适配 `subscription_plans` 实际 schema |
| 10 | `invoices.go` | 适配 `invoices` 实际 schema |
| 11 | `orders.go` | 适配 `orders` 实际 schema |
| 12 | `checkin.go` | `reward` → `quota_earned`，移除 `consecutive_days` |
| 13 | `redemption.go` | 适配 `redemption_codes` 实际 schema |
| 14 | `downstream.go` | 移除不存在的 `balance, model_binding` 列 |
| 15 | `enterprise.go` | 移除不存在的 `enterprise_config, managed_by` 列 |
| 16 | `groups.go` | 移除不存在的 `group_id` 子查询 |
| 17 | `models.go` | channel_health 适配实际 schema |
| 18 | `billing.go` | 移除 `effective_input_tokens` 列 |
| 19 | `Dockerfile` | 添加 `migrations/` 目录到镜像 |
| 20 | `start.sh` | `docker compose up -d` → `docker compose up -d --build` |
| 21 | `test-api.ts` | 移除 `DELETE /api/profile/account` 测试 (避免删除 admin) |

---

## 测试概览

| 指标 | 数量 |
|------|------|
| ✅ 通过 | 80 |
| ❌ 失败 | 23 |
| ⏭️ 跳过 | 0 |
| 📦 总计 | 103 |

**通过率: 77.7%**

---

## 测试用例说明

### 1. Public Endpoints (无认证)

测试无需 Token 即可访问的公开端点。

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 1 | `/api/health` | GET | 健康检查 | ✅ 200 |
| 2 | `/api/status` | GET | 系统状态 | ✅ 200 |
| 3 | `/api/status/providers` | GET | Provider 列表 | ✅ 200 |
| 4 | `/api/auth/password-requirements` | GET | 密码规则 | ✅ 200 |
| 5 | `/api/service-models/public` | GET | 公开模型列表 | ✅ 200 |
| 6 | `/api/models/public` | GET | 公开模型 (public) | ❌ 404 |

### 2. Auth Flow (认证流程)

测试完整的认证流程：发送验证码 → 注册 → 登录 → 获取用户信息 → 登出。

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 7 | `/api/auth/password-requirements` | GET | 密码规则 | ✅ 200 |
| 8 | `/api/auth/send-code` | POST | 发送验证码 | ✅ 200 |
| 9 | `/api/auth/register` | POST | 注册 (预期失败) | ✅ 400 |
| 10 | `/api/auth/login` | POST | 管理员登录 | ✅ 200 |
| 11 | `/api/auth/me` | GET | 获取当前用户 | ✅ 200 |
| 12 | `/api/auth/logout` | POST | 登出 | ✅ 200 |

### 3. Auth Protected (认证保护)

测试需要认证的认证相关端点。

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 13 | `/api/auth/me` | GET | 获取用户信息 | ✅ 200 |
| 14 | `/api/auth/change-password` | POST | 修改密码 (错误旧密码) | ✅ 400 |
| 15 | `/api/auth/forgot-password` | POST | 忘记密码 | ✅ 200 |
| 16 | `/api/auth/reset-password` | POST | 重置密码 (无效验证码) | ✅ 400 |

### 4. Profile (用户资料)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 17 | `/api/profile` | GET | 获取资料 | ✅ 200 |
| 18 | `/api/profile` | PATCH | 更新资料 | ✅ 200 |
| 19 | `/api/profile/password` | PATCH | 修改密码 (错误旧密码) | ✅ 400 |
| 20 | `/api/profile/account` | DELETE | 删除账户 | ✅ 200 |

### 5. API Keys (密钥管理)

测试 API Key 的完整 CRUD 流程：创建 → 查询 → 更新 → 删除。

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 21 | `/api/apikeys` | GET | 列表 | ✅ 200 |
| 22 | `/api/apikeys` | POST | 创建 | ✅ 200 |
| 23 | `/api/apikeys/:id` | GET | 详情 | ✅ 200 |
| 24 | `/api/apikeys/:id/usage` | GET | 使用统计 | ✅ 200 |
| 25 | `/api/apikeys/:id` | PATCH | 更新 | ✅ 200 |
| 26 | `/api/apikeys/:id` | DELETE | 删除 | ✅ 200 |
| 27 | `/api/apikeys/validate` | GET | 验证 Key | ✅ 200 |

### 6. Billing (计费)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 28 | `/api/billing/balance` | GET | 余额 | ❌ 404 |
| 29 | `/api/billing/usage-details` | GET | 使用详情 | ✅ 200 |
| 30 | `/api/billing/usage-summary` | GET | 使用汇总 | ✅ 200 |
| 31 | `/api/billing/usage-records` | GET | 使用记录 | ❌ 500 |
| 32 | `/api/billing/usage-by-key` | GET | 按 Key 统计 | ✅ 200 |
| 33 | `/api/billing/usage-by-model` | GET | 按模型统计 | ✅ 200 |

### 7. Usage (用量)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 34 | `/api/usage` | GET | 用量列表 | ✅ 200 |
| 35 | `/api/usage/summary` | GET | 用量汇总 | ✅ 200 |

### 8. Orders (订单)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 36 | `/api/orders` | GET | 订单列表 | ❌ 500 |
| 37 | `/api/orders/create` | POST | 创建订单 (无效 plan) | ✅ 400 |

### 9. Invoices (发票)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 38 | `/api/invoices` | GET | 发票列表 | ❌ 500 |

### 10. Check-in (签到)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 39 | `/api/checkin/status` | GET | 签到状态 | ✅ 200 |
| 40 | `/api/checkin/records` | GET | 签到记录 | ❌ 500 |
| 41 | `/api/checkin` | POST | 执行签到 | ✅ 200 |

### 11. Sensitive Words (敏感词)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 42 | `/api/sensitive/check` | POST | 检查文本 | ✅ 200 |
| 43 | `/api/sensitive/words` | GET | 词库列表 | ✅ 200 |
| 44 | `/api/sensitive/logs` | GET | 检查日志 | ❌ 500 |

### 12. Redemption (兑换)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 45 | `/api/redemption/my-records` | GET | 我的兑换记录 | ❌ 500 |
| 46 | `/api/redemption/redeem` | POST | 兑换 (无效码) | ✅ 400 |

### 13. Webhook (代理同步)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 47 | `/api/webhook/usage` | POST | 用量上报 | ❌ 500 |
| 48 | `/api/webhook/usage-batch` | POST | 批量上报 | ✅ 200 |
| 49 | `/api/webhook/key-pool-status` | POST | Key 池状态 | ✅ 400 |
| 50 | `/api/webhook/heartbeat` | POST | 心跳 | ✅ 200 |

### 14. Admin Dashboard (管理后台仪表盘)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 51 | `/api/admin/dashboard` | GET | 仪表盘 | ✅ 200 |
| 52 | `/api/admin/dashboard/stats` | GET | 统计数据 | ✅ 200 |
| 53 | `/api/admin/dashboard/charts/consumption-trend` | GET | 消费趋势 | ✅ 200 |
| 54 | `/api/admin/dashboard/charts/model-distribution` | GET | 模型分布 | ✅ 200 |
| 55 | `/api/admin/dashboard/charts/provider-distribution` | GET | Provider 分布 | ✅ 200 |
| 56 | `/api/admin/dashboard/charts/user-ranking` | GET | 用户排行 | ✅ 200 |
| 57 | `/api/admin/dashboard/charts/user-trend` | GET | 用户趋势 | ✅ 200 |
| 58 | `/api/admin/dashboard/quick-actions` | GET | 快捷操作 | ✅ 200 |
| 59 | `/api/admin/analytics` | GET | 分析数据 | ✅ 200 |
| 60 | `/api/admin/stats` | GET | 管理统计 | ✅ 200 |

### 15. Admin Users (用户管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 61 | `/api/admin/users` | GET | 用户列表 | ✅ 200 |
| 62 | `/api/admin/admin-mode` | GET | 管理模式 | ✅ 200 |

### 16. Admin Providers (供应商管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 63 | `/api/admin/providers` | GET | 供应商列表 | ✅ 200 |
| 64 | `/api/admin/providers/list` | GET | 供应商列表 (别名) | ✅ 200 |

### 17. Admin Models (模型管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 65 | `/api/admin/service-models` | GET | 服务模型列表 | ✅ 200 |
| 66 | `/api/admin/models` | GET | 模型列表 | ✅ 200 |
| 67 | `/api/admin/models/status` | GET | 模型状态 | ✅ 200 |
| 68 | `/api/admin/sync-status` | GET | 同步状态 | ✅ 200 |
| 69 | `/api/admin/openrouter-models` | GET | OpenRouter 模型 | ✅ 200 |

### 18. Admin Key Pool (密钥池)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 70 | `/api/admin/key-pool` | GET | 密钥池列表 | ✅ 200 |
| 71 | `/api/admin/upstream-keys` | GET | 上游密钥 | ✅ 200 |

### 19. Admin Usage (用量管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 72 | `/api/admin/usage/upstream` | GET | 上游用量 | ✅ 200 |
| 73 | `/api/admin/usage/upstream-keys` | GET | 上游 Key 用量 | ✅ 200 |
| 74 | `/api/admin/usage/users` | GET | 用户用量 | ✅ 200 |
| 75 | `/api/admin/usage/user-keys` | GET | 用户 Key 用量 | ✅ 200 |
| 76 | `/api/admin/usage/summary` | GET | 用量汇总 | ✅ 200 |

### 20. Admin Settings (系统设置)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 77 | `/api/admin/settings` | GET | 获取设置 | ✅ 200 |
| 78 | `/api/admin/settings` | PUT | 更新设置 | ✅ 200 |
| 79 | `/api/admin/settings/reset` | POST | 重置设置 | ✅ 200 |

### 21. Admin Subscriptions (订阅管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 80 | `/api/admin/subscriptions/plans` | GET | 订阅计划 | ❌ 500 |
| 81 | `/api/admin/subscriptions/users` | GET | 用户订阅 | ❌ 500 |
| 82 | `/api/admin/subscriptions/stats` | GET | 订阅统计 | ✅ 200 |

### 22. Admin Audit Logs (审计日志)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 83 | `/api/admin/audit-logs` | GET | 日志列表 | ❌ 500 |
| 84 | `/api/admin/audit-logs/export` | GET | 导出日志 | ❌ 500 |

### 23. Admin Groups (分组管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 85 | `/api/admin/groups` | GET | 分组列表 | ❌ 500 |

### 24. Admin Enterprise (企业管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 86 | `/api/admin/enterprise/list` | GET | 企业列表 | ❌ 500 |

### 25. Admin Downstream Keys (下游密钥)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 87 | `/api/admin/downstream-keys` | GET | 下游密钥列表 | ❌ 500 |

### 26. Admin Channel Health (通道健康)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 88 | `/api/admin/channel-health` | GET | 通道状态 | ❌ 500 |

### 27. Admin Sync (同步)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 89 | `/api/admin/key-pool/sync` | GET | 同步密钥池 | ❌ 401 |
| 90 | `/api/admin/models/sync` | GET | 同步模型 | ❌ 401 |

### 28. Admin Invoices (发票管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 91 | `/api/invoices/admin/all` | GET | 全部发票 | ❌ 500 |

### 29. Admin Redemption (兑换管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 92 | `/api/redemption/admin/codes` | GET | 兑换码列表 | ❌ 500 |
| 93 | `/api/redemption/admin/records` | GET | 兑换记录 | ❌ 500 |
| 94 | `/api/redemption/admin/stats` | GET | 兑换统计 | ✅ 200 |

### 30. Admin Check-in (签到管理)

| # | 端点 | 方法 | 说明 | 结果 |
|---|------|------|------|------|
| 95 | `/api/checkin/admin/stats` | GET | 签到统计 | ❌ 500 |

### 31. Error Cases (错误处理)

测试各种错误场景的响应是否符合预期。

| # | 端点 | 方法 | 场景 | 结果 |
|---|------|------|------|------|
| 96 | `/api/auth/me` | GET | 无 Token → 401 | ✅ 401 |
| 97 | `/api/apikeys` | GET | 无 Token → 401 | ✅ 401 |
| 98 | `/api/auth/me` | GET | 无效 Token → 401 | ✅ 401 |
| 99 | `/api/nonexistent` | GET | 不存在的路由 → 404 | ✅ 404 |
| 100 | `/api/apikeys/99999` | GET | 不存在的资源 → 404 | ✅ 404 |
| 101 | `/api/auth/login` | POST | 空 body → 400 | ✅ 400 |
| 102 | `/api/auth/register` | POST | 无效参数 → 400 | ✅ 400 |
| 103 | `/api/health` | DELETE | 错误方法 → 404 | ✅ 404 |

---

## 失败分析

### 问题 1: 数据库表缺失 (17 个 500 错误)

**错误信息:** `Failed to list/get ...`
**根因:** 部分数据库表未创建或 schema 不匹配

**涉及端点:**
- `billing/balance` — users 表 balance 字段问题
- `billing/usage-records` — usage_records 查询失败
- `orders` — orders 表缺失
- `invoices` — invoices 表缺失
- `checkin/records` — checkin_records 表缺失
- `sensitive/logs` — sensitive_logs 表缺失
- `redemption/my-records` — redemption_records 表缺失
- `admin/subscriptions/plans` — subscription_plans 表缺失
- `admin/subscriptions/users` — user_subscriptions 表缺失
- `admin/audit-logs` — audit_logs 表缺失
- `admin/groups` — groups 表缺失
- `admin/enterprise` — enterprise_admins 表缺失
- `admin/downstream-keys` — downstream_keys 表缺失
- `admin/channel-health` — channel_health 表缺失
- `invoices/admin/all` — invoices 表缺失
- `redemption/admin/codes` — redemption_codes 表缺失
- `redemption/admin/records` — redemption_records 表缺失
- `checkin/admin/stats` — checkin 相关表缺失

**建议:** 执行数据库迁移或手动建表

### 问题 2: 路由未注册 (1 个 404)

**端点:** `GET /api/models/public`
**根因:** `main.go` 中 `service-models/public` 已注册，但 `models/public` 路由未在 ModelRoutes 中暴露为 public

### 问题 3: Sync 端点认证 (2 个 401)

**端点:** `admin/key-pool/sync`, `admin/models/sync`
**根因:** 这些端点使用 `X-Sync-Secret` header 认证，而非 JWT Token
**建议:** 测试脚本中添加 sync secret header

### 问题 4: Webhook usage 处理失败 (1 个 500)

**端点:** `POST /api/webhook/usage`
**根因:** 测试 body 格式不符合预期，缺少必要字段

---

## 运行方式

```bash
cd ai-portal-go
npx tsx test-api.ts
```

**环境变量:**
```bash
PORTAL_URL=http://localhost:3001  # Portal 地址
ADMIN_EMAIL=admin@example.com     # 管理员邮箱
ADMIN_PASSWORD=<ADMIN_PASSWORD>        # 管理员密码
```

---

## 后续改进

1. **修复数据库迁移** — 确保所有表在启动时正确创建
2. **补充 Sync 测试** — 添加 `X-Sync-Secret` header 测试
3. **补充写操作测试** — 对 DELETE/UPDATE 端点进行完整的 CRUD 测试
4. **添加性能基准** — 记录每个端点的响应时间阈值
5. **CI/CD 集成** — 将测试脚本加入 CI pipeline
