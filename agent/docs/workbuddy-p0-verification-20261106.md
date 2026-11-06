# WorkBuddy P0 验证报告（Linux 实机 + 代码逆向）

- 日期：2026-11-06
- 验证人：Kimi Code（ECS `ecs-5e68` 实机）
- 对象：WorkBuddy Linux 官方 deb **5.6.2.40046327**（从官方更新接口 `https://copilot.tencent.com/v2/update?platform=workbuddy-linux-x64-deb` 获取，sha256 与官方元数据一致）
- 结论：**P0 通过**。workbuddy 支持自定义 OpenAI 兼容端点（base_url + apiKey），配置写在 `~/.workbuddy/models.json`（明文 JSON，官方明确支持手编），我方网关（nginx → ai-proxy-go）已实机跑通换 key 后的真实推理。

## 1. 实机验证环境

- ECS 上 `apt install ./WorkBuddy*.deb`，安装于 `/opt/WorkBuddy/`（标准 Electron 应用）
- 同机部署 woodgit 全栈（portal+proxy+nginx，5 容器 healthy）
- workbuddy 的 CLI（`codebuddy`，随包自带 headless bundle）直接复用桌面端同一 `models.json`

## 2. 关键发现（全部来自实装包代码 + 实测）

### 2.1 自定义模型配置

- 文件：`$WORKBUDDY_CONFIG_DIR/models.json`，默认 `WORKBUDDY_CONFIG_DIR = <os.homedir()>/.workbuddy`
  - Windows 即 `%USERPROFILE%\.workbuddy\models.json`（`main/index.js resolveDefaultConfigDir`：`os.homedir()` + 默认 `.workbuddy`，产品配置可用 `dataFolderName` 覆盖）
  - 可被环境变量 `WORKBUDDY_CONFIG_DIR` 覆盖
- 格式：JSON，兼容两种形态——纯数组，或 `{ "models": [ ... ], "availableModels": [...] }`（`availableModels` 为可选过滤清单）
- 官方明确支持手编该文件：解析器容忍 BOM 和尾随逗号（`credential-protection.js parseJsonTolerantly`）
- 模型条目字段（`server.js toModelInfoFromProduct` 全字段 + 表单保存逻辑）：
  ```json
  {
    "id": "glm-5.3",
    "name": "GLM 5.3 (Woodgit 网关)",
    "vendor": "woodgit",
    "url": "http://<网关>/v1",
    "apiKey": "sk-wgit-en-...",
    "supportsToolCall": true,
    "supportsImages": true,
    "supportsReasoning": true,
    "useCustomProtocol": false
  }
  ```
  `id/name/vendor/url/apiKey` 为实际生效的核心字段；能力开关影响 UI 与工具调用。

### 2.2 调用协议（OpenAI Chat Completions）

来自 `server.js local-custom-model-connectivity.ts`（与 agent-cli 真实调用规则一致）：

- `useCustomProtocol=false`（默认）：`url` 被归一化为**以 `/chat/completions` 结尾**——用户填 `http://host/v1`，实际请求 `POST http://host/v1/chat/completions`
- `useCustomProtocol=true`：`url` 原样使用
- 请求头：`Authorization: Bearer <apiKey>`，`Content-Type: application/json`，`Accept: text/event-stream, application/json`
- 请求体：`{"model": "<id>", "stream": true, "messages": [...]}`——SSE 与纯 JSON 响应都接受
- 错误语义：401/403=UNAUTHORIZED、404=NOT_FOUND、429=RATE_LIMITED，映射到 UI 提示

### 2.3 apiKey 落盘加密行为

- 用户级 `models.json` 的 `apiKey` 字段有 WBEF1 字段加密 codec（`credential-protection.js`），**仅在凭证策略 ≥ files 时写密文**；Windows 有 wincred 可能加密，Linux 无 keyring 时落明文
- **codec 解码兼容明文**（密文有 WBEF1 魔数，明文原样透传），因此 agent 写明文 apiKey 两边都安全
- 降级时还会主动把密文重写回明文（`reconcileUserModelsJsonToPolicy`）

### 2.4 其他与集成相关的机制

- 模型列表合并顺序：本地 product.json → 远端 `/v3/config` → TAB 实验 → **企业模型** → **自定义模型**（`desktop-models-repo.ts` 注释）——企业模型是另一条注入通道，控制台可配
- **企业策略可禁用「企业成员个人自定义模型」**（ModelsSettingsPanel 的 `enterprisePolicy`）：被禁用时 add/edit 置灰。给客户服务时若对方企业策略禁自定义模型，需改用企业模型通道（管理台统一下发）
- 桌面端登录：OAuth 轮询或环境变量 `WORKBUDDY_API_KEY` / `CODEBUDDY_API_KEY`（`auth-service.ts`）；**自定义模型使用条目自带的 url+apiKey，与平台登录态无关**（CLI 实测未登录可用）
- 项目级配置：`<cwd>/.codebuddy/models.json`（CLI 场景，不走加密 codec）；用户级在 `~/.workbuddy/models.json`

## 3. 活体测试记录（2026-11-06）

1. DB 造临时企业 `wb-p0-test` + 成员 + 企业 key `sk-wgit-en-80e7f9aa-…`（仅此测试用，已清理）
2. 写 `/root/.workbuddy/models.json`（即 2.1 的示例，url 指向 `http://123.60.182.168/v1`）
3. 运行：
   ```bash
   /opt/WorkBuddy/resources/app.asar.unpacked/cli/bin/codebuddy \
     -p "只回复两个字：成功" --model glm-5.3 --output-format json
   ```
4. 结果：assistant 回复「成功」，`is_error=false`，约 2s
5. 网关侧证据（ai-proxy-go 日志）：
   ```
   Fallback routing primary=zhipu fallback=approach model=glm-5.3
   request timing upstream_initial_ms=1558 phase=stream_success provider=approach
   ```
6. 计费侧证据：`usage_records` 记录 2 次请求、input 41612 tokens（CLI 代理脚手架较大）、client_ip=123.60.182.168——**完整走通 公网 IP → nginx:80 → proxy → approach 上行 → 计费** 全链路

注：workbuddy 内置模型清单（`--help`）中**没有** glm-5.3，该 id 只可能来自我们写入的 models.json，不存在撞名回落歧义。

## 4. 对 woodgit-agent 的实施结论

1. **注入方式**：`inject_method=file`，`config_path`：
   - linux：`$HOME/.workbuddy/models.json`
   - windows：`%USERPROFILE%\.workbuddy\models.json`（代码证据充分，仍建议 Win11 真机复核一次）
2. **注入语义**：不是单点 key/value，而是向 `models` 数组**按 `id` upsert 条目**（须保留用户已有自定义模型）→ 已在 adapter manifest 增加 `entries` 模板支持（`{{base_url}}` / `{{api_key}}` 占位符）
3. **base_url 注意**：当前 portal `APP_BASE_URL=http://api.adaii.com` 是未备案占位域名，**不可解析**。在备案完成前，agent 注入的 base_url 必须是 `http://123.60.182.168`（注意是 http——本机 nginx 用自签名证书，Electron/Node 的 fetch 会拒绝自签 https）。备案+正规证书到位后再切 `https://api.adaii.com`
4. **install_paths 探测**：linux `[/opt/WorkBuddy/workbuddy]`；windows 候选 `[%LOCALAPPDATA%\Programs\WorkBuddy\WorkBuddy.exe, C:\Program Files\WorkBuddy\WorkBuddy.exe]`
5. 企业策略风险（2.4 第 2 条）已记入风险评估：给客户部署前确认对方企业策略未禁用成员自定义模型

## 5. 遗留事项

- [ ] Win11 真机复核 `%USERPROFILE%\.workbuddy\models.json` 路径（本会话代码证据已很强）
- [ ] 域名备案后：portal `.env` 改 `APP_BASE_URL=https://api.adaii.com` 并重启
- [ ] workbuddy 桌面 GUI 的「测试连接」按钮走同一 normalize 规则，无需额外适配
