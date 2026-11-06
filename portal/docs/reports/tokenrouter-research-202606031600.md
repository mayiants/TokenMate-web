# TokenRouter (tokenrouter.com) 竞品研究报告

> **状态**: ACTIVE
> **日期**: 2026-06-03（更新 2026-06-13）
> **用途**: 为 ai-portal-go 产品设计提供参考

---

## 一、公司背景

| 项目 | 内容 |
|------|------|
| 运营公司 | **PaleBlueDot AI, Inc.**（硅谷） |
| 产品名 | PBD TokenRouter |
| 域名 | https://www.tokenrouter.com/ |
| 融资 | Series B — **$1.5亿**（B Capital 领投） |
| 估值 | 超过 **$10亿**（独角兽） |
| 竞品参考 | tokenrouter.io（另一家 LLM 路由平台，非同一家） |
| 发布时间 | 2026年4月21日 |
| 技术栈 | React SPA（`meta generator="new-api"`），Semi UI 设计系统 |
| SDK | Python (`tokenrouter`)、Node.js/TypeScript (`tokenrouter`) |

---

## 二、设计风格

### 前端框架
- **React SPA**，`<div id="root">` 客户端渲染
- 使用 `Semi UI` 组件库（字节出品）
- 字体: `PPNeueMontreal-Regular` / `PPNeueMontreal-Medium`
- 构建工具: Vite (JS bundles in `/assets/`)
- 分析: Umami + Google Analytics + GTM

### 视觉风格
- 极简主义，白底，大面积留白
- 主色: `#141821`（深色文字）、`#e8f2ff`（浅蓝强调）
- 圆角设计（`border-radius: 6px/8px/12px`）
- 无衬线字体（Inter 作为 fallback）
- 布局: 三栏（左侧导航 240px + 内容区 + 右侧面板 220px）
- 暗色模式: 支持

### 与同类网站对比
- 比 ai-portal-go 更现代、更轻量
- 接近 Vercel / Linear 风格
- 比传统管理后台更少表格感，更多卡片式布局

---

## 三、核心功能

### 3.1 模型路由（核心卖点）
- **Smart Token Routing**: 每次请求自动选择最佳模型/供应商
- **路由模式**:
  - `auto:balance` — 平衡性价比
  - `auto:cost` — 成本优先
  - `auto:quality` — 质量优先
  - `auto:latency` — 延迟优先
  - `specific-model:mode` — 指定具体模型
- **Multi-Channel Failover**: 多条上游 + 自建推理集群，保证 **99.95% 可用性**

### 3.2 控制台功能（基于 palebluedot.ai 功能指南）

| 模块 | 功能 |
|------|------|
| **Dashboard** | 余额概览、用量趋势图、账户状态 |
| **Models** | 浏览所有可用模型、搜索、查看价格 |
| **Chat** | 在线对话测试、选择 API Key 和模型 |
| **API Keys** | 创建/管理 API Key、配置权限和限制 |
| **Usage Logs** | 请求历史、消耗记录、过滤查询 |
| **Balance** | 钱包余额、充值、充值记录 |
| **Providers** | 配置上游供应商 API Key（OpenAI, Anthropic, Google 等） |
| **Routing Rules** | 自定义路由规则（条件匹配 + 动作） |
| **Firewall** | 内容过滤和安全策略 |

### 3.3 API 兼容性
- 完全 **OpenAI 兼容**（`/v1/responses` 端点）
- 支持流式（SSE）
- API Key 格式: `tr_...`
- Base URL: `https://api.tokenrouter.io/v1`

### 3.4 模型支持
支持的供应商（公开信息）:
- OpenAI (GPT-5.5, GPT-5.5 Pro)
- Anthropic (Claude Opus 4.7, Claude Sonnet 4.6)
- Google (Gemini 2.5 Pro, Gemini 3.1 Pro)
- DeepSeek (V4 Flash)
- Meta
- Mistral
- Moonshot AI (Kimi K2.6)
- MiniMax (M2.7)
- Qwen (Qwen3.6)
- Zhipu AI (GLM 5.1)
- NVIDIA (Nemotron)
- xAI

### 3.5 SDK
- **Python**: `pip install tokenrouter` (v1.2.1)
- **Node/TypeScript**: `npm install tokenrouter`
- 支持同步/异步客户端，类型安全

---

## 四、计费模式

> 注: 未公开固定套餐价格页面，采用 **按量付费 + 企业定制** 模式。

### 计费特点
- **按 Token 计费**（无固定套餐）
- **信用额度充值**（预付余额模式）
- **企业级**: 可按成员/团队/部门设置预算上限
- **实时预算控制**: 请求全生命周期自动执行

### 模型价格参考（预估，基于公开信息）
| 模型 | 输入/1M tokens | 输出/1M tokens |
|------|---------------|----------------|
| DeepSeek V4 Flash | $0.14 | $0.28 |
| Gemini 2.5 Pro | $1.25 | $10.00 |
| Claude Sonnet 4.6 | $3.00 | $15.00 |
| GPT-5.5 | $5.00 | $30.00 |
| GPT-5.5 Pro | $30.00 | $180.00 |

### 成本优化手段
- 智能路由自动选择最优性价比模型
- 缓存命中降低费用
- 批量处理折扣

---

## 五、文档体系

### 文档站点
- 主站: https://docs.tokenrouter.io/
- 包含: 快速开始、认证、API 参考、SDK 文档、路由规则、防火墙

### 主要内容
| 文档 | 内容 |
|------|------|
| **Quickstart** | 5 分钟上手，安装 SDK → 创建 Key → 添加供应商 → 发请求 |
| **Authentication** | API Key 格式 `tr_...`，Bearer Auth，最佳实践 |
| **Provider Keys** | 配置上游供应商 API Key |
| **Auto Routing** | 智能路由工作原理 |
| **Routing Modes** | 四种路由模式详解 |
| **Rules API** | 自定义路由规则（条件匹配 + 动作） |
| **Firewall API** | 内容过滤 API |
| **SDK Docs** | Node/Python SDK 完整文档 |

### 法律文档（S3 托管）
| 文档 | 链接 |
|------|------|
| **Terms of Use** | `palebluedotai-prod.s3.ap-southeast-1.amazonaws.com/PaleBlueDotTermsOfUse.pdf` |
| **Privacy Policy** | `palebluedotai-prod.s3.ap-southeast-1.amazonaws.com/PaleBlueDotPrivacyPolicy.pdf` |
| **Cookie Policy** | S3 同域名 |
| **Accessibility** | S3 同域名 |

> PDF 文件为 FlateDecode 编码，无法直接提取文本。

---

## 六、与 ai-portal-go 的对比

| 维度 | TokenRouter (竞品) | ai-portal-go (我们) |
|------|-------------------|---------------------|
| 公司 | PaleBlueDot AI ($150M 融资) | — |
| 前端 | React SPA + Semi UI | Go 模板 + 原生 HTML |
| 路由 | 四种 auto 模式 + specific model | **两层路由**: Admin 手动配置 provider 优先级 + 6 因子智能路由 |
| 路由粒度 | 请求级路由模式选择 | Key 级健康/ Tier/ RPM/ 延迟/ 配额/ LRU |
| 供应商管理 | 用户自行添加上游 Key | 平台统一管理 |
| 故障切换 | 多通道自动 Failover (99.95%) | **Circuit breaker + 跨 provider fallback + 跨 key LRU** |
| 防火墙 | 内容过滤、PII 检测 | — |
| API 兼容 | OpenAI Responses API | OpenAI 兼容 |
| SDK | Python + Node 官方 SDK | — |
| 计费 | 先充值后使用，按 Token | 类似模式 |
| 企业控制 | 成员/部门预算、审计日志 | 企业分组/成员管理 + **Key 优先级 (High/Normal/Economy)** |
| 在线 Chat | 内置 Chat 测试页面 | — |

---

## 七、登录尝试

- 使用 `e2e-user@example.com` / `E2E_TestPass123!` 尝试登录
- `/api/login` 端点返回 **405 Method Not Allowed**
- 前端为 SPA，实际登录流程需通过 JS 应用处理，非标准 POST 表单
- 控制台位于独立子域名或 API 网关后

---

## 八、关键发现与启发

1. **API 兼容性是核心**: 主打 OpenAI 兼容，降低迁移成本
2. **路由差异化**: 四种路由模式是最大卖点，ai-portal-go 缺少此能力
3. **供应商 Key 管理**: 用户自带 Key 模式 vs 平台统一采购，各有优劣
4. **前端现代化**: React SPA 体验远优于 Go 模板，建议前端独立
5. **企业级控制**: 成员/部门预算分配是刚需，我们已有企业分组功能
6. **法律文档托管**: PDF 存在 S3 上，可通过 CDN 加速
7. **SDK 覆盖**: Node 和 Python SDK 降低集成门槛
8. **内置 Chat 工具**: 方便开发者和客户快速测试模型，值得借鉴

---

## 九、路由逻辑深度对比

> 基于实际代码审计: `ai-proxy-go/internal/server/handlers.go` — `resolveProviderAndKey()` + `smartRoute()`

### 9.1 核心路由函数: `resolveProviderAndKey`

`ai-proxy-go` 中 `internal/server/handlers.go:1109` 是请求级路由的统一入口，处理所有 chat/embedding/image/audio 请求。流程如下:

```
1. relay.GetProviderFromModel(model) → 获取首选 provider (硬编码模型→Provider映射)
2. 获取所有支持该模型的 provider 列表
3. 排除 Circuit Breaker 打开的 provider
4. GetAllKeysForModel(model, excluded) → 跨所有 provider 收集候选 key (high/normal/economy 分组)
5. 按用户优先级 (high/normal/economy) 过滤候选集
6. 检查 Admin 手动配置: GetRoutingOrder(model) ?
   ├── 有配置 → 按 Admin 配置的 provider 顺序排序 (providerRank map)
   ├── 无配置 → 进入 smartRoute() 6 因子智能路由
7. 选择候选集第一个 key，返回 (provider, key, upstreamModelName, isFallback)
```

### 9.2 智能路由链: `smartRoute`

当 Admin 未配置 provider 手动路由时，`internal/server/handlers.go:1215` 执行 6 步智能路由链——**全部基于 per-key 指标，而非 per-provider**:

| 步骤 | 开关 | 逻辑 | 数据来源 |
|------|------|------|---------|
| 1. HEALTH | `sm["health"]` | 排除 error_rate > 80% 或 status=unhealthy 的 key | Portal sync `key_health_stats` |
| 2. TIER | `sm["tier"]` | Premium > Enterprise > Default > Economy | upstream_key.tier |
| 3. RPM | `sm["rpm"]` | 排除达到 RPM 限制的 key | 内存计数器 |
| 4. LATENCY | `sm["latency"]` | 按平均延迟分桶排序 (<800ms / <2s / <5s / ≥5s) | Portal sync `key_health_stats` |
| 5. QUOTA | `sm["quota"]` | 优先剩余配额多的 key (有配额的 key 优先于无限的) | upstream_key.quota_limit / quota_used |
| 6. LRU | 始终启用 | Least Recently Used (同层级内) | 内存 LastUsed |

用户亲和性 (`preferredKeyID`): 在 LRU 步骤前检查，优先返回同一 provider 中之前用过的 key。

6 个步骤中每个都通过 `sm["xxx"]` 开关控制，开关由 Portal 的 `system_settings.smart_routing_config` JSONB 字段同步到 Proxy。

### 9.3 配置架构

```
Portal (Admin 控制面)                            Proxy (数据面)
┌──────────────────────────┐              ┌───────────────────────────┐
│  model_provider_routing  │  sync  →    │  Manager.modelRouting     │
│  (模型→Provider 优先级)   │              │  GetRoutingOrder(model)   │
├──────────────────────────┤              ├───────────────────────────┤
│  system_settings         │  sync  →    │  Manager.smartRouting     │
│  .smart_routing_config   │              │  GetSmartRouting()        │
│  (6 因子开关)            │              │                            │
├──────────────────────────┤              ├───────────────────────────┤
│  key_health_stats        │  sync  →    │  Manager.keyHealth        │
│  (per-key 延迟/成功率)    │              │  GetKeyHealth()           │
├──────────────────────────┤              ├───────────────────────────┤
│  provider_health_stats   │  sync  →    │  Manager.providerHealth   │
│  (per-provider 延迟/状态) │              │  GetProviderHealth()      │
├──────────────────────────┤              ├───────────────────────────┤
│  upstream_keys           │  sync  →    │  Manager.PriorityQueue    │
│  (tier/rpm/quota/model)  │              │  GetKeyByPriority()       │
└──────────────────────────┘              └───────────────────────────┘
```

### 9.4 TokenRouter 路由模式 vs 我们的实现

| TokenRouter 模式 | 宣称行为 | 我们的等价实现 | 差异点 |
|-----------------|---------|---------------|--------|
| `auto:balance` | 平衡性价比 | smartRoute TIER + LATENCY + QUOTA (无价格因子) | 我们没有 per-key 价格数据，用 tier 近似性价比 |
| `auto:cost` | 成本优先 | smartRoute TIER (优先 economy tier) | TokenRouter 的 cost 基于真实价格数据；我们基于 tier 标签 |
| `auto:quality` | 质量优先 | smartRoute HEALTH + TIER (premium) | 接近，我们缺少"质量分"元数据 |
| `auto:latency` | 延迟优先 | smartRoute LATENCY step | 我们的延迟是分桶排序(非精确值)，TokenRouter 可能做精确排序 |
| `specific-model` | 指定具体模型 | 用户通过 API 请求 `model=X` | 相同 |
| Multi-Channel Failover | 99.95% 可用性 | Circuit breaker + 跨 provider 候选回退 | 我们没有宣传可用性指标，但架构上支持类似能力 |
| Custom Routing Rules | 条件匹配 + 动作 | model_provider_routing 手动配置 (仅静态 provider 排序) | TokenRouter 的条件路由更灵活(基于请求属性)；我们是静态优先级 |

### 9.5 关键差距分析

| 我们有的 | TokenRouter 可能没有的 |
|---------|----------------------|
| **Per-key 健康/延迟/配额** — 路由到单 key 级别 | 他们可能是 per-provider 级别路由 |
| **Tier 系统** — High/Normal/Economy 跨 provider 统一排序 | 可能只有优先级标签 |
| **Circuit breaker** — 请求级熔断 + 回退 | 他们有 Multi-Channel Failover |
| **Per-key RPM 限制** — 防止 key 级限流 | 可能是 provider 级限流 |
| **Portal sync 架构** — 控制面/数据面分离 | 一体式架构 |

| TokenRouter 有的 | 我们可能没有的 |
|-----------------|--------------|
| **请求级路由模式选择** — 每次请求可指定 auto:balance/cost/quality/latency | 我们路由模式由 Admin 全局配置，用户无法请求级切换 |
| **价格感知路由** — 基于真实 input/output 价格排序 | 我们用 tier 近似，没有 per-model 价格优化 |
| **Conditional Routing Rules** — 按 IP/地域/用户/请求头条件匹配 | 我们只有静态 provider 顺序 |
| **Firewall/Content Filter** — PII 检测、内容过滤 | 完全没有 |
| **Client SDK** — Python/Node 官方封装 | 无 |
| **预构建模型定价** — 公开每模型定价表 | 我们的价格不公开，依赖 upstream 定价 |

### 9.6 结论

我们的路由引擎在 **key 级粒度**（而不是 provider 级）和 **6 因子智能路由链** 方面比 TokenRouter 的四种 auto 模式更细化。但我们在以下方面落后:

1. **用户选择权**: 用户不能按请求选择路由模式（需要 SDK/API 参数支持）
2. **价格感知**: 我们的 tier 是近似值，没有真正的 per-model 价格优化
3. **条件路由**: 没有动态规则引擎（基于请求属性）
4. **内容安全**: 完全没有防火墙功能
5. **可观测性**: 没有路由决策的 UI 展示

**建议优先补齐**：用户请求级路由模式选择（对 API 用户最有价值）+ 价格感知路由（对成本敏感用户最有价值）。

---

## 十、2026-06-13 代码验证更新

> 基于对 ai-portal-go 和 ai-proxy-go 当前代码的完整审查，更新各借鉴项的实现状态。

### 10.1 UI 模式实现状态

| # | TokenRouter 做法 | 之前评估 | 当前实现状态 | 验证依据 |
|---|-----------------|---------|-------------|---------|
| 1 | 通用右侧 Drawer（读+写，无 backdrop） | 🔴 需修正 | ✅ **已实现** | `apikeys.html` 有 `create-key-drawer` + `settings-drawer`；14 个模板使用 Drawer 组件；`app.css` 有完整 `.drawer` / `.drawer-backdrop` 样式 |
| 2 | API Keys 卡片化布局 | 🔴 设计过重 | ✅ **已实现** | `apikeys.html` 使用 `.key-card-grid` 卡片网格，含 key-card-header / key-card-meta / key-card-models / key-card-footer 四段式结构；状态切换为行内按钮 |
| 3 | 两级侧栏导航 | ⚠️ 太长 | ✅ **已实现** | `layout.html` 使用 `sidebar-section-title` 分组：User Console → Enterprise Console → System Console 三大区域，含 divider 分隔 |
| 4 | Dashboard 统计卡片 | ✅ 吻合 | ✅ **已实现** | `dashboard.html` 使用 `.stats-grid` + `.stat-card`，8 个 KPI 卡片（余额/活跃Key/今日Token/月Token/活跃订阅/Token剩余/请求剩余/套餐余额），含 SVG 图标和颜色区分 |
| 5 | 暗色模式 | — | ✅ **已实现** | `layout.html` 有 `data-theme` + `data-theme-preference` 支持 system/light/dark 三模式；`app.css` 有完整 dark 主题样式 |
| 6 | i18n 国际化 | — | ✅ **已实现** | `layout.html` 使用 `{{t "key"}}` 模板函数；`_i18nMessages` JS 对象覆盖数百个翻译键；中/英双语完整 |
| 7 | Sidebar 折叠 | ✅ 吻合 | ✅ **已实现** | `layout.html` 有 `sidebar.collapsed` 状态；`app.css` 有 `sidebar.collapsed` 响应式样式 |
| 8 | 侧栏滚动位置记忆 | — | ✅ **已实现** | `layout.html` 有 `sidebarScrollPos` sessionStorage 存取逻辑 |
| 9 | 主题切换按钮 | — | ✅ **已实现** | `layout.html` 有 `theme-toggle-btn`，使用 Alpine.js 绑定 |
| 10 | 语言切换 | — | ✅ **已实现** | `layout.html` 有 "Switch language" 按钮（表单提交切换 locale） |
| 11 | 移动端响应式 | — | ✅ **已实现** | `layout.html` 有 `mobile-menu-btn` + `sidebar-overlay`；`app.css` 有 mobile 断点样式 |

### 10.2 Drawer 模式详细验证

TokenRouter 的通用 Drawer 模式已在我们的代码中全面落地：

| 模板 | Drawer 用途 | 类型 |
|------|-----------|------|
| `apikeys.html` | 创建 Key + 设置 Key | 创建/编辑（写） |
| `admin-groups.html` | 创建分组 + 编辑分组 | 创建/编辑（写），编辑 Drawer 宽 840px |
| `admin-models-aws.html` | 确认模型 + 编辑模型 | 编辑（写） |
| `admin-models-general.html` | 编辑模型 | 编辑（写） |
| `admin-providers.html` | 编辑 Provider | 编辑（写） |
| `admin-keypool.html` | 创建/编辑上游 Key | 创建/编辑（写） |
| `admin-users.html` | 查看用户详情 | 查看（读） |
| `admin-enterprise.html` | 编辑企业 | 编辑（写） |
| `admin-subscriptions.html` | 编辑订阅 | 编辑（写） |
| `admin-vouchers.html` | 创建/编辑券码 | 创建/编辑（写） |
| `admin-announcements.html` | 编辑公告 | 编辑（写） |
| `enterprise/groups.html` | 分组操作 | 编辑（写） |
| `enterprise/keys.html` | Key 操作 | 编辑（写） |
| `enterprise/users.html` | 成员操作 | 编辑（写） |
| `invoices.html` | 发票详情 | 查看（读） |

**结论**: 我们已完全采纳 "Drawer 优先，Modal 例外" 原则。Drawer 统一承载读+写，backdrop 可选（创建/编辑一般不带 backdrop，仅阻断场景用 backdrop）。这与 TokenRouter 的模式一致。

### 10.3 功能差距更新

| 差距项 | 之前状态 | 当前状态 | 说明 |
|--------|---------|---------|------|
| **请求级路由模式选择** | 🔴 缺失 | 🟡 **部分实现** | Proxy 已有 `model_routing_mode`（single/round_robin/priority），但尚未暴露 auto:balance/cost/quality/latency 模式。API Key 级别可配置 model_routing_mode |
| **价格感知路由** | 🔴 缺失 | 🔴 **仍缺失** | 路由基于 Tier 近似（High/Normal/Economy），无 per-model 价格优化。service_models 表有 input_price/output_price，但 smartRoute 未引用 |
| **条件路由规则** | 🔴 缺失 | 🟡 **部分实现** | `model_provider_routing` 表支持 Admin 手动配置 provider 优先级顺序，但不支持按请求属性（IP/地域/请求头）动态匹配 |
| **内容安全/防火墙** | 🔴 缺失 | 🟡 **部分实现** | Portal 有 `sensitive.go`（敏感词检测），Proxy 端无防火墙。`convert/` 包有 `content_filter` stop reason 处理 |
| **Client SDK** | 🔴 缺失 | 🔴 **仍缺失** | 无官方 Python/Node SDK |
| **Chat Playground** | 🔴 缺失 | 🔴 **仍缺失** | 无内置对话测试 UI |
| **公开模型定价表** | 🔴 缺失 | 🟡 **部分实现** | Portal 有 `/api/service-models/public` 公开端点，但前端无类似 TokenRouter 的 Models 卡片浏览页 |
| **Profile Switcher** | 🔴 缺失 | 🔴 **仍缺失** | 无右上角个人/企业上下文切换下拉菜单，侧栏用区域分组代替 |

### 10.4 安全修复验证

| 安全问题 | 状态 | 说明 |
|---------|------|------|
| CORS Allow-All | ✅ **已修复** | 最近 commit `25c3d73` 添加了安全修复 |
| MustExec 批量修复 | ✅ **已修复** | commit `d42a784` + `014756b` 完成订阅/支付/券码的 MustExec 清理 |
| Redis fail-closed | ✅ **已修复** | commit `25c3d73` Redis 不可用时认证 fail-closed |
| Metrics 端点保护 | ✅ **已修复** | commit `25c3d73` Metrics 需要 ADMIN_SECRET |
| Bcrypt Cost | 🔴 **仍为 10** | 代码中仍用 `bcrypt.GenerateFromPassword(..., 10)` |
| 错误信息泄露 | 🟡 **部分修复** | 安全批量修复后改善，但仍有 `err.Error()` 返回给客户端的情况 |
| 非特权容器 | 🔴 **仍为 root** | Portal Dockerfile 没有 `USER appuser`，Proxy 已有 |

### 10.5 我们已超越 TokenRouter 的方面

| 维度 | 我们的优势 | TokenRouter 对比 |
|------|-----------|----------------|
| Key 级路由粒度 | ✅ 6 因子智能路由链（健康/Tier/RPM/延迟/配额/LRU） | 可能是 provider 级路由 |
| 多模型路由模式 | ✅ single/round_robin/priority 三模式 per-key | 仅有 static model 选择 |
| 企业分组管理 | ✅ 分组限流/模型权限/定价倍率/子分组 | 有成员管理，分组粒度不明 |
| 熔断器 | ✅ Provider 级熔断 + 半开探测 | 有 Multi-Channel Failover |
| 余额近实时同步 | ✅ Redis Pub/Sub 10s 余额缓存 + 降级 30min | 未知 |
| Drawer 模式 | ✅ 全面落地（15 个模板） | 有 Drawer |
| 暗色模式 | ✅ system/light/dark | 有暗色模式 |
| 双语 i18n | ✅ 完整中/英 | 未知是否有 i18n |
| 法律文档 | ✅ 内置（ToS/隐私政策/支付条款） | 外部 S3 PDF |
| 订阅系统 | ✅ Token/Code 双类型 + 自动续费 | 仅有按量付费 |
| 优惠券系统 | ✅ 统一券码（礼品/促销/推荐/欢迎） | 仅有 Voucher |
| 每日签到 | ✅ 奖励积分系统 | 无 |
| 管理员对账 | ✅ 有 admin-reconciliation 页面（开发中） | 有 Org Usage Logs |

### 10.6 剩余优先差距（按影响力排序）

| # | 差距 | 影响用户 | 实现难度 | 优先级 |
|---|------|---------|---------|--------|
| 1 | **Profile Switcher**（右上角个人/企业切换） | 企业用户 | 低（纯前端） | P1 |
| 2 | **Chat Playground**（内置对话测试） | 开发者 | 中（前端+后端） | P1 |
| 3 | **价格感知路由**（smartRoute 引用 service_models 定价） | 成本敏感用户 | 中 | P1 |
| 4 | **公开模型浏览页**（类似 TokenRouter Models 卡片） | 新用户 | 低（前端，数据已有） | P2 |
| 5 | **请求级 auto 路由模式** | API 高级用户 | 高 | P2 |
| 6 | **Client SDK**（Python/Node） | 开发者 | 中 | P2 |
| 7 | **条件路由规则** | 企业用户 | 高 | P3 |
| 8 | **内容防火墙** | 合规需求用户 | 高 | P3 |
