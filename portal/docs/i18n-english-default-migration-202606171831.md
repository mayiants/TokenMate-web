# i18n 英文默认改造方案与结果

> 本文档记录将 ai-portal-go 前端 UI chrome 从“中文 key + 英文翻译”切换为“英文 key + 中文翻译”的改造方案、实施结果和后续计划。
> 时间戳：2026-06-17 18:31

---

## 1. 背景与目标

### 1.1 改造前的问题

- 模板中大量使用中文 key（如 `{{t "确定要删除吗？"}}`）。
- `en.json` 里塞满了从中文翻译而来的英文值。
- 新增英文文案需要走“中文 → 英文”的逆向流程，容易 awkward。
- 大段 Terms/Privacy/Refund 条款也塞进 `zh.json`/`en.json`，导致 JSON 臃肿、法务难以审阅。

### 1.2 目标（Plan B）

- **UI chrome（导航、标题、按钮、表格列、状态、提示）**：统一使用英文 phrase 作为 key，例如 `{{t "Delete"}}`、`{{t "Are you sure you want to delete this user?"}}`。
- `zh.json`：英文 key → 中文翻译。
- `en.json`：英文 key → 英文自身（identity fallback）。
- **大段条款/政策类文案**：不进入 i18n JSON，改用按语言分文件的 HTML 文档，由页面按 locale 直接读取。

---

## 2. 整体范围与统计

截至 2026-06-18，模板/静态 JS/Go 代码中实际在用的 i18n key 统计：

| 范围 | 总 key 数 | 已转英文 | 仍中文 |
|------|----------|----------|--------|
| layout / 导航 | 43 | 43 | 0 |
| super | 54 | 54 | 0 |
| enterprise | 257 | 257 | 0 |
| admin | 777 | 777 | 0 |
| user（含条款页面标题/链接） | 520 | 520 | 0 |
| 静态 JS（admin/enterprise/common） | 68 | 68 | 0 |
| 确认弹窗变量后置重构新增 key | ~14 | ~14 | 0 |
| **合计** | **~1,733** | **~1,733** | **0** |

- 已完成的 UI chrome 整改覆盖 **约 1,733 个 key**。
- 模板、静态 JS、Go 代码中正在使用的中文 key 已降至 **0**。
- `zh.json` 中**中文 key 总数为 0**，中文孤儿 key 数为 **0**。
- `zh.json`/`en.json` 中已清理完全部 **681 个总孤儿 key**；当前孤儿 key 数为 **0**。
- `en.json`/`zh.json` 当前总 key 数：**1,945**。

`zh.json`/`en.json` 体积已从改造前的数百 KB 降至约 112 KB。

> **补充**：2026-06-18 完成了 admin/enterprise/subscription/keys/users/provider 等确认弹窗的“变量后置”重构：将 `Are you sure you want to {action} plan "{name}"?` 改为 `Are you sure you want to disable this plan?` + `\n\n` + `planName`，避免句子中间插入变量，也避免 `\"` 转义导致审计脚本误报。

---

## 3. 分阶段实施

### 3.1 P0：交互层硬编码中文（已完成）

**范围**：admin / enterprise / user 页面中未包裹的确认弹窗、错误提示、JS 提示文案。

**处理方式**：将硬编码中文字符串改为 `_t('English phrase')`，并在 `zh.json`/`en.json` 中补翻译。

**主要文件**：

- `web/templates/admin-groups.html`
- `web/templates/admin-subscriptions.html`
- `web/templates/admin-keypool.html`
- `web/templates/admin-enterprise.html`
- `web/templates/admin-providers.html`
- `web/templates/admin-reconciliation.html`
- `web/templates/enterprise/users.html`
- `web/templates/enterprise/keys.html`
- `web/templates/apikeys.html`
- `web/templates/invoices.html`
- `web/templates/subscriptions.html`
- `web/templates/my-subscriptions.html`
- `web/templates/plan-and-group.html`

**示例**：

```html
<!-- before -->
showConfirm('删除Key', '确定要删除此Key吗？', function() { ... });

<!-- after -->
showConfirm(_t('Delete Key'), _t('Are you sure you want to delete this key?'), function() { ... });
```

### 3.2 P1：非条款类剩余中文 key（已完成）

**范围**：仍在模板中使用、但不属于条款政策类的中文 key。

**主要类别**：

- layout 全局弹窗按钮：`nav_提示`、`nav_确定`、`nav_确认_3/4`、`nav_取消`、`nav_公告`、`nav_我知道了`、`ui_跳过`
- 审计日志详情标签：`label_时间`、`label_用户`、`label_分类`、`ui_操作`、`label_级别`、`label_消息`、`label_详细信息`
- 分页文案：`页`
- 企业分组说明：`ui_企业内部管理的分组...`
- 订阅提示：`ui_如果您只是暂时不需要...`
- 支付渠道标签：`label_微信支付`、`label_支付宝`

**示例**：

```html
<!-- before -->
{{t "nav_提示"}}

<!-- after -->
{{t "Notice"}}
```

### 3.3 P2：大段条款/政策类文案（已完成）

**范围**：

- `landing.html` 的 Terms of Service、Privacy Policy、Security、Acceptable Use、Billing 说明
- `billing.html` 的充值、退款、发票、余额有效期等政策
- `invoices.html` 的发票处理时间说明

**方案：文档直读，不走 i18n 翻译。**

#### 为什么用文档直读？

| 维度 | i18n JSON 方案 | 文档直读方案 |
|---|---|---|
| 内容长度 | 长段落塞进 key，污染翻译文件 | 文档独立管理，key 只保留标题/链接 |
| 法律审阅 | diffs 难看，法务不好审 | HTML 可直接走法务/合规流程 |
| 更新频率 | 发版需改代码/JSON | 文档可热更新，无需重启 |
| 多格式 | 只能纯文本 | 可支持 HTML、PDF 下载 |
| 版本控制 | 历史埋在 git 大 JSON | 每份文档独立版本，清晰 |

#### 实现

1. **存储位置**：`web/static/docs/`

   ```
   web/static/docs/
   ├── terms-of-service-en.html
   ├── terms-of-service-zh.html
   ├── privacy-policy-en.html
   ├── privacy-policy-zh.html
   ├── payment-terms-en.html
   └── payment-terms-zh.html
   ```

2. **加载方式**：前端 `loadPolicyDoc(container, docName)` 根据 `<html lang="...">` 拉取对应语言文档，失败时 fallback 到英文版。

   ```js
   function loadPolicyDoc(container, docName) {
     if (!container) return;
     var lang = document.documentElement.lang || 'en';
     var url = '/static/docs/' + docName + '-' + lang + '.html';
     fetch(url)
       .then(function(r) { if (!r.ok) throw new Error('load failed'); return r.text(); })
       .then(function(html) { container.innerHTML = html; })
       .catch(function() {
         fetch('/static/docs/' + docName + '-en.html')
           .then(function(r) { return r.text(); })
           .then(function(html) { container.innerHTML = html; })
           .catch(function() {
             container.innerHTML = '<p class="text-muted">' + _t('Document not available') + '</p>';
           });
       });
   }
   ```

3. **渲染方式**：HTML 片段直接注入模态框容器；i18n 只保留短标签。

4. **i18n 保留内容**：
   - 只保留短标签，例如 `"Terms of Service"`、`"Privacy Policy"`、`"Payment Terms"`、`"Last updated"`、`"Document not available"`。
   - 条款正文不再进入 `zh.json`/`en.json`。

5. **清理收益**：
   - P2 完成后，模板中正在使用的中文 key 降至 **0**。
   - 约 **2,550** 个中文孤儿 key 已从 `zh.json`/`en.json` 删除。

---

## 4. 孤儿 key 清理

### 4.1 清理前

- `zh.json` 中约有 **2,550** 个中文 key 已不在任何模板/静态 JS 中使用。
- 清单输出到 `docs/orphan-chinese-i18n-keys.md`。

### 4.2 清理过程

- 运行清理脚本，从 `zh.json`/`en.json` 中删除这些中文孤儿 key。
- 清理后发现 **38** 个中文 key 在 `web/static/js/` 中实际通过 `_t()` 使用，但被脚本误判为孤儿（因为脚本最初只扫描了模板）。
- 已重新将这些 key 补回 `zh.json`/`en.json`，并补全对应翻译。

### 4.3 静态 JS 中文 key 清理

- 使用 `scripts/convert_static_js_i18n.py` 将 `docs/remaining-chinese-i18n-keys.md` 中的 **63** 个中文 key 全部替换为英文 phrase key。
- 过程中还发现 5 个仅通过 `app-layout.js` 内联回退表 `_i18nMessages` 使用的中文 key：`共 `、`将 `、`状态`、`正在导出...`、`AWS 格式: access_key_id:secret_access_key (将被加密存储)`，也一并转换并补翻译。

### 4.4 清理后

- `docs/orphan-chinese-i18n-keys.md` 当前为 **0** 个中文孤儿 key。
- `docs/remaining-chinese-i18n-keys.md` 当前为 **0** 个仍在使用的中文 key。
- `zh.json` 中中文 key 总数：**0**。

---

## 5. 已产生的辅助文件

| 文件 | 说明 |
|------|------|
| `scripts/admin_i18n.py` | admin 页面 UI chrome 批量迁移脚本 |
| `scripts/enterprise_i18n.py` | enterprise 页面 UI chrome 批量迁移脚本 |
| `scripts/super_i18n.py` | super 页面 UI chrome 批量迁移脚本 |
| `scripts/remaining_i18n.py` | P0/P1 剩余中文片段和 key 的迁移脚本 |
| `scripts/convert_static_js_i18n.py` | 静态 JS 中最后 63 个中文 key 批量转换脚本 |
| `web/static/docs/privacy-policy-{en,zh}.html` | 隐私政策文档 |
| `web/static/docs/terms-of-service-{en,zh}.html` | 服务条款文档 |
| `web/static/docs/payment-terms-{en,zh}.html` | 支付与退款政策文档 |
| `docs/orphan-chinese-i18n-keys.md` | 中文孤儿 key 清单（当前 0 条） |
| `docs/remaining-chinese-i18n-keys.md` | 仍在使用的中文 key 清单（当前 0 条） |
| `docs/i18n-english-default-migration-202606171831.md` | 本文档 |

---

## 6. 验证记录

### 6.1 单元测试

```bash
go test ./internal/web/... ./pkg/...
# ✅ passed
```

### 6.2 E2E 测试

| 测试文件 | 结果 |
|---|---|
| `ui-all-pages.spec.js` | 39/39 passed ✅ |
| `ui-console-error-scan.spec.js` | 37/37 passed ✅ |
| `ui-admin-field-roundtrip.spec.js` | 28/28 passed ✅ |

> 全量合跑时因总时长超过 300s 被 shell 超时中断，拆分后剩余单测与 console-error 扫描均通过，无失败用例。

---

## 7. 后续行动清单

- [x] 静态 JS 中文 key 清理：将 `docs/remaining-chinese-i18n-keys.md` 中的 63 个中文 key 改为英文 phrase key。
- [x] 确认弹窗变量后置重构：admin/enterprise/subscription/keys/users/provider 等 disable/enable/delete 弹窗已改为“通用描述 + `\n\n` + 变量名”模式。
- [x] 警示图标规范：已移除 enterprise 删除等非关键操作前的 `⚠️` 图标，仅在删除用户 profile 等极关键场景保留。
- [x] 评估并清理剩余 **681** 个英文孤儿 key。
- [x] 迁移/清理 `app-layout.js` 内联 `_i18nMessages` 表（已清空，英文 locale 直接加载 `en.json`）。
- [x] 删除 `ui-chrome.json` 与 `i18n-messages.json`（确认无代码引用）。
- [x] 邮件模板 i18n 化（`pkg/mail` 已迁移到 `pkg/i18n`）。
- [x] 新用户默认语言改为英文：`getLocale` / `GetLocale` 在无 cookie 且浏览器非中文时返回 `en`；中文浏览器仍按 `Accept-Language` 返回 `zh`。DB schema、用户表默认值、`default_locale` system setting 同步改为 `en`。
- [ ] 条款文档内容最终由法务/合规审阅，必要时从当前占位文案替换为正式版本。

---

## 8. 关键原则

1. **UI chrome 用英文 key**：短文案、按钮、标签、状态、提示统一英文 phrase。
2. **条款政策用文档**：长文本走按语言分文件的 HTML/PDF，不走 i18n JSON。
3. **避免在 JS 单引号字符串里使用英文撇号**：如必须，改用 `"` 包裹或去除撇号，防止 Go `html/template` 上下文错误。
4. **变量尽量后置，避免在 key 中使用 `{name}` 和转义引号**：
   - 推荐：`_t('Are you sure you want to delete this plan?') + '\n\n' + planName`
   - 不推荐：`_t('Are you sure you want to {action} plan \"{name}\"?')`（`\"` 容易导致审计/模板转义问题）
5. **改完即测**：每次批量改动后跑 `go test` + `ui-all-pages` + `ui-console-error-scan` + `ui-admin-field-roundtrip`。
6. **清理前导出清单**：删除孤儿 key 前先生成 markdown 清单，便于核对和回滚。
