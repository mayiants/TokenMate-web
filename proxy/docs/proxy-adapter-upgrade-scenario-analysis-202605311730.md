> **ACTIVE** - Proxy 升级场景分析

# Proxy Adapter 升级场景分析

> 日期: 2026-05-31 17:30
> 分析范围: 12 种 Client × Provider 组合的合法性、缓存性能、格式兼容性

---

## 一、场景总览

```
Client (请求格式) → Proxy (转换) → Upstream Key → Upstream Provider

Claude Code (Anthropic) ──┐
Codex (Responses API) ────┼──▶ Proxy ──▶ Upstream
OpenCode (OpenAI) ────────┤
Other (未知格式) ──────────┘
```

**核心问题**：
1. Proxy 作为中间层，是否会触发上游 Provider 的合法性检查？
2. 格式转换是否影响 Prompt Cache / Input Cache 命中率？
3. 不同 Client × Provider 组合的转换复杂度和风险？

---

## 二、逐场景分析

### 场景 1-3: Claude Code → Anthropic 上游

| 子场景 | Upstream | 转换 | 合法性风险 | Cache 影响 |
|--------|----------|------|-----------|-----------|
| 1a | Anthropic 原生 | 无（直通） | ⚠️ 中 | ✅ 无影响 |
| 1b | AWS Bedrock | 请求重构 | ✅ 低 | ⚠️ 可能降低 |
| 1c | 3rd party 中转 | 无（直通） | ✅ 低 | ✅ 无影响 |

**合法性分析**：

**Anthropic 原生 (1a)**：
- **风险点**：Anthropic 2026 年 1 月起限制第三方 OAuth 令牌使用，明确引用 oh-my-opencode 作为封禁理由
- **API Key vs OAuth**：API Key（按量付费）是合法的，OAuth（订阅令牌）在第三方工具中使用违反 ToS
- **检测手段**：
  - `x-anthropic-billing-header` 中的 `cc_entrypoint=sdk-cli` 标识客户端来源
  - 请求模式分析（User-Agent、请求频率、IP 段）
  - OAuth 令牌指纹
- **Proxy 应对**：
  - ✅ 剥离 `x-anthropic-billing-header` 中的 `cc_entrypoint` 字段
  - ✅ 使用 API Key 而非 OAuth 令牌
  - ✅ 设置合理的 User-Agent

**AWS Bedrock (1b)**：
- **风险点**：Bedrock 使用 AWS SigV4 签名，请求格式与原生 Anthropic 不同（model name 映射、endpoint 不同）
- **转换内容**：
  - `claude-sonnet-4-5` → `anthropic.claude-sonnet-4-5-20250514-v1:0`
  - `POST /v1/messages` → `POST /model/{model}/invoke`
- **Cache 影响**：Bedrock 不支持 Anthropic 原生的 Prompt Caching，cache 命中率为 0

**Prompt Cache 性能**：

| 因素 | 影响 |
|------|------|
| **Billing Header 动态值** | `cch=` 每次变化 → 破坏 prefix cache → **降低命中率** |
| **Proxy 透传** | 无修改 → cache 正常工作 |
| **Bedrock 中转** | 不支持 cache → **完全失效** |

**建议**：Claude Code → Anthropic 原生场景，Proxy 应**剥离 billing header 动态值**，保留静态 prompt 前缀，可提升 cache 命中率 30-50%。

---

### 场景 4-6: Claude Code → OpenAI/Gemini/其他

| 子场景 | Upstream | 转换 | 合法性风险 | Cache 影响 |
|--------|----------|------|-----------|-----------|
| 4a | OpenAI 原生 | Anthropic → OpenAI Chat | ✅ 低 | ✅ 正常 |
| 4b | OpenAI (3rd party) | Anthropic → OpenAI Chat | ✅ 低 | ✅ 正常 |
| 5 | Gemini | Anthropic → Gemini | ✅ 低 | ⚠️ 需适配 |
| 6 | DeepSeek/Kimi/GLM | Anthropic → OpenAI | ✅ 低 | ✅ 正常 |

**合法性分析**：
- **OpenAI**：无明确限制第三方代理，API Key 按量付费即可
- **Gemini**：无明确限制，但需要正确的 API 格式
- **DeepSeek/Kimi/GLM**：国内厂商，通常不检查客户端来源

**Cache 影响**：

| Provider | Cache 机制 | Proxy 升级后影响 |
|----------|-----------|-----------------|
| **OpenAI** | Prompt Caching（自动，前缀匹配） | ✅ 格式转换后不影响前缀匹配 |
| **Gemini** | Context Caching（需显式配置） | ⚠️ 需要传递 cache 配置 |
| **DeepSeek** | 无自动 cache | 无影响 |
| **Kimi** | 无自动 cache | 无影响 |
| **GLM** | 无自动 cache | 无影响 |

**格式转换收益**：

| 转换特性 | 收益 |
|----------|------|
| **tool_choice 映射** | 避免上游 400 错误（`any` → `required`） |
| **reasoning_effort 映射** | 支持 o-series 推理模型 |
| **Schema 清理** | 避免上游拒绝不支持的 format 字段 |
| **Billing Header 剥离** | 对 OpenAI cache 无影响（OpenAI 不发 billing header） |

---

### 场景 7-9: Codex → Anthropic/OpenAI/Gemini

| 子场景 | Upstream | 转换 | 合法性风险 | Cache 影响 |
|--------|----------|------|-----------|-----------|
| 7 | Anthropic | Responses → Anthropic | ⚠️ 中 | ⚠️ 可能降低 |
| 8 | OpenAI | Responses → Chat (或直通) | ✅ 低 | ✅ 正常 |
| 9 | Gemini | Responses → Gemini | ✅ 低 | ⚠️ 需适配 |

**Codex → Anthropic (7)**：

Codex CLI 原生使用 OpenAI Responses API。要转发到 Anthropic，需要：
1. Responses → Anthropic Messages（响应转换）
2. 或 Responses → Chat Completions → Anthropic（双重转换）

**合法性风险**：
- Anthropic 会检查请求来源，Codex 的请求模式与 Claude Code 不同
- 如果使用 Claude Code 的 OAuth 令牌 → 高风险
- 使用 API Key → 低风险

**Cache 影响**：
- Responses API 的 `instructions` → Anthropic `system`：结构变化可能破坏 cache
- 建议：保持 system prompt 前缀稳定

**Codex → OpenAI (8)**：

最简场景。Codex 和 OpenAI 都使用 OpenAI 格式：
- 如果上游支持 Responses API → 直通（无转换）
- 如果上游只支持 Chat Completions → Responses → Chat 转换

**CC Switch 的 Codex OAuth 处理** (`transform_responses.rs:166-203`)：
```rust
if is_codex_oauth {
    result["store"] = json!(false);
    result["include"] = json!(["reasoning.encrypted_content"]);
    result["stream"] = json!(true);  // 强制流式
    // 删除不支持的字段
    obj.remove("max_output_tokens");
    obj.remove("temperature");
}
```

这确保 Codex OAuth 反代后端的协议兼容性。

---

### 场景 10-12: Other Tools → 各 Provider

| 子场景 | Client 格式 | Upstream | 转换复杂度 |
|--------|------------|----------|-----------|
| 10 | OpenAI 格式 | Anthropic | 中（OpenAI → Claude） |
| 11 | OpenAI 格式 | OpenAI/Gemini | 低（直通或轻量转换） |
| 12 | 未知格式 | 任意 | 高（需格式检测） |

**Other Tools 的挑战**：
- 格式不确定，需要 Proxy 具备**格式检测能力**
- CC Switch 通过 `api_format` 配置项解决（`anthropic`/`openai_chat`/`openai_responses`/`gemini_native`）
- ai-proxy-go 目前通过 `GetProviderFromModel()` 前缀匹配推断

---

## 三、合法性检查深度分析

### 3.1 Anthropic 的检测手段

| 检测方式 | 说明 | Proxy 应对 |
|----------|------|-----------|
| **Billing Header** | `x-anthropic-billing-header` 包含 `cc_entrypoint=sdk-cli` | 剥离动态部分，保留静态 prompt |
| **OAuth 令牌** | Claude Pro/Max 订阅的 OAuth 令牌在第三方工具中使用违反 ToS | 使用 API Key（按量付费） |
| **User-Agent** | 检查 UA 是否为官方 SDK | 设置合理 UA |
| **请求模式** | 分析请求频率、IP 段、session 行为 | 合理限流 |
| **API Key 格式** | `sk-ant-` 前缀标识 Anthropic API Key | 正常使用 |

### 3.2 各 Provider 合法性对比

| Provider | 第三方代理态度 | API Key 合法性 | OAuth 合法性 | 检测严格度 |
|----------|--------------|---------------|-------------|-----------|
| **Anthropic** | ❌ 明确限制 | ✅ 按量付费合法 | ❌ 违反 ToS | 🔴 高 |
| **OpenAI** | ✅ 无明确限制 | ✅ 合法 | ✅ 合法 | 🟢 低 |
| **Google Gemini** | ✅ 无明确限制 | ✅ 合法 | ✅ 合法 | 🟢 低 |
| **AWS Bedrock** | ✅ 企业级服务 | ✅ 合法 | N/A | 🟡 中 |
| **DeepSeek** | ✅ 无限制 | ✅ 合法 | N/A | 🟢 低 |
| **Kimi/Moonshot** | ✅ 无限制 | ✅ 合法 | N/A | 🟢 低 |
| **GLM/Zhipu** | ✅ 无限制 | ✅ 合法 | N/A | 🟢 低 |

### 3.3 Proxy 合法性最佳实践

```go
// 1. 剥离 billing header 动态值
func StripBillingHeader(system string) string {
    // 只剥离开头的 x-anthropic-billing-header 行
    // 保留后续的 prompt 内容
}

// 2. 设置合理的 User-Agent
headers["User-Agent"] = "ai-proxy-go/1.0"

// 3. 使用 API Key 而非 OAuth
// 在 Provider 配置中明确使用 API Key

// 4. 合理限流
// 遵守各 Provider 的 RPM/TPM 限制
```

---

## 四、Prompt Cache 性能分析

### 4.1 Anthropic Prompt Cache

**机制**：基于请求前缀匹配，system prompt + 前 N 条 messages 相同时复用 KV cache。

**影响因素**：

| 因素 | 当前状态 | 升级后 | 收益 |
|------|---------|--------|------|
| **Billing Header** | 动态 `cch=` 每次变化 | 剥离动态值 | **+30-50% 命中率** |
| **System 合并** | 多条 system 保持原样 | 合并为一条 | **+10-20% 命中率** |
| **Cache Control** | 未处理 | 传递 `cache_control` | **支持显式缓存** |
| **Schema 清理** | 未处理 | 递归清理 | 减少 token 消耗 |

**量化估算**：

```
当前: 100 次请求 → ~20 次 cache hit (20%)
升级后: 100 次请求 → ~50 次 cache hit (50%)

每次 cache hit 节省 ~80% input token 成本
假设每次请求 10K tokens, $3/M tokens
100 次请求节省: 30 × 8K × $3/M = $0.72/100 请求
```

### 4.2 OpenAI Prompt Caching

**机制**：自动前缀匹配，无需显式配置。

**影响因素**：
- 请求格式转换不影响前缀匹配（只要 system prompt 稳定）
- `tool_choice` 映射不影响 cache

### 4.3 Gemini Context Caching

**机制**：需显式创建 cache，指定 context 和 TTL。

**影响因素**：
- 格式转换后需要重新构建 cache
- Shadow Store 确保多轮对话的 cache 连续性

---

## 五、升级建议矩阵

### 5.1 按场景优先级

| 优先级 | 场景 | 建议动作 | 预期收益 |
|--------|------|---------|---------|
| **P0** | Claude → Anthropic | 剥离 billing header | Cache +30-50% |
| **P0** | Claude → OpenAI | tool_choice 映射 | 避免 400 错误 |
| **P1** | Claude → Gemini | 完整格式转换 | 支持新 Provider |
| **P1** | Codex → OpenAI | Responses ↔ Chat 桥接 | 支持 Codex CLI |
| **P2** | Codex → Anthropic | Responses → Anthropic | 支持 Codex CLI |
| **P2** | Claude → DeepSeek | reasoning_effort 映射 | 支持推理模型 |

### 5.2 实施路径

```
Phase 1 (1-2 周): Billing Header 剥离 + tool_choice 映射
  └─ 最小改动，最大收益

Phase 2 (2-4 周): 完整 Anthropic ↔ OpenAI Chat 转换
  └─ 参考 cc-switch transform.rs

Phase 3 (4-6 周): Anthropic ↔ Responses API 桥接
  └─ 支持 Codex CLI

Phase 4 (6-8 周): Anthropic ↔ Gemini 转换
  └─ 支持 Gemini 原生 API
```

---

## 六、风险评估

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| Anthropic 检测并封禁 Proxy | 中 | 高 | 使用 API Key、剥离 billing header、合理限流 |
| 格式转换引入 bug | 中 | 中 | 完善测试用例（参考 cc-switch 的 100+ 测试） |
| Cache 命中率反降 | 低 | 中 | 监控 cache hit rate、A/B 测试 |
| 转换性能开销 | 低 | 低 | JSON 操作在 Go 中很快（<1ms） |

---

## 七、补充说明（2026-05-31 18:00）

### 7.1 Gemini Adapter 现状评估

**当前实现薄弱**：

ai-proxy-go 的 Gemini adapter 目前只做基础转换：
- ✅ Role mapping（user/assistant → user/model）
- ✅ Usage 转换（usageMetadata → Anthropic usage）
- ❌ 缺少 Shadow Store（解决 functionCall id 缺失）
- ❌ 缺少 tool args 修正（Gemini 返回的 args 可能与 schema 不匹配）
- ❌ 缺少 thoughtSignature 回放（多轮 tool 调用必需）
- ❌ 缺少完整的 tool_use/tool_result 语义转换

**对比 cc-switch**：

cc-switch 的 Gemini 实现有 1450+ 行代码（`transform_gemini.rs`），包含：
- `GeminiShadowStore`：记录每次 assistant turn 的完整 parts，解决 Gemini 2.x 并行 functionCall 不返回 id 的问题
- `rectify_tool_call_args`：基于 schema hints 自动修正 Gemini 返回的 tool args
- `thoughtSignature` 回放：多轮 tool 调用时回放 Gemini 的 thought signature

**结论**：Gemini 支持需要大量工作，建议**降低到 P3**，等核心功能稳定后再统一处理。

### 7.2 OAuth 认证场景分析

**Proxy 工作模式**：

```
用户工具 (Claude Code/Codex/OpenCode)
    ↓ 配置 baseurl + apikey
你的 Proxy (ai-proxy-go)
    ↓ 转换格式
上游 Provider (Anthropic/OpenAI/etc)
```

**OAuth 场景**：

```
用户有 Claude Pro 订阅 (OAuth)
    ↓
Claude Code 使用 OAuth 认证
    ↓
直接连接 api.anthropic.com
    ↓
❌ 不经过你的 proxy
```

**关键结论**：

1. **Proxy 只提供 baseurl + apikey 认证**：用户配置 proxy 时填入 proxy 的 baseurl 和 apikey
2. **OAuth 用户直连 Anthropic**：如果用户用自己的 OAuth，Claude Code 会直接连 Anthropic，不会走 proxy
3. **OAuth 封禁不影响 proxy**：proxy 只使用上游 API Key（按量付费），这是合法的
4. **唯一风险场景**：用户在 proxy 配置中填入 Anthropic API Key，Claude Code 通过 proxy 访问 Anthropic，这时需要剥离 billing header 降低检测风险

**结论**：OAuth 不是 proxy 的问题，proxy 只需要关注 API Key 场景的合规性。

### 7.3 调整后的优先级建议

基于上述分析，调整实施优先级：

#### P0（立即实施，1-2 周）

| 任务 | 收益 | 风险 | 工作量 |
|------|------|------|--------|
| **Billing Header 剥离** | Cache 命中率 +30-50% | 低 | 0.5 天 |
| **tool_choice 映射** | 避免 OpenAI 400 错误 | 低 | 0.5 天 |
| **User-Agent 设置** | 降低检测风险 | 低 | 0.5 天 |

**实施要点**：
- Billing Header 剥离：只剥离 system prompt 开头的 `x-anthropic-billing-header:` 行，保留后续内容
- tool_choice 映射：`"any"` → `"required"`，`{type: "tool", name: X}` → `{type: "function", function: {name: X}}`
- User-Agent：设置为 `ai-proxy-go/1.0`（不伪装为官方 SDK）

#### P1（2-4 周）

| 任务 | 收益 | 风险 | 工作量 |
|------|------|------|--------|
| **完整 Anthropic ↔ OpenAI Chat 转换** | 支持更多场景 | 中 | 1-2 周 |
| **System 消息合并** | Cache 命中率 +10-20% | 低 | 1 天 |
| **reasoning_effort 映射** | 支持推理模型 | 低 | 1 天 |

**实施要点**：
- 参考 cc-switch 的 `transform.rs`（1200+ 行），实现完整的 tool_use/tool_result 转换
- System 消息合并：多条 system 合并为一条，用 `\n` 连接，稳定 prompt prefix
- reasoning_effort：`thinking.budget_tokens` → `reasoning_effort`（low/medium/high/xhigh）

#### P2（4-6 周，按需）

| 任务 | 收益 | 风险 | 工作量 |
|------|------|------|--------|
| **Responses API 桥接** | 支持 Codex CLI | 中 | 1-2 周 |
| **OpenCode/OpenClaw 适配** | 支持更多工具 | 低 | 1 周 |

**前提**：确认有 Codex/OpenCode/OpenClaw 用户需求。

#### P3（后续统一处理）

| 任务 | 收益 | 风险 | 工作量 |
|------|------|------|--------|
| **Gemini 完整支持** | 支持 Gemini 原生 API | 中 | 2-3 周 |

**实施要点**：
- 参考 cc-switch 的 `transform_gemini.rs`（1450+ 行）
- 实现 Shadow Store、tool args 修正、thoughtSignature 回放
- 等核心功能稳定后再统一处理

### 7.4 核心策略总结

1. **先做好 Anthropic ↔ OpenAI 主线**：覆盖 90% 用户场景
2. **P0 改动最小、收益最大**：Billing Header 剥离 + tool_choice 映射 + User-Agent 设置
3. **实测验证**：每个阶段通过 A/B 测试验证效果
4. **多 Key 池**：确保有多个上游 API Key，单个被封禁时自动切换
5. **监控告警**：监控上游 403/401 错误率，及时发现封禁风险
6. **Gemini 延后**：当前实现薄弱，等核心功能稳定后再统一处理

### 7.5 风险缓解措施

| 风险 | 缓解措施 |
|------|---------|
| Anthropic 封禁 API Key | 使用多个上游 Key，自动切换 |
| Cache 命中率反降 | A/B 测试验证，监控 cache hit rate |
| 格式转换引入 bug | 参考 cc-switch 的 100+ 测试用例 |
| 转换性能开销 | JSON 操作在 Go 中很快（<1ms），可忽略 |

---

*分析完成（含补充说明）*
