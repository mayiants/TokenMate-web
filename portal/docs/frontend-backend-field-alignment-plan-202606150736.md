# 前后端字段对齐检测与修复方案

> 状态：脚本已落地，首轮扫描 0 mismatch；E2E 字段回环测试已扩展至 28 个用例并全部通过；已修复 providers / models / subscriptions / vouchers / enterprise-members / models-aws / groups / announcements 等字段缺口与 update 零值覆盖问题  
> 作者：AI Assistant  
> 日期：2026-06-15 07:36 UTC（2026-06-15 12:20 UTC 更新实施结果）  
> 关联问题：`admin/enterprise` 创建企业时 `pricing_ratio` 字段前端已提交但后端未接收，导致设置 0.8 仍存为 1.0

---

## 一、问题定义

在表单/表格类页面中，经常出现以下一种或多种字段丢失场景：

| 场景 | 示例 | 后果 |
|------|------|------|
| 前端有字段，后端 struct 没接 | `pricing_ratio` 未在 `createEnterprise` 请求 struct 中声明 | 用户输入被静默忽略，DB 使用默认值 |
| 后端有字段，前端没展示 | 后端返回 `allowed_domains`，表单没提供输入框 | 功能不可用，管理员无法配置 |
| 字段名拼写不一致 | 前端 `maxUses` vs 后端 `max_uses` | JSON 绑定失败或取默认值 |
| 类型不匹配 | 前端传字符串 `"0.8"`，后端期望 `float64` | `ShouldBindJSON` 失败或归零 |
| 更新时无法区分“未传”与“置空” | `PUT` 接口把 0 当作删除 | 误改数据 |

**核心痛点：** 这类问题通常不会触发报错，只有在人工点击、核对结果时才会暴露，效率极低。

---

## 二、检测方法

### 方法 A：E2E 创建-回读测试（Roundtrip Test）⭐ 推荐

在每次表单提交后，通过 API 或数据库把刚创建的记录读回来，断言每个字段都按预期保存。

**覆盖范围：**
- 所有含 `create`/`update` 表单的页面
- 重点覆盖数值、比例、限额、币种等容易默认化的字段

**优点：**
- 能真实验证数据落库
- 直接复用现有 Playwright 环境
- 对 `pricing_ratio` 这类 Bug 100% 覆盖

**缺点：**
- 需要为每个表单写回读逻辑
- 依赖测试数据环境稳定

### 方法 B：静态字段映射扫描

编写脚本自动扫描：
1. `web/templates/**/*.html` 中的 `<input name="...">`、`<select name="...">`、`<textarea name="...">`
2. `internal/routes/**/*.go` 中对应 handler 的请求 struct JSON tag
3. 按 URL/页面分组做 diff

**输出示例：**

```text
/admin/enterprise/create
  前端字段: name, contact_email, quota_total, credit_total, max_admins,
            max_members, allowed_domains, billing_cycle, pricing_ratio
  后端字段: name, contact_email, quota_total, credit_total, max_admins,
            max_members, allowed_domains, billing_cycle
  ⚠️  缺失: pricing_ratio
```

**优点：**
- 不依赖运行时，CI 中快速执行
- 一次扫描所有页面
- 能发现前端有而后端无、后端有而前端无的双向缺口

**缺点：**
- 对 JS 动态生成的字段识别有限
- 需要维护 URL ↔ handler 映射规则
- 只能检测“字段存在性”，不保证运行时逻辑正确

### 方法 C：后端严格校验

#### C1 关键字段加 `binding:"required"`

对业务上必须显式传入的字段加 required 校验：

```go
type CreateEnterpriseRequest struct {
    Name         string  `json:"name" binding:"required"`
    PricingRatio float64 `json:"pricing_ratio"` // 即使可选也要有字段
}
```

#### C2 拒绝未知字段（可选，需谨慎）

```go
dec := json.NewDecoder(c.Request.Body)
dec.DisallowUnknownFields()
if err := dec.Decode(&req); err != nil {
    c.JSON(400, gin.H{"error": "unknown field: " + err.Error()})
    return
}
```

> ⚠️ 开启后如果前端传了后端未声明的字段会直接 400，可能误伤正常扩展字段。建议仅在核心写入接口试点。

#### C3 区分“未传”与“置零”

更新接口使用指针类型，避免把 0 当作删除：

```go
type UpdateEnterpriseRequest struct {
    PricingRatio *float64 `json:"pricing_ratio"`
}
```

### 方法 D：数据库约束兜底

对业务字段尽量 `NOT NULL`，并结合应用层显式赋值，避免依赖 DB 默认值隐藏 Bug：

```sql
pricing_ratio NUMERIC(5,4) NOT NULL DEFAULT 1.0000;
```

> 仅作为最后一道防线，不能替代字段映射校验。

### 方法 E：API 契约测试（OpenAPI / JSON Schema）

为 API 编写 OpenAPI 或 JSON Schema，前后端共享同一份契约：
- 后端用中间件校验请求/响应
- 前端表单提交前做 schema 校验
- E2E 用 schema 校验响应结构

**优点：** 最彻底，能发现字段缺失、类型错误、多余字段。  
**缺点：** 引入成本高，需要重构现有 API 文档流程。

---

## 三、推荐实施组合

结合当前项目现状（Go + html/template + Alpine/HTMX + Playwright），建议按以下组合落地：

| 优先级 | 方法 | 工作量 | 解决的问题 | 落地方式 |
|--------|------|--------|------------|----------|
| P0 | 静态字段映射扫描 | 低 | 前端有而后端无、后端有而前端无 | Python 脚本 + CI |
| P0 | E2E 创建-回读 | 中 | 字段未真正落库 | Playwright 扩展 |
| P1 | 后端关键字段 required + 指针类型 | 低 | 必填缺失、更新误删 | Go handler 改造 |
| P2 | 数据库 NOT NULL 约束 | 低 | 默认值隐藏 Bug | migration 改造 |
| P3 | OpenAPI 契约 | 高 | 全量字段一致性 | 长期规划 |

---

## 四、具体实施步骤

### Step 1：建立字段映射扫描脚本（1-2 小时）

创建文件：`scripts/scan-field-alignment.py`

**功能：**
1. 扫描 `web/templates/` 中所有表单字段
2. 扫描 `internal/routes/` 中 handler 请求 struct 的 JSON tag
3. 按 URL 输出缺失/多余字段报告
4. 在 CI 中运行，失败时输出报告并退出非零

**输入示例：**

```bash
python3 scripts/scan-field-alignment.py
```

**输出示例：**

```text
=== /admin/enterprise/create ===
Frontend fields (9): name, contact_email, quota_total, credit_total,
                     max_admins, max_members, allowed_domains,
                     billing_cycle, pricing_ratio
Backend fields  (8): name, contact_email, quota_total, credit_total,
                     max_admins, max_members, allowed_domains,
                     billing_cycle
⚠️  Missing in backend: pricing_ratio

=== /admin/groups/create ===
...

SUMMARY: 3 mismatches found
```

**扫描规则初稿：**

- 前端：匹配 `<input name="([^"]+)"`、`<select name="([^"]+)"`、`<textarea name="([^"]+)"`
- 后端：匹配 handler 函数内 `var req struct { ... }` 中每个字段的 `json:"([^"]+)"`
- 映射表：在脚本中维护一个 `url -> {template, handler}` 的字典，可逐步扩充

### Step 2：修复已知字段缺失（0.5-1 天）

根据扫描报告，逐个修复：

1. 后端 struct 加字段
2. INSERT/UPDATE SQL 加字段
3. 默认值处理（如 0 表示缺失时显式设为默认值）
4. 前端字段名核对

**已修复示例：**

```go
// internal/routes/enterprise_common.go
var req struct {
    ...
    PricingRatio float64 `json:"pricing_ratio"`
}
if req.PricingRatio == 0 {
    req.PricingRatio = 1.0
}
```

### Step 3：给 E2E 加创建-回读断言（1-2 天）

为每个 create 表单扩展 Playwright 测试：

```js
// e2e/ui-admin-enterprise.spec.js（新增或扩展现有）
test('create enterprise persists all fields', async ({ page }) => {
  await page.goto('/admin/enterprise');
  await page.click('button:has-text("创建企业")');

  // 填入唯一值，便于回读识别
  const unique = Date.now();
  await page.fill('[name="name"]', `E2E-${unique}`);
  await page.fill('[name="contact_email"]', `e2e-${unique}@test.com`);
  await page.fill('[name="pricing_ratio"]', '0.8');
  await page.selectOption('[name="billing_cycle"]', 'yearly');

  const [response] = await Promise.all([
    page.waitForResponse(r => r.url().includes('/api/admin/enterprise') && r.request().method() === 'POST'),
    page.click('button:has-text("创建")')
  ]);

  const { enterprise } = await response.json();

  // API 回读
  const read = await page.evaluate(async (id) => {
    const r = await fetch(`/api/admin/enterprise/${id}`, { credentials: 'same-origin' });
    return r.json();
  }, enterprise.id);

  expect(read.enterprise.pricing_ratio).toBe(0.8);
  expect(read.enterprise.billing_cycle).toBe('yearly');
  expect(read.enterprise.name).toBe(`E2E-${unique}`);
});
```

**建议优先覆盖的页面：**

- `/admin/enterprise` — 创建/编辑企业
- `/admin/groups` — 创建/编辑分组
- `/admin/subscriptions` — 创建/编辑套餐
- `/admin/vouchers` — 创建/编辑券码
- `/admin/models` — 创建/编辑模型
- `/admin/providers` — 创建/编辑供应商
- `/admin/keypool` — 创建/编辑上行 Key

### Step 4：后端关键字段加 required/指针改造（0.5-1 天）

- 对 create 接口的必填字段加 `binding:"required"`
- 对 update 接口的 optional 字段改用指针 `*T`，避免 0 值误删
- 统一在 handler 入口处做默认值补全

### Step 5：CI 集成（0.5 天）

在 `.github/workflows/` 或等效 CI 配置中加入：

```yaml
- name: Field alignment scan
  run: python3 scripts/scan-field-alignment.py

- name: E2E roundtrip tests
  run: npx playwright test e2e/ui-admin-*.spec.js
```

### Step 6：数据库约束补全（可选，0.5 天）

审查所有业务字段，将“必须有业务值”的列改为 `NOT NULL`：

```sql
ALTER TABLE enterprises ALTER COLUMN pricing_ratio SET NOT NULL;
```

> 注意：先确保所有 insert path 都显式赋值，否则改 NOT NULL 会触发错误。

---

## 五、成功标准

| 指标 | 目标 |
|------|------|
| 字段映射扫描 | CI 运行，0 mismatch |
| E2E 创建-回读 | 覆盖 28 个测试用例（Admin + Enterprise 页面），全部通过 |
| 后端必填字段 | 核心 create 接口必填字段 100% 加 `required` |
| 生产 Bug | 3 个月内不再出现“前端有字段、后端未接收”导致的静默数据丢失 |

---

## 六、文件清单

| 文件 | 说明 |
|------|------|
| `scripts/scan-field-alignment.py` | 字段映射扫描脚本（待创建） |
| `e2e/ui-admin-*.spec.js` | 扩展或新增创建-回读测试 |
| `internal/routes/*.go` | 按扫描报告修复 handler struct |
| `migrations/` | 必要时补 NOT NULL 约束 |

---

## 七、实施结果（2026-06-15 更新）

### 7.1 扫描脚本落地

`scripts/scan-field-alignment.py` 已实现并运行通过：

```bash
$ python3 scripts/scan-field-alignment.py
===========================================================================
Frontend ↔ Backend Field Alignment Report
===========================================================================
✅ No mismatches found.
```

脚本当前覆盖页面：

| 页面 | 前端来源 | 后端 Handler |
|------|----------|--------------|
| `/admin/enterprise` | `create-form` / `edit-form` | `createEnterprise` / `updateEnterprise` |
| `/admin/groups` | `create-form` / `submitEditGroup()` | `createGroup` / `updateGroup` |
| `/admin/group-edit` | `group-form` | `updateGroup` |
| `/admin/subscriptions` | `submitCreatePlan()` / `submitEditForm()` | `createPlan` / `updatePlan` |
| `/admin/vouchers` | `submitCreate()` / `submitEdit()` / `saveWelcome()` | `create` / `update` / `saveWelcome` |
| `/admin/keypool` | `submitAddKey()` / `submitEditKey()` | `addKeyPoolKey` / `updateKeyPoolKey` |
| `/admin/providers` | `submitAddProvider()` / `submitEditProvider()` | `createProvider` / `updateProvider` |
| `/admin/models` | `submitAddModel()` | `createServiceModel` |
| `/apikeys` | `submitCreateKey()` | `createKey` |

### 7.2 本轮修复的字段缺失

1. **供应商 `api_key` / `description` 前端提交后被丢弃**
   - 新增 migration：`migrations/202606150900_add_provider_api_key_description.sql`
   - `internal/query/admin.go`：在 `AdminProvider` 与查询中暴露 `api_key`、`description`
   - `internal/routes/admin_providers.go`：`createProvider` / `updateProvider` 接收并持久化这两个字段
   - `web/templates/admin-providers.html`：编辑抽屉回填已有值

2. **模型创建缺少 `show_in_store` / `sort_order`**
   - `web/templates/admin-models-general.html`：创建/编辑抽屉增加显示开关和排序输入
   - `submitAddModel` / `submitEditModel` 将字段提交到后端
   - `editModel` 回填当前值

3. **API Key 创建 handler 使用匿名 struct 无 JSON tag**
   - `internal/routes/apikeys_crud.go`：为 `createKey` 的请求 struct 补齐 `json` tag，便于静态扫描识别

4. **订阅套餐 `/admin/subscriptions` 速率限制字段读回缺失**
   - `internal/query/plans.go`：`AdminPlan` 与 `GetAdminPlansData` 增加 `rp5h_limit`、`rpd_limit`、`rpw_limit`、`tpd_limit`、`tpw_limit`，确保列表与详情读取完整
   - `internal/routes/subscriptions_plans.go`：`listPlans` 返回所有速率限制字段；新增 `getPlan` handler，支持通过 `GET /api/admin/subscriptions/plans/:id` 回读单条计划
   - `internal/routes/subscriptions_common.go`：注册 `GET /api/admin/subscriptions/plans/:id` 路由

5. **`/admin/models-aws` 编辑 `input_price` 后端未接收**
   - `internal/routes/models_crud.go`：`updateServiceModel` 增加 `input_price` 字段读取，并同时写入 `input_price` 与 `input_price_cache_miss`，保证 AWS 模型页输入价能落库和回显；`getServiceModel` 单条详情也返回 `input_price`。

6. **`/admin/group-edit` 是 `/admin/groups/:id/edit` 的完整表单编辑器**
   - 后端 `GET /api/admin/groups/:id` 已返回全部 10 个限流字段，E2E 通过该页面验证了编辑时所有 rate-limit 字段的持久化。

5. **`/admin/vouchers` 列表缺失 `description` / `plan_id`**
   - `internal/routes/vouchers_admin.go`：`listVouchers` 查询与返回增加 `description`、`plan_id`，确保编辑抽屉能正确回填

6. **`/enterprise/users` 列表缺失 `status`、编辑表单未提交状态且鉴权头错误**
   - `internal/routes/enterprise_common.go`：`getMyEnterpriseMembers` 查询与返回增加 `u.status`
   - `web/templates/enterprise/users.html`：修复 `submitEditUser()` 与 `toggleMemberStatus()` 错误使用 `Authorization: Bearer <csrf-token>` 的问题，改为依赖会话 Cookie 鉴权；编辑抽屉新增状态回显，保存时通过 `/api/enterprise/members/:id/toggle` 同步状态变更

### 7.3 已纳入脚本白名单的 intentional 差异

以下字段属于“后端可选/默认/由单独流程管理”，已加入 `BACKEND_ONLY_FIELDS`：

- `model_permissions`、`models`、`rate_limit_rpm`（groups 由独立页面管理）
- `notes`、`enabled`、`key`（keypool 部分字段由单独流程处理）
- `reward_type`（vouchers 后端默认）
- `access_mode`、`allowed_models`、`rate_limit_rpm`、`rate_limit_tpm`（apikeys 创建抽屉未暴露）
- `description`、`features`、`model_permissions`（subscriptions 可选字段）
- `status`（subscriptions 通过 toggle 接口切换）
- `sync_source`（providers 后端默认 `manual`）

### 7.4 Step 4A 兜底：create 接口加 `binding:"required"`

已完成核心 create 接口的关键字段必填校验：

| Handler | 已加 required 的字段 |
|---------|---------------------|
| `createGroup` | `name` |
| `createPlan` | `name`、`price`、`quota`、`duration_days` |
| `create` (voucher) | `name`、`amount` |
| `createServiceModel` | `provider`、`product_id` |
| `createEnterprise` | `name`（原本已有） |
| `addKeyPoolKey` | `provider`、`key`（原本已有） |
| `createProvider` | `id`、`name`（原本已有） |

> 说明：`update` 接口的指针改造（Step 4B）已部分实施：
> - `internal/routes/groups.go`：`updateGroup` 的 10 个限流字段改为按字段单独 `UPDATE`，避免前端只传部分字段时把未传的限流值清零。
> - `internal/routes/announcements.go`：`update` 改为动态 SQL，仅更新非空的字段；`pinned`、`show_popup`、`priority` 改为指针类型，避免部分请求时布尔/整数字段被重置为零值。
> - 其他 update 接口经审计后暂无需改造（已是指针 + 条件更新，或前端始终发送全量字段）。

### 7.5 Step 3：E2E 创建-回读测试

已实现并运行通过，测试用例从 2 个扩展到 **28 个**，覆盖创建、编辑、删除的完整字段回环：

- `e2e/ui-admin-field-roundtrip.spec.js`
  - **Admin 页面（创建 + 编辑 + 删除）**
    - `/admin/providers`：`api_key`、`description`
    - `/admin/models`：`output_price`、`show_in_store`、`sort_order`
    - `/admin/groups`：`pricing_ratio`、`routing_track`、`rpm`
    - `/admin/group-edit`：`status`、`group_type`、`billing_mode`、`pricing_ratio`、`routing_track`、`description`、全部 10 个限流字段
    - `/admin/users`：`name`、`balance`
    - `/admin/enterprise`：`pricing_ratio`、`billing_cycle`、`contact_email`、`max_members`
    - `/apikeys`：`name`
    - `/admin/subscriptions`：`price`、`daily_limit`、`tpm_limit`、`tpd_limit` 等速率/价格字段
    - `/admin/keypool`：`provider`、`rpm`、`tpm`、`quota`、`tier`、`base_url`
    - `/admin/vouchers`：code 创建/编辑（`name`、`type`、`amount`、`count`、`max_uses`、`description`、`expires_at`、`plan_id`）、welcome code、referral settings
    - `/admin/announcements`：title、content、level、start_at、end_at、sticky、audience
    - `/admin/settings`：11 项系统设置开关/输入值
    - `/admin/models-aws`：`input_price`、`output_price`、`status`
  - **Enterprise 页面（创建 + 编辑 + 删除）**
    - `/enterprise/keys`：`name`
    - `/enterprise/groups`：`name`、`tpm`
    - `/enterprise/users`：添加成员、编辑 role/status/quota_limit、移除成员

编辑场景统一使用“等待页面重载或全局提示弹窗”后再通过 API 回读，避免 `waitForResponse` 与页面导航竞态导致的偶发失败。

```bash
$ npx playwright test e2e/ui-admin-field-roundtrip.spec.js --workers=1
  28 passed (2.7m)
```

### 7.6 下一步建议

1. **Step 5：CI 集成（待实施）**

   可选方案（记录如下，暂不实现）：

   - **方案 A（轻量）**：仅将 `python3 scripts/scan-field-alignment.py` 加入 CI。
     - 优点：速度快、无需 Docker/Playwright、能 catch 前端有而后端无的字段缺口。
     - 缺点：无法验证字段真正落库。

   - **方案 B（完整）**：在 CI 中启动 `docker compose -f docker-compose.portal.yml up -d`，然后运行 `npx playwright test e2e/ui-admin-field-roundtrip.spec.js --workers=1`。
     - 优点：能发现字段未落库、后端未返回、更新时清零等运行时问题。
     - 缺点：每次约 3 分钟，需要维护测试数据清理和容器健康检查。

   - **方案 C（推荐最终形态）**：A + B 组合。
     - PR 阶段先跑静态扫描门禁，合并后 nightly / 手动触发跑全量 E2E。

   > 当前状态：CI 尚未配置，已在本文档记录三种方案，待后续根据团队 CI 资源和节奏决定实施。

2. ~~评估并实施 Step 4B：update 接口 optional 字段改用指针类型~~  
   **已完成核心风险点修复**（groups、announcements），其余 update 接口经审计暂无风险。
