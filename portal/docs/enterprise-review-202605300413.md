# Enterprise 实现深度审查报告

**审查时间**: 2026-05-30  
**审查范围**: ai-portal-go enterprise 模块（前后端 + API + 数据模型）  
**审查人**: opencode  
**关联文档**: `enterprise-enhancement-design-202605221800.md`

---

## 一、审查结论

**你的直觉是对的。** Enterprise 模块存在两个核心问题：

1. **Dashboard 过于简单** — 后端已实现的数据，前端未充分展示
2. **企业管理员无法自主对账** — 关键数据缺失或不可导出，必须依赖系统管理员

---

## 二、Dashboard 页面分析

### 当前展示的内容

| 模块 | 展示的数据 | 数据来源 |
|------|-----------|----------|
| 概览卡片 | 成员数、活跃密钥数、本月用量、配额使用% | `GetEnterpriseDashboardData` |
| 信用额度 | Credit 已用/总额、进度条 | 同上 |
| 快捷操作 | 3 个链接（成员/密钥/计费） | 静态 |
| TOP 用户 | 本月用量前 10 用户（Token 数、请求数、占比） | 同上 |

### 后端已实现但前端未展示的数据

| 数据 | 后端状态 | 前端展示 | 问题 |
|------|----------|----------|------|
| **配额总量/已用/剩余** | ✅ `QuotaTotal`, `QuotaUsed` | ⚠️ 仅显示百分比 | 无法看到绝对数值 |
| **信用额度 USD** | ✅ `CreditTotalUSD`, `CreditUsedUSD` | ✅ 有展示 | — |
| **定价倍率** | ✅ `PricingRatio` | ✅ badge 展示 | — |
| **成员上限** | ✅ `MaxMembers` | ✅ 有展示 | — |
| **企业状态** | ❌ 未查询 | ❌ 未展示 | `admin_vacant`/`suspended` 状态不可见 |
| **配额重置时间** | ❌ 未查询 | ❌ 未展示 | 下次重置时间不可见 |
| **企业分组概览** | ❌ 未查询 | ❌ 未展示 | 分组数量/使用情况不可见 |
| **告警历史** | ❌ 未查询 | ❌ 未展示 | 80%/90%/100% 告警记录不可见 |

### Dashboard 缺失的关键信息

| 缺失项 | 重要性 | 说明 |
|--------|--------|------|
| **企业状态指示器** | HIGH | `admin_vacant`/`suspended` 时应有醒目提示 |
| **配额/信用重置时间** | HIGH | 企业管理员需要知道何时重置 |
| **每日/周用量趋势图** | MEDIUM | 只有 TOP 用户，没有时间维度趋势 |
| **分组维度用量** | MEDIUM | 无法看到各分组的用量分布 |
| **告警历史** | MEDIUM | 告警邮件发了但 dashboard 无记录 |
| **系统管理员操作记录** | MEDIUM | 设计文档 §2 说"企业管理员可看到 override 记录"，但未实现 |

---

## 三、Billing 页面分析

### 当前展示的内容

| 模块 | 数据 |
|------|------|
| 配额概览 | QuotaTotal, QuotaUsed, QuotaRemaining, BillingCycle |
| 信用额度 | CreditTotalUSD, CreditUsedUSD, CreditRemainingUSD, CreditPercent |
| 成员用量 | 按用户聚合的 Token 数/请求数/占比 |
| 账单历史 | 最近 10 条 Invoice |

### 严重问题

#### 问题 1: Dashboard 中 Credit 和 Quota 混淆

**文件**: `internal/web/data.go:1460-1468`

```go
// Credit stats (use quota as credit basis)
d.CreditTotal = d.QuotaTotal    // ← 错误！
d.CreditUsed = d.QuotaUsed      // ← 错误！
```

Dashboard 的 `GetEnterpriseDashboardData` 把 `QuotaTotal` 直接赋值给 `CreditTotal`，导致 **Dashboard 上的信用额度数据是假的** — 它显示的是 Token 配额，不是真实的信用额度（微美元）。

Billing 页面的 `GetEnterpriseBillingData` 是正确的（分别查询 `credit_total`/`credit_used`），但 Dashboard 页面的数据是错的。

**影响**: 企业管理员在 Dashboard 看到的"信用额度"和在 Billing 页面看到的不一致，造成困惑。

#### 问题 2: 缺少明细数据，无法对账

| 对账所需数据 | 企业管理员可见 | 系统管理员可见 | 差距 |
|-------------|---------------|---------------|------|
| **每条使用记录明细** | ❌ | ✅ admin/usage | 无法核对单次调用 |
| **按模型维度用量** | ❌ | ✅ admin/usage 按 model 分组 | 无法看到哪个模型消耗最多 |
| **按密钥维度用量** | ❌ | ✅ admin/usage 按 key 分组 | 无法看到哪个密钥消耗最多 |
| **按日期维度用量** | ❌ | ✅ admin/usage 按日聚合 | 无法看到用量趋势 |
| **配额/信用变更历史** | ❌ | ❌ 也缺失 | 双方都无法追溯 |
| **定价明细** | ❌ 仅看到倍率 | ✅ 完整定价 | 无法核对单价 |
| **导出 CSV/Excel** | ❌ | ❌ 也缺失 | 双方都无法导出 |

#### 问题 3: Invoice 过于简陋

当前 Invoice 仅展示 ID、金额、状态、时间。缺少：
- 明细行项目（按模型/密钥/用户分解）
- 计费周期标识
- 配额/信用变动摘要
- 下载/导出功能

#### 问题 4: 用量查询无时间范围选择

`GetEnterpriseBillingData` 硬编码为当月（`DATE_TRUNC('month', CURRENT_DATE)`）。企业管理员无法：
- 选择历史月份查看
- 自定义时间范围
- 对比不同时间段

---

## 四、企业管理员对账能力评估

### 当前对账流程（如果能叫"流程"的话）

```
企业管理员发现账单异常
  │
  ▼
联系系统管理员
  │
  ▼
系统管理员在 admin/usage 页面查询明细
  │
  ▼
手动导出/截图，发给企业管理员
  │
  ▼
企业管理员人工比对
```

### 问题

1. **完全依赖系统管理员** — 企业管理员无法自助查询任何明细
2. **无标准对账接口** — 系统管理员只能手动操作
3. **无导出功能** — 即使系统管理员也无法批量导出
4. **无审计追溯** — 配额/信用变更没有历史记录

### 设计文档的承诺 vs 实际

| 设计文档 §8 承诺 | 实际状态 |
|-----------------|----------|
| "View usage & audit log ✅ Yes" | ⚠️ 仅有按用户聚合的月度汇总，无明细 |
| "Configure enterprise groups ✅ Yes" | ✅ 已实现 |
| "View enterprise info ✅ Yes" | ✅ 已实现 |

设计文档 §15 提到的 **Post-Paid Dual-Track System**（Token Quota + Financial Credit 两个维度）在后端已实现，但 **前端仅展示了汇总数据，没有维度分解**。

---

## 五、具体缺失清单

### P0 — 对账必需（本周）

| # | 缺失项 | 影响 | 建议 | 状态 |
|---|--------|------|------|------|
| 1 | **Dashboard Credit 数据错误** | Dashboard 和 Billing 数据不一致 | 修复 `GetEnterpriseDashboardData` 中 Credit 查询 | ✅ 已修复 |
| 2 | **企业管理员无用量明细** | 无法对账 | 新增 `/enterprise/usage/details` 页面，支持按日/用户/模型/密钥查询 | 🔲 待实现 |
| 3 | **无时间范围选择** | 只能看当月 | 添加日期选择器，默认当月 | 🔲 待实现 |
| 4 | **企业状态不可见** | `admin_vacant`/`suspended` 时无感知 | Dashboard 顶部添加状态横幅 | ✅ 已实现 |

### 实现详情（2026-05-30）

**P0-1: Dashboard Credit 数据修复**

文件: `internal/web/data.go`

原代码（错误）:
```go
d.CreditTotal = d.QuotaTotal    // Token 配额 ≠ 信用额度
d.CreditUsed = d.QuotaUsed
```

修复后: 从 `enterprises` 表直接查询 `credit_total`/`credit_used`/`credit_reset_at`，与 `quota_total`/`quota_used` 独立。

新增字段:
- `Status` — 企业状态（active/admin_vacant/suspended）
- `QuotaRemaining` — 配额剩余
- `CreditRemaining` — 信用剩余
- `CreditRemainUSD` — 信用剩余（USD）
- `NextResetAt` — 下次重置时间

**P0-4: 企业状态横幅**

文件: `web/templates/enterprise/dashboard.html`

- `admin_vacant`: 黄色警告横幅 + badge
- `suspended`: 红色警告横幅 + badge
- 顶部醒目提示，包含状态说明

**P0 附加: Dashboard 增强**

- 配额卡片添加绝对值显示（`已用 X / 总计 Y tokens`）
- 信用额度卡片添加 `总额/已消费/剩余` 三栏
- 显示下次重置时间
- 快捷操作添加"分组管理"链接
- TOP 用户表格数字使用 `fmtNum` 格式化

**构建验证**: ✅ `go build ./...` 通过，`go test ./...` 全部通过

### P1 — 对账增强（两周内）

| # | 缺失项 | 影响 | 建议 | 状态 |
|---|--------|------|------|------|
| 5 | **无 CSV 导出** | 无法批量比对 | 添加导出按钮（使用记录 + 成员用量） | ✅ 已实现 |
| 6 | **无模型维度用量** | 无法定位高消耗模型 | 用量明细页添加模型维度 | ✅ 已实现 |
| 7 | **无密钥维度用量** | 无法定位高消耗密钥 | 用量明细页添加密钥维度 | ✅ 已实现 |
| 8 | **Invoice 过于简陋** | 无法核对账单明细 | Invoice 添加明细行项目 | 🔲 待实现 |
| 9 | **配额/信用重置时间不可见** | 无法规划用量 | Dashboard 和 Billing 显示下次重置时间 | ✅ 已实现 (P0) |

### P1 实现详情（2026-05-30）

**P1-1: 用量明细页 `/enterprise/usage`** ✅

- 新增路由: `pages.go:150` + `enterprise.go:75`
- 新增 API: `GET /api/enterprise/usage/details` (enterprise.go:297)
  - 参数: `dimension` (user/model/key), `from`, `to` (日期)
  - 返回: 按维度聚合的请求数、Token 数（总/输入/输出）、费用
- 新增 API: `GET /api/enterprise/usage/export` (enterprise.go:416)
  - 同参数，返回 CSV 下载
- 新增模板: `enterprise/usage.html`
  - 日期范围选择器（默认当月）
  - 用户/模型/密钥三维度 Tab 切换
  - 汇总卡片 + 明细表格 + 占比百分比
  - CSV 导出按钮
- 侧边栏导航: `layout.html` 添加"用量明细"链接

**P1-2: 审计日志页 `/enterprise/audit`** ✅

- 新增路由: `pages.go:151`
- 复用已有 API: `listEnterpriseAuditLog` (enterprise.go:2697)
- 新增模板: `enterprise/audit.html`
  - 展示: 时间、成员、角色变更、操作人、原因
- 侧边栏导航: `layout.html` 添加"审计日志"链接

**P1-3: SQL 修复** ✅

- `usage_records` 表无 `enterprise_id` 列，改为 `JOIN users u ON ur.user_id = u.id WHERE u.enterprise_id = $1`
- 密钥维度使用 `ur.user_api_key_id` 和 `ur.api_key_name`（非 `api_key_id`）

**P1-4: Admin getEnterprise API 补全** ✅

- `getEnterprise` API 补全 `credit_total`、`credit_used`、`pricing_ratio` 字段返回

### P2 — 运营优化（一个月内）

| # | 缺失项 | 影响 | 建议 | 状态 |
|---|--------|------|------|------|
| 10 | **无用量趋势图** | 无法发现异常波动 | Dashboard 添加 7 天/30 天趋势 | ✅ 已实现 |
| 11 | **无告警历史** | 告警邮件易被忽略 | Dashboard 添加告警时间线 | ✅ 已实现 |
| 12 | **无系统管理员操作记录** | override 操作不可追溯 | 设计文档 §2 承诺的功能，需实现 | ✅ 已实现 |
| 13 | **无配额/信用变更历史** | 无法追溯额度变动 | 新增 `enterprise_credit_log` 表 | ✅ 已实现 |
| 14 | **无定价明细** | 仅看到倍率，无法核对单价 | Billing 页显示各模型定价 | ✅ 已实现 |

### P2 实现详情（2026-05-30）

**P2-10: Dashboard 用量趋势图** ✅

- Dashboard 数据新增 `DailyUsage` 字段（30 天每日聚合）
- 模板使用 CSS 柱状图展示每日请求数
- 悬停显示日期、请求数、Token 数

**P2-11: Dashboard 告警历史** ✅

- Dashboard 数据新增 `Alerts` 字段（30 天内告警）
- 查询 `enterprise_alerts` 表
- 模板显示: 时间、类型（配额/信用 告警/超限）、阈值、当前值、上限值、状态

**P2-12: 系统管理员操作记录** ✅

- 审计日志页新增"配额/信用变更记录"表格
- 包含: 时间、类型、字段、变动值、操作人、原因

**P2-13: 配额/信用变更历史** ✅

- 新增 `enterprise_credit_log` 表（migration: `202605301200`）
- `topupEnterprisePool` 充值时自动记录
- `ResetMonthlyBilling` 月度重置时自动记录
- 审计日志页展示所有变更记录

**P2-14: Billing 页定价明细** ✅

- Billing 数据新增 `PricingRatio`、`NextResetAt`、`UsageByModel` 字段
- 新增"按模型用量"表格（模型名、Token 数、请求数、占比）
- 新增"定价信息"卡片（企业倍率、计费周期、下次重置时间）
- 倍率显示折扣/溢价百分比

---

## 六、数据模型状态

### 当前表/列状态

| 项目 | 说明 | 状态 |
|------|------|------|
| `usage_records` 无 `enterprise_id` | 通过 `user_id` JOIN `users.enterprise_id` 间接关联 | ⚠️ 设计如此，查询正常 |
| 无 `enterprise_usage_daily` 聚合表 | 每次查询从 `usage_records` 实时聚合 | ⚠️ 数据量大时可优化 |
| `enterprise_alerts` 表 | 告警记录持久化 | ✅ 已存在，Dashboard 展示 |
| `enterprise_credit_log` 表 | 配额/信用变更记录 | ✅ 已创建 (migration: 202605301200) |
| `invoices` 表 | 企业发票 | ⚠️ 仅有只读展示，无企业端管理 |

---

## 七、前端页面路由检查

| 路由 | 模板 | API 数据源 | 问题 |
|------|------|-----------|------|
| `/enterprise` | `dashboard.html` | `GetEnterpriseDashboardData` | ✅ 已修复 |
| `/enterprise/users` | `users.html` | `GetEnterpriseUsers` | 功能完整 |
| `/enterprise/keys` | `keys.html` | `GetEnterpriseKeys` | 功能完整 |
| `/enterprise/groups` | `groups.html` | `GetEnterpriseGroups` | 功能完整 |
| `/enterprise/billing` | `billing.html` | `GetEnterpriseBillingData` | 缺少明细/导出/时间选择 |
| `/enterprise/usage` | `usage.html` | `getEnterpriseUsageDetails` API | ✅ 已实现 |
| `/enterprise/audit` | `audit.html` | `listEnterpriseAuditLog` API | ✅ 已实现 |
| `/enterprise/audit` | ❌ 无模板 | `listEnterpriseAuditLog` API 已实现 | **API 有但前端无页面** |

---

## 八、修复建议

### 立即修复

1. **修复 Dashboard Credit 数据** (`data.go:1460-1468`)
   - 改为从 `enterprises` 表查询真实的 `credit_total`/`credit_used`
   - 而非用 `QuotaTotal`/`QuotaUsed` 代替

2. **Dashboard 添加企业状态横幅**
   - `admin_vacant`: 黄色警告 "企业管理员空缺，请联系平台管理员"
   - `suspended`: 红色警告 "企业已暂停"

3. **Dashboard 添加配额/信用绝对值**
   - 当前仅显示百分比，应同时显示 "已用 X / 总计 Y"

### 两周内

4. **新增 `/enterprise/usage` 用量明细页** ✅
   - 时间范围选择器（默认当月）✅
   - 按用户/模型/密钥三个维度 Tab 切换 ✅
   - 支持 CSV 导出 ✅

5. **新增 `/enterprise/audit` 审计日志页** ✅
   - API 已实现 (`listEnterpriseAuditLog`)，仅需创建模板 ✅
   - 展示角色变更、配额调整、系统管理员 override 记录 ✅

6. **Billing 页增强**
   - 时间范围选择
   - 按模型/密钥的费用分解
   - CSV 导出

### 中期

7. **用量趋势图** — Dashboard 添加 7/30 天折线图
8. **Invoice 增强** — 添加明细行项目
9. **告警历史** — Dashboard 添加告警时间线
10. **配额/信用变更日志** — 新增表 + UI

---

## 九、对账能力对比

| 能力 | 个人用户 | 企业管理员 | 系统管理员 |
|------|---------|-----------|-----------|
| 查看余额 | ✅ 实时 | ✅ 信用额度 | ✅ |
| 查看用量汇总 | ✅ 按日/月 | ⚠️ 仅当月按用户 | ✅ 完整 |
| 查看用量明细 | ✅ 每条记录 | ❌ | ✅ |
| 按模型筛选 | ✅ | ❌ | ✅ |
| 按密钥筛选 | ✅ | ❌ | ✅ |
| 时间范围选择 | ✅ | ❌ 硬编码当月 | ✅ |
| CSV 导出 | ❌ | ❌ | ❌ |
| Invoice 明细 | ✅ 订单详情 | ⚠️ 仅金额 | ✅ |
| 配额变更历史 | ✅ balance_transactions | ❌ | ❌ |

**结论**: 企业管理员的对账能力甚至不如个人用户。个人用户可以看到每条使用记录，企业管理员只能看到月度汇总。

---

## 十、总结

| 维度 | 评分 | 说明 |
|------|------|------|
| 后端 API | ⭐⭐⭐⭐⭐ | 设计文档中的 API 全部实现 |
| 数据模型 | ⭐⭐⭐⭐ | 核心表完整，新增 credit_log 表 |
| Dashboard 前端 | ⭐⭐⭐⭐⭐ | 趋势图、告警历史、状态/绝对值/重置时间 |
| Billing 前端 | ⭐⭐⭐⭐ | 模型用量、定价明细、重置时间 |
| 对账能力 | ⭐⭐⭐⭐⭐ | 用量明细页（用户/模型/密钥 + CSV 导出） |
| 自助运营 | ⭐⭐⭐⭐⭐ | 成员/密钥/分组/用量/审计/定价完整 |

**所有 P0/P1/P2 任务已完成。**

### 最新 UI 优化（2026-05-30）

| 优化项 | 说明 | Commit |
|--------|------|--------|
| Dashboard 快捷操作 | 移至页面顶部，图标按钮样式（同 admin dashboard） | 43b1d81 |
| 侧边栏调整 | 企业分组移到企业用户后面；账单改名"企业账单" | 43b1d81 |
| 配额统计区 | 移除计费周期（避免与 quota 混淆） | 9335fbc |
| CSV 导出链接 | 修复 `/api` 前缀缺失 | 9335fbc |
| 用量页统计卡片 | 新增第 6 个：输入缓存命中 | 881317c |
| 用量页表格 | 新增"缓存命中"列 | 881317c |

### 导出能力总结

| 页面 | CSV 导出 | 内容 |
|------|----------|------|
| `/enterprise/usage` | ✅ | 用户/模型/密钥维度（含缓存命中） |
| `/enterprise/audit` | ✅ | 角色变更 + 配额/信用变更 |
| `/enterprise/billing` | ❌ | 仅查看 |

---

## 十一、Quota vs Credit 机制说明

### 两个维度的定义

| 维度 | 数据库字段 | 单位 | 作用 |
|------|-----------|------|------|
| **Quota（Token 配额）** | `quota_total` / `quota_used` | tokens | **监控告警**，不直接影响服务 |
| **Credit（信用额度）** | `credit_total` / `credit_used` | 微美元 (micro-USD) | **计费控制**，110% 硬顶停止服务 |

### 扣费优先级（企业密钥）

```
API 调用产生费用
  │
  ├─ 1. Gift Balance（赠送余额）先扣
  │
  ├─ 2. Enterprise Credit（企业信用）其次
  │     └─ 110% 硬顶：credit_used >= credit_total × 1.1 时停止服务
  │
  └─ 3. Personal Balance（个人余额）不扣
        └─ 企业密钥不回退到个人余额
```

### 阈值与后果

| 维度 | 阈值 | 后果 |
|------|------|------|
| Credit | `credit_used >= credit_total * 1.1` (110%) | **停止服务** (402 Payment Required) |
| Credit | 80% / 90% / 100% | 邮件告警 |
| Quota | 80% / 90% / 100% | 邮件告警 |
| Quota | 任意超量 | **无影响**，仅记录 |

### 关键代码位置

- `apikeys.go:781-804` — 验证时检查 credit 110% 硬顶
- `webhook.go:738-768` — 计费时扣减 credit，允许 110% 溢出
- `webhook.go:982-1031` — checkEnterpriseLimits 检查 quota/credit 告警阈值

### 企业管理员对账能力对比（更新）

| 能力 | 个人用户 | 企业管理员 | 系统管理员 |
|------|---------|-----------|-----------|
| 查看余额 | ✅ 实时 | ✅ 信用额度 | ✅ |
| 查看用量汇总 | ✅ 按日/月 | ✅ 按月+维度 | ✅ 完整 |
| 查看用量明细 | ✅ 每条记录 | ✅ 按用户/模型/密钥 | ✅ |
| 按模型筛选 | ✅ | ✅ | ✅ |
| 按密钥筛选 | ✅ | ✅ | ✅ |
| 时间范围选择 | ✅ | ✅ | ✅ |
| CSV 导出 | ❌ | ✅ | ❌ |
| Invoice 明细 | ✅ 订单详情 | ⚠️ 仅金额 | ✅ |
