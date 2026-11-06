# P4 实现计划：请求/响应改写插件链

## 目标

在 `ai-proxy-go` 中落地 GT AI Gateway 的插件链思想：

1. 把目前散落在 adapter/handler 中的硬编码改写逻辑（如 `stream_options.include_usage` 注入）迁移为可开关的插件。
2. 新增 Claude Code 专用插件：
   - CCH 规范化（把 `x-anthropic-billing-header` 中随机 `cch=` 值固定为 `A1234`）
   - Tracking marker 清洗（把 `Today(')s date is YYYY/MM/DD` 统一为 `YYYY-MM-DD` 格式，减少对 Anthropic prompt caching 的破坏）
3. 新增 Responses API `prompt_cache_key` 注入插件。
4. 所有插件编译内建，通过配置/环境变量启用，默认开启以维持现有行为。

## 设计决策

### 插件是编译时内建模块，不是动态外挂

- 插件代码放在 `pkg/plugin/`，随 binary 一起编译。
- 启用/禁用通过 `plugin.*` 配置或 `PLUGIN_*` 环境变量控制。
- 禁用插件时不会被注册到链中，对其他模块零影响。

### 插件接口基于原始 JSON 字节

```go
package plugin

type Context struct {
    ClientFormat   string // e.g. "openai", "anthropic", "responses"
    UpstreamFormat string // e.g. "openai", "anthropic", "responses"
    HostKey        string // 建议用 keyID 或 upstreamKey.ID
    ClientName     string // 建议用 userID 或 User-Agent
}

type Plugin interface {
    Name() string
    Transform(body []byte, ctx Context) ([]byte, error)
}

type Chain struct { ... }
func (c *Chain) Apply(body []byte, ctx Context) ([]byte, error)
```

- 与 GT AI Gateway 保持一致：插件接收上游/客户端原始 JSON，修改后再返回。
- 每个插件自行判断 `ctx.ClientFormat` / `ctx.UpstreamFormat`，不匹配时直接原样返回。
  - `claude_code_cch_normalizer` 与 `claude_code_tracking_remover` 同时支持 Anthropic 原生客户端（`ClientFormat == "anthropic"`）和 OpenAI facade → Claude 上游（`UpstreamFormat == "claude"`）。
- `Transform` 内部出错时捕获异常并返回原始 body，避免因为一个插件失败整个请求失败。

### 插件链在 handler 中分两次调用

- **转换前（pre-conversion）**：基于 `ClientFormat`，处理客户端专属内容。
  - `responses_prompt_cache_key`：处理 responses 客户端请求体。
- **转换后（post-conversion）**：基于 `UpstreamFormat`，处理上游协议补充字段。
  - `stream_options_injector`：当上游为 openai 且 `stream=true` 时注入 `stream_options.include_usage`。
  - `claude_code_cch_normalizer` / `claude_code_tracking_remover`：当上游为 claude 时清洗 system prompt（也支持 Anthropic 原生客户端）。

这样 OpenAI facade → Claude 上游时，tracking/CCH 会在转换后被清洗；Responses API 请求会在转换前拿到 `prompt_cache_key`。

### adapter 层支持直接发送字节

当前 `BaseAdaptor.DoRequest` 接收 `interface{}` 并自己 marshal。为了发送插件修改后的字节，新增：

```go
func (a *BaseAdaptor) DoRequestBytes(body []byte, stream bool) (*http.Response, error)
```

复用现有的 URL、header、auth、ExtraHeaders 逻辑，只是跳过 marshal 步骤。

## 文件变更清单

### 新增

- `pkg/plugin/plugin.go` — 接口、`Context`、`Chain`
- `pkg/plugin/stream_options_injector.go` + `_test.go`
- `pkg/plugin/claude_code_cch_normalizer.go` + `_test.go`
- `pkg/plugin/claude_code_tracking_remover.go` + `_test.go`
- `pkg/plugin/responses_prompt_cache_key.go` + `_test.go`

### 修改

- `pkg/relay/base/adaptor.go` — 新增 `DoRequestBytes`
- `pkg/relay/channel/openai/openai.go` — 移除 `ToProvider` 中硬编码的 `stream_options` 注入逻辑
- `internal/config/config.go` — 新增 `PluginConfig`，加默认值和环境变量覆盖
- `cmd/server/main.go` — 根据配置构建 plugin chain 并注入 HandlerService
- `internal/server/handler_service.go` — 新增 `pluginChain` 字段和 setter
- `internal/server/handlers_chat.go` — 接入 pre/post conversion 插件链；Anthropic passthrough 路径也接入
- `internal/server/handlers_responses.go` — 接入 pre-conversion 插件链
- `docs/gtai-features-adoption-plan.md` — 把实现状态更新为“已完成”

## 配置设计

```yaml
plugin:
  enabled: true
  stream_options_injector: true
  claude_code_cch_normalizer: true
  claude_code_tracking_remover: true
  responses_prompt_cache_key: true
```

环境变量：

```bash
PLUGIN_ENABLED=true
PLUGIN_STREAM_OPTIONS_INJECTOR=true
PLUGIN_CLAUDE_CODE_CCH_NORMALIZER=true
PLUGIN_CLAUDE_CODE_TRACKING_REMOVER=true
PLUGIN_RESPONSES_PROMPT_CACHE_KEY=true
```

- 所有插件默认 `true`，保持当前行为等价。
- `plugin.enabled=false` 时整个链不注册，零开销。

## 实现步骤

1. **定义插件框架**
   - 创建 `pkg/plugin/plugin.go`。
   - 实现 `Chain` 的按顺序执行和错误兜底。

2. **实现 4 个内置插件**
   - 每个插件独立文件 + 单元测试。
   - 参考 GT AI 的 regex 和行为。

3. **迁移 `stream_options` 注入**
   - 从 `pkg/relay/channel/openai/openai.go` 移除硬编码注入。
   - 在 `stream_options_injector` 插件中实现，并保证已有测试仍然通过。

4. **adapter 层支持字节请求**
   - 在 `pkg/relay/base/adaptor.go` 新增 `DoRequestBytes`。
   - 确保 header、auth、ExtraHeaders 逻辑复用，不重复实现。

5. **配置接入**
   - `internal/config/config.go` 增加 `PluginConfig` 和 `PLUGIN_*` 环境变量覆盖。
   - `cmd/server/main.go` 构建 chain 并通过 `SetPluginChain` 注入。
   - `internal/server/handler_service.go` 新增字段和 setter。

6. **在 handler 中接入插件链**
   - `handleChatCompletions`：pre-conversion（openai 客户端无操作），post-conversion 注入 stream_options。
   - `handleClaudeMessages`：pre-conversion 清洗 system prompt，passthrough 直接发送修改后 body；conversion 路径 post-conversion 注入 stream_options（如果上游是 openai）。
   - `handleResponses`：pre-conversion 注入 prompt_cache_key，post-conversion 按需处理。

7. **补全 retention/audit 记录**
   - 插件修改前后分别记录日志（`Info` 级别，记录插件名和是否修改）。
   - retention 继续记录原始客户端请求体；如后续需要，可再扩展 `UpstreamRequest` 字段。

8. **测试**
   - `go test ./pkg/plugin/...` 单元测试。
   - `go test ./internal/server/...` handler 集成测试。
   - `go test ./...` 全量回归。
   - `go vet ./... && gofmt -l .`。

9. **提交并推送**
   - 单条 commit：`feat: implement P4 request/response rewrite plugin chain`。
   - `git push origin main`。

## 测试重点

- `stream_options` 插件：stream=true 且无设置时注入；非 stream 不注入；已有值不覆盖。
- CCH normalizer：命中 `x-anthropic-billing-header:...cch=xxx;` 时替换为 `cch=A1234;`；未命中时原样返回。
- Tracking remover：`Today(')s date is 2026/06/28` → `Today's date is 2026-06-28`；数组/字符串 system 都支持。
- Responses cache key：无值时注入 `{hostKey}:{clientName}`；有值时尊重原值。
- Chain：插件顺序执行；单个插件 panic/error 不影响返回原 body。

## 风险与回退

- **stream_options 迁移回退**：如果测试发现某些上游对 `stream_options` 不兼容，可临时通过 `PLUGIN_STREAM_OPTIONS_INJECTOR=false` 关闭，无需改代码。
- **CCH/Tracking 改写回退**：如果清洗规则误伤用户正常 system prompt，可单独关闭对应插件。
- **性能**：插件操作是 JSON parse/stringify，只在请求链路上执行一次；Chain 内部短路不匹配格式，开销可忽略。
