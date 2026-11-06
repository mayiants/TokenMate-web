# Enterprise Admin 精细化管理增强方案

> **Status:** COMPLETE (P0 + P1 fully implemented)  
> 创建时间：2026-07-26 01:30 UTC  
> 关联提交：64bfd9d fix(enterprise/audit): repair credit/audit log, log member limit changes, skip no-op edits  
> 完成状态：P0（审计补齐）✅、P1（成员详情页、Key 详情页、告警中心）✅、P2（失败请求面板、财务精细化）为增强型功能，按需实施。

---

## 1. 背景与目标

### 1.1 当前现状

Portal 的 enterprise admin 侧已有以下页面：

| 页面 | 主要能力 |
|------|---------|
| `/enterprise/dashboard` | 企业概览、KPI、趋势图、Top Models/Members/Keys、当前 Alert |
| `/enterprise/users` | 成员列表、编辑（role/quota limit/credit limit/name）、批量操作、邀请 |
| `/enterprise/keys` | 企业 key 创建、重新分配、禁用/启用、删除 |
| `/enterprise/billing` | 额度/信用概览、发票历史、发票申请 |
| `/enterprise/usage` | 按成员/模型/Key 的用量明细、月度对账、成员 API Logs |
| `/enterprise/groups` | 企业分组创建/编辑/删除 |
| `/enterprise/settings` | 企业资料、变更申请 |
| `/enterprise/audit` | 角色变更日志、额度/信用变更日志、企业申请记录 |

### 1.2 核心问题

与 system admin 侧相比，enterprise admin 目前更像一个「轻量控制台」：能看总量、能做一些基础操作，但缺乏：

1. **操作可追溯性**：大量管理操作未进入审计日志。
2. **对象级 drill-down**：成员、key、group 缺少独立的详情页。
3. **告警可管理性**：只能被动看 dashboard 底部 alert，无法配置阈值、查看历史。
4. **故障自查能力**：缺少失败请求、rate limit 命中、异常调用等企业级可观测能力。
5. **财务精细化**：缺少交易流水、成本预测、按成员成本分摊。

### 1.3 目标

在不向 enterprise admin 开放平台级权限的前提下，补齐其对「本企业数据」的精细化展示与管理能力，降低对 platform admin 的依赖。

---

## 2. 差距分析（System Admin vs Enterprise Admin）

### 2.1 审计与合规

| System Admin 能力 | Enterprise Admin 现状 | 差距 |
|-------------------|----------------------|------|
| `/admin/audit-logs`：全平台操作审计，支持筛选、分页、导出、软删除 | `/enterprise/audit`：只有 role change + credit/quota change | 关键操作（key、member、group、settings）无审计 |
| `/admin/compliance`：合规与数据保留 | 无 | 企业无 GDPR/数据导出入口 |

### 2.2 用户/成员管理

| System Admin 能力 | Enterprise Admin 现状 | 差距 |
|-------------------|----------------------|------|
| 用户详情页：余额、role、登录状态、usage | 只有列表和简单编辑抽屉 | 无成员详情页、无 last active |
| 用户状态管理：toggle status、promote/demote | 支持 toggle + promote | 邀请状态跟踪弱 |

### 2.3 Key 与权限管理

| System Admin 能力 | Enterprise Admin 现状 | 差距 |
|-------------------|----------------------|------|
| `/admin/keypool`：upstream key 池管理 | `/enterprise/keys`：只管理企业 key | 无 key 级用量、无 key 审计 |
| `/admin/rate-limits`：rate limit 监控 | 无 | 企业看不到自己的并发/限速命中情况 |

### 2.4 用量与可观测

| System Admin 能力 | Enterprise Admin 现状 | 差距 |
|-------------------|----------------------|------|
| `/admin/upstream-ops`：provider health、error 分析 | 无 | 企业无法自助排查失败原因 |
| `/admin/usage-explorer`：高级多维分析 | `/enterprise/usage` 只有基础维度 | 缺少 endpoint、error、latency 等维度 |

### 2.5 财务与计费

| System Admin 能力 | Enterprise Admin 现状 | 差距 |
|-------------------|----------------------|------|
| `/admin/reconciliation`：对账、ledger、top users/enterprises | `/enterprise/reconciliation` 只有月度成员汇总 | 缺少交易流水、成本预测 |
| 发票管理 | 可申请发票、看列表 | 缺少下载、交易明细 |

---

## 3. 增强方案

### 3.1 P0：补齐 Enterprise Audit（操作可追溯）

**目标**：让 enterprise admin 能查到所有本企业内的管理操作。

#### 3.1.1 扩展审计事件类型

建议统一写入 `enterprise_audit_log`（额度/资金相关）或新建 `enterprise_audit_log`（通用管理操作）。考虑到已有 `audit_logs` 通用审计表，推荐方案：

- **方案 A（推荐）**：复用并扩展 `enterprise_audit_log`，新增 `action_category` 字段区分 `billing` / `member` / `key` / `group` / `settings` / `alert`。
- **方案 B**：新建 `enterprise_audit_log` 表，专门记录非资金类管理操作。

考虑到当前 `enterprise_audit_log` 已经有 `change_type`、`field_name`、`old_value`、`new_value`、`user_id` 等字段，**方案 A 成本更低**。

#### 3.1.2 需要补录的审计事件

| 事件 | change_type | 触发位置 | 备注 |
|------|------------|---------|------|
| 企业 key 创建 | `enterprise_key_created` | `createEnterpriseKey` | 记录 key_id、assignee |
| 企业 key 重新分配 | `enterprise_key_reassigned` | `reassignKey` | 记录旧/新 assignee |
| 企业 key 禁用/启用 | `enterprise_key_status_changed` | `revokeEnterpriseKey` / `reactivateEnterpriseKey` | 记录旧/新 status |
| 企业 key 删除 | `enterprise_key_deleted` | `deleteEnterpriseKey` | 记录 key_prefix |
| 成员邀请发送 | `member_invited` | `inviteMember` | 记录邮箱、role |
| 成员邀请批准/拒绝 | `member_invitation_approved` / `rejected` | `approveInvitation` / `rejectInvitation` | 记录 inviter |
| 成员移除 | `member_removed` | `removeMyEnterpriseMember` | 记录 user_id |
| 成员状态切换 | `member_status_toggled` | `toggleMemberStatus` | 记录旧/新 status |
| group 创建/编辑/删除 | `group_created` / `updated` / `deleted` | `createEnterpriseGroup` / `updateEnterpriseGroup` / `deleteEnterpriseGroup` | 记录 group_id |
| 企业资料修改 | `enterprise_profile_updated` | `updateMyEnterprise` | 记录修改字段 |
| 变更请求提交 | `change_request_submitted` | `submitEnterpriseChangeRequest` | 记录字段、旧/新值 |
| Alert 确认 | `alert_acknowledged` | `acknowledgeAlert` | 记录 alert_id |

#### 3.1.3 Audit 页面增强

- 增加操作类型筛选（全部 / Billing / Member / Key / Group / Settings / Alert / Role）✅
- 增加时间范围筛选 ✅
- 增加按操作人、按目标成员筛选 ✅
- 导出 CSV 包含完整字段 ✅

### 3.2 P1：成员详情页（Member Detail）

**目标**：从「列表管理」升级到「对象级管理」。

#### 3.2.1 页面路径

`/enterprise/members/:userId`

#### 3.2.2 展示内容

| 模块 | 内容 |
|------|------|
| 基础信息 | 邮箱、姓名、role、status、加入时间 |
| 限额卡片 | quota_limit / quota_used、credit_limit / credit_used |
| 用量概览 | 本月/本季度 requests、tokens、cost |
| 持有 Key | 分配给该成员的 key 列表及状态 |
| 所属 Group | 所属 groups（后续扩展） |
| 操作日志 | 该成员相关的 audit 事件 |
| API Logs | 最近 200 条调用记录（复用 `/api/enterprise/usage/member-logs`） |

### 3.3 P1：Key 详情与用量

**目标**：让企业 admin 知道每把 key 的用量、成本、归属、变更历史。

#### 3.3.1 现有 `/enterprise/keys` 增强

- 列表增加列：Assigned To、Status、Last Used、本月 Cost
- 增加操作：View Details

#### 3.3.2 Key Detail Drawer / 页面

- 基础信息：key prefix、status、assignee、created_at
- 用量概览：requests / tokens / cost（本月/本季度）
- 最近调用：复用 usage 日志，按 key 过滤
- 变更历史：key 创建、分配、禁用、删除等 audit 事件

### 3.4 P1：告警中心（Alert Center）

**目标**：从 dashboard 底部被动展示，升级为主动管理中心。

#### 3.4.1 页面路径

`/enterprise/alerts`

#### 3.4.2 功能

- 告警历史列表（全部 / 未确认 / 已确认）
- 支持按类型筛选：quota_warning / quota_exceeded / credit_warning / credit_exceeded
- 告警阈值配置：
  - quota_warning 百分比（默认 70%）
  - quota_exceeded 百分比（默认 100%）
  - credit_warning 百分比（默认 70%）
  - credit_exceeded 百分比（默认 100%）
- 通知方式配置：企业管理员邮箱（可选）

#### 3.4.3 数据表扩展

当前 `enterprise_alerts` 表（假设存在）需要支持：
- `acknowledged_by` / `acknowledged_at`
- `threshold_percent`
- `alert_type`

如尚未有 alert 配置表，需新增 `enterprise_alert_settings`。

### 3.5 P2：失败请求与可观测性

**目标**：企业 admin 能自助排查「为什么某些请求失败了」。

#### 3.5.1 新增 Error Analysis 标签页

在 `/enterprise/usage` 增加第四个维度或独立标签：

- 按错误码聚合（429 / 401 / 403 / 500 / MODEL_CAPABILITY_EXCEEDED / MEMBER_CREDIT_EXHAUSTED 等）
- 按时间分布
- 按 key / 成员分布

#### 3.5.2 Rate Limit 命中看板

- 企业级并发限制配置与命中统计
- 展示哪些 key/member 触发了 429

### 3.6 P2：财务精细化

#### 3.6.1 交易流水（Transactions）✅

在 `/enterprise/billing` 增加「Transactions」标签页：

- 企业池充值（topup）
- 月度重置
- 发票扣减
- 退款

#### 3.6.2 成本预测 ✅

在 dashboard 增加「本月预测成本」卡片：

- 基于当前日均用量 × 剩余天数
- 考虑 pricing_ratio

#### 3.6.3 按成员成本分摊 ✅

在月度对账中增加「按成员成本占比」导出，方便企业内部 chargeback。

---

## 4. 技术实现建议

### 4.1 审计日志表结构

推荐在现有 `enterprise_audit_log` 基础上扩展，或统一迁移到新表 `enterprise_audit_log`：

```sql
CREATE TABLE enterprise_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    enterprise_id UUID NOT NULL REFERENCES enterprises(id),
    category VARCHAR(50) NOT NULL,        -- billing | member | key | group | settings | alert | security
    action VARCHAR(50) NOT NULL,          -- created | updated | deleted | reassigned | toggled | acknowledged
    change_type VARCHAR(50),              -- 与现有 enterprise_audit_log 对齐
    target_type VARCHAR(50),              -- user | key | group | enterprise | alert
    target_id VARCHAR(100),               -- 目标对象 ID
    user_id UUID REFERENCES users(id),    -- 被操作成员（可选）
    changed_by UUID REFERENCES users(id), -- 操作人
    old_value JSONB,
    new_value JSONB,
    delta BIGINT,                         -- 资金类变更使用
    field_name VARCHAR(100),
    reason TEXT,
    ip_address INET,
    created_at TIMESTAMP DEFAULT NOW()
);

CREATE INDEX idx_enterprise_audit_enterprise_created ON enterprise_audit_log(enterprise_id, created_at DESC);
CREATE INDEX idx_enterprise_audit_category ON enterprise_audit_log(enterprise_id, category, created_at DESC);
CREATE INDEX idx_enterprise_audit_target ON enterprise_audit_log(target_type, target_id);
```

### 4.2 后端改动点

| 模块 | 改动 |
|------|------|
| `pkg/audit` 或新包 | 封装 `EnterpriseAuditLogger.Log(ctx, category, action, targetType, targetID, oldValue, newValue, userID)` |
| `internal/routes/enterprise_*.go` | 在关键操作后调用审计日志 |
| `internal/routes/enterprise_common.go` | 扩展 `listEnterpriseAuditLog` / `exportAuditCSV` |
| `internal/web/pages.go` | 新增 `/enterprise/members/:id`、 `/enterprise/alerts` 页面 handler |

### 4.3 前端改动点

| 页面 | 改动 |
|------|------|
| `/enterprise/audit.html` | 增加筛选器、完整事件类型展示 |
| 新增 `/enterprise/member-detail.html` | 成员详情 |
| `/enterprise/keys.html` | 增加用量列、详情 drawer |
| 新增 `/enterprise/alerts.html` | 告警历史与阈值配置 |
| `/enterprise/usage.html` | 增加 Error Analysis 标签 |
| `/enterprise/billing.html` | 增加 Transactions 标签、成本预测 |

---

## 5. 实施优先级

| 优先级 | 事项 | 价值 | 工作量 |
|--------|------|------|--------|
| P0 | 补齐 audit：key/member/group/settings/alert 事件 | 高（合规、排障） | 中 |
| P1 | 成员详情页（Member Detail） | 高（管理颗粒度） | 中 |
| P1 | Key 详情与用量 | 高（key 管理） | 中 |
| P1 | 告警中心（Alert History + 阈值配置） | 高（从被动到主动） | 中 |
| P2 | 失败请求 / Error Analysis | 中（自助排障） | 中 |
| P2 | 交易流水与成本预测 | 中（财务透明） | 中 |
| P3 | 数据导出 / GDPR | 低（合规补充） | 低 |

---

## 6. 验收标准

- [x] `/enterprise/audit` 能查询到最近 90 天内所有企业管理操作。
- [x] 点击成员可进入详情页，看到用量、key、日志、限额历史。
- [x] 每把 enterprise key 可查看用量、最后使用时间、变更历史。
- [x] `/enterprise/alerts` 支持历史查询和阈值配置。
- [x] usage 页面能按错误码/错误原因聚合失败请求。
- [x] billing 页面能查看交易流水和本月成本预测。

---

## 7. 备注

- 所有新增能力必须限制在「本企业数据」范围内，不得泄露其他企业信息。
- 审计日志保留策略建议：热数据 12 个月在线查询，超过 12 个月归档或仅限 super admin 查询。
- 与 system admin 的 `/admin/audit-logs` 保持字段命名一致，便于后续统一。

---

## 更新记录

- **2026-07-26 06:30 UTC** (`e862490`)：P0 Audit 筛选完成。`/enterprise/audit` 新增时间范围、操作人、目标成员筛选，CSV 导出同步支持这些条件。
- **2026-07-26 06:45 UTC** (`cdf1ad7`)：P2 Transactions 标签页完成。`/enterprise/billing` 新增 Transactions 标签，展示企业池充值、月度重置、发票扣减等交易流水。
- **2026-07-26 06:50 UTC** (`6cd5f1f`)：P2 成本预测完成。Dashboard 新增「本月预测成本」卡片。
- **2026-07-26 06:55 UTC** (`5824d23`)：P2 按成员成本分摊完成。reconciliation CSV 导出增加 `Cost %` 列，API 响应增加 `percent` 字段。
