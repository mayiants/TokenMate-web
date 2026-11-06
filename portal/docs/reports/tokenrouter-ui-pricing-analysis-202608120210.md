# TokenRouter 竞品抓取与价格/UI 分析

> 状态：ACTIVE  
> 时间：2026-08-12 02:10 UTC  
> 范围：TokenRouter 公开页/控制台、WOODGIT 公开页、Portal 价格体系

---

## 1. GitHub 状态（使用 HANDOVER 中的 PAT 查询）

| 仓库 | 最新 commit | 时间 | CI 状态 |
|---|---|---|---|
| ai-portal-go | `4ee84cf` fix(apikeys): fallback to default general group when user has no group | 2026-08-07 | ✅ Portal CI success |
| ai-proxy-go | `aacb65b` feat(proxy): bill partial tokens on Anthropic stream interrupt | 2026-08-04 | ✅ Proxy CI success |

两个仓库均为私有仓库，未认证时 GitHub API 返回 404。已使用 HANDOVER.md 中的 PAT 完成查询；用户计划随后轮换 PAT。

---

## 2. TokenRouter 重新登录与抓取

### 2.1 登录结果

- 账号 `thinbee09@qq.com / Test@123456` 仍可登录成功。
- 登录后进入 `https://www.tokenrouter.com/console`。
- 保存的 `cookies.json` 不能跨会话复用（再次使用会回到首页），必须在同一次 Puppeteer 会话内完成登录 → 截图/抓取。

### 2.2 抓取产物

| 产物 | 路径 | 说明 |
|---|---|---|
| 模型价格 JSON | `/opt/ai-platform/workspace/tokenrouter/models-20260812.json` | 117 个模型卡片数据 |
| Markdown 参考 | `/opt/ai-platform/ai-portal-go/docs/tokenrouter-model-pricing-reference-202608120125.md` | 按类型整理的价目表 |
| 截图 | `/tmp/tokenrouter-scrape/` | home.png / pricing.png / models_loggedin_v2.png / apikeys_loggedin_v2.png / dashboard_loggedin_v2.png / woodgit_api_home.png |

### 2.3 模型广场概况

- **117 个模型**（比 7 月的 113 个增加 4 个）。
- 计费方式：`Pay as You Go` 107 个，`Pay Per Request` 10 个。
- 类型：Text 101、Video 8、Image 8。
- 推广标签：仅 1 个 `Free + Test`（`moonshotai/kimi-k3-free`）。

---

## 3. 价格体系差异：service_models vs model_pricing_rules

### 3.1 两张表的定位

| 表 | 页面 | 语义 | 当前问题 |
|---|---|---|---|
| `service_models` | `/admin/upstream-pricing` | 上游成本（Cost basis） | 上游成本，不应直接展示给终端用户 |
| `model_pricing_rules` | `/admin/pricing` | 下游客户价（Billing price） | 部分规则比上游还低，存在亏损风险 |

### 3.2 关键差异示例

#### deepseek-v4-flash

| 来源 | input | output | cache_read | cache_write | reasoning |
|---|---|---|---|---|---|
| service_models（deepseek 直供） | 0.27 | 1.10 | 0.027 | 0.3375 | — |
| model_pricing_rules（deepseek） | 0.50 | 2.00 | 0.05 | 0.50 | 0.75 |
| TokenRouter | 0.14 | 0.28 | 0.0028 | — | — |

**问题**：公开模型广场 `/api/models/public` 展示的是 `service_models` 价格（0.27/1.1），但实际 billing 按 `model_pricing_rules`（0.5/2.0）扣费。用户看到的价格低于实际扣费。

#### anthropic/claude-haiku-4-5（amazon-us-east-1）

| 来源 | input | output | cache_read | cache_write | reasoning |
|---|---|---|---|---|---|
| service_models | 1.00 | 5.00 | 0.10 | 1.25 | — |
| model_pricing_rules | 0.80 | 4.00 | 0.08 | 0.80 | 1.20 |
| TokenRouter | 1.00 | 5.00 | 0.10 | 1.25 | — |

**问题**：下游售价比上游成本还低，按此规则每调用都会亏损。

#### 数据扫描结果

- 55 条 `service_models` 与 `model_pricing_rules` 的 provider/product/model 完全匹配。
- 其中 **4 条下游价格比上游低**，存在亏损或数据错误：
  - `amazon-us-east-1 / anthropic.claude-haiku-4-5`
  - `tokenrouter / anthropic/claude-fable-5`
  - `tokenrouter / anthropic/claude-haiku-4.5`
  - `tokenrouter / claude-haiku-4-5`

### 3.3 根因

1. **公开价格来源错误**：`/api/models/public` 与落地页模型展示直接从 `service_models` 取价，未经过 `model_pricing_rules` 转换。
2. **去重策略错误**：公开接口按 `api_model_name` 去重并保留 `input_price` 最低的一条，这实际上是“上游成本最低”的 provider，而不是“客户应该付多少”。
3. **价格规则维护缺失**：没有校验“下游价 ≥ 上游价”，导致部分规则倒挂。

---

## 4. UI 对比与借鉴

### 4.1 公开首页（TokenRouter vs api.woodgit.com）

| 维度 | TokenRouter | WOODGIT（api.woodgit.com） | 可借鉴点 |
|---|---|---|---|
| 品牌色 | 明亮蓝 `#0086ff`，有渐变和彩色配图 | 蓝白，主色较保守 | 引入更鲜明的品牌色和渐变 hero |
| 首屏 | 大标题 + 动态 slogan + 一键复制 base URL | 大标题 + 两个 CTA | 增加“复制 base URL”和 provider logo 墙 |
| 促销 | 顶部固定 Banner「Top up 8, get 2 free」 | 无 | 增加可配置顶部营销 Banner |
| 信任背书 | Trusted By 客户 logo 墙 | 无 | 添加客户/合作伙伴 logo |
| 模型入口 | 「View All Models」+ 模型卡片预览 | 「Models」顶部链接 | 首页直接展示热门模型卡片 |
| Quick Start | 三步卡片 + 进度连线 | 三步卡片 + 代码片段 | 已接近，可增加步骤连线 |
| 代码示例 | Tab 切换 cURL/Python/Node | Tab 切换 cURL/Python/Node.js | 一致，可加入一键 Copy |
| FAQ | 手风琴交互 | 手风琴交互 | 一致 |
| Footer | 多栏链接 + CTA | 极简 | 增加 CTA 和多栏导航 |

### 4.2 控制台模型广场（TokenRouter vs WOODGIT）

TokenRouter 优势：

- **彩色模型卡片**：每张卡片左上角有 provider 彩色 logo/图标，首屏信息密度高。
- **彩色 Badge**：`Free`、`Test`、`Pay as You Go`、`Pay Per Request`、`Text`、`Video`、`Image`、`Audio` 等标签带背景色。
- **促销卡片高亮**：`moonshotai/kimi-k3-free` 使用深色渐变背景突出显示。
- **价格高亮**：价格数字使用蓝色/绿色，免费显示 `$0.0000`。
- **操作入口**：每张卡片右上角有复制模型名、加入多选的小图标。
- **顶部 Banner**：登录后所有页面顶部固定蓝色促销条，强化转化。

WOODGIT 当前状态：

- Admin 模型列表仍以表格为主，用户侧缺少独立的模型广场页。
- API Keys 已卡片化，但模型/服务浏览页尚未卡片化。
- 状态标签、类型标签颜色较素。

### 4.3 Dashboard 对比

TokenRouter：

- 顶部固定促销 Banner。
- 统计卡：大数字 + 图标 + 辅助说明，颜色区分（余额蓝色、支出绿色等）。
- 「Model Data Analysis」用 tab 切换 Consumption Distribution / Trend / Ranking。

WOODGIT：

- 已有 8 个统计卡，但视觉层次可再增强。
- 图表和模型排行可以聚合到同一 tab 区块，减少纵向长度。

---

## 5. 建议行动

### 5.1 价格体系修复（高优先级）

1. **公开 API/页面改读客户价**
   - 修改 `/api/models/public` 和落地页模型展示，从 `model_pricing_rules` 解析价格，而不是 `service_models`。
   - 多 provider 模型（如 deepseek 同时有 deepseek 直供和 tokenrouter）展示“默认路由价”或“起售价”，避免展示某个低成本 provider 的价格。

2. **增加价格一致性校验**
   - 在 `/admin/pricing` 保存/导入时校验：对同一 provider/product/model，若 `service_models` 有价格，则 `model_pricing_rules` 各组件价不应低于上游成本。
   - 对已有的 4 条倒挂规则进行数据清理或重新定价。

3. **上游定价页增强**
   - `/admin/upstream-pricing` 增加「当前客户价」和「毛利率」列，让运营一眼看到 markup。
   - 提供「按上游价自动生成客户价」按钮（默认 markup 可配置，如 20%）。

4. **定期参考 TokenRouter 价格**
   - 使用本次抓取的 `models-20260812.json` 作为竞品价格基线，评估自身 markup 是否合理。

### 5.2 UI 借鉴（中优先级）

1. **品牌视觉升级**
   - 引入更鲜明的主色（参考 TokenRouter `#0086ff`），在 hero、按钮、价格数字、图标上使用。
   - 给促销/免费模型卡片增加渐变背景或彩色边框。

2. **顶部营销 Banner**
   - 控制台和公开页顶部增加可配置 Banner，用于活动/公告，提高转化率。

3. **彩色图标与 Badge**
   - 状态标签（active/disabled/revoked）使用填充色块：绿/灰/红。
   - 模型类型标签（Text/Image/Video/Audio）使用不同颜色。
   - 计费模式标签（Pay as You Go / Per Request）使用主色/辅色。

4. **公开模型广场页**
   - 新增 `/models` 公开页面，以卡片网格展示模型，支持搜索、类型筛选、价格排序。
   - 每张卡片展示 provider 图标、价格、类型、促销 badge。

5. **首页增强**
   - 增加「Trusted By」logo 墙、provider logo 墙。
   - hero 区增加 base URL 一键复制。
   - footer 增加多栏导航和 CTA。

6. **Dashboard 聚合**
   - 将消费趋势、模型调用排行、消费分布合并为「Analysis」tab 区块，参考 TokenRouter。

---

## 6. 附录：产物清单

```
/tmp/tokenrouter-scrape/
├── home.png
├── pricing.png
├── models_loggedin_v2.png
├── apikeys_loggedin_v2.png
├── dashboard_loggedin_v2.png
├── woodgit_api_home.png
└── models_loggedin.json

/opt/ai-platform/workspace/tokenrouter/
├── models-20260812.json
└── sidesheet_all-20260812.json

/opt/ai-platform/ai-portal-go/docs/
├── tokenrouter-model-pricing-reference-202608120125.md
└── reports/tokenrouter-ui-pricing-analysis-202608120210.md
```
