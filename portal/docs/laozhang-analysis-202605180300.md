# LaoZhang.ai API 中转平台分析

**日期**: 2026-05-18
**状态**: ACTIVE

## 概述

LaoZhang.ai（老张API）是一个基于 [new-api/one-api](https://github.com/songquanpeng/one-api) 开源项目的 AI API 中转平台，面向企业用户和开发者，提供统一的 OpenAI 兼容 API 接口，聚合 200+ AI 模型。

- **网站**: https://www.laozhang.ai
- **API 端点**: https://api.laozhang.ai/v1
- **文档**: https://docs.laozhang.ai
- **不支持中国大陆个人用户**，仅接受 Gmail 注册

---

## 1. 分组体系（Group System）

### 核心概念

LaoZhang 使用 **Group（分组）** 作为计费和路由的核心维度。每个 Token（API Key）属于一个 Group，Group 决定了：

1. **可用模型范围** — 每个 Group 关联一组可用模型
2. **价格倍率（GroupRatio）** — 同一模型在不同 Group 下价格不同
3. **计费类型（BillingType）** — 按量计费 vs 按次计费

### 当前 Group 列表与价格倍率

| Group | GroupRatio | 说明 |
|-------|-----------|------|
| `default` | 1.0 | 默认分组，标准价格 |
| `AlibabaCloud` | 0.88 | 阿里云分组，12% 折扣 |
| `claude_code` | 1.0 | Claude Code 专用分组 |
| `sora_official` | 1.0 | Sora 官方分组 |
| `GPT_Image_2_Enterprise` | 1.2 | GPT-Image-2 企业分组，20% 加价 |
| `Nano_Banana_Enterprise` | 1.4 | Nano Banana 企业分组，40% 加价 |

### Token 的 Group 与 Fallback Group

Token 可以设置：
- **group**: 主分组（如 `AlibabaCloud`）
- **fallback_groups**: 备选分组（如 `default`），当主分组无可用渠道时回退

示例：测试用户的 Token 设置为 `group=AlibabaCloud, fallback_groups=default`，意味着优先走阿里云渠道（0.88 倍率），无可用渠道时回退到 default（1.0 倍率）。

### ModelGroupBillingTypes

每个模型可以指定在不同 Group 下的计费类型：

```json
{
  "DeepSeek-V3.2-Exp-nothinking": {"AlibabaCloud": [1], "default": [1]},
  "claude-haiku-4-5-20251001": {"claude_code": [1], "default": [1]},
  "MiniMax-M2.5-lightning": {"default": [1]}
}
```

- `[1]` 表示按量计费（per-token）
- 模型可以只在特定 Group 下可用（如 MiniMax-M2.5-lightning 仅在 default 下）

---

## 2. 计费模型（Billing Model）

### 双计费模式

LaoZhang 支持两种计费方式，**按次计费优先**：

| 计费方式 | 适用场景 | 说明 |
|---------|---------|------|
| **按量计费（per-token）** | 文本/对话模型 | 按 input/output token 数量计费 |
| **按次计费（per-call）** | 图像/视频/特殊模型 | 每次调用固定价格 |

当模型同时支持两种计费时，默认按次计费。但如果 Token 设置为"仅按量计费"，则强制按 token 计费。

### 按量计费公式

```
费用 = (input_tokens × ModelRatio × GroupRatio) + (output_tokens × ModelRatio × CompletionRatio × GroupRatio)
```

其中：
- **ModelRatio**: 模型基础价格倍率（相对于 GPT-3.5-Turbo = 0.25）
- **CompletionRatio**: 输出 token 相对输入 token 的价格倍率（通常 3-5 倍）
- **GroupRatio**: 分组价格倍率

### 按次计费（ModelFixedPrice）

非 token 计费的模型使用固定价格，按调用次数收费：

| 类别 | 代表模型 | 价格范围（USD/次） |
|------|---------|-----------------|
| 图像生成 | gpt-4o-image, flux-kontext-pro | $0.01 - $0.09 |
| 视频生成 | kling-v2-6, veo-3.1, sora-2 | $0.15 - $12.5 |
| Midjourney | mj_imagine, mj_upscale | $0.002 - $0.1 |
| 音频 | audio1.0 | $0.625 |
| 特殊功能 | claude-*-all, gpt-*-all | $0.01 - $0.1 |

### 条件计费（ModelConditionalPricing）

部分模型根据 token 用量分档计费：

```json
{
  "anthropic/claude-opus-4": {
    "Conditions": [
      {"MinTokens": 0, "MaxTokens": 204800, "InputRatio": 7.5, "CompletionRatio": 5},
      {"MinTokens": 204801, "MaxTokens": -1, "InputRatio": 15, "CompletionRatio": 3.75}
    ]
  }
}
```

- 0-204K tokens: InputRatio=7.5, CompletionRatio=5
- 204K+ tokens: InputRatio=15, CompletionRatio=3.75（长上下文输入更贵，输出更便宜）

当前有 35 个模型配置了条件计费，主要是 Claude 和 GPT 系列的高端模型。

### Quota 系统

- 用户有 `quota`（总配额）和 `used_quota`（已用配额）
- Token 可以设置 `unlimited_quota: true`（无限配额）
- 配额单位为内部单位，1 quota ≈ $0.000001（需确认）
- `TopupGroupMinLimit: 15` — 充值最低限额
- `InviteBonusRatio: 0.05` — 邀请奖励比例 5%

---

## 3. 模型管理

### 模型规模

- **总模型数**: 7022 个（含渠道/版本变体）
- **唯一模型名**: ~608 个
- **有定价的模型**: 1122 个（ModelRatio）
- **固定价格模型**: 638 个（ModelFixedPrice）
- **有计费信息的模型**: 301 个（ModelBillingInfo）

### 模型分类

| 类别 | 说明 | 代表模型 |
|------|------|---------|
| Chat | 对话/文本生成 | gpt-4o, claude-sonnet-4, deepseek-v3, qwen-max |
| Reasoning | 推理/思考 | o3, deepseek-r1, qwq-plus, grok-4 |
| Coding | 代码生成 | claude-sonnet-4, gpt-4.1, deepseek-coder |
| Image | 图像生成/编辑 | gpt-image-2, flux-2-pro, ideogram-v2, midjourney |
| Video | 视频生成 | kling-v2-6, veo-3.1, sora-2, runway, vidu |
| Vision | 图像理解/OCR | gemini-2.5-flash, qwen-vl-max, doubao-vision |
| Audio | 语音/转录 | gpt-4o-tts, whisper-1, gemini-tts |

### 模型命名规则

LaoZhang 支持多种模型命名格式：

| 格式 | 示例 | 来源 |
|------|------|------|
| 标准名 | `gpt-4o`, `claude-sonnet-4` | OpenAI/Anthropic 官方 |
| 带日期 | `gpt-4o-2024-11-20`, `claude-3-5-sonnet-20241022` | 版本快照 |
| 带后缀 | `gpt-4o-all`, `claude-*-thinking` | 特殊功能（全模态/思考模式） |
| 带路径 | `deepseek-ai/DeepSeek-V3`, `meta/llama-2-70b` | 开源模型 |
| 带参数 | `kling-v2-6/pro/10/audio/voice` | 视频/图像参数变体 |
| Cloudflare | `@cf/meta/llama-3-8b-instruct` | Cloudflare Workers AI |
| HuggingFace | `@hf/mistralai/mistral-7b-instruct-v0.2` | HuggingFace 推理 |

### CompletionRatio 分布

输出 token 相对输入 token 的价格倍率：

| CompletionRatio | 适用模型 |
|----------------|---------|
| 1.5 | DeepSeek-V3.2, deepseek-v3.2 |
| 2 | command, gpt-3.5-turbo (旧版) |
| 3 | chatgpt-4o-latest, claude-2, codestral, mistral |
| 4 | MiniMax-M2.x, gemini-1.5, deepseek-r1 |
| 5 | Claude 3/4 全系列, GPT-4/5 全系列 |
| 8 | MiniMax-M2.5-lightning, doubao-seed |
| 10+ | doubao-seed, qwen3, seed 系列 |

---

## 4. 用户与 Token 管理

### 用户层级

| 字段 | 值 | 说明 |
|------|---|------|
| `role` | 1 | 普通用户（非管理员） |
| `user_type` | 5 | 用户类型 |
| `group` | "default" | 用户默认分组 |
| `status` | 1 | 正常状态 |

### Token（API Key）属性

| 字段 | 示例值 | 说明 |
|------|--------|------|
| `key` | ryYVKsw3...9d27 | API Key（前缀+随机） |
| `name` | "20260418" | 用户自定义名称 |
| `group` | "AlibabaCloud" | 主分组 |
| `fallback_groups` | "default" | 备选分组 |
| `billing_type` | 5 | 计费类型 |
| `unlimited_quota` | true | 无限配额 |
| `models` | "" | 限定模型（空=全部） |
| `subnet` | "" | 子网限制 |
| `ip_whitelist` | "" | IP 白名单 |
| `expired_time` | 1776707713 | 过期时间（Unix 时间戳） |
| `is_initial_token` | true | 初始令牌 |

---

## 5. UI/UX 特点

### 点击复制（Click-to-Copy）

LaoZhang 的管理面板（基于 new-api/one-api 前端）在多处实现了点击复制功能：

- **模型名称** — 在模型列表页，点击模型名即可复制到剪贴板
- **API Key** — Token 列表页，Key 值可点击复制
- **API 端点** — 文档页提供可复制的端点 URL

这对于有 7000+ 模型的平台非常实用，用户无需手动选择和拷贝长模型名。

### 模型列表展示

- 模型按渠道（Channel）分组，每个渠道关联一组模型
- 管理面板显示模型名、所属渠道、状态
- 定价页按类别分组展示（Chat/Reasoning/Coding/Image/Video/Vision/Audio）

### 文档站

文档站（docs.laozhang.ai）提供：
- 按类别的模型定价表（含 Input/Output 价格）
- 成本估算示例
- 集成指南（Cursor, Claude Code, Cherry Studio 等）
- 企业服务说明

---

## 6. 与 Portal 的对比与借鉴

### LaoZhang 的优势

| 特性 | LaoZhang | Portal 现状 |
|------|----------|------------|
| 分组计费 | Group + GroupRatio + Fallback Group | 无分组概念 |
| 条件计费 | 按 token 量分档 | 统一倍率 |
| 双计费模式 | 按量/按次自动切换 | 仅按量 |
| 模型数量 | 7000+ | ~100 |
| 点击复制 | 模型名/Key/端点均可复制 | 无 |
| 固定价格 | 638 个非 token 模型 | 无 |

### 可借鉴的设计

1. **Group + Fallback Group 机制**
   - Token 绑定主分组 + 备选分组
   - 不同分组不同价格倍率（如企业折扣、VIP 加价）
   - 主分组无可用渠道时自动回退

2. **双计费模式**
   - 图像/视频模型按次计费（ModelFixedPrice）
   - 文本模型按量计费（ModelRatio + CompletionRatio）
   - Token 级别可强制指定计费方式

3. **条件计费（ConditionalPricing）**
   - 长上下文调用分档定价
   - 鼓励短上下文使用，长上下文加价

4. **点击复制 UX**
   - 模型名、API Key、端点 URL 均可点击复制
   - 对大量模型的平台尤其重要

5. **模型命名兼容**
   - 支持多种命名格式（标准/路径/参数变体）
   - `-all` 后缀表示全模态（含图像/文件）
   - `-thinking` 后缀表示思考模式

---

## 7. 技术架构

LaoZhang 基于 [new-api](https://github.com/songquanpeng/one-api)（one-api 的增强分支），核心架构：

```
用户请求 → LaoZhang Gateway → 路由（Group + Model → Channel）→ 上游 API
                ↓
           计费（Quota 扣减）
                ↓
           日志记录
```

### 关键数据结构

- **User**: id, username, group, quota, used_quota, role, user_type
- **Token**: key, group, fallback_groups, billing_type, unlimited_quota, models, expired_time
- **Channel**: id, type, key, base_url, models, group, priority, weight
- **ModelRatio**: model_name → price_multiplier
- **CompletionRatio**: model_name → output/input_ratio
- **GroupRatio**: group_name → price_multiplier
- **ModelFixedPrice**: model_name → fixed_price_per_call
- **ModelConditionalPricing**: model_name → conditions[{MinTokens, MaxTokens, InputRatio, CompletionRatio}]
- **ModelGroupBillingTypes**: model_name → {group → [billing_type]}

### API 端点

| 端点 | 说明 |
|------|------|
| `/api/user/self` | 当前用户信息（含 ModelFixedPrice, TopupGroupMinLimit） |
| `/api/token/` | Token 列表管理 |
| `/api/models` | 模型列表（按渠道分组） |
| `/api/pricing` | 完整定价信息（所有 Ratio、FixedPrice、ConditionalPricing） |
| `/api/log/self/` | 用户请求日志 |

---

## 8. 定价参考（部分模型）

### 文本模型（按量计费，USD/1M tokens）

| 模型 | Input | Output | CompletionRatio |
|------|-------|--------|----------------|
| gpt-4.1-mini | $0.15 | $0.60 | 4 |
| gpt-4.1 | $2.50 | $10.00 | 4 |
| gpt-4o | $1.25 | $5.00 | 4 |
| gpt-5 | $0.625 | $3.13 | 5 |
| claude-sonnet-4 | $1.50 | $7.50 | 5 |
| claude-opus-4 | $7.50 | $37.50 | 5 |
| claude-haiku-4-5 | $0.50 | $2.50 | 5 |
| deepseek-v3 | $0.14 | $0.56 | 4 |
| deepseek-r1 | $0.29 | $1.14 | 4 |
| gemini-2.5-flash | $0.15 | $0.60 | 4 |
| gemini-2.5-pro | $0.625 | $3.13 | 5 |
| qwen-max | $0.80 | $3.20 | 4 |
| qwen-plus | $0.20 | $0.80 | 4 |

### 图像模型（按次计费，USD/次）

| 模型 | 价格 |
|------|------|
| gpt-4o-image | $0.01 |
| gpt-image-2 | $0.03 |
| flux-kontext-pro | $0.035 |
| dall-e-3 | $0.04 |
| ideogram-v2 | $0.17 |
| midjourney (imagine) | $0.10 |

### 视频模型（按次计费，USD/次）

| 模型 | 价格 |
|------|------|
| kling-v2-6 (std/5s) | $0.75 |
| kling-v2-6 (pro/5s) | $1.25 |
| veo-3.1 | $0.25 |
| veo-3.1-fast | $0.15 |
| sora-2 | $0.15 |
| sora-2-pro | $0.80 |

> 注：以上价格为 `default` 分组（GroupRatio=1.0）下的价格。`AlibabaCloud` 分组享受 0.88 倍率折扣。
