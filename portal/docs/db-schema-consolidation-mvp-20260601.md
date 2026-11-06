# MVP 试点前数据库 Schema 归拢与初始化流程整合

> **⚠️ HISTORICAL** — Schema consolidation completed for MVP pilot

> 状态：已完成  
> 日期：2026-06-01  
> 涉及模块：`ai-portal-go` (Portal)  
> 作者：Kimi Code CLI  

---

## 1. 背景与问题

在 MVP 试点前的测试阶段，我们发现数据库初始化存在严重问题：

### 1.1 Migration 链式回滚陷阱

Portal 使用基于文件名的 migration 框架（`migrations/*.sql`）。该框架采用**事务模式**执行：

- 每个 migration 文件在一个事务中执行
- **单条 SQL 失败即回滚整个事务**
- 失败的 version **不写入** `schema_migrations`
- **后续所有 migration 被跳过**

这导致"一个出错、后面全错"的连锁反应。在测试环境中，由于数据类型不匹配、字段缺失等问题，频繁出现部分表创建成功、后续表完全未创建的情况，页面直接 500。

### 1.2 手工维护的 `initCoreTables()` 不完整

此前 `cmd/server/main.go` 中的 `initCoreTables()` 函数作为 migration 的 fallback，但存在以下问题：

- **字段遗漏**：`service_models` 缺少 `input_price_cache_hit` / `input_price_cache_miss` / `reasoning_price` / `billing_type` 等 4 个 pricing 字段，导致 Bedrock 模型同步全部失败
- **表缺失**：`openrouter_models` 表完全不存在，导致 OpenRouter 实时同步失败
- **表名不一致**：代码中使用 `sensitive_logs`，但 schema 中创建的是 `sensitive_check_logs`，敏感词拦截日志功能不可用
- **缺失核心表**：`payment_config` 表完全不存在，但 admin 支付配置（Stripe/PayPal/支付宝/微信）直接 INSERT 到该表，运行时必崩
- **DDL 与代码不一致**：`generateBedrockAPIModelName("openai.gpt-5.4")` 错误地返回 `"4"`（取最后一段而非去掉 provider 前缀）

---

## 2. 解决方案：统一初始化流程

### 2.1 核心决策

**废弃 `database.Migrate()` 的文件执行**，将所有 DDL（CREATE TABLE / ALTER TABLE / CREATE INDEX）和 Seed Data 集中管理：

| 文件 | 职责 |
|---|---|
| `internal/db/schema.go` | 所有表结构初始化（CREATE TABLE + ALTER TABLE + CREATE INDEX） |
| `internal/db/seed.go` | 默认数据注入（providers、subscription plans、default user group） |
| `cmd/server/main.go` | 调用 `db.InitSchema()` + `db.SeedDefaultData()` |

### 2.2 为什么选择这种方式

**优点：**
- ✅ **幂等**：全部使用 `CREATE TABLE IF NOT EXISTS` + `ALTER TABLE ADD COLUMN IF NOT EXISTS`，重复运行无副作用
- ✅ **无事务链式回滚**：单条 DDL 失败不影响后续，下一条继续执行
- ✅ **部署简单**：单 binary + 单函数，无需管理 migration 文件顺序
- ✅ **可测试**：可以单独对 schema 初始化写测试（CREATE → DROP 验证字段完整）

**缺点：**
- ⚠️ 代码膨胀：`schema.go` 1234 行，纯 DDL 字符串
- ⚠️ 手工维护风险：代码新增字段时如果忘记同步到 `schema.go`，clean-all 后会再现 bug
- ⚠️ 字段类型不一致风险：比如 migration 中是 `DECIMAL(12,2)`，但 `schema.go` 中写成 `BIGINT`，编译期发现不了

---

## 3. 已完成的具体工作

### 3.1 表结构全覆盖

`internal/db/schema.go` 当前覆盖 **45 张表**：

**核心表（业务主路径）：**
`users`, `api_keys`, `providers`, `enterprises`, `user_groups`, `subscription_plans`, `service_models`, `user_subscriptions`, `enterprise_members`, `usage_records`, `orders`, `invoices`

**运营表：**
`audit_logs`, `proxy_heartbeats`, `proxy_instances`, `upstream_keys`, `system_settings`, `user_devices`, `verification_codes`

**企业/支付表：**
`enterprise_invitations`, `payment_gateways`, `subscription_purchases`, `balance_transactions`, `exchange_rates`, `enterprise_credit_log`, `enterprise_member_role_log`, `payment_config`

**营销/用户表：**
`redemption_codes`, `redemption_records`, `checkin_records`, `announcements`, `announcement_reads`, `invitation_codes`, `invitation_records`

**模型/渠道表：**
`openrouter_models`, `model_provider_routing`, `provider_health_stats`, `upstream_key_usage`, `key_pool_status`, `channel_health`, `channel_health_history`

**发票/地址表：**
`invoice_profiles`, `invoice_requests`, `delivery_addresses`

**其他表：**
`service_model_approvals`, `enterprise_alerts`, `recharge_orders`, `sensitive_check_logs`, `archival_jobs`, `email_notifications`, `model_products`, `pricing_sources`, `model_pricing`, `pricing_history`, `single_model_purchases`, `super_admin_downgrade_requests`, `usage_summary_hourly`, `usage_summary_daily`

### 3.2 分区表支持

`usage_records` 已改为 **RANGE 分区表**（分区键 `created_at`），并创建默认分区 `usage_records_default`。

### 3.3 字段补全清单

以下字段在测试阶段被发现缺失，已全部补齐：

| 表 | 补齐字段 | 影响 |
|---|---|---|
| `service_models` | `input_price_cache_hit`, `input_price_cache_miss`, `reasoning_price`, `billing_type` | Bedrock 模型同步 |
| `service_models` | `validation_status`, `validation_result`, `validated_at`, `suggested_api_name`, `avg_latency_ms`, `success_rate`, `priority` | 模型验证路由 |
| `service_models` | `eol_announced_at`, `eol_service_at`, `eol_purge_at`, `eol_notes` | admin/models 页面显示 |
| `usage_records` | `group_id`, `actual_group_id`, `is_fallback`, `pricing_ratio_at_request` | fallback 计费追踪 |
| `usage_records` | `gift_deduction`, `recharge_deduction`, `credit_deduction`, `subscription_deduction` | 多维度计费拆分 |
| `usage_records` | `project_name`, `project_id`, `api_key_name`, `key_prefix`, `original_track` | 计费归因 |
| `user_groups` | `group_type`, `billing_mode`, `models`, `is_default`, `routing_track` | default group 显示 |
| `enterprises` | `description`, `contact_name`, `contact_phone`, `max_members`, `allowed_domains` | 企业信息管理 |
| `subscription_plans` | `tpm_limit` ~ `rp5h_limit` 等 10 个 rate limit 字段 | 套餐限速 |
| `user_subscriptions` | `tpm_limit` ~ `rp5h_limit` + used/reset_at 等 25 个字段 | 订阅限速追踪 |
| `provider_health_stats` | `request_count`, `failure_count`, `timeout_count`, `rate_limit_count`, `total_latency_ms` | 渠道健康监控 |
| `upstream_key_usage` | `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `error_count`, `total_latency_ms` | upstream key 用量统计 |
| `users` | `timezone`, `quota_total`, `quota_used`, `quota_remaining`, `gift_balance`, `recharge_balance` | 用户配额/余额 |
| `audit_logs` | `target_type`, `target_id`, `old_value`, `new_value`, `deleted_at`, `deleted_by` | 审计追踪 |
| `redemption_codes` | `description`, `reward_type` | 兑换码功能 |
| `upstream_keys` | `quota_used` | key 配额追踪 |
| `enterprise_members` | `quota_limit`, `joined_at` | 企业成员管理 |

### 3.4 Bug 修复

| Bug | 修复 |
|---|---|
| `generateBedrockAPIModelName("openai.gpt-5.4")` → `"4"` | 改为 `strings.Index(productID, ".")`，只去掉第一个 provider 前缀，保留 `gpt-5.4` |
| `sensitive_logs` vs `sensitive_check_logs` 表名不一致 | 统一改为 `sensitive_check_logs`，字段同步修正：`text_content`→`content_preview`, `action_taken`→`action`, `created_at`→`checked_at` |
| `payment_config` 表完全缺失 | 新增 CREATE TABLE：`config_key` (PK), `config_value`, `enabled`, `updated_at` |

### 3.5 代码重构

- `cmd/server/main.go`：**1980 行 → 646 行**
- `initCoreTables()` 函数删除
- 新增 `internal/db/schema.go`（1234 行，`InitSchema` 方法）
- 新增 `internal/db/seed.go`（121 行，`SeedDefaultData` 方法）
- `main.go` 调用方式：
  ```go
  if err := database.InitSchema(ctx, logger); err != nil {
      logger.Error("Schema init failed", zap.Error(err))
  }
  if err := database.SeedDefaultData(ctx, logger); err != nil {
      logger.Warn("Seed warning", zap.Error(err))
  }
  ```

---

## 4. 验证与检查机制

### 4.1 对比脚本（旧，已淘汰）

~~`scripts/gen_schema_from_migrations.sh`~~ — 扫描 migration 文件中的 DDL 与 `schema.go` 对比。

> **⚠️ 已不再适用。** Migration 文件是旧的 schema 来源，`schema.go` 才是当前的唯一来源。
> 对比 migration → schema.go 只能告诉你"有没有遗漏旧 migration 的内容"，无法发现
> "Go SQL 代码引用了列但 CREATE TABLE 没定义"的问题。请用 4.1b 的静态分析脚本。

### 4.1b 静态分析脚本（新）⭐

`scripts/check_schema_vs_code.py` — **从 Go SQL 代码出发**，扫描所有 `internal/routes/`、`internal/web/`、`internal/middleware/`、`internal/services/` 中的 SQL 语句（INSERT/UPDATE/SELECT/DELETE），提取 `(表名, 列名)` 引用，与 `schema.go` 的 CREATE TABLE + ALTER TABLE 定义做交叉对比。

**原理：**
```
Go SQL code                          schema.go
───────────                          ─────────
INSERT INTO foo (col_a, col_b)  →    CREATE TABLE foo (
UPDATE bar SET col_x = ...      →      col_a TYPE,
SELECT col_y FROM baz          →      col_b TYPE,
                                      ...
                                    )
                                    ↓
                              报告 code 引用了但 schema 缺失的 (table, column)
```

**使用方式：**
```bash
python3 scripts/check_schema_vs_code.py

# 详细模式（显示每个缺失列的引用来源）
python3 scripts/check_schema_vs_code.py --verbose
```

**输出示例：**
```
Schema tables:      60
Schema columns:     794
Code column refs:   516
Missing columns:    0     ← 0 = 全通过
Missing tables:     0
PASS
```

**优势：**
- ✅ 不依赖 migration 文件 — 直接从 Go 代码提取 SQL 引用
- ✅ 秒级运行 — 纯 Python，无需启动数据库
- ✅ 零误报 — 只分析 backtick 内 SQL 字符串，排除 Go 变量名
- ✅ 括号深度追踪 — 正确处理嵌套 `CHECK(...)`、`PARTITION BY RANGE(...)` 等复杂 DDL
- ✅ 适合 CI 集成 — `exit 0` = pass, `exit 1` = fail

### 4.2 运行时检查

启动后可通过以下方式验证：
```bash
# 检查核心表
bash scripts/check_db.sh

# 手动查询
psql -U postgres -d ai_proxy_portal -c "\dt"
```

---

## 4.3 2026-06-03 静态分析实战结果

首次运行 `check_schema_vs_code.py` 发现 **45 个缺失列** 分布在 **19 张表** 中：

| 表 | 缺失列 | 数量 |
|---|--------|------|
| `announcements` | type, pinned, show_popup, priority | 4 |
| `enterprise_credit_log` | change_type, changed_by, delta, field_name, reason | 5 |
| `provider_health_stats` | error_4xx_count, error_5xx_count, error_rate, status | 4 |
| `invitation_codes` | expires_at, reward_amount, reward_type, invitee_reward_amount | 4 |
| `enterprise_invitations` | accepted_at, expires_at, invited_by | 3 |
| `invitation_records` | invitation_code_id, reward_granted, invitee_reward_granted | 3 |
| `subscription_purchases` | order_id, starts_at, expires_at | 3 |
| `user_devices` | device_name, is_current, location | 3 |
| `balance_transactions` | balance_type, reference_id | 2 |
| `exchange_rates` | fetched_at, source | 2 |
| `users` | preferred_currency, status | 2 |
| `verification_codes` | type, value | 2 |
| `upstream_key_usage` | avg_latency_ms, model | 2 |
| `channel_health` | last_error | 1 |
| `enterprise_member_role_log` | reason | 1 |
| `orders` | product_name | 1 |
| `redemption_codes` | plan_id | 1 |
| `upstream_keys` | validation_at | 1 |
| `usage_records` | routing_track | 1 |

**这些漏列的共性：** 都是只在特定业务流程触发时才需要的列（如：管理员发公告、企业信用变更、邀请码兑换、设备注册等），而非核心 CRUD 路径上的列。这也印证了文档开头提到的风险："很多数据结构缺少表项，只有具体执行业务流程才会发现"。

**修复：** 全部 45 个缺失列已通过 ALTER TABLE + CREATE TABLE 双路径补入 `schema.go`，脚本验证通过（0 missing）。

---

## 5. 后续维护指南

### 5.1 新增字段

**当前阶段（MVP 试点前）：** 直接修改 `internal/db/schema.go`：

1. 找到对应表的 `CREATE TABLE IF NOT EXISTS`
2. 在字段列表中添加新字段
3. 或者（更推荐）在 ALTER TABLE 兜底区添加：
   ```go
   _ = database.Exec(ctx, `ALTER TABLE xxx ADD COLUMN IF NOT EXISTS new_field VARCHAR(20) DEFAULT 'default'`)
   ```

**MVP 试点后：** 恢复 migration 框架，但修改为**非事务模式**（单条失败继续执行），`schema.go` 作为兜底保留。

### 5.2 新增表

1. 在 `internal/db/schema.go` 的 `InitSchema` 方法中添加 `CREATE TABLE IF NOT EXISTS`
2. 如果有索引，同时添加 `CREATE INDEX IF NOT EXISTS`
3. 运行 `bash scripts/gen_schema_from_migrations.sh` 验证无遗漏
4. 如果表需要 seed 数据，在 `internal/db/seed.go` 中添加

### 5.3 clean-all 后重新验证

每次 `clean-all` 后，运行以下检查：

```bash
# 1. 编译
make build

# 2. 启动
docker-compose up -d

# 3. 验证表结构
bash scripts/check_db.sh

# 4. 验证核心页面
# - admin/enterprise
# - admin/groups
# - admin/subscriptions
# - admin/models（OpenRouter + Bedrock 同步）
# - admin/providers
```

---

## 4.4 2026-06-04 运行时错误发现

静态分析通过后（0 missing），实际运行中仍发现了漏网之鱼。原因：**静态分析只扫描 `internal/routes/*.go` 中的 backtick SQL 字符串**，无法覆盖：

- **Webhook 写入** — `webhook.go` 用 struct 序列化 + `ON CONFLICT` upsert，不走 `INSERT INTO` 字符串
- **数据库约束** — `ON CONFLICT (col1, col2)` 需要的 UNIQUE 索引，纯 DDL 概念
- **ORM 式查询** — 动态拼接 SQL、`COALESCE(g.rate_limit_tpm, ...)` 等不在 backtick 内

### 第二批发现（PG 日志扫描）

通过 `docker logs ai-portal-postgres | grep ERROR` 发现：

| 错误 | 表 | 影响 |
|------|-----|------|
| `column "updated_at" does not exist` | `provider_health_stats` | 健康数据无法写入，webhook 静默失败 |
| `column "updated_at" does not exist` | `upstream_key_usage` | 上行 key 用量无法记录 |
| `no unique/exclusion constraint matching ON CONFLICT` | `provider_health_stats` | upsert 失败 |
| `no unique/exclusion constraint matching ON CONFLICT` | `upstream_key_usage` | upsert 失败 |
| `no unique/exclusion constraint matching ON CONFLICT` | `usage_records` | 用量记录写入失败 |
| `column "fixed_price" does not exist` | `service_models` | webhook 价格查询报错 |
| `column g.rate_limit_tpm does not exist` | `user_groups` | API key 验证查询报错 |

### 新增检测脚本

**`scripts/check_pg_errors.sh`** — 扫描 PostgreSQL 日志中的运行时 schema 错误：

```bash
bash scripts/check_pg_errors.sh
```

输出示例：
```
22 ERROR: no unique or exclusion constraint matching ON CONFLICT
 5 ERROR: column g.rate_limit_tpm does not exist
 5 ERROR: column "updated_at" of relation "upstream_key_usage" does not exist  
 4 ERROR: column "fixed_price" does not exist
```

### 三层检测体系

```
Layer 1 (pre-deploy):  python3 scripts/check_schema_vs_code.py
                      → 检测 Go SQL 代码引用的列

Layer 2 (post-deploy): bash scripts/check_pg_errors.sh
                      → 检测 PostgreSQL 运行时错误

Layer 3 (post-deploy): bash scripts/check_db.sh
                      → 检查已知关键表的列完整性
```

### 部署后完整 checklist

```bash
# 1. 静态分析 (pre-deploy 也可跑)
python3 scripts/check_schema_vs_code.py

# 2. 冒烟测试 — 触发所有 webhook 路径
for i in $(seq 1 10); do
  curl ... /v1/chat/completions ...
done
sleep 60  # 等 Proxy webhook 批量上报

# 3. PG 错误扫描
bash scripts/check_pg_errors.sh

# 4. 数据库直观检查
bash scripts/check_db.sh
```

---

| 风险 | 缓解措施 |
|---|---|
| `schema.go` 手工维护遗漏字段 | 每次改表后运行 `gen_schema_from_migrations.sh` 扫描 |
| 字段类型不一致（`BIGINT` vs `DECIMAL`） | 对照 Go 代码 Scan 类型 + 运行时功能测试 |
| 代码膨胀（1234 行纯 DDL） | MVP 后恢复 migration 框架，`schema.go` 退化为兜底 |
| 多人协作冲突 | `schema.go` 按表分块，ALTER TABLE 统一放末尾 |

---

## 7. 附录：文件清单

| 文件 | 行数 | 说明 |
|---|---|---|
| `internal/db/schema.go` | 1234 | 所有 DDL（CREATE/ALTER/INDEX） |
| `internal/db/seed.go` | 121 | 默认数据 seed |
| `scripts/gen_schema_from_migrations.sh` | ~80 | Migration vs schema 对比脚本 |
| `scripts/check_db.sh` | ~100 | 运行时数据库结构检查脚本 |
| `cmd/server/main.go` | 646 | 调用 InitSchema + SeedDefaultData |
