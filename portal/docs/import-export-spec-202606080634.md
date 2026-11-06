# 导入/导出功能规格说明

> 生成时间：2026-06-08 06:34 UTC　|　更新时间：2026-06-08  
> 适用分支：`enterprise-cardification`

---

## 一、导出功能 (Export)

### 1.1 功能清单

| 页面 | 导出项 | 后端 API | 前端实现方式 | 文件格式 |
|------|--------|----------|-------------|----------|
| **billing-details.html** | Realtime 消费明细 | `GET /api/billing/realtime/export` | `downloadExport` | CSV |
| **billing-details.html** | Monthly 月账单 | `GET /api/billing/monthly/export` | `downloadExport` | CSV |
| **billing-details.html** | Requests 请求明细 | `GET /api/billing/requests/export` | `downloadExport` | CSV |
| **billing-details.html** | Recharge Records 充值记录 | `GET /api/billing/recharge-records/export` | `downloadExport` | CSV |
| **orders.html** | 订单列表 | `GET /api/orders/export` | `downloadExport` | CSV |
| **admin-upstream-usage.html** | 上游用量明细 | `GET /api/admin/upstream-usage/export` | `downloadExport` | CSV |
| **admin-provider-health.html** | 供应商健康状态 | `GET /api/admin/provider-health/export` | `downloadExport` | CSV |
| **admin-audit-logs.html** | 审计日志 | `GET /api/admin/audit-logs/export` | `downloadExport` | CSV |
| **admin-keypool.html** | Keypool 批量导出 | `POST /api/admin/keypool/export` | `fetch` + JSON → 前端组装 CSV | CSV |
| **enterprise/usage.html** | 企业用量统计 | `GET /api/enterprise/usage/export` | `<a>` 链接 | CSV |
| **enterprise/audit.html** | 企业审计日志（角色/配额） | `GET /api/enterprise/audit/export` | `<a>` 链接 | CSV |
| **profile.html** | 个人数据导出 (GDPR) | `GET /api/gdpr/export` | `fetch` + JSON → Blob | JSON |
| **super/payment.html** | 支付配置导出 | — | 前端本地组装 JSON | JSON |

### 1.2 后端 CSV 导出统一规范

所有 CSV 导出后端 handler 遵循以下规范：

```go
// 1. 设置响应头
c.Header("Content-Type", "text/csv; charset=utf-8")
c.Header("Content-Disposition", "attachment; filename=<prefix>-<timestamp>.csv")

// 2. 写入 UTF-8 BOM（确保 Excel 正确识别中文）
c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

// 3. 写入表头（使用 i18n 翻译）
c.Writer.WriteString(T("csv.header.xxx", c) + ",...\n")

// 4. 逐行写入数据
for rows.Next() {
    // ... scan & write
}
```

**文件名格式**：`<prefix>-YYYYMMDD-HHMMSS.csv`，例如 `billing-realtime-20240607-144941.csv`。

---

## 二、导入功能 (Import)

### 2.1 功能清单

| 页面 | 导入项 | 后端 API | 前端实现方式 | 文件格式 |
|------|--------|----------|-------------|----------|
| **admin-keypool.html** | Keypool 批量导入 | `POST /api/admin/keypool/import` | Drawer + FormData | CSV / TXT |
| **admin-models-general.html** | Provider 模型同步 | — | Tab 内嵌表单 | — |
| **admin-models-general.html** | OpenRouter 模型导入 | — | Tab 内嵌表单 | — |
| **admin-models-aws.html** | AWS 模型从 Provider 导入 | — | Select + 按钮触发 | — |

### 2.2 Keypool 导入详细流程

1. 点击"批量导入"打开 Drawer
2. 选择导入模式：
   - **单供应商模式**：选择一个 provider，上传 key 列表（每行一个 key）
   - **混合供应商模式**：上传 CSV，格式为 `provider,key,base_url,rpm_limit,tpm_limit,enabled`
3. 前端通过 `FormData` POST 到 `/api/admin/keypool/import`
4. 后端返回导入结果：`{ imported: N, skipped: N }`

---

## 三、导出方式不统一问题评估

### 3.1 现状：三种实现方式并存

当前项目使用了 ~~3 种~~ 已统一为以 `downloadExport` 为主的前端导出实现方式：

| 方式 | 使用页面 | 代码示例 |
|------|----------|----------|
| **`downloadExport`** (推荐，已统一) | billing-details, orders, admin-upstream-usage, admin-provider-health, admin-audit-logs | `downloadExport(url, filename)` |
| **`fetch` + `blob` + `a.download`** | admin-keypool (POST + 密码验证), profile (JSON → Blob) | 保留独立实现（需 POST 或 JSON 处理） |
| **`<a>` 链接** | enterprise/usage, enterprise/audit | `<a href="/api/enterprise/usage/export">` |
| ~~`window.location.href`~~ | ~~billing-details (Realtime/Monthly/Requests)~~ | **已迁移** |
| ~~`window.open`~~ | ~~admin-upstream-usage, admin-provider-health, admin-audit-logs~~ | **已迁移** |

### 3.2 各方式优劣对比

| 维度 | A. `location.href` / `open` | B. `fetch` + `blob` | 推荐场景 |
|------|---------------------------|---------------------|----------|
| **代码复杂度** | ⭐ 极简（1 行） | ⭐⭐⭐ 较复杂（10+ 行） | 简单导出 → A |
| **错误处理** | ❌ 差（404/500 打开空白页或报错页） | ✅ 好（可 catch 并显示 toast） | 需要错误提示 → B |
| **自定义文件名** | ❌ 完全依赖后端 `Content-Disposition` | ✅ 前端可自由设置 | 需要动态文件名 → B |
| **Loading 状态** | ❌ 无（用户不知道是否正在下载） | ✅ 可显示 loading / spinner | 大数据量 → B |
| **大文件性能** | ✅ 流式下载，不占用前端内存 | ❌ 需完整加载到内存（blob） | 大文件 > 10MB → A |
| **跨域兼容性** | ✅ 浏览器自动处理 | ✅ 需 CORS 支持 | 两者都行 |
| **POST 请求支持** | ❌ 不支持 | ✅ 支持（如 keypool 导出需密码） | 需要 POST → B |

### 3.3 为什么不统一？历史原因

1. **billing-details 的 Realtime/Monthly/Requests**：最早实现时为了简单，直接用 `window.location.href`。这些导出数据量不大，且不需要错误提示。
2. **billing-details 的 Recharge**：后来新增功能，开发者选择了 `fetch` + `blob`，可能是为了统一错误处理或自定义文件名。
3. **orders**：使用 `fetch` + `blob`，因为需要在导出前组装 filter 参数，且希望有错误提示。
4. **admin 页面**：`window.open` 是简单直观的方式，适合管理员快速导出。
5. **enterprise 页面**：`<a>` 链接是最简单的实现，适合不需要动态参数的场景。

### 3.4 统一方案建议

#### 方案一：全部统一为 `fetch` + `blob`（推荐）

**优点**：
- 统一的错误处理（所有导出失败都能显示 toast）
- 统一的前端文件名控制
- 可统一添加 loading 状态
- 代码可复用（封装一个 `downloadCSV(url, filename)` 工具函数）

**缺点**：
- 大文件（> 10MB）会占用前端内存
- 代码量增加

**实现**：

```javascript
// app-common.js 中新增通用导出函数
function downloadExport(url, filename) {
  showToast('info', _t('正在导出...'));
  fetch(url)
    .then(function(r) {
      if (!r.ok) throw new Error('export failed');
      return r.blob();
    })
    .then(function(blob) {
      var downloadUrl = window.URL.createObjectURL(blob);
      var a = document.createElement('a');
      a.href = downloadUrl;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      window.URL.revokeObjectURL(downloadUrl);
      showToast('success', _t('导出成功'));
    })
    .catch(function(e) {
      showToast('error', _t('导出失败，请重试'));
    });
}
```

然后各页面统一调用：
```javascript
// billing-details
function exportRealtime() {
  downloadExport('/api/billing/realtime/export', 'billing-realtime-' + formatTimestamp() + '.csv');
}

// admin-upstream-usage
function exportCSV() {
  var url = '/api/admin/upstream-usage/export?...';
  downloadExport(url, 'upstream-usage-' + formatTimestamp() + '.csv');
}
```

#### 方案二：全部统一为 `window.location.href`

**优点**：
- 极简代码
- 大文件流式下载，性能好

**缺点**：
- 无法错误处理
- 无法自定义文件名（必须完全依赖后端）
- 无法显示 loading

**适用场景**：如果所有导出都是小文件（< 1MB）且不需要复杂交互。

#### 方案三：混合策略（按场景选择）

| 场景 | 推荐方式 |
|------|----------|
| 小文件 + 需要错误提示/自定义文件名 | `fetch` + `blob` |
| 大文件（> 10MB）或简单导出 | `window.location.href` / `window.open` |
| 不需要参数的动态链接 | `<a>` 标签 |
| 需要 POST body（如密码验证） | `fetch` + `blob` |

**结论**：✅ **方案一已实施** — `downloadExport()` 已封装到 `app-common.js`（`?v=3`），所有 GET 导出页面已统一调用。对于需要 POST body（keypool 密码验证）或大文件场景，保留独立实现。

---

## 四、已知问题记录

| 问题 | 状态 | 修复 Commit |
|------|------|------------|
| Recharge Records 导出缺少 `WHERE` 导致 SQL 语法错误 | ✅ 已修复 | `060dbf4` |
| Audit Logs 导出 URL 缺少 `/api` 前缀 | ✅ 已修复 | `b3f0e33` |
| Provider Health 导出后端路由缺失 | ✅ 已修复 | `b3f0e33` |
| Keypool 导出未提供 operation password | ✅ 已修复 | `b3f0e33` |
| 导出文件名无时间戳，同一天多次导出会覆盖 | ✅ 已修复 | `cb16aaf` |
| 分页在记录数 ≤ 20 时仍显示 | ✅ 已修复 | `56b02d7` |
| Recharge Records `formatDateTime` 日期格式错乱 | ✅ 已修复 | `77b0f8f` |
| 4 个 CSV 导出缺少 i18n / BOM / charset（usage、upstream-usage、provider-health、audit） | ✅ 已修复 | `3393284` |
| upstream-usage / audit 导出 header 在 body 之后，浏览器显示文本而非下载 | ✅ 已修复 | `0c6b785` |
| 前端导出实现不统一（`window.open` / `location.href` / `fetch+blob` 混用） | ✅ 已修复 | `app-common.js?v=3` |

---

## 五、统一实现记录

### 5.1 新增 `downloadExport()` 通用函数

**文件**：`web/static/js/app-common.js?v=3`

```javascript
function formatExportTimestamp() {
  var d = new Date();
  var pad = function(n) { return n < 10 ? '0' + n : '' + n; };
  return '' + d.getFullYear() + pad(d.getMonth() + 1) + pad(d.getDate()) + '-' +
         pad(d.getHours()) + pad(d.getMinutes()) + pad(d.getSeconds());
}

function downloadExport(url, filename) {
  showToast('info', _t('正在导出...'));
  fetch(url, { credentials: 'same-origin' })
    .then(function(r) {
      if (!r.ok) throw new Error('export failed');
      return r.blob();
    })
    .then(function(blob) {
      var downloadUrl = window.URL.createObjectURL(blob);
      var a = document.createElement('a');
      a.href = downloadUrl;
      a.download = filename || 'export-' + formatExportTimestamp() + '.csv';
      document.body.appendChild(a);
      a.click();
      a.remove();
      window.URL.revokeObjectURL(downloadUrl);
      showToast('success', _t('导出成功，文件已下载'));
    })
    .catch(function(e) {
      showToast('error', _t('导出失败，请重试'));
    });
}
```

### 5.2 已迁移页面

| 页面 | 原实现 | 迁移后 |
|------|--------|--------|
| `billing-details.html` — exportRealtime | `window.location.href` | `downloadExport('/api/billing/realtime/export', 'billing-realtime-' + formatExportTimestamp() + '.csv')` |
| `billing-details.html` — exportMonthly | `window.location.href` | `downloadExport(url, 'billing-monthly-' + ...)` |
| `billing-details.html` — exportRequests | `window.location.href` | `downloadExport(url, 'billing-requests-' + ...)` |
| `billing-details.html` — exportRechargeRecords | `fetch` + `blob` | `downloadExport(url, 'recharge-records-' + ...)` |
| `orders.html` — exportOrders | `fetch` + `blob` | `downloadExport(url, 'orders-' + ...)` |
| `admin-upstream-usage.html` — exportCSV | `window.open` | `downloadExport(url, 'upstream-usage-' + ...)` |
| `admin-provider-health.html` — exportCSV | `window.open` | `downloadExport(url, 'provider-health-' + ...)` |
| `admin-audit-logs.html` — 导出按钮 | `window.open` | `downloadExport(url, 'audit-logs-' + ...)` |

### 5.3 保留独立实现的页面

| 页面 | 原因 | 实现方式 |
|------|------|----------|
| `admin-keypool.html` | 需要 POST + operation password 验证 | `fetch` + JSON → 前端组装 CSV |
| `profile.html` (GDPR) | 导出 JSON 非 CSV | `fetch` + JSON → Blob |
| `enterprise/usage.html` / `enterprise/audit.html` | 简单 `<a>` 链接，无动态参数 | 保持 `<a>` |

---

## 六、后端路由汇总

### 导出路由

```
GET  /api/orders/export
GET  /api/billing/realtime/export
GET  /api/billing/monthly/export
GET  /api/billing/requests/export
GET  /api/billing/recharge-records/export
GET  /api/admin/upstream-usage/export
GET  /api/admin/provider-health/export
GET  /api/admin/audit-logs/export
GET  /api/admin/keypool/export
POST /api/admin/keypool/export          # 带密码验证
GET  /api/admin/key-pool/export         # 兼容路由（同 keypool/export）
POST /api/admin/key-pool/export         # 兼容路由（同 keypool/export）
GET  /api/enterprise/usage/export
GET  /api/enterprise/audit/export
GET  /api/gdpr/export
```

### 导入路由

```
POST /api/admin/keypool/import
POST /api/admin/key-pool/import
```
