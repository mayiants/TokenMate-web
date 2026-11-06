# Proxy 路由与 Prompt Cache 机制说明

**日期：** 2026-07-14
**状态：** 现行实现（含 2026-07-14 优化批次，均已上线）
**范围：** ai-proxy-go 的请求路由、亲和（affinity）、Prompt Cache 及与 Portal 计费的衔接

---

## 一、两个独立机制，先分清楚

proxy 里"cache"相关的优化其实是**两个独立机制**，常被混为一谈：

| 机制 | 本质 | 供应商相关性 | 降本作用 |
|------|------|-------------|---------|
| **亲和路由（Affinity）** | 路由层：把同一会话稳定路由到同一上行 key | 供应商无关（所有轨通用） | 间接——只在供应商 cache 为 key 级时才提升命中；主要价值是确定性路由 |
| **Prompt Cache（提示词缓存）** | 调用上游的 token 缓存能力 | 供应商强相关（分显式/自动两派） | 直接——缓存命中部分按 cache_hit 价计费，约省 90% |

关键认知：**proxy 自身从不缓存任何 token**。它只做路由与 cache 标记的注入/解析；真正的缓存发生在上游（AWS Bedrock / OpenAI / DeepSeek / Anthropic）。这解释了优化前"本地 proxy 零命中"的现象——那时根本没接上游的 prompt cache。

---

## 二、请求处理链路

```
客户端 (OpenAI / Anthropic / Responses 格式)
   │
   ▼
auth 中间件 → Portal validateKey → 写入 gin context：
   routing_track(bedrock/mantle/standard)、key 信息、access_mode
   │
   ▼
rate limit 中间件（RPM / TPM，Redis 分布式）
   │
   ▼
handler（chat / responses / messages）
   1. 读 body → c.Set("request_body") → 前置 plugin 链清洗
   2. GetPreferredKeyID（亲和读）→ 把解析出的 cache key 存进 context
   3. resolveTrack → bedrock / mantle / standard
   4. resolve{Bedrock,Mantle,Provider}AndKey：
      健康过滤 → 亲和软优先 → LRU 兜底
   5. logicalModel := req.Model 快照；req.Model = 上游 product_id
   6. 转成供应商格式（aws.ToProvider 在此注入 cachePoint）
   7. 发上游
   8. RecordPreferredKeyID（亲和写，复用步骤 2 存的 key）
   9. emitUsage → Portal 计费
```

**对外端点**（`internal/server/server.go`）：

- `POST /v1/chat/completions`（OpenAI）、`POST /v1/completions`、`POST /v1/embeddings`
- `POST /v1/messages`（Anthropic 原生格式，经 cc-switch 式转换进统一链路）
- `POST /v1/responses`（OpenAI Responses）
- `GET /v1/models/list`、`GET /v1/models/stats`、`GET /admin/cache/affinity`（观测）

因此无论客户端是 OpenAI 还是 Anthropic 格式，proxy 都能接入（Anthropic 格式在入口处归一化），不强制客户端改格式。

---

## 三、路由机制

### 3.1 三轨路由

track 由 Portal `validateKey` 决定（auth 中间件写入 context，`handlers_common.go:405` `resolveTrack`），不是请求属性：

- **bedrock**（VIP 轨）：`resolveBedrockProviderAndKey`，Bedrock 多 region 按优先级failover，过滤链简化为 HEALTH → LRU；失败可回落 standard。
- **mantle**：`resolveMantleProviderAndKey`。
- **standard**：`resolveProviderAndKey`，通用路径。

### 3.2 亲和路由（Affinity）

**cache key 格式**（`pkg/cache/affinity.go` `buildAffinityKey`）：

```
track : priority : model : group : fingerprint
```

- `fingerprint` = **会话种子哈希**（`conversationPrefixFingerprint`）：system 提示词 + 第一条非 system 消息，OpenAI/Anthropic 两种请求形状归一化为等价结果，单段截断 2048 字符。编码 agent 每轮重发不断增长的历史，但会话种子不变 → 同一会话指纹恒定。
- 存储：进程内 `sync.Map`（localCache，优先）+ Redis（前缀 `ai-proxy:affinity:v2`，默认 TTL 1h，用于跨重启/多实例共享）。
- **软优先**（`handlers_common.go:482`）：preferred key 若在当前健康候选中则直接采用，否则回落 LRU——粘但不强绑，key 不健康时自动让位。

**Get/Record 一致性修复（0cd2c61）**——本批最重要的修复：

handler 在 `GetPreferredKeyID` 之后才把 `req.Model` 换成上游 product_id、才 set `routing_track`；而 `RecordPreferredKeyID` 原先用**改后**的 model/track 重建 key。Get（逻辑模型 + track=standard）与 Record（上游模型 + track=bedrock）算出的 key 不一致，Record 写的 key Get 永远查不到 → 每轮 miss → 退回轮询（实测同一会话 A、B、A 轮换）。

修复：Get 把解析好的 key 存进 gin context（复用常量 `GinKeyAffinityCacheKey`），Record 直接复用同一 key，model/track 后续如何变更都不影响一致性；仅在 Get 未运行时回退重建。附回归测试（Get 后变更 model+track，下一轮 Get 仍命中）。

**线上验证**：同一会话 4 轮请求全部落同一上行 key（修复前为轮询）；Redis 亲和 key 以逻辑模型名写入。

> **为什么 key 里用逻辑模型名而非 product_id**：① Get 调用点还没有 product_id（要后续才解析）；② 一个逻辑模型会路由到多 region 的 product_id（实测 opus-4-6 → anthropic./us./global. 三个变体），用 product_id 会在换 region 时断掉粘性；③ model 段的职责是"稳定性"而非唯一性，唯一性由 fingerprint 负责。详见下方第五节。

---

## 四、Prompt Cache 机制

### 4.1 供应商分两派

| 派别 | 供应商 | proxy 要做的 |
|------|--------|-------------|
| **显式缓存**（需注入标记） | AWS Bedrock（cachePoint）、Anthropic 原生（cache_control）、Gemini 显式 | 注入 cache 标记 + 解析 cache usage |
| **自动缓存**（无需注入） | OpenAI、DeepSeek | 仅解析 cache usage（供应商按前缀自动缓存） |

### 4.2 Bedrock 显式缓存（本批新增，140bc81）

`pkg/relay/channel/aws/aws.go`：

- **注入**：`injectCachePoints()` 在 system 提示词与会话末尾注入 cachePoint，阈值用 ≥4096 字符启发式（约 1024 token，Bedrock 最小可缓存前缀；小请求 Bedrock 静默不缓存、不报错）。编码 agent 每轮重发增长的历史，下一轮的前缀即成 cache read。
- **解析**：`bedrockUsage` 解析 `cacheReadInputTokens` / `cacheWriteInputTokens`。
- **计费口径**：`promptTokensForBilling() = inputTokens + cacheRead`（OpenAI 语义：prompt_tokens 含 cache，cached 部分在 `prompt_tokens_details.cached_tokens` 拆出）。FromProvider / ExtractUsage / emitUsage 三处统一映射 cache 字段。

### 4.3 usage 归一化（多供应商口径统一）

`dto/openai.go` 的 `Usage` 结构把各家 cache 口径归一：

- `prompt_cache_hit_tokens`（DeepSeek / OpenAI 顶层）
- `prompt_tokens_details.cached_tokens`（OpenAI 嵌套；`openai.go:131` 归一到 `PromptCacheHitTokens`）
- `claude_cache_creation_5_m_tokens` / `..._1h_...`（Anthropic cache 写入）

### 4.4 计费链路

```
proxy emitUsage（带 cached tokens）
   → Portal pricing.go：effectiveInput = Input - Cached，cached 部分按 cache_hit 价
   → usage_records.cached_input_tokens + total_cost
```

**线上验证（opus-4-6，cache_hit 价 1.5 USD/M）**：一条 input=7011、cached=7003 的记录 total_cost=10924 μ$，拆账 = 7003×1.5/M（cache_hit）+ 8×15/M（effective input）+ 4×75/M（output）= 10504+120+300 = 10924 ✓。即缓存 token 按 1.5 而非 15 计费，客户省约 90%。

> 小瑕疵：`usage_records` 的分项成本列（input_cost/cache_cost/…）未持久化，只落 total_cost——观测缺口，非计费错误，已列入 P2。

---

## 五、账号级 vs key 级 cache —— 亲和到底帮不帮命中

这是判断"同 key 路由能否提升 cache hit"的关键：

- **账号级 cache（Bedrock、OpenAI、DeepSeek 均为账号级自动缓存）**：cache 按 (账号, 模型, 前缀) 共享，账号下任何 key 都能命中。此时**同 key 路由不提升命中**，命中取决于**前缀是否稳定且够长**（客户端行为）。已实测 Bedrock 跨 key 也能读到 cache。
- **key 级 cache（若某供应商是）**：同 key 路由才是提升命中的关键——这正是亲和的价值。

结论：亲和是供应商无关的通用杠杆，提供**确定性路由**（解决"同一 key 打到不同上行 key"），但只在 key 级 cache 时才直接提升命中。对账号级自动缓存的供应商，提升命中靠的是前缀稳定 + cache usage 解析正确。

---

## 六、各供应商 cache 现状与支持矩阵

| 供应商 | channel | cache 类型 | 注入 | usage 解析 | 现状 |
|--------|---------|-----------|------|-----------|------|
| AWS Bedrock | aws | 账号级·显式 | ✅ cachePoint 自动注入 | ✅ cacheRead/Write | 已上线，实测命中 |
| OpenAI | openai | 账号级·自动 | 不需要 | ✅ cached_tokens | 解析链路在，待真实命中验证 |
| DeepSeek | openai（兼容） | 账号级·自动 | 不需要 | ✅ prompt_cache_hit_tokens | ✅ 已实测命中+计费（见下） |
| Anthropic 原生 | claude | 账号级·显式 | ⚠️ 仅透传客户端 cache_control，未自动注入 | ✅ cache_creation_* | 当前无直连 Anthropic 上游，未启用 |
| Gemini | gemini | 显式/隐式 | 视配置 | 部分 | 待评估 |

> 直连 Anthropic（api.anthropic.com）目前**没有配置上游 key**，所有 Claude 模型经 Bedrock（aws channel）提供服务，缓存走 Bedrock cachePoint。如需 Anthropic 原生缓存，须先接入一个直连 Anthropic 上游 key；且 claude channel 需补"自动注入 cache_control"才能对齐 Bedrock 的"总是缓存"行为。

---

## 七、观测入口

Bedrock prompt cache 是**临时的**（默认 5 分钟滑动 TTL，命中刷新；部分模型可配 1h），AWS 无逐条缓存浏览器，只能经用量指标观测：

1. **Portal `usage_records`**：每条请求的 `cached_input_tokens`（我们计费口径）。
2. **Portal `upstream_key_usage`**：每上行 key/模型/天的 `cache_read_tokens` / `cache_creation_tokens`（上游上报口径）。
3. **API 响应**：`usage.prompt_tokens_details.cached_tokens`（客户端可见）。
4. **proxy 管理端点**：`GET /admin/cache/affinity`（亲和 + cache 命中率统计，`handleGetAffinityStats`）。
5. AWS CloudWatch Bedrock invocation 指标可按模型看 cache token 数。

---

## 八、实测验证记录（2026-07-14）

- **Bedrock opus-4-6**：大 system（~40k 字符）两轮，cache_create 7003 → cached 7003；`usage_records.cached_input_tokens=7003`，total_cost 10924 μ$ 与 cache_hit 价（1.5/M）拆账精确吻合。
- **DeepSeek v4-flash**：~9000 字符 system 前缀两轮，round1 cached=0（建缓存）→ round2 `prompt_cache_hit_tokens=1536`（=24×64，符合 64 token 粒度）；`usage_records.cached_input_tokens=1536`、`effective_input=74`，total_cost 748→126 μ$（cache_hit 价 0.05/M 为 input 0.5/M 的 1/10），**省约 83%**。证明自动缓存供应商无需注入，proxy 解析 + Portal 计费链路全通。
- **亲和粘性**：同一会话 4 轮请求全部落同一上行 key（0cd2c61 修复前为 A、B、A 轮询）。

> 注：测试 key 已清理，测试消耗已回滚，usage_records 保留作验证凭证。

---

## 九、相关提交（2026-07-14 批次，均已上线）

| Commit | 内容 |
|--------|------|
| ai-proxy-go `140bc81` | Bedrock prompt cache（cachePoint 注入 + usage 解析）+ 亲和指纹改会话种子哈希 |
| ai-proxy-go `0cd2c61` | 亲和 Get/Record key 一致性修复（解决轮询不粘连） |
| ai-proxy-go `852c834` / `a47e517` / `82ccaf1` | kimi code 400 三连修（空消息跳过 / 上游错误日志 / maxTokens 钳位重试） |
| ai-portal-go `0b59030` | 企业 key 计费三修（按 key hash 判类型 + 企业 key 只扣企业 credit + 预消费查企业 credit） |

---

## 十、路由治理三项改进（2026-07-19 批次，均已上线）

本批源于一次线上事故链复盘：WorkBuddy 客户端调用 `wgit-maas-latest` 时连续暴露三个路由层问题——回退越界（落到未激活通道）、跨 provider 横跳（缓存被打散）、判决顺序失效（LRU 实际独大）。

### 10.1 候选目录闸门（catalog gate，b343416）

**问题**：`GetAllKeysForModel` 以 key 自广告的 models 列表作为候选依据。zimaocloud 的 key 广告了 60+ 模型（含 deepseek-v4-flash/pro），而我们在 Zimao 控制台从未激活 deepseek、`service_models` 里也没有 zimaocloud×deepseek 条目、更没有对应定价规则。DeepSeek 官方直连失败后，回退按"广告"落到 Zimao——调用成功了，但计费只能命中全局通配兜底价（provider='*'，$1/M in + $3/M out），而非该通道的设定价格。反例同样存在：Anthropic 直连 key 从不返回模型列表，按广告逻辑永远进不了候选。

**修复**：候选资格的唯一依据改为 Portal 同步的 model name map（源自 `service_models` 可服务状态，见 10.3）。key 广告列表不再产生路由资格；未同步前的启动窗口保留旧广告逻辑兜底防拒流。`GetProvidersForModel` 同步加闸。上游模型名直接取目录映射的 product_id。

**效果**：未激活的 provider×model 永不路由（zimaocloud×deepseek 立即出局）；计费永远落在已配置规则上；管理面恢复单一事实源（admin/models）。代价是回退链变短——可用性换可控性，如需启用某通道，正确动作是"激活目录条目 + 配定价"，而非绕过闸门。

### 10.2 smartRoute 判决顺序修复（b343416）

**问题（两处）**：

1. **LRU 末排覆盖**：实现是 HEALTH → TIER → LATENCY → QUOTA → LRU 连续多次 `sort.SliceStable` 全排，最后一次 LRU 排序覆盖此前全部键序。实际效果 = 跨 provider 纯 LRU 轮转：2 把 deepseek key + 1 把 zimao key 时约每 3 次请求 zimao 被轮中一次，**不需要任何失败**（`is_fallback` 仅表示"选中非首选 provider"，不代表发生重试）。上游缓存按 provider 账户隔离，横跳直接打散 prompt cache——事故窗口内 zimao 调用 `cached_input_tokens` 全为 0，deepseek 直连则 60–97%；按 flash 定价（命中 $0.05/M vs 未命中 $0.5/M），每次横跳成本约 10 倍。
2. **亲和被架空**：亲和命中条件附带"亲和 key 的 provider 必须等于 candidates[0].Provider"，LRU 轮转到其他 provider 时亲和直接失效，且每次服务后亲和指针被改写，形成来回翻转。

**修复**：

- 判决改为**单次复合排序**：TIER → 延迟桶 → 剩余配额 → LRU，LRU 只作同级决胜。
- **亲和置顶提前到排序之前**：会话指纹命中的 key 在候选集内直接置顶返回（健康过滤已先行）。长会话锁定同一上行 key，缓存局部性得到结构性保证。

### 10.3 可服务目录的生命周期修正（ai-portal-go dec345d）

同步给 Proxy 的 model name map 原查询为 `status='active'`，漏掉模型生命周期中仍在服务的 EOL 状态。修正为 `IN ('active','eol_pending','eol')`——即 **In-service + EOL 预告 + EOL 服务进行中** 三态叠加，与 `active_models_cache.go` 既有定义对齐（`internal/routes/sync.go`、`internal/db/db.go` 两处）。闸门（10.1）的目录输入因此与平台生命周期语义一致。

### 10.4 配套修复（同批，3347e43）

- **reasoning 字段规范化**：WorkBuddy 等客户端把推理文本放在非标准 `reasoning` 字段，DTO 解析时丢弃 → DeepSeek 思考模式要求历史 assistant 轮回传 `reasoning_content` → 400。现按 deepseek 模型映射 `reasoning`→`reasoning_content`（其他模型照旧剥离）。
- **requestid 唯一化**：客户端复用/全零 X-Request-ID 会穿透到 `usage_records.trace_id`，Portal 按 trace_id 更新扣费列时一片行被末次覆盖（报表少报约 30%）。现内部 request_id 恒由 Proxy 生成，客户端 ID 仅存 `client_request_id` 并回显。被污染历史数据已按 balance_transactions 实扣修复。

### 10.5 本批提交

| Commit | 内容 |
|--------|------|
| ai-proxy-go `b343416` | 候选目录闸门 + smartRoute 复合排序 + 亲和置顶 |
| ai-portal-go `dec345d` | 可服务目录纳入 eol_pending / eol 生命周期 |
| ai-proxy-go `3347e43` | reasoning 字段规范化 + requestid 唯一化 |
