# Enterprise 自助注册功能 Review

> 日期：2026-06-24  
> 范围：commit `ec73b57` feat: enterprise self-registration and verification 及其后续 fix `2952ff8`
> 评审人：Kimi Code

---

## 1. 总体评价

功能骨架已经完整跑通：

- 用户可以提交企业注册申请
- Admin 可以查看列表、详情、通过 / 拒绝 / 要求补充材料
- 通过后自动创建企业，并把申请人设置为 `enterprise_admin`
- 前端页面、侧边栏入口、i18n、Playwright 测试均已补齐
- `go build ./...` 和 `go test ./internal/routes/...` 均通过

但代码在**路由归属、事务安全、数据一致性、服务端校验、权限边界**上存在明显问题。下文按严重度列出，并给出具体修复思路。

---

## 1.5. 修复状态更新（截至最新代码）

| 原问题 | 状态 | 备注 |
|---|---|---|
| 2.1 用户侧 API 挂在 `/api/admin/` | 🟡 部分修复 | 已在 `/api/enterprise-application` 独立挂载；`/api/admin/enterprise-application` 仍作为兼容路径保留，建议移除并完成接收器拆分 |
| 2.2 审批流程非原子 | ✅ 已修复 | `approveEnterpriseApplication` 已用 `WithTx` 包裹，失败回滚 |
| 2.3 并发重复申请 | ✅ 已修复 | 已增加 partial unique index |
| 2.4 服务端校验不足 | ✅ 已修复 | submit 已加 trim + 必填/长度校验 |
| 2.5 设计文档功能缺口 | ✅ 已修复 | 资质文件上传、7 天超时自动关闭、状态变更邮件通知均已实现 |
| 2.6 前端编辑表单未回填 | ✅ 已修复 | Step 2 编辑表单已自动回填 |
| 2.7 request-info 死代码 | ✅ 已修复 | 已删除，并补 audit log |
| 2.7 拒绝/request-info audit | ✅ 已修复 | reject / request-info 均已记 audit log |
| 邀请审批非原子 | ✅ 已修复 | `approveInvitation` 已用 `WithTx` 包裹 |
| 邀请拒绝无 audit | ✅ 已修复 | `rejectInvitation` 已补 audit log |
| 变更申请审批字段映射不全 | ✅ 已修复 | commit `61b9346`：支持全部 10 个可变更字段 |
| 变更申请不支持清空字段 | ✅ 已修复 | commit `61b9346`：允许提交空值以清空字段 |
| 企业申请编辑缺 billing 字段 | ✅ 已修复 | commit `61b9346`：编辑表单/后端/详情均补全地址和银行信息 |

---

## 2. 主要问题与解决方案

### 2.1 用户侧 API 错误挂在 `/api/admin/` 下，且属于 `AdminRoutes`

**现状**

```go
// internal/routes/admin_common.go
rg.POST("/enterprise-application", r.submitEnterpriseApplication)          // 普通用户可访问
rg.GET("/enterprise-application", r.getMyEnterpriseApplication)            // 普通用户可访问
rg.PUT("/enterprise-application/:id", r.updateEnterpriseApplication)       // 普通用户可访问
```

这些方法定义在 `enterprise_application.go` 中，但接收器是 `*AdminRoutes`。路径前缀却是 `/api/admin/...`。这导致：

- URL 语义混乱：普通用户调用的是 `/api/admin/...`
- 权限审计困难：路由分组和中间件混在一起
- 与设计文档 `docs/enterprise-self-registration-design-202606231945.md` 不一致（设计里写的是 `/api/enterprise-application`）

**解决方案**

1. 在 `internal/routes/` 中新建 `enterprise_application.go`（当前已有，但接收器需调整）或 `EnterpriseApplicationRoutes`。
2. 用户侧 API 挂载到 `/api/enterprise-application`，仅使用 `RequireAuth()`，不使用 `RequireAdmin()`。
3. Admin 侧 API 保留在 `/api/admin/enterprise-applications`，使用 `RequireAdmin()`。
4. 用户侧和 Admin 侧的方法可以共用同一个 `*AdminRoutes` / `*DB` 依赖，但逻辑上分组清晰。

**推荐目录结构**

```
internal/routes/
  enterprise_application.go      # 用户侧：submit / get / update
  admin_enterprise_application.go # Admin 侧：list / detail / approve / reject / request-info
```

或者统一放在 `enterprise_application.go` 中，但用不同 struct：

```go
type EnterpriseApplicationRoutes struct { db *db.DB; ... }
type AdminEnterpriseApplicationRoutes struct { db *db.DB; ... }
```

---

### 2.2 审批流程非原子，存在数据不一致风险

**现状**

`approveEnterpriseApplication` 分三次调用 DB：

1. `INSERT INTO enterprises`
2. `UPDATE users SET enterprise_id, role`
3. `UPDATE enterprise_applications SET status = 'approved'`

如果第 1 步成功，第 2 或 3 步失败，会产生孤儿企业或用户未关联企业的脏状态。

**解决方案**

使用数据库事务包裹整个审批流程。检查项目里 `db.DB` 是否已有事务封装：

```go
// 假设 db 已提供类似方法
err := r.db.WithTx(ctx, func(tx *sql.Tx) error {
    // 1. INSERT enterprises
    // 2. UPDATE users
    // 3. UPDATE enterprise_applications
    return nil
})
```

如果当前 `db` 没有事务封装，可临时使用 `db.Pool.Begin(ctx)`，并在 `approveEnterpriseApplication` 内部用 `pgx` 或 `database/sql` 事务完成。注意 `db.Exec` / `db.QueryRow` 当前是否统一走 `Pool`。

---

### 2.3 缺少数据库约束，同一用户可并发创建多个活跃申请

**现状**

`submitEnterpriseApplication` 先 `SELECT COUNT(*)` 再 `INSERT`：

```go
_ = r.db.QueryRow(..., `SELECT COUNT(*) FROM enterprise_applications 
    WHERE applicant_user_id = $1 AND status IN ('pending_review','needs_more_info','approved')`, ...)
```

并发场景下两条请求可能同时通过检查，导致重复申请。

**解决方案**

增加 partial unique index：

```sql
CREATE UNIQUE INDEX idx_enterprise_applications_one_active_per_user
ON enterprise_applications(applicant_user_id)
WHERE status IN ('pending_review', 'needs_more_info');
```

代码中捕获唯一约束冲突（PostgreSQL 错误码 `23505`），返回明确错误：

```go
if isUniqueViolation(err) {
    c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "You already have an active application"}})
    return
}
```

> 注意：`approved` 状态不需要包含在约束中，因为一个用户最多只能有一个 approved 申请，且 approved 后该用户已经属于某企业；但保留在业务层检查更安全。

---

### 2.4 服务端校验严重不足

**现状**

- `contact_email` 只在 HTML 层用 `type="email"` 校验，后端无校验
- `enterprise_name` 只判空，未 trim、未限长
- `country` 前端必填，后端未校验
- `contact_phone`、`registration_number` 未做格式或长度校验

**解决方案**

提交和更新时统一校验：

```go
func validateEnterpriseApplication(req *applicationRequest) error {
    req.EnterpriseName = strings.TrimSpace(req.EnterpriseName)
    req.Country = strings.TrimSpace(req.Country)
    req.ContactEmail = strings.TrimSpace(req.ContactEmail)
    req.ContactName = strings.TrimSpace(req.ContactName)

    if req.EnterpriseName == "" || len(req.EnterpriseName) > 255 {
        return errors.New("enterprise name required, max 255 chars")
    }
    if req.Country == "" || len(req.Country) > 100 {
        return errors.New("country required, max 100 chars")
    }
    if req.ContactName == "" || len(req.ContactName) > 255 {
        return errors.New("contact name required")
    }
    if req.ContactEmail != "" {
        if _, err := mail.ParseAddress(req.ContactEmail); err != nil {
            return errors.New("invalid contact email")
        }
    }
    if len(req.ContactPhone) > 100 {
        return errors.New("contact phone too long")
    }
    if len(req.RegistrationNumber) > 255 {
        return errors.New("registration number too long")
    }
    return nil
}
```

---

### 2.5 设计文档中的功能缺口

对比 `docs/enterprise-self-registration-design-202606231945.md`：

| 设计需求 | 实现状态 | 建议 |
|---|---|---|
| 资质文件上传 (`documents` JSONB) | ❌ 未实现 | 若 MVP 不需要，更新设计文档说明；若需要，增加文件上传接口（S3 / 本地存储） |
| 7 天超时自动关闭 | ❌ 未实现 | 增加后台 cron / 定时任务，将 `needs_more_info` 超过 7 天的改为 `expired` |
| 状态变更邮件通知 | ❌ 未实现 | 在 approve / reject / request-info 后调用 `mail.Sender` |
| `verified_at` 后续使用 | ⚠️ 只写入，未读取 | 明确是否用于展示“已认证”徽章或权限判断 |

---

### 2.6 前端编辑表单未回填

**现状**

`enterprise-apply.html` 中点击 **Update Application** 只是切换 `display`，表单字段保持空值。用户必须重新填写所有信息。

**解决方案**

在 `showStatus(app)` 中把当前值缓存到 input：

```js
function showEditForm() {
  document.getElementById('status-view').style.display = 'none';
  document.getElementById('form-view').style.display = '';
  // 回填
  document.getElementById('f-name').value = _appData.enterprise_name || '';
  document.getElementById('f-country').value = _appData.country || '';
  // ... 其他字段
}
```

或在 `loadApplication()` 返回时直接回填表单，再把表单隐藏起来。

---

### 2.7 其他代码质量问题

1. **死代码**
   ```go
   now := time.Now().Format(time.RFC3339)
   _ = now
   ```
   出现在 `requestMoreInfoEnterpriseApplication` 中，应删除。

2. **拒绝 / 要求补充材料未记 audit**
   只有 approve 和 submit 写了 audit。建议所有 admin 操作都记录。

3. **approve 时企业名称重复无友好提示**
   `enterprises.name` 有唯一索引，若重复会 500。应捕获唯一冲突返回 409。

4. **列表未过滤已删除用户**
   `listEnterpriseApplications` 的 `JOIN users` 未加 `users.deleted_at IS NULL`。

5. **更新接口允许空更新**
   `updateEnterpriseApplication` 可以提交全空字段，实际无变化但返回 success。

---

## 3. 权限边界讨论

### 3.1 谁可以申请成为 enterprise admin？

**正确理解**：只有普通用户（`role = 'user'`）且当前**不属于任何企业**时，才能提交企业注册申请。

当前代码已做部分检查：

```go
var enterpriseID *string
_ = r.db.QueryRow(..., `SELECT enterprise_id::text FROM users WHERE id = $1`, userID).Scan(&enterpriseID)
if enterpriseID != nil && *enterpriseID != "" {
    c.JSON(http.StatusConflict, ..."already a member of an enterprise")
    return
}
```

**建议补强的检查**：

```go
var role string
var enterpriseID *string
_ = r.db.QueryRow(ctx, `SELECT role, enterprise_id::text FROM users WHERE id = $1`, userID).Scan(&role, &enterpriseID)

if role == "system_admin" || role == "super_admin" {
    // System admin 不需要走申请流程，直接创建企业
    c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Admin users cannot submit enterprise applications"}})
    return
}
if role == "enterprise_admin" {
    c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "You are already an enterprise administrator"}})
    return
}
if enterpriseID != nil && *enterpriseID != "" {
    c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "You are already a member of an enterprise"}})
    return
}
```

> 注意：如果项目里 `role` 与 `enterprise_id` 一定同步，那么只检查 `enterprise_id` 就够了；但历史上可能不同步，所以同时检查更稳健。

### 3.2 已加入某 enterprise 的 member，能否申请成为另一家 enterprise 的 admin？

**结论：当前阶段不建议提供这个能力。**

原因：

1. 当前数据模型以 `users.enterprise_id` 为核心，一个用户只能属于一个企业。
2. 允许 member 申请创建新企业，会让他同时属于两个企业，这与现有代码大量依赖 `c.GetString("enterprise_id")` 的假设冲突。
3. 这个场景属于“多企业成员”能力，需要先把 `enterprise_members` 表作为用户-企业关系的主数据源，才能安全支持。

**如果未来要支持，需要做的改造**：

- 把 `users.enterprise_id` 降级为“当前默认企业”或完全弃用
- 所有 `WHERE enterprise_id = $1` 的查询改为通过 `enterprise_members` 关联
- 路由中间件需要知道“当前访问的是哪个企业”（可能从 URL 或 session 取）
- 申请表需要指定“为哪家企业申请 admin”，而不是创建新企业

### 3.3 一个自然人能否拥有多家公司？

**现实情况**：可以。一个人可能是 A 公司、B 公司、C 公司的法人代表或授权代表。

**当前产品决策**：如果产品定位为“一个账号 = 一个企业”，那么该自然人需要注册多个账号（不同邮箱）来管理不同公司。这是许多 B2B SaaS 的常见做法，实现简单，权限清晰。

**如果产品需要支持“一个账号管理多家企业”**，则属于多企业成员架构，改造范围较大，建议作为独立项目规划。

**建议**：

- **短期（MVP）**：保持“一个用户只能属于一个企业”。在申请页面明确提示：
  > “每个账号只能申请或加入一家企业。如需管理多家企业，请使用不同账号。”
- **中长期**：评估是否值得引入多企业成员模型。如果值得，优先级应低于核心付费和权限功能。

---

## 4. 推荐修复优先级

| 优先级 | 事项 | 影响 |
|---|---|---|
| P0 | 用户侧 API 从 `/api/admin/` 迁出 | 架构正确性、权限清晰 |
| P0 | 审批流程改为事务 | 数据一致性 |
| P0 | 增加 partial unique index 防并发重复申请 | 数据完整性 |
| P1 | 增加服务端字段校验 | 安全性、数据质量 |
| P1 | 前端编辑表单回填 | 用户体验 |
| P1 | 拒绝 / 要求补充材料记 audit | 审计合规 |
| P2 | approve 企业名称重复时返回 409 | 运营体验 |
| P2 | 清理死代码、修复空更新 | 代码质量 |
| P3 | 实现文件上传、超时关闭、邮件通知 | 功能完整性（按设计文档） |
| P3 | 明确“一用户一企业”产品策略并文档化 | 产品边界 |

---

## 5. 关键代码改造示例

### 5.1 事务化审批（示意）

```go
func (r *AdminRoutes) approveEnterpriseApplication(c *gin.Context) {
    appID := c.Param("id")
    adminUserID := c.GetString("user_id")
    ctx := c.Request.Context()

    tx, err := r.db.Pool.Begin(ctx)
    if err != nil {
        r.logger.Error("begin tx failed", zap.Error(err))
        c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to process approval"}})
        return
    }
    defer tx.Rollback(ctx)

    // 1. 查询并锁定申请
    var applicantID, enterpriseName, country, contactEmail, status string
    err = tx.QueryRow(ctx, `
        SELECT applicant_user_id::text, enterprise_name, country, COALESCE(contact_email,''), status
        FROM enterprise_applications WHERE id = $1::uuid FOR UPDATE`, appID,
    ).Scan(&applicantID, &enterpriseName, &country, &contactEmail, &status)
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": gin.H{"message": "Application not found"}})
        return
    }
    if status != "pending_review" {
        c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": "Application is not pending review"}})
        return
    }

    // 2. 检查用户是否仍是普通用户且无企业
    var userRole string
    var userEnterpriseID *string
    err = tx.QueryRow(ctx, `SELECT role, enterprise_id::text FROM users WHERE id = $1::uuid`, applicantID).Scan(&userRole, &userEnterpriseID)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to verify applicant"}})
        return
    }
    if userRole != "user" || (userEnterpriseID != nil && *userEnterpriseID != "") {
        c.JSON(http.StatusConflict, gin.H{"error": gin.H{"message": "Applicant is no longer eligible"}})
        return
    }

    // 3. 创建企业
    var enterpriseID string
    err = tx.QueryRow(ctx, `
        INSERT INTO enterprises (name, contact_email, status, application_id, verified_at, created_at, updated_at)
        VALUES ($1, $2, 'active', $3::uuid, NOW(), NOW(), NOW()) RETURNING id::text`,
        enterpriseName, contactEmail, appID,
    ).Scan(&enterpriseID)
    if err != nil {
        r.logger.Error("Failed to create enterprise", zap.Error(err))
        // 可进一步判断唯一冲突
        c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to create enterprise"}})
        return
    }

    // 4. 设置用户为企业 admin
    _, err = tx.Exec(ctx, `
        UPDATE users SET enterprise_id = $1::uuid, role = 'enterprise_admin', updated_at = NOW()
        WHERE id = $2::uuid`, enterpriseID, applicantID)
    if err != nil {
        r.logger.Error("Failed to assign enterprise admin", zap.Error(err))
        c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to assign enterprise admin"}})
        return
    }

    // 5. 更新申请状态
    _, err = tx.Exec(ctx, `
        UPDATE enterprise_applications
        SET status = 'approved', reviewed_by = $1::uuid, reviewed_at = NOW(), updated_at = NOW()
        WHERE id = $2::uuid`, adminUserID, appID)
    if err != nil {
        r.logger.Error("Failed to update application status", zap.Error(err))
        c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to finalize application"}})
        return
    }

    if err := tx.Commit(ctx); err != nil {
        r.logger.Error("commit tx failed", zap.Error(err))
        c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"message": "Failed to process approval"}})
        return
    }

    r.audit.Log(ctx, adminUserID, c.GetString("email"), "admin", "enterprise_application_approved", "info",
        fmt.Sprintf("Approved enterprise application %s, created enterprise %s for user %s", appID, enterpriseName, applicantID))

    c.JSON(http.StatusOK, gin.H{"success": true, "enterprise_id": enterpriseID})
}
```

### 5.2 用户侧 API 迁移建议

在 `internal/web/handler.go` 或 `cmd/server/router.go` 中：

```go
// 用户侧
api.POST("/enterprise-application", auth.RequireAuth(), enterpriseAppRoutes.Submit)
api.GET("/enterprise-application", auth.RequireAuth(), enterpriseAppRoutes.GetMy)
api.PUT("/enterprise-application/:id", auth.RequireAuth(), enterpriseAppRoutes.Update)

// Admin 侧
admin := api.Group("/admin")
admin.Use(auth.RequireAdmin())
{
    admin.GET("/enterprise-applications", adminEnterpriseAppRoutes.List)
    admin.GET("/enterprise-applications/:id", adminEnterpriseAppRoutes.Detail)
    admin.POST("/enterprise-applications/:id/approve", adminEnterpriseAppRoutes.Approve)
    admin.POST("/enterprise-applications/:id/reject", adminEnterpriseAppRoutes.Reject)
    admin.POST("/enterprise-applications/:id/request-info", adminEnterpriseAppRoutes.RequestMoreInfo)
}
```

---

## 6. 结论

当前实现适合作为功能原型或内部 MVP，但要在生产环境使用，必须解决：

1. 路由和权限模型不清晰
2. 审批非原子
3. 并发下可能重复申请
4. 服务端校验缺失

建议先按 P0/P1 修复，再逐步补齐设计文档中的文件上传、超时关闭、邮件通知等功能。同时明确“一个用户只能属于一个企业”的产品边界，避免后续权限模型失控。

---

## 7. 文件上传与管理方案

> 本章节回应：项目最初没有设计用户文件缓存，现在需要支持企业资质文件上传、Admin 审阅、归档，应如何扩展存储与文件管理。

### 7.1 现状

- `enterprise_applications` 表中有 `documents JSONB DEFAULT '[]'` 字段，但当前代码未使用。
- 项目 `go.mod` 中**没有**对象存储 SDK（S3 / MinIO / R2）。
- `.env.template` 和 `internal/config/config.go` 中**没有**存储相关配置。
- 当前只有 audit log 长期保留，用户上传文件属于新增数据类型，需要单独考虑 retention、备份、权限。

### 7.2 核心设计原则

1. **文件内容不存数据库** —— 只存 metadata；二进制走对象存储或受控文件系统。
2. **绝不公开直链** —— 所有下载/预览必须经过后端鉴权，使用 presigned URL 或代理流。
3. **最小权限** —— 用户只能看到自己申请的文件的 metadata；Admin 只能看到被分配到的申请的文件。
4. **可审计** —— 每次 Admin 查看、下载文件都记 audit。
5. **可配置 backend** —— 本地文件系统用于 dev/单节点；S3-compatible 用于生产。
6. **合规保留** —— 支持 soft-delete + lifecycle / retention policy，满足 KYC/企业合规归档需求。

### 7.3 存储后端选型

| 方案 | 优点 | 缺点 | 适用场景 |
|---|---|---|---|
| **本地文件系统** | 零依赖、开箱即用 | 多节点同步难、备份麻烦、容量受限 | 本地开发、单节点 MVP |
| **AWS S3** | 成熟、高可用、lifecycle 完善 | 需要 AWS 账号、依赖 SDK | 已上 AWS 的生产环境 |
| **MinIO / R2 / OSS** | S3-compatible、可私有化 | 需额外部署或配置 | 私有部署、多云 |

**推荐**：实现一个 `StorageProvider` 接口，默认本地存储，生产通过环境变量切到 S3/MinIO。这样不会把部署绑死在 AWS 上。

### 7.4 推荐架构

```
┌─────────────────┐     ┌──────────────────────┐     ┌─────────────────┐
│  Web / API      │────▶│  StorageProvider     │────▶│  Local FS / S3  │
│  (auth check)   │     │  (interface)         │     │                 │
└─────────────────┘     └──────────────────────┘     └─────────────────┘
         │
         ▼
┌─────────────────┐
│  PostgreSQL     │  enterprise_application_documents (metadata)
└─────────────────┘
```

#### 7.4.1 Storage 接口

新建 `internal/storage/storage.go`：

```go
package storage

import (
    "context"
    "io"
    "time"
)

type UploadOptions struct {
    ContentType string
    Size        int64
    Metadata    map[string]string
}

type Provider interface {
    // Upload 上传文件，key 为存储路径（如 enterprise-apps/{appID}/{uuid}-{filename}）
    Upload(ctx context.Context, key string, reader io.Reader, opts UploadOptions) error

    // Download 返回文件流，调用方负责 Close
    Download(ctx context.Context, key string) (io.ReadCloser, error)

    // Delete 删除文件
    Delete(ctx context.Context, key string) error

    // PresignURL 生成临时访问链接（用于 Admin 预览，过期时间如 5 分钟）
    PresignURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}
```

实现：

- `internal/storage/local.go` — 本地文件系统实现
- `internal/storage/s3.go` — 基于 `aws-sdk-go-v2/service/s3` 或 `minio-go` 实现

#### 7.4.2 数据库 Metadata 表

不建议复用 `enterprise_applications.documents` JSONB，因为：

- 难以做权限查询（哪些文件属于哪个申请）
- 难以统计容量、审计访问
- 难以 soft-delete 和 lifecycle 管理

推荐新增表：

```sql
CREATE TABLE enterprise_application_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id UUID NOT NULL REFERENCES enterprise_applications(id),
    original_name VARCHAR(255) NOT NULL,
    storage_key VARCHAR(512) NOT NULL UNIQUE,
    storage_provider VARCHAR(50) NOT NULL DEFAULT 'local',
    content_type VARCHAR(100) NOT NULL,
    file_size BIGINT NOT NULL,
    file_hash VARCHAR(64),              -- SHA-256，用于完整性校验和去重
    uploaded_by UUID NOT NULL REFERENCES users(id),
    uploaded_at TIMESTAMP DEFAULT NOW(),
    deleted_at TIMESTAMP,
    delete_reason TEXT,
    metadata JSONB DEFAULT '{}'         -- 可扩展：mime check、virus scan status 等
);

CREATE INDEX idx_enterprise_app_docs_application_id
ON enterprise_application_documents(application_id)
WHERE deleted_at IS NULL;
```

#### 7.4.3 配置文件扩展

`.env.template` 增加：

```bash
# ---------------------------------------------------------------------------
# FILE STORAGE
# ---------------------------------------------------------------------------
# Provider: local | s3 | minio
FILE_STORAGE_PROVIDER=local

# Local storage root (only used when FILE_STORAGE_PROVIDER=local)
FILE_STORAGE_LOCAL_ROOT=./uploads

# S3 / MinIO settings (used when FILE_STORAGE_PROVIDER=s3 or minio)
FILE_STORAGE_ENDPOINT=          # MinIO 需要填；AWS S3 留空
FILE_STORAGE_REGION=ap-southeast-1
FILE_STORAGE_BUCKET=ai-portal-uploads
FILE_STORAGE_ACCESS_KEY=
FILE_STORAGE_SECRET_KEY=
FILE_STORAGE_USE_SSL=true
FILE_STORAGE_PATH_PREFIX=enterprise-apps

# File upload limits
FILE_MAX_SIZE_MB=10
FILE_MAX_TOTAL_MB_PER_APPLICATION=50
```

`internal/config/config.go` 增加：

```go
type FileStorageConfig struct {
    Provider            string
    LocalRoot           string
    Endpoint            string
    Region              string
    Bucket              string
    AccessKey           string
    SecretKey           string
    UseSSL              bool
    PathPrefix          string
    MaxSizeMB           int64
    MaxTotalMBPerApp    int64
}
```

### 7.5 安全与访问控制

#### 7.5.1 上传阶段

- **鉴权**：必须登录；只能给自己 `pending_review` 或 `needs_more_info` 的申请上传。
- **类型白名单**：`application/pdf`, `image/jpeg`, `image/png`。
- **大小限制**：单文件 10MB，单个申请累计 50MB。
- **文件名安全**：不使用用户原始文件名作为 storage key；使用随机 UUID + 安全 suffix。
- **Magic bytes 校验**：不能只看 `Content-Type`，需校验文件头。
- **病毒扫描（可选但推荐）**：部署 ClamAV 或调用云扫描 API，结果写入 `metadata`。

#### 7.5.2 下载/预览阶段

**绝不能**直接把对象存储 URL 暴露给前端。

推荐方案 A：后端代理流（最安全，带宽走 Portal）

```go
func (r *AdminRoutes) downloadDocument(c *gin.Context) {
    docID := c.Param("docId")
    // 1. 查询 metadata
    // 2. 鉴权：用户只能看自己的；Admin 可以看所有
    // 3. 从 storage provider 读取 stream
    // 4. c.DataFromReader(...) 返回
    // 5. 记 audit：admin X downloaded doc Y of application Z
}
```

推荐方案 B：短期 presigned URL（带宽走对象存储，适合大文件）

```go
url, err := storageProvider.PresignURL(ctx, doc.StorageKey, 5*time.Minute)
// 返回 { "download_url": url, "expires_in": 300 }
// 前端通过该 URL 下载
```

建议 Admin 预览小图片/PDF 用 **presigned URL**，下载大文件也用 presigned URL；用户侧下载用 **后端代理** 更可控。

### 7.6 API 设计

#### 用户侧

```http
POST   /api/enterprise-application/:id/documents         # 上传文件（multipart/form-data）
GET    /api/enterprise-application/:id/documents         # 列出当前申请的文档 metadata
DELETE /api/enterprise-application/:id/documents/:docId  # 软删除（仅 pending_review / needs_more_info 可删）
```

#### Admin 侧

```http
GET    /api/admin/enterprise-applications/:id/documents              # 列表
GET    /api/admin/enterprise-applications/:id/documents/:docId       # 下载/预览（后端代理或 presign）
DELETE /api/admin/enterprise-applications/:id/documents/:docId       # 软删除（记录 delete_reason）
```

### 7.7 前端交互

- 用户申请表单增加文件上传组件（可多选）
- 显示已上传文件列表、大小、删除按钮
- Admin 审阅 modal 增加 “Documents” tab，可预览/下载

### 7.8 归档与生命周期

- 申请被 `approved` 或 `rejected` 后，文件至少保留 **N 天**（如 90 天或按法务要求 3~7 年）。
- 超过保留期后：
  - 数据库标记 `deleted_at`
  - 对象存储配置 lifecycle 规则自动清理（S3 Lifecycle / MinIO ILM）
- 用户账号被删除时，应触发其上传文件的清理任务（soft-delete + 异步物理删除）。

### 7.9 依赖引入建议

生产推荐 AWS SDK v2：

```bash
go get github.com/aws/aws-sdk-go-v2
go get github.com/aws/aws-sdk-go-v2/config
go get github.com/aws/aws-sdk-go-v2/service/s3
```

如果希望一个 SDK 同时支持 S3/MinIO/R2/OSS，可用 MinIO 客户端：

```bash
go get github.com/minio/minio-go/v7
```

### 7.10 最小可行实现（MVP）

如果不想引入 SDK，可先实现 **本地文件系统 + 后端代理下载**：

1. 新增 `enterprise_application_documents` 表。
2. 新增 `internal/storage/local.go`（约 100 行）。
3. 新增 4 个 API 端点（用户上传/列表/删除 + Admin 下载）。
4. 前端增加文件上传组件。
5. 所有下载记 audit。

后续切换到 S3 只需：

1. 配置环境变量 `FILE_STORAGE_PROVIDER=s3`。
2. 实现 `internal/storage/s3.go`。
3. 其他代码无需改动。

### 7.11 与现有 `documents` JSONB 列的关系

建议：

- 保留 `enterprise_applications.documents` 字段但**不写入数据**。
- 或者迁移为冗余缓存：在申请记录中保存 `document_ids` 数组，便于详情页一次性读取。
- 更推荐完全用关系表，未来可 drop 该 JSONB 列。

### 7.12 元数据与文件分离：为什么 PG 只存 metadata？

这是用户特别关心的部署与容灾问题。

#### 7.12.1 基本结论

**是的，PostgreSQL 只应存储 metadata（文件路径、大小、类型、hash、上传者等），真实文件必须放在外部存储。**

原因：

1. **Blob 不适合存 PG**：企业资质文件可能是 PDF、扫描件，长期累积会撑大 PG 表空间，影响备份、迁移、复制性能。
2. **Docker 容器是无状态的**：当前 `ai-portal-postgres` 用的是 Docker named volume，虽然数据可以持久化，但 `docker-compose down -v` 或重建环境时容易被清空。
3. **备份粒度分离**：metadata 用 `pg_dump` 即可；文件用对象存储自身的备份/版本控制。两者可以独立恢复。
4. **容灾恢复简单**：只要恢复 PG 备份，文件链接仍然有效，因为文件在外部存储中未丢失。

#### 7.12.2 你的 Docker 场景下的正确做法

当前 `docker-compose.portal.yml` 中：

```yaml
volumes:
  postgres-data:
  redis-data:
```

这种 named volume 默认在 `/var/lib/docker/volumes/...` 下，**不是最安全的企业级持久化方案**。如果整个 Docker 环境被清理，volume 也可能丢失。

##### 方案 A：本地文件系统 + 绑定挂载到宿主机（适合单节点 / 测试）

把上传目录挂载到宿主机固定路径：

```yaml
services:
  portal:
    volumes:
      - /opt/ai-portal/uploads:/app/uploads:rw
    environment:
      FILE_STORAGE_PROVIDER: local
      FILE_STORAGE_LOCAL_ROOT: /app/uploads
```

这样：

- 即使 `portal` 容器重建，`/opt/ai-portal/uploads` 仍在宿主机上。
- 只要恢复 PG 备份，`enterprise_application_documents.storage_key` 仍然指向 `/app/uploads/enterprise-apps/...`，文件可访问。

**注意事项**：

- 该目录必须定期备份（rsync / restic / 云同步）。
- 多节点部署时不适用，因为节点 A 的文件节点 B 看不到。

##### 方案 B：MinIO 作为独立 Docker 服务（推荐用于生产私有化部署）

在 `docker-compose.portal.yml` 中新增 MinIO 服务：

```yaml
services:
  minio:
    image: minio/minio:latest
    container_name: ai-portal-minio
    restart: always
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: ${FILE_STORAGE_ACCESS_KEY}
      MINIO_ROOT_PASSWORD: ${FILE_STORAGE_SECRET_KEY}
    ports:
      - "127.0.0.1:9000:9000"
      - "127.0.0.1:9001:9001"
    volumes:
      - minio-data:/data
    networks:
      - ai-platform-net

  portal:
    environment:
      FILE_STORAGE_PROVIDER: s3
      FILE_STORAGE_ENDPOINT: http://minio:9000
      FILE_STORAGE_BUCKET: ai-portal-uploads
      FILE_STORAGE_ACCESS_KEY: ${FILE_STORAGE_ACCESS_KEY}
      FILE_STORAGE_SECRET_KEY: ${FILE_STORAGE_SECRET_KEY}
      FILE_STORAGE_USE_SSL: "false"
      FILE_STORAGE_PATH_PREFIX: enterprise-apps

volumes:
  postgres-data:
  redis-data:
  minio-data:
```

MinIO 提供 S3-compatible API，代码无需两套实现。同时 MinIO 数据目录也建议绑定到宿主机或网络存储。

##### 方案 C：AWS S3 / R2 / OSS（适合公有云）

- `FILE_STORAGE_PROVIDER=s3`
- `FILE_STORAGE_ENDPOINT=` 留空（AWS）或填 R2/OSS endpoint
- 由云服务商负责多副本、备份、lifecycle

这是最高可用方案，但需要外部账号和费用。

#### 7.12.3 备份与恢复流程

假设采用 **方案 B（MinIO）**：

**日常备份**

```bash
# 1. 备份 PostgreSQL
docker exec ai-portal-postgres pg_dump -U postgres -d ai_proxy_portal > portal_$(date +%F).sql

# 2. 备份 MinIO 数据（如果挂载到宿主机 /opt/ai-portal/minio）
rsync -a /opt/ai-portal/minio/ /backup/ai-portal/minio/
```

**灾难恢复**

```bash
# 1. 启动全新 Docker Compose 环境
# 2. 恢复 PostgreSQL
cat portal_2026-06-24.sql | docker exec -i ai-portal-postgres psql -U postgres -d ai_proxy_portal

# 3. 恢复 MinIO 数据
rsync -a /backup/ai-portal/minio/ /opt/ai-portal/minio/

# 4. 重启 MinIO 服务
```

恢复后，应用从 PG 中读到的 `storage_key` 仍然能正确指向 MinIO 中的对象，文件访问不受影响。

#### 7.12.4 如果一定要把文件放在本地，必须做绑定挂载

如果你暂时不想引入 MinIO/S3，也**绝对不能**把文件放在容器内部目录（如 `/app/uploads` 不挂载）。正确做法：

```yaml
services:
  portal:
    volumes:
      - /opt/ai-portal/uploads:/app/uploads:rw
```

并在 `.env.go-platform` 中：

```bash
FILE_STORAGE_PROVIDER=local
FILE_STORAGE_LOCAL_ROOT=/app/uploads
```

这样文件实际存在于 `/opt/ai-portal/uploads`，容器删除不影响文件。

#### 7.12.5 storage_key 设计建议

为了让 metadata 与外部存储解耦，建议 `storage_key` 不要包含绝对路径：

```
enterprise-apps/{application_id}/{uuid}.{ext}
```

例如：

```
enterprise-apps/550e8400-e29b-41d4-a716-446655440000/business-license.pdf
```

`StorageProvider` 实现负责把它解析为实际位置：

- Local: `{FILE_STORAGE_LOCAL_ROOT}/enterprise-apps/.../business-license.pdf`
- S3: `s3://{bucket}/enterprise-apps/.../business-license.pdf`

这样切换存储后端时，metadata 不需要迁移。

#### 7.12.6 容灾时的一致性检查

恢复后建议运行一致性检查脚本：

```sql
SELECT d.id, d.storage_key, d.original_name
FROM enterprise_application_documents d
WHERE d.deleted_at IS NULL
  AND NOT EXISTS (
    -- 这里需要外部工具校验文件是否存在
  );
```

更实际的做法是在应用层提供一个 admin 工具：

```bash
./portal check-documents
# 输出：metadata 中存在但 storage 中缺失的文件列表
```

该工具遍历 `enterprise_application_documents`，对每个 `storage_key` 调用 `StorageProvider.Exists()` 检查。

### 7.13 归档与文件生命周期管理思路

企业资质文件属于合规敏感数据，不能流程结束后就不可见或随意删除。建议建立完整的归档与生命周期管理体系。

#### 7.13.1 当前已实现的基础

- `enterprise_application_documents` 表永久保留 metadata，不会随申请状态变化而物理删除。
- `deleted_at` 字段支持 soft-delete，用户删除只是标记，物理文件可保留备查。
- 用户侧 Step 3 状态页现在始终显示已上传文件，包括 approved/rejected/expired 状态。
- Admin 审阅 modal 中可以下载文件。

#### 7.13.2 建议的归档管理策略

##### 阶段一：保留期管理（MVP 可做到）

- 申请 approved/rejected 后，文件至少保留 **90 天 ~ 7 年**（按法务要求）。
- 通过 DB 字段 `metadata` 记录归档状态：
  ```json
  {"archived": false, "archive_bucket": "", "retention_until": "2027-06-24T00:00:00Z"}
  ```
- 超过保留期后：
  - DB 中标记 `deleted_at`
  - 物理文件通过对象存储 lifecycle 自动清理（S3 Lifecycle / MinIO ILM）

##### 阶段二：归档到冷存储（生产建议）

-  approved/rejected 后 N 天，异步任务将文件复制到低成本冷存储：
  - AWS S3 Glacier / Deep Archive
  - MinIO 冷盘池
  - 本地 NAS / 磁带备份
- 原热存储保留 30~90 天便于快速下载，之后从热存储删除，只保留冷存储副本。
- 冷存储路径同样记录在 `metadata` 中。

##### 阶段三：Admin 归档查询界面

新增 Admin 页面 `/admin/enterprise-documents`：

- 按企业名称、申请人、时间范围筛选
- 显示文件列表、大小、上传时间、关联申请 ID
- 支持打包下载（ZIP）某企业全部申请资料
- 支持标记为已归档 / 已销毁

##### 阶段四：合规与审计

- 所有 Admin 下载文件都记 audit（已实现）。
- 定期生成文件留存报告：
  ```sql
  SELECT DATE_TRUNC('month', uploaded_at) AS month, COUNT(*), SUM(file_size)
  FROM enterprise_application_documents
  WHERE deleted_at IS NULL
  GROUP BY month;
  ```
- 用户账号注销时，触发异步任务清理其上传文件（soft-delete + 物理删除）。

#### 7.13.3 推荐实现时序

| 阶段 | 功能 | 优先级 |
|---|---|---|
| 1 | 保留期字段 + soft-delete | P2 |
| 2 | 对象存储 lifecycle 策略 | P3 |
| 3 | 异步归档到冷存储 | P3 |
| 4 | Admin 归档查询与打包下载 | P3/P4 |
| 5 | 自动清理与合规报告 | P4 |

#### 7.13.4 与现有代码的衔接

- 无需改动 `StorageProvider` 接口。
- 新增 `internal/jobs/archive.go` 处理归档任务。
- 新增 `internal/routes/admin_enterprise_documents.go` 提供归档查询 API。
- 新增 `web/templates/admin-enterprise-documents.html` 归档管理页面。

---

## 8. 更新后的修复优先级
| 优先级 | 事项 | 影响 | 状态 |
|---|---|---|---|
| P0 | 用户侧 API 从 `/api/admin/` 迁出 | 架构正确性、权限清晰 | ✅ 已迁移到 /api/enterprise-application |
| P0 | 审批流程改为事务 | 数据一致性 | ✅ 已修复（WithTx） |
| P0 | 增加 partial unique index 防并发重复申请 | 数据完整性 | ✅ 已修复 |
| P1 | 增加服务端字段校验 | 安全性、数据质量 | ✅ 已修复 |
| P1 | 前端编辑表单回填 | 用户体验 | ✅ 已修复（3步向导） |
| P1 | 拒绝 / 要求补充材料记 audit | 审计合规 | ✅ 已修复 |
| P2 | approve 企业名称重复时返回 409 | 运营体验 | 延后 |
| P2 | 清理死代码、修复空更新 | 代码质量 | ✅ 已修复 |
| **P2** | **企业资质文件上传（本地存储 MVP）** | **满足设计文档 P1 需求** | ✅ 已实现 |
| P3 | 超时自动关闭、邮件通知 | 功能完整性 | ✅ 已实现（cron 12h + 邮件通知） |
| P3 | 文件存储切换为 S3/MinIO | 生产扩展性 | 延后 |
| P3 | 明确"一用户一企业"产品策略并文档化 | 产品边界 | ✅ 已文档化 |
---

## 10. 新增 Enterprise 模块相关审查发现

> 以下问题来自对最新 enterprise 页面、批量操作、设置/变更申请流程的审查。

### 10.1 Enterprise Settings 变更申请流程不完整

**现状**

`web/templates/enterprise/settings.html` 展示了一个“Request Information Change”弹窗，字段包括 name、contact_email、country。后端 `submitEnterpriseChangeRequest` 仅将申请记为 audit log，没有持久化到专门的 change_request 表，也没有 system admin 审阅/批准的入口。

同时，`updateMyEnterprise`（`PUT /api/enterprise/profile`）仍然允许 enterprise admin 直接修改企业名称和联系邮箱，**没有 verified_at 检查**，也没有走审批流程。这与 settings 页面“已认证信息需管理员批准”的提示相矛盾。

**问题**

1. 变更申请只是 audit log，无法被 admin 检索、审批、追溯。
2. 直接修改接口未被禁用，变更申请流于形式。
3. settings.html 依赖 `$d.VerifiedAt` 和 `$d.Country`，但 `getMyEnterprise` 查询没有返回这两个字段。

**建议**

1. 新增 `enterprise_change_requests` 表：
   ```sql
   CREATE TABLE enterprise_change_requests (
       id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
       enterprise_id UUID NOT NULL REFERENCES enterprises(id),
       requested_by UUID NOT NULL REFERENCES users(id),
       field VARCHAR(50) NOT NULL,
       old_value TEXT,
       new_value TEXT NOT NULL,
       reason TEXT,
       status VARCHAR(20) DEFAULT 'pending', -- pending, approved, rejected
       reviewed_by UUID REFERENCES users(id),
       reviewed_at TIMESTAMP,
       created_at TIMESTAMP DEFAULT NOW(),
       updated_at TIMESTAMP DEFAULT NOW()
   );
   ```
2. `submitEnterpriseChangeRequest` 写入该表，而不是只写 audit。
3. 当 enterprise 有 `verified_at` 时，禁用 `updateMyEnterprise` 直接修改被保护字段；只允许提交 change request。
4. Admin 后台增加 `/admin/enterprise-change-requests` 列表和审批操作。
5. 修复 `getMyEnterprise`，返回 `verified_at`、`country` 等字段。

---

### 10.2 批量成员导入导出（`enterprise_batch.go`）数据一致性问题 ✅ 已修复

> 修复 commit：`8c5c66a`。
> 对应 UI：`/enterprise/users` 新增「Import CSV / Export CSV / Batch Quota」按钮。

**现状**

`internal/routes/enterprise_batch.go` 实现了：
- `POST /enterprise/members/import` CSV 批量导入
- `GET /enterprise/members/export` CSV 导出
- `POST /enterprise/members/batch-quota` 批量设置额度

**问题**

1. **CSV 解析 bug**：`nameIdx := colMap["name"]` 后判断 `if nameIdx > 0`，如果 name 列正好在第 0 列，会被忽略。
2. **成员关系表不一致**：导入已存在用户时只更新 `users.enterprise_id`，**没有写入 `enterprise_members`** 表。但 `getMyEnterpriseMembers` 查询同时 JOIN 了 `enterprise_members`，会导致成员列表不一致。
3. **无事务保护**：导入过程中某一行失败不会回滚，可能出现部分用户已加入、部分 invitation 已发送的半完成状态。
4. **并发越限**：成员上限 `currentCount` 在内存中累计，并发导入可能超出上限。应依赖 DB 约束或事务内重新统计。
5. **安全问题**：只要邮箱存在于平台，就可以被任意 enterprise admin 拉入该企业，无需用户确认。存在账号劫持风险。
6. **CSV 导出未转义**：`exportEnterpriseMembers` 直接拼接字段，如果 name/email 包含逗号或换行，CSV 会损坏。
7. **批量额度效率低**：`batchSetMemberQuota` 逐条执行 UPDATE，应改为一次 `UPDATE ... WHERE id = ANY($1)`。

**建议**

1. 修复 name 列索引判断：使用布尔值标记列是否存在，而不是依赖索引是否大于 0。
2. 加入 enterprise 时同时写入 `enterprise_members` 表，保持与 `users.enterprise_id` 一致。
3. 整个导入过程放在一个事务中。
4. 对导入用户增加确认机制：已注册用户应收到邀请通知并确认加入，而不是直接被拉入。
5. CSV 导出使用 `encoding/csv` 写入，而不是字符串拼接。
6. 批量额度改为单条 SQL：
   ```sql
   UPDATE users SET quota_limit = $1, updated_at = NOW()
   WHERE id = ANY($2::uuid[]) AND enterprise_id = $3::uuid AND deleted_at IS NULL;
   ```

---

### 10.3 权限中间件边界问题 ✅ 已修复

> 修复 commit：`8c5c66a`。

**现状**

`enterpriseAdmin` 路由组使用 `requireEnterpriseAdmin()`，允许 `enterprise_admin`、`system_admin`、`super_admin` 通过。但 `system_admin`/`super_admin` 如果没有 enterprise，在 GET 接口中 `enterprise_id` 为空，查询会返回空结果；在带 `requireEnterpriseContext()` 的 POST/PUT 上会被拒绝。

**问题**

1. `enterpriseAdmin` 组的 GET 接口对 system/super admin 不友好（ enterprise_id 为空）。
2. `requireEnterpriseAdmin()` 和 `requireEnterpriseAccess()` 有重复逻辑，且对 admin 角色处理不一致。

**建议**

1. 明确 enterprise 自管理页面是否允许 system/super admin 访问。如果不允许，应直接拒绝；如果允许，应支持按 URL 参数指定 enterprise。
2. 合并/简化中间件，避免重复。

---

### 10.4 `users.enterprise_id` 与 `enterprise_members` 表双轨关系

**现状**

部分代码以 `users.enterprise_id` 为准（如 `deleteEnterprise`），部分代码 JOIN `enterprise_members`（如 `getMyEnterpriseMembers`）。两者可能不同步。

**问题**

- 导入成员时只更新 `users.enterprise_id`，未写 `enterprise_members`。
- `getMyEnterpriseMembers` 的 JOIN 条件 `em.enterprise_id = u.enterprise_id` 在 `enterprise_members` 缺失时会返回空。
- 删除企业时 soft-delete `enterprise_members` 但把 `users.enterprise_id` 设为 NULL，逻辑不一致。

**建议**

1. 确定主数据源：建议以 `enterprise_members` 为主，逐步弃用 `users.enterprise_id`。
2. 在所有增删成员操作处同时维护两张表，或加 trigger 保持同步。
3. 短期补丁：导入成员时同时插入 `enterprise_members`。

---

### 10.4 Enterprise 开票信息来源设计决策

> 决策时间：2026-06-24
> 相关 commit：`8624618`

**现状**

- Enterprise 侧目前**没有独立的发票信息录入 UI**。
- Enterprise 开票信息直接取自 `enterprises` 表中的以下字段：
  - `tax_number`（纳税人识别号）
  - `registered_address`（注册地址）
  - `bank_name`（开户银行）
  - `bank_account`（银行账号）
  - `contact_name` / `contact_email` / `contact_phone`（联系方式）

**设计理由**

- 当前按**中国增值税发票格式**设计，企业认证流程已统一收集上述字段。
- 海外公司开票信息格式可能与中国不同，目前无需为 enterprise 单独维护一套 invoice_profile。
- 个人/普通用户仍使用独立的 `invoice_profiles`（`/invoices` 页面维护），与 enterprise 开票信息**不混用**。

**影响**

- admin 批准企业信息变更申请 → `enterprises` 表更新 → enterprise 开票信息同步更新。
- admin 否决变更申请 → `enterprises` 表保持原值 → enterprise 开票信息也保持不变。
- 因此，变更申请的审批结果直接影响 enterprise 开票信息，user 侧需要能清晰看到 approve/reject 状态（已在 `/enterprise/settings` 的「变更申请历史」中实现）。

**未来扩展**

- 若后续需要支持海外公司非中国 VAT 格式的开票信息，再考虑新增 `/enterprise/invoices` 独立维护页面。

---

## 11. 结论

当前 Enterprise 自助注册功能已完成主流程，并补充了文件上传、三步向导 UI、批量成员操作、企业设置页面等能力。本次提交已补齐：

1. ✅ **审批事务化与并发保护**：`approveEnterpriseApplication` 与 `approveInvitation` 均改为 `WithTx` 事务。
2. ✅ **企业信息变更申请闭环**：新增 `enterprise_change_requests` 表，已认证企业不能直接修改信息，需管理员在 `/admin/enterprise-change-requests` 审批。
3. ✅ **批量导入数据一致性**：事务导入、`enterprise_members` 同步、CSV 转义导出、单条 SQL 批量配额、导入上限 500 条、audit log。
4. ✅ **权限边界**：enterprise 自管理页面和 API 不再对 system/super admin 开放；管理员统一使用 `/api/admin/enterprise/*`。
5. ✅ **变更申请审批历史可见性**：user 在 `/enterprise/settings` 可查看 approve/reject 历史。
6. ✅ **Enterprise 开票信息来源明确**：直接取自 `enterprises` 档案字段，与 `invoice_profiles` 不混用。

仍待处理：

1. ~~路由与权限模型的正确归属~~（✅ 已迁移到 /api/enterprise-application）。
2. ~~`users.enterprise_id` 与 `enterprise_members` 双轨关系治理~~（✅ 已添加 DB trigger 自动同步）。
3. ~~超时关闭、邮件通知~~（✅ 已实现）。文件 StorageProvider 抽象（长期优化）。

文件管理建议采用 **抽象 StorageProvider 接口 + 关系型 metadata 表 + 本地文件系统 MVP + S3 可切换** 的方案，既满足当前审阅需求，又为未来合规归档和私有化部署留下扩展空间。
