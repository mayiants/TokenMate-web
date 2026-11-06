# Enterprise 页面完整审计报告

> 日期：2026-06-25  
> 范围：enterprise/ 下全部 7 个页面  
> 对比：admin 管理页面

---

## 一、7 个页面内容清单

### 1. Dashboard（概览）

| 状态卡片 | 数据来源 | 评估 |
|---------|---------|------|
| Balance | `CreditRemainUSD` | ✅ 核心指标 |
| Monthly Usage | `MonthlyUsage` | ✅ 核心指标 |
| Member Count / Max | `Members / MaxMembers` | ✅ 核心指标 |
| Active Keys | `ActiveKeys` | ✅ 核心指标 |
| Total Requests | 汇总自 `DailyUsage` | ✅ 核心指标 |
| Total Tokens | 汇总自 `DailyUsage` | ✅ 核心指标 |

其他内容：状态横幅（suspended/admin_vacant警告）、Credit 仪表盘、分析图表（5个tab）、本月 Top 用户、告警历史。

**评估：完整，无缺失。**

### 2. Users（成员管理）

| 状态卡片 | 评估 |
|---------|------|
| Members（总数） | ✅ |
| Active（活跃数） | ✅ |
| Administrator（管理员数） | ✅ |
| Pending Confirmation（待确认邀请） | ✅ |

功能：添加/编辑/禁用/移除成员、批量操作、邀请管理、搜索过滤。

**评估：功能完整。批量导入/导出/批量配额已实现 UI 并绑定后端 API；后端已改为事务导入、CSV 转义、`enterprise_members` 同步、audit log，详见第五节。**

### 3. Keys（密钥管理）

| 状态卡片 | 评估 |
|---------|------|
| Total Keys | ✅ |
| Active | ✅ |
| Disabled | ✅ |

功能：创建/编辑/禁用/删除/重新激活/分配成员/查看完整Key、搜索+状态筛选。

**评估：完整。**

### 4. Billing & Invoice（账单与发票）

| 状态卡片 | 评估 | 建议 |
|---------|------|------|
| Total Quota | ✅ 核心指标 | 保留 |
| Quota Used | ✅ 核心指标 | 保留 |
| Remaining Quota | ✅ 核心指标 | 保留 |
| Enterprise Pricing Ratio | ⚠️ 几乎不变 | **建议降级为信息行**（非卡片） |
| Billing Cycle | ⚠️ 几乎不变 | **建议降级为信息行**（非卡片） |
| Next Reset | ✅ 时效性信息 | 保留 |

功能：Credit 仪表盘、Billing History 表、Invoice Requests + 开票申请 + Breakdown 弹窗。

**建议：Pricing Ratio 和 Billing Cycle 改为 Credit Limit Overview 区块内的次要信息行，不占卡片位置。**

### 5. Groups（分组管理）

| 状态卡片 | 评估 |
|---------|------|
| Total Groups | ✅ |
| Platform Group（继承平台分组数） | ✅ |
| Only for administrators（管理员可见数） | ✅ |

功能：创建/编辑/禁用/删除分组、继承平台分组、配置 RPM/TPM/模型权限。

**评估：完整。**

### 6. Usage & Reconciliation（用量及对账）

| 状态卡片 | 评估 |
|---------|------|
| Input Tokens | ✅ |
| Input Cache Hit | ✅ |
| Output Tokens | ✅ |
| Total Tokens | ✅ |
| Total Requests | ✅ |
| Total Cost (micro-USD) | ✅ |

功能：趋势图（柱状图+Tokens/Requests/Cost tab）、维度明细表（Member/Model/Key tab）、月度对账汇总 + 成员钻取 + CSV 导出。

**评估：完整。**

### 7. Audit（审计日志）

| 状态卡片 | 评估 |
|---------|------|
| Audit Logs（总数） | ✅ |
| Quota Top-up | ✅ |
| Monthly Reset | ✅ |
| Admin Operation | ✅ |

功能：角色变更日志 + 导出、额度/信用变更日志 + 导出、企业申请记录。

**评估：完整。**

---

## 二、对比 Admin 功能缺失分析

| Admin 功能 | Enterprise 是否有对应 | 评估 |
|-----------|---------------------|------|
| 用户 CRUD + 角色变更 | ✅ 成员管理（增删改禁用+角色） | 对齐 |
| 分组管理 + 状态切换 | ✅ | 对齐 |
| Key Pool 管理 | ✅ 企业密钥（不同对象但功能对齐） | 对齐 |
| 审计日志 + 导出 | ✅ | 对齐 |
| Usage 多维分析 | ✅ | 对齐 |
| 对账 / Reconciliation | ✅（合入 Usage） | 对齐 |
| 发票审核 | ✅ 企业可提交申请 | 对齐（admin 审核） |
| **公告管理** | ❌ 缺失 | 🟡 企业不需要（由平台发） |
| **供应商管理** | ❌ 缺失 | 🟡 不应暴露（企业无权管理上游） |
| **模型管理** | ❌ 缺失 | 🟡 企业通过分组间接控制 |
| **优惠券/订阅** | ❌ 缺失 | 🟡 企业统一计费，无需个人订阅 |
| **监控（上游/下游）** | ❌ 缺失 | 🟡 运维功能，企业无需关心 |
| **设置页面** | ⚠️ 部分缺失 | 🟠 见下方建议 |

### 建议补充的功能

| 功能 | 优先级 | 说明 |
|------|--------|------|
| 企业设置/信息页 | P1 | 查看企业名称、联系人、认证状态、注册国家等（目前 dashboard 有编辑入口但无独立设置页） |
| 成员批量导入 | P2 | CSV 上传批量添加成员（见第四点讨论） |
| 通知/公告展示 | P3 | 企业 admin 能看到平台发布的公告（只读） ✅ 已实现（2026-06-27）：可关闭列表已展示于 `dashboard.html` 与 `enterprise/dashboard.html` |

---

## 三、状态卡片审查

### 建议修改的卡片

| 页面 | 卡片 | 问题 | 建议 |
|------|------|------|------|
| Billing | Enterprise Pricing Ratio | System Admin 设定，极少变化，不属于"状态" | 降级为 Credit Overview 区块内的文字行 |
| Billing | Billing Cycle | 同上 | 降级为 Credit Overview 区块内的文字行 |
| Usage | Total Cost (micro-USD) | 单位"micro-USD"对用户不友好 | ✅ 已实现（2026-06-27）：按企业 `display_currency` / `exchange_rate` 显示本地币种金额 |

### 保留不动的卡片

其他所有卡片都反映了**动态变化的状态指标**，适合作为状态卡片展示。

---

## 四、币种问题分析与建议

### 现状

- 用户个人页面支持 CNY/USD 切换（通过 `ExchangeRate` 和 `Currency` 设置）
- Enterprise 全部硬编码 USD
- `usage_records.total_cost` 存储为 micro-USD（百万分之一美元）
- `invoice_requests.amount` 存储为 cents（分）

### 建议方案

| 场景 | 处理方式 | 理由 |
|------|---------|------|
| **统计/报表展示** | 全部使用 USD | 企业客户跨国团队统一口径，避免汇率波动导致历史数据不一致 |
| **页面显示** | 根据企业注册国家自动选择展示币种 | 中国客户看 ¥ 数字更直观 |
| **展示实现** | 所有金额统一存 USD，前端用 `formatMoney(amount, currency, rate)` 转换显示 | 已有此函数，只需在 enterprise 模板中调用 |
| **开票** | 按客户要求的币种开具 | 中国客户开 CNY 发票，新加坡客户开 SGD/USD |
| **Invoice Request 表单** | 增加币种选择（默认 USD，可选 CNY/SGD） | 开票金额需匹配发票币种 |

### 推荐实施路径

1. **短期（不改后端）**：Enterprise 页面的金额展示调用 `formatMoney`，使用企业的 `country` 字段推断默认币种和汇率（已由中期方案替代并落地）
2. **中期**：`enterprises` 表增加 `display_currency` 和 `exchange_rate` 字段，由 System Admin 设定 ✅ 已实现（2026-06-27）
3. **长期**：接入实时汇率 API（如 Open Exchange Rates），每日更新

### 开票币种

建议在 Invoice Request 提交时让企业选择开票币种：
- 默认根据注册国家推断（中国 → CNY，新加坡 → SGD，其他 → USD）
- System Admin 在 Invoice Review 时可以看到请求的币种
- 发票金额 = USD 金额 × 对应汇率

---

## 五、批量导入导出建议

### 需求场景

- 企业有 50+ 成员需要一次性导入
- HR 系统对接时需要批量操作
- 新企业 onboarding 时一次性添加所有团队成员

### 建议实现

#### 成员批量导入（P2）

**格式**：CSV 文件
```
email,name,role
john@company.com,John Smith,member
jane@company.com,Jane Doe,admin
```

**流程**：
1. Enterprise Admin 上传 CSV
2. 后端解析并校验（邮箱格式、重复检查、角色合法性）
3. 显示预览（xx 条将导入，xx 条有问题）
4. 确认后批量创建邀请（状态为 pending，需邮箱确认）

**API**：
```
POST /api/enterprise/members/import    (multipart/form-data)
GET  /api/enterprise/members/export    (CSV download)
```

#### 成员导出

**格式**：CSV 文件
```
email,name,role,status,quota_used,quota_limit,joined_at
```

**用途**：HR 审计、财务对账、系统对接。

#### 分组批量操作

暂不建议——分组数量通常 < 10，手动管理即可。

### 安全注意事项

- 单次导入上限：500 条（防滥用）
- 导入在数据库事务中执行，失败全部回滚
- 已存在用户直接加入企业时会同步写入 `enterprise_members` 并校验是否已归属其他企业
- 导入结果记录到 audit log
- CSV 导出使用 `encoding/csv` 并附加 UTF-8 BOM，避免 Excel 乱码
- 邮箱必须验证后才能激活（邀请流程）

---

## 六、综合建议优先级

| 优先级 | 事项 | 影响范围 | 状态 |
|--------|------|---------|------|
| P0 | Billing 卡片降级（Pricing Ratio + Billing Cycle → 信息行） | UI 优化 | ✅ 已完成 |
| P0 | Usage 页面 Total Cost 单位从 micro-USD 改为 USD 显示 | 用户体验 | ✅ 已完成 |
| P1 | Enterprise 金额展示统一调用 `formatMoney`，支持币种切换 | 国际化 | ✅ 已实现（2026-06-27）：`enterprises.display_currency` / `exchange_rate` + `ExchangeService.GetRate` / `GetRateOrDefault` 已落地，dashboard / billing 按本地币种展示 |
| P1 | Invoice Request 增加币种选择字段 | 开票合规 | ✅ 已完成（USD/CNY/SGD） |
| P1 | 企业独立设置页（查看认证信息、联系人等） | 信息完整 | ✅ 已完成 |
| P2 | 成员批量导入/导出 CSV | 规模化运营 | ✅ 已实现；事务化导入、CSV 转义、`enterprise_members` 同步、导入上限 500 条 |
| P2 | 批量设置配额 | 规模化运营 | ✅ 已实现，已改为单条 SQL 批量更新 |
| P2 | 企业信息变更审批闭环 | 数据一致性 | ✅ 已新增 `enterprise_change_requests` 表、Admin 审批页、同步更新企业信息 |
| P2 | enterprises 表增加 `display_currency` 字段 | 数据模型 | ✅ 已实现（2026-06-27）：含 `exchange_rate`，admin API 与 `admin-enterprise.html` 表单已支持 |
| P3 | 平台公告只读展示 | 信息触达 | ✅ 已实现（2026-06-27）：可关闭只读公告列表已展示于 `dashboard.html` 与 `enterprise/dashboard.html` |











---

## 七、结论

Enterprise 7 个页面的功能覆盖已经与 admin 对齐（在企业管理范围内）。主要优化方向是：

1. **UI 精细度**：Billing 卡片层级区分、Cost 单位友好化
2. **国际化**：金额显示币种适配、开票币种支持
3. **规模化**：成员批量导入导出（已实现并补强数据一致性）
4. **信息展示**：币种设置、企业信息独立页
5. **变更流程**：企业信息变更申请已实现审批闭环（管理员可在 `/admin/enterprise-change-requests` 审批）

这些都属于 P1-P3 优化项，不阻塞核心功能使用，但批量导入和安全边界建议优先处理。
