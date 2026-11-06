# GDPR 审计日志缺口分析与补充方案

> **生成时间**: 2026-05-28 17:00  
> **关联系统**: ai-portal-go (Portal)  
> **保留策略**: `audit_retention_years = 7` (system_settings 表)

---

## 一、当前状态

### 1.1 数据库层面

- `audit_logs` 表已存在，结构完整（含 user_id, user_email, category, action, level, message, details, ip_address, user_agent, created_at）
- `system_settings` 中已配置 `gdpr_retention_years=7` 和 `audit_retention_years=7`
- `users` 表已有 `gdpr_data_exported_at` 和 `gdpr_erasure_requested_at` 字段
- `archival_jobs` 表存在（warm→cold→cleanup 框架），但**自动化归档任务尚未接入**

### 1.2 已实现的 GDPR 数据权利接口 (`internal/routes/gdpr.go`)

| 接口 | 方法 | 功能 | Audit Log |
|------|------|------|-----------|
| `/api/gdpr/export` | GET | 导出个人数据（user + apiKeys + usageRecords + orders） | ✅ `gdpr` / `data_exported` |
| `/api/gdpr/delete` | POST | 删除个人数据（需确认码 `DELETE_MY_DATA`） | ✅ `gdpr` / `data_deleted` |
| `/api/gdpr/status` | GET | 查看导出/删除状态 | — |

**注意**: `gdpr` 分类**未加入** `admin-audit-logs.html` 的下拉筛选选项中。

### 1.3 当前已记录的审计日志分类

| category | 覆盖场景 | 记录数量 |
|----------|----------|----------|
| `auth` | 注册、登录、邀请码注册 | 3 个动作 |
| `apikeys` | 创建、更新、删除 API Key | 3 个动作 |
| `billing` | 支付完成、订阅购买/取消、发票 | 5 个动作 |
| `enterprise` | 企业成员管理、额度分配、邀请等 | 16 个动作 |
| `profile` | 资料更新 | 1 个动作 |
| `security` | 密码修改、账号删除 | 2 个动作 |
| `user` | 管理员删除用户 | 1 个动作 |
| `admin` | 修改用户角色、余额（使用 LogStructured） | 2 个动作 |
| `gdpr` | 数据导出、数据删除 | 2 个动作 |

---

## 二、GDPR 要求的关键审计日志缺口

### 2.1 P0 — 核心数据处理活动（Article 30）

#### ❌ API 推理请求（`webhook.go`）

**问题**: `internal/routes/webhook.go` 是 AI 推理请求的入口，**完全没有 audit log**。

**GDPR 关联**:
- Article 30: 必须记录"处理的目的"和"数据主体的类别"
- 用户通过 API Key 发送的 prompt 可能包含 PII（个人身份信息）
- 数据泄露时无法追溯"谁、何时、用了什么模型、消耗了多少 token"

**建议记录字段**:
```
category: "api"
action: "inference_request"
details: {user_id, api_key_id, model, provider, input_tokens, output_tokens, trace_id}
```

**注意**: 推理请求量极大（每秒可能数百次），直接写入 `audit_logs` 会导致表膨胀。建议：
- 方案 A: 仅记录异常/高额度请求（>X tokens）
- 方案 B: 写入单独的 `inference_logs` 表，生命周期短（如 90 天）
- 方案 C: 使用 `details` JSONB 字段记录关键元数据，不记录 prompt 内容

#### ❌ 登录失败（`auth.go`）

**问题**: 只记录了 `user_login`（成功），没有记录失败。

**GDPR 关联**: Article 32 — 必须记录安全事件，登录失败是暴力破解攻击的关键指标。

### 2.2 P1 — 管理员系统变更（Article 5(2) Accountability）

管理员对系统的配置变更直接影响所有用户的数据处理，**当前大量操作零日志**：

| 操作 | 文件 | 函数 | 当前状态 |
|------|------|------|----------|
| Provider 创建 | `admin.go` | `createProvider` | ❌ 无日志 |
| Provider 修改 | `admin.go` | `updateProvider` | ❌ 无日志 |
| Provider 删除 | `admin.go` | `deleteProvider` | ❌ 无日志 |
| Provider 切换状态 | `admin.go` | `toggleProvider` | ❌ 无日志 |
| Keypool 创建 | `admin.go` | `addKeyPoolKey` | ❌ 无日志 |
| Keypool 修改 | `admin.go` | `updateKeyPoolKey` | ❌ 无日志 |
| Keypool 删除 | `admin.go` | `deleteKeyPoolKey` | ❌ 无日志 |
| Keypool 导入 | `admin.go` | `importKeyPoolKeys` | ❌ 无日志 |
| Keypool 切换状态 | `admin.go` | `toggleKeyPoolKey` | ❌ 无日志 |
| 模型批准/切换 | `models.go` | `approveModel`, `toggleModel` | ❌ 无日志 |
| 系统设置创建/修改/删除 | `admin_handlers.go` | `HandleCreateSetting` 等 | ❌ 无日志 |
| Redemption code 创建/禁用/删除 | `admin_handlers.go` | `HandleCreateRedemptionCode` 等 | ❌ 无日志 |
| 审计日志导出 | `pages.go` | `handleAdminAuditLogsExport` | ❌ 无日志 |

### 2.3 P1 — 认证安全事件（Article 32）

| 操作 | 文件 | 函数 | 当前状态 |
|------|------|------|----------|
| 密码重置请求 | `auth.go` | `forgotPassword` | ❌ 无日志 |
| 密码重置完成 | `auth.go` | `resetPassword` | ❌ 无日志 |
| 登出 | — | 无相关 handler | ❌ 无日志 |

### 2.4 P2 — 数据主体权利行使

| 操作 | 当前状态 | 备注 |
|------|----------|------|
| 用户查看自己的审计日志 | ❌ 无日志 | 虽然不是严格 GDPR 要求，但建议记录 |
| 用户数据导出 | ✅ 已有 | `gdpr` / `data_exported` |
| 用户数据删除 | ✅ 已有 | `gdpr` / `data_deleted` |

### 2.5 前端筛选选项与代码实际值不一致

`admin-audit-logs.html` 下拉选项中缺少 `gdpr` 和 `admin` 分类。

---

## 三、实施方案

### 3.1 Phase 1 — 高优先级（安全 + 问责）

1. **登录失败记录** (`auth.go`)
   - 在登录验证失败时记录 `auth` / `login_failed`
   - 包含失败原因（密码错误 / 用户不存在 / 账户已删除）
   - 注意：频繁失败可能产生大量日志，建议不记录每次失败，或限制同一 IP 的失败记录频率

2. **密码重置记录** (`auth.go`)
   - `forgotPassword`: `security` / `password_reset_requested`
   - `resetPassword`: `security` / `password_reset_completed`

3. **登出记录**
   - 需要确认前端是否在登出时调用了后端 API
   - 如果前端只是清除 cookie，可以改为调用 POST `/logout` 并在后端记录

### 3.2 Phase 2 — 管理员操作（问责制）

为所有 admin 路由的关键变更操作添加 audit log。建议统一使用 `admin` category：

- `admin` / `provider_created`
- `admin` / `provider_updated`
- `admin` / `provider_deleted`
- `admin` / `provider_toggled`
- `admin` / `keypool_created`
- `admin` / `keypool_updated`
- `admin` / `keypool_deleted`
- `admin` / `keypool_imported`
- `admin` / `model_approved`
- `admin` / `model_toggled`
- `admin` / `setting_created`
- `admin` / `setting_updated`
- `admin` / `setting_deleted`
- `admin` / `redemption_created`
- `admin` / `audit_logs_exported`

### 3.3 Phase 3 — API 推理请求（Article 30）

**推荐方案 B**（写入独立表 + 短期保留）:

```sql
CREATE TABLE inference_logs (
    id BIGSERIAL PRIMARY KEY,
    user_id UUID,
    api_key_id UUID,
    model VARCHAR(100),
    provider VARCHAR(50),
    input_tokens INT,
    output_tokens INT,
    total_cost NUMERIC(18,8),
    trace_id VARCHAR(100),
    ip_address INET,
    created_at TIMESTAMP DEFAULT NOW()
);

-- 保留 90 天后自动清理（与 audit_logs 的 7 年分开）
CREATE INDEX idx_inference_logs_user_id ON inference_logs(user_id, created_at);
CREATE INDEX idx_inference_logs_created_at ON inference_logs(created_at);
```

理由：
- 推理请求量可能是 audit log 的 100~1000 倍
- GDPR 要求"可追踪"，但不需要保留 7 年（处理活动记录通常 3-5 年足够）
- 单独表可以独立设置保留策略和清理任务

如果坚持写入 `audit_logs`，建议：
- 仅记录首次使用新模型的请求
- 或仅记录异常请求（如消耗 token > 阈值）

### 3.4 Phase 4 — 前端筛选修复

在 `admin-audit-logs.html` 下拉选项中补充：
- `gdpr` → GDPR 权利行使
- `admin` → 系统管理

---

## 四、风险与建议

### 4.1 当前风险

| 风险 | 严重度 | 说明 |
|------|--------|------|
| 无法追溯 API 推理请求 | 🔴 高 | 数据泄露时无法定位影响范围 |
| 管理员操作无日志 | 🔴 高 | 系统被篡改后无法追责 |
| 登录失败无日志 | 🟡 中 | 无法检测暴力破解 |
| 审计日志无自动归档 | 🟡 中 | 热数据库长期膨胀 |

### 4.2 建议

1. **audit_logs 表当前数据量**: 先统计当前行数，评估是否需要立即分区或归档
2. **inference_logs 独立表**: 强烈建议单独建表，不要混用 `audit_logs`
3. **定期清理**: 接入 `archival_jobs` 框架，实现 audit_logs 的 warm→cold→cleanup 生命周期
4. **权限审计**: 定期审查哪些 admin 用户访问了 `/admin/audit-logs`（当前未记录管理员查看审计日志的行为）

---

## 五、附录：当前 audit_logs 表数据样本

```sql
SELECT DISTINCT category, COUNT(*) FROM audit_logs GROUP BY category ORDER BY category;
```

当前实际数据（截至 2026-05-28）：
- `apikeys`: 3 条
- `auth`: 3 条
- `billing`: 4 条
- `profile`: 1 条
