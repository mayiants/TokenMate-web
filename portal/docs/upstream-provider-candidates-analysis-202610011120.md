# 上游 Provider 候选补充分析（LZ/TR 之外）

> 状态：ACTIVE
> 日期：2026-10-01
> 背景：现有上游组合 = TokenRouter（目录全、RPM 极低 8-15/key）+ laozhang（容量好 ~965 RPM/key、但 claude 全线下线、gemini 渠道波动）+ AWS Bedrock/Mantle（品种少、账号权限受限）。本文评估可补充模型能力与容量的候选聚合平台。
> 评估口径与 `tr-vs-laozhang-provider-comparison-analysis-202609090905.md` 一致：覆盖 / 容量 / 稳定性 / 合规 / 接入成本。

---

## 一、痛点回顾（补充要解决的问题）

| 痛点 | 现状 |
|---|---|
| claude 供给单一 | LZ 全线下线（资源不足）；TR 是唯一渠道且 RPM 8-15/key（靠 30 key keypool 硬撑）；AWS 账号无 opus-4.7/4.8/sonnet-5 权限 |
| 容量冗余不足 | 高需求模型（claude、gpt-5.x、deepseek）实际上都压在单家渠道上 |
| 中国模型经多层经销 | qwen/glm/kimi 走的是 LZ/TR 转售，价格波动与下架风险不可控（09-27 误杀教训：上游 group 503 会放大成我们侧事故） |
| 运营质量 | LZ/TR 均有瞬时故障、目录漂移、计费与日志缺陷（见 LZ 反馈 MD） |

## 二、候选分层

### 第一梯队：建议立即小额试接

**1. OpenRouter（国际，付费档）** — 补 claude 供给 + 全模型国际冗余
- 付费模型**无平台硬限速**（RPM 随用量动态调整；免费档 20 RPM/50-1000 RPD 与我们无关）；claude/gpt/gemini/grok 全 lineup 含最新旗舰。
- 定价 = 官方原价 + ~5.5% 平台费，credits 永不过期；有标准化 `/models`（含 per-model endpoint 类型与 modality，正好喂我们的能力主数据体系）。
- **接入成本极低**：代码库已有 `openrouter_models` 表（385 行）+ `addFromOpenRouter` 导入通道（models_sync.go）；keypool 本就支持任意 OpenAI 兼容 provider（base_url + key + 定价规则即接入）。
- 风险：需国际支付；对我们 CN 主体有账务合规成本；数据经美区中转（企业客户如要求 claude 直连合规需评估）。

**2. 302.AI（国内主体，按量付费）** — claude 全家桶的国内备份渠道
- 500+ 模型（Claude/GPT/Gemini/Grok 最新版全 lineup）、OpenAI 兼容、**余额永久有效、无 TPM 限制**、按量计费无月费。
- 与 LZ/TR 同属中转站类别，但公开资料称企业级稳定；**定位：OpenRouter 的国内镜像备份**——两条 claude 渠道互为 failover。
- 风险：同中转站类（政策/资金链/线路质量），**预付费余额控制在小额**（≤$100）；模型命名需映射进我们的裸名规范。

### 第二梯队：结构性互补（建议下季度做）

**3. SiliconFlow 硅基流动** — 中国模型"去经销商化"
- 与 Qwen/DeepSeek/GLM 等厂商有官方合作的开源模型推理云，qwen/glm/deepseek 主力**价格与官方对齐、渠道稳定、CN 直连**；小模型有永久免费档。
- 价值：把 default-general 里最高频的中国模型（qwen3.x、glm-5.x）从 LZ/TR 转售迁到一手渠道，消除转售层的下架/价格波动风险。
- 不提供 claude/gpt 闭源模型——纯互补，不解决 claude。

**4. 官方直连（DashScope/BigModel.cn/Moonshot/DeepSeek）** — 终极稳定态
- 对用量大的单一厂商模型，官方直连是一手价格 + 官方 SLA。DeepSeek 已直连；qwen/glm 用量上来后可谈。
- 缺点：N 家厂商 N 套对接与对账；适合"单模型大流量"阶段再做。

### 第三梯队：仅观察，不进生产

- **Nous Portal**（订阅制 50-800 RPM，模式不契合按量转售）、**TeamoRouter/4SAPI/PoloAPI 等深折中转（官价 1-2 折）**：价格不可持续即有跑路/降质风险，与我们已踩过的中转站坑同类；如要试，仅限非关键模型且余额 ≤$20。

## 三、建议路线图

| 阶段 | 动作 | 预期收益 |
|---|---|---|
| 本周 | OpenRouter 充值 $50-100 起号 → 验证 claude RPM 实测 → 接为 claude 第三渠道（enterprise 组） | claude 供给冗余，摆脱 TR 单点 |
| 本周 | 302.AI 小额（$20）起号，claude 实测对比 OpenRouter | 国内 failover + 支付便利 |
| 观察 2 周 | 两家 claude 渠道计费/稳定性对账（复用 LZ 反馈 MD 的核对方法） | 决定主备 |
| 下季度 | SiliconFlow 接 qwen/glm 主力；评估是否替换 LZ/TR 的对应转售线路 | 中国模型去经销商化 |
| 持续 | TR/LZ 保持现状（目录宽度价值仍在），RPM 敏感模型逐步分流 | 容量池健康度 |

## 四、集成与风控清单（无论接哪家）

- keypool 接入 = providers 表加项 + upstream_keys 一条 + 定价规则（media/text）+ validate 缓存刷新，存量流程全部复用。
- **余额纪律**：中转站类预付费一律 ≤$100，跑通对账 2 周后再加。
- 接入后必做：probe 脚本实测 RPM/TPM 上限（写入手册数据）；模型命名裸名化映射；能力类型经 override 主数据校准。
- 合规提示：企业客户合同如含"数据不经第三方"条款，OpenRouter/302.AI 线路仅限 default/personal 层，enterprise 专属组可选 AWS 官方渠道。

## 补记：opencode 服务评估（2026-10-01，用户问讯后追加）

opencode（Anomaly，原 SST）有三样东西，作为上游的可行性截然不同：

| 服务 | 形态 | 结论 |
|---|---|---|
| OpenCode CLI | 免费开源（MIT）终端编码 agent | 与上游无关；但见下方反向价值 |
| **Zen** | 项目方自建**按量付费 API 网关**（per-token、官价 +4.4% + $0.30/笔、6 个免费模型、余额 <$5 自动充 $20） | 合法可用但模型池是精选子集、容量按交互式编码设计、无第三方转售 SLA；**列为第三梯队小额补充，不改变路线图** |
| **Go** | **$10/月订阅**（含 GLM-5.x/Kimi K2.x/MiMo/MiniMax M2.5 等编程模型，按模型用量窗口） | **不进生产**——订阅制不是 API 产品，逆向/转售属厂商重点打击对象 |

**关键风险背景**：2026-01 Anthropic 明确封禁 opencode 等第三方工具通过用户订阅调 claude（[编程中国报道](https://www.bccn.net/news/40982)），社区 `opencode-claude-auth`/`sub2api` 类逆向服务被批量杀号（[Fadouse 风控笔记](https://fadouse.github.io/)）。"编码订阅当 API 源"整类（opencode Go、Claude Pro/Max 逆向、ChatGPT Plus 逆向、Copilot auth）= 厂商 actively 追杀的地基，生产平台严禁接入。

**反向价值**：opencode 支持任意 OpenAI 兼容 provider——woodgit API 可被 opencode 用户直接配置使用（base_url → api.woodgit.com）。这是获客渠道而非上游渠道，值得做进开发者文档。

## 附：信息来源

- [OpenRouter 免费档限速说明（付费档无硬限速的口径参照）](https://klymentiev.com/blog/openrouter-free-tier)、[EvoLink: paid models 无 OpenRouter 强制限速](https://evolink.ai/blog/claude-code-with-openrouter-limits-errors-alternatives)、[TeamoRouter 对 OpenRouter 定价结构的拆解（官价+5.5%）](https://teamorouter.cn/zh/blogs/openrouter-tidai-fangan-teamorouter)
- [Ofox AI 企业级中转选型（302.AI/SiliconFlow 定位描述）](https://ofox.ai/zh/blog/ai-api-relay-station-comparison-guide-2026/)、[302.AI 计费模式（余额不过期、无 TPM）](https://n.mumingfang.com/archives/xo_link/302-ai)、[302.AI 评测（EggStriker）](https://www.eggstriker.com/ai-api/302ai)
- [SiliconFlow 模型中心](https://www.siliconflow.cn/models)、[SiliconFlow 2026 免费模型清单](https://yangmao.ai/zh/blog/siliconflow-free-models-list/)
- [TokenKarma：2026 各 API 隐藏限速实测](https://tokenkarma.app/blog/hidden-rate-limits-major-ai-apis/)
- [opencode 服务形态（Morph AI 对比 / Standard Compute 定价 / SaikoDev Zen 说明）](https://standardcompute.com/opencode-pricing)、[OpenCode Go 模型列表](https://www.chineseaitools.com/zh/products/opencode-go)
