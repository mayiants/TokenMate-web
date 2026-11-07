# 子处理者清单(Sub-processor List)

Status: DRAFT — 需逐家核实后才能对外发布
Date: 2026-11-06
Source: 生产库 `providers` 表(只读导出,2026-11-06)
用途:DPA 附件 C;对外发布为"信任中心/子处理者页面"的底稿。

> 说明:下表是当前平台**已配置**的上游供应商。某个企业客户实际触达哪些,取决于其可用的模型与路由。**"数据处理地区"与"训练政策"两列必须逐家按各自最新条款核实后再对外承诺**;当前标 `TODO` 的单元格不得写入对客户的 DPA。

## A. 直接模型供应商

| 供应商 | 平台内名称 | 角色 | 处理的数据 | 处理地区 | 训练政策(需核实) |
|---|---|---|---|---|---|
| Amazon Web Services | AWS Bedrock(46 条,覆盖 af/ap/ca/eu/il/me/mx/sa/us 多区域)、AWS Mantle、Amazon | 子处理者 + 基础设施 | 转发的 prompt/response;平台自身运行于 AWS(EC2/EBS,ap-southeast-1) | 按所选 Bedrock 区域 | Bedrock 默认不用于训练(TODO 附条款链接) |
| OpenAI | OpenAI | 子处理者 | prompt/response | 美国(TODO) | API 默认不训练(TODO) |
| Anthropic | Anthropic | 子处理者 | prompt/response | 美国(TODO) | API 默认不训练(TODO) |
| Google | Google | 子处理者 | prompt/response | TODO | TODO |
| Microsoft | Microsoft | 子处理者 | prompt/response | TODO | TODO |
| Mistral | Mistral | 子处理者 | prompt/response | 欧盟(TODO) | TODO |
| Cohere | Cohere | 子处理者 | prompt/response | TODO | TODO |
| Groq | Groq | 子处理者 | prompt/response | 美国(TODO) | TODO |
| Perplexity | Perplexity | 子处理者 | prompt/response | TODO | TODO |
| xAI | xAI | 子处理者 | prompt/response | 美国(TODO) | TODO |
| Meta | Meta | 子处理者 | prompt/response | TODO | TODO |
| DeepSeek | DeepSeek | 子处理者 | prompt/response | 中国(TODO) | TODO |
| Moonshot(kimi) | Moonshot | 子处理者 | prompt/response | 中国(TODO) | TODO |
| Zhipu(GLM) | Zhipu | 子处理者 | prompt/response | 中国(TODO) | TODO |
| MiniMax | MiniMax | 子处理者 | prompt/response | 中国(TODO) | TODO |
| ByteDance | ByteDance | 子处理者 | prompt/response | 中国(TODO) | TODO |
| Alibaba(通义) | Alibaba | 子处理者 | prompt/response | 中国(TODO) | TODO |
| Tencent(混元) | Tencent | 子处理者 | prompt/response | 中国(TODO) | TODO |
| Baidu(文心) | Baidu | 子处理者 | prompt/response | 中国(TODO) | TODO |
| Huawei(盘古) | Huawei | 子处理者 | prompt/response | 中国(TODO) | TODO |
| Xiaomi | Xiaomi | 子处理者 | prompt/response | 中国(TODO) | TODO |

## B. 聚合/中转供应商(本身再转发给下游,需特别披露)

| 供应商 | 平台内名称 | 说明 |
|---|---|---|
| OpenRouter | Openrouter | 聚合网关,下游为多家模型商;对最终数据流向的披露依赖其清单(TODO) |
| TokenRouter | Tokenrouter | 聚合/路由(内部使用);下游 TODO |
| Approaching(approach) | Approaching | 中转网关(`www.tokenservices.store`);下游 TODO。注:已知其网关对 ≥8k 上下文有约 60s 超时 |
| Laozhang | Laozhang | 中转(TODO) |
| Zimao | Zimao | 中转(TODO) |

**中转类供应商对合规是较大风险点**:数据会再流向不透明的下游。建议对企业客户默认**不启用**中转类通道,或在 DPA 中单列并取得客户额外同意。

## C. 平台自身基础设施子处理者

| 供应商 | 用途 | 数据 | 核实项 |
|---|---|---|---|
| AWS(EC2/EBS,ap-southeast-1) | 运行 Portal 与 Proxy、块存储 | 全部(含 PII、计费、审计、留存) | EBS 加密、快照策略 TODO |
| AWS S3(规划中) | 加密异地备份目的地 | 数据库备份(含 PII) | 尚未启用,见合规评估 §4 #1 |
| 支付渠道:Stripe / PayPal / 支付宝 / 微信 | 支付处理 | 账单、支付标识 | 是否实际开通、各自 DPA TODO(`pkg/payment`) |
| 邮件(SMTP 供应商) | 验证码、通知、发票投递 | 邮箱、通知内容 | 供应商与 DPA TODO |

## 变更记录
- 2026-11-06:首版,依据 `providers` 表导出。所有"地区/训练政策/下游"为 TODO,须逐家核实。
