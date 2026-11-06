# i18n 默认语言与硬编码中文问题分析及重构方案

## 背景

项目最初的设计目标是 **默认英文界面，i18n 支持其他语种**。但当前实现偏离了这一目标：界面默认显示中文，英文反而需要额外翻译；大量弹窗文案使用中文片段拼接，英文环境下经常漏翻译而直接显示中文。

## 当前实现概览

### 1. 两套翻译文件

- `web/i18n/zh.json`：3372 个 key
- `web/i18n/en.json`：3373 个 key

key 的选取方式是 **用中文句子/短语作为 key**，例如：

```json
{
  "ui_订阅计划": "Subscription Plans",
  "确定要删除计划 \"{name}\" 吗？删除后无法恢复！": "Are you sure..."
}
```

### 2. 后端翻译函数

`internal/routes/i18n.go` 中的 `T(key, locale)`：

- 先查当前 locale 的字典；
- 找不到再 fallback 到 `en.json`；
- 最后 fallback 到 key 本身。

`internal/web/handler.go` 的 `getLocale` 默认返回 `zh`。

### 3. 前端翻译函数

`web/static/js/app-layout.js` 中的 `_t(text)`：

```js
function _t(text) {
  var lang = document.documentElement.lang || 'zh';
  if (lang !== 'en') return text;   // 非英文直接返回中文 key
  return window._i18nMessages[text] || text;
}
```

- `window._i18nMessages` 是一个 1020 条左右的硬编码中文→英文对象；
- 启动时再异步合并 `web/static/js/i18n-messages.json` 里的补充条目；
- 只有 `lang === 'en'` 时才查字典，其他语言（包括默认）都直接返回 key。

### 4. 拼接式弹窗

大量 JS 弹窗使用中文片段 + 变量 + 中文片段的方式拼接：

```js
showConfirm(_t('确认删除'),
  _t('⚠️ 确定要永久删除企业 "') + name + _t('" 吗？\n\n此操作将...'),
  ...
);
```

只要某个片段在 `i18n-messages.json` 里缺漏，英文界面就会显示一段中文。

## 问题范围统计

| 项目 | 数量 |
|---|---|
| 翻译 key 总数 | ~3,372 |
| 使用 `{{t ...}}` 的模板文件 | 56 个 |
| `{{t ...}}` 调用次数 | ~2,438 次 |
| 使用 `_t(...)` 的 JS/HTML 文件 | 41 个 |
| `_t(...)` 调用次数 | ~1,137 次 |
| 存在 `_t('...') + var + _t('...')` 拼接的文件 | 30 个 |

主要涉及页面：

- `admin-enterprise.html`
- `admin-subscriptions.html`
- `admin-groups.html`
- `admin-users.html`
- `admin-vouchers.html`
- `apikeys.html`
- `invoices.html`
- `profile.html`
- `web/static/js/page-admin-enterprise.js`
- `web/static/js/page-admin-models.js`
- ...

## 根因分析

1. **默认语言被设为中文**
   - `getLocale` 默认返回 `zh`；
   - `_t` 非英文直接返回 key，而 key 是中文，导致默认显示中文。

2. **key 设计反了**
   - 正常 i18n 应该使用**英文标识符**作为 key（如 `modal.confirm_delete_plan`），然后 `zh.json` / `en.json` 分别给出翻译；
   - 当前使用中文作为 key，导致中文成为“默认回退文案”。

3. **JS 翻译只支持英文**
   - `_t` 没有走 locale→字典的通用逻辑，而是写死 “只有英文才翻译”。
   - 这导致新增语言（如日文、法文）时无法扩展。

4. **拼接式文案难以维护**
   - 一个句子被拆成多个 key，变量前后都有中文片段；
   - 翻译人员/维护人员很难发现所有片段，极易漏翻。

## 影响

- 英文用户经常看到混合中文弹窗，体验差；
- 每修复一个弹窗都要改模板/JS + 补 `i18n-messages.json`，重复劳动；
- 无法支持第三种语言；
- 中文 key 里常带空格、换行、引号、emoji，作为 key 不稳定，容易因转义问题导致匹配失败。

## 目标

1. **默认界面为英文**；
2. 中文、其他语言通过 i18n 文件切换；
3. 弹窗等含变量的文案使用参数化 key（如 `"Are you sure you want to delete plan \"{name}\"?"`），不再拼接；
4. 前后端使用统一的 locale 解析与翻译逻辑。

## 可选方案

### 方案 A：最小改动，保持中文 key

**做法**：
- 把 `_t` 改为根据 `document.documentElement.lang` 查对应字典；
- 把 `getLocale` 默认改为 `en`；
- 给 `zh.json` 填上所有中文 key 的中文翻译（目前很多 key 的值就是 key 本身，需要美化）；
- 保留现有拼接写法，只补漏。

**优点**：改动小，快速让默认变成英文。

**缺点**：
- 中文 key 仍然丑陋，无法支持第三种语言；
- 拼接问题没根治，后续还会漏翻；
- `zh.json` 里存一堆 `ui_xxx`、`action_xxx` 这种半中文半英文 key，不伦不类。

**工作量**：1～2 天，主要是补翻译和改默认 locale。

### 方案 B：全量重构为英文标识符 key（推荐）

**做法**：
1. 制定 key 命名规范，例如：
   - `modal.confirm_delete_enterprise`
   - `action.suspend`
   - `table.user`
   - `hint.quota_description`
2. 用脚本把现有中文 key 批量替换为英文标识符；
3. `en.json` 的值就是当前英文翻译（可直接复用）；
4. `zh.json` 的值就是当前中文 key 的中文部分（需要人工审核美化）；
5. 所有拼接文案改成参数化 key + `.replace('{name}', name)`；
6. 改造 `_t` 函数：
   - 支持当前 locale 查字典；
   - 支持 `{name}` 占位符替换；
   - 默认英文。
7. 改造 Go 端 `T` 函数，同样支持占位符和默认英文；
8. 统一 `getLocale` / `document.documentElement.lang` 默认值为 `en`。

**优点**：
- 彻底解决问题；
- 支持任意多语言扩展；
- 弹窗不再拼接，维护简单。

**缺点**：
- 工作量大；
- 需要全量回归测试，防止 key 替换导致页面显示异常。

**工作量**：
- 脚本开发与跑批：0.5 天
- 人工审核/修正 key 与翻译：2～3 天
- 模板与 JS 拼接改造：2～3 天
- 前后端 `_t` / `T` 函数改造：0.5 天
- 全站回归测试：1～2 天

**总计：约 1 周（1 名前端/全栈）**。

### 方案 C：渐进式重构

**做法**：
- 先实施方案 A，让默认语言立刻变成英文，解决当前最痛的弹窗中文问题；
- 后续按页面/模块逐步把中文 key 替换为英文标识符；
- 新功能一律使用英文 key 和参数化文案。

**优点**：风险低，可分期交付。

**缺点**：技术债会持续一段时间。

**工作量**：
- 阶段 1（方案 A）：1～2 天
- 阶段 2（按模块替换）：每周 1～2 个页面，约 1 个月完成全站

## 推荐实施方案

**推荐方案 C：先 A 后 B，分阶段进行。**

### 第一阶段：止血（1～2 天）

1. 修改 `internal/web/handler.go` 的 `getLocale` 和 `internal/routes/i18n.go` 的 `GetLocale`，默认返回 `en`；
2. 修改前端 `_t`：
   - 根据 `document.documentElement.lang` 查字典；
   - 默认英文；
   - 支持 `{name}` 占位符。
3. 把 `zh.json` 中值等于 key 的条目美化成正常中文（至少保证中文界面可读）；
4. 把已经发现的拼接弹窗改成参数化 key，统一补到 `i18n-messages.json` 或新字典里。

### 第二阶段：根治（按周推进）

1. 制定并文档化 key 命名规范；
2. 写一个迁移脚本，扫描 `web/templates` 和 `web/static/js`，把中文 key 自动转换为规范英文 key；
3. 逐页/逐模块 PR 替换，每个 PR 附带截图验证中英文；
4. 全量替换完成后，删除 `i18n-messages.json` 里的中文 key，统一使用 `en.json` / `zh.json`；
5. 删除 `app-layout.js` 里硬编码的 1020 条中文→英文对象，全部改为从静态文件加载。

## 关键改造点 checklist

- [x] `internal/routes/i18n.go`：`GetLocale` 默认已改为 `en`，同时仍识别中文浏览器 `Accept-Language: zh` 返回 `zh`。
- [x] `internal/web/handler.go`：`getLocale` 默认已改为 `en`；`RenderPartial` / `Partial` 已同步 locale。
- [x] 前端 `_t()` 调用英文翻译已补齐（`web/static/js/i18n-messages.json` 覆盖全部扫描到的前端字符串）。
- [x] 模板 `{{t ...}}` 调用英文翻译已补齐；模板中已无裸露中文硬编码。
- [x] 专有名词词库 `web/static/js/proper-nouns.json` 已创建，`app-layout.js` 的 `_t()` 会先查词库避免误翻译；`scripts/protect_proper_nouns.py` 可自动保护 `en.json` / `i18n-messages.json` 中的专有名词。
- [x] 拼接式/中置变量确认弹窗已重构为**变量后置**模式（admin/enterprise/subscription/keys/users/provider），避免 `"{name}"` 转义问题并提升可维护性。
- [x] `web/static/js/app-layout.js`：硬编码翻译对象已清空，`_t` 英文 fallback 直接查 `en.json`。
- [ ] `web/static/js/app-layout.js`：`_t` 改为通用 locale-aware（支持任意语言，默认英文）—— 长期可选。
- [ ] `web/i18n/en.json`：key 改为英文标识符（长期，当前仍使用英文 phrase key）。
- [ ] `web/i18n/zh.json`：key 改为英文标识符，值为中文（长期）。
- [x] 评估并清理 681 个英文孤儿 key（当前为 0）。
- [x] 清理 `i18n-messages.json` / `ui-chrome.json`（确认无引用，已删除）。

## 结论

当前 i18n 实现的偏离是**默认语言设置 + key 设计方向 + 拼接式文案**三个问题叠加导致的。

- **Phase 1（止血）已完成**：通过 `scripts/i18n_completion_report.py` 扫描，所有 689 个前端 `_t()` 字符串和全部 `{{t ...}}` 模板字符串均已补全英文翻译；`web/static/js/i18n-messages.json` 从 414 条扩展到覆盖全部前端字符串；模板中最后 1 处裸露中文 placeholder 已加 key。
- **Phase 2（根治）仍是可选技术债**：把中文 key 全部替换为英文标识符、统一前后端 locale 默认值为 `en`、将拼接式文案改为参数化 key（或变量后置模式）。全量重构大约需要 **1 名开发者 1 周集中时间**，或 **1 个月渐进式完成**。

> **2026-06-18 更新**：已确认“变量后置”比“参数化 key + `.replace('{name}', name)`”更适合当前确认弹窗场景：无需改动 `_t()` 支持占位符，翻译 key 保持完整英文句子，变量在渲染时追加到末尾，维护成本更低。
