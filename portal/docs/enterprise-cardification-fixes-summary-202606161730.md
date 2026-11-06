# Enterprise Cardification 近期修复汇总

> **ACTIVE** - 持续维护中

## 概要

本次汇总记录 2026-06-16 针对 `enterprise-cardification` 分支进行的一轮 UI/前端修复。所有修改均已在本地 Docker 环境验证并提交。

## 已修复问题

### 1. admin/groups 编辑抽屉 Model List 空白
- **问题**：编辑 Group 时，Model List 区域不显示可选 service models。
- **原因**：`/api/admin/models/grouped` 返回结构为 `{providers:[{name, models: [...]}]}`，但前端仍按旧的 `{models:[...]}` 解析。
- **修复**：`web/templates/admin-groups.html` 按 provider 结构遍历、去重、显示 `api_model_name || product_id`。
- **Commit**: `caff89e`

### 2. admin/groups 列表 Models 列始终显示 “All Models”
- **问题**：无论 Group 是否配置了模型，表格 Models 列都显示 “All Models”。
- **原因**：`GetAdminGroupsData` 用 `interface{}` 扫描 `models` 字段后手动反序列化计数，pgx 返回类型导致 `ModelCount` 始终为 0。
- **修复**：`internal/query/groups.go` 在 SQL 中用 `COALESCE(jsonb_array_length(g.models), 0)` 直接计算 `model_count`。
- **Commit**: `557b64e`

### 3. plan-and-group 页面模型名展示不全
- **问题**：Individual Group 表格的 Model 列只显示前 3 个模型，剩余用 `+N` 隐藏。
- **修复**：`web/templates/plan-and-group.html` 移除截断逻辑，列出全部模型名。
- **Commit**: `557b64e`

### 4. 模型名双击复制缺失
- **问题**：`apikeys` 页面已支持双击模型名复制，但 `plan-and-group` 和 `enterprise/groups` 页面丢失该功能。
- **修复**：在上述两个页面的模型标签上添加 `ondblclick="copyModelName(this)"`，并注入 `copyModelName` 函数。
- **Commit**: `1487bab`

### 5. admin/enterprise 成员操作后页面不刷新
- **问题**：成员邀请、移除、通过/拒绝邀请后，只刷新弹窗内列表，页面表格的成员数等状态未更新。
- **修复**：`web/templates/admin-enterprise.html` 中这些操作成功后关闭弹窗并 `window.location.reload()`。
- **Commit**: `1487bab`

### 6. enterprise/groups 禁用/启用 toggle 不生效
- **问题**：Group 卡片上的 Disable 按钮点击无效。
- **原因**：
  - 前端始终发送 `status: 'disabled'`。
  - 后端 `updateEnterpriseGroup` 不支持 `status` 更新。
  - `EnterpriseGroupItem` 未返回 `status`。
- **修复**：
  - 后端返回 `status` 并支持 PUT 切换 `active` ↔ `inactive`。
  - 前端按钮根据状态显示 Enable/Disable，点击发送相反状态。
  - 卡片增加禁用状态徽章。
- **Commit**: `b03e678`

### 7. enterprise/groups 卡片 TPM 提示过长
- **问题**：卡片显示 “Not recommended for arbitrary configuration. 0 means no limit.” 等长文案。
- **修复**：卡片标签改为 `TPM:`，并删除抽屉中过长的 warning 提示。
- **Commit**: `b03e678`

### 8. enterprise/keys 禁用 toggle 不生效
- **问题**：active 密钥的禁用按钮点击无响应。
- **原因**：按钮调用不存在的 `revokeKey`，实际函数名为 `confirmRevoke`。
- **修复**：`web/templates/enterprise/keys.html` 中将 `revokeKey` 改为 `confirmRevoke`。
- **Commit**: `b03e678`

### 9. 模拟充值成功后跳转错误
- **问题**：开启模拟充值后，充值完成不是弹窗，而是跳转到 `/api/orders?toast=...`，浏览器显示 JSON 错误。
- **原因**：`internal/routes/payment.go` 在模拟模式下把 `payment_url` 指向了 API 端点；`billing.html` 也未区分模拟模式，直接跳转。
- **修复**：
  - 后端 `/api/payment/create` 模拟模式返回 `/orders?toast=...`（前端页面）。
  - `billing.html` 检测到 `data.simulated` 后使用 `showMessage` 弹窗提示“充值成功（模拟模式）”。
- **Commit**: `bb38937`

### 10. billing 模拟充值弹窗确认后 404
- **问题**：模拟充值弹窗点击 OK 后跳转到 `/orders`，该页面不存在，返回 `{"error":{"message":"Not found"}}`。
- **修复**：`web/templates/billing.html` 弹窗回调跳转目标改为 `/billing`（充值原页面）。
- **Commit**: `da8fe32`

### 11. 充值/订阅失败未弹窗提示
- **问题**：非模拟充值无支付网关、或创建订单/订阅失败时，仅显示 toast，不符合用户期望的弹窗提示。
- **修复**：`billing.html`、`subscriptions.html`、`plan-and-group.html`、`my-subscriptions.html` 中，失败和模拟成功均使用 `showMessage` 弹窗。
- **Commit**: `1987f9e`

### 12. 随机跳回登录页
- **问题**：登录后不定时（尤其是跨天）点击页面会突然跳回 `/login`。
- **原因**：JWT 默认 30 天有效，但 cookie `Max-Age` 在未勾选“记住我”时只有 1 小时，勾选后只有 24 小时，导致浏览器在正常使用期间删除 cookie。
- **修复**：`internal/web/session.go` 将 cookie 有效期改为未勾选“记住我”7 天、勾选后 30 天，与 JWT 默认有效期对齐。
- **Commit**: `2f75529`

## 相关文档

- [i18n 默认语言重构方案](./i18n-default-language-refactor-plan-202606161630.md)

## 验证方式

- `./start.sh portal` 重新构建并启动 Docker 环境。
- 使用 Playwright 临时脚本登录 admin / enterprise_admin 账号，触发对应按钮并检查页面 reload。
- 验证后已将被测数据恢复（group/key 状态、测试账号角色）。

## E2E 测试结果

运行时间：2026-06-16

| 测试套件 | 结果 | 说明 |
|---------|------|------|
| `ui-console-error-scan.spec.js` | ✅ 37/37 passed | 所有角色页面无 console/network 错误 |
| `ui-all-pages.spec.js` | ✅ 39/39 passed | 所有页面 HTTP 200，跨角色访问控制正常 |
| `ui-smoke.spec.js` | ✅ 3/3 passed | 已修复选择器，适配新 UI |

### `ui-smoke.spec.js` 修复记录

- **问题**：创建企业/创建密钥按钮按文案匹配失败，因为页面使用 `ui_新建` 等通用文案，且存在多个同名按钮。
- **修复**：改用精确的 `onclick` 属性选择器：
  - admin/enterprise: `button[onclick="openCreateDrawer()"]`
  - enterprise/keys: `button[onclick="openCreateKeyDrawer()"]`
- **Commit**: `7efca6e`



## 同步到 `main` 的风险评估

> 评估基于 `git diff main..enterprise-cardification`，截至 commit `3d93d00`。

### 总体结论

**当前不建议直接合并到 `main`。**

`enterprise-cardification` 分支是一个长期功能分支，包含大量 enterprise、billing、rate-limit、i18n 与 UI 重构，直接合并到 `main` 属于**高风险发布**，建议按以下方式处理：

1. **先作为预发布/灰度分支**，在 staging/canary 环境跑完全量测试并验证数据迁移。
2. **修复 `ui-smoke.spec.js` 选择器**后再做一次完整 E2E 回归。
3. **确认生产环境配置**（SMTP、支付网关、JWT_SECRET 等）已就绪。
4. **数据库迁移前备份**，并验证迁移脚本顺序。

### 量化指标

| 指标 | 数值 | 说明 |
|------|------|------|
| 领先 `main` 的 commit 数 | **214** | 长期功能分支，范围很大 |
| 影响的文件数 | **289** | 涉及前后端、模板、脚本、部署配置 |
| 代码行变化 | **+64,401 / -32,092** | 大量新增与重构 |
| 与 `main` 的冲突 | **0** | 无合并冲突，但功能风险高 |
| 新增/修改的 migration | **1** | `migrations/202606150900_add_provider_api_key_description.sql` |

### 主要风险点

1. **数据库 schema 与数据兼容性**
   - 两个分支的 migration 文件数量相同（55 个），但 `enterprise-cardification` 在该分支生命周期内已经历多轮 schema 变更（enterprise、user_groups、subscription_plans、rate limits、orders 等）。
   - 如果 `main` 当前运行的生产库没有应用过这些中间 migration，直接合并且启动服务会导致表/列缺失。
   - **建议**：在生产升级前，从 `main` 的当前 schema 快照跑一遍 `enterprise-cardification` 的全部 migration，确认无报错。

2. **破坏性数据模型变更**
   - `refactor(user/group/key): remove user_type, derive access_mode from group_type`：移除了 `user_type`。
   - `i18n(group/key): align type labels to general/economy/subscription/enterprise`：group/key 类型标签变更。
   - 如果生产数据仍依赖旧字段/旧标签，升级后会出现查询/展示异常。

3. **业务功能依赖外部配置**
   - 新增/强化的企业邀请邮件、支付网关、模拟充值、优惠券、发票等功能依赖：
     - SMTP / 邮件模板
     - 支付网关配置（Alipay/WeChat/Stripe/PayPal）
     - `payment_simulation_mode` 等 system_settings
   - 缺少配置不会导致崩溃，但会让相关功能不可用或表现异常。

4. **E2E 测试未全绿**
   - `ui-smoke.spec.js` 因按钮选择器过时失败（2/3）。
   - 虽然 `ui-console-error-scan` 和 `ui-all-pages` 全绿，但核心冒烟用例失败意味着关键路径的自动化回归不足。

5. **部署配置变更**
   - `Dockerfile`、`docker-compose.portal.yml`、`start.sh` 有修改。
   - 合并后需要重新构建镜像并验证 compose 启动流程，避免 CI/CD 脚本失效。

### 已通过的基础验证

- `go test ./internal/web/...` ✅
- `go test ./internal/routes/...` ✅
- `./start.sh portal` 本地构建并启动成功 ✅
- `ui-console-error-scan.spec.js` 37/37 ✅
- `ui-all-pages.spec.js` 39/39 ✅

### 建议的同步步骤

1. 从 `main` 当前 HEAD 创建 `release/enterprise-cardification` 预发布分支。
2. 将 `enterprise-cardification` 合并到该预发布分支，跑 CI/CD 全量测试。
3. 更新 `ui-smoke.spec.js` 选择器，确保冒烟测试全绿。
4. 在 staging 环境执行 migration 并导入生产脱敏数据做灰度验证。
5. 确认 production `JWT_SECRET`、`SMTP`、`payment_gateways`、`system_settings` 已配置。
6. 选择低峰期发布：先备份 DB，再按 migration → 服务 → 验证流程上线。
7. 上线后观察 error log、支付/订阅/企业邀请等关键路径。

## 待后续处理

- ✅ i18n 中英双语键补齐：已通过脚本扫描完成，`_t()` / `{{t ...}}` 调用均已有英文翻译，模板中已无裸露中文硬编码。
- ✅ 确认弹窗变量后置重构：admin/enterprise/subscription/keys/users/provider 等 disable/enable/delete 弹窗已改为“通用描述 + `\n\n` + 变量名”模式，并移除非关键操作的 `⚠️` 图标。
- ✅ `scripts/i18n_full_audit.py` 已增强对 JS 转义（`\n`、`\'`、`\"`）与 `s.t("mail.*")` 的解析。
- ✅ 清理剩余约 **681** 个英文孤儿 key（当前孤儿 key 数为 0）。
- ✅ 迁移/清理 `app-layout.js` 内联 `_i18nMessages` 表（已清空，0 中文 key 残留）。
- ✅ 删除 `web/static/js/i18n-messages.json` 与 `web/static/js/ui-chrome.json`（确认无代码引用）。
- ✅ 邮件模板 i18n 化：`pkg/mail` 已接入 `pkg/i18n`，新增 34 个 `mail.*` key。
- ⏳ 其他页面类似的模板/交互一致性检查。
