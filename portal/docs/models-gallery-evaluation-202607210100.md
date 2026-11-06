# 模型广场页面评估

- **评估时间**：2026-07-21（更新于同日，含 TokenRouter 7 月版本实测截图）
- **需求**：在 portal 内新增"模型广场"页面，只做展示（价格、能力、上下文长度等），不提供调用/试用
- **参考**：
  - `docs/tokenrouter-model-pricing-reference-202607181255.md`（价格数据）
  - `docs/reports/tokenrouter-research-202606031600.md`（6 月竞品研究）
  - `aws-backup/tokenrouter/notes.md`（6 月实测笔记，v1~v9 截图）
  - `aws-backup/tokenrouter/v10-screenshots/`（7 月实测截图，v10 脚本）

## 1. TokenRouter 7 月版本实测（v10 截图）

### 1.1 页面结构

| 维度 | 6 月版本 | 7 月版本（实测） |
|------|---------|----------------|
| 路径 | `/console/pricing`（需登录） | `/models`（**公开页面，无需登录**即可浏览） |
| 页面标题 | `All Models` + `87 Models` badge | `All Models`（113 个模型） |
| 技术栈 | React SPA + Semi Design | 同（`new-api` 生态） |
| 卡片网格 | 2 列 | 2 列（每列 ~666px 宽） |
| 搜索 | `Fuzzy search model name` | 同（Semi Input） |
| 类型筛选 | Text / Video / Image / Audio chips | 同（4 个 checkbox chips） |
| 排序 | Newest↓ / USD↓ | 同 |
| 批量复制 | 顶部 Copy 按钮（disabled until multi-select） | 同 |
| 视图切换 | [⊞][≡] 卡片/列表切换 | 同 |
| 促销标签 | Free / HOT / 60% off 徽章 | 同（`z-ai/glm-5.2-free` 标 `Free`，`z-ai/glm-5.2` 标 `HOT 60% off`） |
| 促销弹窗 | "Top up 6, get 4 free" | "GLM 5.2 Now Free" + "Don't remind me again" |

### 1.2 卡片设计（7 月实测）

每张卡片（~666×170px）内容：
```
┌──────────────────────────────────────────┐
│ [Provider Logo] z-ai/glm-5.2   [📋] [☐] │  ← 名称 + 复制/多选图标
│ [HOT] [60% off]                          │  ← 促销徽章（如有）
│                                          │
│ Input Price:   ~~$1.1430~~ $0.4572 /1M   │  ← 原价删除线 + 现价
│ Output Price:  ~~$4.0000~~ $1.6000 /1M  │
│ Cache Read:    ~~$0.2858~~ $0.1143 /1M   │
│                                          │
│ [Pay as You Go]                    [Text]│  ← 计费模式 + 类型标签
└──────────────────────────────────────────┘
```

关键变化（vs 6 月）：
- 促销模型原价带**删除线**，现价高亮
- 卡片更紧凑（170px 高 vs 6 月约 200px）
- 阶梯价模型在卡片上仍显示基础价（≤阈值价），详情 Drawer 中才展示完整阶梯

### 1.3 Drawer 详情（7 月实测，关键升级）

**组件**：Semi Design `semi-sidesheet`（右侧侧滑面板）

| 属性 | 6 月版本 | 7 月版本 |
|------|---------|---------|
| 宽度 | ~640px | **900px**（`semi-sidesheet-size-small`） |
| 位置 | 右侧滑入 | 右侧滑入（x=540, 1440 视口） |
| Mask 遮罩 | **无** backdrop | **有** `semi-sidesheet-mask`（点击关闭） |
| 区块数 | 2 个（API Endpoints + Price） | **3 个**（API Endpoints + Quick Start + Price） |

**三个区块**（均为 Semi Card 组件，圆角 `!rounded-2xl`）：

#### 区块 1：API Endpoints
```
┌─ API Endpoints ──────────────────────────┐
│ [🔗 icon]                                │
│ API Endpoints                            │
│ All models on TokenRouter support the    │
│ OpenAI-compatible API format.            │
│                                          │
│ OpenAI-compatible: /v1/chat/completions  │
│ POST                                     │
│ ⚠ Client URL handling may vary...       │
└──────────────────────────────────────────┘
```

#### 区块 2：Quick Start（7 月新增！）
```
┌─ Quick Start ────────────────────────────┐
│ [📖 icon]                                │
│ Quick Start                              │
│ Copy the Python example and replace the  │
│ API key with your own TokenRouter key.   │
│                                          │
│ [Python] [cURL]          ← tab 切换      │
│ ┌──────────────────────────────────────┐ │
│ │ from openai import OpenAI           │ │
│ │                                      │ │
│ │ client = OpenAI(                    │ │
│ │     base_url='https://api.tokenrouter.com/v1', │ │
│ │     api_key='<YOUR_API_KEY>',       │ │
│ │ )                                    │ │
│ │                                      │ │
│ │ messages = [                         │ │
│ │     {"role": "system", "content": "You are..."}, │ │
│ │     {"role": "user", "content": "Hello..."}, │ │
│ │ ]                                    │ │
│ │                          [Copy]      │ │
│ └──────────────────────────────────────┘ │
└──────────────────────────────────────────┘
```

- **Python / cURL 双 tab**（7 月新增，6 月无代码示例）
- 代码块带语法高亮（`hljs language-python`）
- 每个代码块有 **Copy 按钮**

#### 区块 3：Price
```
┌─ Price ──────────────────────────────────┐
│ [💲 icon]                                │
│ Price                                    │
│ Pricing Breakdown by Model and Service   │
│ Type.                                    │
│                                          │
│ Billing Type: Pay as You Go              │
│ ┌──────────────────────────────────────┐ │
│ │ Input Price    $0.0000 /M tokens    │ │
│ │ Output Price   $0.0000 /M tokens    │ │
│ │ Cache Read     $0.0000 /M tokens    │ │
│ └──────────────────────────────────────┘ │
└──────────────────────────────────────────┘
```

- 6 月版本有 **Billing Type / Price Summary 双 tab**
- 7 月版本简化为直接展示价格表
- 阶梯价模型在此处展示分档（如 `≤256K $0.400 / >256K $1.200`）

### 1.4 Drawer Header
```
┌─ [Provider Logo] z-ai/glm-5.2-free  [Free] [×] ┐
│  [Text]                                         │
│  Free compute capacity is limited. Service     │
│  stability and concurrency are not guaranteed.  │
└─────────────────────────────────────────────────┘
```
- 模型名称 + Provider logo
- 徽章（Free / HOT / 60% off）
- 类型标签（Text / Image / Video / Audio）
- 简短描述/警告

## 2. 我们现有的基础

### 2.1 已有的数据层

| 能力 | 位置 | 说明 |
|------|------|------|
| `service_models` 表 | `internal/db/schema.go` | 已有完整字段：provider, product_id, api_model_name, input_price, output_price, input_price_cache_hit/miss, reasoning_price, aimodels_data(JSON), show_in_store, status, eol_* |
| 公开 API | `GET /api/models/public` (`models_common.go:42`) | 无需鉴权，查询 `status IN ('active','eol_pending') AND show_in_store=true`，返回价格、名称、描述、features、context_window、EOL 信息 |
| `show_in_store` 字段 | `service_models` | 管理员可控制哪些模型在广场展示 |
| `aimodels_data` JSON | `service_models` | 已存储 name, description, features, context_window 等元数据 |
| 管理端模型管理 | `admin-models-general.html` / `admin-models-aws.html` | 管理员可增删改模型、设置 show_in_store、定价 |
| Drawer 组件 | `app.css` + 15 个模板 | 已有成熟的 Drawer 组件（创建/编辑/查看均用），含 backdrop、滑入动画 |

### 2.2 结论

**数据层、API 层、Drawer 组件均已就绪**。`GET /api/models/public` 已返回模型广场所需的全部字段。Drawer 交互模式已在 15 个模板中验证。需要做的主要是**模型广场专属的卡片网格 + Drawer 详情页**。

## 3. 实现方案（基于 7 月实测）

### 3.1 页面路由决策

| 选项 | 说明 | 推荐 |
|------|------|------|
| A. 公开页面 `/models` | 无需登录即可浏览（同 TokenRouter 7 月版本） | ✅ **推荐** |
| B. 登录后可见 `/console/models` | 需登录才能浏览 | ❌ 不推荐 |

理由：TokenRouter 7 月版本已改为公开页面，公开价格有利于 SEO 和用户转化。我们已有 `show_in_store` 字段控制可见性，敏感模型不展示即可。

### 3.2 前端（主要工作）

| # | 文件 | 说明 | 工作量 |
|---|------|------|--------|
| 1 | `web/templates/models-gallery.html` | 新建模板：2 列卡片网格 + 搜索栏 + 类型筛选 chips + 排序 | 中 |
| 2 | `web/static/js/page-models-gallery.js` | 新建 JS：调用 `/api/models/public`，渲染卡片，搜索/筛选/排序 | 中 |
| 3 | `web/static/css/models-gallery.css`（或内联） | 卡片样式：价格高亮、徽章、删除线原价 | 小 |
| 4 | `internal/web/pages.go` | 新增路由 `GET /models` → `handleModelsGallery`，无需鉴权 | 小 |
| 5 | i18n key | 新增 `nav.models_gallery`、`models.input_price` 等多语言 key | 小 |
| 6 | 导航栏 | 在顶部导航加入"模型广场"入口 | 小 |

### 3.3 卡片设计（参考 TokenRouter 7 月实测）

```
┌──────────────────────────────────────────┐
│ [Provider] 模型名称          [📋复制]    │
│ [HOT] [60% off]（如有促销）              │
│                                          │
│ Input Price:   $0.4572 /M tokens         │
│ Output Price:  $1.6000 /M tokens         │
│ Cache Read:    $0.1143 /M tokens         │
│                                          │
│ [按量计费]                          [Text]│
└──────────────────────────────────────────┘
```

### 3.4 Drawer 详情设计（参考 TokenRouter 7 月三区块结构）

**复用已有 Drawer 组件**，内容分为三个 Card 区块：

**区块 1 — API 端点**：
- 端点路径：`/v1/chat/completions`
- 兼容格式：OpenAI-compatible
- 注意事项说明

**区块 2 — 快速开始**（Quick Start）：
- Python / cURL 双 tab（**展示用，不实际调用**）
- 代码示例（含语法高亮 + Copy 按钮）
- 说明文案："复制示例并将 API key 替换为你的密钥"

**区块 3 — 价格明细**：
- Billing Type：Pay as You Go
- Input Price / Output Price / Cache Read / Cache Write
- 阶梯价分档展示（如有）

**Drawer Header**：
- 模型名称 + Provider
- 类型标签（Text / Image / Video / Audio）
- 状态徽章（如 Free / HOT）
- 简短描述

### 3.5 后端（小改动）

| # | 改动 | 说明 | 工作量 |
|------|------|------|--------|
| 1 | `listPublicModels` 补充返回字段 | 补充：`reasoning_price`、`input_price_cache_hit`、`capabilities`、`billing_type` | 小 |
| 2 | 搜索/筛选 API 参数 | 加 `?provider=&q=&type=&sort=` 参数 | 小 |
| 3 | 模型类型分类 | 从 `aimodels_data` 或 provider 推断类型 | 小 |

### 3.6 不需要做的

- ❌ Playground / Chat UI（需求明确不做）
- ❌ 实际 API 调用 / 模型试用
- ❌ 模型上传 / 创建（管理端已有）
- ❌ 批量多选 + 复制功能（非 MVP）

## 4. 工作量评估

| 阶段 | 内容 | 预计工作量 |
|------|------|-----------|
| 后端 API 增强 | 搜索/筛选参数，补充返回字段 | 0.5 天 |
| 前端卡片网格 | 模板 + JS + CSS（2 列卡片、搜索、筛选、排序） | 1 天 |
| 前端 Drawer 详情 | 三区块（API 端点 + Quick Start 代码 + 价格明细），Python/cURL tab | 1 天 |
| 导航 + i18n | 导航入口、多语言 key | 0.5 天 |
| 测试 | API 测试、前端冒烟 | 0.5 天 |
| **合计** | | **3.5 天** |

## 5. 风险与注意事项

| # | 风险 | 缓解 |
|---|------|------|
| 1 | 公开页面泄露未发布模型 | 已有 `show_in_store=true` + `status IN ('active','eol_pending')` 过滤 |
| 2 | 价格泄露敏感信息 | `service_models` 价格即对外展示价格，非上游成本价；安全 |
| 3 | 性能（模型数量多） | 当前 LIMIT 500，可加分页；前端用懒加载 |
| 4 | SEO | 公开页面加 meta 标签，利于搜索引擎收录 |
| 5 | Quick Start 代码安全 | 代码中 API key 用 `<YOUR_API_KEY>` 占位，不泄露真实 key |
| 6 | 与管理端冲突 | 管理端 `/admin/models` 不变；广场页 `/models` 只读展示 |

## 6. MVP 范围

**Phase 1（MVP）**：
- `/models` 公开页面，2 列卡片网格
- 搜索（按名称）+ 类型筛选（Text/Image/Video/Audio）+ 排序
- 卡片：名称、Provider、Input/Output/Cache 价格、计费模式、类型标签
- 点击卡片 → 右侧 Drawer 详情（三区块：API 端点 + Quick Start 代码 + 价格明细）
- Python/cURL 代码 tab（展示用）
- 导航栏入口

**Phase 2（可选增强）**：
- 促销/推荐标签（Free / HOT）
- 原价删除线 + 现价高亮
- 阶梯价分档展示
- 批量多选 + 复制模型名
- 模型对比功能
- SEO 优化

## 7. 结论

**可行，推荐实施**。数据层和 API 已 80% 就绪，Drawer 组件已有成熟实现。预计 3.5 天可完成 MVP。

与 TokenRouter 7 月版本的差异：
- **不做 Playground**（需求明确只展示不调用）
- **Quick Start 代码**保留（展示 API 调用示例，帮助用户理解如何接入，但不实际调用）
- Drawer 宽度可调整为我们的标准 Drawer 宽度（已有 840px 的先例），不必完全照搬 900px
- 促销标签和原价删除线留到 Phase 2
