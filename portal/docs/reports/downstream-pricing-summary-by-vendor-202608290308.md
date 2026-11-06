# WOODGIT 下游客户模型价格汇总

> **Status:** ACTIVE
> 生成时间：2026-08-30 15:11 UTC
> 说明：本表为面向下游客户的对外报价，按模型厂商（Vendor）归类，不含上游供应商信息。
> 文本模型价格单位为 **USD / 1M tokens**；图像/视频模型为 **按用量单价（USD）**。
> 每次请求另收固定请求费 **$0.005 / request**（所有模型统一）。
> **渠道**：`标准` 为通用渠道；`AWS` / `AWS Mantle` 为 AWS Bedrock 合规渠道，价格通常不同，故分行列出（同一模型不同渠道不合并）。
> 已剔除 `-thinking` 等模式后缀与区域前缀，仅做名称规范化（横杠版本号统一为点号）。
> 价格核查：已对照各厂商官网 / OpenRouter 校正。占位符价（$1/$3）已修复；huawei/laozhang 等渠道已对齐 tokenrouter 参考价；Claude/Qwen/Kimi/Grok 等真实模型已与官方价对齐。

- 条目总数：**140**（文本 129 · 图像/视频 11）
- 厂商数：**17**

---

## Anthropic

### 文本模型（USD / 1M tokens）

| 模型 | 渠道 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- | --- |
| claude-fable-5 | 标准 | $10 | $50 | $1 | $12.5 | $0 |
| claude-haiku-4.5 | 标准 | $1 | $5 | $0.08 | $0.8 | $1.2 |
| claude-haiku-4.5 | AWS | $0.8 | $4 | $0.08 | $0.8 | $1.2 |
| claude-opus-4.5 | 标准 | $5 | $25 | $0.5 | $6.25 | $0 |
| claude-opus-4.5 | AWS | $5 | $25 | $0.5 | $6.25 | $0 |
| claude-opus-4.6 | 标准 | $5 | $25 | $0.5 | $6.25 | $0 |
| claude-opus-4.6 | AWS | $5 | $25 | $0.5 | $6.25 | $0 |
| claude-opus-4.7 | 标准 | $5 | $25 | $0.5 | $6.25 | $0 |
| claude-opus-4.7 | AWS | $5 | $25 | $0.5 | $6.25 | $0 |
| claude-opus-4.7-fast | 标准 | $30 | $150 | $3 | $37.5 | $0 |
| claude-opus-4.8 | 标准 | $5 | $25 | $0.5 | $6.25 | $0 |
| claude-opus-4.8 | AWS | $5 | $25 | $0.5 | $6.25 | $0 |
| claude-opus-4.8-fast | 标准 | $10 | $50 | $1 | $12.5 | $0 |
| claude-opus-5 | 标准 | $5 | $25 | $0.5 | $6.25 | $0 |
| claude-opus-5-fast | 标准 | $10 | $50 | $1 | $12.5 | $0 |
| claude-sonnet-4 | 标准 | $3 | $15 | $0.3 | $3.75 | $0 |
| claude-sonnet-4.5 | 标准 | $3 | $15 | $0.3 | $3.75 | $0 |
| claude-sonnet-4.5 | AWS | $3 | $15 | $0.3 | $3 | $4.5 |
| claude-sonnet-4.6 | 标准 | $3 | $15 | $0.3 | $3 | $4.5 |
| claude-sonnet-4.6 | AWS | $3 | $15 | $0.3 | $3 | $4.5 |
| claude-sonnet-5 | 标准 | $2 | $10 | $0.2 | $2.5 | $0 |

---

## OpenAI

### 文本模型（USD / 1M tokens）

| 模型 | 渠道 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- | --- |
| gpt-4o-mini | 标准 | $0.15 | $0.6 | $0.075 | $0 | $0 |
| gpt-5 | 标准 | $1.25 | $10 | $0.125 | $0 | $0 |
| gpt-5-image | 标准 | $10 | $40 | $2.5 | $0 | $0 |
| gpt-5-image-mini | 标准 | $2.5 | $8 | $0.25 | $0 | $0 |
| gpt-5-mini | 标准 | $0.25 | $2 | $0.025 | $0 | $0 |
| gpt-5.2 | 标准 | $1.75 | $14 | $0.175 | $0 | $0 |
| gpt-5.3-codex | 标准 | $1.75 | $14 | $0.175 | $0 | $0 |
| gpt-5.4 | 标准 | $2.5 | $15 | $0.25 | $0 | $0 |
| gpt-5.4-image-2 | 标准 | $8 | $30 | $2 | $0 | $0 |
| gpt-5.4-mini | 标准 | $0.75 | $4.5 | $0.075 | $0 | $0 |
| gpt-5.4-nano | 标准 | $0.2 | $1.25 | $0.02 | $0 | $0 |
| gpt-5.4-pro | 标准 | $30 | $180 | $3 | $30 | $30 |
| gpt-5.5 | 标准 | $5 | $30 | $0.5 | $0 | $0 |
| gpt-5.5-pro | 标准 | $30 | $180 | $0 | $0 | $0 |
| gpt-5.6-luna | 标准 | $0.2 | $1.2 | $0.02 | $0.25 | $0 |
| gpt-5.6-sol | 标准 | $5 | $30 | $0.5 | $6.25 | $0 |
| gpt-5.6-terra | 标准 | $2 | $12 | $0.2 | $2.5 | $0 |
| gpt-audio | 标准 | $2.5 | $10 | $0 | $0 | $0 |
| gpt-audio-mini | 标准 | $0.6 | $2.4 | $0 | $0 | $0 |
| gpt-oss-120b | 标准 | $0.039 | $0.18 | $0 | $0 | $0 |
| gpt-oss-120b | AWS Mantle | $0.03 | $0.17 | $0.03 | $0 | $0 |
| gpt-oss-20b | AWS Mantle | $0.05 | $0.2 | $0.05 | $0 | $0 |
| gpt-oss-safeguard-120b | AWS Mantle | $0.03 | $0.17 | $0.03 | $0 | $0 |
| gpt-oss-safeguard-20b | AWS Mantle | $0.05 | $0.2 | $0.05 | $0 | $0 |

---

## Google

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| gemini-2.5-flash-image | $0.3 | $2.5 | $0 | $0 | $0 |
| gemini-3-flash-preview | $0.5 | $3 | $0.05 | $0.0833 | $0 |
| gemini-3-pro-image-preview | $2 | $12 | $0 | $0 | $0 |
| gemini-3.1-flash-image-preview | $0.5 | $3 | $0 | $0 | $0 |
| gemini-3.1-flash-lite-image | $0.25 | $1.5 | $0 | $0 | $0 |
| gemini-3.1-pro-preview | $2 | $12 | $0.2 | $0.375 | $0 |
| gemini-3.5-flash | $1.5 | $9 | $0.15 | $0.0833 | $0 |
| gemini-3.5-flash-lite | $0.3 | $2.5 | $0.03 | $0.0833 | $0 |
| gemini-3.6-flash | $1.5 | $7.5 | $0.15 | $0.0833 | $0 |
| gemini-3.7-flash | $0.75 | $3.75 | $0.075 | $0.0417 | $0 |
| gemma-4.26b-a4b-it | $0.06 | $0.33 | $0 | $0 | $0 |

---

## Mistral AI

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| devstral-2512 | $0.4 | $2 | $0.04 | $0 | $0 |
| mistral-medium-3.5 | $1.5 | $7.5 | $0 | $0 | $0 |
| mistral-small-2603 | $0.15 | $0.6 | $0.015 | $0 | $0 |
| voxtral-small-24b-2507 | $0.1 | $0.3 | $0.01 | $0 | $0 |

---

## xAI (Grok)

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| grok-4.1-fast | $0.2 | $0.5 | $0.05 | $0 | $0 |
| grok-4.20-beta | $2 | $6 | $0.2 | $0 | $0 |
| grok-4.3 | $1.25 | $2.5 | $0.2 | $0 | $0 |
| grok-4.5 | $2 | $6 | $0.5 | $0 | $0 |
| grok-4.6 | $2 | $6 | $0.5 | $0 | $0 |
| grok-build-0.1 | $1 | $2 | $0.2 | $0 | $0 |

---

## Microsoft (Phi)

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| mai-image-2.5 | $5 | $47 | $0 | $0 | $0 |

---

## NVIDIA

### 文本模型（USD / 1M tokens）

| 模型 | 渠道 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- | --- |
| nemotron-3-nano-omni-30b-a3b-reasoning:free | 标准 | $0 | $0 | $0 | $0 | $0 |
| nemotron-3-super-120b-a12b | 标准 | $0.3 | $0.9 | $0.1 | $0 | $0 |
| nemotron-3.5-lightning | 标准 | $0.1 | $0.25 | $0.05 | $0 | $0 |
| nemotron-nano-12b | AWS Mantle | $0.08 | $0.2 | $0.04 | $0 | $0 |
| nemotron-nano-30b | AWS Mantle | $0.08 | $0.2 | $0.04 | $0 | $0 |
| nemotron-nano-9b | AWS Mantle | $0.08 | $0.2 | $0.04 | $0 | $0 |
| nemotron-super-120b | AWS Mantle | $0.09 | $0.45 | $0.045 | $0 | $0 |

---

## DeepSeek

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| deepseek-v3.2 | $0.26 | $0.38 | $0.13 | $0 | $0 |
| deepseek-v4-flash | $0.14 | $0.28 | $0.0028 | $0 | $0 |
| deepseek-v4-flash-0731 | $0.14 | $0.28 | $0.0028 | $0 | $0 |
| deepseek-v4-pro | $0.435 | $0.87 | $0.0036 | $0 | $0 |
| deepseek-v4-pro-0813 | $0.2175 | $0.435 | $0.0018 | $0 | $0 |
| deepseek-v4-pro-0813-free | $0 | $0 | $0 | $0 | $0 |

---

## Alibaba (Qwen)

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| qwen3-coder-next | $0.12 | $0.75 | $0.06 | $0 | $0 |
| qwen3.5-flash | $0.1048 | $0.4194 | $0 | $0 | $0 |
| qwen3.5-omni-plus | $1 | $5.7143 | $0 | $0 | $0 |
| qwen3.5-plus-02.15 | $0.26 | $1.56 | $0 | $0 | $0 |
| qwen3.5.122b-a10b | $0.26 | $2.08 | $0 | $0 | $0 |
| qwen3.5.35b-a3b | $0.1625 | $1.3 | $0 | $0 | $0 |
| qwen3.5.397b-a17b | $0.39 | $2.34 | $0 | $0 | $0 |
| qwen3.5.9b | $0.1 | $0.15 | $0 | $0 | $0 |
| qwen3.6-flash | $0.171 | $1.029 | $0.017 | $0.214 | $0 |
| qwen3.6-plus | $0.54 | $3.21 | $0 | $0 | $0 |
| qwen3.7-max | $1.25 | $3.75 | $0.25 | $0 | $0 |
| qwen3.7-plus | $0.4 | $1.6 | $0.08 | $0 | $0 |
| qwen3.8-max | $2 | $6 | $0.25 | $2.5 | $0 |
| qwen3.8-max-free | $0 | $0 | $0 | $0 | $0 |

---

## Zhipu AI (GLM)

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| glm-4.5-air | $0.13 | $0.85 | $0.025 | $0 | $0 |
| glm-4.6 | $0.6 | $2.2 | $0.11 | $0 | $0 |
| glm-4.6v | $0.3 | $0.9 | $0 | $0 | $0 |
| glm-4.7 | $0.6 | $2.2 | $0.11 | $0 | $0 |
| glm-5 | $1 | $3.2 | $0.2 | $0 | $0 |
| glm-5-turbo | $1.2 | $4 | $0.24 | $0 | $0 |
| glm-5.1 | $1.05 | $3.5 | $0.525 | $0 | $0 |
| glm-5.2 | $1.4 | $4.4 | $0.26 | $0 | $0 |

---

## Moonshot AI (Kimi)

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| kimi-k2.5 | $0.6 | $3 | $0.1 | $0 | $0 |
| kimi-k2.6 | $0.95 | $4 | $0.16 | $0 | $0 |
| kimi-k2.7-code | $0.9286 | $3.8571 | $0.1857 | $0 | $0 |
| kimi-k3 | $3 | $15 | $0.3 | $0 | $0 |

---

## MiniMax

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| minimax-m2-her | $0.3 | $1.2 | $0.03 | $0 | $0 |
| minimax-m2.1 | $0.3 | $1.2 | $0.03 | $0 | $0 |
| minimax-m2.1-highspeed | $0.6 | $2.4 | $0.06 | $0 | $0 |
| minimax-m2.5 | $0.3 | $1.2 | $0.03 | $0 | $0 |
| minimax-m2.7 | $0.3 | $1.2 | $0.06 | $0 | $0 |
| minimax-m2.7-highspeed | $0.6 | $2.4 | $0.06 | $0 | $0 |
| minimax-m3 | $0.3 | $1.2 | $0.06 | $0 | $0 |

### 图像 / 视频模型（按用量单价）

| 模型 | 计费单位 | 规格 | 单价 (USD) |
| --- | --- | --- | --- |
| MiniMax-H3 | second | 2k | $0.13 |
|  | second | 768P | $0.09 |
|  | image | Input More than five images | $0.04 |
| minimax-hailuo-2.3 | block | 1080P 6s | $0.49 |
|  | block | 768P 10s | $0.56 |
|  | block | 768P 6s | $0.28 |

---

## Xiaomi (MiMo)

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| mimo-v2-flash | $0.1 | $0.3 | $0.01 | $0 | $0 |
| mimo-v2-omni | $0.4 | $2 | $0.08 | $0 | $0 |
| mimo-v2-pro | $1 | $3 | $0.2 | $0 | $0 |
| mimo-v2.5 | $0.4 | $2 | $0.08 | $0 | $0 |
| mimo-v2.5-pro | $1 | $3 | $0.2 | $0 | $0 |

---

## StepFun

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| step-3.5-flash | $0.1 | $0.3 | $0.02 | $0 | $0 |
| step-3.7-flash | $0.2 | $1.15 | $0.04 | $0 | $0 |

---

## ByteDance (Seedream)

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| seed-2.0-code-preview-260328 | $1 | $6 | $0.2 | $0.0083 | $0 |
| seed-2.0-lite-260428 | $0.5 | $4 | $0.1 | $0.0083 | $0 |
| seed-2.0-mini-260428 | $0.2 | $0.8 | $0.04 | $0.0083 | $0 |
| seed-2.0-pro-260328 | $1 | $6 | $0.2 | $0.0083 | $0 |

### 图像 / 视频模型（按用量单价）

| 模型 | 计费单位 | 规格 | 单价 (USD) |
| --- | --- | --- | --- |
| seedream-4.5 | image | Model price | $0.04 |
| seedream-5.0-lite | image | Model price | $0.035 |
| seedream-5.0-pro | image | Input More than one image | $0.003 |
|  | image | Output Pixels <= 236W | $0.045 |
|  | image | Output Pixels > 236W | $0.09 |

---

## Kuaishou (Kling)

### 图像 / 视频模型（按用量单价）

| 模型 | 计费单位 | 规格 | 单价 (USD) |
| --- | --- | --- | --- |
| kling-3.0-turbo | second | 1080P | $0.14 |
|  | second | 720P | $0.112 |
| kling-v2.6 | block | pro x 10s x audio x noVoice | $1.4 |
|  | block | pro x 10s x audio x voice | $1.68 |
|  | block | pro x 10s x noAudio x noVoice | $0.7 |
|  | block | pro x 10s x noAudio x voice | $0.7 |
|  | block | pro x 5s x audio x noVoice | $0.7 |
|  | block | pro x 5s x audio x voice | $0.84 |
|  | block | pro x 5s x noAudio x noVoice | $0.35 |
|  | block | pro x 5s x noAudio x voice | $0.35 |
|  | block | std x 10s x noAudio x noVoice | $0.42 |
|  | block | std x 10s x noAudio x voice | $0.42 |
|  | block | std x 5s x noAudio x noVoice | $0.21 |
|  | block | std x 5s x noAudio x voice | $0.21 |
| kling-v3 | second | 4k x audio x noVoice | $0.42 |
|  | second | 4k x noAudio x noVoice | $0.42 |
|  | second | pro x audio x noVoice | $0.168 |
|  | second | pro x noAudio x noVoice | $0.112 |
|  | second | pro x noAudio x voice | $0.112 |
|  | second | std x audio x noVoice | $0.126 |
|  | second | std x noAudio x noVoice | $0.084 |
|  | second | std x noAudio x voice | $0.084 |
| kling-v3-omni | second | 4k x audio x noVideo | $0.42 |
|  | second | 4k x noAudio x noVideo | $0.42 |
|  | second | 4k x noAudio x video | $0.42 |
|  | second | pro x audio x noVideo | $0.14 |
|  | second | pro x noAudio x noVideo | $0.112 |
|  | second | pro x noAudio x video | $0.168 |
|  | second | std x audio x noVideo | $0.112 |
|  | second | std x noAudio x noVideo | $0.084 |
|  | second | std x noAudio x video | $0.126 |

---

## Other

### 文本模型（USD / 1M tokens）

| 模型 | 输入 | 输出 | 缓存读取 | 缓存写入 | 推理 |
| --- | --- | --- | --- | --- | --- |
| fugu-ultra | $5 | $30 | $0.5 | $0 | $0 |
| hy3-preview | $0.066 | $0.26 | $0.029 | $0 | $0 |
| mirothinker-1.7-deepresearch | $4 | $25 | $0 | $0 | $0 |
| mirothinker-1.7-deepresearch-mini | $1.25 | $10 | $0 | $0 | $0 |
| openpangu-2.0-flash | $0.15 | $0.6 | $0.015 | $0 | $0 |

### 图像 / 视频模型（按用量单价）

| 模型 | 计费单位 | 规格 | 单价 (USD) |
| --- | --- | --- | --- |
| happyhorse-1.0-i2v | second | 1080P | $0.24 |
|  | second | 720P | $0.14 |
| happyhorse-1.0-t2v | second | 1080P | $0.24 |
|  | second | 720P | $0.14 |

---
