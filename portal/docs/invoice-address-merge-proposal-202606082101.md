# Invoice + Address 页面合并方案

> 文档编号：`invoice-address-merge-proposal-202606082101`  
> 创建时间：2026-06-08 21:01 UTC  
> 对应分支：`enterprise-cardification`

---

## 1. 现状分析

### 1.1 页面与路由

| 页面 | 路由 | 模板文件 | 用途 |
|------|------|----------|------|
| 发票管理 | `/invoices` | `web/templates/invoices.html` | 发票抬头管理 + 发票申请记录 + 申请发票 |
| 地址管理 | `/addresses` | `web/templates/addresses.html` | 收货地址 CRUD |

### 1.2 当前代码结构

```
internal/web/pages.go
├── GET /invoices  → handleInvoices()
└── GET /addresses → handleAddresses()

internal/routes/*.go
├── /api/invoices/profiles   (GET/POST/PUT/DELETE)
├── /api/invoices/requests   (GET/POST)
└── /api/addresses           (GET/POST/PUT/DELETE)

web/templates/
├── invoices.html   (672 行)
└── addresses.html  (349 行)
```

### 1.3 现有交互关系

`/invoices` 页面在**申请发票弹窗**中其实已经调用了 `/api/addresses`：

- 当配送方式选择「纸质发票邮寄」时，显示 `<select id="req-address">`
- 下拉选项通过 `loadAddresses()` → `GET /api/addresses` 填充
- 如果用户没有地址，会提示：
  > "请先在 [地址管理](/addresses) 中添加收货地址再选择寄送地址"

这构成了事实上的**两个页面 + 一个跳转**的交互路径。

### 1.4 问题诊断

1. **冗余跳转**：申请纸质发票时必须跳到 `/addresses` 添加地址，再返回 `/invoices`，打断流程。
2. **地址页面孤立**：`/addresses` 独立存在，但唯一被业务引用的场景就是发票邮寄。
3. **重复 Country 选择器**：两个模板各自维护了一份 `countryNames` 字典，数据不同步（invoices 只列了 15 个国家，addresses 列了 30 个）。
4. **重复 Modal 代码**：删除确认弹窗、消息弹窗在两个模板中各自实现。
5. **发票页面已涵盖地址读取**：invoices.html 已经会调用 `/api/addresses`，说明合并成本低。

---

## 2. 合并目标

将 `/addresses` 页面整体并入 `/invoices`，形成一个统一的**「发票与配送地址」**页面：

- 发票抬头管理（现有）
- 发票申请记录（现有）
- 配送地址管理（从 addresses.html 迁入）
- 申请发票时，可在同一页面直接添加/选择地址，无需跳转

合并后：
- 删除 `/addresses` 路由和 `addresses.html` 模板
- 导航栏只保留「发票管理」一项
- 提升纸质发票申请的用户体验

---

## 3. 推荐合并方案

### 方案 A：标签页式（Tab）—— 推荐

在 `/invoices` 页面顶部增加两个 Tab：

```
┌────────────────────────────────────────────┐
│ 发票管理              [发票抬头] [配送地址]  │
├────────────────────────────────────────────┤
│                                            │
│  Tab 1: 发票抬头 + 申请记录 + 申请按钮      │
│  Tab 2: 地址列表 + 添加地址按钮             │
│                                            │
└────────────────────────────────────────────┘
```

**优点**：
- 结构清晰，用户一眼知道有两类信息管理
- 地址管理独立成 Tab，交互与原先 `/addresses` 页面几乎一致
- 申请发票弹窗中可以直接调用同页面的地址逻辑

**缺点**：需要为 Tab 切换写少量 JS。

### 方案 B：单页折叠式（Section）

将地址管理作为一个 Card Section 放在发票申请记录下方：

```
发票抬头
申请记录
配送地址   ← 新增 Section
```

**优点**：实现最简单，一个页面往下滚动即可看到全部。

**缺点**：页面会变长；如果地址很多，发票记录和地址管理互相干扰。

### 结论

推荐 **方案 A（标签页）**。地址与发票在业务上属于两个不同维度，用 Tab 分隔最自然，也便于未来扩展（例如再加一个「发票设置」Tab）。

---

## 4. 具体实施步骤

### Phase 1：模板合并

1. 将 `addresses.html` 中的以下内容迁移到 `invoices.html`：
   - 地址列表渲染逻辑 (`loadAddresses`, `renderAddresses`)
   - 地址添加/编辑弹窗 (`address-modal`)
   - 删除确认弹窗（复用 invoices 已有的 `delete-profile-modal` 或统一成一个通用 confirm）
   - `saveAddress`, `editAddress`, `setDefault`, `deleteAddress` 等函数

2. 在 `invoices.html` 顶部增加 Tab 导航：
   ```html
   <div class="tabs">
     <button class="tab active" data-tab="invoices" onclick="switchTab('invoices')">发票管理</button>
     <button class="tab" data-tab="addresses" onclick="switchTab('addresses')">配送地址</button>
   </div>
   ```

3. 将现有发票内容包在 `#tab-invoices`，将地址内容包在 `#tab-addresses`。

4. 统一 `countryNames` 字典到 30 国版本（以 addresses.html 为基准）。

### Phase 2：申请发票弹窗优化

将 `request-modal` 中的地址选择器改为**「下拉选择 + 快速添加」**：

```
[请选择寄送地址 ▼]
或 [+ 添加新地址]
```

点击「添加新地址」直接在同页面打开 address-modal，保存后刷新地址下拉选项。

这样用户无需离开发票申请流程。

### Phase 3：路由与导航清理

1. `internal/web/pages.go`：
   - 删除 `authPages.GET("/addresses", h.handleAddresses)`
   - 删除 `handleAddresses()` 函数

2. `web/templates/layout.html`：
   - 从侧边栏删除「地址管理」导航项

3. 删除 `web/templates/addresses.html`

### Phase 4：API 路由保留

`/api/addresses` 系列接口**全部保留**，因为 invoices.html 仍依赖它们。仅删除页面路由，不移除 API。

### Phase 5：测试与回归

- 添加/编辑/删除地址
- 设置默认地址
- 申请电子发票（邮箱送达）
- 申请纸质发票（选择地址）
- 在申请弹窗中快速添加地址
- 响应式布局检查

---

## 5. 变更清单

| 类型 | 文件/路由 | 操作 |
|------|-----------|------|
| 模板 | `web/templates/invoices.html` | 修改：增加地址 Tab 和地址管理功能 |
| 模板 | `web/templates/addresses.html` | 删除 |
| 路由 | `GET /addresses` | 删除 |
| Handler | `handleAddresses()` in `internal/web/pages.go` | 删除 |
| 导航 | `web/templates/layout.html` | 删除「地址管理」入口 |
| API | `/api/addresses` 系列 | 保留 |
| 文案 | i18n | 可能新增/调整少量标签 |

---

## 6. 风险与兼容性

| 风险 | 等级 | 缓解措施 |
|------|------|----------|
| 用户已收藏的 `/addresses` 书签失效 | 低 | 合并后添加 302 重定向：`/addresses` → `/invoices?tab=addresses` |
| 申请发票弹窗中地址逻辑变复杂 | 中 | 保持原有 `/api/addresses` 调用不变，仅把 Modal 内嵌 |
| 两个模板 JS 命名冲突（如 `addresses` 变量） | 低 | invoices.html 已有一个局部 `addresses` 变量，合并后作用域一致，无需额外改动 |
| 国家列表不一致 | 低 | 统一为 addresses.html 的 30 国字典 |

---

## 7. 决策记录

- **是否保留 `/api/addresses` 独立接口？** → 保留。invoices 页面仍通过 API 操作地址，避免后端重构。
- **是否将地址数据合并进发票 API？** → 不合并。保持 REST 资源独立，前端聚合即可。
- **是否支持在申请弹窗内直接新增地址？** → 支持。这是合并的核心体验提升点。
- **Tab 还是 Section？** → Tab。更清晰的语义边界。

---

## 8. 实施状态

✅ **已完成**。`invoices.html` 已合并地址管理 Tab：
- 顶部 Tab 导航：「发票抬头」+「配送地址」
- 申请纸质发票弹窗中可直接切换到地址 Tab 添加地址
- `/addresses` 独立页面和 `addresses.html` 模板已移除
- `/api/addresses` 系列 API 保留，invoices.html 仍依赖它们

## 9. 下一步行动

1. ✅ 按 Phase 1~5 实施模板与路由调整。
2. ✅ 回归测试发票申请与地址管理全链路。
3. 可选：添加 `/addresses` → `/invoices?tab=addresses` 的 302 重定向（当前未实施）。
