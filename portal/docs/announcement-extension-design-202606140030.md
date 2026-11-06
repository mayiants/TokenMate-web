# Announcement 系统扩展设计：促销 Banner + 首次登录引导

> **状态**: COMPLETED ✅
> **日期**: 2026-06-14
> **用途**: 将现有 Announcement 系统扩展为支持顶部促销 Banner 和首次登录引导向导
> **参考竞品**: TokenRouter (tokenrouter.com) — 顶部固定促销 Banner + 首次登录营销 Modal

---

## 一、背景与动机

### 1.1 竞品参考

TokenRouter 在用户登录后展示两种营销/引导组件：

1. **顶部固定促销 Banner**（全宽，蓝色，固定在 header 下方）：
   ```
   🎁 LIMITED OFFER  🎉 Top up 6, get 4 free (40% off), plus claim $200 developer credits.  [Claim Now →]
   ```
   - 跨所有 console 页面持续显示
   - 是收入/转化的强信号

2. **首次登录营销 Modal**（两列对比）：
   - "Get 40% Off" (POPULAR) vs "Apply for $200 Developer Credits"
   - 列表用 ✓ 图标
   - "Don't remind me again" 复选框
   - "View Campaign Rules" 链接
   - 首次登录后立即弹出，是产品/营收模式信号

### 1.2 我们现有 Announcement 系统

当前系统已具备：

| 能力 | 实现方式 |
|------|---------|
| CRUD | Admin 管理页 + REST API |
| 类型 | `info` / `warning` / `maintenance` / `update` |
| 发布流程 | draft → published → archived |
| 定时发布 | `start_at` / `end_at` |
| 置顶 | `pinned` 字段 |
| 弹窗展示 | `show_popup` + Modal 渲染 |
| 已读追踪 | `announcement_reads` 表 + `/api/announcements/:id/read` |
| 未读计数 | `/api/announcements/unread-count` |
| 客户端去重 | `localStorage.seen_announcements` |

**缺失**：顶部横幅渲染、CTA 转化按钮、首次登录触发、永久关闭、目标受众过滤。

---

## 二、三种组件的本质区别

| 维度 | Announcement（现有） | 促销 Banner（新增） | 首次登录引导（新增） |
|------|---------------------|---------------------|---------------------|
| **定位** | 信息通知（维护/更新/警告） | 营收转化（充值优惠/限时活动） | 用户激活（引导完成关键步骤） |
| **展示位置** | Modal 弹窗（`show_popup=true` 时） | 顶部固定横幅（header 下方，全宽） | Modal 或多步向导（首次登录触发） |
| **展示范围** | 所有已登录页面（layout.html 全局） | 所有已登录页面（layout.html 全局） | 仅首次登录时触发一次 |
| **关闭行为** | 关闭后 localStorage 记录 `seen_announcements` | 可关闭，但刷新/换页可能再出现 | 关闭后永不再出现（DB 记录） |
| **交互** | "我知道了"按钮 | CTA 按钮（"立即领取→"） | 多步操作或"不再提醒" |
| **TokenRouter 参考** | — | 🎁 `LIMITED OFFER... [Claim Now →]` | 两列对比："Get 40% Off" vs "$200 Developer Credits" |

---

## 三、设计决策：扩展而非新建

**促销 Banner 和首次登录引导属于 Announcement 系统的扩展应用，不是独立功能。**

理由：

1. **共享基础设施**：CRUD、发布流程、定时、已读追踪、Admin 管理页 — 全部可复用
2. **数据模型同源**：都是"管理员创建 → 定时发布 → 用户可见 → 可关闭"的生命周期
3. **避免碎片化**：不建新表、不建新路由、不建新管理页
4. **渐进式改造**：现有 `type=info/warning/maintenance/update` 的公告完全不受影响

核心改动：
- DB 加 7 个扩展字段
- `layout.html` 加 Banner 渲染区域 + Wizard Modal
- Admin 管理页加新类型选项和对应字段
- 后端 API 返回新字段 + `target_audience` 过滤逻辑

---

## 四、数据库扩展

### 4.1 新增字段

```sql
-- 在 announcements 表增加以下字段（ALTER TABLE，向后兼容）

ALTER TABLE announcements ADD COLUMN IF NOT EXISTS display_mode    VARCHAR(20)  DEFAULT 'modal';
-- 渲染模式: 'modal'(弹窗) | 'banner'(顶部横幅) | 'wizard'(向导)

ALTER TABLE announcements ADD COLUMN IF NOT EXISTS cta_text        VARCHAR(100);
-- CTA 按钮文案: "立即领取" / "充值享优惠" / "开始设置" / NULL(无按钮)

ALTER TABLE announcements ADD COLUMN IF NOT EXISTS cta_link        VARCHAR(500);
-- CTA 跳转链接: "/billing/topup?plan=xxx" / "/onboarding" / NULL(无跳转)

ALTER TABLE announcements ADD COLUMN IF NOT EXISTS banner_color    VARCHAR(20)  DEFAULT 'primary';
-- Banner 背景色: 'primary'(蓝) | 'success'(绿) | 'warning'(黄) | 'danger'(红)

ALTER TABLE announcements ADD COLUMN IF NOT EXISTS dismiss_mode    VARCHAR(20)  DEFAULT 'session';
-- 关闭行为: 'session'(本session不再显示) | 'permanent'(永久关闭,写DB) | 'always'(每次加载都显示)

ALTER TABLE announcements ADD COLUMN IF NOT EXISTS target_audience VARCHAR(20)  DEFAULT 'all';
-- 目标受众: 'all' | 'new_user'(注册N天内) | 'enterprise'(企业用户) | 'free'(免费用户)

ALTER TABLE announcements ADD COLUMN IF NOT EXISTS icon_emoji      VARCHAR(10);
-- 图标 emoji: '🎁' / '🎉' / '🚀' / '⚠️' / NULL
```

### 4.2 完整字段清单

| 字段 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| `id` | UUID | gen_random_uuid() | PK |
| `title` | VARCHAR(255) | — | 标题 |
| `content` | TEXT | — | 内容（支持换行） |
| `status` | VARCHAR(20) | 'draft' | draft / published / archived |
| `type` | VARCHAR(50) | 'info' | **扩展**: info / warning / maintenance / update / **promo** / **onboarding** |
| `display_mode` | VARCHAR(20) | 'modal' | **新增**: modal / banner / wizard |
| `pinned` | BOOLEAN | false | 置顶 |
| `show_popup` | BOOLEAN | false | 弹窗展示（modal 模式专用） |
| `priority` | INTEGER | 0 | 排序权重 |
| `start_at` | TIMESTAMP | NULL | 定时发布开始 |
| `end_at` | TIMESTAMP | NULL | 定时发布结束 |
| `cta_text` | VARCHAR(100) | NULL | **新增**: CTA 按钮文案 |
| `cta_link` | VARCHAR(500) | NULL | **新增**: CTA 跳转链接 |
| `banner_color` | VARCHAR(20) | 'primary' | **新增**: Banner 背景色 |
| `dismiss_mode` | VARCHAR(20) | 'session' | **新增**: 关闭行为 |
| `target_audience` | VARCHAR(20) | 'all' | **新增**: 目标受众 |
| `icon_emoji` | VARCHAR(10) | NULL | **新增**: 图标 emoji |
| `created_by` | UUID | NULL | 创建人 |
| `created_at` | TIMESTAMP | NOW() | 创建时间 |
| `updated_at` | TIMESTAMP | NOW() | 更新时间 |

### 4.3 类型与渲染模式映射

| type | 典型 display_mode | 典型 dismiss_mode | 典型 target_audience |
|------|-------------------|-------------------|---------------------|
| `info` | modal | session | all |
| `warning` | modal / banner | session | all |
| `maintenance` | banner | always | all |
| `update` | modal | session | all |
| **`promo`** | **banner** | **session / always** | **all / free** |
| **`onboarding`** | **wizard** | **permanent** | **new_user** |

### 4.4 字段填写规范

#### `icon_emoji` — 图标 Emoji

- **输入方式**：任何标准 emoji 字符均可 — 电脑输入法自带 emoji 面板、复制粘贴、网页 emoji picker 皆可
- **长度限制**：`VARCHAR(10)`，单个 emoji 占 1-2 字符，组合 emoji（如 `👨‍👩‍👧‍👦`）最多 4-5 字符，10 字符足够
- **无格式校验**：前端不校验是否为合法 emoji，输入什么就存什么；非 emoji 字符（如 `abc`）会原样显示为文字
- **推荐值**：

| 场景 | 推荐 emoji |
|------|-----------|
| 促销/优惠 | `🎁` `🎉` `💰` `🔥` |
| 新功能/更新 | `🚀` `✨` `📢` |
| 维护/警告 | `⚠️` `🔧` `🛠️` |
| 引导/欢迎 | `👋` `🎯` `💡` |

#### `cta_text` — CTA 按钮文案

- **必填条件**：当 `display_mode` 为 `banner` 或 `wizard` 时，`cta_text` + `cta_link` 同时填写才渲染 CTA 按钮；任一为空则不显示按钮
- **内容要求**：简短动词短语，2-6 个字

| 场景 | 推荐 cta_text |
|------|-------------|
| 促销领取 | `立即领取` / `马上参与` / `领优惠` |
| 充值引导 | `去充值` / `立即充值` |
| 新手引导 | `开始设置` / `立即体验` |
| 活动详情 | `查看详情` / `了解规则` |

#### `cta_link` — CTA 跳转链接

- **格式**：页面内相对路径（以 `/` 开头）或完整外部 URL（以 `https://` 开头）
- **渲染方式**：渲染为 `<a href="...">` 标签，相对路径在当前域名内跳转，外部 URL 直接跳转
- **推荐值**：

| 目标页面 | cta_link |
|---------|----------|
| 充值页 | `/billing/topup` |
| 充值指定套餐 | `/billing/topup?plan=promo-40off` |
| API Keys 管理 | `/apikeys` |
| 订阅计划 | `/plan-and-group` |
| Dashboard | `/dashboard` |
| 外部文档 | `https://docs.example.com/promo-rules` |

- **注意事项**：
  - 相对路径必须对应系统内真实存在的页面路由，否则点击后 404
  - 可附加 query 参数传递上下文（如 `?plan=xxx`、`?source=banner`），方便后端追踪转化来源
  - 不支持 `javascript:` 协议（安全风险）

---

## 五、前端渲染设计

### 5.1 Banner 模式（`display_mode='banner'`）

嵌入位置：`layout.html` 的 `<header>` 和 `<main>` 之间

```
┌──────────────────────────────────────────────────────────┐
│  🔵 Logo    Dashboard  Keys  ...     🌙 🌐 🔔 👤       │  ← header
├──────────────────────────────────────────────────────────┤
│ 🎁 LIMITED OFFER — 充6送4(40%off)，再领$200开发者额度  │  ← banner
│                                          [立即领取 →] ✕  │
├──────────────────────────────────────────────────────────┤
│                                                          │
│  Dashboard 主内容区                                       │  ← main
│                                                          │
└──────────────────────────────────────────────────────────┘
```

HTML 结构：

```html
<!-- layout.html: 在 <header> 和 <main> 之间 -->
{{if .IsAuth}}
<div id="promo-banner-area"></div>
{{end}}
```

Banner 渲染模板（JS 动态生成）：

```html
<div class="promo-banner promo-banner--primary" data-announcement-id="xxx">
  <div class="promo-banner__content">
    <span class="promo-banner__icon">🎁</span>
    <span class="promo-banner__text">LIMITED OFFER — 充6送4(40%off)，再领$200开发者额度</span>
  </div>
  <div class="promo-banner__actions">
    <a href="/billing/topup?plan=promo-40off" class="promo-banner__cta">立即领取 →</a>
    <button class="promo-banner__dismiss" onclick="dismissBanner('xxx')">&times;</button>
  </div>
</div>
```

CSS 样式：

```css
.promo-banner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 24px;
  font-size: 14px;
  font-weight: 500;
  animation: slideDown 0.3s ease;
}
.promo-banner--primary { background: var(--color-primary); color: #fff; }
.promo-banner--success { background: var(--color-success); color: #fff; }
.promo-banner--warning { background: var(--color-warning); color: #1a1a1a; }
.promo-banner--danger  { background: var(--color-danger);  color: #fff; }

.promo-banner__content { display: flex; align-items: center; gap: 8px; }
.promo-banner__icon    { font-size: 18px; }
.promo-banner__cta {
  background: rgba(255,255,255,0.2);
  color: inherit;
  padding: 4px 12px;
  border-radius: 4px;
  text-decoration: none;
  font-weight: 600;
  transition: background 0.2s;
}
.promo-banner__cta:hover { background: rgba(255,255,255,0.35); }
.promo-banner__dismiss {
  background: none;
  border: none;
  color: inherit;
  opacity: 0.7;
  font-size: 18px;
  cursor: pointer;
  margin-left: 8px;
}
.promo-banner__dismiss:hover { opacity: 1; }

@keyframes slideDown {
  from { transform: translateY(-100%); opacity: 0; }
  to   { transform: translateY(0);     opacity: 1; }
}
```

### 5.2 Modal 模式（`display_mode='modal'`，现有行为）

保持不变。`show_popup=true` 且 `display_mode='modal'` 的公告以现有 Modal 弹窗展示。

### 5.3 Wizard 模式（`display_mode='wizard'`，首次登录引导）

```
┌─────────────────────────────────────────────┐
│                                             │
│        🚀 欢迎来到 AI Portal!               │
│                                             │
│   ┌─────────────┐  ┌─────────────┐         │
│   │  Get 40% Off │  │ $200 Free   │         │
│   │  (POPULAR)   │  │ Credits     │         │
│   │  ✓ 充6送4    │  │ ✓ 开发者额度 │         │
│   │  ✓ 永久有效  │  │ ✓ 限7天领取  │         │
│   │  [选择此方案] │  │ [申请额度]   │         │
│   └─────────────┘  └─────────────┘         │
│                                             │
│   ☐ 不再提醒                                │
│                              [跳过 →]       │
└─────────────────────────────────────────────┘
```

Wizard 的 `content` 字段可存储结构化 JSON（两列方案数据），前端解析渲染。简单场景下也可用纯文本 + 单个 CTA 按钮。

关闭逻辑：
- 勾选"不再提醒" → 调用 `POST /api/announcements/:id/read`，写 `announcement_reads` 表
- `dismiss_mode='permanent'` → 服务端检查 `announcement_reads`，已读则不再返回
- `target_audience='new_user'` → 服务端检查 `user.created_at` 是否在 7 天内

---

## 六、展示范围规则

| 页面 | Banner | Modal | Wizard |
|------|--------|-------|--------|
| Landing（未登录） | ❌ 不显示 | ❌ 不显示 | ❌ 不显示 |
| Dashboard | ✅ | ✅ | ✅（首次登录） |
| API Keys | ✅ | ✅ | ✅（首次登录） |
| Billing / Usage | ✅ | ✅ | — |
| Enterprise 页面 | ✅ | ✅ | — |
| Admin 页面 | ❌（Admin 不需要看促销） | ✅（维护公告仍显示） | ❌ |

**设计原则**：
- Banner（`type=promo`）只对非 Admin 页面显示
- 维护/更新类 Announcement（`type=maintenance/update`）仍对所有页面显示
- Wizard（`type=onboarding`）只在首次登录时触发，之后永不再显示

---

## 七、后端 API 改动

### 7.1 `GET /api/announcements` 扩展

现有查询增加新字段返回 + `target_audience` 过滤：

```sql
SELECT id, title, content, type, pinned, show_popup, start_at, end_at, created_at,
       display_mode, cta_text, cta_link, banner_color, dismiss_mode, icon_emoji
FROM announcements
WHERE status = 'published'
  AND (start_at IS NULL OR start_at <= NOW())
  AND (end_at IS NULL OR end_at >= NOW())
  AND (
    target_audience = 'all'
    OR (target_audience = 'new_user' AND EXISTS (
      SELECT 1 FROM users WHERE id = $1 AND created_at > NOW() - INTERVAL '7 days'
    ))
    OR (target_audience = 'enterprise' AND EXISTS (
      SELECT 1 FROM users WHERE id = $1 AND role IN ('enterprise_admin', 'enterprise_member')
    ))
    OR (target_audience = 'free' AND NOT EXISTS (
      SELECT 1 FROM subscription_subscriptions WHERE user_id = $1 AND status = 'active'
    ))
  )
ORDER BY pinned DESC, priority DESC, created_at DESC
```

### 7.2 `POST /api/announcements/:id/read` 扩展

现有逻辑不变。`dismiss_mode='permanent'` 的公告关闭后，`announcement_reads` 记录确保服务端不再返回该公告。

### 7.3 Admin CRUD 扩展

`POST /api/admin/announcements` 和 `PUT /api/admin/announcements/:id` 的请求体增加：

```json
{
  "title": "LIMITED OFFER",
  "content": "充6送4(40%off)，再领$200开发者额度",
  "type": "promo",
  "display_mode": "banner",
  "cta_text": "立即领取",
  "cta_link": "/billing/topup?plan=promo-40off",
  "banner_color": "primary",
  "dismiss_mode": "session",
  "target_audience": "all",
  "icon_emoji": "🎁",
  "pinned": true,
  "priority": 100,
  "start_at": "2026-06-15",
  "end_at": "2026-07-15"
}
```

---

## 八、前端 JS 逻辑改动

扩展现有 `checkAnnouncementPopups()` 为统一的 `loadAndRenderAnnouncements()`：

```javascript
function loadAndRenderAnnouncements() {
  fetch('/api/announcements', { credentials: 'same-origin' })
    .then(function(r) { return r.json(); })
    .then(function(data) {
      var announcements = data.announcements || [];

      // 1. 渲染 Banner（display_mode=banner）
      var banners = announcements.filter(function(a) {
        return a.display_mode === 'banner' && !isBannerDismissed(a);
      });
      renderBanners(banners);

      // 2. 渲染 Modal（display_mode=modal, show_popup=true）— 现有逻辑
      var popups = announcements.filter(function(a) {
        return a.show_popup && a.display_mode !== 'banner' && a.display_mode !== 'wizard'
          && !isAnnouncementSeen(a.id);
      });
      if (popups.length > 0) {
        showAnnouncementPopup(popups[0]);
        markAnnouncementSeen(popups[0].id);
      }

      // 3. 渲染 Wizard（display_mode=wizard, target_audience=new_user）
      var wizards = announcements.filter(function(a) {
        return a.display_mode === 'wizard' && !isAnnouncementRead(a.id);
      });
      if (wizards.length > 0) {
        showOnboardingWizard(wizards[0]);
      }
    })
    .catch(function() {});
}

// Banner 关闭逻辑
function dismissBanner(announcementId) {
  var banner = document.querySelector('[data-announcement-id="' + announcementId + '"]');
  if (banner) banner.remove();

  // 根据 dismiss_mode 决定关闭行为
  fetch('/api/announcements/' + announcementId + '/read', {
    method: 'POST', credentials: 'same-origin'
  });

  // session 模式: sessionStorage 记录
  var dismissed = JSON.parse(sessionStorage.getItem('dismissed_banners') || '[]');
  dismissed.push(announcementId);
  sessionStorage.setItem('dismissed_banners', JSON.stringify(dismissed));
}

function isBannerDismissed(announcement) {
  if (announcement.dismiss_mode === 'always') return false;
  if (announcement.dismiss_mode === 'permanent') {
    // 服务端已过滤（announcement_reads），不会返回
    return false;
  }
  // session 模式
  var dismissed = JSON.parse(sessionStorage.getItem('dismissed_banners') || '[]');
  return dismissed.indexOf(announcement.id) !== -1;
}
```

---

## 九、Admin 管理页改动

`admin-announcements.html` 的 Drawer 表单增加：

1. **类型下拉**增加 `promo`（促销）和 `onboarding`（引导）选项
2. **渲染模式**下拉：Modal / Banner / Wizard（根据 type 自动推荐默认值）
3. **CTA 文案**输入框（type=promo/onboarding 时显示）
4. **CTA 链接**输入框（type=promo/onboarding 时显示）
5. **Banner 颜色**下拉（display_mode=banner 时显示）
6. **关闭行为**下拉（dismiss_mode）
7. **目标受众**下拉（target_audience）
8. **图标 Emoji** 输入框

条件显示逻辑（Alpine.js 或原生 JS）：

```javascript
// type 变化时自动设置 display_mode 默认值
document.getElementById('ann-type').addEventListener('change', function() {
  var mode = document.getElementById('ann-display-mode');
  if (this.value === 'promo' && !mode.value) mode.value = 'banner';
  if (this.value === 'onboarding' && !mode.value) mode.value = 'wizard';
});

// display_mode 变化时显示/隐藏相关字段
function toggleDisplayModeFields() {
  var mode = document.getElementById('ann-display-mode').value;
  document.getElementById('cta-fields').style.display = (mode === 'banner' || mode === 'wizard') ? '' : 'none';
  document.getElementById('banner-color-field').style.display = mode === 'banner' ? '' : 'none';
}
```

---

## 十、实现优先级与工时估算

| # | 任务 | 工时 | 优先级 | 状态 |
|---|------|------|--------|------|
| 1 | DB ALTER TABLE 加 7 个字段 | 0.5h | P1 | ✅ 已完成 |
| 2 | 后端 API 扩展（返回新字段 + target_audience 过滤） | 2h | P1 | ✅ 已完成（修复 `user_subscriptions` 表名） |
| 3 | layout.html Banner 渲染区域 + CSS | 2h | P1 | ✅ 已完成（fixed 定位 + inline style 绕过 Tailwind preflight） |
| 4 | layout.html JS: `loadAndRenderAnnouncements()` 重构 | 2h | P1 | ✅ 已完成 |
| 5 | Admin Drawer 表单扩展（新字段 + 条件显示） | 2h | P2 | ✅ 已完成 |
| 6 | Wizard Modal 组件（首次登录向导） | 3h | P2 | ✅ 已完成（双列方案 + 纯文本 fallback） |
| 7 | i18n 翻译键补充 | 1h | P2 | ✅ 已完成（20 键，中/英） |
| 8 | E2E 测试 | 2h | P3 | ✅ 已完成（`e2e/ui-announcement-extension.spec.js`） |
| **合计** | | **~14.5h** | **全部完成** |

**额外完善项**：

| # | 任务 | 状态 |
|---|------|------|
| 9 | Admin 页面过滤 promo banner（`/admin/*` 不显示促销横幅） | ✅ 已完成 |
| 10 | CSS 版本号升级（`app.css?v=14`） | ✅ 已完成 |
| 11 | 设计文档字段填写规范（§4.4） | ✅ 已完成 |

**实施过程关键修复**：

1. **`subscription_subscriptions` → `user_subscriptions`**：SQL 查询中表名错误导致 `/api/announcements` 全部返回 500，即使 `target_audience='all'` 的公告也受影响（PostgreSQL 评估所有 OR 分支）
2. **Banner 被 fixed topbar 遮挡**：`.topbar` 是 `position: fixed; top: 0`，Banner 插在它后面被完全覆盖。修复为 `position: fixed; top: 56px; z-index: 90`
3. **Inline style 绕过 Tailwind Preflight**：Tailwind 的 preflight reset 会覆盖 CSS class 中的 `background`/`color`，Banner 改用 JS inline style 渲染确保可见

---

## 十一、与现有系统的兼容性

| 场景 | 影响 |
|------|------|
| 现有 `type=info/warning/maintenance/update` 的公告 | ✅ 完全不受影响，`display_mode` 默认 `modal`，行为与现在一致 |
| 现有 `show_popup=true` 的弹窗公告 | ✅ 不变，仍以 Modal 展示 |
| 现有 `announcement_reads` 已读记录 | ✅ 复用，`dismiss_mode=permanent` 的公告关闭后写入此表 |
| 现有 Admin CRUD | ✅ 向后兼容，新字段为可选 |
| 现有 `/api/announcements` 响应 | ✅ 扩展（增加字段），不破坏现有客户端 |

---

## 十二、架构关系图

```
Announcement 系统（现有 + 扩展）
├── type: info/warning/maintenance/update  ← 信息类（现有）
│   └── display_mode: modal                ← 弹窗（现有行为）
├── type: promo (新增)                     ← 促销类
│   └── display_mode: banner               ← 顶部横幅
│   └── cta_text + cta_link                ← 转化按钮
│   └── banner_color                       ← 背景色
│   └── dismiss_mode: session/always       ← 关闭行为
├── type: onboarding (新增)                ← 引导类
│   └── display_mode: wizard               ← 首次登录向导
│   └── target_audience: new_user          ← 仅新用户
│   └── dismiss_mode: permanent            ← 永久关闭
└── 共享基础设施
    ├── announcements 表（扩展 7 字段）
    ├── announcement_reads 表（已读/关闭记录，复用）
    ├── Admin CRUD（admin-announcements.html 扩展）
    ├── /api/announcements 端点（返回新字段 + 过滤）
    └── layout.html 全局渲染（Banner + Modal + Wizard）
```

---

*本文档基于 2026-06-13 代码审查和 TokenRouter 竞品分析编写。实现前请确认产品需求（促销策略、新用户引导流程、CTA 跳转目标页）。*
