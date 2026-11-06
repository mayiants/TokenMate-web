# 全站时间显示点时区一致性审计报告

> 日期：2026-05-26  
> 范围：ai-portal-go 全站模板 + 后端  
> 状态：P0-P2 统一修复计划

---

## 一、背景

此前已完成 `billing-details.html` 和 `orders.html` 的时区感知改造，但全站约 **40 处** 时间显示点仍未覆盖时区转换。本报告汇总所有时间使用点，按渲染方式、敏感度分类，并制定统一修复策略。

### 已完成的改造

| 页面 | 方式 | 状态 |
|------|------|------|
| `billing-details.html` | JS `formatDateTime()` + `window.userTimezone` | ✅ |
| `orders.html` | JS `formatDate()` + `window.userTimezone` | ✅ |
| `profile.html` | 时区选择器 + cookie + DB | ✅ |
| `internal/web/handler.go` | `PageData.Timezone` + `getTimezone()` | ✅ |
| `migrations/` | `users.timezone VARCHAR(50) DEFAULT 'UTC'` | ✅ |
| DSN | `&timezone=UTC` | ✅ |

---

## 二、按渲染方式分类

### 2.1 Go 模板服务端渲染（formatDate/formatDateTime/.Format）

当前 `formatDate()`/`formatDateTime()` 使用服务器本地时区（容器内可能是 UTC，但不可依赖）。

| # | 页面 | 代码 | 字段 | 当前时区 |
|---|------|------|------|----------|
| 1 | `admin-audit-logs.html:58` | `{{formatDateTime .CreatedAt}}` | 审计日志创建时间 | 服务器本地 |
| 2 | `admin-keypool.html:78` | `{{formatDate .CreatedAt}}` | 上游 Key 创建时间 | 服务器本地 |
| 3 | `admin-users.html:49` | `{{formatDate .CreatedAt}}` | 用户注册时间 | 服务器本地 |
| 4 | `admin.html:173` | `{{formatDate .CreatedAt}}` | 统计面板-用户 | 服务器本地 |
| 5 | `admin.html:198` | `{{formatDateTime .CreatedAt}}` | 统计面板-订单 | 服务器本地 |
| 6 | `admin-enterprise.html:68` | `{{.CreatedAt.Format "2006-01-02 15:04"}}` | 企业创建时间 | 服务器本地 |
| 7 | `admin-monitor-upstream.html:118` | `{{.LastValidatedAt.Format "..."}}` | 上游 Key 最后验证时间 | 服务器本地 |
| 8 | `admin-monitor-upstream.html:123` | `{{.CreatedAt.Format "..."}}` | 上行监控创建时间 | 服务器本地 |
| 9 | `admin-monitor-downstream.html:120` | `{{.CreatedAt.Format "..."}}` | 下行监控创建时间 | 服务器本地 |
| 10 | `dashboard.html:156` | `{{formatDate .CreatedAt}}` | 最近创建的 API Key | 服务器本地 |
| 11 | `enterprise/billing.html:117` | `{{.CreatedAt.Format "..."}}` | 企业计费记录 | 服务器本地 |
| 12 | `enterprise/keys.html:38` | `{{.CreatedAt}}` | 企业 Key 创建时间 | 服务器本地 |
| 13 | `super/admin-management.html:28` | `{{formatDate .CreatedAt}}` | 超级管理员创建时间 | 服务器本地 |
| 14 | `partials/audit-log-detail.html:12` | `{{.CreatedAt}}` | 审计日志详情时间 | 服务器本地 |

**统一修复方式**：重构 `internal/web/helpers.go` 的 `formatDate()`/`formatDateTime()` 接受 timezone 参数，模板统一传入 `$.Timezone`。

```go
// 改造后
func formatDate(t time.Time, tz string) string {
    if t.IsZero() { return "-" }
    if tz == "" { tz = "UTC" }
    loc, _ := time.LoadLocation(tz)
    return t.In(loc).Format("2006-01-02")
}
```

模板调用：
```html
{{formatDate .CreatedAt $.Timezone}}
```

---

### 2.2 JS 动态渲染（已有 `window.userTimezone` 基础设施）

| # | 页面 | 代码 | 字段 | 状态 |
|---|------|------|------|------|
| 15 | `billing-details.html:346` | `formatDateTime()` | 计费明细时间 | ✅ 已修复 |
| 16 | `orders.html:310` | `formatDate()` | 订单创建/完成时间 | ✅ 已修复 |
| 17 | `redemption.html:246` | `formatDate()` | 兑换码创建/过期时间 | ❌ 待修复 |
| 18 | `my-subscriptions.html:370` | `formatDate()` | 订阅开始/过期时间 | ❌ 待修复 |

**统一修复方式**：添加 `window.userTimezone = '{{.Timezone}}'`，`formatDate` 使用 `toLocaleString(..., {timeZone})`。

---

### 2.3 JS `new Date().toLocaleString()`（浏览器本地时区）

| # | 页面 | 代码 | 字段 | 当前时区 |
|---|------|------|------|----------|
| 19 | `admin-announcements.html:111` | `new Date(a.created_at).toLocaleString()` | 公告创建时间 | 浏览器本地 |
| 20 | `admin-enterprise.html:320` | `new Date(e.created_at).toLocaleString()` | 企业详情弹窗创建时间 | 浏览器本地 |
| 21 | `admin-invitations.html:308` | `new Date(c.created_at).toLocaleString()` | 邀请码创建时间 | 浏览器本地 |
| 22 | `admin-invitations.html:340` | `new Date(r.created_at).toLocaleString()` | 邀请记录时间 | 浏览器本地 |
| 23 | `admin-invitations.html:379` | `new Date(wc.created_at).toLocaleString()` | 欢迎码创建时间 | 浏览器本地 |
| 24 | `invitation.html:180` | `new Date(c.created_at).toLocaleString()` | 用户邀请码列表 | 浏览器本地 |
| 25 | `invitation.html:199` | `new Date(r.created_at).toLocaleString()` | 用户邀请记录 | 浏览器本地 |
| 26 | `invoices.html:398` | `new Date(r.created_at).toLocaleDateString()` | 发票申请时间 | 浏览器本地 |

**统一修复方式**：统一改为 `toLocaleString('en-US', {timeZone: window.userTimezone || 'UTC'})`。

---

### 2.4 JS `new Date().toLocaleDateString()`（浏览器本地时区）

| # | 页面 | 代码 | 字段 |
|---|------|------|------|
| 27 | `admin-invitations.html:304` | `new Date(c.expires_at).toLocaleDateString()` | 邀请码过期日期 |
| 28 | `admin-invitations.html:378` | `new Date(wc.expires_at).toLocaleDateString()` | 欢迎码过期日期 |
| 29 | `invitation.html:173` | `new Date(c.expires_at).toLocaleDateString()` | 用户邀请码过期日期 |
| 30 | `admin-redemption.html:266` | `new Date(c.expires_at).toLocaleDateString()` | 兑换码过期日期 |

**统一修复方式**：同上，改为 `toLocaleDateString('en-US', {timeZone: window.userTimezone || 'UTC'})`。

---

## 三、按敏感度分类

### 🔴 高敏感（金融/计费/安全审计）

| 场景 | 页面 | 说明 |
|------|------|------|
| 计费明细时间 | `billing-details.html` | 用户每一笔消费的时间戳 ✅ |
| 订单创建/完成时间 | `orders.html` | 充值/购买订单时间 ✅ |
| 企业计费记录 | `enterprise/billing.html` | 企业级消费时间 |
| 发票申请时间 | `invoices.html` | 税务相关 |
| 订阅开始/过期 | `my-subscriptions.html` | 服务有效期 |
| 兑换码有效时间 | `redemption.html` | 促销有效期 |
| 邀请码过期时间 | `invitation.html`, `admin-invitations.html` | 奖励活动有效期 |
| 审计日志时间 | `admin-audit-logs.html` | 安全追踪 |
| 最后登录时间 | `internal/web/auth.go:103` | 仅后台存储，前端不显示 |
| GDPR 导出/删除时间 | `internal/routes/gdpr.go` | 合规记录 |
| 账号锁定时间 | `users.locked_until` | 安全机制 |

### 🟡 中敏感（用户/企业管理）

| 场景 | 页面 | 说明 |
|------|------|------|
| 用户注册时间 | `admin-users.html` | 用户管理 |
| 企业创建时间 | `admin-enterprise.html` | 企业生命周期 |
| Key 创建时间 | `admin-keypool.html`, `enterprise/keys.html` | 资源管理 |
| API Key 创建时间 | `dashboard.html` | 用户自助管理 |
| 管理员创建时间 | `super/admin-management.html` | 权限管理 |

### 🟢 低敏感（配置/公告/统计）

| 场景 | 页面 | 说明 |
|------|------|------|
| 公告创建时间 | `admin-announcements.html` | 内容管理 |
| 上游 Key 验证时间 | `admin-monitor-upstream.html` | 运维监控 |
| 统计面板时间 | `admin.html` | Dashboard |
| 订阅计划删除时间 | `admin-subscriptions.html` | 配置管理 |

---

## 四、后端存储时间审计

| 方式 | 数量 | 典型代码 | 风险 |
|------|------|---------|------|
| `time.Now()` (本地) | ~80+ | `"created_at": time.Now()` | 存的是服务器本地时间 |
| `time.Now().UTC()` | 3 | `Timestamp: time.Now().UTC()` | ✅ 正确 |
| SQL `NOW()` | 大量 | `created_at = NOW()` | 存的是会话时区时间 |
| SQL `updated_at = NOW()` | 大量 | 更新操作 | 同上 |

> **说明**：通过 DSN `&timezone=UTC` 已将数据库会话时区固定为 UTC，SQL `NOW()` 返回 UTC 时间。Go 端 `time.Now()` 返回服务器本地时间，但在 Docker 容器中通常是 UTC，因此实际上也是 UTC。后端存储时间本身不是当前主要问题。

---

## 五、修复优先级与计划

### P0（立即修复）— 用户可见金融/个人页面

| # | 页面 | 当前问题 | 工作量 |
|---|------|---------|--------|
| 1 | `my-subscriptions.html` | JS `formatDate()` 用浏览器时区 | 小 |
| 2 | `redemption.html` | JS `formatDate()` 用浏览器时区 | 小 |
| 3 | `invitation.html` | JS `toLocaleString()` 用浏览器时区 | 小 |
| 4 | `invoices.html` | JS `toLocaleDateString()` 用浏览器时区 | 小 |
| 5 | `enterprise/billing.html` | Go `.Format` 用服务器时区 | 中 |

### P1（近期修复）— Admin 运维页面

| # | 页面 | 当前问题 | 工作量 |
|---|------|---------|--------|
| 6 | `admin-audit-logs.html` | Go `formatDateTime` 用服务器时区 | 中 |
| 7 | `admin-enterprise.html` | Go `.Format` + JS `toLocaleString` | 中 |
| 8 | `admin-announcements.html` | JS `toLocaleString()` | 小 |
| 9 | `admin-invitations.html` | JS `toLocaleString()` + `toLocaleDateString()` | 小 |
| 10 | `admin-monitor-upstream.html` | Go `.Format` 用服务器时区 | 中 |
| 11 | `admin-monitor-downstream.html` | Go `.Format` 用服务器时区 | 中 |

### P2（后续修复）— 一般管理页面

| # | 页面 | 当前问题 |
|---|------|---------|
| 12 | `admin-users.html` | Go `formatDate` 用服务器时区 |
| 13 | `admin-keypool.html` | Go `formatDate` 用服务器时区 |
| 14 | `admin.html` | Go `formatDate`/`formatDateTime` 用服务器时区 |
| 15 | `dashboard.html` | Go `formatDate` 用服务器时区 |
| 16 | `super/admin-management.html` | Go `formatDate` 用服务器时区 |
| 17 | `enterprise/keys.html` | Go 直接输出 `.CreatedAt` |
| 18 | `partials/audit-log-detail.html` | Go 直接输出 `.CreatedAt` |

---

## 六、统一修复方案

### 6.1 Go 模板 helpers 改造

```go
// internal/web/helpers.go

func formatDate(t time.Time, tz string) string {
    if t.IsZero() { return "-" }
    if tz == "" { tz = "UTC" }
    loc, _ := time.LoadLocation(tz)
    return t.In(loc).Format("2006-01-02")
}

func formatDateTime(t time.Time, tz string) string {
    if t.IsZero() { return "-" }
    if tz == "" { tz = "UTC" }
    loc, _ := time.LoadLocation(tz)
    return t.In(loc).Format("2006-01-02 15:04")
}
```

模板调用改为：
```html
{{formatDate .CreatedAt $.Timezone}}
{{formatDateTime .CreatedAt $.Timezone}}
```

### 6.2 JS 动态渲染改造

每个需要时区感知的页面：

```html
<script>
window.userTimezone = '{{.Timezone}}';

function formatDateTime(dateStr) {
    if (!dateStr) return '-';
    var d = new Date(dateStr);
    var tz = window.userTimezone || 'UTC';
    var p = { timeZone: tz, year: 'numeric', month: '2-digit', day: '2-digit', 
              hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false };
    var parts = {};
    ['year','month','day','hour','minute','second'].forEach(function(k) {
        parts[k] = d.toLocaleString('en-US', Object.assign({[k]: p[k]}, {timeZone: p.timeZone})).replace(/\D/g,'');
    });
    return parts.year + '-' + parts.month + '-' + parts.day + ' ' + parts.hour + ':' + parts.minute + ':' + parts.second;
}
</script>
```

### 6.3 JS `toLocaleString()` 改造

所有 `new Date(x).toLocaleString()` 改为：
```javascript
new Date(x).toLocaleString('en-US', {timeZone: window.userTimezone || 'UTC'})
```

---

## 七、实施结果

### 7.1 已修改文件（21 个）

| 优先级 | 文件 | 修改内容 |
|--------|------|----------|
| **Backend** | `internal/web/helpers.go` | `formatDate`/`formatDateTime` 改为 variadic，支持可选 timezone 参数，向后兼容 |
| **Backend** | `internal/web/handler.go` | `RenderPartial`/`Partial` 自动注入 Timezone/Locale/Currency/ExchangeRate |
| **P0** | `my-subscriptions.html` | JS `formatDate()` → 时区感知 |
| **P0** | `redemption.html` | JS `formatDate()` → 时区感知 |
| **P0** | `invitation.html` | JS `toLocaleString()`/`toLocaleDateString()` → 时区感知 |
| **P0** | `invoices.html` | JS `toLocaleDateString()` → 时区感知 |
| **P1** | `admin-audit-logs.html` | Go `formatDateTime` → 传入 `$.Timezone` |
| **P1** | `admin-enterprise.html` | Go `.Format` + JS `toLocaleString()` → 时区感知 |
| **P1** | `admin-announcements.html` | JS `toLocaleString()` → 时区感知 |
| **P1** | `admin-invitations.html` | JS `toLocaleString()`/`toLocaleDateString()` → 时区感知 |
| **P1** | `admin-monitor-upstream.html` | Go `.Format` → `formatDateTime` + `$.Timezone` |
| **P1** | `admin-monitor-downstream.html` | Go `.Format` → `formatDateTime` + `$.Timezone` |
| **P2** | `admin-users.html` | Go `formatDate` → 传入 `$.Timezone` |
| **P2** | `admin-keypool.html` | Go `formatDate` → 传入 `$.Timezone` |
| **P2** | `admin.html` | Go `formatDate`/`formatDateTime` → 传入 `$.Timezone` |
| **P2** | `dashboard.html` | Go `formatDate` → 传入 `$.Timezone` |
| **P2** | `super/admin-management.html` | Go `formatDate` → 传入 `$.Timezone` |
| **P2** | `enterprise/keys.html` | Go 直接输出 → `formatDateTime` + `$.Timezone` |
| **P2** | `enterprise/billing.html` | Go `.Format` → `formatDateTime` + `$.Timezone` |
| **P2** | `partials/audit-log-detail.html` | Go 直接输出 → `formatDateTime` + `.Timezone` |
| **P2** | `admin-redemption.html` | JS `toLocaleDateString()` → 时区感知 |

### 7.2 关键技术决策

1. **Go helpers 向后兼容**：`formatDate(t time.Time, tz ...string)` 变参设计，旧调用 `{{formatDate .CreatedAt}}` 无需改动（默认 UTC），新调用 `{{formatDate .CreatedAt $.Timezone}}` 使用时区
2. **Partial 自动注入**：`RenderPartial`/`Partial` 现在自动为 `gin.H` 注入 Timezone/Locale/Currency/ExchangeRate，所有 partial 模板都能使用时区
3. **JS 统一模式**：所有 JS 页面添加 `window.userTimezone = '{{.Timezone}}'`，`formatDate()`/`formatDateTime()` 使用 `toLocaleString('en-US', {timeZone})` 提取各部分

### 7.3 Bug 修复（实施后）

| Bug | 原因 | 修复 |
|-----|------|------|
| audit-log-detail ISE | `HandleAuditLogDetail` 传了字符串 `CreatedAt`，但 `formatDateTime` 需要 `time.Time` | 改为传 `createdAt`（time.Time） |
| orders 分钟缺零（17:3） | `toLocaleString('en-US', {minute: '2-digit'})` 单独使用时浏览器不补零 | 统一改用 parts extraction 模式 |
| billing-details 同样隐患 | 同上 | 同上 |

### 7.4 编译与测试

- `go build ./...` ✅ 编译通过
- E2E 全量回归：**18/23 通过** ✅
- **5 个失败** 全部是 enterprise admin 登录测试（缺少 `e2e-user@example.com` 测试用户，与本次修改无关）

---

*Report generated by code audit on 2026-05-26*
