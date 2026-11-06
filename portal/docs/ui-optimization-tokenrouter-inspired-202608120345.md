# TokenRouter 风格 UI 优化方案

> **Status**: ACTIVE  
> **Date**: 2026-08-12 03:45  
> **Scope**: ai-portal-go 前端（含公开首页 `/api/models/public`、管理后台、Dashboard）  
> **参考素材**: `workspace/tokenrouter/` 下的最新截图与抓取的模型/价格数据  
> **目标**: 吸收 TokenRouter 在色彩、图标、卡片布局、信息层级上的优点，提升 WOODGIT 的品牌感与可用性，同时保持现有 Tailwind + HTMX 技术栈不变。

---

## 1. 当前差距速览

| 页面/模块 | TokenRouter | WOODGIT 现状 | 差距等级 |
|---|---|---|---|
| 公开首页 | 大 Hero + base URL 一键复制 + 特性指标 + 模型走马灯 | 较单薄，以登录/注册为主 | 高 |
| 模型广场 | 2 列卡片网格、彩色 provider icon、类型/价格 chip、HOT/Free badge | 以 JSON/API 为主，缺少面向用户的卡片页 | 高 |
| Dashboard | 顶部促销 banner + 4 KPI 卡片 + 图表 Tab | 已有 8 个 stat-card，但缺少固定 banner 和图表 Tab | 中 |
| API Keys | 单行表格 + 行内操作，或右抽屉创建 | 已实现卡片 + 抽屉，信息更丰富 | 低 |
| 空状态 | 插画 + 文案 | 纯文字 "暂无数据" | 中 |
| 配色/图标 | 深蓝紫渐变、圆角卡片、彩色 provider 图标 | 当前偏素雅，图标以 Lucide 轮廓为主 | 中 |

---

## 2. TokenRouter 可借鉴的设计点

### 2.1 公开首页（`home.png` / `home.txt`）

- **Hero 区**: 一行 slogan + 一句行动文案 + 一个 **base URL 复制框**（`https://api.tokenrouter.com/v1`）。
- **信任指标**: 99.9% Uptime / Smart Caching / Always-On Routing 三个大字号指标。
- **模型走马灯**: 横向滚动展示热门模型卡片，每张卡片带 provider 标签（XAI · reasoning model）。
- **促销 Banner**: 顶部固定条 "Top up 7, get 3 free (30% off)"，强化转化。
- **CTA 组合**: "Get API Key" 主按钮 + "Read Docs" 次按钮。

**对 WOODGIT 的启示**:
- 在 `api.woodgit.com` 首页增加 Hero 区、base URL 复制、信任指标、热门模型走马灯。
- 复用已有的 `/api/models/public` 数据驱动走马灯。

### 2.2 模型广场（`models.png` / `pricing.txt`）

- **2 列卡片网格**: 紧凑，关键信息前置（Input / Output / Cache Read 价格）。
- **彩色 Provider 图标**: 每个模型左上角有品牌色小图标，增强识别度。
- **Badge 系统**:
  - 状态/营销: `Free`, `Test`, `HOT`, `60% off`
  - 类型: `Text`, `Image`, `Video`, `Audio`
  - 计费: `Pay as You Go`, `Pay Per Request`
- **右上角快捷操作**: 复制模型名、多选复选框（hover 才显示）。
- **点击卡片 → 右侧 Drawer 详情**:
  - API Endpoints 区块（OpenAI-compatible）
  - Price 区块，支持 `Billing Type` / `Price Summary` Tab
  - 分级定价展示（≤256K / >256K）
- **筛选栏**: 搜索 + Copy + 排序 + 类型多选 + 视图切换（网格/列表）。

**对 WOODGIT 的启示**:
- 新增 `/models` 公开页面，用卡片网格展示 `/api/models/public`。
- 为常见 provider（openai/anthropic/google/deepseek 等）做彩色 SVG icon。
- 用 chip/badge 展示类型、计费方式、营销标签。
- 详情用右侧 Drawer（无 backdrop，点外部关闭），复用当前 Drawer 组件但去掉可选 backdrop。

### 2.3 Dashboard（`dashboard.png` / `dashboard.txt`）

- **顶部固定促销 banner**: 蓝色背景，跨所有 console 页面。
- **4 个 KPI 卡片**: Account Data / Usage Statistics / Resource Consumption / Performance Indicators。
- **Model Data Analysis**: 4 个图表 Tab（Consumption Distribution / Trend / Models Call Distribution / Models Call Ranking）。
- **空状态**: 可爱插画 + "No Results Found"。

**对 WOODGIT 的启示**:
- 在 Dashboard 顶部增加可配置 announcement banner（数据可来自 `announcements` 表或配置）。
- 将现有 8 个 stat-card 按主题分组（财务 / 用量 / 性能 / 配额）。
- 用量图表区增加 Tab 切换（Tokens / Requests / Spend / Model 排名）。
- 空状态用插画替代纯文字。

### 2.4 全局导航与头部

- **Topbar**: Balance / Docs / Release Notes / 通知 / 头像下拉。
- **Sidebar**: 可折叠，当前项高亮（背景色 + 左边框）。
- **Profile Switcher**: 头像下拉里切换 Personal / Enterprise 上下文。
- **两级侧栏**: Enterprise 模式下同时展示 Admin Console 与 Personal 分组。

**对 WOODGIT 的启示**:
- 保持现有 sidebar 折叠能力，补充顶部固定 banner 区域。
- 头像下拉可增加 "切换企业"（已有 enterprise 功能可接入）。
- 当前激活项的视觉高亮可再强化（左边框 + 柔和背景色）。

---

## 3. 具体实施建议

### 3.1 品牌色与视觉系统

- **主色**: 深蓝紫渐变（`#1e3a8a → #7c3aed`）作为 Hero / Banner / 激活态背景，替代当前单一的靛蓝。
- **辅助色**:
  - 成功/在线: 翠绿 `#10b981`
  - 警告: 琥珀 `#f59e0b`
  - 错误: 玫瑰 `#f43f5e`
  - 信息: 天蓝 `#0ea5e9`
- **卡片**: 白色背景、圆角 `12px`、柔和阴影 `shadow-sm` → hover 时 `shadow-md` + 轻微上浮。
- **字体**: 保持 Inter / system-ui，增加数字等宽显示价格。
- **图标**: 保留 Lucide，但为 provider 增加一组彩色圆形/圆角图标（可放在 `web/static/img/providers/`）。

### 3.2 新增/改造页面

#### A. 公开首页 (`web/templates/home.html` 或新增 `landing.html`)

建议模块:
1. **顶部 Banner**（可选关闭）: 促销/公告。
2. **Hero**: 左侧文案 + 右侧 base URL 复制框。
3. **信任指标**: 3-4 个大数字。
4. **热门模型走马灯**: 横向滚动卡片，数据来源 `/api/models/public?limit=20`。
5. **特性简介**: 3 列小卡片（统一接入、透明定价、企业级控制）。
6. **CTA**: 注册 / 查看文档。

#### B. 公开模型广场 (`web/templates/models-public.html`)

建议模块:
1. **页面标题**: "All Models" + 模型总数 badge。
2. **筛选栏**:
   - 搜索框（fuzzy）
   - 类型 chip 多选（Text / Image / Video / Audio）
   - 排序（Newest / Price Low / Price High）
   - 视图切换（Grid / List）
   - 批量 Copy（选中后启用）
3. **卡片网格**: 2 列响应式。
   - 顶部: provider icon + 模型名 + 复制/多选图标
   - 中部: Input / Output / Cache 价格
   - 底部: 计费方式 badge + 类型 badge + 营销 badge
4. **右侧 Drawer 详情**:
   - 模型名 + provider + 类型 badge
   - API Endpoints（OpenAI-compatible 标签页）
   - Price Summary（表格列出所有价格项）
   - 若存在分级定价，展示阈值列。

#### C. 管理后台上游定价页 (`admin-upstream-pricing.html`)

已完成本次改动（新增客户价、Fixed/Req、毛利率列与"生成下游价"按钮）。后续可补充:
- 毛利率为负或零时标红提示。
- 行内批量生成下游价（选中多行后统一 markup）。

#### D. Dashboard (`admin-dashboard.html`)

- 增加顶部 `announcement-banner` 组件。
- 将 stat-card 按主题分组，增加图标颜色区分。
- 用量图表区增加 Tab 切换。

### 3.3 组件级改进

| 组件 | 当前 | 建议 |
|---|---|---|
| Badge | 单一颜色 | 按语义区分：blue(info)/green(success)/amber(warning)/rose(error)/purple(feature) |
| Chip | 灰色为主 | 类型 chip 带颜色：Text 蓝、Image 紫、Video 粉、Audio 橙 |
| Drawer | 有 backdrop | 详情/创建类 Drawer 默认无 backdrop，删除确认保留 backdrop/modal |
| Empty State | 纯文字 | 加 SVG 插画 + 引导文案 + 操作按钮 |
| Table Actions | 部分在抽屉 | 高频操作（Enable/Disable/Copy/Delete）行内展示 |
| Banner | 无 | 新增 `site-banner` 与 `console-banner` 两个位置 |

### 3.4 图标与插画资源

- Provider 彩色图标：openai、anthropic、google、deepseek、meta、mistral、x-ai、moonshot、qwen、minimax、bytedance 等。
- 空状态插画：无数据、无模型、无日志、无账单 4 个场景。
- 可在 `web/static/img/` 下新建 `providers/` 与 `illustrations/`。

---

## 4. 实施阶段（建议）

### P1 — 品牌与首页（1-2 周）
- 更新 Tailwind 配置，增加品牌渐变与 badge 颜色变量。
- 重制公开首页：Hero + base URL 复制 + 信任指标 + 模型走马灯。
- 新增顶部公告 banner 组件。

### P2 — 模型广场卡片化（2-3 周）
- 新增 `/models` 公开页面，卡片网格读取 `/api/models/public`。
- 实现 provider 彩色图标、类型 chip、badge。
- 实现右侧 Drawer 详情（API Endpoints / Price Summary）。
- 多选 + 批量复制模型名。

### P3 — Dashboard 与全局细节（2 周）
- Dashboard 增加 banner、KPI 分组、图表 Tab。
- 空状态插画替换。
- 头像下拉增加 Profile Switcher（Personal / Enterprise）。
- API Keys 创建 Drawer 增加快捷过期时间按钮组（Never / 1 Day / 1 Month）。

### P4 —  polish（1 周）
- 动效：卡片 hover、Drawer 滑入、banner 滚动。
- 响应式：移动端模型广场单列、首页堆叠。
- i18n：所有新增文案同步 `en.json` / `zh.json`。

---

## 5. 与本次价格/模型改动的协同

- 本次已将 117 个 TokenRouter 模型导入 `service_models` 与 `model_pricing_rules`，且 `/api/models/public` 已改从下游价格规则读取。
- 因此 P2 的公开模型广场可以直接使用现有 API，无需后端改动。
- 建议在 P2 中优先展示 `provider='tokenrouter'` 的模型，因为它们已具备完整的价格与类型数据。

---

## 6. 参考资料

- `workspace/tokenrouter/home.png` — 公开首页截图
- `workspace/tokenrouter/models.png` / `pricing.png` — 模型广场截图
- `workspace/tokenrouter/dashboard.png` — Dashboard 截图
- `workspace/tokenrouter/detail.png` — 模型详情 Drawer 截图
- `workspace/tokenrouter/tokenrouter-notes-20260715.md` — 前期详细调研笔记
- `workspace/tokenrouter/models-20260812.json` — 本次抓取的 117 个模型及价格数据

---

## 7. 结论

TokenRouter 的核心优势在于：**信息密度高、视觉层级清晰、营销转化路径明确**。WOODGIT 已经在 Drawer、卡片、Dashboard 等方面做了大量工作，下一步应重点补齐：

1. **公开首页的丰富度**（Hero + 信任指标 + 模型走马灯）
2. **模型广场的卡片化与详情 Drawer**
3. **品牌色/图标的焕新**
4. **空状态插画与顶部 Banner**

按 P1→P4 分阶段落地，可在不引入新框架的前提下显著提升产品感。
