# 模板一致性改造审计报告

> 实施范围：ai-portal-go admin + super 页面模板
> 创建时间: 2026-05-26

---

## P1: 页面刷新策略

**状态**: ✅ 已完成

| 方案 | 决策 | 原因 |
|------|------|------|
| P1 方案 2（深层 JS 刷新） | ❌ 暂缓 | 后端 list API 无 LIMIT/OFFSET，无统一 JSON 接口；改动面大（11 个页面），收益小 |
| 保持 reload + 客户端分页 | ✅ 采纳 | 翻页已不 reload（JS 分页），CRUD 后 reload 是可接受的不完美 |

---

## P2: 翻页策略

### 诊断

当前系统中同时存在两种分页，导致冲突：

**服务端分页**（每页 50 条，后端 LIMIT/OFFSET + 前端 `.pagination` HTML）
- 页面：users, models-general, keypool, enterprise, audit-logs, monitor-upstream, monitor-downstream, upstream-usage
- 表现：翻页时 `window.location.reload()`，URL 带 `?page=N`

**客户端分页**（`table-pagination.js`，CSS display 切换，10/20/50 选择器）
- 页面：providers, subscriptions, groups, super/providers, super/admin-management
- 表现：无刷新翻页，支持 10/20/50 密度切换，localStorage 记忆

**冲突现场**（8 个页面同时存在两种分页）：
- 数据量 ≤10：都不显示 ✅
- 数据量 11~50：服务端分页隐藏，客户端分页显示 → 但 `.pagination-controls` 无 CSS 样式，显示为朴素白底，用户可能没注意到
- 数据量 >50：服务端分页 + 客户端分页同时显示 → 两个控件叠在一起，混乱

### 根因分析

高数据量业务必须保留服务端分页：

| 业务页面 | 数据增长特性 | 必须服务端分页 |
|---------|------------|--------------|
| admin/monitor-downstream | 每次请求一条记录，日增万级 | ✅ |
| admin/audit-logs | 全站操作记录，月增十万级 | ✅ |
| admin/users | 用户增长，长期累积 | ✅ |
| admin/keypool | 上游 Key 监控记录 | ✅ |
| admin/models | 模型数量持续增长 | ✅ |
| admin/enterprise | 企业用户增长 | ✅ |
| billing-details | 计费明细，量级最大 | ✅ |
| orders | 订单/充值记录 | ✅ |
| admin/invitations | 邀请码批量生成 | ✅ |
| admin/redemption | 兑换码批量生成 | ✅ |

低数据量页面可用客户端分页：
- providers, subscriptions, groups 等配置表，通常 < 50 条

### 修复方案：方案 C（保持混合，移除冲突）

**决策**：有服务端分页 HTML 的页面保留服务端分页，移除 `initTablePagination()`；无服务端分页的页面保留客户端分页。

**操作**：从以下 8 个页面中删除 `initTablePagination()` 调用

1. `admin-users.html`
2. `admin-models-general.html`
3. `admin-keypool.html`
4. `admin-enterprise.html`
5. `admin-audit-logs.html`
6. `admin-monitor-upstream.html`
7. `admin-monitor-downstream.html`
8. `admin-redemption.html`

**保留客户端分页的页面**（无服务端分页 HTML）
- `admin-providers.html`
- `admin-subscriptions.html`
- `admin-groups.html`
- `super/providers.html`
- `super/admin-management.html`

---

## P3: models 页面拆分

**状态**: ✅ 已完成

| 页面 | 职责 |
|------|------|
| `/admin/models` (admin-models-general.html) | 模型列表、状态管理、OpenRouter 同步、Provider 同步、路由配置 |
| `/admin/models-aws` (admin-models-aws.html) | AWS Bedrock 模型独立列表、同步预览、价格编辑 |

---

## P4: Modal 统一

**状态**: ✅ 已完成

全部 admin + super 页面已使用 `layout.html` 的 `showConfirm`/`showMessage`/`executeGlobalConfirm`。

---

## 提交记录

| Commit | 内容 |
|--------|------|
| `af3f319` | E2E 测试套件 |
| `8f3c1ff` | Modal 统一 |
| `d2951f0` | 客户端分页 table-pagination.js |
| `7a94d97` | Super 页面对齐 |
| `0e9f007` | Models 拆分为 general + AWS |
| `f3cbfd2` | AWS 模型列表 + 路由配置说明 + 下拉框修复 |

---

## P5: JavaScript 执行可靠性修复

**状态**: ✅ 已完成

### 问题现象

重启服务后，以下页面出现"加载中..." stuck 状态：

| 页面 | 受影响元素 | 表现 |
|------|-----------|------|
| `/admin/models-aws` | AWS 模型列表表格 | 始终显示"加载中..." |
| `/admin/models` (general) | 路由配置表格 | 始终显示"加载中..." |
| `/admin/models` (general) | `routing-model-filter` 下拉 | 仅显示"所有模型" |
| `/admin/settings` | 设置表单容器 | 始终显示"加载中..." |

### 根因分析

**主因：`executeGlobalConfirm is not defined`**

`layout.html` 底部（`</body>` 之前）定义了 `executeGlobalConfirm()` 函数。各 admin 页面在 `{{block "content"}}` 中的 `<script>` 里引用了 `var executeConfirm = executeGlobalConfirm;`。由于 Go template 渲染顺序，admin 页面的脚本在 `layout.html` 底部脚本**之前**执行，导致 `ReferenceError`，整个 `<script>` 块后续代码（包括所有 `loadXxx()` 初始化函数）**全部中断**。

**次因：`DOMContentLoaded` 时序不可靠**

部分页面使用 `document.addEventListener('DOMContentLoaded', ...)` 触发初始化，但当脚本位于页面底部时，事件可能已触发过，监听器不会执行。

**次因：`fetch()` 缺少 `.catch()` 错误处理**

API 请求失败时，无错误处理的分支会导致页面停留在"加载中..."状态。

### 修复方案

**方案 1：全局函数前置（已采纳）**

在 `layout.html` `<head>` 的内联脚本中定义 `executeGlobalConfirm` 占位符：
```javascript
function executeGlobalConfirm() {}
```
底部脚本中的真实定义会覆盖此占位符。所有 admin 页面在引用时不再报错。

**方案 2：`DOMContentLoaded` → `readyState` 检查（已采纳）**

将 `document.addEventListener('DOMContentLoaded', initFn)` 替换为：
```javascript
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initFn);
} else {
  initFn();
}
```

**方案 3：为所有 `fetch()` 添加 `.catch()`（已采纳）**

对无错误处理的 `fetch` 链补充 `.catch()`，在失败时将"加载中..."替换为"加载失败: xxx"。

### 修复范围

| 文件 | 修复内容 |
|------|---------|
| `layout.html` | `<head>` 添加 `executeGlobalConfirm` 占位符；底部 `DOMContentLoaded` 改为 `readyState` 检查 |
| `admin-models-aws.html` | `DOMContentLoaded` → `readyState+setTimeout` |
| `admin-models-general.html` | add-routing-modal dropdown 的 `DOMContentLoaded` → `readyState` + `.catch()` |
| `admin-settings.html` | 由 `layout.html` 修复自动解决 |
| `admin-enterprise.html` | 10 处 `fetch` 添加 `.catch()`；`loadMembers()` 添加"加载中..."初始状态 |
| `admin-users.html` | `viewUser()` `fetch` 添加 `.catch()` + "加载中..."初始状态 |
| `profile.html` | 10 个 `async` 函数添加 `try/catch` |
| `apikeys.html` | `DOMContentLoaded` → `readyState` + 3 处 `fetch` 添加 `.catch()` |
| `billing-details.html` | `DOMContentLoaded` → `readyState` |
| `enterprise/groups.html` | `DOMContentLoaded` → `readyState` |
| `admin.html` | `DOMContentLoaded` → `readyState` |
| `redemption.html` | `DOMContentLoaded` → `readyState` |

### 验证

- E2E 测试：19/19 通过
- Console 错误：从 `[ERROR] executeGlobalConfirm is not defined` → 零错误
- `/admin/settings`：从"加载中..." → 正常显示 28 项配置
- `/admin/models-aws`：从"加载中..." → 正常显示 AWS 模型列表
- `/admin/models` routing：从"加载中..." + 1 个选项 → "暂无路由规则" + 60 个模型选项

---

## P6: Provider Filter + 样式一致性 + Alert 清理

**状态**: ✅ 已完成

### 功能性 Bug 修复

| 文件 | 问题 | 修复 |
|------|------|------|
| `admin-models-aws.html` | `loadAWSModels()` 未过滤 provider，API `/admin/service-models?provider=amazon` 实际返回所有模型 | JS 前端添加 `.filter(m => m.provider === 'amazon')` |

### 样式一致性

| 文件 | 问题 | 修复 |
|------|------|------|
| `admin-invitations.html` | 4 处 inline style (`style="text-align:center;color:var(--text-muted);padding:24px"`) | 统一为 `class="text-center text-muted py-4"` |
| `admin-redemption.html` | 2 处同样 inline style | 同上 |
| `apikeys.html` | 2 处 loading state 缺少 `text-muted` | 添加 `text-muted` class |

### Alert → showMessage 统一

| 文件 | 原 `alert()` 数量 | 替换后 |
|------|------------------|--------|
| `admin-models-general.html` | 5 处 | `showMessage('错误', '...')` |
| `admin-provider-health.html` | 1 处 | `showMessage('加载失败', '...')` |

---

## 系统整体状态汇总

### ✅ 已统一

| 维度 | 状态 | 说明 |
|------|------|------|
| Modal/Toast 反馈 | ✅ | 全部 admin 页面使用 `layout.html` 的 `showMessage`/`showConfirm`/`executeGlobalConfirm` |
| CRUD 后 reload | ✅ | 创建/编辑/删除/禁用后统一 `window.location.reload()` 或局部刷新 |
| 分页策略 | ✅ | 大数据量页面保留服务端分页；小数据量页面保留客户端分页 `initTablePagination()` |
| JS 执行可靠性 | ✅ | `DOMContentLoaded` → `document.readyState` 检查已全部修复 |
| fetch 错误处理 | ✅ | 所有关键 API 调用已补充 `.catch()` 或 `try/catch` |
| `alert()` 清理 | ✅ | 全部替换为 `showMessage`/`showToast` |

### ⚠️ 仍存在的 minor 不一致（不影响功能）

1. **Empty state 文案**：有的写"暂无数据"，有的写"暂无记录"，有的写"暂无XXX" — 纯文案差异
2. **操作按钮 class**：大部分用 `btn-sm btn-secondary` 编辑、`btn-sm btn-danger` 删除，个别用 `btn-ghost`
3. **Profile 页面用 `showToast`** 而非 `showMessage` — 设计差异，合理

---

## 提交记录

| Commit | 内容 |
|--------|------|
| `af3f319` | E2E 测试套件 |
| `8f3c1ff` | Modal 统一 |
| `d2951f0` | 客户端分页 table-pagination.js |
| `7a94d97` | Super 页面对齐 |
| `0e9f007` | Models 拆分为 general + AWS |
| `f3cbfd2` | AWS 模型列表 + 路由配置说明 + 下拉框修复 |
| `b8e1aea` | 分页冲突修复（移除 8 页面 initTablePagination） |
| `f700a42` | 修复 admin 页面"加载中..." stuck（executeGlobalConfirm + DOMContentLoaded） |
| `c0bf163` | enterprise/user/profile 页面 JS 可靠性修复（fetch catch + readyState） |
| `e285199` | provider filter + 样式统一 + `alert()` 清理 |

---

*最后更新时间: 2026-05-26*
