# C2 前端清理进度报告：P1~P5 全部完成

> **ACTIVE** — P1~P5 全部完成，前端 JS 业务逻辑已全部归并到 Go handler，字段命名统一为 snake_case
>
> 日期: 2026-06-04 ~ 2026-06-05
> Commit: `9136d01` (P1~P3) + `859bee6` (P4~P5)

---

## 一、总体状态

| 阶段 | 内容 | 状态 | Commit |
|------|------|------|--------|
| P1 | JS 提取与统一 | ✅ 完成 | `9136d01` |
| P2 | 权限判断移到 Go handler | ✅ 完成 | `9136d01` |
| P3 | fetch() → 动态 form POST | ✅ 完成 | `9136d01` |
| P4 | 剩余页面提取 JS 到独立文件 | ✅ 完成 | `859bee6` |
| P4.a | 排查剩余 JS 权限判断 → 下沉 Go | ✅ 完成 | `859bee6` |
| P5 | camelCase → snake_case 统一 | ✅ 完成 | `859bee6` |

---

## 二、P1：JS 提取与统一

### 2.1 admin-enterprise.html JS 提取

- 提取 329 行内联 JS 到 `web/static/js/page-admin-enterprise.js`
- 模板仅保留 modal HTML 结构，脚本引用 `<script src="/static/js/page-admin-enterprise.js">`

### 2.2 window.userTimezone 统一

- `layout.html` `<head>` 中统一注入：`window.userTimezone = '{{.Timezone}}' || 'UTC'`
- 从以下 10 个模板中移除重复定义：
  - `admin-announcements.html`
  - `admin-enterprise.html`
  - `admin-groups.html`
  - `admin-invitations.html`
  - `admin-redemption.html`
  - `billing-details.html`
  - `invitation.html`
  - `invoices.html`
  - `my-subscriptions.html`
  - `orders.html`
  - `redemption.html`

---

## 三、P2：权限判断移到 Go Handler

### 3.1 apikeys key type 权限

- 模板不再用 JS 判断用户是否有 subscription/enterprise 权限
- Go handler `handleAPIKeys` 计算 `CanCreateSubscriptionKey` / `CanCreateEnterpriseKey`，传入模板
- JS 只读取模板渲染的布尔值：
  ```javascript
  var hasSubscription = '{{$d.CanCreateSubscriptionKey}}' === 'true';
  var hasEnterprise = '{{$d.CanCreateEnterpriseKey}}' === 'true';
  ```

### 3.2 groups delete 前置检查

- 模板不再用 JS 判断 `memberCount==0 && keyCount==0 && !IsDefault`
- Go handler `GetAdminGroupsData` 计算 `CanDelete bool` 字段
- 模板直接用 `{{if .CanDelete}}` 控制删除按钮显示

---

## 四、P3：fetch() → HTML Form POST

### 4.1 转换完成清单

| 页面 | 操作 | 原 fetch | 新 form action | 后端 handler |
|------|------|----------|----------------|--------------|
| admin-enterprise | suspend | `POST /api/admin/enterprise/:id/suspend` | `POST /admin/enterprise/:id/suspend` | `HandleSuspendEnterprise`（已有） |
| admin-enterprise | activate | `POST /api/admin/enterprise/:id/activate` | `POST /admin/enterprise/:id/activate` | `HandleActivateEnterprise`（已有） |
| admin-enterprise | delete | `DELETE /api/admin/enterprise/:id` | `POST /admin/enterprise/:id/delete` | `HandleDeleteEnterprise`（新增） |
| apikeys | delete key | `DELETE /api/keys/:id` | `POST /apikeys/:id/delete` | `HandleDeleteAPIKey`（已有） |
| super/providers | toggle | `POST /api/admin/providers/:id/toggle` | `POST /super/providers/:id/toggle` | `HandleToggleSuperProvider`（已有） |
| super/providers | delete | `DELETE /api/admin/providers/:id` | `POST /super/providers/:id/delete` | `HandleDeleteSuperProvider`（已有） |
| admin-keypool | delete key | `DELETE /api/admin/keypool/:id` | `POST /admin/keypool/:id/delete` | `HandleDeleteUpstreamKeyForm`（新增） |
| admin-models | delete model | `DELETE /api/admin/service-models/:id` | `POST /admin/models/:id/delete` | `HandleDeleteServiceModel`（新增） |
| admin-models | delete routing | `DELETE /api/admin/model-routing/:id` | `POST /admin/model-routing/:id/delete` | `HandleDeleteModelRouting`（新增） |
| admin-models-aws | delete AWS model | `DELETE /api/admin/service-models/:id` | `POST /admin/models/:id/delete` | `HandleDeleteServiceModel`（复用） |

### 4.2 新增后端 handler

```go
// internal/web/admin_handlers.go

HandleDeleteEnterprise        // 软删除企业 + 清理成员/密钥/邀请
HandleDeleteUpstreamKeyForm   // 删除 upstream_key + 重定向
HandleDeleteServiceModel      // 软删除 service_model
HandleDeleteModelRouting      // 删除 model_provider_routing
```

### 4.3 保留 fetch() 的复杂操作

以下操作因涉及复杂 JSON payload 或动态局部刷新，保留 `fetch()`：

| 页面 | 操作 | 原因 |
|------|------|------|
| admin-enterprise | create/edit enterprise | JSON body 含 8+ 字段 |
| admin-enterprise | topup | 条件字段（quota/credit 二选一） |
| admin-enterprise | loadMembers/invite/remove/approve/reject | 动态刷新 members 列表 |
| apikeys | create key | JSON body 含模型/组优先级数组 |
| apikeys | update key settings | PATCH JSON 含条件字段 |
| apikeys | copy key | GET 获取完整 key 值 |
| admin-subscriptions | edit plan | 表单字段多，已有 form POST 创建 |
| admin-models | create/edit model | JSON body 含 provider/model 映射 |
| admin-models | loadRoutingRules/add routing | 动态刷新路由规则列表 |
| admin-models-aws | sync-from-key | JSON body 含模型数组 |
| admin-models-aws | ignore/pause/resume | POST API 操作 |

---

## 五、验证结果

```bash
$ go build ./...
# ✅ 零错误

$ go test ./internal/web/... ./internal/db/... ./internal/query/... -count=1
ok      github.com/ai-proxy/ai-portal-go/internal/web     0.034s
ok      github.com/ai-proxy/ai-portal-go/internal/db      3.506s
?       github.com/ai-proxy/ai-portal-go/internal/query   [no test files]

$ go build ./...
# ✅ 零错误
```

---

## 六、代码统计

| 指标 | 变化 |
|------|------|
| 新增 JS 文件 | `page-admin-enterprise.js` (329 行) |
| 新增 JS 文件（P4） | `page-admin-keypool.js` (454 行)、`page-admin-users.js` (126 行)、`page-admin-subscriptions.js` (135 行)、`page-admin-providers.js` (124 行) |
| 模板内联 JS 减少 | `admin-enterprise.html` 减少 ~358 行；P4 四个页面合计减少 ~839 行 |
| 重复函数清理 | `admin-invitations.html`、`admin-redemption.html` 删除 `formatDate`/`api` 重复定义 |
| 新增 Go handler | 4 个 (`HandleDeleteEnterprise` 等) |
| 新增路由 | 4 个 POST 路由 |
| 转换 fetch() → form | 10 处 |
| 移除 camelCase 双字段 | `routes/admin.go` 9 个、`routes/groups.go` 9 个、`routes/apikeys.go` 19 个 |

---

## 七、P4：剩余页面 JS 提取到独立文件

### 7.1 提取完成清单

| 页面 | 新 JS 文件 | 原内联 JS 行数 | 说明 |
|------|-----------|---------------|------|
| admin-keypool.html | `page-admin-keypool.js` | 454 行 | 含 provider base URL、导入/导出、验证 |
| admin-users.html | `page-admin-users.js` | 126 行 | deleteUser、filterUsers、viewUser、编辑表单 |
| admin-subscriptions.html | `page-admin-subscriptions.js` | 135 行 | filterPlans、toggle/delete form、编辑 modal |
| admin-providers.html | `page-admin-providers.js` | 124 行 | 改用 `data-*` 属性注入 provider 数据 |

### 7.2 重复函数清理

| 页面 | 删除的重复函数 | 来源（已在 app-common.js） |
|------|---------------|---------------------------|
| admin-invitations.html | `formatDate`, `api` | app-common.js |
| admin-redemption.html | `formatDate`, `api` | app-common.js |

---

## 八、P4.a：排查剩余 JS 权限判断

- 扫描范围：`enterprise/*`、`admin-redemption`、`admin-subscriptions`、`admin-invitations`
- 结论：**无新增权限判断需要下沉**
  - 所有操作按钮的显示/隐藏已由 Go 模板 `{{if}}` 条件控制
  - 表单验证（如非空检查）属于 UI 体验，非业务权限

---

## 九、P5：camelCase → snake_case 字段命名统一

### 9.1 后端双字段移除

移除以下文件中的 camelCase 双字段（仅保留 snake_case）：

| 文件 | 移除的 camelCase 字段 |
|------|----------------------|
| `routes/admin.go` | `userType`, `groupId`, `groupName`, `productId`, `apiModelName`, `inputPrice`, `outputPrice`, `showInStore`, `sortOrder` |
| `routes/groups.go` | `groupType`, `billingMode`, `pricingRatio`, `rateLimitRpm`, `createdAt`, `updatedAt`, `memberCount`, `keyCount`, `routingTrack` |
| `routes/apikeys.go` | `keyPrefix`, `accessMode`, `allowedModels`, `rateLimitRpm`, `totalRequests`, `totalTokens`, `monthRequests`, `monthTokens`, `todayRequests`, `todayTokens`, `groupId`, `groupName`, `keyType`, `billingMode`, `currentModel`, `modelRoutingMode`, `selectedModels`, `groupPriority`, `createdAt` |

### 9.2 前端 fallback 清理

- `apikeys.html`：`g.groupType \/\/ g.group_type` → `g.group_type`
- `apikeys.html`：`g.id \/\/ g.groupId` → `g.id`

### 9.3 剩余单一 camelCase（暂不改动）

| 文件 | 字段 | 原因 |
|------|------|------|
| `routes/dashboard.go` | `inputTokens`, `outputTokens`, `activeUsers`, `avgLatency` | Dashboard API，前端无直接引用 |
| `routes/settings.go` | `updatedAt` | Settings API，前端无直接引用 |
| `routes/admin.go` | `enabledModels` | 内部 API，前端无直接引用 |

---

## 十、后续待办

| 优先级 | 内容 | 说明 | 预估工作量 |
|--------|------|------|-----------|
| P6 | 复杂 fetch() 逐步 HTMX 化 | 11 处保留的复杂操作（create/edit/topup/invite 等） | 2-3 天 |
| C3 | 补全测试 | 扩展 schema 测试、API 冒烟测试、CI 集成 | 2-3 天 |

> i18n 相关问题（`/admin/models-aws` body 双语残留、`favicon.ico` 404、`/admin/groups` "标准通道" 硬编码）计划待 C1/C2 全部完成后统一处理。

---

## 参考

- `frontend-refactoring-evaluation_202606041900.md` — C2 总体评估与方案
- `c1-c2-assessment-and-recommendations-202606050330.md` — C1/C2 评估与建议
- Commit `9136d01` — P1~P3 完整实现
