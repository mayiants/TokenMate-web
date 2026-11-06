# Enterprise 创建与授权机制说明

> 版本：2026-06-15  
> 适用分支：`enterprise-cardification`  
> 涉及模块：`internal/routes/enterprise_common.go`、`internal/query/enterprise.go`、`internal/web/admin_handler_common.go`、`web/templates/enterprise/dashboard.html`、`web/templates/plan-and-group.html`

---

## 1. 核心数据模型

Enterprise 的唯一标识是 `enterprises.id`（UUID），所有业务关联都通过该字段完成。

```sql
CREATE TABLE enterprises (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    contact_email VARCHAR(255),
    status VARCHAR(20) DEFAULT 'active',
    quota_total BIGINT DEFAULT 0,
    quota_used BIGINT DEFAULT 0,
    credit_total BIGINT DEFAULT 0,
    credit_used BIGINT DEFAULT 0,
    max_members INTEGER DEFAULT 50,
    max_admins INTEGER DEFAULT 3,
    pricing_ratio DOUBLE PRECISION DEFAULT 1.0,
    billing_cycle VARCHAR(20) DEFAULT 'monthly',
    allowed_domains TEXT,
    deleted_at TIMESTAMP
);
```

- `name` 与 `contact_email` 在 **未删除（`deleted_at IS NULL`）** 的企业之间有唯一索引，仅作为展示与联系用途，不作为系统识别字段。
- 用户与企业通过 `users.enterprise_id` 外键关联；成员关系通过 `enterprise_members` 表维护。

---

## 2. 授权关系

Enterprise 内的角色由两个地方共同决定：

| 层级 | 字段/表 | 说明 |
|---|---|---|
| 用户全局角色 | `users.role = 'enterprise_admin'` | 决定该用户能否进入 Enterprise Admin Console |
| 成员记录 | `enterprise_members.role = 'admin' / 'member'` | 决定在企业成员管理中的角色与权限 |

成为 Enterprise Admin 必须同时满足：

1. `users.enterprise_id = <enterprise_id>`
2. `users.role = 'enterprise_admin'`
3. `enterprise_members` 中存在有效记录且 `role = 'admin'`、`removed_at IS NULL`

> 注意：仅设置 `users.role` 或仅插入 `enterprise_members` 都不够；两者必须同步。

---

## 3. 方式一：System Admin 创建（当前已支持）

### 3.1 流程

```
System Admin 登录
    │
    ▼
/admin/enterprise 页面
    │
    ▼
填写企业名称、联系邮箱、配额等 → POST /admin/enterprise
    │
    ▼
后台执行 INSERT INTO enterprises
    │
    ▼
System Admin 在 /admin/enterprise/:id/members 邀请/添加用户
    │
    ▼
被添加用户角色设为 admin → 同步更新 users.role = 'enterprise_admin'
    │
    ▼
该用户成为 Enterprise Admin
```

### 3.2 代码路径

- Web 表单：`internal/web/admin_handler_common.go` → `HandleCreateEnterprise`
- JSON API：`internal/routes/enterprise_common.go` → `createEnterprise`
- 添加成员并授权 admin：`internal/routes/enterprise_members.go` → `addEnterpriseMember` / `HandleAddEnterpriseUser`

### 3.3 特点

- 完全由平台运营方控制，适合内部部署、销售驱动或受邀请制场景。
- 创建企业时不会自动绑定创建者；需要后续手动指定 admin。

---

## 4. 方式二：User 申请创建（参考 TokenRouter 模式）

### 4.1 TokenRouter 的交互

在 TokenRouter 的个人 Dashboard 中，直接提供 **“Create Your Organization”** 入口：

- 任何已登录用户都可以发起创建组织的申请。
- 申请通过后，该用户自动成为组织的 Super Admin / Enterprise Admin。
- 组织设置页提供 `Organization Information` 编辑和 `Danger Zone` 危险操作。

### 4.2 建议流程（未实现，供升级参考）

```
User 注册并登录
    │
    ▼
Dashboard 显示 "Create Your Organization" 入口
    │
    ▼
填写申请表单（企业名称、联系邮箱、申请理由、域名等）
    │
    ▼
POST /api/enterprise/applications
    │
    ▼
创建 enterprise_applications 记录，状态 = pending
    │
    ▼
System Admin 在 /admin/enterprise/applications 审批
    │
    ▼
批准后：
  - INSERT INTO enterprises
  - UPDATE users SET enterprise_id = ?, role = 'enterprise_admin'
  - INSERT INTO enterprise_members (enterprise_id, user_id, role = 'admin')
    │
    ▼
申请人成为 Enterprise Admin
```

### 4.3 必要的数据表（建议）

```sql
CREATE TABLE enterprise_applications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    applicant_user_id UUID REFERENCES users(id),
    proposed_name VARCHAR(255) NOT NULL,
    contact_email VARCHAR(255),
    reason TEXT,
    domain VARCHAR(255),
    status VARCHAR(20) DEFAULT 'pending', -- pending / approved / rejected
    reviewed_by UUID REFERENCES users(id),
    reviewed_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW()
);
```

### 4.4 利弊评估

| 利 | 弊 |
|---|---|
| 降低 System Admin 手动创建成本 | 需要审批工作流，防止滥用 |
| 用户转化路径更自然 | 申请人成为 admin 后可控资源较多，需身份/域名校验 |
| 与 TokenRouter、SiliconFlow 等主流平台对齐 | 同名/相似名企业冲突需要额外审核 |
| 适合规模化 toB/自助入驻 | 账单、发票、合同等法务流程需与审批状态联动 |

---

## 5. 当前已升级的解决方案

本次迭代对企业信息展示与自管理做了以下增强：

### 5.1 Enterprise Admin Dashboard 显示企业身份信息

在 `/enterprise/dashboard` 新增 **Enterprise Info** 卡片，展示：

- **Enterprise ID**：完整 UUID（唯一标识）
- **Enterprise Name**：当前企业名称
- **Contact Email**：当前联系邮箱

数据来源：`internal/query/enterprise.go` → `EnterpriseDashboardData` 新增 `ID`、`Name`、`ContactEmail` 字段，并在 `GetEnterpriseDashboardData` 中从 `enterprises` 表读取。

### 5.2 Enterprise Admin 可编辑 Name / Contact Email

- 新增 API：`PUT /api/enterprise/profile`
- 处理函数：`internal/routes/enterprise_common.go` → `updateMyEnterprise`
- 仅允许修改 `name` 与 `contact_email`，并校验唯一性。
- 前端在 dashboard 提供 inline 编辑弹窗，保存后刷新页面。

### 5.3 普通成员在 Plan-and-Group 页面看到所属企业

- 扩展 `internal/query/groups.go` → `MyGroupData`，增加 `EnterpriseID`、`EnterpriseName`。
- 在 `/plan-and-group` 页面顶部显示当前用户所属企业名称，帮助普通成员确认自己所在组织。

### 5.4 之前已修复的数据归属问题

- 企业 Dashboard、Usage、Billing、Reconciliation 等统计全部改为通过 `api_keys.enterprise_id` 限定。
- 只有 `enterprise_id IS NOT NULL` 的 key 产生的用量才计入企业账单；成员个人 key 不计入。

---

## 6. 权限校验要点

无论是 System Admin 创建还是 User 申请创建，最终都应当保证以下权限检查：

1. **Enterprise 访问校验**：`requireEnterpriseMember()` 中间件从 JWT 中取出 `enterprise_id`，确认用户属于该企业。
2. **Enterprise Admin 校验**：`requireEnterpriseAdmin()` 额外检查 `users.role = 'enterprise_admin'`。
3. **资源归属校验**：所有 usage、keys、groups、billing 查询都使用 `k.enterprise_id = $1` 或 `g.enterprise_id = $1`，而不是 `u.enterprise_id`，避免成员个人消费混入企业账单。

---

## 7. 后续可选升级

1. **实现 User 申请创建企业工作流**：新增 `enterprise_applications` 表、申请表单、审批页面。
2. **创建企业时自动绑定创建者**：System Admin 创建企业时可同时指定首个 admin，减少两步操作。
3. **企业名称/邮箱变更审计**：将 `name`、`contact_email` 变更写入 `enterprise_audit_log` 或新的 `enterprise_audit_log`。
4. **域名验证**：企业申请时验证 `contact_email` 域名与 `allowed_domains` 一致性，增强可信度。
