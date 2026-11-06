# i18n 全面审计报告与修复建议

> **ACTIVE** - 基于 2026-06-17 代码快照的全量 i18n 审计
> 
> 关联文档：
> - [i18n English-Default Migration](./i18n-english-default-migration-202606171831.md)
> - [Enterprise Cardification Fixes Summary](./enterprise-cardification-fixes-summary-202606161730.md)
> - [Project Comprehensive Review](./project-comprehensive-review-202606132130.md)

---

## 1. 执行摘要

i18n English-default 迁移的**正式系统**（`en.json`/`zh.json` + `{{t "key"}}`）已完整：2,402 key 完全同步，模板中 0 遗漏。

但审计发现 **三个重大盲区**，可能导致用户在特定场景下看到未翻译的中文：

| 盲区 | 规模 | 严重度 | 影响范围 |
|------|------|--------|---------|
| JS 内联回退表硬编码中文 | 916 行 / 782 个中文 key | ❌ 高 | 英文 locale 下 JS 弹窗/提示 |
| Go 后端 API 错误消息硬编码中文 | 228 行 | ❌ 高 | API 响应、toast、flash 消息 |
| 缺乏自动化持续检测机制 | — | ⚠️ 中 | 新代码可能引入新遗漏 |

---

## 2. 当前 i18n 架构全景

### 2.1 三套并行的翻译系统

```
┌─────────────────────────────────────────────────────────────────┐
│  系统 A：Go 模板端 {{t "key"}}                                   │
│  ─────────────────────────────────────                          │
│  翻译源：web/i18n/en.json + zh.json（各 2,402 keys）            │
│  加载：internal/web/i18n.go → LoadTranslations()                │
│  调用：{{t "key"}}（1,178 unique keys, 56 个模板文件）           │
│  状态：✅ 完全同步，0 个 key 不匹配，0 个中文 key               │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│  系统 B：JS 前端端 _t('key')                                     │
│  ─────────────────────────────────────                          │
│  翻译源：app-layout.js 内联 _i18nMessages 表（~905 entries）    │
│         + zh.json（通过 XHR 同步加载）                           │
│  调用：_t('key')（67 unique keys in 静态 JS）                   │
│        + 模板内联 JS 中的 _t()（~793 次调用）                    │
│  状态：⚠️ 内联表仍有 782 个中文 key                            │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│  系统 C：Go 后端直出（硬编码）                                    │
│  ─────────────────────────────────────                          │
│  228 行中文：API 错误消息、flash/toast、seed 数据、settings      │
│  状态：❌ 未经 i18n 系统                                        │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 _t() 函数工作机制

`_t()` 定义在 `web/static/js/app-layout.js:1084`：

```javascript
function _t(text) {
    // Proper nouns (Token, RPM, API, etc.) should never be translated
    if (window._properNouns && window._properNouns[text]) return text;
    var lang = document.documentElement.lang || 'zh';
    if (lang === 'en') {
      // English locale: use inline fallback table
      var msg = window._i18nMessages[text];
      return msg !== undefined ? msg : text;
    }
    // Chinese locale: use zh.json (English keys -> Chinese)
    var zh = window._i18nMessagesZh[text];
    return zh !== undefined ? zh : text;
}
```

**关键问题**：英文 locale 下，`_t()` 从 `window._i18nMessages` 查找翻译。该表在 `app-layout.js:12` 处以**中文为 key** 内联定义（782 个中文 key → 英文值）。这意味着：

1. JS 代码中调用 `_t('删除')` 时，英文 locale 返回 `'Delete'`，中文 locale 从 `zh.json` 查找
2. 但如果 JS 代码中**直接硬编码中文**而不调用 `_t()`，则永远不会被翻译

### 2.3 Locale 检测机制

Go 端（`internal/web/handler.go` 和 `internal/routes/i18n.go`）：
1. Cookie `locale`
2. `Accept-Language` header
3. Query parameter `lang`
4. Default: `"zh"`

---

## 3. 量化审计结果

### 3.1 正式翻译系统（✅ 健康）

| 指标 | 数值 | 状态 |
|------|-----:|------|
| en.json key 数量 | 2,402 | — |
| zh.json key 数量 | 2,402 | — |
| key 集合是否一致 | 完全一致 | ✅ |
| 空值 key | 0 | ✅ |
| 模板 `{{t}}` key 缺失翻译 | 0 | ✅ |
| JS `_t()` key 缺失翻译 | 0 | ✅ |
| 模板中硬编码中文（非注释） | 0 | ✅ |
| 模板中硬编码中文（注释） | 2 行 | ✅ 忽略 |

### 3.2 盲区一：JS 文件硬编码中文（❌ 高严重度）

**1,108 行** JS 代码包含未用 `_t()` 包裹的中文：

| 文件 | 硬编码中文行数 | 说明 |
|------|-------------:|------|
| `app-layout.js` | **916** | 内联 `_i18nMessages` 回退表 + showMessage/showConfirm 硬编码 |
| `page-admin-models.js` | 58 | 模型管理操作消息 |
| `page-admin-keypool.js` | 42 | Key 池操作消息 |
| `flatpickr/l10n/zh.js` | 36 | 日期选择器中文 locale（✅ 合理，不需翻译） |
| `page-admin-enterprise.js` | 23 | 企业管理操作消息 |
| `page-admin-models-aws.js` | 15 | AWS 模型管理 |
| `page-admin-providers.js` | 13 | 供应商管理 |
| `table-pagination.js` | 5 | 分页文案 |

**典型示例**（`page-admin-enterprise.js`）：

```javascript
// ❌ 硬编码中文，英文用户会直接看到
showMessage('创建成功', '企业已创建');
showMessage('创建失败', result.error ? result.error.message : '未知错误');
showConfirm('确认移除', '确定要移除此成员吗？', function() { ... });
showConfirm('确认删除', '⚠️ 确定要永久删除企业 "' + name + '" 吗？\n\n此操作将：\n- 移除所有成员的企业关联\n- 删除企业配额和设置\n- 此操作不可撤销', function() { ... });
```

### 3.3 盲区二：Go 后端硬编码中文（❌ 高严重度）

**228 行** Go 代码包含面向用户的中文字符串：

| 文件 | 类型 | 示例 |
|------|------|------|
| `routes/admin_models.go` | API 错误消息 | `"请先在 admin/keypool 添加一个 Amazon (AK/SK) 上行key并验证通过"` |
| `routes/enterprise_members.go` | 邀请/注册 flash | `"请填写所有必填字段"`, `"邀请链接无效或已过期"` |
| `routes/settings.go` | 设置分类名 | `"运营设置"`, `"监控设置"`, `"令牌设置"` |
| `routes/payment.go` | 模拟充值 toast | `"充值成功（模拟模式）"` |
| `routes/billing.go` | SQL 产品类别 | `'模型推理' as product_type` |
| `web/admin_handler_models.go` | 模型操作 flash | `"模型已创建"`, `"更新失败"` |
| `web/auth.go` | 认证 toast | `"请输入邮箱地址"`, `"验证码已发送"` |
| `db/seed.go` | 种子数据 | `'新用户注册欢迎奖励'` |
| `routes/vouchers_admin.go` | 种子数据 | `'欢迎奖励'` |

### 3.4 盲区三：i18n-messages.json 中文 key（⚠️ 中严重度）

`web/static/js/i18n-messages.json` 包含 **905 个 key**，其中 **782 个以中文为 key**：

```json
{
  " 个": " items",
  " 个Key, 跳过 ": " keys, skipped ",
  "确认删除": "Confirm Delete",
  "确定要删除供应商 ": "Are you sure you want to delete provider "
}
```

这些 key 被 `app-layout.js` 中的 `_i18nMessages` 内联表使用。虽然功能上可用（英文 locale 查中文 key 返回英文值），但存在以下问题：

1. **维护困难**：新增 JS 翻译需要同时修改 `i18n-messages.json` 和 `app-layout.js` 内联表
2. **与正式系统脱节**：782 个中文 key 中有 309 个的英文值不在 `en.json` 中，形成独立的翻译孤岛
3. **不符合 English-default 原则**：key 仍然是中文

### 3.5 孤儿 key 分析

| 类别 | 数量 | 说明 |
|------|-----:|------|
| en.json 中未被模板/JS/Go 直接引用的 key | 1,157 | 部分通过 `_i18nMessages` 内联表间接引用 |
| en/zh 值完全相同的 key（>3 字符） | 32 | 多为专有名词或技术术语（如 "API Key", "WOODGIT"） |

### 3.6 邮件模板（⚠️ 中严重度）

`pkg/mail/templates.go` 使用独立的双语模板系统，**不走 `pkg/i18n`**：
- 有自己的 locale 判断逻辑
- 硬编码中英文字符串
- 邮件主题和正文直接在 Go 代码中拼接

---

## 4. 现有扫描工具评估

### 4.1 工具清单

| 工具 | 路径 | 覆盖范围 | 盲区 |
|------|------|---------|------|
| `check_i18n.py` | `scripts/` | `{{t}}`、`_t()`、`$t()` 模式覆盖检查 | 不扫描硬编码中文 |
| `i18n_completion_report.py` | `scripts/` | 扫描 `_t()`/showMessage/showConfirm 中的字符串 | 仅扫描前端，不覆盖 Go 后端 |
| `protect_proper_nouns.py` | `scripts/` | 保护 en.json/i18n-messages.json 中专有名词 | 不覆盖 zh.json |
| `convert_static_js_i18n.py` | `scripts/` | 批量转换静态 JS 中文 key | 一次性迁移工具 |
| E2E `ui-console-error-scan` | `e2e/` | 运行时 JS 错误检测 | 不检测翻译缺失 |
| E2E `ui-all-pages` | `e2e/` | 页面 HTTP 200 检测 | 不检测翻译内容 |
| E2E `ui-admin-field-roundtrip` | `e2e/` | 字段 CRUD 往返测试 | 不检测翻译 |

### 4.2 核心盲区

1. ❌ **没有工具检测 JS 文件中未用 `_t()` 包裹的硬编码中文**
2. ❌ **没有工具检测 Go 后端 API 响应中的硬编码中文**
3. ❌ **没有运行时翻译缺失检测**（key 在代码中使用但 JSON 中不存在）
4. ❌ **没有中英文页面 DOM 内容对比**机制
5. ❌ **没有 CI/CD 集成**的 i18n 自动化检查

---

## 5. 修复建议

### 5.1 P0 — 立即实施

#### 5.1.1 编写综合扫描脚本 `scripts/i18n_full_audit.py`

**目标**：一个脚本覆盖所有 i18n 盲区检测。

**检查项**：

```
Check 1: 硬编码中文检测
├── 扫描 web/static/js/*.js 中未用 _t() 包裹的中文
├── 扫描 web/templates/*.html 中未用 {{t}} 或 _t() 包裹的中文
├── 扫描 internal/**/*.go 中 API 响应/flash/toast 中的中文
├── 排除：注释、proper-nouns.json 中的专有名词、flatpickr locale
└── 输出：文件:行号:内容，按严重度分级

Check 2: 翻译完整性
├── 所有 {{t "key"}} 的 key 是否在 en.json 和 zh.json 中都存在
├── 所有 _t('key') 的 key 是否在 en.json 和 zh.json 中都存在
├── 所有 i18n.T("key", ...) 的 key 是否在 en.json 和 zh.json 中都存在
├── en.json 和 zh.json 的 key 集合是否完全一致
└── 空值检测（key 存在但值为空字符串）

Check 3: 孤儿 key 检测
├── en.json/zh.json 中的 key 是否被任何模板/JS/Go 引用
├── 区分"真孤儿"和"间接引用"（通过 _i18nMessages 内联表）
└── 输出可安全删除的 key 清单

Check 4: 翻译质量
├── en/zh 值完全相同的 key（可能是未翻译）
├── 专有名词保护验证
└── 占位符一致性（{name}、{count} 等在 en/zh 中都存在）
```

**预估工时**：4h

#### 5.1.2 清理 `app-layout.js` 内联回退表

**问题**：`app-layout.js:12-1046` 中 916 行硬编码中文。

**方案**：
1. 将 `_i18nMessages` 表中的中文 key 全部替换为英文 phrase key
2. 同步更新所有调用点：`_t('删除')` → `_t('Delete')`
3. 最终目标：`_i18nMessages` 表只包含英文 key → 英文值（identity mapping），或直接删除该表，让英文 locale 直接返回 key 本身

**预估工时**：8h

### 5.2 P1 — 高优先级

#### 5.2.1 Go 后端 API 错误消息 i18n 化

**影响文件**：

| 文件 | 中文行数 | 改造方式 |
|------|--------:|---------|
| `routes/admin_models.go` | 4 | 改用 `routeI18n.T(key, locale)` |
| `routes/enterprise_members.go` | 6 | 改用 `routeI18n.T(key, locale)` |
| `routes/settings.go` | 9 | 分类名改用 i18n key |
| `routes/payment.go` | 1 | 改用 `routeI18n.T(key, locale)` |
| `web/admin_handler_models.go` | 8 | 改用 `h.msgf(c, key, args...)` |
| `web/auth.go` | 5 | 改用 `h.msgf(c, key, args...)` |
| `db/seed.go` | 2 | seed 数据保留中文（仅管理员可见） |

**预估工时**：8h

#### 5.2.2 添加运行时翻译缺失日志

在 `pkg/i18n/i18n.go` 的 `T()` 函数中添加缺失翻译日志：

```go
func (i I18n) T(key string, locale string) string {
    dict, ok := i[locale]
    if !ok {
        dict = i["en"]
    }
    val, ok := dict[key]
    if !ok {
        log.Warn("i18n: missing translation", "key", key, "locale", locale)
        enDict := i["en"]
        if v, ok := enDict[key]; ok { return v }
        return key
    }
    return val
}
```

同理在 JS 的 `_t()` 函数中添加 `console.warn`：

```javascript
function _t(text) {
    if (window._properNouns && window._properNouns[text]) return text;
    var lang = document.documentElement.lang || 'zh';
    if (lang === 'en') {
      var msg = window._i18nMessages[text];
      if (msg === undefined) console.warn('[i18n] missing en:', text);
      return msg !== undefined ? msg : text;
    }
    var zh = window._i18nMessagesZh[text];
    if (zh === undefined) console.warn('[i18n] missing zh:', text);
    return zh !== undefined ? zh : text;
}
```

**收益**：在 staging 环境跑一遍所有页面，自动收集所有缺失翻译。

**预估工时**：2h

### 5.3 P2 — 中期实施

#### 5.3.1 Playwright 双语 DOM 扫描测试

新增 `e2e/i18n-completeness.spec.js`：

```javascript
// 遍历所有页面，在英文模式下扫描 DOM 中泄漏的中文字符
// 在中文模式下扫描 DOM 中泄漏的英文句子（非专有名词）
test('所有页面中英文无硬编码泄漏', async ({ page }) => {
    const pages = [/* 所有页面 URL */];
    const properNouns = JSON.parse(
        fs.readFileSync('web/static/js/proper-nouns.json')
    ).nouns;

    for (const url of pages) {
        // 英文模式
        await page.addInitScript(() => {
            document.cookie = 'locale=en';
        });
        await page.goto(url);

        const chineseLeaks = await page.evaluate(() => {
            const walker = document.createTreeWalker(
                document.body, NodeFilter.SHOW_TEXT);
            const leaks = [];
            while (walker.nextNode()) {
                const text = walker.currentNode.textContent.trim();
                if (/[\u4e00-\u9fff]/.test(text) && text.length > 1) {
                    leaks.push(text.substring(0, 50));
                }
            }
            return leaks;
        });

        // 过滤掉专有名词和预期中文（如日期选择器）
        const unexpected = chineseLeaks.filter(
            t => !Object.keys(properNouns).some(n => t.includes(n))
        );
        expect(unexpected, `Chinese leaked on ${url}: ${unexpected}`)
            .toEqual([]);
    }
});
```

**预估工时**：4h

#### 5.3.2 清理孤儿 key

从 `en.json`/`zh.json` 中删除 1,157 个未被引用的孤儿 key：

1. 运行 `i18n_full_audit.py --orphans` 生成清单
2. 人工审核，排除后端动态使用的 key
3. 批量删除确认的孤儿 key
4. 运行 E2E 测试验证

**预估工时**：2h

#### 5.3.3 迁移 `i18n-messages.json` 到正式系统

将 `i18n-messages.json` 中 782 个中文 key 的翻译合并到 `en.json`/`zh.json`，然后：
1. 将所有 `_t('中文key')` 调用改为 `_t('English key')`
2. 删除 `i18n-messages.json`
3. 删除 `app-layout.js` 中的 `_i18nMessages` 内联表
4. 让英文 locale 的 `_t()` 直接从 `en.json` 查找（通过 XHR 加载）

**预估工时**：6h

### 5.4 P3 — 长期优化

#### 5.4.1 CI/CD 集成 i18n 检查

```yaml
# .github/workflows/i18n-audit.yml
- name: i18n Full Audit
  run: python3 scripts/i18n_full_audit.py --strict

- name: i18n Key Parity Check
  run: |
    python3 -c "
    import json
    en = set(json.load(open('web/i18n/en.json')))
    zh = set(json.load(open('web/i18n/zh.json')))
    assert en == zh, f'Key mismatch: {en ^ zh}'
    "
```

**预估工时**：2h

#### 5.4.2 评估删除 `ui-chrome.json`

`web/static/js/ui-chrome.json`（778 keys）与 `en.json`/`zh.json` 有 777 个重叠 key。确认无代码引用后可删除。

**预估工时**：1h

#### 5.4.3 邮件模板 i18n 化

将 `pkg/mail/templates.go` 中的硬编码双语字符串迁移到 `pkg/i18n` 系统。

**预估工时**：4h

---

## 6. 实施优先级总览

| 优先级 | 编号 | 行动 | 预估工时 | 依赖 |
|--------|------|------|---------|------|
| **P0** | 5.1.1 | 编写 `i18n_full_audit.py` 综合扫描脚本 | 4h | 无 |
| **P0** | 5.1.2 | 清理 `app-layout.js` 916 行硬编码中文 | 8h | 5.1.1 |
| **P1** | 5.2.1 | Go 后端 API 错误消息 i18n 化 | 8h | 5.1.1 |
| **P1** | 5.2.2 | 添加运行时翻译缺失日志 | 2h | 无 |
| **P2** | 5.3.1 | Playwright 双语 DOM 扫描测试 | 4h | 无 |
| **P2** | 5.3.2 | 清理 1,157 个孤儿 key | 2h | 5.1.1 |
| **P2** | 5.3.3 | 迁移 `i18n-messages.json` 到正式系统 | 6h | 5.1.2 |
| **P3** | 5.4.1 | CI/CD 集成 i18n 检查 | 2h | 5.1.1 |
| **P3** | 5.4.2 | 评估删除 `ui-chrome.json` | 1h | 5.3.2 |
| **P3** | 5.4.3 | 邮件模板 i18n 化 | 4h | 5.2.1 |

**总预估工时**：~41h

**推荐实施路径**：

```
5.1.1 扫描脚本 (4h)
    ├── 5.1.2 清理 app-layout.js (8h)
    │       └── 5.3.3 迁移 i18n-messages.json (6h)
    ├── 5.2.1 Go 后端 i18n 化 (8h)
    │       └── 5.4.3 邮件模板 i18n 化 (4h)
    ├── 5.3.2 清理孤儿 key (2h)
    │       └── 5.4.2 删除 ui-chrome.json (1h)
    └── 5.4.1 CI/CD 集成 (2h)
5.2.2 运行时日志 (2h)     ← 可并行
5.3.1 Playwright 测试 (4h) ← 可并行
```

---

## 7. 实施状态更新（截至 2026-06-18）

> 本报告基于 2026-06-17 快照生成，后续已有多项推进。以下按第 5 节建议的编号给出当前状态。

| 编号 | 行动 | 状态 | 备注 / TODO |
|------|------|------|-------------|
| 5.1.1 | 编写 `scripts/i18n_full_audit.py` 综合扫描脚本 | ✅ 已完成 | 已覆盖硬编码中文、翻译完整性、key parity、孤儿 key、翻译质量；2026-06-18 进一步修复了对 `\n`、`\'`、`\"` 等 JS 转义的提取误报，并新增对 `s.t("mail.*")` 的识别。 |
| 5.1.2 | 清理 `app-layout.js` 916 行硬编码中文（内联 `_i18nMessages` 表） | ✅ 已完成 | 内联表已清空（改为 `window._i18nMessages = {};`），英文 locale 直接通过同步 XHR 加载 `en.json`。扫描确认 0 个中文 key 残留。 |
| 5.2.1 | Go 后端 API 错误消息 i18n 化 | ✅ 已完成 | 后端用户面向的中文字符串已清理为 0 行；`i18n.T()` / `routeI18n.T()` / `h.msgf()` 已统一使用英文 key。 |
| 5.2.2 | 添加运行时翻译缺失日志 | ✅ 已完成 | `pkg/i18n/i18n.go` 已加 `log.Warn`，JS `_t()` 已加 `console.warn`。 |
| — | 确认弹窗变量后置重构 | ✅ 已完成 | admin/enterprise/subscription/keys/users/provider 等页面的 disable/enable/delete 确认弹窗已全部改为“通用描述 + `\n\n` + 变量名”模式，并移除非关键操作的 `⚠️` 图标。 |
| — | 新用户默认语言改为英文 | ✅ 已完成 | `getLocale` / `GetLocale` 在无 cookie 且浏览器非中文时返回 `en`；中文浏览器仍按 `Accept-Language` 返回 `zh`。DB schema、users 表、`default_locale` system setting 同步改为 `en`。
| 5.3.1 | Playwright 双语 DOM 扫描测试 | ⏳ TODO | 建议新增 `e2e/i18n-completeness.spec.js`，在英文/中文 locale 下扫描 DOM 文本泄漏。 |
| 5.3.2 | 清理孤儿 key | ✅ 已完成 | 已从 `en.json`/`zh.json` 删除 681 个未被代码直接引用的孤儿 key；审计脚本识别为 0 孤儿。 |
| 5.3.3 | 迁移 `i18n-messages.json` 到正式系统 | ✅ 已完成 | `i18n-messages.json` 已删除；`app-layout.js` 内联表已清空；英文 locale 直接查 `en.json`。 |
| 5.4.1 | CI/CD 集成 i18n 检查 | ⏳ TODO | 在 GitHub Actions 增加 `i18n_full_audit.py --strict` 与 key parity 检查。 |
| 5.4.2 | 评估删除 `ui-chrome.json` | ✅ 已完成 | 确认无代码引用，已删除 `web/static/js/ui-chrome.json`。 |
| 5.4.3 | 邮件模板 i18n 化 | ✅ 已完成 | `pkg/mail/templates.go` 与 `pkg/mail/mail.go` 已迁移到 `pkg/i18n`，新增 34 个 `mail.*` key；`Sender` 构造函数现接受 `i18n.I18n`。 |

### 当前核心指标（2026-06-18）

| 指标 | 数值 | 状态 |
|------|-----:|------|
| `en.json` / `zh.json` key 数 | 1,945 / 1,945 | ✅ 完全一致 |
| 模板 `{{t}}` / JS `_t()` / Go `i18n.T()` / `s.t()` 缺失翻译 | 0 | ✅ |
| JS 文件硬编码中文（非 `_t()`） | 0 | ✅ |
| 模板硬编码中文 | 0 | ✅ |
| Go 后端用户面向硬编码中文 | 0 | ✅ |
| `app-layout.js` 内联 `_i18nMessages` 中文 key | 0 | ✅ |
| 孤儿 key | 0 | ✅ |
| 完全一致的 en/zh 值（非专有名词） | 13 | ⚠️ 需人工复核 |

---

## 8. 验证方式

每项修复完成后的验证清单：

1. **单元测试**：`go test ./internal/web/... ./pkg/...`
2. **E2E 测试**：
   - `ui-all-pages.spec.js`（39/39 passed）
   - `ui-console-error-scan.spec.js`（37/37 passed）
   - `ui-admin-field-roundtrip.spec.js`（28/28 passed）
3. **i18n 审计脚本**：`python3 scripts/i18n_full_audit.py --strict`（0 errors）
4. **手动验证**：切换 `locale=en` cookie 后遍历所有页面，确认无中文泄漏
5. **运行时日志**：检查 staging 环境日志中无 `[i18n] missing` 警告

---

*本报告由 2026-06-17 代码审计生成。如有疑问请参考源代码和原始文档。*
