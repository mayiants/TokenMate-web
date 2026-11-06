> **⚠️ PARTIALLY OUTDATED** — 2026-05-05 起草。2026-05-16/17 架构重构后，模型路由已从 "provider-centric" 迁移至 "api_model_name-centric"。v2 中 "Model 动态切换 → 暂缓（Phase 2）" 的决策已被推翻 — 该功能已在 `model-name-translation-202605171430.md` 中完整实现（Phase 1 & 2）。当前路由基于 `resolveProviderAndKey` + `ModelRoutingRule` / `GetRoutingOrder`。详见 `model-name-translation-202605171430.md` 和 `cross-provider-fallback-routing-202605161900.md`。
>
> **仍有效的部分**: Key Pool 结构、Rate Limit、Affinity Cache、Failure Handling、Circuit Breaker。
> **已过时的部分**: 模型路由决策表（v1→v2 变更摘要中关于 Model 动态切换的决策）。

# Proxy Key Pool 完整路由架构深度分析 v2

**Status:** ACTIVE ⚠️ 部分章节已过时
**Created:** 2026-05-05 21:44 UTC
**Previous:** keypool-architecture-analysis-202605031303.md (v1)
**Changes:** 基于用户 16 条回复，更新架构决策，明确 coding 计划

---

## 变更摘要 (v1 → v2)

基于用户对 keypool-alignment-questions-202605051731.md 中 16 条追问的回复，本文档更新以下架构决策：

| 维度 | v1 理解 | v2 确认决策 |
|---|---|---|
| Provider Base URL | Portal 表是唯一来源 | Portal 表是缺省来源，Proxy DefaultProviderConfigs 作为 fallback |
| assignment_mode | 默认应为 shared（与代码矛盾） | 默认 exclusive 正确，多 Proxy 防检测 |
| Model 动态切换 | 需要 productId → apiModelName 映射 | 暂缓（Phase 2）。需引入 user-group-model 三级架构 |
| Key models 过滤 | Proxy 未检查 models 字段 | Proxy 必须按 key 的 models 过滤 |
| Rate Limit | 只基于 user key | 双轨制：user key + upstream key 各自 token bucket |
| validation_status | 不过滤 | 使用 enable + valid |
| Provider ID | xai vs x-ai 不一致 | 统一为 xai，公司名作为 id |
| Sync 机制 | 60s polling | 保持 polling + 新增 Redis Pub/Sub |
| Affinity Cache | 读写 key 不一致，不影响路由 | 统一 key 格式，真正影响路由决策 |
| Provider 关闭 | 未明确 | 关闭不影响 Proxy 路由，已有订阅继续服务 |
| Azure | 已有 microsoft entry | 开发专门 Azure adaptor，base_url 由 key 传下来 |
| AWS | 已有 amazon entry | 开发专门 AWS adaptor，base_url 由 key 传下来 |
| Group 体系 | 未涉及 | 暂缓（Phase 2），见 §十二 |

---

## 一、设计原则确认（基于用户回复）

### 1.1 核心原则

1. **Portal 是配置中心，Proxy 是执行引擎**
   - Portal 管理 providers、models、upstream keys、user keys
   - Proxy 负责路由、限速、故障处理、计费上报
   - 两者通过 sync + Redis Pub/Sub 保持同步

2. **Provider 关闭不等于服务停止**
   - Admin 关闭 provider/model 的目的是：不让新用户看到/购买/续费
   - 已有订阅客户的服务必须继续
   - Proxy 不因 provider enabled=false 而停止该 provider 的 upstream key 服务

3. **Upstream key 的信息优先级高于 provider 配置**
   - 只要 key pool 还有一个 upstream key 存在，proxy 就能提供服务
   - provider 的状态管理只是从根源上减少用户选择该 provider，不是一刀切

4. **Group-Model 动态切换（Phase 2）**
   - 当前暂缓，见 §十二

### 1.2 命名规范

- **Provider ID = 公司全称小写**，不能用产品名代替
  - alibaba（不是 qwen）
  - anthropic（不是 claude）
  - xai（不是 x-ai）
  - meta（不是 llama）

---

## 二、请求全链路

```
User Request
    │
    ▼
[Auth Middleware] --> Portal API 验证 (5min缓存, 30min优雅降级)
    │                    返回: user_id, access_mode, key_hash, allowed_models
    ▼
[Rate Limit Middleware] --> Token Bucket (用户key层面)
    │
    ▼
[handlerChatCompletions]
    │
    ├── 1. Model --> Provider 解析 (GetProviderFromModel)
    ├── 2. 用户 Priority 判定 (getUserPriority)
    ├── 3. Key Pool 选择 (GetKeyByPriority)
    │      过滤: priority + models 支持 + validation_status
    │      检查: upstream key token bucket (RPM/TPM)
    │      Affinity Cache 命中 --> 优先历史 key
    │      LRU fallback
    │      返回: upstreamKey
    ├── 4. 创建 Adaptor (GetAdaptor)
    │      openai/claude/gemini/openai-compatible/azure/aws
    ├── 5. 请求转换 (ToProvider)
    ├── 6. 上游请求 (DoRequest)
    ├── 7. 响应转换 (FromProvider)
    ├── 8. 计费 (CalculateCost)
    ├── 9. 审计日志 (AuditLogger)
    └── 10. 返回用户
```

---

## 三、Service 等级（Priority + Tier）

### 3.1 Priority（用户维度）

| Priority | 来源 | 可用 Key |
|---|---|---|
| high | enterprise / subscription / single_model | high + normal |
| economy | economy | economy --> fallback normal |
| normal | 默认 | normal |

全局兜底：按 priority 过滤后无可用 key，则放开限制，任意可用 key 都可以选。

### 3.2 Tier（Upstream Key 维度）

| Tier | RPM | TPM |
|---|---|---|
| premium | 10000 | 1000000 |
| enterprise | 5000 | 500000 |
| standard | 1000 | 100000 |
| basic | 300 | 30000 |
| economy | 60 | 6000 |
| default | 1000 | 100000 |

注意：Tier 的 RPM/TPM 存储在 key 元数据中，但 Proxy 尚未 enforced（待实现 upstream key token bucket）。

---

## 四、Key 选择算法（LRU + Affinity + Models 过滤）

### 4.1 选择流程

1. Priority 过滤
2. 可用性检查 (Enabled, Purged, Cooldown, RateLimit)
3. Models 过滤 (key.Models 非空时只支持这些模型)
4. Upstream Key Token Bucket 检查
5. Affinity Cache 命中 --> 优先历史 key
6. LRU fallback (LastUsed 最旧的)
7. 返回 selected，更新 LastUsed

### 4.2 Models 过滤规则

- key.Models 非空 --> 该 key 只能服务这些模型
- key.Models 为空 --> 该 key 支持该 provider 所有模型
- 如果 admin 更新不及时导致无法使用其他模型，这是 admin 的失职，proxy 不补救

### 4.3 Key 可用性判定

- !key.Enabled --> 不可用
- key.PurgedFromPool --> 不可用
- key.CooldownUntil > now --> 不可用
- key.RateLimitUntil > now --> 不可用

---

## 五、Failure Handling

| 错误类型 | 处理 | ActiveKeys 变化 |
|---|---|---|
| Daily quota 耗尽 | CooldownUntil=明天零点, RateLimitUntil=明天零点, Status=cooldown | -- |
| 普通 rate limit (429) | CooldownUntil=now+60s, RateLimitUntil=now+60s, Status=cooldown | -- |
| 401/403 / 认证错误 | 立即 Enabled=false, Status=inactive | ActiveKeys-- |
| 10分钟内失败>=10次 | Enabled=false, Status=inactive | ActiveKeys-- |
| 累计失败>=3次 | CooldownUntil=now+60s, Status=cooldown | -- |

Cooldown 恢复：健康检查中，cooldown 过期后 Failures--，Status 恢复 active。

---

## 六、RPM / TPM 限速（双轨制）

### 6.1 User Key Rate Limit

- 算法：Token Bucket（本地内存）
- Key：ratelimit:user:<keyHash>
- 限额来源：Portal sync 的 rate_limit_rpm / rate_limit_tpm
- 超限时：立即返回 429
- Redis：异步 INCR，多实例参考
- 待改进：当前是 8 档硬编码，需改为从 user key 元数据读取

### 6.2 Upstream Key Rate Limit

- 算法：Token Bucket（本地内存）
- Key：upstream:<provider>:<keyID>
- 限额来源：Portal sync 的 rpm_limit / tpm_limit
- 目的：防止单 key 爆单，保证多 key 间负载均衡
- 检查时机：GetKeyByPriority 第 4 步

设计原则：静态限额数据由 Portal 管理，动态消耗由 Proxy 本地 token bucket 执行。

---

## 七、Affinity Cache（真正影响路由）

### 7.1 目的

相似请求路由到同一 upstream key，保持 prompt cache 热态，降低延迟和成本。

### 7.2 Cache Key 格式（统一）

priority:model:group:sha1(body)[:8]

GetChannelWithPriority 和 RecordChannelWithPriority 使用相同的 key 格式。

### 7.3 路由优先级

Priority 过滤 --> 可用性检查 --> Models 过滤 --> Upstream Token Bucket --> Affinity hit（如有）--> LRU fallback

- Affinity 不替代 priority 和可用性
- 只在已过滤的候选集内优先选历史 key
- 如果 affinity 的 key 不在候选集或不可用，fallback 到 LRU

### 7.4 权重

路由考虑 60%，Affinite 考虑 30%。

---

## 八、Provider 配置管理

### 8.1 数据来源优先级

1. Upstream key 级别的 base_url（key.BaseURL）
2. Portal sync 的 providers[].baseUrl
3. Proxy DefaultProviderConfigs（fallback）

### 8.2 Provider 关闭规则（关键业务规则）

- Portal providers.enabled=false 不影响 Proxy 路由
- Proxy 消费 providers[] 时，只用它来获取 base_url，不因 provider 关闭而停用 upstream key
- 只要 key pool 还有一个 upstream key，proxy 就继续服务

### 8.3 Proxy Provider 列表（20 家）

| ID | 名称 | Base URL (fallback) | Auth Type | 需专门 Adaptor |
|---|---|---|---|---|
| openai | OpenAI | https://api.openai.com/v1 | bearer | No |
| anthropic | Anthropic | https://api.anthropic.com/v1 | x-api-key | No |
| google | Google | https://generativelanguage.googleapis.com/v1beta | query | No |
| meta | Meta | https://api.llama.com/compat/v1 | bearer | No |
| mistral | Mistral | https://api.mistral.ai/v1 | bearer | No |
| cohere | Cohere | https://api.cohere.ai/compatibility/v1 | bearer | No |
| amazon | Amazon/AWS | https://bedrock-runtime.us-east-1.amazonaws.com | aws | **Yes** |
| microsoft | Microsoft/Azure | https://{resource}.openai.azure.com/openai/deployments | api-key | **Yes** |
| xai | xAI | https://api.x.ai/v1 | bearer | No |
| perplexity | Perplexity | https://api.perplexity.ai | bearer | No |
| deepseek | DeepSeek | https://api.deepseek.com/v1 | bearer | No |
| alibaba | Alibaba | https://dashscope.aliyuncs.com/compatible-mode/v1 | bearer | No |
| moonshot | Moonshot | https://api.moonshot.cn/v1 | bearer | No |
| zhipu | Zhipu | https://open.bigmodel.cn/api/paas/v4 | bearer | No |
| bytedance | ByteDance | https://ark.cn-beijing.volces.com/api/v3 | bearer | No |
| tencent | Tencent | https://api.hunyuan.cloud.tencent.com/v1 | bearer | No |
| minimax | MiniMax | https://api.minimax.chat/v1 | bearer | No |
| baidu | Baidu | https://qianfan.baidubce.com/v2 | bearer | No |
| groq | Groq | https://api.groq.com/openai/v1 | bearer | No |
| xiaomi | Xiaomi | https://api.xiaomimimo.com/v1 | bearer | No |

**AWS 和 Azure 说明**：
- AWS：base_url 随区域变化（us-east-1, us-west-2, eu-west-1 等），需专门 adaptor 处理 AWS SigV4 认证
- Azure：base_url 包含 resource name 和 deployment-id，需专门 adaptor 处理路径替换和 api-key 认证
- 两者的实际 base_url 均由 upstream key 的 `base_url` 字段传下来，覆盖 DefaultProviderConfigs 中的默认值

---

## 九、Portal 与 Proxy 数据流对齐矩阵

| 数据 | Portal 输出 | Proxy 消费 | 状态 |
|---|---|---|---|
| keys[].key | sync 发送 | SyncKey.Key 接收 | 对齐 |
| keys[].provider | sync 发送 | 接收 | 对齐 |
| keys[].priority | sync 发送 | GetKeyByPriority | 对齐 |
| keys[].baseUrl | sync 发送 | upstreamKey.BaseURL | 对齐 |
| keys[].models | sync 发送 | 待实现过滤 | 进行中 |
| keys[].assignmentMode | sync 发送 | 待实现（多Proxy） | 进行中 |
| keys[].tier | sync 发送 | 待用于 upstream token bucket | 进行中 |
| keys[].enabled | 只发 enabled=true | 接收 | 对齐 |
| providers[] | sync 发送 | 待消费（覆盖fallback） | 进行中 |
| models[].productId | models/sync | 待实现映射 | 进行中 |
| models[].apiModelName | models/sync | 待实现映射 | 进行中 |
| rpm_limit / tpm_limit | 表中有 | 待实现 upstream token bucket | 进行中 |
| validation_status | 表中有 | 待实现过滤（enable+valid） | 进行中 |
| rate_limit_rpm / rate_limit_tpm | user key 中有 | 待替换8档硬编码 | 进行中 |

---

## 十、已修复 Bug 清单

### Portal 侧

| Bug | 修复内容 | 文件 |
|---|---|---|
| trimSpace 是 no-op | strings.TrimSpace(s) | admin.go |
| Legacy handler 错误列名+明文 | key_encrypted + crypto.Encrypt | admin_handlers.go |
| providerDefaultURLs xai key 错误 | x-ai --> xai | admin.go |
| openrouter_models 表缺失 | 新建迁移 | migrations/ |

### Proxy 侧

| Bug | 修复内容 | 文件 |
|---|---|---|
| LastUsed 在 RLock() 下写入 | RLock() --> Lock() | manager.go |
| Failure double count | UpdateKeyUsage 移除 Failures++ | manager.go |
| persistToRedis data race | lock 内 marshal snapshot | manager.go |
| pool.ActiveKeys 不同步 | inactive 时 ActiveKeys-- | manager.go |
| 缺 provider | 添加 groq + xiaomi | providers.go |

---

## 十一、待实现功能清单（Coding 计划）

### Phase 1（当前 sprint）

| # | 任务 | 预估 | 文件 |
|---|---|---|---|
| 1 | Portal sync 过滤 validation_status | 15min | sync.go |
| 2 | Proxy 消费 providers[] | 2h | manager.go |
| 3 | Key models 过滤 | 1h | manager.go |
| 4 | Upstream Key Token Bucket | 4h | manager.go + 新文件 |
| 5 | User Key Rate Limit 精细化 | 2h | ratelimit.go |
| 6 | Affinity Cache 重构 | 3h | affinity.go + manager.go |
| 7 | Azure Adaptor | 3h | 新文件 |
| 8 | AWS Adaptor | 3h | 新文件 |
| 9 | Redis Pub/Sub 同步 | 4h | Portal + Proxy |
| **总计** | | **~22.5h** | |

### Phase 2（后续 sprint）

| # | 任务 | 说明 |
|---|---|---|
| 1 | Group-Model 动态切换体系 | 暂缓。需新建 user_groups/group_models 表，api_keys 新增字段 |
| 2 | Sync 状态合并（非重建） | 暂缓。需更仔细的 runtime 状态管理设计 |
| 3 | productId --> apiModelName 映射 | 暂缓。等 Group 体系确定后再实现 |

---

## 十二、用户确认的设计决策汇总

| # | 决策 |
|---|---|
| 1 | DefaultProviderConfigs 作为 fallback，Portal providers 表是主来源 |
| 2 | assignment_mode 默认 exclusive 正确 |
| 3 | Group-Model 动态切换 暂缓 Phase 2 |
| 4 | Key 的 models 不为空时只支持这些模型 |
| 5 | Proxy 双轨制限速：user key + upstream key |
| 6 | validation_status 用 enable + valid |
| 7 | xai 统一为 xai，公司名作为 id |
| 8 | 需要 HTTP polling + Redis Pub/Sub |
| 9 | Affinity 统一 key 格式，真正影响路由 |
| 10 | Provider 关闭不影响 Proxy 路由 |
| 11 | Azure 专门 adaptor，base_url 由 key 传下来 |
| 12 | AWS 专门 adaptor，base_url 由 key 传下来 |
| 13 | 路由 60%，Affinity 30% |
| 14 | 20 家 provider（18 + groq + xiaomi） |

---

*文档结束。本文档是 coding 前的最终架构基准。所有 Phase 1 的实现都应遵循本文档中的设计决策。*
