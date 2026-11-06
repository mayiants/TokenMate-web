# AWS Bedrock 高级配置选项 — 路线图与分析

> **日期:** 2026-06-04  
> **参考:** OpenClaw Bedrock 配置文档  
> **范围:** ai-proxy-go 三轨路由 (standard / bedrock-runtime / bedrock-mantle)

---

## 1. 当前实现 vs 业界参考

对比 [OpenClaw Bedrock 配置](https://docs.openclaw.ai/zh-CN/providers/bedrock) 与我们的当前实现：

| 功能 | OpenClaw | 我们 (ai-proxy-go) | 差距 |
|------|----------|-------------------|------|
| **凭证方式** | AWS SDK chain (env/profile/IMDS/SSO) | 手动 `AKID:SECRET` | 我们更可控但需手动 |
| **模型发现** | `ListFoundationModels` + `ListInferenceProfiles` | 仅 `ListFoundationModels` | 缺 InferenceProfiles |
| **发现缓存** | 1 小时 TTL (可配置) | 无缓存，每次实时查询 | 浪费 API 调用 |
| **Provider 过滤** | `providerFilter` (如仅 Anthropic) | 无，全部 125 个模型 | 列表太长 |
| **Context Window** | 自动填充 `defaultContextWindow: 32000` | 未设置 | 用户看不到上下文长度 |
| **Max Tokens** | 自动填充 `defaultMaxTokens: 4096` | 未设置 | 用户看不到输出限制 |
| **Inference Profiles** | 自动识别，继承模型能力，跨 region | 未识别，作为普通模型 | 无法利用跨区域调度 |
| **Service Tier** | flex / priority / reserved | 不支持 | 无法做成本优化 |
| **Guardrails** | 内置内容过滤 | 不支持 | - |
| **多 Region Failover** | SDK 自动处理 | 手动实现 (`resolveBedrockProviderAndKey`) | 已具备基础 |

---

## 2. 凭证管理方案对比

### 方案 A: 手动 AK/SK（当前方案）

```
Portal UI → 输入 AKID:SECRET → 加密存储 → Proxy 使用时解密 → SigV4 签名
```

**优点:**
- Portal 集中管理，UI 添加/替换/禁用
- 适合多租户 —— 每个客户配独立 key + 权限范围
- 不依赖服务器环境变量或 ~/.aws 目录
- 适合 MVP 试点（快速给客户开通、撤销）

**缺点:**
- AK/SK 暴露风险（Portal 需强加密存储）
- 不支持 IAM Role / SSO / IMDS 等安全凭证方式
- 无法利用 SDK 的自动刷新机制（STS temporary credentials）

### 方案 B: AWS SDK Default Chain（OpenClaw 方案）

```
环境变量 → ~/.aws/credentials → IAM Role (EC2) → SSO → ...
```

**优点:**
- 安全性更好（支持临时凭证、实例角色）
- 自动处理 credential refresh
- 与 AWS 生态无缝集成

**缺点:**
- Portal 无法直接管理（凭证不在 Portal 里）
- 多租户困难（所有客户共享同一套凭证）
- EC2 环境需要显式开启 discovery

### 推荐: 方案 A + B 兼容

保持手动 AK/SK 作为主要方式，同时支持 SDK Chain 作为 fallback：

```
resolveAWSCredentials():
  1. 如果 upstream key 有 AK/SK → 使用（当前方案）
  2. 否则 → 检查环境变量 AWS_ACCESS_KEY_ID / AWS_BEARER_TOKEN_BEDROCK
  3. 否则 → 使用 SDK default chain (~/.aws, IMDS, SSO)
```

这样：
- MVP 试点: Portal 管理 AK/SK（方案 A）
- 自部署: 环境变量或 IAM Role（方案 B），无需在 Portal 存凭证

---

## 3. 模型发现增强

### 3.1 当前缺陷

```
当前: GET bedrock.{region}.amazonaws.com/foundation-models → 125 models
      全部返回，无分类，无缓存
```

问题：
- 125 个模型太多，admin 无法快速找到需要的
- 每次点击都调 API（无缓存）
- 缺少模型能力信息（context window、max tokens、input modalities）

### 3.2 目标架构

```
┌──────────────────────────────────────────────────────┐
│               Model Discovery Flow                    │
├──────────────────────────────────────────────────────┤
│ 1. ListFoundationModels  ─┐                          │
│ 2. ListInferenceProfiles ─┤ → merge + dedup          │
│                            │ → filter by provider     │
│                            │ → filter by modality     │
│  ┌─────────────────────────┘                          │
│  ▼                                                    │
│  Model Registry (cached, 1h TTL)                      │
│  ├─ modelId: anthropic.claude-sonnet-4-6              │
│  │  ├─ contextWindow: 200000                          │
│  │  ├─ maxTokens: 8192                                │
│  │  ├─ inputModalities: [text, image]                 │
│  │  ├─ reasoning: true                                │
│  │  └─ inferenceProfile: us.anthropic.claude-...      │
│  ├─ ...                                               │
│                                                       │
│  Admin filters:                                       │
│  ├─ Provider: [anthropic] [amazon] [meta] [mistral]   │
│  ├─ Modality: [text] [image] [embedding]              │
│  └─ Capability: [streaming] [reasoning] [vision]      │
└──────────────────────────────────────────────────────┘
```

### 3.3 ListInferenceProfiles 价值

`bedrock:ListInferenceProfiles` 返回推理配置文件，这些是 AWS 的跨区域模型路由入口：

```
例: us.anthropic.claude-sonnet-4-6-v1:0
  → 自动路由到 us-east-1 / us-west-2 / eu-west-1 中有容量的区域
  → 继承 base model 的 contextWindow / maxTokens
  → 不需要我们的代码处理多区域 failover
```

目前我们有 125 个原始模型，如果正确识别 Inference Profiles：
- 区分 "base models" 和 "inference profiles"
- Inference profiles 自动获得多区域能力
- 减少冗余（同一个模型的不同区域变体可以合并）

### 3.4 Provider 过滤

AWS Bedrock 上的模型来自多个第三方 provider：

| Provider | 示例模型 | 数量 |
|----------|---------|------|
| Anthropic | Claude 系列 | ~15 |
| Amazon | Nova 系列 | ~10 |
| Meta | Llama 系列 | ~8 |
| Mistral | Mistral/Ministral 系列 | ~10 |
| Cohere | Command 系列 | ~5 |
| Stability AI | SDXL 系列 | ~10 |
| 其他 | DeepSeek, Qwen, NVIDIA 等 | ~60+ |

`providerFilter` 可以让 admin 按需拉取（如只需 Anthropic + Amazon Nova）。

---

## 4. 多区域 Failover

### 4.1 当前实现

`resolveBedrockProviderAndKey()` 已支持多区域优先顺序：

```go
for _, provider := range getBedrockProviders() {
    // amazon, amazon-us-west-2, amazon-eu-west-1 ...
    high, normal, economy := keyPoolManager.GetKeysForProvider(provider)
    allCandidates = append(allCandidates, ...)
}
```

但需要 **每个区域配一个 AK/SK key**，目前只有一个 `amazon`（us-east-1）。

### 4.2 利用 Inference Profiles 简化

如果使用 Inference Profiles，一个 key 就能跨区域：

```
当前: 需要一个 us-east-1 key + us-west-2 key + eu-west-1 key
改进: 一个 Inference Profile → AWS 自动路由到有容量的区域
```

但 Inference Profile 调用时仍需指定 region 参数（profile 的 source region），不能完全消除多区域 key 需求。

### 4.3 推荐的 Failover 策略

```
Tier 1: Inference Profiles (跨区域自动)
  → 一个 us.anthropic.claude-* profile → AWS 自动选择区域
  → 不需要我们处理 failover

Tier 2: Per-region keys + 我们的 failover
  → amazon-us-east-1, amazon-us-west-2, amazon-eu-west-1
  → resolveBedrockProviderAndKey 顺序尝试

Tier 3: Circuit breaker 自动降级
  → providerBreakers.RecordSuccess/Failure
  → 连续失败 → 临时排除 → 半开重试
```

---

## 5. Convert 层对 Token 统计和计费的影响分析

### 5.1 当前的 Token 统计路径

```
用户请求
  │
  ├─ Chat Completions (OpenAI format)
  │   │
  │   ├─ Standard/Mantle track:
  │   │   OpenAIAdaptor.ToProvider() → upstream API
  │   │   OpenAIAdaptor.ExtractUsage() → dto.Usage
  │   │   ✅ Token 来自 provider 响应，准确
  │   │
  │   └─ Bedrock Runtime track:
  │       AWSAdaptor.ToProvider() → Bedrock Converse
  │       AWSAdaptor.ExtractUsage() → dto.Usage
  │       ✅ Token 来自 Bedrock 响应，准确
  │
  └─ Responses API (OpenAI new format)
      │
      ├─ Passthrough mode (Mantle):
      │   直接转发 → provider /v1/responses
      │   ExtractResponsesUsage() → dto.Usage
      │   ✅ Token 来自 provider Responses 格式响应，准确
      │
      └─ Convert mode:
          ResponsesToOpenAI() → Chat Completions → OpenAIToResponses()
          Token 来自中间 OpenAI 格式的 usage 字段
          ⚠️ 取决于上游 adapter 是否忠实返回 usage
```

### 5.2 风险分析

| 路径 | Token 来源 | 准确度 | 风险 |
|------|-----------|--------|------|
| Chat Completions (standard/mantle) | `response.usage` | 高 | 低 |
| Chat Completions (bedrock) | `bedrockUsage.{inputTokens,outputTokens}` | 高 | 低 |
| Responses passthrough | `response.usage` (native format) | 高 | 低 |
| Responses convert | `OpenAIAdaptor.ExtractUsage()` 中间结果 | 中 | 中 |

**Convert 模式的风险不在 token 丢失，而在 token 语义差异：**
- OpenAI Chat Completions 的 `usage.prompt_tokens` 和 Bedrock Converse 的 `inputTokens` 可能计数规则不同
- 多模态（图片+文本）时 token 计数差异更明显
- Cache 命中 token 的计算方式不同（OpenAI: `prompt_tokens_details.cached_tokens`，Bedrock: 不支持 cache）

### 5.3 建议: 以 Adapter 侧数据为准

当前 billing 记录 (`usage_records`) 依赖 Proxy handler 层面提取的 `dto.Usage`。要获得更准确的计费数据：

```
方案: 双层数据采集

层1 (实时 - 当前方案):
  dto.Usage → usage_records → 快速账单估算
  用于: 实时配额扣减、速率限制

层2 (准实时 - 建议新增):
  定时从 AWS Cost Explorer / Bedrock 计费 API 拉取实际费用
  用于: 对账、精确计费、成本分析
```

这样即使 convert 层有微小偏差，最终计费以 AWS 账单为准，兼顾实时性和准确性。

### 5.4 具体改进步骤

1. **给 `dto.Usage` 增加 `provider_usage_raw` 字段** → 保留 provider 原始返回的 token 数据
2. **Adapter 侧增加 pricing 信息** → 每个模型的 AWS 官方定价缓存在 model registry
3. **定时对账任务** → 每小时比对 `usage_records` 估算 vs AWS 实际账单
4. **Cache token 独立计费** → 区分 prompt tokens / completion tokens / cache read tokens / cache write tokens

---

## 6. 计费与定价策略建议

### 6.1 AWS Bedrock 计费模型

AWS Bedrock 有多种计费维度：

| 维度 | 说明 | On-Demand 示例 (Claude Sonnet 4) |
|------|------|-------------------------------|
| **On-Demand** | 按 token 付费 | $3.00/1M input, $15.00/1M output |
| **Batch** | 异步批处理，50% 折扣 | $1.50/1M input, $7.50/1M output |
| **Provisioned Throughput** | 预留容量，按小时付费 | $xx/hour, 不限 token |
| **Cross-Region Inference** | 跨区域推理，标准价格 | 同 On-Demand |
| **Cache Read** | 上下文缓存命中 | $0.30/1M tokens |
| **Cache Write** | 上下文缓存写入 | $3.75/1M tokens |

### 6.2 客户定价策略

当前 Portal 的 `service_models` 表已有 `input_price`, `output_price`, `cache_read_price` 等字段。建议：

```
客户价格 = AWS 成本价 × 定价倍率 (pricing_ratio)
  ├─ On-Demand: 1.0x - 1.5x (标准利润)
  ├─ Cache Hit: 0.5x - 1.0x (吸引客户使用缓存)
  └─ Batch:    0.8x - 1.0x (低优先级，闲置资源)
```

通过 `user_groups.pricing_ratio` 已经支持了倍率配置，只需确保计费数据准确即可。

### 6.3 成本可见性

Admin 侧应该能看到：
- 每个 upstream key 的 AWS 实际费用（从 Cost Explorer 拉取，非实时）
- 每个用户/组的费用（实时估算，来自 usage_records）
- 利润 = 客户收费 - AWS 成本

### 6.4 Cost Explorer 对账 — 自动 vs 手动

#### 推荐: Portal 定时任务自动拉取

```
Portal Cron (每天 UTC 00:30)
  → AWS Cost Explorer API (GetCostAndUsage)
    → Filter: Service=Amazon Bedrock, GroupBy: MODEL
    → 返回: 每个模型的每日实际费用 + 用量
  → 写入 aws_cost_records 表
  → 与 usage_records 估算对比
  → 偏差超过 5% → 触发告警
```

**为什么不需要手动登录 AWS:**

| 方式 | 频率 | 操作 |
|------|------|------|
| 自动 (推荐) | 每天 00:30 UTC | Portal 后台任务调 Cost Explorer API，Admin 看 Dashboard |
| 手动触发 | 按需 | Admin 点 "立即同步" 按钮，Portal 立即拉取一次 |
| AWS 后台 | 偶尔 | 仅在排查异常时登录 AWS Console |

**Cost Explorer API 注意事项:**
- 数据延迟: ~24 小时（今天的费用明天才能查到）
- 费用: $0.01/请求（每天一次 = $0.30/月）
- 权限: 上游 key 的 IAM policy 需加 `ce:GetCostAndUsage`

**Dashboard 示例:**

```
┌───────────────────────────────────────────────────┐
│ AWS 费用对账                           2026-06-04 │
│                                                   │
│ 模型             估算     AWS实际   偏差   利润    │
│ claude-sonnet-4  $127.50  $131.20  +2.8%  $68.80 │
│ deepseek.v3.1    $45.30   $44.90   -0.9%  $22.10 │
│ nova-lite        $12.10   $12.10   0.0%    $5.90  │
│ ...                                               │
│                                                   │
│ 总计: 估算 $892 / 实际 $912 / 利润 $445            │
│ 最后同步: 2026-06-04 00:32 UTC                     │
│ [手动触发同步]                                      │
└───────────────────────────────────────────────────┘
```

### 6.5 AWS 凭证配置 — UI 设计

Admin 不需要理解 SDK Chain，只需选择凭证来源。Portal 根据来源类型切换输入框和验证方式。

**UI 流程:**

```
admin/keypool → "添加上行key"
  │
  ├─ Provider: [amazon-us-east-1 ▼]
  │
  ├─ 凭证类型: ○ AK/SK  ○ Bearer Token  ○ IAM Role  ○ 环境变量
  │
  └─ 输入区 (根据类型动态切换):
```

**完整页面布局（4 种凭证类型对比）:**

```
┌──────────────────────────────────────────────────────────────┐
│  添加上行key                                         [×]     │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  Provider *                                                   │
│  ┌──────────────────────────────────────┐                    │
│  │ amazon-us-east-1 (AK/SK Runtime)  ▼  │                    │
│  └──────────────────────────────────────┘                    │
│                                                              │
│  凭证类型 *                                                   │
│  ○ AK/SK    ○ Bearer Token    ○ IAM Role    ○ 环境变量       │
│                                                              │
│  ═══════════ 输入区根据凭证类型切换 ═══════════               │
│                                                              │
│  ┌─ AK/SK 选中时 ──────────────────────────────────────┐     │
│  │                                                      │     │
│  │  Access Key ID + Secret Access Key *                  │     │
│  │  ┌──────────────────────────────────────────────┐    │     │
│  │  │ AKIARLRC...:PXFMp/JxC3pgyPEsvIuI2dLYsMX...   │    │     │
│  │  └──────────────────────────────────────────────┘    │     │
│  │  Placeholder: AKIA...:secret...                       │     │
│  │  💡 AWS IAM 用户凭证，将使用 SigV4 签名调用 Bedrock   │     │
│  │     Runtime API。需要 bedrock:InvokeModel 权限。      │     │
│  └──────────────────────────────────────────────────────┘     │
│                                                              │
│  ┌─ Bearer Token 选中时 ───────────────────────────────┐     │
│  │                                                      │     │
│  │  Bearer Token *                                       │     │
│  │  ┌──────────────────────────────────────────────┐    │     │
│  │  │ ABSKQmVkcm9jayBNYW50bGUgVG9rZW4uLi4=         │    │     │
│  │  └──────────────────────────────────────────────┘    │     │
│  │  Placeholder: sk-... 或 AWS Bearer Token              │     │
│  │  💡 Bedrock Mantle 使用 OpenAI 兼容 API，Bearer 方式  │     │
│  │     认证。适用于 amazon-mantle-* provider。           │     │
│  └──────────────────────────────────────────────────────┘     │
│                                                              │
│  ┌─ IAM Role 选中时 ───────────────────────────────────┐     │
│  │                                                      │     │
│  │  Role ARN *                                           │     │
│  │  ┌──────────────────────────────────────────────┐    │     │
│  │  │ arn:aws:iam::123456789:role/BedrockAccess    │    │     │
│  │  └──────────────────────────────────────────────┘    │     │
│  │  Placeholder: arn:aws:iam::account:role/role-name     │     │
│  │  💡 Portal 使用 STS AssumeRole 获取临时凭证。         │     │
│  │     凭证不在 Portal 中存储，每次自动刷新。            │     │
│  │     ☑ 自动刷新 (每 1 小时)                             │     │
│  └──────────────────────────────────────────────────────┘     │
│                                                              │
│  ┌─ 环境变量 选中时 ───────────────────────────────────┐     │
│  │                                                      │     │
│  │  (无需输入凭证)                                       │     │
│  │                                                      │     │
│  │  💡 使用服务器上的 AWS SDK Default Chain:             │     │
│  │     ① 环境变量 AWS_ACCESS_KEY_ID / AWS_PROFILE       │     │
│  │     ② ~/.aws/credentials 文件                        │     │
│  │     ③ EC2 IAM Instance Role (IMDS)                   │     │
│  │     ④ SSO / Web Identity                             │     │
│  │                                                      │     │
│  │  Provider 过滤 (可选):                                 │     │
│  │  ☑ Anthropic  ☑ Amazon  ☑ Meta  ☑ Mistral  ☑ 其他   │     │
│  └──────────────────────────────────────────────────────┘     │
│                                                              │
│  ════════════════════════════════════════════════════════     │
│                                                              │
│  RPM 限制: [600]     TPM 限制: [100000]                      │
│  Tier: [default ▼]    Base URL: [自动填充 ▼]                 │
│                                                              │
│  ☑ 自动发现模型 (ListFoundationModels + ListInferenceProfiles) │
│  Provider 过滤: ☑ Anthropic  ☑ Amazon  ☐ Meta  ☐ Mistral     │
│  模型缓存: [3600] 秒    Context Window 默认: [32000]           │
│  Max Output Tokens 默认: [4096]                                │
│                                                              │
│                                          [取消]  [添加 Key]   │
└──────────────────────────────────────────────────────────────┘
```

| 凭证类型 | 输入内容 | 存储方式 | 适用于 |
|---------|---------|---------|--------|
| **AK/SK** | `AKIARLRC...:PXFMp/...` | 加密存 Portal | Runtime, 多租户 |
| **Bearer Token** | `ABSKQmVk...` | 加密存 Portal | Mantle |
| **IAM Role ARN** | `arn:aws:iam::123:role/BedrockAccess` | 存 ARN (不存凭证) | EC2 部署 |
| **环境变量** | `(无需输入)` | 不存 Portal | 自部署、K8s |

**后端逻辑:**

```go
func resolveAWSCreds(key *UpstreamKey) (accessKey, secretKey, sessionToken string, err error) {
    switch key.CredentialType {
    case "aksk":
        // 解密 key.key_encrypted → AKID:SECRET
        return parseAKSK(decrypt(key.KeyEncrypted))
    case "bearer":
        // Mantle — 不需要 SigV4，走 OpenAI 兼容
        return "", "", "", nil
    case "iam_role":
        // STS AssumeRole → 临时凭证 (自动刷新)
        return stsAssumeRole(key.RoleARN)
    case "env":
        // SDK default chain: env → ~/.aws → IMDS → SSO
        return resolveFromSDKChain()
    }
}
```

这样就实现了「既要简化管理（UI 选择），又要用上 AWS 高级能力（SDK chain 在后台自动处理）」。

---

## 7. 实施路线图

### Phase 1: 模型发现增强（本周）

```
☐ ListInferenceProfiles 集成
☐ 发现结果缓存（1h TTL, Redis）
☐ providerFilter 支持
☐ api_model_name 自动填充 contextWindow / maxTokens
☐ admin/models-aws 页面显示模型能力信息
```

### Phase 2: 凭证管理增强（下周）

```
☐ 支持 AWS_BEARER_TOKEN_BEDROCK 环境变量
☐ 支持 AWS SDK default chain fallback
☐ STS temporary credential 自动刷新
☐ 多 region key 配置 UI
```

### Phase 3: 计费增强（2 周内）

```
☐ dto.Usage 增加 provider_usage_raw 字段
☐ Adapter 侧 pricing 信息缓存
☐ usage_records 增加 AWS 计费维度 (on-demand/batch/cache)
☐ 定时对账任务（Portal ↔ AWS Cost Explorer）
```

### Phase 4: 高级特性（1 月内）

```
☐ Service Tier 支持 (flex/priority)
☐ Guardrails 内容过滤
☐ Provisioned Throughput 计费模式
☐ 多区域自动 failover 完善
```

---

## 8. 附录: AWS Bedrock API 权限参考

### 最小 IAM Policy

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "bedrock:ListFoundationModels",
        "bedrock:ListInferenceProfiles",
        "bedrock-runtime:Converse",
        "bedrock-runtime:ConverseStream",
        "bedrock-runtime:InvokeModel",
        "bedrock-runtime:InvokeModelWithResponseStream"
      ],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "ce:GetCostAndUsage"
      ],
      "Resource": "*"
    }
  ]
}
```

### 环境变量

| 变量 | 用途 |
|------|------|
| `AWS_ACCESS_KEY_ID` | AK/SK 方式的 Access Key |
| `AWS_SECRET_ACCESS_KEY` | AK/SK 方式的 Secret Key |
| `AWS_SESSION_TOKEN` | STS 临时凭证 |
| `AWS_BEARER_TOKEN_BEDROCK` | Bedrock Mantle Bearer Token |
| `AWS_REGION` | 默认区域 |
| `AWS_PROFILE` | 命名 profile (~/.aws/config) |
