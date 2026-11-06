# AWS Bedrock 双轨路由方案 — 分析与设计报告

> **Date:** 2026-05-24 → 2026-06-24
> **Scope:** ai-proxy-go + ai-portal-go
> **Status:** Code complete, awaiting AWS credential validation
>
> **Commits:** Portal `d0fae17` / Proxy `0160618`
> **Updated:** 2026-06-10 — Status unchanged; still needs AWS credential testing

---

## 1. 背景与目标

### 1.1 当前痛点

当前 proxy 的路由 + cache 能力存在以下问题：

| 痛点 | 现状 | 影响 |
|------|------|------|
| **单一路由通道** | 所有用户共用同一套 `resolveProviderAndKey` → `smartRoute` 链路 | VIP 用户无法获得确定性低延迟服务 |
| **Cache 无隔离** | `AffinityCache` 虽然按 `priority` 隔离，但底层 key pool 是共享的 | 高优先级用户可能被普通用户的缓存污染 |
| **Provider 质量参差** | `accessapi`、`alibaba/laozhang` 等 reseller 的 Claude/GPT 质量不如 AWS Bedrock 官方通道 | VIP 用户体验不稳定 |
| **无 tier-aware 路由** | `access_mode` 仅影响 key priority（high/normal/economy），不影响 provider 选择 | 无法为不同 tier 分配不同品质的 upstream |

### 1.2 目标

建立 **双轨并行机制**：

```
┌─────────────────────────────────────────────────────────────┐
│                     用户请求 (API Key)                       │
│                         │                                   │
│              ┌──────────┴──────────┐                        │
│              │   Auth Middleware   │                        │
│              │  (validateKey)      │                        │
│              └──────────┬──────────┘                        │
│                         │                                   │
│         ┌───────────────┼───────────────┐                   │
│         │               │               │                   │
│    VIP / Premium      Enterprise      General / Economy     │
│    (Track A)          (Track A)       (Track B)             │
│         │               │               │                   │
│    ┌────▼────┐    ┌────▼────┐    ┌────▼────┐               │
│    │ AWS     │    │ AWS     │    │ Existing│               │
│    │ Bedrock │    │ Bedrock │    │ Routing │               │
│    │ Track   │    │ Track   │    │ Track   │               │
│    └────┬────┘    └────┬────┘    └────┬────┘               │
│         │               │               │                   │
│    Dedicated      Dedicated       Shared Cache              │
│    Cache          Cache           (existing)                │
│         │               │               │                   │
│    ┌────▼────┐    ┌────▼────┐    ┌────▼────┐               │
│    │ Claude  │    │ Claude  │    │ accessapi│               │
│    │ GPT-OSS │    │ GPT-OSS │    │ laozhang │               │
│    │ (official)│   │ (official)│   │ deepseek │               │
│    └─────────┘    └─────────┘    └─────────┘               │
└─────────────────────────────────────────────────────────────┘
```

**Track A (VIP/Premise/Enterprise)**：
- 路由到 **AWS Bedrock**（官方 Claude + GPT-OSS）
- 独立 key pool、独立 cache、独立 affinity
- 确定性低延迟、高 SLA

**Track B (General/Economy)**：
- 走现有 `smartRoute` 通道（accessapi、laozhang、deepseek 等 reseller）
- 共享现有 cache 和 key pool
- 成本优先

---

## 2. AWS Bedrock 深度分析

### 2.1 接口协议

Bedrock 提供 **3 种调用方式**，当前 proxy 的 `aws.go` 使用的是 **Converse API**（推荐）：

| API | 层级 | 特点 | 适用场景 |
|-----|------|------|---------|
| **Converse** | 高 | 统一接口，跨模型兼容，支持 streaming | 对话式应用（推荐） |
| **InvokeModel** | 低 | 模型原生格式，需按 provider 定制 payload | 非对话型、需要原生特性 |
| **OpenAI-compatible** | 兼容 | `/openai/v1/chat/completions`，2025年8月发布 | OpenAI SDK 迁移 |

**当前 proxy 实现（`pkg/relay/channel/aws/aws.go`）**：
- 使用 **Converse API** (`/model/{modelId}/converse` 和 `/converse-stream`)
- **SigV4 签名**（AWS 标准 HMAC-SHA256）
- 支持 streaming（`ConverseStream`）
- 请求格式转换：OpenAI `messages` → Bedrock `bedrockRequest`（提取 system prompt 到顶层）
- 模型名映射：`claude-3-5-sonnet` → `anthropic.claude-3-5-sonnet-20240620-v1:0`

**Converse API 请求示例**：
```json
{
  "modelId": "anthropic.claude-sonnet-4-6-20251022-v1:0",
  "messages": [
    {"role": "user", "content": [{"text": "Hello"}]}
  ],
  "system": [{"text": "You are a helpful assistant."}],
  "inferenceConfig": {
    "temperature": 0.7,
    "maxTokens": 1024
  }
}
```

**ConverseStream 响应事件**：
```go
// 事件类型：
// - MessageStart: role
// - ContentBlockDelta: 文本片段 (Delta.Text)
// - MessageStop: stop_reason
// - Metadata: usage (inputTokens, outputTokens)
```

### 2.2 支持的模型（2026年5月）

**Claude 系列（Anthropic）**：
| 模型 | Input/1M | Output/1M | Context | 状态 |
|------|----------|-----------|---------|------|
| Claude Opus 4.6 | $5.00 | $25.00 | 200K | ✅ 可用 |
| Claude Sonnet 4.6 | $3.00 | $15.00 | 200K | ✅ 可用 |
| Claude Sonnet 4.5 | $3.00 | $15.00 | 200K | ✅ 可用 |
| Claude Haiku 4.5 | $1.00 | $5.00 | 200K | ✅ 可用 |

**OpenAI GPT-OSS（2026年新增）**：
| 模型 | Input/1M | Output/1M | 状态 |
|------|----------|-----------|------|
| GPT-OSS 20B | $0.07 | $0.30 | ✅ 可用 |
| GPT-OSS 120B | $0.15 | $0.60 | ✅ 可用 |

> ⚠️ Bedrock 的 OpenAI 兼容端点（`/openai/v1`）目前 **仅支持 GPT-OSS 模型**，不支持其他 Bedrock 模型。Claude 仍需通过 Converse API 调用。

**其他（非 VIP 目标，但可用于 Track B）**：
| 提供商 | 模型 | 价格 |
|--------|------|------|
| DeepSeek | R1 | $0.32/$0.32 |
| Qwen | Qwen3 32B | $0.15/$0.60 |
| Moonshot | Kimi K2.5 | $0.60/$3.00 |
| GLM | GLM 4.7 | $0.60/$2.20 |
| MiniMax | M2.1 | $0.30/$1.20 |

### 2.3 计费模式

**On-Demand（按量付费）**：
- 按 token 计费，无最低消费
- 定价与 Anthropic/OpenAI 官方 API **基本一致**
- 额外费用：AWS 数据传输费（跨区域）、CloudWatch Logs

**Batch Inference（批量推理）**：
- 50% 折扣
- 24小时内返回结果
- 适合离线处理场景

**Provisioned Throughput（预留吞吐）**：
- 按小时计费（$40-$200/小时，取决于模型）
- 1个月或6个月承诺期
- 适合高流量生产环境

**Prompt Caching（提示缓存）**：
- 5分钟缓存：write 1.25x，read 0.1x
- 1小时缓存：write 2.0x，read 0.1x
- 长上下文（>200K tokens）：2x 费率

### 2.4 关键限制

| 限制 | 详情 | 影响 |
|------|------|------|
| **Region 可用性** | 不同模型在不同 region 可用 | 需要选择正确的 region |
| **模型更新滞后** | 新模型比直接 API 晚 1-4 周 | VIP 用户可能无法第一时间使用最新模型 |
| **Tool Calling** | Converse API 支持；OpenAI 兼容端点不支持原生 tool_calls | 需要 adapter 处理 |
| **JSON Schema** | 无原生 `response_format`；需通过 `toolConfig` 模拟 | 结构化输出需要适配 |
| **Cross-region** | +10% 费用 | 成本增加 |

---

## 3. 当前架构适配分析

### 3.1 Proxy 端已有能力

| 组件 | 当前状态 | 适配双轨需要改动 |
|------|---------|-----------------|
| **AWS Adapter** | ✅ 已有 Converse API + SigV4 | 无需改动 |
| **KeyPool** | ✅ 动态 provider 池 | 添加 "bedrock" provider pool |
| **Routing** | ✅ `resolveProviderAndKey` + `smartRoute` | 添加 track 判断逻辑 |
| **Auth Middleware** | ✅ 注入 `access_mode`, `key_type`, `group_id` | 需要新增 VIP 标识字段 |
| **AffinityCache** | ✅ 按 priority 隔离 | 需要添加 "track" 维度 |
| **Model Resolution** | ✅ `relay.GetProviderFromModel` | 需要支持同一模型映射到不同 provider |

### 3.2 Portal 端已有能力

| 组件 | 当前状态 | 适配双轨需要改动 |
|------|---------|-----------------|
| **upstream_keys** | ✅ 支持任意 provider 名称 | 添加 Bedrock keys（provider="amazon"） |
| **user_groups** | ❌ 无 priority/tier 字段 | 需要添加 `routing_track` 或 `priority_tier` |
| **api_keys** | ✅ `key_type`, `access_mode` | 可作为 VIP 判断依据（但不完美） |
| **validateKey** | ❌ 不返回 VIP/priority 标志 | 需要新增 claim |
| **service_models** | ✅ 支持 per-provider pricing | 添加 Bedrock 模型定价 |

---

## 4. 建议方案

### 4.1 方案概述

**核心设计：在 `resolveProviderAndKey` 之前插入 Track 决策层**

```
Request
  ↓
Auth Middleware → validateKey (新增 is_vip flag)
  ↓
Handler (chat/completions)
  ↓
resolveModel() → 确定实际模型名
  ↓
【新增】resolveTrack(c) → "bedrock" or "standard"
  ↓
if track == "bedrock":
    resolveBedrockProviderAndKey() → 只选 Bedrock keys
else:
    resolveProviderAndKey() → 现有 smartRoute
  ↓
Relay → 对应 adapter
  ↓
Post-processing → 独立的 usage recording per track
```

### 4.2 关键改动点

#### 改动 1：Portal 端 — 添加 VIP 标识机制

**选项 A：基于 `api_keys.key_type`（最小改动）**
- `key_type == "enterprise"` 或 `"subscription"` → Track A
- 其他 → Track B
- ❌ 缺点：不够灵活，无法精确控制哪些 enterprise 用户走 Bedrock

**选项 B：新增 `users.routing_track` 字段（推荐）**
```sql
ALTER TABLE users ADD COLUMN routing_track VARCHAR(20) DEFAULT 'standard'
    CHECK (routing_track IN ('bedrock', 'standard', 'auto'));
```
- `bedrock` → 强制走 Bedrock
- `standard` → 走现有通道
- `auto` → 根据模型 availability 自动选择（未来扩展）

**validateKey 响应新增字段**：
```json
{
  "routing_track": "bedrock",
  "is_vip": true
}
```

#### 改动 2：Proxy 端 — Track 决策 + 独立 KeyPool 查询

**`resolveTrack(c *gin.Context) string`**（新增）：
```go
func resolveTrack(c *gin.Context) string {
    track := c.GetString("routing_track")
    if track == "bedrock" {
        return "bedrock"
    }
    // 未来扩展：auto 模式根据模型可用性选择
    return "standard"
}
```

**`resolveBedrockProviderAndKey()`**（新增）：
```go
func resolveBedrockProviderAndKey(ctx context.Context, model, preferredKeyID string) 
    (string, *keypool.KeyInternal, string, bool, error) {
    
    // 1. 只查询 provider="amazon" 的 keys
    bedrockKeys := keyPoolManager.GetKeysForProvider("amazon")
    
    // 2. 模型名映射：claude-sonnet-4-6 → anthropic.claude-sonnet-4-6-xxx
    bedrockModelID := mapToBedrockModelID(model)
    
    // 3. 选择最佳 key（简化版 smartRoute，只考虑 health + LRU）
    //    不区分 priority tier（Bedrock keys 都是 premium）
    
    // 4. 返回 provider="amazon", key, upstreamModelName
}
```

**`handleChatCompletions` 修改**：
```go
// 现有逻辑前添加 track 判断
track := resolveTrack(c)
if track == "bedrock" {
    provider, upstreamKey, upstreamModelName, isFallback, err := 
        resolveBedrockProviderAndKey(c.Request.Context(), req.Model, preferredKeyID)
    // ... 后续与现有逻辑相同
} else {
    provider, upstreamKey, upstreamModelName, isFallback, err := 
        resolveProviderAndKey(c.Request.Context(), req.Model, userPriority, preferredKeyID)
}
```

#### 改动 3：独立 Cache（可选，Phase 2）

**Phase 1**：复用现有 `AffinityCache`，cache key 增加 track 维度
```go
cacheKey := fmt.Sprintf("%s:%s:%s:%s", track, priority, model, fingerprint)
```

**Phase 2**（未来）：完全独立的 Bedrock Cache 实例
- 独立的 Redis key prefix (`bedrock:affinity:`)
- 独立的 TTL 策略
- 独立的预热机制

#### 改动 4：Portal 端 — Bedrock Key 管理

**upstream_keys 新增 Bedrock 条目**：
```sql
INSERT INTO upstream_keys (provider, key_encrypted, key_preview, tier, base_url, models, enabled)
VALUES (
    'amazon',
    'encrypted_aws_creds',
    'AKIA...',
    'premium',
    'https://bedrock-runtime.us-east-1.amazonaws.com',
    '["claude-sonnet-4-6", "claude-opus-4-6", "gpt-oss-120b"]',
    true
);
```

**service_models 新增 Bedrock 定价**：
```sql
INSERT INTO service_models (provider, product_id, api_model_name, input_price_cache_miss, output_price, billing_type)
VALUES
    ('amazon', 'claude-sonnet-4-6', 'claude-sonnet-4-6', 3.00, 15.00, 'per_token'),
    ('amazon', 'claude-opus-4-6', 'claude-opus-4-6', 5.00, 25.00, 'per_token'),
    ('amazon', 'gpt-oss-120b', 'gpt-oss-120b', 0.15, 0.60, 'per_token');
```

#### 改动 5：模型名映射表

Bedrock 使用 AWS 模型 ID 格式，需要映射：

| 用户请求模型 | Bedrock Model ID |
|-------------|-----------------|
| `claude-sonnet-4-6` | `us.anthropic.claude-sonnet-4-6-20251022-v1:0` |
| `claude-opus-4-6` | `us.anthropic.claude-opus-4-6-20251022-v1:0` |
| `claude-haiku-4-5` | `us.anthropic.claude-haiku-4-5-20251001-v1:0` |
| `gpt-oss-20b` | `openai.gpt-oss-20b-1:0` |
| `gpt-oss-120b` | `openai.gpt-oss-120b-1:0` |

> 注意：`us.` prefix 表示 us-east-1 region。不同 region 的前缀不同。

映射实现：在 proxy 的 `aws.go` 或 Portal 的 model sync 中维护。

### 4.3 数据流对比

**Track A (Bedrock)**：
```
User (VIP) → API Key → validateKey → routing_track="bedrock"
  → resolveBedrockProviderAndKey → provider="amazon"
  → AWSAdaptor.ToProvider() → Converse API request (SigV4)
  → Bedrock Runtime (us-east-1)
  → AWSAdaptor.FromProvider() → OpenAI format response
  → recordUsageAsync (provider="amazon")
```

**Track B (Standard)**：
```
User (General) → API Key → validateKey → routing_track="standard"
  → resolveProviderAndKey → smartRoute
  → provider="accessapi" / "alibaba" / "deepseek"
  → OpenAICompatibleAdaptor → standard HTTP request
  → reseller upstream
  → recordUsageAsync (provider="accessapi" / ...)
```

---

## 5. Risk 分析

| # | Risk | 概率 | 影响 | 缓解措施 |
|---|------|------|------|---------|
| 1 | **AWS 凭证泄露** | 中 | 高 | 使用 Portal 加密存储（已有），限制 Bedrock key 的 IAM 权限（最小权限原则） |
| 2 | **Bedrock Region 不可用** | 低 | 高 | 多 region 部署（us-east-1 + us-west-2），自动 failover 到 Track B |
| 3 | **模型更新滞后** | 高 | 中 | VIP 用户可能无法第一时间使用 Claude 最新版本；需明确沟通 SLA |
| 4 | **成本激增** | 中 | 高 | Bedrock 定价与直接 API 一致，但 AWS 数据传输费额外；需要成本监控 |
| 5 | **Track 切换逻辑 bug** | 中 | 高 | 添加 feature flag，灰度发布；E2E 测试覆盖双轨场景 |
| 6 | **Cache 污染** | 低 | 中 | Phase 1 使用 track 前缀隔离 cache key；监控 cache hit rate |
| 7 | **SigV4 签名过期** | 低 | 中 | 每次请求重新签名（已有实现），但需处理时钟同步问题 |
| 8 | **Bedrock 限流/配额** | 中 | 中 | 监控 RPM/TPM，设置合理的 `rpm_limit` 和 `tpm_limit` |
| 9 | **Provisioned Throughput 承诺风险** | 低 | 中 | 初期使用 On-Demand，流量稳定后再考虑 Provisioned Throughput |
| 10 | **Converse API 与 InvokeModel 行为差异** | 中 | 低 | Claude 系列在 Converse API 下测试充分；GPT-OSS 需要额外测试 |

---

## 6. 工作量评估（用户选定方案：Phase 1 + 核心增强，一次到位 ~18h）

### 设计原则：Clean 隔离，完全独立

- Bedrock track 与 Standard track **零共享逻辑**
- Portal 端配置完全隔离（独立的 Bedrock key 管理、独立的 group track 设置）
- Proxy 端路由链路完全隔离（独立的 resolve 函数、独立的 cache、独立的 key pool 查询）
- 互不干扰，任何一方的改动不会影响另一方

### 实施清单

#### Portal 端（~5h）

| # | 任务 | 文件 | 预估工时 |
|---|------|------|---------|
| 1 | 新增 `users.routing_track` + `user_groups.routing_track` 字段 + migration | migration SQL | 15min |
| 2 | validateKey 返回 `routing_track` | `apikeys.go` | 30min |
| 3 | Admin UI：group 编辑添加 routing_track 选择 | admin template | 1h |
| 4 | Admin UI：upstream key 管理添加 Bedrock 专用表单 | admin template | 1h |
| 5 | 添加 Bedrock service_models 数据（定价） | `models.go` / DB seed | 30min |
| 6 | Portal 端成本监控：usage_records 按 track 拆分统计 | `webhook.go` + SQL | 1.5h |

#### Proxy 端（~10h）

| # | 任务 | 文件 | 预估工时 |
|---|------|------|---------|
| 7 | Auth middleware 读取 `routing_track` | `auth.go` | 15min |
| 8 | `resolveTrack()` 函数 | `handlers.go` | 15min |
| 9 | `resolveBedrockProviderAndKey()` — 完全独立的 Bedrock 路由 | `handlers.go` | 2h |
| 10 | `GetKeysForProvider("amazon")` + provider 过滤 | `manager.go` | 30min |
| 11 | Bedrock model name → AWS model ID 映射表 | `aws.go` + `bedrock_models.go` | 1h |
| 12 | handleChatCompletions 插入 track 判断 | `handlers.go` | 30min |
| 13 | handleEmbeddings + handleImages 同样修改 | `handlers.go` | 30min |
| 14 | 独立 Bedrock AffinityCache（Redis prefix: `bedrock:`） | `affinity.go` | 1.5h |
| 15 | Bedrock 多 region failover（us-east-1 → us-west-2） | `aws.go` + `handlers.go` | 2h |
| 16 | UsageRecord 添加 `routing_track` 字段 | `billing.go` + `handlers.go` | 30min |
| 17 | E2E 测试：双轨并行验证 | `e2e-run.sh` | 1h |

#### 文档与部署（~1h）

| # | 任务 | 预估工时 |
|---|------|---------|
| 18 | 文档更新 | 30min |
| 19 | 部署 checklist + feature flag | 30min |

**总计：~18 小时（2-3 个工作日）**

### 明确排除的 Phase 2 项（后续迭代）

| 任务 | 排除原因 |
|------|---------|
| Batch Inference | 需要独立的 S3 集成 + 异步队列，非核心 |
| Provisioned Throughput 自动化 | 需要流量预测算法，初期 On-Demand 足够 |
| 灰度发布机制 | `routing_track` 字段本身就是开关，无需额外机制 |

---

## 7. 建议的实施顺序（Clean 隔离方案）

```
Day 1: Portal Schema + API
  ├─ migration: users.routing_track, user_groups.routing_track
  ├─ validateKey 返回 routing_track
  ├─ 添加 Bedrock upstream_keys + service_models
  └─ 提交 Portal

Day 2: Portal Admin UI
  ├─ Group 编辑：routing_track 选择器（standard / bedrock）
  ├─ Upstream Key 管理：Bedrock 专用表单（AWS creds + region + model list）
  └─ 提交 Portal

Day 3: Proxy Core — Bedrock 路由链路
  ├─ Auth middleware 读取 routing_track
  ├─ resolveTrack() → resolveBedrockProviderAndKey()
  ├─ GetKeysForProvider("amazon") 过滤逻辑
  ├─ Bedrock model ID 映射表
  └─ 提交 Proxy

Day 4: Proxy Integration + Cache + Failover
  ├─ handleChatCompletions / handleEmbeddings / handleImages 插入 track 判断
  ├─ 独立 Bedrock AffinityCache（Redis prefix: `bedrock:`）
  ├─ 多 region failover（us-east-1 primary, us-west-2 fallback）
  ├─ UsageRecord 添加 routing_track
  └─ 提交 Proxy

Day 5: 测试 & 部署
  ├─ E2E 双轨测试（VIP → Bedrock，普通 → standard）
  ├─ 手动验证：Bedrock Claude/GPT-OSS 端到端
  ├─ 回滚计划验证
  └─ 提交文档更新
```

---

## 8. 关键决策点

| 决策 | 选项 A | 选项 B | 推荐 |
|------|--------|--------|------|
| **VIP 标识方式** | `key_type` 判断 | `users.routing_track` 字段 | **B** — 更灵活 |
| **Bedrock 调用方式** | Converse API（现有） | OpenAI 兼容端点 | **A** — Claude 不支持兼容端点 |
| **Cache 隔离** | track 前缀（Phase 1） | 独立 Cache 实例（Phase 2） | **A 先，B 后** |
| **Region** | us-east-1 单 region | 多 region + failover | **A 先，B 后** |
| **定价模式** | On-Demand | On-Demand + Batch | **A 先** |
| **模型映射位置** | Proxy 端 (`aws.go`) | Portal 端 (model sync) | **Proxy 端** — 减少 sync 复杂度 |

---

## 9. 与现有 P0/P1 修复的关系

当前已完成的 P0/P1 修复（timestamp replay、decrypt fallback、CORS、rate limit、nonce cache、billing cache、fallback group billing、per-request billing）**不影响**双轨机制实施。双轨机制是独立的新功能，但建议在所有 P0/P1 稳定后再开始，避免并行开发增加风险。


---

## 10. 实现状态（Implementation Status）

> **Date:** 2026-06-24  
> **Commits:** Portal `d0fae17` / Proxy `0160618`

### 10.1 Portal 端

| # | 任务 | 文件 | 状态 |
|---|------|------|------|
| 1 | `users.routing_track` + `user_groups.routing_track` 字段 + migration | `202606241700_add_routing_track.sql` | ✅ |
| 2 | `usage_records.routing_track` 字段 + migration | `202606241800_usage_records_routing_track.sql` | ✅ |
| 3 | `validateKey` 返回 `routing_track`（user-level overrides group-level） | `apikeys.go` | ✅ |
| 4 | `webhook.go` UsageRecord + INSERT 包含 `routing_track` | `webhook.go` | ✅ |
| 5 | `groups.go` create/update/list/get 支持 `routing_track` | `groups.go` | ✅ |
| 6 | Admin UI: group 编辑 modal 添加 routing_track 下拉选择 | `admin-groups.html` | ✅ |
| 7 | Admin UI: upstream key 添加 Bedrock provider 提示 + 多 region base URL | `admin-keypool.html` | ✅ |
| 8 | Admin UI: "同步 AWS Bedrock" 按钮 + `syncBedrock()` JS | `admin-models.html` | ✅ |
| 9 | API: `POST /api/admin/service-models/sync-bedrock` 端点 | `admin.go` | ✅ |

### 10.2 Proxy 端

| # | 任务 | 文件 | 状态 |
|---|------|------|------|
| 1 | Auth middleware 注入 `routing_track` 到 gin context | `auth.go` | ✅ |
| 2 | `resolveTrack()` — 读取 gin context | `handlers.go` | ✅ |
| 3 | `resolveBedrockProviderAndKey()` — 完全独立的 Bedrock 路由 | `handlers.go` | ✅ |
| 4 | 多 region failover（`amazon` → `amazon-us-west-2` → `amazon-eu-west-1`） | `handlers.go` | ✅ |
| 5 | Chat completions fallback：Bedrock 失败 → 标准路由 | `handlers.go` | ✅ |
| 6 | Embeddings/images fallback：Bedrock 失败 → 标准路由 | `handlers.go` | ✅ |
| 7 | `UsageRecordContext` + `recordUsageAsync` 传递 `RoutingTrack` | `handlers.go` + `billing.go` | ✅ |
| 8 | `keypool/manager.go` `GetKeysForProvider()` 按 provider 隔离查询 | `manager.go` | ✅ |
| 9 | `aws.go` `MapModelToBedrock()` 导出 + 更新模型映射 | `aws.go` | ✅ |
| 10 | AffinityCache track 隔离（cache key 包含 track 前缀） | `affinity.go` | ✅ |

---

## 11. 设计讨论 Q&A

### Q1: sync AWS 和 sync OpenRouter 是否冲突？

**A: 不会冲突。**
- Sync OpenRouter 的 provider 是 `openrouter`，Sync Bedrock 的 provider 是 `amazon`
- 两者都使用 `ON CONFLICT (provider, product_id) DO UPDATE`，provider 不同自然不冲突
- 同一个物理模型（如 Claude Sonnet 4.6）在 service_models 中会有两条记录（`anthropic` 和 `amazon`），Portal 计费时通过 `record.Provider` 区分

### Q2: 选择 AWS 服务的粒度是什么？

**A: Group 级别。**
- `users.routing_track` 优先，`user_groups.routing_track` fallback
- 不是按 model、套餐或 enterprise 实现的
- 如需更细粒度，可扩展：model 白名单、subscription plan 映射、enterprise 字段

### Q3: 如果 Bedrock 失败，有无 fallback？

**A: 有。**
- Chat completions：Bedrock 失败时自动 fallback 到标准路由（`resolveProviderAndKey`）
- Embeddings/images：同样支持 fallback
- 日志记录：`"Bedrock routing failed, falling back to standard"`

### Q4: 多 region 如何配置？

**A: 每个 region = 一个独立 provider。**
- `amazon` → us-east-1
- `amazon-us-west-2` → us-west-2
- `amazon-eu-west-1` → eu-west-1
- Failover：按顺序尝试，无可用 keys 时自动切到下一个 region

### Q5: Usage 统计是统一还是分开？

**A: 数据已分开，Dashboard 待做。**
- `usage_records.routing_track` 字段已存在
- 可按 track 拆分统计：`SELECT routing_track, SUM(total_cost) FROM usage_records ...`
- 但 Admin Dashboard 尚未添加 per-track 成本可视化

---

## 12. 简化与折中

| 原计划 | 实际实现 | 原因 |
|--------|---------|------|
| 独立 Bedrock Cache 实例（Redis prefix + 独立 TTL） | `buildAffinityKey` 加入 track 前缀 | Cache 实例共享，key 隔离即可达到同样效果，改动最小 |
| Bedrock Prompt Caching 专用参数 | 复用现有 `cacheHitPrice` / `CachedTokens` | Bedrock Converse API 的 prompt caching 行为与现有 cache 统计兼容 |
| AWSAdaptor 内部 region failover | `resolveBedrockProviderAndKey` 顺序尝试 provider | 更简洁，不需要改动 adaptor 内部逻辑 |
| Admin UI 全新 Bedrock 专用表单 | 现有表单 + provider 提示 + auto-fill | 前端改动最小化 |
| AWS API 实时拉取模型列表 | 硬编码模型列表 | 没有 AWS 凭证，无法测试 API 调用 |
| 灰度发布机制 | `routing_track` 字段本身就是开关 | 不需要额外的百分比/AB 测试逻辑 |

---

## 13. 测试前待办（需 AWS 凭证）

| # | 任务 | 说明 |
|---|------|------|
| 1 | 执行 migration | `202606241700_add_routing_track.sql` + `202606241800_usage_records_routing_track.sql` |
| 2 | 同步 Bedrock 模型 | Admin → Models → 点击「同步 AWS Bedrock」 |
| 3 | 配置 Bedrock upstream key | Admin → Key Pool → 添加上行 Key，provider 选 `amazon`，API Key 填 `AKIA...:secret...` |
| 4 | 设置 group routing_track | Admin → Groups → 编辑目标 group，routing_track 选「AWS Bedrock」 |
| 5 | E2E 双轨测试 | VIP 用户请求 → 验证路由到 Bedrock；普通用户 → 验证走标准通道 |
| 6 | Fallback 测试 | 禁用所有 Bedrock keys → VIP 用户请求应 fallback 到标准通道 |
| 7 | 成本监控验证 | 检查 usage_records 中 routing_track 是否正确区分 |
| 8 | 多 region failover 测试 | 禁用 us-east-1 keys → 验证自动切换到 us-west-2 |

---

## 14. 已知限制 & 后续迭代

| 限制 | 说明 | 优先级 |
|------|------|--------|
| 模型更新滞后 | Bedrock 新模型比直接 API 晚 1-4 周 | 低（用户已接受） |
| 无 per-track 成本 Dashboard | Admin Dashboard 未添加 Bedrock/Standard 成本拆分 | 中 |
| Batch Inference 未实现 | 50% 折扣的批量推理通道 | 低 |
| Provisioned Throughput 未实现 | 预留吞吐自动管理 | 低 |
| 按 model 粒度路由 | 不能指定"只有 claude 走 Bedrock" | 低 |
| 按 enterprise 粒度路由 | 不能指定"Enterprise A 走 Bedrock" | 低 |

---

## 15. 模型 ID 前缀剥离优化（2026-06-03 补充）

### 背景问题
AWS Bedrock 的 Model ID 带点分隔前缀，例如：
- `google.gemma-3-12b-it`
- `anthropic.claude-3-opus`
- `us.anthropic.claude-sonnet-4-6-20251022-v1:0`

原始同步逻辑将完整 ID 同时写入 `product_id` 和 `api_model_name`，导致用户调用 API 时必须输入 `google.gemma-3-12b-it`，操作繁琐。

### 与 General 模型的对比

| 模型来源 | ID 分隔符 | 前缀剥离逻辑 | 所在文件 |
|---------|----------|-------------|---------|
| OpenRouter | `/` | `generateAPIModelName()` 按 `/` 分割取最后一段 | `internal/routes/models.go` |
| Provider `/v1/models` | `/` | 同上 | `internal/routes/models.go` |
| **AWS Bedrock** | `.` | **新增** `generateBedrockAPIModelName()` 按 `.` 分割取最后一段 | `internal/routes/models.go` |

### 已实现改动

**1. 新增前缀剥离函数**
```go
// internal/routes/models.go
func generateBedrockAPIModelName(productID string) string {
    if !strings.Contains(productID, ".") {
        return productID
    }
    parts := strings.Split(productID, ".")
    return parts[len(parts)-1]
}
```

**2. 同步时自动应用剥离**
```go
// internal/routes/admin.go — syncBedrockModels()
apiModelName := generateBedrockAPIModelName(m.ProductID)

// product_id 存完整 AWS ID（用于实际调用 Bedrock API）
// api_model_name 存剥离后的简洁名（用户可见、API 调用输入）
INSERT INTO service_models (provider, product_id, api_model_name, ...)
VALUES ("amazon", m.ProductID, apiModelName, ...)
```

**效果示例：**

| 原始 AWS Model ID (`product_id`) | 存储的 `api_model_name` | 用户 API 调用输入 |
|----------------------------------|------------------------|------------------|
| `google.gemma-3-12b-it` | `gemma-3-12b-it` | `gemma-3-12b-it` |
| `anthropic.claude-3-opus` | `claude-3-opus` | `claude-3-opus` |
| `claude-opus-4-6` (无前缀) | `claude-opus-4-6` | `claude-opus-4-6` |

### Portal → Proxy 映射链路验证

**关键结论：前缀剥离只影响用户侧输入，不影响上游调用。**

完整数据流：
1. Portal 存储：`product_id = "google.gemma-3-12b-it"`, `api_model_name = "gemma-3-12b-it"`
2. Portal Sync 接口 `/api/admin/models/sync` 返回 `{productId, apiModelName}`
3. Proxy `updateModelNameMap` 建立映射 `"amazon"["gemma-3-12b-it"] = "google.gemma-3-12b-it"`
4. 用户调用 API 输入 `gemma-3-12b-it`
5. Proxy `ResolveUpstreamModelName("amazon", "gemma-3-12b-it")` → `"google.gemma-3-12b-it"`
6. Proxy `GetEndpoint` 生成 `/model/google.gemma-3-12b-it/converse`

Proxy 关键代码：
```go
// ai-proxy-go/pkg/keypool/manager.go
func (m *Manager) updateModelNameMap(entries []ModelNameMapEntry) {
    mm[e.Provider][e.APIModelName] = e.ProductID  // api_model_name → product_id
}

func (m *Manager) ResolveUpstreamModelName(provider, apiModelName string) string {
    return m.modelNameMap[provider][apiModelName]
}
```

### 多 Region 配置建议

偏好方案：**预设多个 provider**，通过 user group 动态分配 region。

**完整 33 个商业区域（代码已自动注册）：**

| Provider ID | Region | Runtime Endpoint |
|-------------|--------|------------------|
| `amazon-us-east-1` | US East (N. Virginia) | `https://bedrock-runtime.us-east-1.amazonaws.com` |
| `amazon-us-east-2` | US East (Ohio) | `https://bedrock-runtime.us-east-2.amazonaws.com` |
| `amazon-us-west-1` | US West (N. California) | `https://bedrock-runtime.us-west-1.amazonaws.com` |
| `amazon-us-west-2` | US West (Oregon) | `https://bedrock-runtime.us-west-2.amazonaws.com` |
| `amazon-ca-central-1` | Canada (Central) | `https://bedrock-runtime.ca-central-1.amazonaws.com` |
| `amazon-ca-west-1` | Canada West (Calgary) | `https://bedrock-runtime.ca-west-1.amazonaws.com` |
| `amazon-eu-central-1` | Europe (Frankfurt) | `https://bedrock-runtime.eu-central-1.amazonaws.com` |
| `amazon-eu-central-2` | Europe (Zurich) | `https://bedrock-runtime.eu-central-2.amazonaws.com` |
| `amazon-eu-north-1` | Europe (Stockholm) | `https://bedrock-runtime.eu-north-1.amazonaws.com` |
| `amazon-eu-south-1` | Europe (Milan) | `https://bedrock-runtime.eu-south-1.amazonaws.com` |
| `amazon-eu-south-2` | Europe (Spain) | `https://bedrock-runtime.eu-south-2.amazonaws.com` |
| `amazon-eu-west-1` | Europe (Ireland) | `https://bedrock-runtime.eu-west-1.amazonaws.com` |
| `amazon-eu-west-2` | Europe (London) | `https://bedrock-runtime.eu-west-2.amazonaws.com` |
| `amazon-eu-west-3` | Europe (Paris) | `https://bedrock-runtime.eu-west-3.amazonaws.com` |
| `amazon-ap-east-2` | Asia Pacific (Taipei) | `https://bedrock-runtime.ap-east-2.amazonaws.com` |
| `amazon-ap-northeast-1` | Asia Pacific (Tokyo) | `https://bedrock-runtime.ap-northeast-1.amazonaws.com` |
| `amazon-ap-northeast-2` | Asia Pacific (Seoul) | `https://bedrock-runtime.ap-northeast-2.amazonaws.com` |
| `amazon-ap-northeast-3` | Asia Pacific (Osaka) | `https://bedrock-runtime.ap-northeast-3.amazonaws.com` |
| `amazon-ap-south-1` | Asia Pacific (Mumbai) | `https://bedrock-runtime.ap-south-1.amazonaws.com` |
| `amazon-ap-south-2` | Asia Pacific (Hyderabad) | `https://bedrock-runtime.ap-south-2.amazonaws.com` |
| `amazon-ap-southeast-1` | Asia Pacific (Singapore) | `https://bedrock-runtime.ap-southeast-1.amazonaws.com` |
| `amazon-ap-southeast-2` | Asia Pacific (Sydney) | `https://bedrock-runtime.ap-southeast-2.amazonaws.com` |
| `amazon-ap-southeast-3` | Asia Pacific (Jakarta) | `https://bedrock-runtime.ap-southeast-3.amazonaws.com` |
| `amazon-ap-southeast-4` | Asia Pacific (Melbourne) | `https://bedrock-runtime.ap-southeast-4.amazonaws.com` |
| `amazon-ap-southeast-5` | Asia Pacific (Malaysia) | `https://bedrock-runtime.ap-southeast-5.amazonaws.com` |
| `amazon-ap-southeast-6` | Asia Pacific (New Zealand) | `https://bedrock-runtime.ap-southeast-6.amazonaws.com` |
| `amazon-ap-southeast-7` | Asia Pacific (Thailand) | `https://bedrock-runtime.ap-southeast-7.amazonaws.com` |
| `amazon-il-central-1` | Israel (Tel Aviv) | `https://bedrock-runtime.il-central-1.amazonaws.com` |
| `amazon-me-central-1` | Middle East (UAE) | `https://bedrock-runtime.me-central-1.amazonaws.com` |
| `amazon-me-south-1` | Middle East (Bahrain) | `https://bedrock-runtime.me-south-1.amazonaws.com` |
| `amazon-af-south-1` | Africa (Cape Town) | `https://bedrock-runtime.af-south-1.amazonaws.com` |
| `amazon-sa-east-1` | South America (São Paulo) | `https://bedrock-runtime.sa-east-1.amazonaws.com` |
| `amazon-mx-central-1` | Mexico (Central) | `https://bedrock-runtime.mx-central-1.amazonaws.com` |

> 缺失的 3 个特殊合规区域（GovCloud + EU Sovereign Cloud）不在代码自动注册范围内，如有需要可手动通过 `RegisterProvider()` 添加：
> - `us-gov-east-1` / `us-gov-west-1` — AWS GovCloud，需 GovCloud 账号
> - `eusc-de-east-1` — EU Sovereign Cloud Germany，需 Sovereign Cloud 账号

**Runtime vs Control Plane 分离确认：**
- **Runtime** (`bedrock-runtime.{region}.amazonaws.com`) = Proxy 转发 user API 调用时用的推理 endpoint
- **Control Plane** (`bedrock.{region}.amazonaws.com`) = 管理员后台调用（ListFoundationModels 等），不暴露给用户
- 当前 Portal 代码中仅有 Runtime endpoint 配置；Control Plane endpoint 留给未来动态同步时使用。

### 后续实施顺序

| 优先级 | 改动 | 说明 |
|--------|------|------|
| **P0 ✅ 已完成** | `generateBedrockAPIModelName()` + `syncBedrockModels()` 自动剥离 | `internal/routes/models.go` + `admin.go` |
| **P0 ✅ 已完成** | Proxy 代码自动注册 33 个 `amazon-{region}` provider | `pkg/relay/providers.go` + `interface.go` |
| P1 | upstream key 表单增加 AWS Access Key + Secret Key + Region | `admin-keypool.html` + DB |
| P1 | `provider_default_urls` 拆分 runtime / control | 配置层 |
| P2 | 实现 `listBedrockModels()` 调用 AWS ListFoundationModels API | 动态替代硬编码 |
| P2 | 前端预览模态框支持多 region 选择 | `admin-models-aws.html` |

---

## 16. 33 区域 Provider 自动注册实现（2026-06-03 补充）

### 设计目标

- 用户无需手动配置 33 个 region 的 provider
- Proxy 启动时自动注册 `amazon-{region}` 到 `DefaultProviderConfigs`
- `GetAdaptor` 识别 `amazon-*` prefix，自动返回 `aws.NewAWSAdaptor`
- 保留原有 `amazon` provider 作为 us-east-1 的通用别名（向后兼容）

### 代码实现

**`pkg/relay/providers.go`**

```go
// BedrockRegions — AWS Bedrock 商业区域列表（33 个）
var BedrockRegions = []string{
    "us-east-1", "us-east-2", "us-west-1", "us-west-2",
    "ca-central-1", "ca-west-1",
    "eu-central-1", "eu-central-2", "eu-north-1", "eu-south-1", "eu-south-2",
    "eu-west-1", "eu-west-2", "eu-west-3",
    "ap-east-2",
    "ap-northeast-1", "ap-northeast-2", "ap-northeast-3",
    "ap-south-1", "ap-south-2",
    "ap-southeast-1", "ap-southeast-2", "ap-southeast-3", "ap-southeast-4",
    "ap-southeast-5", "ap-southeast-6", "ap-southeast-7",
    "il-central-1",
    "me-central-1", "me-south-1",
    "af-south-1",
    "sa-east-1",
    "mx-central-1",
}

func init() {
    for _, region := range BedrockRegions {
        providerName := "amazon-" + region
        DefaultProviderConfigs[providerName] = ProviderConfig{
            Name:     providerName,
            BaseURL:  "https://bedrock-runtime." + region + ".amazonaws.com",
            AuthType: "aws",
        }
    }
}
```

**`pkg/relay/interface.go`**

```go
func GetAdaptor(provider string, cfg *base.AdaptorConfig) Adaptor {
    // ... existing cases ...
    case "amazon":
        return aws.NewAWSAdaptor(cfg)
    default:
        if strings.HasPrefix(provider, "amazon-") {
            return aws.NewAWSAdaptor(cfg)
        }
        return NewOpenAICompatibleAdaptor(provider, cfg)
    }
}
```

### 效果

| 场景 | 行为 |
|------|------|
| `provider == "amazon"` | 使用 `bedrock-runtime.us-east-1.amazonaws.com`（向后兼容） |
| `provider == "amazon-us-west-2"` | 使用 `bedrock-runtime.us-west-2.amazonaws.com` |
| `provider == "amazon-eu-central-1"` | 使用 `bedrock-runtime.eu-central-1.amazonaws.com` |
| 未知 provider（如 `accessapi`） | 返回 `NewOpenAICompatibleAdaptor` |

### Portal 侧无需改动

由于 Proxy 的 `DefaultProviderConfigs` 在启动时自动填充了 33 个 `amazon-{region}` 条目，Portal 侧：
- Admin 添加 upstream key 时，`provider` 字段可直接输入 `amazon-us-west-2` 等
- Proxy 启动后 `SupportedProviders()` 已包含全部 33 个 region
- 不需要在 Portal 数据库或代码中硬编码 region 列表

---

## 17. Bedrock Mantle Endpoint 支持（2026-06-03 补充）

### 背景

AWS Bedrock 存在 **两个独立的推理 endpoint 家族**：

| Endpoint 家族 | URL 格式 | API 类型 | 认证 | 典型模型 |
|--------------|----------|---------|------|---------|
| **Bedrock Runtime** | `bedrock-runtime.{region}.amazonaws.com` | Converse / InvokeModel / Chat Completions / Messages | SigV4 (AWS creds) | Claude、Nova、Titan |
| **Bedrock Mantle** | `bedrock-mantle.{region}.api.aws` | Responses / Chat Completions / Anthropic Messages | Bearer Token (Bedrock API Key) | GPT-5.5、GPT-5.4、GPT-OSS、Qwen、Kimi |

> ⚠️ **关键区别**：Mantle 是 AWS 的 OpenAI-compatible 推理引擎，使用 **Bearer Token** 认证（不是 SigV4），返回格式与 OpenAI API 兼容。

### Mantle 支持的 13 个区域（AWS 官方）

| Provider ID | Region | Endpoint |
|-------------|--------|----------|
| `amazon-mantle-us-east-1` | US East (N. Virginia) | `https://bedrock-mantle.us-east-1.api.aws/v1` |
| `amazon-mantle-us-east-2` | US East (Ohio) | `https://bedrock-mantle.us-east-2.api.aws/v1` |
| `amazon-mantle-us-west-2` | US West (Oregon) | `https://bedrock-mantle.us-west-2.api.aws/v1` |
| `amazon-mantle-ap-northeast-1` | Asia Pacific (Tokyo) | `https://bedrock-mantle.ap-northeast-1.api.aws/v1` |
| `amazon-mantle-ap-south-1` | Asia Pacific (Mumbai) | `https://bedrock-mantle.ap-south-1.api.aws/v1` |
| `amazon-mantle-ap-southeast-2` | Asia Pacific (Sydney) | `https://bedrock-mantle.ap-southeast-2.api.aws/v1` |
| `amazon-mantle-ap-southeast-3` | Asia Pacific (Jakarta) | `https://bedrock-mantle.ap-southeast-3.api.aws/v1` |
| `amazon-mantle-eu-central-1` | Europe (Frankfurt) | `https://bedrock-mantle.eu-central-1.api.aws/v1` |
| `amazon-mantle-eu-north-1` | Europe (Stockholm) | `https://bedrock-mantle.eu-north-1.api.aws/v1` |
| `amazon-mantle-eu-south-1` | Europe (Milan) | `https://bedrock-mantle.eu-south-1.api.aws/v1` |
| `amazon-mantle-eu-west-1` | Europe (Ireland) | `https://bedrock-mantle.eu-west-1.api.aws/v1` |
| `amazon-mantle-eu-west-2` | Europe (London) | `https://bedrock-mantle.eu-west-2.api.aws/v1` |
| `amazon-mantle-sa-east-1` | South America (São Paulo) | `https://bedrock-mantle.sa-east-1.api.aws/v1` |

### 代码实现

**`pkg/relay/providers.go`**

```go
var MantleRegions = []string{
    "us-east-1", "us-east-2", "us-west-2",
    "ap-northeast-1", "ap-south-1", "ap-southeast-2", "ap-southeast-3",
    "eu-central-1", "eu-north-1", "eu-south-1", "eu-west-1", "eu-west-2",
    "sa-east-1",
}

func init() {
    // ... BedrockRegions 注册 ...
    for _, region := range MantleRegions {
        providerName := "amazon-mantle-" + region
        DefaultProviderConfigs[providerName] = ProviderConfig{
            Name:     providerName,
            BaseURL:  "https://bedrock-mantle." + region + ".api.aws/v1",
            AuthType: "bearer",
        }
    }
}
```

**`pkg/relay/interface.go`**

```go
default:
    if strings.HasPrefix(provider, "amazon-") {
        return aws.NewAWSAdaptor(cfg)
    }
    if strings.HasPrefix(provider, "amazon-mantle-") {
        return NewOpenAICompatibleAdaptor(provider, cfg)
    }
    return NewOpenAICompatibleAdaptor(provider, cfg)
```

### Provider 对比总结

| Provider 前缀 | Endpoint 家族 | Adaptor | AuthType | 典型用途 |
|--------------|--------------|---------|----------|---------|
| `amazon` | Runtime | `AWSAdaptor` | `aws` | Claude、Nova（向后兼容） |
| `amazon-{region}` | Runtime | `AWSAdaptor` | `aws` | Claude、Nova 多 region |
| `amazon-mantle-{region}` | Mantle | `OpenAICompatibleAdaptor` | `bearer` | GPT-OSS、Qwen、Kimi 等 |

### GPT-5.5 / GPT-5.4 特殊说明

GPT-5.5 和 GPT-5.4（2026-06-01 GA）是 **Responses-API-only**，当前仅在部分 region 可用：

| 模型 | 可用 Region | 模型 ID |
|------|------------|---------|
| GPT-5.5 | us-east-2 only | `openai.gpt-5.5` |
| GPT-5.4 | us-east-2, us-west-2 | `openai.gpt-5.4` |

OpenAI SDK 调用 GPT-5.5 时使用的 base URL 为 `https://bedrock-mantle.us-east-2.api.aws/openai/v1`（带 `/openai/v1` 前缀），而一般 Mantle Chat Completions 使用 `https://bedrock-mantle.{region}.api.aws/v1`。如需调用 GPT-5.5 Responses API，可在 upstream key 的 `base_url` 字段手动覆盖为 `/openai/v1` 路径。

---

## 18. Responses API Passthrough 模式设计方案

### 18.1 背景与问题

当前 Proxy 的 `handleResponses()` 对所有 Responses 请求采用**统一转换模式**：

```
Client /responses → Proxy ResponsesToOpenAI() → /chat/completions → upstream
                                      ↑
                               强制转换，无法 bypass
```

这对 **Responses-only 模型**（如 GPT-5.5、GPT-5.4）是致命的：
- Mantle 收到 `/chat/completions` 请求 → 返回 400 "model does not support Chat Completions"
- 客户端（Codex、OpenAI SDK）无法使用这些模型

### 18.2 目标

为 Responses-only 模型新增 **Passthrough 模式**：

```
Client /responses ──→ Proxy ──→ upstream /v1/responses（原始转发，不转换）
              ↑                            ↓
              └──────────────────── 原始 Responses 响应直接返回
```

### 18.3 现有功能兼容性分析

| 功能 | 兼容状态 | 说明 |
|------|---------|------|
| **Rate Limit (RPM/RPH/RP5H/RPW/RPD/Concurrent)** | ✅ 无缝 | Middleware 层基于 `key_hash` 计数，与请求格式完全无关 |
| **Rate Limit (TPM/TPH/TPD)** | ⚠️ 需适配 | Post-request 通过 `c.Set("tokens_consumed")` 记录。Passthrough 需从 Responses usage 提取 token 数 |
| **Affinity Cache** | ⚠️ 有 bug + 需适配 | `handleChatCompletions` 设置了 `request_body`，但 `handleResponses` **未设置**，导致 fingerprint 为空、cache 失效。Passthrough 需修复 |
| **Billing / Cost** | ✅ 无缝 | `CalculateCost(model, usage)` 与格式无关，只需正确 token 数 |
| **Usage Stat / Audit** | ✅ 无缝 | `recordUsageAsync` 只需 `input_tokens` + `output_tokens` |
| **Token Counter 预估算** | ⚠️ 需适配 | 当前 `EstimateMessages(messages, model)` 基于 Chat Completions。Passthrough 需基于 Responses `input` 字段估算 |

### 18.4 核心问题：如何灵活判断模型能力（不 hardcode）

#### 方案对比

| 方案 | 描述 | 优点 | 缺点 |
|------|------|------|------|
| **A. Hardcoded** | 代码中硬编码 `gpt-5.5`, `gpt-5.4` | 最简单 | ❌ 不可维护，新模型需改代码 |
| **B. Provider 开关** | `amazon-mantle-*` 全部 passthrough | 简单 | ❌ GPT-OSS 也支持 Chat，不需要 passthrough |
| **C. Model metadata** | Portal `service_models` 新增 `capabilities` JSONB | 精确到模型，可扩展 | 需改 Portal schema + sync |
| **D. Auto-discovery** | Proxy 调用 upstream `/v1/models` | 全自动 | 依赖 upstream 可用，启动延迟 |
| **E. Dynamic fallback** | 先尝试 Chat，失败后自动 fallback | 零配置 | 第一次请求必失败 |

#### 推荐方案：C（Model metadata）+ E（Dynamic fallback）兜底

**主要路径 — Portal Model Capability Sync：**

1. **Portal Schema 新增 `capabilities` JSONB 字段**
   ```sql
   ALTER TABLE service_models ADD COLUMN capabilities JSONB DEFAULT '{}';
   ```

2. **Capability 数据结构**
   ```json
   {
     "chat_completions": true,
     "responses": true,
     "converse": false,
     "anthropic_messages": false
   }
   ```

3. **Portal Sync 时填充**
   - Sync Bedrock Runtime 模型 → `converse: true`
   - Sync Bedrock Mantle 模型 → 根据 AWS 文档设置
     - GPT-OSS → `chat_completions: true, responses: true`
     - GPT-5.5 → `chat_completions: false, responses: true`
     - Claude (Mantle) → `anthropic_messages: true`

4. **Proxy 同步 capabilities**
   - `pkg/keypool/manager.go` `updateModelNameMap()` 同步 `capabilities`
   - 新增 `GetModelCapabilities(provider, modelID)` 查询接口

5. **`handleResponses()` 决策逻辑**
   ```go
   caps := modelCapabilities.Get(provider, upstreamModelName)
   if caps != nil && caps.Responses && !caps.ChatCompletions {
       // Responses-only → Passthrough
       handleResponsesPassthrough(c, bodyBytes, provider, upstreamKey)
       return
   }
   // 否则 → 现有转换模式
   ```

**兜底路径 — Dynamic Fallback（capabilities 缺失时）：**

```go
func resolveResponseRoute(caps *ModelCapabilities, provider string) ResponseRoute {
    if caps != nil {
        if caps.Responses && !caps.ChatCompletions {
            return RoutePassthrough
        }
        return RouteConvert
    }
    // capabilities 缺失 → 先尝试转换，失败后再 fallback
    cached := routeCache.Get(provider + ":" + model)
    if cached != nil {
        return *cached
    }
    return RouteTryConvertFirst // 先尝试转换，失败后自动切换并缓存
}
```

- 第一次请求：尝试 Chat Completions 转换
- 如果 upstream 返回 400 "does not support Chat Completions" → 自动切换到 Passthrough
- 结果缓存到内存 `routeCache`（TTL 1 小时），后续请求直接走 Passthrough
- 缺点：第一次请求会失败，但仅影响新模型上线后的首次调用

### 18.5 Passthrough 模式详细实现

#### 数据流

```
Client POST /v1/responses
        │
        ▼
┌─────────────────────────────────────────────────────────────┐
│ 1. 读取 body，设置 c.Set("request_body", bodyBytes)          │
│    （修复 affinity cache bug）                               │
│ 2. 解析 ResponsesRequest（提取 model）                       │
│ 3. Model resolution → resolvedModel                          │
│ 4. Capability check → Passthrough 分支                      │
│ 5. Token 预估算（基于 input 字段内容）                       │
│ 6. Resolve provider + key → amazon-mantle-us-east-2        │
│ 7. 直接发送原始 body:                                        │
│    POST https://bedrock-mantle.us-east-2.api.aws/v1/       │
│        responses                                             │
│    跳过 adaptor.ToProvider() 转换                            │
│ 8. 读取 upstream 响应（原生 Responses 格式）                 │
│ 9. 直接返回给 client（跳过 OpenAIToResponses() 转换）        │
│ 10. 提取 usage → c.Set("tokens_consumed", input+output)     │
│ 11. Billing / Audit（复用现有逻辑）                          │
└─────────────────────────────────────────────────────────────┘
```

#### 关键代码改动点

**改动 1：新增 `handleResponsesPassthrough()`**

```go
func handleResponsesPassthrough(c *gin.Context, bodyBytes []byte,
    provider string, upstreamKey *keypool.KeyInternal, model string) {

    // 构建 HTTP 请求（直接转发原始 body）
    upstreamURL := resolveBaseURL(upstreamKey, provider) + "/responses"
    req, _ := http.NewRequest("POST", upstreamURL, bytes.NewReader(bodyBytes))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+upstreamKey.ActualKey)

    // 发送请求
    client := &http.Client{Timeout: 120 * time.Second}
    resp, err := client.Do(req)
    // ... error handling ...

    // 读取响应 body
    respBody, _ := io.ReadAll(resp.Body)

    // 提取 usage（从 Responses 格式）
    var usage dto.Usage
    if extracted := extractResponsesUsage(respBody); extracted != nil {
        usage = *extracted
        c.Set("tokens_consumed", int64(usage.PromptTokens+usage.CompletionTokens))
    }

    // 直接返回（不转换）
    c.Data(resp.StatusCode, "application/json", respBody)

    // Billing / Audit
    recordUsageAsyncFromContext(c, keyHash, model, provider, &usage, ...)
}
```

**改动 2：修复 `handleResponses` 的 affinity cache**

```go
func handleResponses(c *gin.Context) {
    // ... 读取 bodyBytes ...
    c.Set("request_body", bodyBytes)  // ← 新增，修复 cache
    // ...
}
```

**改动 3：Token 预估算（Responses 格式）**

```go
func estimateResponsesTokens(req *convert.ResponsesRequest, model string) int {
    // 基于 input 字段估算
    switch v := req.Input.(type) {
    case string:
        return deps.TokenCounter.EstimateText(v, model)
    case []interface{}:
        total := 0
        for _, item := range v {
            // 提取文本内容估算
            total += estimateInputItemTokens(item, model)
        }
        return total
    }
    return 0
}
```

**改动 4：Portal Schema + Sync**

```sql
-- Migration
ALTER TABLE service_models ADD COLUMN capabilities JSONB DEFAULT '{}';

-- Sync Bedrock 模型时填充
UPDATE service_models SET capabilities = '{"chat_completions": false, "responses": true}'
WHERE provider = 'amazon' AND product_id = 'openai.gpt-5.5';
```

**改动 5：Proxy Model Capability Cache**

```go
// pkg/keypool/manager.go
type ModelCapability struct {
    ChatCompletions   bool `json:"chat_completions"`
    Responses         bool `json:"responses"`
    Converse          bool `json:"converse"`
    AnthropicMessages bool `json:"anthropic_messages"`
}

func (m *Manager) GetModelCapability(provider, modelID string) *ModelCapability {
    return m.modelCapabilityMap[provider][modelID]
}
```

### 18.6 影响范围与风险

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| Affinity cache 失效（当前已存在） | 高 | 中 | 修复 `request_body` 设置 |
| 第一次 fallback 请求失败 | 中 | 低 | 仅影响新模型首次调用，缓存后不再失败 |
| Portal schema 变更兼容性 | 低 | 中 | `capabilities` 默认 `{}`，不影响旧数据 |
| Passthrough 模式下 adaptor 未参与 | 中 | 低 | 直接转发无需 adaptor，但日志中 provider 信息减少 |
| Responses usage 提取失败 | 低 | 低 | Fallback 到 0 token，billing 为 0（可后续补录） |

### 18.7 实施顺序

```
Phase 1（最小可行）：
  ├─ 修复 handleResponses request_body 设置
  ├─ 实现 handleResponsesPassthrough()
  ├─ Hardcoded model 列表作为临时判断（gpt-5.5, gpt-5.4）
  └─ 验证 GPT-5.5 端到端

Phase 2（Capability 系统）：
  ├─ Portal: service_models.capabilities 字段 + migration
  ├─ Portal: syncBedrockModels() 填充 capabilities
  ├─ Proxy: modelCapabilityMap 同步
  ├─ Proxy: capability-based 路由决策
  └─ 移除 hardcoded 列表

Phase 3（兜底优化）：
  ├─ Dynamic fallback 机制
  ├─ routeCache 内存缓存
  └─ 监控 dashboard：passthrough vs convert 比例
```

### 18.8 与 CC Switch 的关系

Proxy 现有的 Responses API 支持（`pkg/convert/responses.go`）是从 CC Switch 迁移的：
- ✅ DTO 定义完整（`ResponsesRequest`, `ResponsesResponse`）
- ✅ 转换逻辑完整（`ResponsesToOpenAI`, `OpenAIToResponses`）
- ✅ Streaming 转换完整（`OpenAIChunkToResponsesEvent`）
- ✅ 路由已注册（`POST /responses`）

---

## 19. 2026-06-03 实现更新

> **实际实现日期:** 2026-06-03  
> **Commits:** Portal `3498026`→`50f6dc4` / Proxy `de8c416`→`e7747e9`

本节记录设计方案在实际落地时的关键决策和实现细节。

### 19.1 路由通道从双轨扩展为三轨

原设计为双轨（standard + bedrock），实际实现扩展为 **三轨**：

```
resolveTrack() → routing_track:
  ┌──────────┬──────────────────────────────────────────────────┐
  │ standard │ 任意 provider → OpenAI 兼容 API                   │
  │ bedrock  │ amazon-{region} providers → SigV4 + Converse API │
  │ mantle   │ amazon-mantle-* providers → Bearer + /v1/...     │
  └──────────┴──────────────────────────────────────────────────┘
```

**原因：** AWS 有两套 API 体系：
- **Bedrock Runtime** — 传统 API，使用 SigV4 签名，URL 格式 `/model/{modelId}/converse`
- **Bedrock Mantle** — 新 API 层，使用 Bearer Token，OpenAI 兼容格式 `/v1/chat/completions`、`/v1/responses`

两者都叫 "AWS Bedrock" 但认证方式和 API 格式完全不同，必须分为两个独立通道。

### 19.2 三轨在 Proxy 中的实现

**`resolveTrack()`** — 从 gin context 读取 `routing_track`，返回 `"bedrock"` / `"mantle"` / `"standard"`。

**4 个 Handler 均支持三轨：**

| Handler | 文件位置 | 说明 |
|---------|---------|------|
| `handleChatCompletions` | `handlers.go` | Chat 请求 |
| `handleEmbeddings` | `handlers.go` | Embedding 请求 |
| `handleImageGenerations` | `handlers.go` | 图片生成请求 |
| `handleResponses` | `handlers.go` | Responses API 请求 |

每个 handler 的路由逻辑相同：

```go
if track == "bedrock" {
    provider, key, model, fallback, err = resolveBedrockProviderAndKey(...)
    // fallback → standard
} else if track == "mantle" {
    provider, key, model, fallback, err = resolveMantleProviderAndKey(...)
    // fallback → standard
} else {
    provider, key, model, fallback, err = resolveProviderAndKey(...)
}
```

**`resolveMantleProviderAndKey()`** — 类似 bedrock resolver 的结构：
1. 遍历 Mantle region 列表（13 个区域）
2. 收集所有 `amazon-mantle-*` provider 的可用 key
3. 按模型列表过滤 → 健康过滤（circuit breaker） → 亲和性 → LRU 选择
4. **不使用 model ID mapping**（Mantle 使用与 key models 列表一致的模型名）

### 19.3 Provider 自动注册

**`pkg/relay/providers.go`** — `init()` 自动注册：

```go
// 33 个 Bedrock Runtime 区域
for _, region := range BedrockRegions {
    "amazon-" + region → SigV4 auth, bedrock-runtime.{region}.amazonaws.com
}

// 13 个 Bedrock Mantle 区域  
for _, region := range MantleRegions {
    "amazon-mantle-" + region → Bearer auth, bedrock-mantle.{region}.api.aws/v1
}
```

**关键 Bug 修复：** `GetAdaptor()` 中 `amazon-` 前缀检查必须在 `amazon-mantle-` 之后，否则 Mantle provider 会被错误路由到 AWS SigV4 adaptor。

```go
// ✅ 正确顺序
if strings.HasPrefix(provider, "amazon-mantle-") {
    return NewOpenAICompatibleAdaptor(provider, cfg)  // Bearer token
}
if strings.HasPrefix(provider, "amazon-") {
    return aws.NewAWSAdaptor(cfg)  // SigV4
}
```

### 19.4 Portal 的模型管理重构

#### 页面分离

| 页面 | 路由 | 内容 |
|------|------|------|
| `admin/models` (通用) | `/admin/models` | 非 Amazon provider — openai, anthropic, google 等 |
| `admin/models-aws` (AWS) | `/admin/models-aws` | Amazon Runtime + Mantle — 统一管理 |

#### AWS 页面核心流程

```
1. 选择 Provider (下拉框动态加载有上行key的amazon* provider)
2. 点击 "获取模型列表"
   ├─ AK/SK key → POST /api/internal/validate-upstream-key (Proxy SigV4)
   └─ Bearer key → GET /v1/models (Portal 直连)
3. 预览模型列表 (checkbox + API 模型名称)
4. "确认选中模型" → 写入 service_models (status=pending)
5. Admin 逐条确认 → 自定义弹窗(输入价/缓存命中价/输出价/推理价) → status=active
```

#### 不再使用 Hardcoded 模型列表

原设计中的 `getDefaultBedrockModels()`（13 个 hardcoded 模型）已完全移除。所有模型均从上游 key 实时获取：
- AK/SK key → Proxy SigV4 → AWS ListFoundationModels → 125 个模型
- Bearer key → Portal 直连 → GET /v1/models → 38 个模型

### 19.5 Upstream Key 验证链路

#### Portal 独立验证（Bearer token）

```
Portal validateUpstreamKey()
  → Bearer token → GET {baseURL}/v1/models
  → 解析 response → 返回模型列表
```

支持: `openai`, `anthropic`, `google`, `amazon-mantle-*` 等所有 Bearer token provider

#### Proxy 代理验证（AK/SK SigV4）

```
Portal validateViaProxy()           Proxy handleValidateUpstreamKey()
  → POST /api/internal/                 → SigV4 签名
    validate-upstream-key                 → GET bedrock.{region}.amazonaws.com
  ← {status, models, error}                /foundation-models
                                          ← 解析 modelSummaries
                                        ← 返回 {status, models}
```

新增 Proxy endpoint: `POST /api/internal/validate-upstream-key`
- 认证: `X-Admin-Secret` header（与 `PROXY_SYNC_SECRET` 一致）
- 入参: `{"provider": "amazon", "key": "AKID:SECRET"}`
- 返回: `{"status": "valid", "models": ["anthropic.claude-sonnet-4-6", ...]}`

首次测试结果：从 AWS Bedrock us-east-1 获取到 125 个 foundation models。

#### Key 格式校验

| Provider 类型 | Key 格式 | UI Placeholder |
|--------------|---------|---------------|
| `amazon` / `amazon-{region}` | `ACCESS_KEY_ID:SECRET_ACCESS_KEY` | `AKIA...:secret...` |
| `amazon-mantle-*` | Bearer token | `sk-...` |
| 其他所有 | Bearer token / API key | `sk-...` |

前端 (`admin-keypool.html`) + 后端 (`admin.go` addKeyPoolKey) 双重校验。

### 19.6 静态分析工具

新增 `scripts/check_schema_vs_code.py` — 部署前检查 schema.go 列完整性：

```
扫描 internal/routes/*.go 中所有 SQL
提取 INSERT/UPDATE/SELECT 引用的 (表, 列)
对比 schema.go 的 CREATE TABLE + ALTER TABLE 定义
→ 报告代码引用了但 schema 缺失的列
```

首次运行发现 45 个缺失列（分布在 19 张表中），全部已修复。当前结果：794 columns, 0 missing。

### 19.7 三轨的 Portal 配置

`user_groups.routing_track` 和 `users.routing_track` 现在支持三个值：

| 值 | 说明 |
|----|------|
| `standard` | 默认，走标准 provider（不包括 Amazon） |
| `bedrock` | 走 AWS Bedrock Runtime（SigV4，Converse API） |
| `mantle` | 走 AWS Bedrock Mantle（Bearer Token，OpenAI 兼容 API） |

UI 入口：`admin/groups` → 创建/编辑组 → 路由通道下拉框。

### 19.8 模型同步到 Key

每次 confirm / pause / delete / resume 操作后，系统自动调用 `syncServiceModelsToKeys()` 将 active 模型列表同步到上游 key 的 `models` 字段，Proxy 端可实时读取该 key 支持的模型范围。

### 19.9 文件变更清单

**Proxy (`ai-proxy-go`):**

| 文件 | 改动 |
|------|------|
| `internal/server/handlers.go` | 三轨路由: mantle provider 列表、resolveMantleProviderAndKey()、4 个 handler 的 mantle 分支、resolveTrack() 三态 |
| `internal/server/key_validation_handler.go` | 新建: handleValidateUpstreamKey、validateAWSBedrockKey (SigV4)、validateMantleKey (Bearer)、listAWSBedrockModels |
| `pkg/relay/interface.go` | 修复 amazon-/amazon-mantle- 前缀顺序 |
| `pkg/relay/providers.go` | GetMantleProviderNames()、33+13 region 自动注册 |
| `pkg/relay/channel/aws/aws.go` | SignRequest() 导出 |
| `internal/server/server.go` | 注册 /api/internal/validate-upstream-key 路由 |

**Portal (`ai-portal-go`):**

| 文件 | 改动 |
|------|------|
| `internal/db/schema.go` | +45 缺失列 ALTER TABLE + CREATE TABLE、routing_track 列 |
| `internal/routes/admin.go` | syncFromUpstreamKey (统一)、validateViaProxy (委托 Proxy)、syncBedrockModels (实时 key 替代 hardcode)、syncMantleModels、AWS key 格式校验 |
| `internal/routes/models.go` | confirmModel 自动生成 api_model_name |
| `internal/web/data.go` | GetAdminModels 支持 providerFilter |
| `internal/web/pages.go` | AWS 页面 filter="amazon"，通用页面 filter="!amazon" |
| `web/templates/admin-keypool.html` | AK/SK vs Bearer key 格式提示 + 前端校验 |
| `web/templates/admin-models-aws.html` | 完全重写: Provider 选择+获取+预览+确认+自定义弹窗 |
| `web/templates/admin-models-general.html` | Provider 同步移至顶部、stats 减为 4、Amazon 过滤、OpenRouter 调试标注 |
| `web/templates/admin-groups.html` | routing_track 增加 "mantle" 选项 |
| `web/templates/admin-providers.html` | 移除表格中的类型列 |
| `web/templates/super/providers.html` | 移除表格中的类型列 |
| `scripts/check_schema_vs_code.py` | 新建: schema vs code 静态分析工具 |
| `docs/db-schema-consolidation-mvp-20260601.md` | 更新: 静态分析脚本文档 + 45 个缺失列清单 |

**新增的是 Passthrough 分支**，与现有转换模式**并行共存**，不是替代关系。对于支持 Chat Completions 的模型（GPT-OSS、Qwen、Kimi 等），继续使用现有转换模式；对于 Responses-only 模型（GPT-5.5、GPT-5.4），走 Passthrough 分支。
