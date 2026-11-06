# AI Portal 业务测试计划

> 生成时间：2026-06-15
> 范围：ai-portal-go + ai-proxy-go 端到端业务链路
> 目标：覆盖计费、限流、企业/订阅、上行 key 路由、可靠性、管理后台等核心业务流程

---

## 一、概述

本计划列出当前系统最值得验证的业务测试场景。测试以**端到端**为主，通过真实请求或 Playwright 操作 UI，验证数据最终一致性。

### 测试环境

- Portal: `http://127.0.0.1:3001`
- Proxy: `http://127.0.0.1:8080`
- 默认管理员: `e2e-admin@example.com / <ADMIN_PASSWORD>`
- 测试企业管理员: `e2e-enterprise@example.com / <TEST_PASSWORD>`
- 测试普通用户: `e2e-user@example.com / <TEST_PASSWORD>`

### 测试数据准备

在干净数据库（`./start.sh clean-all && ./start.sh mvp`）后，执行以下初始化：

```sql
-- 创建测试企业
WITH ent AS (
  INSERT INTO enterprises (id, name, contact_email, status, credit_total, max_admins)
  VALUES (gen_random_uuid(), 'Test Enterprise', 'e2e-enterprise@example.com', 'active', 10000000, 3)
  RETURNING id
)
INSERT INTO users (id, name, email, password_hash, role, status, enterprise_id, balance, gift_balance)
SELECT gen_random_uuid(), 'Enterprise Admin', 'e2e-enterprise@example.com',
       '<PLACEHOLDER_PASSWORD_HASH>',
       'enterprise_admin', 'active', ent.id, 10000000, 0
FROM ent;

-- 创建测试普通用户
INSERT INTO users (id, name, email, password_hash, role, status, balance, gift_balance)
VALUES (gen_random_uuid(), 'Test User', 'e2e-user@example.com',
        '<PLACEHOLDER_PASSWORD_HASH>',
        'user', 'active', 10000000, 0);
```

---

## 二、计费扣减链路测试（P0）

### 2.1 普通用户余额扣减

**前置条件**

- 以 `e2e-user@example.com` 登录
- 账户余额 `balance = 10,000,000`（单位：micro-USD）
- 已创建 general API key

**操作步骤**

1. 记录测试前 `users.balance`
2. 调用 Proxy 发送一次 chat completion 请求（使用真实或模拟模型）
3. 等待 5-10 秒（异步 billing 队列处理）
4. 查询 `users.balance` 和 `usage_records`

**预期结果**

- `usage_records` 新增 1 条记录
- `users.balance` 减少量 = `usage_records.total_cost`
- `users.balance` 不会变成负数

**验证 SQL**

```sql
SELECT total_cost, balance_deduction FROM usage_records ORDER BY created_at DESC LIMIT 1;
SELECT balance FROM users WHERE email = 'e2e-user@example.com';
```

---

### 2.2 Subscription 额度优先扣减

**前置条件**

- 普通用户已购买 token subscription（`plan_type = 'token'`）
- `user_subscriptions.quota_remaining > 0`

**操作步骤**

1. 记录 `quota_remaining` 和 `balance`
2. 调用 Proxy 发送请求，消耗 tokens
3. 等待异步处理完成

**预期结果**

- 优先从 `quota_remaining` 扣除（`subscription_deduction`）
- 如果 quota 不足，剩余部分从 `balance` 扣除
- `user_subscriptions.quota_used` 和 `quota_remaining` 更新正确

---

### 2.3 Gift 余额优先扣减

**前置条件**

- 普通用户 `gift_balance > 0` 且 `balance > 0`

**操作步骤**

1. 设置 `gift_balance = 1,000,000`
2. 调用 Proxy 发送消耗大于 gift_balance 的请求
3. 等待处理完成

**预期结果**

- 先扣完 `gift_balance`，再扣 `balance`
- `balance_transactions` 表有两条记录（gift + balance）

---

### 2.4 Enterprise 110% 信用额度

**前置条件**

- 企业 `credit_total = 1,000,000`
- 企业管理员已创建 enterprise key

**操作步骤**

1. 累计消耗信用额度至 100%（1,000,000）
2. 继续发送请求，累计至 110%（1,100,000）
3. 再发送一次请求

**预期结果**

- 步骤 1-2：请求成功，`enterprises.credit_used` 增加
- 步骤 3：请求应被拒绝或标记为 overage，不再增加 `credit_used`

---

### 2.5 余额不足时的行为

**前置条件**

- 普通用户 `balance = 0`，无 subscription

**操作步骤**

1. 调用 Proxy 发送请求
2. 检查 `usage_records` 和 `users.balance`

**预期结果**

- `usage_records` 仍有记录（用于审计）
- `users.balance` 保持为 0
- 返回给用户的响应包含余额不足提示

---

## 三、限流 Enforcement 测试（P0，本次改动核心）

### 3.1 RPM 限制触发

**前置条件**

- 普通用户 subscription 的 `rpm_limit = 10`

**操作步骤**

1. 在 1 分钟内发送 15 次请求
2. 观察第 11-15 次响应

**预期结果**

- 前 10 次请求成功（HTTP 200）
- 第 11 次起返回 429 Too Many Requests
- 响应包含 `Retry-After` header

---

### 3.2 TPM 限制触发

**前置条件**

- 普通用户 subscription 的 `tpm_limit = 1,000`

**操作步骤**

1. 发送单次请求，input + output tokens > 1,000

**预期结果**

- 请求被阻止，返回 429
- `tpm_used` 在 Portal 中正确记录

---

### 3.3 TPW 周限制触发（本次新增）

**前置条件**

- 普通用户 subscription 的 `tpw_limit = 10,000`
- 当前时间不是周日（避免 reset_at 刚好到期）

**操作步骤**

1. 连续发送请求，累计 tokens 超过 10,000
2. 再发送一次请求

**预期结果**

- 累计未超限时请求成功
- 超过 TPW 后返回 429
- `Retry-After` 指向下周日零点
- 到下周日后计数器重置，请求恢复

---

### 3.4 RP5H / RPW 限制触发

**前置条件**

- 设置 `rp5h_limit = 5` 或 `rpw_limit = 20`

**操作步骤**

1. 在 5 小时内发送 6 次请求（测试 RP5H）
2. 或在一周内发送 21 次请求（测试 RPW）

**预期结果**

- 窗口内超限返回 429
- 窗口到期后自动恢复

---

### 3.5 用户级限流覆盖生效

**前置条件**

- 已存在 subscription plan，默认 `tpm_limit = 30,000`
- 管理员登录

**操作步骤**

1. 进入 `/admin/subscriptions`，找到用户订阅
2. 将 `tpm_limit` 修改为 100
3. 保存后立即用该用户的 key 调用 Proxy

**预期结果**

- 修改后的 `tpm_limit = 100` 立即生效
- 单次超过 100 tokens 的请求被阻止

---

## 四、企业与订阅生命周期测试（P1）

### 4.1 企业创建与成员加入

**操作步骤**

1. 以 super admin 登录
2. 进入 `/admin/enterprise`，创建企业
3. 邀请 `e2e-user@example.com` 加入企业
4. 该用户接受邀请

**预期结果**

- `enterprises` 表新增记录
- 用户 `role` 变为 `enterprise_admin` 或 `enterprise_member`
- 用户 `enterprise_id` 指向新企业

---

### 4.2 企业额度消耗

**前置条件**

- 企业 `credit_total = 1,000,000`
- 企业管理员已创建 enterprise key

**操作步骤**

1. 使用 enterprise key 调用 Proxy
2. 等待异步处理

**预期结果**

- `enterprises.credit_used` 增加
- `users.quota_used`（企业成员）同步增加

---

### 4.3 订阅过期

**操作步骤**

1. 给用户购买 subscription
2. 在数据库中将 `expires_at` 修改为过去时间
3. 用该用户的 key 调用 Proxy

**预期结果**

- API key 验证失败或请求被阻止
- 提示 subscription 已过期

---

### 4.4 订阅自动续费

**操作步骤**

1. 购买 subscription，开启 `auto_renew = true`
2. 将 `expires_at` 设置为即将到期
3. 触发续费流程（定时任务或手动调用）

**预期结果**

- 生成新的 `user_subscriptions` 记录
- 新的 `quota_remaining` 重置为 plan 额度

---

## 五、上行 Key 路由与过热保护测试（P1）

### 5.1 上行 key RPM 限制

**前置条件**

- Admin 已添加两个上行 key（Key A, Key B）
- Key A 的 `rpm_limit = 1`，Key B 的 `rpm_limit = 1000`

**操作步骤**

1. 快速发送 3 次请求到同一模型

**预期结果**

- 第 1 次请求可能走 Key A
- 第 2-3 次请求因 Key A RPM 已满，路由到 Key B
- `upstream_key_usage` 表两条 key 都有记录

---

### 5.2 Provider 熔断

**前置条件**

- 配置一个模拟总是失败的 provider
- 或手动修改 `provider_health_stats` 使其 error_rate 很高

**操作步骤**

1. 连续发送 5 次失败请求到该 provider
2. 再发送 1 次请求

**预期结果**

- Circuit breaker 打开
- 后续请求不再路由到该 provider，可能 fallback 到其他 provider

---

### 5.3 Key Cooldown

**前置条件**

- 一个上行 key 已配置
- 模拟该 key 返回 429 rate limit

**操作步骤**

1. 发送请求触发 429
2. 立即再发送请求

**预期结果**

- 该 key 进入 cooldown（约 60 秒）
- 后续请求不选择该 key

---

### 5.4 模型路由规则

**前置条件**

- Admin 配置 `model_routing`，指定某模型优先走 provider A

**操作步骤**

1. 用该模型调用 Proxy
2. 查看 `usage_records.provider`

**预期结果**

- 请求按路由规则选择 provider A

---

## 六、可靠性与对账测试（P1）

### 6.1 Proxy Fallback NDJSON

**前置条件**

- Portal 和 Proxy 正常运行
- 已产生一些 usage

**操作步骤**

1. 停止 Portal 容器
2. 继续通过 Proxy 发送请求
3. 检查 Proxy 容器内 `/var/log/ai-proxy/`

**预期结果**

- 生成 `usage-fallback-*.ndjson` 文件
- 文件包含未成功发送的 usage 记录

---

### 6.2 手动对账补扣

**前置条件**

- 已生成 fallback NDJSON 文件
- Portal 已重新启动

**操作步骤**

1. 以 admin 登录
2. 进入对账页面，上传 NDJSON 文件
3. 触发 reconcile

**预期结果**

- 缺失记录被补扣
- 生成对账报告，显示补扣条数和金额
- `usage_records` 中新增对应记录

---

### 6.3 Redis 不可用 Fallback

**操作步骤**

1. 停止 Redis 容器
2. 调用 Proxy 发送请求

**预期结果**

- Portal 无法使用 Redis 队列
- 走 `processRecord` 同步路径
- 仍能扣费和记录 usage

---

### 6.4 同步/异步路径一致性

**操作步骤**

1. 在 Redis 可用时发送一批请求
2. 在 Redis 不可用时发送相同参数的请求
3. 比较两种路径最终 DB 状态

**预期结果**

- `users.balance`、`usage_records`、`api_keys.total_requests` 等最终一致

---

## 七、管理后台字段一致性测试（P2）

### 7.1 Subscription 限流字段回显

**操作步骤**

1. 以 admin 登录 `/admin/subscriptions`
2. 切换到 User Subscriptions 标签
3. 编辑某个用户订阅，修改 RPM/TPM/TPW 等字段
4. 保存后重新打开编辑抽屉

**预期结果**

- 页面回显值与输入一致
- 数据库 `user_subscriptions` 对应字段更新

---

### 7.2 Rate-limit Dashboard 数据准确性

**操作步骤**

1. 用某个用户的 key 调用 Proxy 产生 usage
2. 以 admin 打开 `/admin/rate-limits`

**预期结果**

- 该用户显示在列表中
- 1h Requests / 1h Tokens 与 usage 一致
- 限流进度条显示正确

---

### 7.3 Enterprise 额度修改生效

**操作步骤**

1. 以 admin 进入 `/admin/enterprise`
2. 编辑企业 `credit_total`
3. 用企业 key 调用 Proxy

**预期结果**

- 新 `credit_total` 立即生效
- 企业成员可用额度同步更新

---

## 八、执行顺序建议

| 阶段 | 测试类别 | 优先级 | 预计耗时 |
|------|---------|--------|---------|
| 1 | 计费扣减链路（A） | P0 | 2-3 天 |
| 2 | 限流 enforcement（B） | P0 | 2-3 天 |
| 3 | 企业与订阅生命周期（C） | P1 | 1-2 天 |
| 4 | 上行 key 路由与过热保护（D） | P1 | 2 天 |
| 5 | 可靠性与对账（E） | P1 | 2 天 |
| 6 | 管理后台字段一致性（F） | P2 | 1 天 |

---

## 九、自动化建议

### 推荐工具

- **Playwright**：适合 UI 流程、字段回显、页面访问权限
- **Go 单元/集成测试**：适合计费扣减、队列处理、DB 状态断言
- **Shell + curl**：适合快速验证 Proxy 限流、健康检查

### 可优先自动化的场景

1. `ui-all-pages.spec.js` 已覆盖所有页面 200 + 无 console 错误
2. 新增 `ui-rate-limits.spec.js`：验证 dashboard 数据加载、编辑限流字段
3. 新增 Go test `TestBillingDeductionFlow`：模拟 usage 记录，断言 balance 变化
4. 新增 Go test `TestRateLimitEnforcement`：调用 validate API 断言 429

---

## 十、相关文档

- `docs/rate-limit-design-202605072130.md` — 限流架构设计
- `docs/billing-data-reliability-analysis-202605291817.md` — 计费可靠性分析
- `docs/admin-reconciliation-design-202606090317.md` — 对账设计

---

## 附录 A：AccessAPI 上行 Provider 测试执行记录

> 执行时间：2026-06-15  
> 执行人：自动化脚本  
> 报告：`docs/business-test-report-accessapi-202606151841.md`

### A.1 接入配置

- Provider ID: `accessapi`
- Base URL: `https://provider.example.com/v1`
- Upstream Key: `<PLACEHOLDER_UPSTREAM_API_KEY>`
- 已激活 Service Models: 27 个

### A.2 执行脚本

```bash
cd ai-portal-go
python3 tests/business/probe_accessapi_models.py
python3 tests/business/test_accessapi_multi_model.py
```

### A.3 关键结果

- **模型可用性探测**：27 个已激活模型中，19 个直接可用，8 个不可用（超时/无渠道/未配置价格/参数限制）。
- **端到端多模型测试**：选取 5 个模型（gpt-5.4-nano/mini、claude-sonnet-4-20250514、gemini-2.5-pro、gemini-3.1-flash-image-preview）通过本地 Proxy 调用，**全部 PASS**。
- **计费扣减**：每条请求均生成 `usage_records`，`provider='accessapi'`，用户余额正确扣减。

### A.4 发现缺陷

- Portal `/api/auth/login` 在 Redis `auth_brute:<ip>` key 不存在时返回 503，需修复 `internal/routes/auth.go` 的 `bruteForceProtection` 中对 `redis.Nil` 的处理。
- 部分 AccessAPI 模型上游实际不可用，建议在 Portal 中标记为 paused 或隐藏。
