# 从 GT AI Gateway 借鉴的 5 个特性——采纳计划

> 来源：对 `/home/jedi/workspace/gtai`（GT AI Gateway）的源码分析  
> 目标项目：`ai-proxy-go`  
> 文档时间：2026-07-01

---

## 背景

GT AI Gateway 是一个基于 TypeScript/Hono 的轻量 AI 网关，在协议转换、流式处理、请求改写、流量记录等方面有一些设计精良的实现。经过与 `ai-proxy-go` 的对比，筛选出 5 个对当前项目价值最高、可落地性最强的特性，形成本采纳计划。

---

## 特性总览

| 优先级 | 特性 | 价值 | 预估改动范围 |
|---|---|---|---|
| P1 | OpenAI 流式 `stream_options.include_usage` 自动注入 | 流式 usage 更准确，计费更可靠 | 小 |
| P2 | 通用 SSE Parser | 统一流式解析，减少重复代码，支持多格式结束判断 | 中 |
| P3 | Header 转发白名单化 | 避免把不该传的 header 带给上游，提高兼容性 | 小 |
| P4 | 请求/响应改写插件链 | 支持 Claude Code CCH 规范化、隐私清洗等优化 | 中 |
| P5 | 丰富 retention metadata | 排查和对账更完整 | 小 |

---

## P1：OpenAI 流式 `stream_options.include_usage` 自动注入

### 好处

- OpenAI 流式响应默认**不会**在最后一个 chunk 返回 `usage`。
- 必须显式传入 `stream_options: { include_usage: true }` 才能拿到最终 token 统计。
- 如果缺少这个字段，`ai-proxy-go` 在流式场景下只能估算 usage，导致计费和用量统计偏差。

### 实现思路

在构造上游 OpenAI 请求体时，如果检测到：

1. 上游格式是 OpenAI 兼容协议
2. 请求体 `stream === true`
3. 用户没有显式设置 `stream_options`

则自动注入：

```json
{
  "stream_options": {
    "include_usage": true
  }
}
```

注入位置建议放在 OpenAI provider adapter 的 `ToProvider` 阶段，或者在 `handleStreamingResponse` 上游转发前统一处理。优先放在 adapter 层，因为：

- 只影响 OpenAI 格式上游
- 与 provider 强相关
- 不会污染其他 provider 的请求体

### 实现计划

1. 在 `pkg/relay/channel/openai/adaptor.go` 的 `ToProvider` 或请求发送前增加注入逻辑。
2. 仅当 `req.Stream == true` 且 `req.StreamOptions == nil` 时注入。
3. 如果用户已设置 `stream_options`，尊重用户原值，不覆盖。
4. 补充单元测试：验证注入行为、非 stream 不注入、已有值不覆盖。

---

## P2：通用 SSE Parser

### 好处

当前 `ai-proxy-go` 的流式处理是在各 handler 中直接读取 SSE 行并解析 `data: ` 前缀，存在几个问题：

- OpenAI、Anthropic、Responses API 的 SSE 结束标志不同（`[DONE]`、`message_stop`、`response.completed`）。
- SSE 事件可能跨多个 TCP chunk，需要缓冲不完整的帧。
- `event:`、`id:` 字段在不同格式中有不同语义（Responses API 把事件类型放在 JSON `type` 字段里）。
- 代码分散在多个 handler 中，重复逻辑多。

引入通用 SSE Parser 可以：

- 统一流式解析入口
- 正确处理不完整 chunk 的缓冲
- 统一不同格式的“结束/错误”判断
- 为后续协议转换和流量记录提供更干净的中间数据

### 实现思路

参考 GT AI Gateway 的 `src/util/sseEvent.ts`，设计 Go 版本：

```go
package sse

type Event struct {
    Data  string
    Event string
    ID    string
}

type Parser struct {
    buffer strings.Builder
}

func (p *Parser) Feed(chunk []byte) ([]Event, error)
func (p *Parser) Flush() ([]Event, error)

func IsStreamError(format ApiFormat, e Event) bool
func IsStreamCompleted(format ApiFormat, e Event) bool
```

关键行为：

- `Feed` 接收任意字节块，按 `\n\n` 拆分事件，未完整事件留在 buffer。
- 每个事件内部按 `\n` 拆分，识别 `data:`、`event:`、`id:` 行。
- 多行 `data:` 用换行拼接（OpenAI 有时会出现）。
- `IsStreamCompleted` 根据 format 判断：
  - OpenAI：`data === "[DONE]"`
  - Anthropic：`event === "message_stop"` 或 JSON `type === "message_stop"`
  - Responses：JSON `type === "response.completed"`

### 实现计划

1. 新建 `pkg/sse/parser.go` 和 `pkg/sse/parser_test.go`。
2. 先实现基础 `Parser`，覆盖单 chunk、跨 chunk、多行 data、空事件等场景。
3. 在 `handleStreamingResponse`、`handleResponsesStream` 等路径中逐步替换原有逐行解析逻辑。
4. 先替换 chat completions 流式路径，验证通过后再推广到 Responses/Claude 流式。
5. 补充单元测试和集成测试。

---

## P3：Header 转发白名单化

### 好处

- 避免把客户端自带的 `Authorization`、`x-api-key` 传给上游，导致上游收到错误凭据。
- 避免把 Cloudflare 注入的 `cf-*`、浏览器注入的 `sec-*` 传给上游，导致部分上游校验失败。
- 避免 hop-by-hop header（`connection`、`keep-alive`、`transfer-encoding` 等）被错误转发。
- 保留有用的自定义 header（如 `X-Request-ID`）用于链路追踪。

### 实现思路

在构造上游请求时，参考 GT AI Gateway 的 `EXCLUDED_HEADERS` 列表：

```go
var excludedHeaders = []string{
    "authorization", "x-api-key", "anthropic-version",
    "content-length", "host", "origin", "referer", "connection",
    "keep-alive", "proxy-authenticate", "proxy-authorization",
    "te", "trailer", "transfer-encoding", "upgrade",
}

// 过滤逻辑
for key, values := range incomingHeaders {
    lower := strings.ToLower(key)
    if strings.HasPrefix(lower, "cf-") || strings.HasPrefix(lower, "sec-") {
        continue
    }
    if slices.Contains(excludedHeaders, lower) {
        continue
    }
    upstreamHeaders[key] = values
}
```

注入上游凭据后再追加自己的 auth header。

### 实现计划

1. 在 `pkg/relay` 或 `internal/server` 中新增 header 过滤工具函数。
2. 在 OpenAI/Anthropic/Responses 等主要 adapter 的 `DoRequest` 中接入过滤。
3. 补充测试：验证 `Authorization` 被移除、`X-Request-ID` 被保留、`cf-*` 被移除。

### 审计友好设计（已补充）

为了既保证上游安全，又能满足事后审计需求，P3 采用**两层设计**：

#### 1. Debug 日志层（排错/开发）

`filterClientHeaders` 在 `Debug` 级别输出本次请求的 header 转发决策：

```json
{
  "level": "debug",
  "msg": "filtered client headers for upstream forwarding",
  "request_id": "...",
  "dropped": ["Authorization", "Sec-Fetch-Site", "CF-Ray"],
  "forwarded": ["X-Request-ID", "User-Agent", "Accept"]
}
```

- 只记录**头名**，不记录被丢弃头的值，避免泄露客户端 API key。
- 通过 `logger.Core().Enabled(zap.DebugLevel)` 做懒计算：生产默认 `Info` 级别下不分配额外内存、不产生日志。

#### 2. Retention 全头捕获层（正式审计）

如果需要持久化原始请求头，开启 retention 的 `capture_all_headers`：

```yaml
retention:
  enabled: true
  directory: ./retention
  capture_all_headers: true
```

Docker/K8s 环境变量：

```yaml
environment:
  RETENTION_ENABLED: "true"
  RETENTION_CAPTURE_ALL_HEADERS: "true"
```

- 开启后会记录**所有请求头**。
- `Authorization`、`X-API-Key`、`Cookie`、`X-Admin-Secret` 等敏感值会被自动脱敏为 `***`。
- 未脱敏的头值（如 `X-Request-ID`、`User-Agent`）原样保留，便于追踪。

---

## P4：请求/响应改写插件链

### 好处

- 把目前散落在 handler/adapter 中的硬编码改写逻辑（如模型名替换、`stream_options` 注入）抽成可插拔的插件。
- 支持 Claude Code 专用优化：
  - **CCH 规范化**：把 `claude-code` 注入的随机 `cch=` 值替换成固定值，提升上下文缓存命中率。
  - **Tracking marker 清洗**：移除 Claude Code 在 system prompt 中注入的当前时间、地区、时区、设备指纹等追踪信息，保护隐私并避免被风控。
- 支持 Responses API 的 sticky cache key 注入，让不同客户端共享 cache。
- 新增优化时不需要改核心 handler，只需新增插件文件并注册。

### 实现思路

设计轻量插件接口：

```go
package plugin

type Context struct {
    Format       string // openai / anthropic / responses
    ClientName   string // e.g. "claude-code"
    UpstreamFormat string
}

type RequestPlugin interface {
    Name() string
    Transform(body []byte, ctx Context) ([]byte, error)
}
```

插件链执行时机：

1. **转换前插件**：在协议转换前执行，针对客户端原始格式。
2. **转换后插件**：在协议转换后执行，针对上游目标格式。

示例插件实现：

```go
type ClaudeCodeTrackingRemover struct{}

func (p *ClaudeCodeTrackingRemover) Name() string { return "claude_code_tracking_remover" }

func (p *ClaudeCodeTrackingRemover) Transform(body []byte, ctx plugin.Context) ([]byte, error) {
    if ctx.Format != "anthropic" { return body, nil }
    // 正则清洗 system prompt 中的日期/地区/设备指纹
    ...
}
```

### 插件管理模型（重要澄清）

P4 中的“插件”**不是运行时安装/卸载的外部模块**，而是**编译在 ai-proxy-go 二进制内部的 Go package**。启用和禁用完全通过配置控制：

```yaml
plugin:
  stream_options_injector: true
  claude_code_cch_normalizer: true
  claude_code_tracking_remover: false
  responses_sticky_cache: true
```

对应环境变量：

```bash
PLUGIN_STREAM_OPTIONS_INJECTOR=true
PLUGIN_CLAUDE_CODE_CCH_NORMALIZER=true
PLUGIN_CLAUDE_CODE_TRACKING_REMOVER=false
PLUGIN_RESPONSES_STICKY_CACHE=true
```

**为什么这样设计：**

- proxy 通常部署在无头服务器/Docker/K8s 中，没有管理界面，也不适合动态加载外部代码。
- 所有插件代码与主二进制一起编译、一起测试，边界可控，不会引入运行时安全风险。
- 禁用某个插件时，它在 `main.go` 中不会被注册，完全不进入请求链路，对其他模块零影响。

**生效方式：**

- 启用/禁用已有插件：**改配置 + 重启 proxy**（容器滚动重启即可），**不需要重新编译**。
- 新增一个计划外的插件：需要新增 Go 代码并重新编译 binary。
- 当前不需要 Portal 管理页面；后续如需要，可以扩展为 Portal 写入配置 + proxy 监听配置变化/SIGHUP 重载。

### 实现计划

1. 新建 `pkg/plugin/` 目录，定义接口、上下文和注册表。
2. 在 `internal/config/config.go` 中增加插件开关配置。
3. 在 `cmd/server/main.go` 中根据开关注册插件。
4. 在 `handleChatCompletions`、`handleClaudeMessages`、`handleResponses` 的“转换前/转换后”接入插件链。
5. 先迁移现有 `stream_options` 注入逻辑作为第一个插件，验证框架。
6. 再实现 Claude Code CCH 规范化插件和 tracking marker 清洗插件。
7. 补充单元测试，确保插件可独立测试、组合顺序正确。
8. 在 retention/audit 中同时记录原始请求和插件改写后的请求，保证审计链条完整。

> **状态**：已实现。详细设计见 [`docs/plugin-development.md`](./plugin-development.md)。

---

## P5：丰富 retention metadata

### 好处

当前 `internal/retention/record.go` 的 `Metadata` 已经能记录用户、模型、provider、usage 等信息，但缺少一些对排查和对账很有价值的字段：

- `client_format` / `upstream_format`：排查协议转换问题
- `vendor_model_name`：记录实际上游模型名，便于对账
- `first_token_latency`：性能分析核心指标
- `is_converted`：是否经过协议转换

### 实现思路

扩展 `retention.Metadata`：

```go
type Metadata struct {
    // 已有字段 ...
    ClientFormat      string `json:"client_format,omitempty"`
    UpstreamFormat    string `json:"upstream_format,omitempty"`
    VendorModelName   string `json:"vendor_model_name,omitempty"`
    FirstTokenLatency int64  `json:"first_token_latency_ms,omitempty"`
    IsConverted       bool   `json:"is_converted,omitempty"`
}
```

在各 handler 中填充这些字段：

- `ClientFormat`：客户端请求的格式（从 URL path 或 content-type 推断）。
- `UpstreamFormat`：实际上游格式。
- `VendorModelName`：上游 key 对应的实际模型名。
- `FirstTokenLatency`：流式场景下首 token 耗时。

### 实现计划

1. 扩展 `retention.Metadata` 结构体。
2. 在 `retentionRecord` 构造逻辑中填充新字段。
3. 流式路径中增加 first token 时间戳记录。
4. 补充 retention 测试，验证新字段被正确记录。

### 与 P3 的联动

P5 的 retention 记录现在会同时记录：

- **协议元数据**：`client_format`、`upstream_format`、`vendor_model_name`、`is_converted`
- **性能指标**：`first_token_latency_ms`
- **可选全量请求头**：通过 `retention.capture_all_headers` 控制，敏感值自动脱敏

这形成了“转发决策 Debug 日志 + 全量请求头 retention 记录”的完整审计链路：

| 场景 | 推荐方式 | 数据保留 | 敏感值 |
|---|---|---|---|
| 线上排查某条请求 | Debug 日志 | 头名 | 不暴露 |
| 合规审计/事后复盘 | Retention 全头捕获 | 头名 + 值 | 自动脱敏 |

---

## 实施节奏建议

| 阶段 | 内容 | 预计时间 |
|---|---|---|
| 第 1 阶段 | P1 + P2：先实现 `stream_options` 注入和 SSE Parser | 1-2 天 |
| 第 2 阶段 | 测试 P1/P2，替换 chat completions 流式路径 | 1 天 |
| 第 3 阶段 | P3：Header 转发白名单化 | 0.5 天 |
| 第 4 阶段 | P5：丰富 retention metadata | 0.5 天 |
| 第 5 阶段 | P4：插件链框架 + Claude Code 优化插件 | 2-3 天 |
| 第 6 阶段 | 全面回归测试、文档更新 | 1 天 |

---

## 风险与注意事项

1. **P1 的兼容性**：某些非 OpenAI 官方上游（如聚合商）可能不识别 `stream_options`，注入后可能报错。建议通过 vendor/provider 配置控制是否启用，或在上游返回 400 时 fallback。
2. **P2 的回归风险**：SSE Parser 替换现有流式逻辑时，必须保证 OpenAI/Anthropic/Responses 三种流式格式都能正确解析，特别是跨 chunk 边界。
3. **P4 的隐私合规**：Claude Code tracking marker 清洗涉及修改用户请求内容，应在日志/retention 中记录原始请求和改写后请求，便于审计。
4. **P3 的 header 过滤**：不要过于激进，避免误过滤某些上游需要的自定义 header。可以先从明显有害的 header 开始。

---

## 下一步行动

优先启动 **P1** 和 **P2**：

- P1 改动小、收益明确，能快速改善流式计费的准确性。
- P2 是后续 P4（插件链中的流式改写）和更精细流量记录的基础。

完成后进行单元测试 + 集成测试，确保 chat completions 流式路径稳定后再推广到其他路径。
