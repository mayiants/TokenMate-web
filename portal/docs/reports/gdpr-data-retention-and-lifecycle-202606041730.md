# GDPR 数据保留策略与生命周期设计

> **ACTIVE** — 全系统 60 张表的数据保留分类、操作规范与审计日志分层归档机制
>
> 日期: 2026-06-04
> 法规依据: GDPR Article 5(1)(e) — 存储限制原则；Article 17 — 被遗忘权
> 合规目标: 所有用户/财务/业务数据统一保留 7 年，到期自动物理清除

---

## 一、双层标记策略

采用 **`status` + `deleted_at`** 双层解耦设计：

```
status     = 业务状态机（active/suspended/eol/cancelled/...）
deleted_at = GDPR 清除标记（软删除标记，7年后按此字段物理清除）
```

**核心原则**：

1. **删除操作不改 `status`** — 用户/管理员删除一个实体，只设 `deleted_at = NOW()`，status 保持原值
2. **查询始终过滤 `WHERE deleted_at IS NULL`** — 除非审计/回收站页面需要查看已删除记录
3. **7 年后统一清理** — 一个 cron job 扫所有有 `deleted_at` 的表，`WHERE deleted_at < NOW() - INTERVAL '7 years'` 物理 DELETE
4. **时序日志按 `created_at` 清理** — 不分 `deleted_at`，直接按时间窗口保留

---

## 二、全表分类清单（60 张表）

### 🔴 需要 `deleted_at` — 含财务/个人数据，有删除操作 (14 张)

| # | 表名 | 数据类别 | 当前 deleted_at | 业务 status 值 | 删除操作 |
|---|------|---------|:---:|------|------|
| 1 | `users` | 个人身份信息 (PII) | ✅ | `active` (persisted in status column) | 用户注销 / 管理员封禁 |
| 2 | `api_keys` | 认证凭证 | ✅ | `active/inactive/revoked/disabled` | 用户删除 Key / 企业禁用 |
| 3 | `enterprises` | 企业信息 | ✅ | `active/suspended/inactive` | 管理员软删除 |
| 4 | `subscription_plans` | 订阅计划 | ✅ | `active/inactive` | 管理员下架 |
| 5 | `user_groups` | 用户分组 | ✅ (刚加) | `active/inactive` | 企业删除分组 |
| 6 | `orders` | 订单数据（金额、支付方式） | ❌ | `pending` (无 CHECK) | 缺少显式删除 |
| 7 | `invoices` | 发票（金额、状态） | ❌ | `pending` (无 CHECK) | 缺少显式删除 |
| 8 | `invoice_profiles` | 发票抬头（税号、银行账号） | ❌ | `normal/special/personal/proforma` | 用户删除 |
| 9 | `invoice_requests` | 发票申请记录 | ❌ | `pending/processing/completed/rejected` | 缺少显式删除 |
| 10 | `user_subscriptions` | 用户订阅记录 | ❌ | `active/expired/cancelled` | 退订 |
| 11 | `subscription_purchases` | 订阅购买记录 | ❌ | `pending` (无 CHECK) | 缺少显式删除 |
| 12 | `recharge_orders` | 充值订单 | ❌ | `pending` (无 CHECK) | 缺少显式删除 |
| 13 | `upstream_keys` | 第三方 API Key（加密） | ❌ | `active/inactive`（无 CHECK） | 管理员删除 |
| 14 | `single_model_purchases` | 单模型购买记录 | ❌ | `active/exhausted/expired` | 缺少显式删除 |

### 🟡 需要 `deleted_at` — 管理员/用户可删除，含审计价值 (5 张)

| # | 表名 | 数据类别 | 当前 | 说明 |
|---|------|---------|:---:|------|
| 15 | `delivery_addresses` | 收货地址 (PII) | ❌ | 用户可删除，保留已发货订单的地址引用 |
| 16 | `redemption_codes` | 兑换码 | ❌ | 管理员可 disable/删除 |
| 17 | `invitation_codes` | 邀请码 | ❌ | 管理员可 disable/删除 |
| 18 | `service_models` | 服务模型定义 | ❌ | EOL/discontinued 后 7 年可物理清除 |
| 19 | `enterprise_members` | 企业成员关系 | ❌ | 成员被移除后保留审计链 |

### 🟢 时序日志 — 不需要 deleted_at，按 created_at 时间窗口保留 (17 张)

| 表名 | 保留策略 | 说明 |
|------|---------|------|
| `usage_records` | `created_at` 分区，按 `log_retention_days`（默认 30 天） | 核心用量日志，已支持 RANGE 分区 |
| `usage_summary_hourly` | 按 hour 清理 | 小时聚合 |
| `usage_summary_daily` | 按 day 清理 | 日聚合 |
| `audit_logs` | 按 `audit_log_retention_days`（默认 90 天），支持 `deleted_at` 软删除 | 审计日志，已有 hot/warm/cold 分层 |
| `balance_transactions` | 按 `created_at` 保留 7 年 | 余额变动流水，财务对账需要 |
| `enterprise_audit_log` | 按 `created_at` 保留 7 年 | 企业授信变更流水 |
| `enterprise_member_role_log` | 按 `created_at` 保留 7 年 | 企业成员角色变更 |
| `checkin_records` | 按 `checkin_date` 保留 1 年 | 签到记录 |
| `email_notifications` | 按 `created_at` 保留 90 天 | 邮件发送记录 |
| `sensitive_check_logs` | 按 `checked_at` 保留 90 天 | 敏感词检测 |
| `pricing_history` | 按 `changed_at` 保留 7 年 | 定价变更审计 |
| `redemption_records` | 按 `created_at` 保留 7 年 | 兑换流水 |
| `invitation_records` | 按 `created_at` 保留 7 年 | 邀请流水 |
| `provider_health_stats` | 按 `bucket` 保留 30 天 | Provider 健康数据 |
| `upstream_key_usage` | 按 `date` 保留 90 天 | 上游 Key 用量 |
| `channel_health_history` | 按 `tested_at` 保留 30 天 | 通道健康检查历史 |
| `archival_jobs` | 元数据表，不清理 | 归档任务执行记录 |

### 🔵 配置/参考数据 — 不需要 deleted_at，直接更新或删除 (16 张)

| 表名 | 类型 | 说明 |
|------|------|------|
| `providers` | 系统配置 | Provider 定义，管理员 CURD |
| `service_model_approvals` | 工作流 | 模型审批流 |
| `model_products` | 参考数据 | 模型产品信息 |
| `model_pricing` | 参考数据 | 模型定价 |
| `pricing_sources` | 参考数据 | 定价来源缓存 |
| `openrouter_models` | 缓存数据 | OpenRouter 同步缓存 |
| `payment_config` | 系统配置 | 支付配置 |
| `payment_gateways` | 系统配置 | 支付网关定义 |
| `exchange_rates` | 参考数据 | 汇率 |
| `system_settings` | 系统配置 | KV 配置 |
| `model_provider_routing` | 路由配置 | 模型→Provider 路由 |
| `announcements` | 运营内容 | 公告，有 `start_at/end_at` 时间范围 |
| `announcement_reads` | 阅读状态 | 公告已读记录 |
| `enterprise_invitations` | 工作流 | 邀请，有 `expires_at` |
| `super_admin_downgrade_requests` | 工作流 | 超管降级审批 |
| `enterprise_alerts` | 工作流 | 企业告警，有 `notified_at/acknowledged_at` |

### ⚪ 瞬时/运维数据 — 不需要 deleted_at (8 张)

| 表名 | 说明 |
|------|------|
| `schema_migrations` | 迁移版本跟踪 |
| `proxy_heartbeats` | Proxy 心跳，频繁更新 |
| `proxy_instances` | Proxy 实例注册 |
| `key_pool_status` | Key 池实时状态 |
| `channel_health` | 通道当前健康状态 |
| `verification_codes` | 验证码，10 分钟过期 |
| `user_devices` | 用户设备/会话 |
| `invoice_profiles` | 上面已列 — 需要 deleted_at |

---

## 三、审计日志分层归档机制

### 3.1 三层架构

```
┌─────────────────────────────────────────────────┐
│  HOT (PostgreSQL: audit_logs)                    │
│  保留: audit_log_retention_days (默认 90 天)    │
│  特点: 支持查询、筛选、导出、软删除              │
│  操作: 管理员 list/export/soft-delete/restore   │
├─────────────────────────────────────────────────┤
│  WARM (对象存储: S3/MinIO)                       │
│  保留: 90 天 ~ 7 年                              │
│  操作: archival_jobs (warm_to_cold)              │
│  格式: CSV/JSON 压缩归档                         │
│  状态: 逻辑已设计 (archival_jobs 表 + 配置)      │
├─────────────────────────────────────────────────┤
│  COLD (对象存储或磁带)                            │
│  保留: 7 年后清理                                 │
│  操作: archival_jobs (cold_cleanup)              │
│  说明: 物理删除 warm 层中超过 7 年的归档文件     │
└─────────────────────────────────────────────────┘
```

### 3.2 当前 audit_logs 表结构

```sql
CREATE TABLE audit_logs (
    id BIGSERIAL PRIMARY KEY,
    log_id UUID UNIQUE DEFAULT gen_random_uuid(),
    user_id UUID,
    user_email VARCHAR(255),
    category VARCHAR(50),       -- billing/enterprise/user/model/key_pool/...
    action VARCHAR(100),        -- created/updated/deleted/suspended/...
    level VARCHAR(20) DEFAULT 'info',   -- debug/info/warn/error
    message TEXT,
    details JSONB,              -- 扩展详情
    target_type VARCHAR(50),    -- 操作目标类型
    target_id VARCHAR(100),     -- 操作目标 ID
    old_value JSONB,            -- 变更前值
    new_value JSONB,            -- 变更后值
    ip_address INET,            -- 操作者 IP
    user_agent TEXT,            -- 操作者 User-Agent
    deleted_at TIMESTAMP,       -- 软删除标记
    deleted_by UUID,            -- 删除操作人
    created_at TIMESTAMP DEFAULT NOW()
);
```

### 3.3 API 端点

| 端点 | 方法 | 角色 | 说明 |
|------|------|------|------|
| `/api/admin/audit-logs` | GET | Admin | 列表查询（不含已删除） |
| `/api/admin/audit-logs/deleted` | GET | SuperAdmin | 查看已删除日志 |
| `/api/admin/audit-logs/:id` | GET | Admin | 查看单条详情 |
| `/api/admin/audit-logs/:id/soft-delete` | POST | Admin | 软删除（设 deleted_at） |
| `/api/admin/audit-logs/:id/restore` | POST | SuperAdmin | 恢复已删除 |
| `/api/admin/audit-logs/export` | GET | Admin | 导出 CSV（最多 10,000 条） |

### 3.4 归档任务 (archival_jobs)

```sql
CREATE TABLE archival_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_type VARCHAR(20) NOT NULL CHECK (job_type IN ('warm_to_cold', 'cold_cleanup')),
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    records_archived INTEGER DEFAULT 0,
    records_deleted INTEGER DEFAULT 0,
    storage_location VARCHAR(500),   -- S3/MinIO 路径
    status VARCHAR(20) DEFAULT 'running' CHECK (status IN ('running', 'completed', 'failed')),
    error_message TEXT,
    started_at TIMESTAMP DEFAULT NOW(),
    completed_at TIMESTAMP
);
```

**作业类型**：

| Job | 触发 | 操作 |
|-----|------|------|
| `warm_to_cold` | Cron (每日) | 将 `audit_logs` 中超过 `audit_log_retention_days` 的记录导出到 S3/MinIO，然后从 PostgreSQL DELETE |
| `cold_cleanup` | Cron (每月) | 扫描归档存储中超过 7 年的文件，物理删除 |

### 3.5 可配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `log_retention_days` | 30 | usage_records 在线保留天数 |
| `audit_log_retention_days` | 90 | audit_logs 在 PostgreSQL 中保留天数 |

---

## 四、usage_records 分区清理

```sql
CREATE TABLE usage_records (
    ...
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
```

- **分区策略**: 按 `created_at` RANGE 分区
- **在线保留**: `log_retention_days` (默认 30 天)
- **清理方式**: `DROP TABLE usage_records_pYYYY_MM_DD` 直接删除过期分区
- **汇总保留**: `usage_summary_hourly` / `usage_summary_daily` 保留更长时间用于报表

---

## 五、GDPR 数据清除 SOP

### 5.1 常规过期清除（自动）

```
每日 Cron:
  1. warm_to_cold: audit_logs 超过 90 天 → S3 归档 → DELETE FROM PostgreSQL
  2. 分区清理: usage_records 过期分区 DROP TABLE
  3. 瞬时数据清理: verification_codes 过期 DELETE

每月 Cron:
  1. cold_cleanup: S3 中超过 7 年的归档文件 → 物理删除
  2. 扫描所有有 deleted_at 的表，物理 DELETE 超过 7 年的记录
```

### 5.2 用户请求删除（GDPR Article 17）

```
1. 用户提交删除请求 → audit_log (action: gdpr_erasure_requested)
2. 管理员审批
3. 执行:
   a. 设 users.deleted_at = NOW()
   b. 级联设 api_keys.deleted_at = NOW()
   c. 级联设 delivery_addresses.deleted_at = NOW()
   d. 级联设 invoice_profiles.deleted_at = NOW()
   e. PII 字段匿名化: name → 'REDACTED', email → 'redacted@deleted', phone → NULL
4. audit_log (action: gdpr_erasure_executed)
5. 7 年后物理 DELETE（由 Cron 自动执行）
```

### 5.3 物理清除检查清单

执行物理 DELETE 前确认：

- [ ] `deleted_at < NOW() - INTERVAL '7 years'`
- [ ] 关联的 usage_records / audit_logs 已归档到 S3
- [ ] `archival_jobs` 中有对应的 `cold_cleanup` 记录
- [ ] 清除后验证: `SELECT COUNT(*) FROM {table} WHERE deleted_at < ...` → 0

---

## 六、GDPR API 端点（已实现）

### 6.1 用户数据导出（Article 15 — Right of Access）

**`GET /api/gdpr/export`**

返回用户全部个人数据（JSON 格式）：

```json
{
  "exportedAt": "2026-06-04T17:30:00Z",
  "user": {
    "id": "uuid", "name": "...", "email": "user@example.com",
    "role": "user", "created_at": "..."
  },
  "apiKeys": [{ "key_prefix": "sk-xxx", "name": "...", "status": "active", "created_at": "..." }],
  "usageRecords": [{ "trace_id": "...", "model": "...", "input_tokens": 100, ... }],
  "orders": [{ "order_id": "...", "amount": 500, "status": "completed", ... }]
}
```

- 导出后设 `users.gdpr_data_exported_at = NOW()`
- 写入 `audit_logs` (category: `gdpr`, action: `data_exported`)
- 记录操作者 IP + User-Agent

### 6.2 用户数据删除（Article 17 — Right to Erasure）

**`POST /api/gdpr/delete`**

```json
// Request
{ "confirm": "DELETE_MY_DATA" }

// Response
{ "ok": true, "message": "Data deletion completed. Your account has been deactivated." }
```

执行步骤：
1. `api_keys.deleted_at = NOW()` — 软删除所有 API Key
2. `DELETE FROM usage_records WHERE user_id = $1` — ⚠️ 硬删除用量记录
3. `DELETE FROM orders WHERE user_id = $1 AND status = 'pending'` — 删除待支付订单
4. `users.deleted_at = NOW()` + `users.gdpr_erasure_requested_at = NOW()` — 软删除用户
5. 写入 `audit_logs` (category: `gdpr`, action: `data_deleted`)

> ⚠️ 注意：此端点仅设 `deleted_at`，不匿名化 PII（如 email、name）。完整的 PII 匿名化需管理员审批后执行。

### 6.3 GDPR 状态查询

**`GET /api/gdpr/status`**

返回用户当前的 GDPR 操作状态：
```json
{
  "gdpr_data_exported_at": "2026-06-04T17:30:00Z",    // 最近导出时间
  "gdpr_erasure_requested_at": "2026-06-04T18:00:00Z"  // 删除请求时间
}
```

### 6.4 账户自助删除（Profile 页面）

**`DELETE /api/profile/account`**

相比 GDPR 删除更轻量：
- `users` 设 `deleted_at`，同时匿名化 email (`deleted_{id}@deleted.local`)、name (`Deleted User`)、phone (NULL)、password_hash (`deleted`)
- `api_keys.deleted_at = NOW()` — 级联软删除
- 写入 `audit_logs` (category: `security`, action: `account_deleted`)

**两套删除路径对比**：

| 维度 | GDPR 删除 (`/api/gdpr/delete`) | 账户删除 (`/api/profile/account`) |
|------|------|------|
| 确认方式 | `{"confirm": "DELETE_MY_DATA"}` | 无需确认 |
| usage_records | 硬删除 | 保留 |
| orders | 删除 pending 状态 | 保留 |
| PII 匿名化 | ❌ 未执行 | ✅ email/name/phone 匿名化 |
| audit 记录 | `gdpr:data_deleted` | `security:account_deleted` |
| 适用场景 | GDPR 合规请求 | 用户主动注销 |

> 🔧 建议改进：GDPR 删除路径应加上 PII 匿名化步骤，与账户删除保持一致。两条路径应统一为一条完整的 GDPR 删除链路。

---

## 七、实施状态

| 优先级 | 操作 | 状态 |
|--------|------|:---:|
| P0 | 添加 `deleted_at` 到 14 张表 | ✅ 已完成 (2026-06-04) |
| P0 | 统一两条删除路径 + PII 匿名化 | ✅ 已完成 (2026-06-04) |
| P1 | 实现 warm_to_cold 归档 | 🔲 待实施 |
| P1 | 实现 cold_cleanup | 🔲 待实施 |
| P1 | 扩展数据导出范围（invoices/balance_transactions 等） | 🔲 待实施 |
| P2 | GDPR 审计报告 | 🔲 待实施 |

### 已实施详情

**P0-1: `deleted_at` 列** — 以下 14 张表已添加 `deleted_at TIMESTAMP`（2026-06-04）：
`orders`, `invoices`, `invoice_profiles`, `invoice_requests`, `user_subscriptions`, `subscription_purchases`, `recharge_orders`, `upstream_keys`, `single_model_purchases`, `delivery_addresses`, `redemption_codes`, `invitation_codes`, `service_models`, `enterprise_members`

加上原先已有的 5 张表（`users`, `api_keys`, `enterprises`, `subscription_plans`, `user_groups`），目前共 **19 张表** 支持 `deleted_at` 软删除。

**P0-2: GDPR 删除路径统一** — `POST /api/gdpr/delete` 现已包含完整 PII 匿名化：
- `api_keys` → 软删除
- `delivery_addresses` → 软删除（新增）
- `invoice_profiles` → 软删除（新增）
- `usage_records` → 硬删除（分区清理处理保留）
- `orders` → 软删除全部（不再仅限于 pending）
- `users` → 软删除 + PII 匿名化（email/name/phone/shipping/invoice_info/backup_email/password_hash）

---

## 八、待解决问题：查询层架构债务

### 8.1 问题描述

当前项目中，同一业务数据的查询逻辑在两处独立维护：

```
web/data.go  (2582 行, 39 个函数)  →  直接写 SQL（页面渲染用）
routes/      (22 个文件)           →  直接写 SQL（API 响应用）
```

两者互不知情。例如 groups 的列表查询：

| 位置 | 用途 | SQL |
|------|------|-----|
| `routes/groups.go:listGroups` | `/api/admin/groups` API | `WHERE g.deleted_at IS NULL` ✅ |
| `web/data.go:GetAdminGroups` | `/admin/groups` 页面渲染 | 最初漏了 `deleted_at` ❌ |

2026-06-04 修复 `deleted_at` 时，发现同一过滤条件需要在 **4 处以上** 分别添加（API 路由 + data.go 页面查询 + 子查询 + 聚合统计），容易遗漏。当天就因 data.go 漏了过滤导致页面仍然显示已删除数据。

### 8.2 影响

- 新增一个数据过滤条件（如 `deleted_at`、`status`、权限控制），需要在多处重复修改
- 页面渲染和 API 返回的数据可能不一致
- 排查问题时难以定位（看起来 API 正确但页面不对）

### 8.3 建议方案

将 data.go 的查询逻辑迁移到 routes 层，页面渲染通过调用 routes 层获取数据：

```
web/pages.go ──→ routes/ (共用查询) ──→ DB
                     │
              ┌──────┴──────┐
              │  JSON API   │  ← /api/xxx
              │  页面渲染    │  ← /admin/xxx
              └─────────────┘
```

**待讨论**：重构范围（一次性 vs 逐步）、routes 层函数签名设计（返回 JSON vs 返回 struct 供模板使用）。
