# 老张API（laozhang）平台问题反馈与建议

> 状态：HISTORICAL（已被 `laozhang-platform-issues-feedback-202610010630.md` 取代，含 10-01 复测更新）
> 整理时间：2026-09-30（基于 09-27 ~ 09-30 实测，default 分组、多把 API Key 交叉验证）
> 反馈人：mayiants（WOODGIT AI 平台，API 网关客户）
> 联系时可引用本文中的模型 ID、错误文案与复现方式。

---

## 一、故障类问题（按影响排序）

### P0-1 四个经销商分组长期无可用通道（9/27 起持续）

**现象**：调用 claude 系、grok-2/3/4 老系、sora、veo 等模型返回：

```
503 Current group default has no available channels for model claude-opus-4-6 under billing mode [pay-as-you-go, pay-per-request]
```

**涉及分组**（按 09-27 liveness 探测统计）：`sora_official`（49 模型）、`claude_code`（48）、`gpt_image_2_reverse`（47）、`SeeDance2`（47）。09-29 换新 Key（api2 备用域名）后同样 503，说明是**账号/分组级通道缺失**，与 Key、域名无关。

**影响**：Claude 全系已在模型总表下架（FAQ 称"资源不足暂时下线"），但 grok-4、grok-2/3 系、sora-2-character、veo-3.1 等仍挂在总表上，客户可选但必失败。

**建议**：
1. 恢复上述分组的渠道，或从 `/v1/models` 与价格总表中**暂时下线这些模型**（列表可见但不可调用是最差体验，也让我们无法用列表判断可用性）；
2. 若分组是定向资源，请在价格总表"令牌分组"列明确标注，避免 default 客户误选。

### P0-2 /v1/moderations 返回空信封（无 results）

**现象**：`POST /v1/moderations`，model 为 `omni-moderation-latest` 或 `omni-moderation-2024-09-26`，HTTP 200 但响应体为：

```json
{"id":"modr-xxxx","object":"","created":null,"model":"omni-moderation-latest","choices":null,"usage":{...}}
```

**缺失** `results[].flagged`、`results[].categories`、`results[].category_scores`——即审核结果完全拿不到，接口形同虚设。且 `omni-moderation-2024-09-26` 的响应 model 字段被改写为 `omni-moderation-latest`。文档（/api-capabilities/moderation）宣称"响应格式与 OpenAI Moderation API 相同"，实际不符。另：`text-moderation-latest`（经典名）返回 503 no channel。

**建议**：修复 moderation 渠道的结果回传；在修复前将两个 omni-moderation 模型从总表标注"暂不可用"。

### P0-3 gemini-3.x 系列间歇性 500 `empty_response`

**现象**：`gemini-3-flash-preview / 3.1-pro-preview / 3.5-flash / 3.6-flash / 3.7-flash / 3.8-flash` 调用间歇返回：

```
500 {"error":{"code":"empty_response","message":"empty response from Gemini API"}}
```

**更新（09-30 10:14 UTC 复测）**：3.5/3.7/3.8-flash 已连续成功；**3.6-flash 仍有约 1/3 概率 500**（3 次实测 2 成 1 败），属间歇性故障而非全断。09-27 期间则为持续性 500，说明该系列渠道近两天部分恢复但不稳定。

**建议**：继续观察并彻底修复 3.6-flash 渠道的空响应问题；此类间歇故障比全断更难缠——客户无法判断是该重试还是换模型，建议贵方在渠道层对 empty_response 做自动重试或明确的可重试标识。

### P1-3 api2 多节点轮询会打散 GPT 模型的 prompt cache（09-30 实测）

**实验**：约 2200 token 固定前缀 + 随机后缀，同一 Key 连续调用，对比单节点 `api` 与 5 节点轮询 `api2` 的 `usage.prompt_tokens_details.cached_tokens`：

| 模型 | api（单节点，预热后） | api2（轮询） | 说明 |
|---|---|---|---|
| deepseek-v4-flash | 100% 命中（2/2） | 100% 命中（6/6） | 不受影响 |
| gpt-5.4 | 100% 命中（缓存块 2048） | **~50% 命中（2/4）** | 命中/未命中交替出现 |
| gpt-5.5 | 100% 命中（缓存块 1792） | **~83% 命中（5/6）** | 偶发未命中 |
| gpt-5.6-luna | 0%（每次仅 `cache_write_tokens=2213`，从不返回命中） | 0% | 该型号经贵方无读缓存收益 |

**结论**：api2 的接入节点后端缓存不共享（或与调度到的上游渠道账号有关），轮询导致相同前缀的请求约一半落到"冷"后端，GPT 系（贵方主力）缓存命中率下降 17%~50%。deepseek 系不受影响（其后端缓存为共享层）。

**建议**：① api2 节点层做按 API Key 的一致性哈希（同一 Key 固定落到同一后端）；或 ② 后端缓存键提升为账号+前缀 维度共享。另外请确认 `gpt-5.6-luna` 的缓存配置（永远 cache_write 不 hit 不合理）。

**补充说明**：prompt 短于约 1024~2048 token 时 GPT 系完全不缓存（缓存块按 2048 粒度），这属 OpenAI 系正常行为，非问题。

### P1-1 价格总表与 API 定价数据不一致

**现象**：`/api/pricing` 与 docs 页面（09-29 更新）数据冲突，例如：

| 模型 | 字段 | 页面 | /api/pricing |
|---|---|---|---|
| deepseek-v4-flash | 缓存读 | $0.088/1M | cache_ratio 0.2 → $0.4/1M |
| 模型总数 | — | 249 在线 | 252 条 |

**建议**：以同一数据源生成页面与 API；价格变更时同步刷新缓存。我们这些下游是靠 `/api/pricing` 做自动定价对齐的，数据漂移会直接造成我们定价错误。

### P1-2 总表在列、实际 404/403 的模型（按 09-30 default 组实测）

| 状态 | 模型 |
|---|---|
| 404（模型不存在/无权限，总表仍在列） | DeepSeek-V3.2-Exp-nothinking、DeepSeek-V3.2-Exp-thinking、glm-4.5-flash、glm-4.5v、glm-4.6v、gpt-3.5-turbo-instruct、kimi-k2、kimi-k2-128k、kimi-k2-instruct、longcat-flash-chat、o1-pro、o1-pro-2025-03-19、o3-pro、o3-pro-2025-06-10、qwen2-72b-instruct、qwen-max-latest、qvq-max-2025-05-15、qvq-max-latest、qvq-plus-2025-05-15、qwq-32b、qwq-plus-2025-03-05、qwq-plus-latest |
| 403 | kimi-k2.6、qwen-turbo-latest、qwen-vl-max-latest、qwen-vl-plus-latest、qwen3.6-flash、qwen3.6-max-preview |
| access_denied（400） | qwen-turbo-2025-07-15 |
| 上游饱和（429，持续多日） | gpt-5-chat、gpt-5.5-pro、gpt-4o-transcribe、gpt-4o-mini-transcribe、omni-moderation-latest、omni-moderation-2024-09-26 |

**建议**：下线或修复上述渠道；429 饱和分组请扩容或在前端给出排队提示。"总表在列 ≠ 可调用"对我们做上游健康度判断成本很高。

---

## 二、计费行为确认（非问题，供贵方知悉）

**已验证的计费规则**：请求一旦 dispatch 到上游，**失败也扣费**（gemini-3.x 的 500、image 生成失败均计费）；渠道层拒绝（503 no channel / 404 / 403 / 429 饱和）不计费。此规则与 OpenAI 官方及部分友商一致，我们已按此设计下游对账逻辑，**请保持规则稳定**并在文档中明示。

## 三、产品建议

1. **模型健康状态接口**：建议提供按模型的可用状态查询（或 `/v1/models` 增加 `status` 字段/ webhook 推送渠道变更）。目前我们只能靠真实调用探测，成本高且会消耗计费配额（一天全量探测约 $0.5，其中 image 类占 90%）。
2. **分组可用性自助查询**：控制台 `/api/user/groups` 对普通 token 权限不足，客户无法自助确认自己分组下哪些模型可用。
3. **moderation / embeddings 文档补充**：moderation 页面建议增加响应字段说明（results 结构）；embeddings 页面建议补充各模型 `max input tokens` 与单批条数上限。
4. **备用域名**：api2.laozhang.ai 与 api.laozhang.ai 实测延迟无差异（/v1/models 快约 200ms，chat 无差别），作为冗余很好；建议文档明确两个域名的同步关系与 SL​​A。
5. **日志完整性**：09-30 实测发现**按次计费（quota_type=1）的 image 调用扣费已发生（余额减少 $0.06），但 `/api/log` 查不到对应记录**（2 次 gpt-image 调用 + 数分钟后的最新调用均缺失，非分页问题）。下游对账依赖调用日志，请修复按次模型的日志落库。
6. **渠道故障自动重试**：对 `empty_response` 类瞬时上游错误，建议渠道层自动重试一次，避免把间歇故障直接抛给客户。

## 附 1：P0 问题复现佐证（request id，可直接用于贵方日志检索）

| 问题 | 复现时间（UTC） | request id | 说明 |
|---|---|---|---|
| P0-1 claude 分组无通道 | 2026-09-30 10:14:26 | `2026093010142630391730096e93ae3gMI67T7l` | model=claude-opus-4-6，503 no channels，api2 域名，default 分组 Key |
| P0-2 moderation 空信封 | 2026-09-30 10:14:33 | completion_id=`modr-7403` | omni-moderation-latest，200 但无 results |
| P0-3 gemini-3.6 间歇 500 | 2026-09-30 10:15–10:16 | 3 次调用 2 成 1 败 | 500 body：`{"error":{"code":"empty_response",...}}` |

（09-27 ~ 09-30 累计复测 20+ 次，跨 6 把 Key、api/api2 双域名，现象稳定复现。）

## 附 2：我方环境信息（便于排查）

- 账号：mayiants（user_id 58609），default 分组 Key 多把（token_name 如 `20260928-default-1~5` 等，09-29 新建 api2 域名 Key 5 把）
- 复现时间：2026-09-27 ~ 2026-09-30（UTC）
- 复现方式：OpenAI 兼容 `POST /v1/chat/completions|moderations`，model 见各条目
