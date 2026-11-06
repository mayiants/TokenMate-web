# 用户时区偏好支持 — 设计与实现报告

> **实施范围**: ai-portal-go 全站时间显示与时区偏好
> **创建时间**: 2026-05-26
> **关联提交**: `faff158`

---

## 1. 问题描述

### 1.1 现状痛点

系统在时间处理上存在以下问题：

| 问题 | 影响 |
|------|------|
| 数据库 `TIMESTAMP` 列无时区信息 | 时间戳的实际含义取决于 PostgreSQL 服务器 OS 时区配置，迁移服务器时会导致数据含义变化 |
| 后端 `time.Now()` 几乎不用 `.UTC()` (86 处中只有 3 处) | 所有记录的时间都是服务器本地时间，无法做跨时区转换 |
| 前端 Go 模板 `formatDateTime()` 用服务器本地时区渲染 | 管理员在美国看到的时间和中国用户看到的不一样 |
| 前端 JS `new Date(dateStr)` 用浏览器本地时区渲染 | 同一笔订单，Go 模板显示的和 JS AJAX 加载的可能是两个时间 |
| **用户没有时区偏好设置** | 所有用户被迫看服务器时区的时间，无法按自己所在地显示 |

### 1.2 典型场景

- 用户 A 在 **Asia/Shanghai**，创建了一笔充值订单，订单显示 `2026-05-26 14:00`
- 用户 B 在 **America/New_York** 查看同一笔订单，因为服务器在 Shanghai，B 也看到 `2026-05-26 14:00`（实际是纽约凌晨 2 点）
- 这种体验对海外用户极其困惑

### 1.3 调查发现的关键代码

```go
// internal/db/db.go:46 — DSN 没有 timezone 参数
dsn := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable", ...)

// internal/routes/billing.go:459 — 存的是服务器本地时间
"created_at": time.Now(),

// internal/web/helpers.go:39 — Go 模板用服务器本地时区渲染
func formatDateTime(t time.Time) string {
    return t.Format("2006-01-02 15:04") // 服务器本地时区
}

// web/templates/orders.html:310 — JS 用浏览器本地时区渲染
function formatDate(dateStr) {
    var d = new Date(dateStr); // 浏览器本地时区
    ...
}
```

---

## 2. 解决思路

### 2.1 核心原则

1. **存储统一用 UTC** — 数据库中所有时间戳的含义不再依赖服务器位置
2. **显示按用户时区** — 每个用户可以选择自己的时区，所有时间按该时区渲染
3. **最小侵入** — 不改现有表结构（`TIMESTAMP` 保持），通过 DSN 参数强制 UTC 解释

### 2.2 架构图

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   用户浏览器     │     │   Go 后端        │     │   PostgreSQL    │
│                 │     │                 │     │                 │
│ 时区选择:        │────▶│ POST /profile   │────▶│ users.timezone  │
│ Asia/Shanghai   │     │ (保存偏好)       │     │ DEFAULT 'UTC'   │
└─────────────────┘     └─────────────────┘     └─────────────────┘
         │
         │ 渲染时间时
         ▼
┌─────────────────────────────────────────────────────────────────┐
│  前端 JS: toLocaleString('en-US', {timeZone: 'Asia/Shanghai'})  │
│  从 UTC 字符串 → 按用户时区格式化                                   │
└─────────────────────────────────────────────────────────────────┘
```

---

## 3. 实施方案

### 3.1 数据库层

#### 3.1.1 新增 `users.timezone` 字段

```sql
-- migrations/202605250800_add_user_timezone.sql
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'users' AND column_name = 'timezone') THEN
        ALTER TABLE users ADD COLUMN timezone VARCHAR(50) DEFAULT 'UTC';
    END IF;
END $$;
```

#### 3.1.2 DSN 强制 UTC

```go
// internal/db/db.go:46
// BEFORE
"postgres://...?sslmode=disable"

// AFTER
"postgres://...?sslmode=disable&timezone=UTC"
```

> **为什么加 `timezone=UTC`**：PostgreSQL `TIMESTAMP`（无时区）在读取时会被 pgx 驱动附加一个 Location。不加参数时附加的是 Go 进程本地时区；加 `timezone=UTC` 后，所有 `TIMESTAMP` 值都被当作 UTC 解析，确保后端拿到的是统一的时间基准。

### 3.2 后端层

#### 3.2.1 全局模板数据 — `PageData.Timezone`

```go
// internal/web/handler.go
type PageData struct {
    // ... existing fields ...
    Timezone string  // <-- 新增
}

func (h *Handler) Render(c *gin.Context, name string, data PageData) {
    // ...
    data.Timezone = h.getTimezone(c)  // <-- 新增
    // ...
}
```

#### 3.2.2 时区解析优先级 — `getTimezone()`

```go
func (h *Handler) getTimezone(c *gin.Context) string {
    // 1. Cookie（已登录用户上次选择）
    if cookie, err := c.Cookie("timezone"); err == nil && cookie != "" {
        return cookie
    }
    // 2. 数据库（用户偏好设置）
    userID := c.GetString("user_id")
    if userID != "" && h.db != nil {
        var tz string
        h.db.QueryRow(..., `SELECT COALESCE(timezone, 'UTC') FROM users WHERE id = $1`, userID).Scan(&tz)
        if tz != "" { return tz }
    }
    // 3. 默认 UTC
    return "UTC"
}
```

#### 3.2.3 Web Form 保存 — `HandleUpdateProfileSettings()`

```go
// internal/web/actions.go
func (h *Handler) HandleUpdateProfileSettings(c *gin.Context) {
    timezone := c.PostForm("timezone")
    if timezone == "" { timezone = "UTC" }

    h.db.Exec(...,
        `UPDATE users SET ..., timezone = $4, ... WHERE id = $5`,
        locale, theme, currency, timezone, userID,
    )
    c.SetCookie("timezone", timezone, 86400*365, "/", "", isSecure(c), false)
}
```

#### 3.2.4 JSON API — `getProfile()` / `updateProfile()`

```go
// internal/routes/profile.go
func (r *ProfileRoutes) getProfile(c *gin.Context) {
    // SELECT ... COALESCE(timezone, 'UTC'), created_at FROM users ...
    resp["timezone"] = timezone
}

func (r *ProfileRoutes) updateProfile(c *gin.Context) {
    var req struct {
        // ...
        Timezone string `json:"timezone"`
    }
    if req.Timezone != "" {
        updates = append(updates, "timezone = $"+...)
    }
}
```

### 3.3 前端层

#### 3.3.1 Profile 页面 — 时区选择下拉框

```html
<!-- web/templates/profile.html — 偏好设置行 -->
<div class="flex items-center gap-2" x-data="timezoneForm()">
  <span class="text-sm font-medium text-muted">时区</span>
  <select class="form-select form-select-sm" x-model="timezone" @change="submit()">
    <option value="UTC">UTC</option>
    <option value="Asia/Shanghai">Asia/Shanghai</option>
    <option value="America/New_York">America/New_York</option>
    <option value="Europe/London">Europe/London</option>
    <option value="Asia/Tokyo">Asia/Tokyo</option>
    <option value="Australia/Sydney">Australia/Sydney</option>
    <option value="America/Los_Angeles">America/Los_Angeles</option>
    <option value="Europe/Berlin">Europe/Berlin</option>
    <option value="Asia/Singapore">Asia/Singapore</option>
    <option value="Asia/Dubai">Asia/Dubai</option>
  </select>
</div>
```

#### 3.3.2 时间显示函数 — 按用户时区格式化

```javascript
// web/templates/billing-details.html
window.userTimezone = '{{.Timezone}}';

function formatDateTime(dateStr) {
    if (!dateStr) return '-';
    var d = new Date(dateStr);  // 解析 UTC 字符串
    var tz = window.userTimezone || 'UTC';
    var parts = {
        year:   d.toLocaleString('en-US', {timeZone: tz, year: 'numeric'}),
        month:  d.toLocaleString('en-US', {timeZone: tz, month: '2-digit'}),
        day:    d.toLocaleString('en-US', {timeZone: tz, day: '2-digit'}),
        hour:   d.toLocaleString('en-US', {timeZone: tz, hour: '2-digit', hour12: false}),
        minute: d.toLocaleString('en-US', {timeZone: tz, minute: '2-digit'}),
        second: d.toLocaleString('en-US', {timeZone: tz, second: '2-digit'})
    };
    return parts.year + '-' + parts.month + '-' + parts.day + ' '
         + parts.hour + ':' + parts.minute + ':' + parts.second;
}
```

### 3.4 修改文件清单

| # | 文件 | 类型 | 说明 |
|---|------|------|------|
| 1 | `migrations/202605250800_add_user_timezone.sql` | 新建 | 添加 `users.timezone` 列 |
| 2 | `internal/db/db.go` | 修改 | DSN 加 `timezone=UTC` |
| 3 | `internal/web/data.go` | 修改 | `ProfileData.Timezone` + SQL/Scan |
| 4 | `internal/web/handler.go` | 修改 | `PageData.Timezone` + `getTimezone()` |
| 5 | `internal/web/actions.go` | 修改 | `HandleUpdateProfileSettings` 保存 timezone |
| 6 | `internal/web/pages.go` | 修改 | `handleProfile` 覆盖 timezone |
| 7 | `internal/routes/profile.go` | 修改 | `getProfile`/`updateProfile` 处理 timezone |
| 8 | `web/templates/profile.html` | 修改 | 时区选择 UI + Alpine form |
| 9 | `web/templates/billing-details.html` | 修改 | `formatDateTime()` 时区感知 |
| 10 | `web/templates/orders.html` | 修改 | `formatDate()` 时区感知 |

---

## 4. 现状

### 4.1 已完成的页面

| 页面 | 状态 | 说明 |
|------|------|------|
| Profile 设置 | ✅ | 下拉框可选 10 个常用时区，保存后 cookie + DB 双写 |
| Billing Details | ✅ | `formatDateTime()` 按 `window.userTimezone` 显示 |
| Orders | ✅ | `formatDate()` 按 `window.userTimezone` 显示 |

### 4.2 尚未覆盖的页面（后续可扩展）

| 页面 | 当前状态 | 扩展方式 |
|------|---------|---------|
| Admin Dashboard (Go 模板渲染) | 仍用服务器本地时区 | 改 `formatDateTime(t)` → `formatDateTime(t, timezone)` |
| Admin Audit Logs | 同上 | 同上 |
| Admin Users | 同上 | 同上 |
| Admin Monitor Upstream/Downstream | 同上 | 同上 |
| Invoices | JS `toLocaleDateString()` 用浏览器时区 | 改为用 `window.userTimezone` |
| Admin Enterprise | JS `toLocaleString()` 用浏览器时区 | 同上 |

### 4.3 验证结果

```bash
# API 返回时区
GET /api/profile → {"user": {"timezone": "UTC", ...}}

# API 更新时区
PATCH /api/profile {"timezone": "Asia/Shanghai"} → {"success": true}

# DB 列检查
SELECT column_name, column_default FROM information_schema.columns
WHERE table_name = 'users' AND column_name = 'timezone';
→ timezone | 'UTC'::character varying

# 用户值
SELECT email, timezone FROM users WHERE email = 'admin@example.com';
→ admin@example.com | Asia/Shanghai
```

### 4.4 已知限制

1. **历史数据未迁移**：现有 `TIMESTAMP` 列中的值在 DSN 加 `timezone=UTC` 后会被解释为 UTC。如果服务器之前运行在 `Asia/Shanghai`，历史数据会显示为 "早 8 小时"。由于当前是测试阶段，数据库可 flush，此限制可接受。

2. **Go 模板函数未改**：`internal/web/helpers.go` 中的 `formatDate()`/`formatDateTime()` 仍使用服务器本地时区，仅影响服务端渲染的页面（admin dashboard 等）。JS 动态加载的页面（billing、orders）已按用户时区显示。

3. **IANA 时区验证**：前端下拉框是固定的 10 个选项，后端未做严格的 IANA 时区名校验。如需开放输入，建议加 allowlist 或 `time.LoadLocation()` 验证。

---

## 5. 后续扩展建议

### 5.1 短期（可选）

- 将 `formatDate()`/`formatDateTime()` Go 模板 helper 改为接受 `timezone` 参数，覆盖 admin 页面的时间显示
- 将 `invoices.html`、`admin-enterprise.html` 等页面的 `toLocaleString()` 统一改为用 `window.userTimezone`

### 5.2 中期（可选）

- 新产生的业务代码统一使用 `time.Now().UTC()` 存时间
- 考虑将核心表的 `TIMESTAMP` 逐步迁移为 `TIMESTAMPTZ`

### 5.3 长期（可选）

- 系统设置中添加全局默认时区配置
- 企业管理员可为企业成员设置默认时区
