# Enterprise UI Redesign Detail

> **ACTIVE** — Enterprise 管理后台卡片化改造 & i18n 清理完成。所有 6 个页面已统一为三段式卡片化结构，所有硬编码中文字符串已替换为 i18n 键引用。

- **Created**: 2026-06-06
- **Updated**: 2026-06-07 (Phase 3 完成 + 14 个 bugfix + Admin 全面 Drawer 化)
- **Branch**: `enterprise-cardification`
- **Related**: `ui-design-principles-202606051751.md`, `ui-improvement-proposal-plan-202606061645.md`, `enterprise-enhancement-design-202605221800.md`

---

## 1. Template Style & Card Design Pattern

### 统一卡片化原则

所有 Enterprise 页面统一使用 `page-header + key-stats + key-card`（或扩展）的三段式结构，不再使用内联 `<style>`。

### CSS 类名体系

| 类名 | 用途 | 来源 |
|------|------|------|
| `page-header` / `page-title` | 页面标题行 | app.css（通用） |
| `key-stats` | KPI 统计卡片容器 flex row | app.css |
| `key-stat-card` | 单张 KPI 卡片（icon + value + label） | app.css |
| `key-stat-icon` | KPI 图标的圆形背景容器 | app.css |
| `key-stat-value` | 统计数值（大号字） | app.css |
| `key-stat-label` | 统计标签（小号灰字） | app.css |
| `key-search-bar` | 搜索栏/操作栏容器 | app.css（源自 keys.html 模式） |
| `key-card` | 通用内容卡片 | app.css |
| `card` / `card-header` / `card-title` | 传统卡片容器 | app.css |

### 图标色彩约定

```html
<div class="key-stat-icon primary">   <!-- 蓝色: 余额/金额类 -->
<div class="key-stat-icon danger">    <!-- 红色: 花费/消耗类 -->
<div class="key-stat-icon success">   <!-- 绿色: 成员/用户类 -->
<div class="key-stat-icon warning">   <!-- 黄色/橙色: 请求/用量类 -->
<div class="key-stat-icon" style="background:#f5f3ff;color:#8b5cf6">  <!-- 紫色: 密钥类 -->
<div class="key-stat-icon" style="background:#ecfdf5;color:#059669">  <!-- 翠绿: Token 类 -->
```

### 通用操作栏

```html
<div class="key-search-bar" style="justify-content:flex-end;...">
  <a href="/enterprise/users" class="btn btn-sm btn-secondary">{{t "ui_成员管理"}}</a>
  ...
</div>
```

所有 enterprise 子页面共享同一组快速操作按钮（成员管理 / 企业分组 / 密钥管理 / 账单 / 用量 / 审计）。

---

## 2. Enterprise 页面清单

### 2.1 Sidebar 导航

`layout.html` 新增企业专属侧栏区域，`{{if isEnterpriseAdmin .User}}` 条件渲染：

```
─ 企业 ──────────────       ← divider
  概览                        ← /enterprise
  成员管理                     ← /enterprise/users
  企业分组                     ← /enterprise/groups
  密钥管理                     ← /enterprise/keys
  账单                         ← /enterprise/billing
  用量明细                     ← /enterprise/usage
  审计日志                     ← /enterprise/audit
```

### 2.2 已卡片化的页面（Phase 3 完成）

| 页面 | 模板 | 状态 | 卡化内容 |
|------|------|------|---------|
| **Dashboard** | `dashboard.html` | ✅ 完成 | 6 KPI (`key-stats` + `key-stat-card`) + 5 Tab Charts + 时间范围下拉 + 信用仪表盘 + 告警表 + 用户排行 |
| **Users** | `users.html` | ✅ 完成 | `page-header` + 3 `key-stat-card`（总成员/活跃/管理员）+ `key-search-bar` + `card` 容器包裹表格 + 批量操作工具栏 |
| **Groups** | `groups.html` | ✅ 完成 | `page-header` + 3 `key-stat-card`（总分组/平台组/管理组）+ `key-search-bar` + `card` 容器包裹表格 |
| **Billing** | `billing.html` | ✅ 完成 | `page-header` + 3 `key-stat-card`（总配额/已用/剩余）+ `card` 信用额度概览 + `card` 模型用量明细 |
| **Usage** | `usage.html` | ✅ 完成 | `page-header` + `card` 筛选区域 + `stats-grid` 汇总卡片 + `card` 明细表格 |
| **Audit** | `audit.html` | ✅ 完成 | `page-header` + 4 `key-stat-card`（总日志/充值/月重置/管理操作）+ `card` 角色变更日志 + `card` 配额变更日志 |

### 2.3 i18n 清理（Phase 3 追加）

所有 6 个模板中的硬编码中文字符串已全部替换为 i18n 键引用：

| 模板 | JS 硬编码清理项 | 新增 i18n 键 |
|------|----------------|-------------|
| `keys.html` | `'请填写密钥名称'`, `'请选择分配成员'`, `'创建失败'`, `'请求失败'`, `'保存失败'`, `'获取密钥失败'`, `'已复制到剪贴板'` | `enterprise.keys_stat_total`, `enterprise.keys_monthly_requests`, `enterprise.keys_monthly_tokens`, `modal_编辑分组`, `error.*`, `common.*` |
| `usage.html` | `'用户'/'模型'/'密钥'`, `'按'`, `'暂无数据'`, `'加载失败: '` | `common.dimension_*`, `common.by`, `common.no_data`, `error.load_failed` |
| `groups.html` | `'请输入分组名称'`, `'请选择平台分组'`, `'加载平台分组失败'`, `'加载模型列表失败'`, `'RPM 不能超过...'`, `'该平台分组...'`, `'新建分组'`, `'编辑分组'`, `'创建'`, `'保存'`, `'删除失败'`, `'请求失败'` | `enterprise.groups_stat_total`, `enterprise.rpm`, `enterprise.tpm_hint` (替换原有双下划线键) |
| `users.html` | `'操作失败'`, `'请求失败'`, `'确认移除'`, `'确定要移除选中的 …'` | (复用已有 `error.*` / `common.*` 键) |

Go 后端 handler 中三处标题也已 i18n 化：
- `handleEnterpriseGroups`: `"企业分组管理"` → `h.i18n.T("enterprise.groups")`
- `handleEnterpriseUsage`: `"用量明细"` → `h.i18n.T("enterprise.usage")`
- `handleEnterpriseAudit`: `"审计日志"` → `h.i18n.T("enterprise.audit")`

### 2.4 Users 模板示例（实际实现）

```
┌──────────────────────────────────────────────────┐
│  Enterprise Members                        [标题] │
│                                                   │
│  [👥 25]  [✅ 22]  [🛡 3]                        │
│  总成员     活跃      管理员                       │  ← key-stats + key-stat-card
│                                                   │
│  [🔍 搜索邮箱...]       [添加成员]                │  ← key-search-bar
│                                                   │
│  [0 项已选择] [批量移除] [取消选择]                │  ← batch-toolbar（条件渲染）
│                                                   │
│  ┌─ 成员列表 ───────────────────────────────────┐ │
│  │  ☐ Email  |  Name  |  Role  | 配额 | Key |…  │ │  ← card > data-table
│  │  ├───────────────────────────────────────────┤ │
│  │  │  ...                                       │ │
│  │  └───────────────────────────────────────────┘ │
└──────────────────────────────────────────────────┘
```

---

## 3. Dashboard UI Design

### 3.1 页面总览

```
┌────────────────────────────────────────────────────────────┐
│  Enterprise Management                                      │
│                                                             │
│  [成员管理] [企业分组] [密钥管理] [账单] [用量] [审计]      │
│                                                             │
│  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ┌────────┐ │
│  │ $100  │ │ $450  │ │  25  │ │  12  │ │ 3.2k │ │ 1.8M   │ │
│  │Balance│ │Spend  │ │Members│ │AKeys │ │ Reqs │ │ Tokens │ │
│  └──────┘ └──────┘ └──────┘ └──────┘ └──────┘ └────────┘ │
│                                                             │
│  ┌─ Credit Limit ──────────────────────── [额度/已用/剩] ─┐ │
│  │  ██████████░░░░░░░░░░  45% used                         │ │
│  │  总额度 $200 | 已消费 $90 | 剩余 $110                    │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                             │
│  ┌─ Analytics ────────────── [▼ 1d / 7d / 15d / 30d / Q] ─┐ │
│  │  [Requests] [Tokens] [Users] [Spend] [Quota]             │ │
│  │  ┌─────────────────────────────────────────────────┐    │ │
│  │  │  独立图表（柱状图 / 环形图 / 仪表盘）             │    │ │
│  │  └─────────────────────────────────────────────────┘    │ │
│  └─────────────────────────────────────────────────────────┘ │
│                                                             │
│  ┌─ Top Users ──────────────────────────────────────────┐  │
│  │  User | Tokens | Requests | %                        │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                             │
│  ┌─ Alert History ──────────────────────────────────────┐  │
│  │  时间 | 类型 | 阈值 | 当前值 | 上限 | 状态           │  │
│  └──────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────┘
```

### 3.2 6 张 KPI 统计卡片

| # | 数据字段 | 来源 | 标签 (i18n key) | 图标色 |
|---|---------|------|-----------------|--------|
| 1 | `CreditRemainUSD` | 数据库 `enterprises.credit_used` 计算 | `enterprise.balance` | `primary` (蓝) |
| 2 | `MonthlyUsage` | 当月 `SUM(total_cost)` | `enterprise.usage_month` | `danger` (红) |
| 3 | `Members` | `COUNT(users WHERE enterprise_id=)` | `enterprise.members` | `success` (绿) |
| 4 | `ActiveKeys` | `COUNT(api_keys WHERE status='active')` | `enterprise.active_keys` | 紫色 (#8b5cf6) |
| 5 | `$totalReqs` | DailyUsage 累加 `Requests` | `enterprise.total_requests` | `warning` (橙) |
| 6 | `$totalTokens` | DailyUsage 累加 `Tokens` | `enterprise.total_tokens` | 翠绿 (#059669) |

### 3.3 5 Tab 图表结构

图表分为 5 个顶层选项卡面板，每个面板独立渲染自己的 Chart.js 图表：

#### Requests（柱状图）

```
  ┌──────────────────────────────────────┐
  │  ██  ██  ██  ██  ██  ██  ██  ██  │  ← 蓝色柱状图
  │  ██  ██  ██  ██  ██  ██  ██  ██  │
  │  05-01  05-03  05-05  ...  05-30   │
  └──────────────────────────────────────┘
```

- 数据源: `DailyUsage[].Requests`
- 图表类型: `bar` (Chart.js)
- 颜色: `#3b82f6` (蓝色)

#### Tokens（柱状图）

- 数据源: `DailyUsage[].Tokens`
- 图表类型: `bar` (Chart.js)
- 颜色: `#8b5cf6` (紫色)

#### Users（2 个图表并排）

```
  ┌─ Token doughnut ─┐    ┌─ Requests bar ────┐
  │  ⭕ 占比环形图    │    │  alice ████        │
  │  alice 44%       │    │  bob   ██          │ ← 横排 bar
  │  bob   28%       │    │  carol █           │
  │  carol 18%       │    └────────────────────┘
  └──────────────────┘
```

- 数据源: `RecentUsage`（本月 TOP 10 用户）
- 图表类型: `doughnut` + `bar` (indexAxis: 'y')

#### Spend（柱状图）

- 数据源: `DailyUsage[].Cost`
- 图表类型: `bar` (Chart.js)
- 颜色: `#ef4444` (红色)

#### Quota（2 个环形仪表盘）

```
  ┌── Quota Gauge ──┐    ┌── Credit Gauge ──┐
  │     45%          │    │      68%          │
  │   ██████████     │    │   ██████████████  │
  │  Used Remaining  │    │  Spent Remaining  │
  └──────────────────┘    └───────────────────┘
```

- 数据源: `QuotaUsed/QuotaTotal` + `CreditPercent`
- 图表类型: `doughnut` (cutout: '75%')

### 3.4 Tab 切换逻辑

```javascript
function switchChartTab(tab)     // [Requests] [Tokens] [Users] [Spend] [Quota]
                                 // 5 个顶层标签，切换显示/隐藏 .chart-panel
                                 // 切换后 setTimeout 100ms resize 对应 Chart 实例

function initRequestsChart()     // 蓝色柱状图 — DailyUsage.Requests
function initTokensChart()       // 紫色柱状图 — DailyUsage.Tokens
function initSpendChart()        // 红色柱状图 — DailyUsage.Cost
function initUsersCharts()       // 环形图 + 横排 bar — RecentUsage
function initGaugeCharts()       // 配额 + 信用仪表盘 — Quota/Credit
```

所有 5 个图表在 `DOMContentLoaded` 时一次性初始化（不再动态销毁重建），Tab 切换仅切换面板可见性 + 触发 Chart.resize()。

### 3.5 数据流

```
PostgreSQL
  ├─ enterprises (credit, quota, status...)
  ├─ users (members count, enterprise_id)
  ├─ api_keys (active keys count)
  ├─ usage_records (daily trend, user ranking, monthly cost)
  └─ enterprise_alerts (alert history)
       ↓
internal/query/enterprise.go: GetEnterpriseDashboardData()
       ↓
internal/web/pages.go: handleEnterpriseDashboard()
       ↓
web/templates/enterprise/dashboard.html
       ↓
Chart.js (chart.min.js in layout.html head)
```

---

## 4. Go 后端数据结构

### EnterpriseDashboardData

```go
type TimeRange struct {
    Interval  string  // e.g. "7 days", "3 months"
    Trunc     string  // e.g. "day", "week", "month", "hour"
    Format    string  // e.g. "MM-DD", "YYYY-MM", "HH24:00"
    LabelKey  string  // i18n key for display label
}

var timeRanges = map[string]TimeRange{
    "1d":      {Interval: "1 day",   Trunc: "hour",  Format: "HH24:00", LabelKey: "enterprise.range_1d"},
    "7d":      {Interval: "7 days",  Trunc: "day",   Format: "MM-DD",   LabelKey: "enterprise.range_7d"},
    "15d":     {Interval: "15 days", Trunc: "day",   Format: "MM-DD",   LabelKey: "enterprise.range_15d"},
    "30d":     {Interval: "30 days", Trunc: "day",   Format: "MM-DD",   LabelKey: "enterprise.range_30d"},
    "quarter": {Interval: "3 months",Trunc: "month", Format: "YYYY-MM", LabelKey: "enterprise.range_quarter"},
}

type EnterpriseDashboardData struct {
    Range           string                   // current time range key ("1d"/"7d"/"15d"/"30d"/"quarter")
    Status          string                   // active, admin_vacant, suspended
    Members         int                      // 成员总数
    MaxMembers      int                      // 最大成员数
    ActiveKeys      int                      // 活跃密钥数
    MonthlyUsage    int64                    // 当月总花费 (micro-USD)
    QuotaUsed       int64                    // 已用配额
    QuotaTotal      int64                    // 总配额
    QuotaRemaining  int64                    // 剩余配额
    PricingRatio    float64                  // 定价倍率
    CreditTotal     int64                    // 信用总额度 (micro-USD)
    CreditUsed      int64                    // 已用信用 (micro-USD)
    CreditRemaining int64                    // 剩余信用 (micro-USD)
    CreditPercent   float64                  // 信用使用百分比
    CreditTotalUSD  float64                  // 信用总额度 (USD)
    CreditUsedUSD   float64                  // 已用信用 (USD)
    CreditRemainUSD float64                  // 剩余信用 (USD)
    NextResetAt     *time.Time               // 信用重置时间
    RecentUsage     []EnterpriseUsageRecord  // 本月 TOP 10 用户用量 (不受 timeRange 影响)
    DailyUsage      []DailyUsagePoint        // 时间范围内每日/周/月趋势
    Alerts          []EnterpriseAlert        // 时间范围内告警
}
```

### TimeRange → SQL 映射

| range | INTERVAL | DATE_TRUNC | 格式 | 柱数 |
|-------|----------|-----------|------|------|
| `1d` | 1 day | `hour` | HH24:00 | 24 |
| `7d` | 7 days | `day` | MM-DD | 7 |
| `15d` | 15 days | `day` | MM-DD | 15 |
| `30d` (默认) | 30 days | `day` | MM-DD | 30 |
| `quarter` | 3 months | `month` | YYYY-MM | 3 |

type DailyUsagePoint struct {
    Date     string  // "YYYY-MM-DD"
    Requests int
    Tokens   int64
    Cost     int64   // micro-USD
}

type EnterpriseUsageRecord struct {
    UserID   string
    UserName string
    Tokens   int64
    Requests int
    Percent  float64
}
```

### 关键 SQL 查询

**DailyUsage 趋势（动态时间范围）**:
```sql
-- interval 和 trunc 由 Go 端根据 rangeKey 从 timeRanges map 动态注入
SELECT TO_CHAR(DATE_TRUNC($trunc, ur.created_at), $format) as date,
       COUNT(*), COALESCE(SUM(ur.input_tokens + ur.output_tokens), 0),
       COALESCE(SUM(ur.total_cost), 0)
FROM usage_records ur
JOIN users u ON ur.user_id = u.id
WHERE u.enterprise_id = $1::uuid
  AND ur.created_at >= NOW() - $2::interval
  AND u.deleted_at IS NULL
GROUP BY DATE_TRUNC($trunc, ur.created_at)
ORDER BY date
```

**RecentUsage 用户排行（固定当月，不受 timeRange 影响）**:
```sql
SELECT u.id, COALESCE(u.name, u.email),
       SUM(ur.total_tokens), COUNT(*),
       SUM(ur.total_cost) / (SELECT SUM(total_cost) ...) * 100 as percent
FROM usage_records ur JOIN users u ON ur.user_id = u.id
WHERE u.enterprise_id = $1::uuid
  AND ur.created_at >= DATE_TRUNC('month', CURRENT_DATE)
GROUP BY u.id, u.name, u.email
ORDER BY SUM(ur.total_cost) DESC LIMIT 10
```

**Alerts（跟随 timeRange）**:
```sql
SELECT ... FROM enterprise_alerts
WHERE enterprise_id = $1::uuid
  AND notified_at >= NOW() - $2::interval
ORDER BY notified_at DESC
```

---

## 5. i18n 翻译键

### Enterprise 专用键（`enterprise.*` 前缀）

| Key | 英文 | 中文 | 使用位置 |
|-----|------|------|---------|
| `enterprise.title` | Enterprise Management | 企业管理 | 页面标题 |
| `enterprise.balance` | Balance | 余额 | 卡片 #1 |
| `enterprise.usage_month` | Monthly Usage | 本月用量 | 卡片 #2 |
| `enterprise.members` | Members | 成员数 | 卡片 #3 |
| `enterprise.active_keys` | Active Keys | 活跃密钥 | 卡片 #4 |
| `enterprise.total_requests` | Total Requests | 总请求数 | 卡片 #5 |
| `enterprise.total_tokens` | Total Tokens | 总Token数 | 卡片 #6 |
| `enterprise.title_analytics` | Analytics | 分析 | 图表区域标题 |
| `enterprise.title_requests` | Requests | 请求数 | 图表标签 #1 + JS 图例 |
| `enterprise.title_tokens` | Tokens | Token数 | 图表标签 #2 + JS 图例 |
| `enterprise.title_users` | Users | 用户 | 图表标签 #3 |
| `enterprise.title_spend` | Spend | 花费 | 图表标签 #4 + JS 图例 |
| `enterprise.title_quota_overview` | Quota Overview | 配额概览 | 图表标签 #5 |
| `enterprise.title_tokens_by_user` | Tokens by User | 用户Token分布 | JS 图表标题 |
| `enterprise.title_requests_by_user` | Requests by User | 用户请求分布 | JS 图表标题 |
| `enterprise.range_1d` | 1 Day | 1天 | 时间范围下拉选项 |
| `enterprise.range_7d` | 7 Days | 7天 | 时间范围下拉选项 |
| `enterprise.range_15d` | 15 Days | 15天 | 时间范围下拉选项 |
| `enterprise.range_30d` | 30 Days | 30天 | 时间范围下拉选项 |
| `enterprise.range_quarter` | Quarter | 按季 | 时间范围下拉选项 |

### 命名规范

- `enterprise.*` → 企业专属
- `admin.*` → 系统管理员
- `dashboard.*` → 用户仪表盘
- `title_*` → 通用页面/区块标题（无前缀的表示跨上下文通用）

企业级新键统一加 `enterprise.` 前缀以避免与未来 admin 或用户端同名键冲突。

---

## 6. 时间范围筛选（已实现）

### 实现方式：方案 A（URL query param）

通过 URL `?range=X` 参数切换时间范围，后端根据 `timeRanges` map 动态生成 SQL。

```
Analytics 区域 Header:
  [标题] ───────────────────────────────────── [▼ 1d/7d/15d/30d/quarter]
```

- 前端：`<select>` 下拉框，onchange 时 `window.location.href='?range='+this.value`
- 后端：`c.Query("range")` → `GetEnterpriseDashboardData(rangeKey)` → `timeRanges[rangeKey]`
- 默认值：`DefaultRange = "30d"`
- 非法 range 值自动回退为默认值

### 影响范围

| 数据 | 受 range 影响？ |
|------|----------------|
| DailyUsage 趋势 | **是** — `INTERVAL` 和 `DATE_TRUNC` 随 range 变化 |
| Alerts 告警 | **是** — 相同 `INTERVAL` |
| RecentUsage 用户排行 | **否** — 固定当月 `DATE_TRUNC('month', CURRENT_DATE)` |
| KPI 卡片 totalReqs/totalTokens | **是** — 从 DailyUsage 累加 |

### 后端改动

```go
// internal/query/enterprise.go
type TimeRange struct {
    Interval string  // SQL INTERVAL value
    Trunc    string  // DATE_TRUNC unit
    Format   string  // TO_CHAR format pattern
    LabelKey string  // i18n key
}

var timeRanges = map[string]TimeRange{...}

func GetEnterpriseDashboardData(ctx, db, userID, rangeKey string) (*EnterpriseDashboardData, error) {
    tr, ok := timeRanges[rangeKey]
    if !ok { tr = timeRanges[DefaultRange]; rangeKey = DefaultRange }
    d.Range = rangeKey
    // SQL: ... WHERE ur.created_at >= NOW() - $2::interval
    //      GROUP BY DATE_TRUNC($trunc, ur.created_at)
    //      TO_CHAR(DATE_TRUNC($trunc, ur.created_at), $format)
}
```

```go
// internal/web/pages.go
func (h *Handler) handleEnterpriseDashboard(c *gin.Context) {
    rng := c.Query("range")
    if rng == "" { rng = query.DefaultRange }
    data, _ := h.GetEnterpriseDashboardData(c.Request.Context(), c.GetString("user_id"), rng)
    ...
}
```

---

## 7. 编译与部署

```bash
# Go 编译
go build -o portal ./cmd/server

# Docker 构建
docker compose -f docker-compose.portal.yml down portal
docker compose -f docker-compose.portal.yml up -d portal --build

# 健康检查
curl http://localhost:3001/api/health
# → {"dependencies":{"postgres":{"status":"up"},"redis":{"status":"up"}},"status":"ok"}
```

当前容器运行于 `localhost:3001`，登录凭据：e2e-user@example.com / E2E_TestPass123!

---

## 8. Git 提交记录

```
ea15fd5 feat(enterprise): cardify enterprise pages + dashboard with 6 KPI cards and 7 Chart.js charts
xxxxxxx feat(enterprise): add dynamic time range selector (1d/7d/15d/30d/quarter) with dropdown
xxxxxxx feat(enterprise): restructure chart tabs to 5 top-level tabs (Requests/Tokens/Users/Spend/Quota)
xxxxxxx fix(enterprise): move Credit Gauge above Analytics; convert range selector to <select> dropdown
(待提交) feat(enterprise): i18n cleanup + stat cards for all 6 enterprise pages
```

分支：`enterprise-cardification`

---

## 9. Phase 3 实施 vs 设计计划对比

### 9.1 对齐情况

以下比对 `ui-improvement-proposal-plan-202606061645.md` Phase 3（Enterprise 重构，5-6 天）和 `ui-design-principles-202606051751.md` 的设计原则。

| 计划项 | 设计原则引用 | 状态 | 偏差说明 |
|--------|-------------|------|---------|
| **B2**: Enterprise Dashboard 卡片化 | 原则 §一.1 Enterprise Admin 中等信息密度 | ✅ 完成 | 6 KPI 卡片 + 5 Tab Charts + 时间范围筛选 + 信用仪表盘。符合"卡片+列表混合"策略 |
| **B4**: Enterprise 页面卡片化 (keys/users/groups/billing/usage/audit) | 原则 §一.1 / §五.2 Enterprise Admin 风格 | ✅ 完成 | 全部 6 个页面统一为 `page-header + key-stats + card` 结构。原计划包含 `enterprise-dashboard.html` 但已提前完成 |
| **B10**: Enterprise Admin 两层 sidebar | 原则 §三.2.2 | ✅ 完成 | "用户控制台" section 标题 + "企业管理" section 标题，折叠时退化为分割线。详见 §10 |
| **B11**: Enterprise Admin Drawer/Modal | 原则 §四 Drawer 优先 | ✅ 完成 | `keys.html` / `groups.html` / `users.html` 创建/编辑均使用 drawer；删除保留 Modal（符合"Drawer 优先 / Modal 例外"原则） |
| **B8**: 右上角下拉菜单 Profile + Logout | 原则 §三.3 | ✅ 已完成 | v1 版下拉菜单已实现，含 Profile/Logout/Enterprise/Admin 入口；v2 精简方案因 sidebar 已完整承载 Admin/Enterprise 入口，不再必需 |
| **右侧面板** | 原则 §二.1 三栏按需出现 | ⏸️ 已搁置 | CSS 框架 `has-right-panel` 已就绪，但 Enterprise 页面当前两栏布局已满足需求，三栏右侧面板暂不启用 |

### 9.2 i18n 清理（设计文档未覆盖的追加项）

Plan 和设计原则均未明确要求 i18n 清理，但实施中系统性完成了：

| 清理范围 | 数量 | 说明 |
|---------|------|------|
| Go handler 硬编码中文字符串 | 3 处 | `pages.go` 中 groups/usage/audit 标题 |
| HTML 模板硬编码 | 6 个模板 | 所有非 i18n 的文本和 label |
| JavaScript 硬编码中文字符串 | ~35 处 | 所有 alert/confirm/error 消息 |
| 新增 i18n 键 | ~30 个 | 分布 `en.json` 和 `zh.json`，含 `enterprise.*`, `error.*`, `common.*` 命名空间 |

### 9.3 CSS 类名规范一致性

实施中与设计文档 §1 CSS 类名体系保持一致：

| 设计文档定义 | 实际使用 | 一致性 |
|-------------|---------|--------|
| `key-stats` / `key-stat-card` / `key-stat-icon` / `key-stat-value` / `key-stat-label` | ✅ 全部 6 个页面使用 | 一致 |
| `page-header` / `page-title` | ✅ 全部 6 个页面使用 | 一致 |
| `key-search-bar` | ✅ users/keys/groups 使用 | 一致 |
| `card` / `card-header` / `card-title` | ✅ users/groups/billing/usage/audit 使用 | 一致 |
| `key-card-grid` / `key-card` (卡片网格) | ✅ keys.html 使用 | 一致 |
| `stats-grid` / `stat-card` (旧式) | `billing.html` 信用明细中使用 | 保留（非 KPI 用途） |
| 图标色彩约定 (primary/danger/success/warning) | ✅ 全部 KPI 卡片遵循 | 一致 |

### 9.4 待实施的 Phase 3 子项

以下标记为 Phase 3 但未完成的子项，建议合并到后续迭代：

| 子项 | 优先级 | 预估工作量 | 依赖 |
|------|--------|-----------|------|
| ~~B10: Enterprise Admin 两层 sidebar~~ | ✅ 完成 | — | 见 §10 实施详情 |
| ~~B11: Enterprise Admin Drawer 推广所有创建/编辑~~ | ✅ 完成 | — | groups.html / users.html 已改用 drawer |
| B8: 右上角下拉菜单精简为 Profile/Logout | 低 | — | ✅ 已完成（v1 已实现；v2 精简不再必需） |
| 右侧面板启用（Enterprise 概览/详情） | 低 | — | ⏸️ 已搁置（两栏布局已满足当前需求） |

---

## 10. B10 两层 Sidebar Section 标题实施

### 10.1 结构概览

Enterprise Admin 左侧导航栏现为两层结构：

```
展开状态 (240px):                    折叠状态 (64px):
┌─────────────────────┐              ┌──┐
│ 用户控制台           │              │──│ ← divider (border-top 1px)
│  ├ 📊 Dashboard      │              │📊│
│  ├ 🔑 API Keys       │              │🔑│
│  ├ 🔄 Subscriptions  │              │🔄│
│  ├ 📊 Billing Details│              │📋│
│  ├ 💳 Billing        │              │💳│
│  └ 🎟 Redemption     │              │🎟│
│ ──────────────────── │              │──│ ← sidebar-divider
│ 企业管理             │              │──│ ← divider (border-top 1px)
│  ├ 📊 总览           │              │📊│
│  ├ 👥 成员管理       │              │👥│
│  ├ 📁 企业分组       │              │📁│
│  ├ 🔑 密钥管理       │              │🔑│
│  ├ 💳 账单           │              │💳│
│  ├ 📈 用量明细       │              │📈│
│  └ 📝 审计日志       │              │📝│
│ ☰                     │              │☰│
└─────────────────────┘              └──┘
```

### 10.2 关键实现

**layout.html** — "用户控制台" section 仅对 enterprise_admin 渲染：

```html
{{if eq .Role "enterprise_admin"}}
<div class="sidebar-section-title">
  <svg>...</svg>
  <span>{{t "sidebar.user_console"}}</span>
</div>
{{end}}
<!-- 6 个个人导航项 ... -->

{{if eq .Role "enterprise_admin"}}
<div class="sidebar-divider"></div>
<div class="sidebar-section-title">
  <svg>...</svg>
  <span>{{t "sidebar.enterprise_console"}}</span>
</div>
<!-- 7 个 enterprise 导航项 ... -->
{{end}}
```

**app.css** — 展开态沿用 `sidebar-section-title` (display:flex, gap, padding)；折叠态退化为分割线：

```css
.sidebar.collapsed .sidebar-section-title {
    height: 0;
    min-height: 0;
    padding: 0;
    margin: 8px 12px;
    font-size: 0;
    line-height: 0;
    border: none;
    border-top: 1px solid var(--border);
    overflow: hidden;
}
.sidebar.collapsed .sidebar-section-title * {
    display: none;
}
```

### 10.3 折叠行为

- **展开**（240px）：section 标题显示 icon + 文字，下方列出导航项
- **折叠**（64px）：标题退化为一条细分割线（`border-top: 1px`），所有文字和图标隐藏，导航图标正常显示
- `localStorage('sidebarCollapsed')` 记忆折叠偏好

### 10.4 i18n 键

| Key | 中文 | 英文 |
|-----|------|------|
| `sidebar.user_console` | 用户控制台 | User Console |
| `sidebar.enterprise_console` | 企业管理 | Enterprise Console |

---

## 11. Phase 4 — Enterprise 各页面深度改造

> **状态**: IN PROGRESS (2026-06-06)
> **目标**: 统一交互模式（Drawer 优先）、增强功能（搜索/禁用）、清理冗余

### 11.1 全局改动

| 改动 | 说明 |
|------|------|
| `page-title` margin-bottom: 20px | 所有 enterprise 页面标题与状态卡片间距统一 |
| dashboard 快捷操作栏移除 | 6 个导航按钮已在 sidebar 中，页面内不再重复 |

### 11.2 enterprise/users

| 改动项 | 说明 |
|--------|------|
| 搜索框 + 图标 | `.key-search-bar` 中左侧搜索 SVG + input，右侧"添加用户"按钮，模糊匹配 name/email |
| 添加用户 → Drawer | 替代 modal，使用右侧 `.drawer` 滑入，表单字段：邮箱、角色、状态、Token配额 |
| 编辑用户 → Drawer | 替代 modal，表单同上，预填现有值 |
| 表格操作列增加 | 每行增加"禁用"按钮（禁止使用 enterprise key，不影响平台权限）和"删除"按钮 |
| 删除确认 Modal | "删除"按钮触发确认 Modal，确认后 POST remove endpoint |

### 11.3 enterprise/groups

| 改动项 | 说明 |
|--------|------|
| 搜索框 + 图标 | `.key-search-bar` 左侧搜索 SVG + input，右侧"新建分组"按钮，模糊匹配名称 |
| 新建分组 → Drawer | 替代 modal，使用右侧 `.drawer`，表单：名称、平台分组、可见性、RPM、TPM、模型选择 |
| 编辑分组 → Drawer | 替代 modal |
| 卡片化布局 | 从 `<table>` 改为 `.key-card-grid`（2 列卡片），每张卡片显示：名称、平台分组、RPM、TPM、模型列表、可见性、操作按钮 |
| 卡片操作 | 每卡"禁用"和"删除"按钮 |
| 删除确认 Modal | "删除"按钮触发确认 Modal |

### 11.4 enterprise/keys

| 改动项 | 说明 |
|--------|------|
| 标题间距 | `page-title` 20px 由全局 CSS 生效，无需模板改动 |
| 标杆模板 | 其他页面的卡片样式/Drawer 均参考此页面 |

### 11.5 enterprise/billing

| 改动项 | 说明 |
|--------|------|
| 移除空卡片 | 删除首张 `card`（`title_企业账单` + subtitle），无实质内容 |
| 合并统计卡片 | 将"定价信息"卡片内的 3 个 stat（企业定价倍率、计费周期、下次重置）提升到顶部 `.key-stats`，与配额 3 卡片合并为 6 统一卡片 |
| 移除定价信息卡片区 | 提取 stats 后删除该 card section |
| Billing History 建议 | 注释标记可扩展包含充值、订阅、退款记录 |

### 11.6 enterprise/usage

| 改动项 | 说明 |
|--------|------|
| 筛选区下移 | 日期选择器 + 维度下拉移至 6 个 stat 卡片下方 |
| 导出按钮 | 在日期选择器旁增加"导出 CSV"按钮 |
| 其他保持 | 统计数据、明细表结构不变 |

### 11.7 enterprise/audit

| 改动项 | 说明 |
|--------|------|
| 标题间距 | `page-title` 20px 由全局 CSS 生效 |
| 其他保持 | 当前格局暂不变，稍后更新 |

---

## 12. Phase 4 — System/Super Admin 后台优化

> **状态**: IN PROGRESS (2026-06-06)
> **目标**: 两层 sidebar（System Admin 区域）、KeyPool/Model/Provider 页面 Drawer 化、表格优化

### 12.1 B10 — System Admin 两层 Sidebar

系统管理员 (system_admin/super_admin) 左侧导航栏新增三层结构：

```
展开状态 (240px):
┌─────────────────────┐
│ 用户控制台           │ ← shared with enterprise
│  ├ Dashboard         │
│  ├ API Keys          │
│  └ ...               │
│ ──────────────────── │
│ 企业管理 (if also ea)│ ← enterprise section
│ ──────────────────── │
│ 系统管理             │ ← NEW: System Console
│  ├ 概览              │
│  ├ 用户管理           │
│  ├ 用户组管理         │
│  ├ 企业管理           │
│  ├ API密钥池          │
│  ├ 模型管理           │
│  ├ 供应商管理         │
│  ├ 订阅计划           │
│  ├ 兑换码管理         │
│  ├ 邀请码管理         │
│  ├ 公告管理           │
│  ├ 审计日志           │
│  ├ 系统设置           │
│  ├ 上行Key监控         │
│  ├ 下行Key监控         │
│  ├ 上行Key使用统计     │
│  └ 供应商状态监控      │
└─────────────────────┘
```

- "用户控制台" section 条件从 `{{if eq .Role "enterprise_admin"}}` 扩展为 `{{if or (eq .Role "enterprise_admin") (eq .Role "system_admin") (eq .Role "super_admin")}}`
- 折叠时 section 标题退化为分割线（复用 B10 CSS）

### 12.2 B4+B12 — 核心后台页面改造

| 页面 | 改造内容 | 参考模式 |
|------|---------|---------|
| **admin-keypool** | page-header + key-stats(4卡) + key-search-bar + table; 创建/编辑 → Drawer | apikeys.html |
| **admin-models-general** | page-header + key-stats(5卡) + filter+toggle bar; 创建/编辑 → Drawer | apikeys.html |
| **admin-providers** | page-header + key-stats(4卡) + search+filter; 创建/编辑 → Drawer | apikeys.html |
| 其余 admin 页面 | page-header 标题间距（CSS 全局生效） | — |

### 12.3 实施原则

- System/Super Admin 层级保持表格为主（不强制卡片化），信息密度中高
- Drawer 用于创建/编辑（保持列表上下文），删除保留 Modal
- 所有改动不破坏后端 API，不创建新 CSS 类

---

## 13. Playwright Smoke Test 结果

> **日期**: 2026-06-07 | **浏览器**: Playwright (Chromium) | **账户**: e2e-user@example.com (enterprise_admin)

### 13.1 Enterprise 页面（全部通过）

| 页面 | HTTP | .page-title | .key-stats | 备注 |
|------|------|-------------|------------|------|
| /enterprise | ✅ 200 | ✅ | ✅ | Dashboard 6 KPI + 5 Charts |
| /enterprise/users | ✅ 200 | ✅ | ✅ | Drawer add/edit + search + disable/delete |
| /enterprise/groups | ✅ 200 | ✅ | ✅ | 2-col card grid + Drawer create/edit |
| /enterprise/keys | ✅ 200 | ✅ | ✅ | Benchmark 模板 |
| /enterprise/billing | ✅ 200 | ✅ | ✅ | 6 unified stat cards |
| /enterprise/usage | ✅ 200 | ✅ | — | 使用 stats-grid（JS 动态数据，预期行为） |
| /enterprise/audit | ✅ 200 | ✅ | ✅ | 4 stat cards + export toolbar |

### 13.2 Admin 页面

| 页面 | HTTP | .page-title | .key-stats | Drawers | Console Errors | 备注 |
|------|------|-------------|------------|---------|----------------|------|
| /admin | 200 | — | — | — | 0 | 旧版布局，未纳入 Phase 4 范围 |
| /admin/keypool | ✅ 200 | ✅ | ✅ | ✅ 2 | 0 | Drawer create/edit + table + 4 stats |
| /admin/models | ✅ 200 | ✅ | ✅ | ✅ 2 | 0 | Drawer + bridge JS + 5 stats |
| /admin/providers | ✅ 200 | ✅ | ✅ | ✅ 2 | 0 | Drawer + priority + delete |

> ✅ 3/3 核心 Admin 页面全部通过，零 Console Error，Drawer 渲染正常。

### 13.3 权限分离验证

| 角色 | Enterprise 页面 | Admin 页面 |
|------|---------------|-----------|
| enterprise_admin (e2e-user@example.com) | ✅ 全部 7 页 | ❌ 403 (预期) |
| system_admin (admin@example.com) | ✅ | ✅ 3 核心页 |

### 13.4 运行时状态

- Go build: `go build -o portal ./cmd/server` — 零错误
- i18n JSON: `python3 -m json.tool` — 双文件校验通过
- Docker: `docker compose up -d portal --build` — 容器运行正常
- Health: `curl /api/health` → `{"status":"ok"}`

---

## 14. 2026-06-07 更新 — Admin 页面全面改造完成

Enterprise 7 页在 Phase 3 已全部完成。后续 Admin 层 22 页也完成了统一改造，详见 `docs/ui-admin-redesign-detail-202606070703.md`。

### Enterprise 相关 Admin 改动

| 改动 | 说明 |
|------|------|
| admin-enterprise Drawer 化 | 创建/编辑企业改用 840px drawer，删除/暂停保留 modal |
| admin-groups Drawer + 合并 | edit-group-drawer 合并 `admin-group-edit.html` 全部字段（10 限流 + 模型选择器） |
| admin-users Drawer | Add/edit 改用 drawer，CSRF 补全 |
| admin-subscriptions Drawer | 重写避免叠加，Batch Import/Generate 也改为 drawer |
| admin-keypool/provider | 全 drawer 化，ID 字段前置提示，API Key 明文显示 |

### Bugfix 回顾（Enterprise 相关）

| Bug | 页面 | 修复 |
|-----|------|------|
| 模板渲染崩溃（`.Description` 字段不存在） | admin-providers | 删除不存在字段 |
| 模板渲染崩溃（`.TotalPages` 不存在） | admin-subscriptions | 移除错误 pagination |
| `toggleGroup` CSRF 缺失 | admin-groups | 补充 csrf_token |
| `confirmDelete/Activate` CSRF 缺失 | admin-enterprise | 补充 csrf_token |
| Rate limit fields 不回显（API 未返回） | admin-groups | `getGroup` API 返回全部 10 字段 |

### Git 状态

- **分支**: `enterprise-cardification` | **最新**: `638ff93` | **Commits**: 25 ahead of main
