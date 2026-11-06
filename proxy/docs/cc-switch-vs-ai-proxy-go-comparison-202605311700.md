> **ACTIVE** - CC Switch vs ai-proxy-go 对比分析

# CC Switch vs ai-proxy-go 对比分析

> 日期: 2026-05-31
> 分析范围: 架构、格式转换、路由、缓存、可借鉴点

---

## 一、定位差异

| 维度 | CC Switch | ai-proxy-go |
|------|-----------|-------------|
| **定位** | 桌面应用，本地代理，管理 5+ CLI 工具 | 云端 API 网关，多租户代理服务 |
| **用户** | 单用户开发者 | 多租户、企业级 |
| **语言** | Rust (Tauri) | Go |
| **运行方式** | 本地进程 (`127.0.0.1:15721`) | Docker 容器 (`:8080`) |
| **核心价值** | 格式转换 + Provider 切换 + 故障转移 | 密钥池 + 多租户计费 + 智能路由 |

---

## 二、格式转换对比

### 2.1 转换能力

| 转换路径 | CC Switch | ai-proxy-go |
|----------|-----------|-------------|
| Anthropic → OpenAI Chat | ✅ 完整实现 | ✅ 仅 role/usage 映射 |
| OpenAI Chat → Anthropic | ✅ 完整实现 | ✅ 仅 role/usage 映射 |
| Anthropic → OpenAI Responses | ✅ 完整实现（含 Codex OAuth） | ❌ 不支持 |
| OpenAI Responses → Anthropic | ✅ 完整实现 | ❌ 不支持 |
| Anthropic → Gemini | ✅ 完整实现（含 Shadow Store） | ✅ 基础实现 |
| Gemini → Anthropic | ✅ 完整实现（含 args 修正） | ✅ 基础实现 |

### 2.2 转换深度

| 特性 | CC Switch | ai-proxy-go |
|------|-----------|-------------|
| **System prompt 处理** | 合并多条 + cache_control 冲突处理 | 基础 role 映射 |
| **tool_use/tool_result** | 完整双向转换 | 仅 role 映射 |
| **thinking/reasoning** | budget_tokens → reasoning_effort 映射 | ❌ 未处理 |
| **reasoning_content** | DeepSeek/Moonshot 兼容模式 | ❌ 未处理 |
| **Billing Header 剥离** | ✅ 提升 cache 命中率 | ❌ 未实现 |
| **Schema 清理** | 递归清理不支持的 format | ❌ 未实现 |
| **tool_choice 映射** | any→required, tool→function | ❌ 未实现 |
| **Streaming SSE** | 逐事件格式转换 | 仅透传 |
| **Codex OAuth 协议** | store/include/stream 强制约束 | ❌ 不涉及 |
| **Gemini Shadow Store** | 记录+回放 thoughtSignature | ❌ 未实现 |
| **Gemini tool args 修正** | 基于 schema hints 自动修正 | ❌ 未实现 |

### 2.3 关键差异总结

**CC Switch** 的转换是**语义级**的：理解每种格式的完整语义，处理边界情况（Codex OAuth、Gemini id 缺失、reasoning budget 映射）。

**ai-proxy-go** 的转换是**结构级**的：主要做 role 和 usage 字段名映射，不做深层语义转换。这是因为 ai-proxy-go 的场景是 OpenAI 兼容 API 代理，大部分上游 already speaks OpenAI format。

---

## 三、路由对比

### 3.1 路由架构

| 维度 | CC Switch | ai-proxy-go |
|------|-----------|-------------|
| **路由层级** | 单层：ProviderRouter | 多层：Smart Route Chain |
| **选择策略** | 优先级队列 + 熔断器 | HEALTH→TIER→RPM→LATENCY→QUOTA→LRU |
| **熔断器** | ✅ 三态（Closed/Open/HalfOpen） | ✅ 同样三态 |
| **故障转移** | 按优先级依次尝试 | 跨 Provider 回退 |
| **Provider 来源** | 本地 DB + 用户配置 | Portal 同步（30s 间隔） |
| **健康检查** | 主动探测 + 被动检测 | 被动（错误率统计） |
| **双轨路由** | ❌ 不需要 | ✅ Bedrock/Standard 双轨 |

### 3.2 关键差异

**CC Switch** 路由更简单（桌面场景，单用户），但熔断器集成更紧密。

**ai-proxy-go** 路由更复杂（云端场景，多租户），有 6 层智能路由链，支持跨 Provider 回退和 Bedrock VIP 双轨。

---

## 四、缓存对比

### 4.1 缓存机制

| 维度 | CC Switch | ai-proxy-go |
|------|-----------|-------------|
| **Prompt Cache 亲和性** | ✅ `prompt_cache_key` 注入 | ✅ Affinity Cache（SHA-256 指纹） |
| **Auth 缓存** | ❌ 不需要（本地代理） | ✅ 5 分钟 TTL，降级 30 分钟 |
| **余额缓存** | ❌ 不需要 | ✅ 10 秒 TTL |
| **Key Pool 缓存** | ❌ 不需要（本地 Key） | ✅ 内存 + Redis 双层 |
| **Billing Header 优化** | ✅ 剥离动态前缀提升命中率 | ❌ 未实现 |
| **Usage 聚合** | ✅ 实时统计 | ✅ 异步批量（3s/30s） |

### 4.2 关键差异

**CC Switch** 的缓存重点在 **prompt cache 命中率优化**（billing header 剥离、prompt_cache_key 注入）。

**ai-proxy-go** 的缓存重点在 **多租户状态管理**（auth 缓存、key pool 缓存、affinity 缓存）。

---

## 五、可借鉴点

### 5.1 高优先级借鉴

#### 1. Billing Header 剥离（提升 Prompt Cache 命中率）

**问题**：Claude Code 会在 system prompt 开头插入动态 `x-anthropic-billing-header:` 行，每次请求的 `cch=` 值不同，导致 prefix cache 无法命中。

**CC Switch 方案** (`transform.rs:18-47`)：
```rust
pub(crate) fn strip_leading_anthropic_billing_header(text: &str) -> &str {
    if !text.starts_with("x-anthropic-billing-header:") { return text; }
    // 只剥离开头的 billing header，保留后续内容
}
```

**建议**：在 ai-proxy-go 的 `pkg/convert/common.go` 中添加类似函数，在 Claude 请求转发前剥离 billing header。

**预期收益**：提升 prompt cache 命中率，降低 token 消耗。

#### 2. reasoning_effort 映射（支持推理模型）

**问题**：DeepSeek-R1、OpenAI o-series 等推理模型使用 `reasoning_effort` 参数控制推理深度，但 Anthropic 格式使用 `thinking.budget_tokens`。

**CC Switch 方案** (`transform.rs:82-112`)：
```
thinking.type: "adaptive" → reasoning_effort: "xhigh"
thinking.budget_tokens: <4000 → "low"
thinking.budget_tokens: 4000-15999 → "medium"
thinking.budget_tokens: ≥16000 → "high"
```

**建议**：在 ai-proxy-go 的请求转换层添加 reasoning_effort 映射，支持推理模型的深度控制。

#### 3. tool_choice 映射

**问题**：Anthropic 的 `tool_choice: {type: "any"}` 在 OpenAI 中应为 `"required"`，`{type: "tool", name: X}` 应为 `{type: "function", function: {name: X}}`。

**CC Switch 方案** (`transform.rs:251-272`)：完整的 tool_choice 双向映射。

**建议**：ai-proxy-go 当前未处理 tool_choice 映射，添加后可避免上游 400 错误。

### 5.2 中优先级借鉴

#### 4. Schema 清理（递归移除不支持的 format）

**CC Switch 方案** (`transform.rs:502-521`)：递归遍历 JSON schema，移除 `"format": "uri"` 等不被所有上游支持的字段。

**建议**：在 ai-proxy-go 的 tool schema 转换中添加清理逻辑。

#### 5. Gemini Shadow Store（解决 functionCall id 缺失）

**问题**：Gemini 2.x 并行 functionCall 不返回 id，导致后续 functionResponse 无法匹配。

**CC Switch 方案**：`GeminiShadowStore` 记录每次 assistant turn 的完整 parts，下一次请求时回放。

**建议**：如果 ai-proxy-go 需要深度支持 Gemini 原生 API，可参考此方案。

#### 6. 流式 SSE 逐事件转换

**问题**：不同 API 格式的 SSE 事件结构不同，直接透传会导致客户端解析失败。

**CC Switch 方案**：为每种格式提供 `create_anthropic_sse_stream` 逐事件转换器。

**建议**：ai-proxy-go 当前流式响应仅透传，如需支持 Claude 客户端直连，需要添加流式转换。

### 5.3 低优先级借鉴

#### 7. Adapter trait 设计

**CC Switch** 的 `ProviderAdapter` trait 将 `needs_transform()` 与 `transform_request/response` 分离，允许延迟判断是否需要转换。

**ai-proxy-go** 的 `Adaptor` 接口将 `ToProvider/FromProvider` 作为固定方法，所有请求都经过转换（即使无需转换）。

**建议**：可考虑添加 `needs_transform()` 方法优化性能。

#### 8. Canonical JSON 序列化

**CC Switch** 使用 `canonical_json_string()` 确保 tool arguments 的 JSON 序列化一致性（字段排序、无多余空白），提升缓存命中率。

**建议**：ai-proxy-go 可在 tool arguments 序列化时使用 canonical JSON。

---

## 六、ai-proxy-go 可输出给 CC Switch 的借鉴

### 6.1 密钥池 + 多租户计费

CC Switch 是单用户桌面工具，不需要密钥池和计费。但 ai-proxy-go 的以下设计可参考：

- **分片锁 + 原子计数器**：高并发下的 key 选择性能
- **跨 Provider 回退**：当主 Provider 不可用时自动尝试所有支持该模型的 Provider
- **Affinity Cache**：基于请求指纹的 provider 亲和性，提升 cache 命中率

### 6.2 审计 + 批量上报

ai-proxy-go 的异步批量 usage 上报（3s/30s 刷新间隔）可参考用于 CC Switch 的用量统计。

### 6.3 健康检查 + 熔断器参数

ai-proxy-go 的熔断器参数（5 次失败/60s 冷却）和健康检查策略（错误率 >80% → unhealthy）可作为 CC Switch 的参考基线。

---

## 七、总结

| 维度 | CC Switch 优势 | ai-proxy-go 优势 |
|------|---------------|-----------------|
| **格式转换** | 语义级完整转换，覆盖所有边界情况 | 结构级轻量转换，适配 OpenAI 生态 |
| **路由** | 简洁直接，桌面场景足够 | 多层智能路由，云端场景必需 |
| **缓存** | Prompt cache 命中率优化 | 多租户状态管理 |
| **可扩展性** | Adapter trait 设计清晰 | Provider 前缀匹配 + 21 个内置配置 |
| **生产就绪** | 桌面应用，单用户 | 企业级，多租户，计费，审计 |

**核心结论**：

1. **格式转换**：CC Switch 的转换实现远比 ai-proxy-go 完整，尤其是 billing header 剥离、reasoning_effort 映射、tool_choice 映射可直接借鉴。

2. **路由/缓存**：ai-proxy-go 的多层路由和多租户缓存设计更成熟，CC Switch 可参考。

3. **架构差异**：两者定位不同，CC Switch 是本地代理（单用户、格式转换为主），ai-proxy-go 是云端网关（多租户、路由计费为主），借鉴时需考虑场景差异。

---

*分析完成*
