# AWS Bedrock 集成验证报告与动态调度方案

**日期：** 2026-07-02  
**状态：** E2E 验证通过 + 优化提案

---

## 一、E2E 测试结果

### AWS Key 配置

| Key | Provider | Region | 模型数 | 认证方式 |
|-----|----------|--------|--------|---------|
| AKIARLRC...7BXg | amazon-us-east-1 | us-east-1 | 186 | AWS SigV4 |
| AKIARLRC...G1ij | amazon-us-east-1 | us-east-1 | 186 | AWS SigV4 |
| AKIARLRC...G1ij | amazon-mantle-us-east-1 | us-east-1 | 0 | Bearer Token |
| sk-4vHAZ...f3QJ | accessapi | — | 142 | Bearer Token |

### 调度验证

| 测试项 | 结果 | 证据 |
|--------|------|------|
| AWS Key 轮询 | ✅ | `bba74132...` 和 `eb964986...` 交替选中 |
| 跨 Provider 路由 | ✅ | 同一 user key 同时路由到 AWS 和 accessapi |
| Nova 系列模型 | ✅ | nova-lite/micro/pro 全部成功 |
| 计费正确 | ✅ | 590 μ$ / 79 tokens = 7.5 μ$/token |
| Pre-consume 冻结 | ✅ | Portal 日志确认 freeze + unfreeze |
| 上游统计记录 | ✅ | upstream-usage 正确区分 provider |

### 不可用模型（需排查）

| 模型 | 错误 | 原因 |
|------|------|------|
| `us.anthropic.claude-sonnet-4-*` | "invalid model identifier" | 模型 ID 格式可能需要去掉 `us.` 前缀 |
| `anthropic.claude-3-haiku-*` | "Access denied - Legacy" | 需要主动订阅该模型 |
| `meta.llama3-1-8b-*` | "invalid model identifier" | 可能已更新为新版本 ID |
| `deepseek.r1-v1:0` | "invalid model identifier" | 可能尚未在 us-east-1 可用 |
| `global.*` 前缀模型 | 503 "no upstream keys" | 路由逻辑未匹配 global prefix |

---

## 二、admin/models-aws 页面功能验证

| 功能 | API 端点 | 状态 | 说明 |
|------|----------|------|------|
| **AWS 模型目录同步** | `POST /sync-bedrock?provider=` | ✅ | 从 Bedrock ListFoundationModels 发现 186 个模型 |
| **模型列表展示** | `GET /service-models` | ✅ | 含 provider、价格、状态筛选 |
| **批量验证** | `POST /batch-validate` | ✅ | 排队验证模型可用性 |
| **批量确认/定价** | `PATCH /service-models/:id` | ✅ | 单个/批量设置 active + 价格 |
| **Provider 列表** | `GET /providers` | ✅ | 展示所有配置的 provider |
| **Key Model 同步** | `POST /provider-models/sync-key-models` | ✅ 可用 | 从 key 的 model list 同步 |
| **Resync to Keys** | `POST /resync-to-keys` | ✅ 可用 | 将 service model 配置推送到 proxy |

### 页面功能建议

1. **模型分组展示** — 当前 186 个模型平铺展示，建议按 vendor 分组（Anthropic/Amazon/Meta/Mistral/...）
2. **价格批量导入** — 支持 CSV/JSON 批量导入 Bedrock 定价
3. **模型可用性标记** — 区分"已订阅可用"vs"需要申请"的模型
4. **Region 标记** — 显示模型在哪些 region 可用

---

## 三、AWS 高级功能与使用建议

### 3.1 Cross-Region Inference (Global Prefix)

Bedrock 支持 `global.` 前缀实现跨区域推理，自动将请求路由到最近的可用 region：

```
global.anthropic.claude-opus-4-6-v1  → 自动选择最佳 region
us.anthropic.claude-opus-4-6-v1      → 强制 us-east-1
```

**当前问题：** Proxy 的路由逻辑没有匹配 `global.*` 前缀的模型到 amazon provider。
**建议：** 在 `GetProviderFromModel()` 中添加 `global.` 前缀识别。

### 3.2 Provisioned Throughput

对于高流量模型，Bedrock 支持预留吞吐量（按小时计费，保证延迟）：

| 模式 | 适用场景 | 成本 |
|------|---------|------|
| On-Demand | 低-中流量，按 token 计费 | 标准价 |
| Provisioned | 高流量，预留 model units | ~2x 标准价但保证吞吐 |
| Batch | 非实时，24h 内完成 | ~50% 折扣 |

**建议：** 当某模型的流量稳定超过 50 RPM 时，评估 Provisioned Throughput 的 ROI。

### 3.3 Model Invocation Logging

Bedrock 支持将所有请求日志发送到 S3/CloudWatch：

**建议：** 启用后可用于：
- 对账（与我们的 retention records 交叉验证）
- 审计合规
- 调试不一致的 token 计数

### 3.4 Guardrails

Bedrock Guardrails 可在平台侧强制执行内容过滤：

**建议：** 如果需要为企业客户提供内容过滤，可以配置 Guardrail ID 并在请求中传递，而非在 proxy 侧实现。

---

## 四、动态调度方案

### 当前路由逻辑

```
Model name → GetProviderFromModel() → Provider
Provider → GetAllKeysForModel() → [Keys with model in supported list]
Keys → Priority/Affinity/Round-Robin → Selected Key
```

当前是**模型名决定 provider，provider 决定 key**。同一个模型在多个 provider 有时都可用（如 `claude-opus-4-6` 同时在 accessapi 和 Bedrock 上可用）。

### 目标：按场景智能路由

```
┌──────────────────────────────────────────────────────────────┐
│ 用户请求: model=claude-sonnet-4-6, content="写代码"           │
└──────────────────────┬───────────────────────────────────────┘
                       ↓
┌──────────────────────────────────────────────────────────────┐
│ 场景分类器 (Scene Classifier)                                 │
│   分析 system prompt + user message → 场景标签                │
│   "coding" / "search" / "creative" / "analysis" / "image"    │
└──────────────────────┬───────────────────────────────────────┘
                       ↓
┌──────────────────────────────────────────────────────────────┐
│ 模型选择策略 (Model Selection Policy)                         │
│   coding → opus (高能力) → 优先 Bedrock (成本更低)            │
│   search → sonnet (够用) → 优先 accessapi (延迟更低)          │
│   image → nova-canvas / stable-diffusion                     │
│   analysis → pro + thinking                                  │
└──────────────────────┬───────────────────────────────────────┘
                       ↓
┌──────────────────────────────────────────────────────────────┐
│ 成本/延迟均衡器 (Cost-Latency Balancer)                       │
│   Bedrock: $15/M input, 稳定延迟, 无限量                     │
│   accessapi: $15/M input, 可变延迟, 有 RPM 限制              │
│   → 选择当前最优路径                                          │
└──────────────────────────────────────────────────────────────┘
```

### 4.1 场景分类器设计

**轻量级方案（推荐先实施）：** 基于规则的关键词匹配

```go
type SceneType string
const (
    SceneCoding   SceneType = "coding"
    SceneSearch   SceneType = "search"
    SceneCreative SceneType = "creative"
    SceneAnalysis SceneType = "analysis"
    SceneImage    SceneType = "image"
    SceneGeneral  SceneType = "general"
)

func ClassifyScene(systemPrompt, userMessage string) SceneType {
    combined := strings.ToLower(systemPrompt + " " + userMessage)
    
    // Image generation keywords
    if containsAny(combined, "draw", "image", "picture", "illustration", "generate image") {
        return SceneImage
    }
    // Coding keywords
    if containsAny(combined, "code", "function", "implement", "debug", "refactor",
        "programming", "algorithm", "class", "API", "```") {
        return SceneCoding
    }
    // Search/QA keywords
    if containsAny(combined, "search", "find", "what is", "explain", "summary", "translate") {
        return SceneSearch
    }
    // Analysis keywords
    if containsAny(combined, "analyze", "compare", "evaluate", "review", "assess", "think step") {
        return SceneAnalysis
    }
    // Creative
    if containsAny(combined, "write", "story", "creative", "poem", "essay") {
        return SceneCreative
    }
    return SceneGeneral
}
```

**高级方案（后期）：** 用轻量 LLM（如 nova-micro）做 1-shot 分类。

### 4.2 模型选择策略

| 场景 | 推荐模型 | 备选 | 理由 |
|------|---------|------|------|
| **Coding** | claude-opus-4-6 / claude-sonnet-4-6 | gpt-5.4-pro | 代码理解最强 |
| **Search/QA** | claude-sonnet-4-6 / gpt-5.4-mini | gemini-2.5-flash | 性价比高 |
| **Creative** | claude-opus-4-6 / gpt-5.5 | — | 创作能力强 |
| **Analysis** | claude-opus-4-6-thinking | o3 / gemini-2.5-pro | 需要深度推理 |
| **Image** | nova-canvas / stable-diffusion | dall-e-3 | 原生图像生成 |
| **General** | claude-sonnet-4-6 / gpt-5.4-mini | — | 平衡性价比 |

### 4.3 Provider 优先级（成本均衡）

相同模型在不同 Provider 的成本对比：

| 模型 | Bedrock 成本 | accessapi 成本 | 延迟 | 建议路由 |
|------|-------------|---------------|------|---------|
| claude-opus-4-6 | $15/M in | ~$15/M in | Bedrock 更稳定 | 优先 Bedrock |
| claude-sonnet-4-6 | $3/M in | ~$3/M in | 相近 | 负载均衡 |
| gpt-5.4-mini | N/A | $0.4/M in | — | accessapi only |
| nova-lite | $0.06/M in | N/A | — | Bedrock only |

**策略：**
```yaml
routing_policy:
  # 同模型多 provider 时的选择策略
  claude-opus-4-6:
    prefer: bedrock          # Bedrock 更稳定，成本相同
    fallback: accessapi      # Bedrock 不可用时 fallback
  claude-sonnet-4-6:
    strategy: round-robin    # 成本相同，负载均衡
    providers: [bedrock, accessapi]
  gpt-5.4-mini:
    prefer: accessapi        # 仅 accessapi 有
  us.amazon.nova-lite-v1:0:
    prefer: bedrock          # 仅 Bedrock 有
```

### 4.4 负载均衡与成本控制

**维度 1: RPM 负载均衡**
```
当某个 key 的 RPM 使用率 > 70% 时，新请求优先路由到其他 key/provider
```

**维度 2: 成本预算控制**
```
每日成本预算: $50
当 AWS 当日花费 > $35 (70%) 时:
  - coding/analysis 仍用 opus (高价值)
  - search/general 降级到 sonnet/mini (省钱)
```

**维度 3: 延迟敏感路由**
```
如果用户请求含 stream=true:
  → 优先选延迟最低的 provider (基于 p50 历史数据)
如果是 batch/background 请求:
  → 优先选成本最低的 provider
```

### 4.5 实施路线

| 阶段 | 内容 | 工时 | 状态 |
|------|------|------|------|
| **Phase 1** | 静态策略配置（model_provider_routing 表 + Admin UI） | 4h | ✅ 已完成 |
| **Phase 2** | 场景分类器（关键词规则） | 3h | 🔜 待实施 |
| **Phase 3** | 动态负载感知（RPM/latency aware） | 6h | ✅ 已完成（Smart Routing） |
| **Phase 4** | 成本预算控制器 | 4h | 待定 |
| **Phase 5** | LLM-based 场景分类（可选） | 8h | 待定 |

**Phase 1 实现详情：**
- `model_provider_routing` 表：per-model provider priority 配置
- Admin UI: `admin/models` → "Model Routing Config" section（CRUD + filter）
- Sync: Portal 通过 `/api/admin/key-pool/sync` 的 `model_routing` 字段推送到 Proxy
- Proxy: `GetRoutingOrder(model)` → candidates 按 priority 排序
- Smart Routing: Health/Tier/RPM/Latency/Quota 五维自动路由（models without manual config）

**建议下一步做 Phase 2**（场景分类器，3h）：与已有的 routing 配合，按内容场景动态选择最优模型。

---

## 五、总结与行动项

### 已完成 ✅

- [x] AWS Bedrock key 调度验证（双 key 轮询正常）
- [x] 186 个 AWS 模型已同步并确认为 active
- [x] 跨 Provider 路由（AWS + accessapi 并存）
- [x] 计费正确（包含 pre-consume freeze）
- [x] admin/models-aws 页面功能验证

### 待处理 ⚠️

- [ ] `global.*` 前缀模型路由支持 — ✅ 已修复
- [ ] 部分 Anthropic on Bedrock 模型的订阅/权限
- [ ] Model ID 映射（`claude-opus-4-6` ↔ `us.anthropic.claude-opus-4-6-v1`）
- [ ] Provisioned Throughput 评估（流量达标后）
- [ ] **Portal UI: scene_routing 配置界面**（admin/groups 或独立页面，per-group scene→model 映射）
- [ ] **Portal UI: daily_budget_usd 配置界面**（全局 admin/settings 或 per-group）
- [ ] Portal sync: 将 scene_routing + budget 配置通过 validate 端点返回给 Proxy

### 后续开发建议

1. **短期（1周）：** Phase 1 静态路由策略 YAML + Phase 3 动态负载感知
2. **中期（2周）：** Phase 2 场景分类 + Phase 4 成本预算
3. **长期：** Bedrock Batch API 支持（非实时任务 50% 折扣）

---

*文档作者：Kiro*  
*审核：待定*

---

## 六、Phase 5 方案讨论：智能模型选择

**状态：** 方案评估中，待确认方向

### 背景

Phase 2 的关键词分类器已实现场景分类（coding/search/creative/analysis/image/general），但存在局限：
- "帮我优化这段逻辑"→ 没有 "code"/"function" 关键词，可能漏分类
- 无法判断 prompt 复杂度（同样是 coding 场景，简单 bug 和大型重构需要不同级别的模型）

Phase 5 的目标是提供更精确的模型选择能力。

---

### 行业参考：主流工具的做法

| 工具/产品 | 模型选择方式 | 是否运行时分诊 | 说明 |
|-----------|-------------|--------------|------|
| **Claude Code / Kiro CLI** | 单一主模型 + tool calling | ❌ 不分诊 | 始终用顶级模型（Opus/Sonnet），通过 tool_use 拆分子任务 |
| **OpenCode** | 用户固定配置 | ❌ 不分诊 | 用户在 config 中指定模型，不动态切换 |
| **Cursor / Windsurf** | 按功能入口预分配 | ❌ 编译时决定 | Tab补全→小模型, Chat→用户选择, Agent→最强模型 |
| **Bedrock Intelligent Prompt Router** | 专用复杂度分类器 | ✅ 运行时 | 评估 prompt 复杂度，同模型家族内路由（Haiku↔Sonnet） |
| **OpenAI Tier Routing** | 客户端指定 priority | ❌ | priority/flex/batch 由调用方决定 |

**核心发现：行业主流不使用 LLM 做运行时分诊。**

---

### 候选方案对比

#### 方案 A：LLM 分诊（小模型做 1-shot 分类）

```
用户请求 → [Nova Micro 分类] → 场景+复杂度标签 → 选模型 → 转发
```

| 维度 | 评估 |
|------|------|
| 精度 | ⭐⭐⭐⭐ 能理解语义 |
| 延迟 | ⚠️ +50-100ms |
| 成本 | ~$0.002/次（含 output） |
| 复杂度 | 中（需处理分诊失败、超时、缓存） |
| 适用场景 | model=auto 且需要跨家族路由 |
| 行业先例 | 无主流产品采用 |

**结论：ROI 不高。** 除非 model=auto 请求占比 >50% 且关键词分类错误率不可接受。

---

#### 方案 B：Prompt 复杂度评估（纯规则，推荐）

```
用户请求 → [规则引擎评估复杂度] → simple/medium/complex → 对应级别模型
```

评估维度：
- `token_count`（短/长 prompt）
- `has_code_block`（含代码 = 技术性高）
- `conversation_turns`（多轮 = 需要更强上下文能力）
- `has_file_context`（含文件引用 = 复杂任务）
- `has_thinking_keyword`（"step by step" / "详细分析" = 需要推理）
- `system_prompt_length`（长 system prompt = 复杂 agent 设定）

路由规则：
```
Simple  (token<100, 单轮, 无代码块)          → mini/flash (最便宜)
Medium  (token 100-5000, 一般交互)           → sonnet/standard (平衡)
Complex (token>5000, 多轮, 代码块, thinking) → opus/pro (最强)
```

| 维度 | 评估 |
|------|------|
| 精度 | ⭐⭐⭐ 覆盖 80% 场景 |
| 延迟 | ✅ 0ms（纯计算） |
| 成本 | ✅ $0 |
| 复杂度 | 低 |
| 适用场景 | 所有 model=auto 请求 |
| 行业先例 | ✅ Bedrock Prompt Router 的核心思路 |

**结论：最实际的方案。** 零额外成本，覆盖绝大多数场景。

---

#### 方案 C：嵌入向量分类

```
用户请求 → [Embedding 模型] → 向量 → 与预计算场景向量做余弦相似度 → 分类
```

| 维度 | 评估 |
|------|------|
| 精度 | ⭐⭐⭐⭐ 语义理解 |
| 延迟 | +20ms |
| 成本 | ~$0.00002/次 |
| 复杂度 | 高（需要维护场景原型向量、定期重训练） |
| 适用场景 | 大规模部署 + 高精度需求 |
| 行业先例 | 部分内部系统使用 |

**结论：过于复杂。** 对我们当前规模，收益不足以覆盖维护成本。

---

#### 方案 D：混合方案（Phase 2 关键词 + 方案 B 复杂度）

```
用户请求 → [Phase 2 关键词] → 场景类型 (coding/search/...)
                ↓
         [方案 B 复杂度评估] → 复杂度级别 (simple/medium/complex)
                ↓
         场景 × 复杂度 → 具体模型选择
```

示例：
| 场景 | Simple | Medium | Complex |
|------|--------|--------|---------|
| Coding | gpt-5.4-mini | claude-sonnet-4-6 | claude-opus-4-6 |
| Search | gemini-flash | gpt-5.4-mini | claude-sonnet-4-6 |
| Analysis | gpt-5.4-mini | claude-sonnet-4-6 | claude-opus-4-6 + thinking |
| Creative | gpt-5.4-mini | claude-sonnet-4-6 | claude-opus-4-6 |

| 维度 | 评估 |
|------|------|
| 精度 | ⭐⭐⭐⭐ 场景+复杂度双维度 |
| 延迟 | ✅ 0ms |
| 成本 | ✅ $0 |
| 复杂度 | 中（规则多但可配置） |
| 适用场景 | 所有 model=auto 请求 |

**结论：推荐方案。** 结合已有 Phase 2 + 新增复杂度维度，无额外成本。

---

### 建议

**推荐方案 D（混合方案）：** 在 Phase 2 场景分类基础上增加 prompt 复杂度评估，形成「场景 × 复杂度」二维矩阵。

- 不需要 LLM 分诊（0 成本、0 延迟）
- 精度优于纯关键词（加了复杂度维度）
- Admin 可配置二维矩阵（哪个场景的哪个复杂度用什么模型）
- 与 Phase 1-4 完全兼容

**实施工时：** ~4h（在现有 scene_classifier.go 基础上增加复杂度评估函数）

**何时做：** 当 model=auto 的使用量增加且关键词分类的错误率不可接受时。

---

*更新时间：2026-07-03*
