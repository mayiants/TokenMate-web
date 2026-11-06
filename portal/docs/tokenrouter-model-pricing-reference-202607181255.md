# TokenRouter 模型与价格参考

- 来源：www.tokenrouter.com
- 抓取时间：UTC 2026-07-18 12:55
- 模型总数：113
- 计费方式：{'Pay as You Go': 104, 'Pay Per Request': 9}
- 模型类型：{'Text': 98, 'Image': 8, 'Video': 7}
- 含阶梯价模型：24
- 含推广标签模型：2

## 1. 平台概述

TokenRouter 是一个统一的 AI 模型聚合与分发平台，特点包括：
- 将多家主流 LLM/VLM/视频/音频模型转换为 OpenAI 兼容的 `/v1/chat/completions` API。
- 提供两种计费模式：按 Token 用量计费（Pay as You Go）和按请求计费（Pay Per Request）。
- 部分模型支持上下文长度阶梯价（如 ≤128K / ≤200K / ≤256K / ≤272K 等）。
- 部分模型有推广标签，如 `Free`、`Test`、`HOT`、`60% off`。
- 登录后可见现金余额、代金券余额、用量统计、API Keys、Management Keys、Usage Logs 等。

## 2. 计费模式说明

| 计费模式 | 说明 |
| --- | --- |
| Pay as You Go | 按 Token 用量计费，常见单位为 `/M tokens`（每百万 Token）。 |
| Pay Per Request | 按请求次数计费，常见单位为 `/image`、`/s` 等。 |

## 3. 价格组成字段

| 字段 | 含义 |
| --- | --- |
| Input Price | 输入 Token 单价 |
| Output Price | 输出 Token 单价 |
| Cache Read | 缓存命中读取单价 |
| Cache Write | 缓存写入单价 |
| Audio Input | 音频输入单价 |
| Audio Completion Price | 音频生成完成单价 |
| Input Audio Cache | 音频缓存单价 |
| Image Output Price | 图片输出单价 |
| Image Input Price | 图片输入单价 |
| Model Price | 单张/单次固定价格 |

## 4. 推广与优惠

| 模型 | 标签 | 说明 |
| --- | --- | --- |
| z-ai/glm-5.2-free | Free | 免费额度，但稳定性与并发不保证 |
| z-ai/glm-5.2 | 60% off | 标注原价与 4 折现价 |

## 5. 阶梯价示例

部分模型根据上下文长度设置不同单价（以下为节选）：

### openai/gpt-5.6-luna

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤272K $1.000; >272K $2.000 /M tokens |
| Output Price | ≤272K $6.000; >272K $9.000 /M tokens |
| Cache Read | ≤272K $0.100; >272K $0.200 /M tokens |

### openai/gpt-5.6-terra

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤272K $2.500; >272K $5.000 /M tokens |
| Output Price | ≤272K $15.000; >272K $22.000 /M tokens |
| Cache Read | ≤272K $0.250; >272K $0.500 /M tokens |

### openai/gpt-5.6-sol

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤272K $5.000; >272K $10.000 /M tokens |
| Output Price | ≤272K $30.000; >272K $45.000 /M tokens |
| Cache Read | ≤272K $0.500; >272K $1.000 /M tokens |

### x-ai/grok-4.5

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤200K $2.000; >200K $4.000 /M tokens |
| Output Price | ≤200K $6.000; >200K $12.000 /M tokens |
| Cache Read | ≤200K $0.500; >200K $1.000 /M tokens |

### sakana/fugu-ultra

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤272K $5.000; >272K $10.000 /M tokens |
| Output Price | ≤272K $30.000; >272K $45.000 /M tokens |
| Cache Read | ≤272K $0.500; >272K $1.000 /M tokens |

### x-ai/grok-build-0.1

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤200K $1.000; >200K $2.000 /M tokens |
| Output Price | ≤200K $2.000; >200K $4.000 /M tokens |
| Cache Read | ≤200K $0.200; >200K $0.400 /M tokens |

### openai/gpt-5.5-pro

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤272K $30.000; >272K $60.000 /M tokens |
| Output Price | ≤272K $180.000; >272K $270.000 /M tokens |

### qwen/qwen3.7-plus

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤256K $0.400; >256K $1.200 /M tokens |
| Output Price | ≤256K $1.600; >256K $4.800 /M tokens |
| Cache Read | ≤256K $0.080; >256K $0.240 /M tokens |
| Cache Write | ≤256K $0.500; >256K $1.500 /M tokens |

### MiniMax-M3

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤512K $0.300; >512K $0.600 /M tokens |
| Output Price | ≤512K $1.200; >512K $2.400 /M tokens |
| Cache Read | ≤512K $0.060; >512K $0.120 /M tokens |

### seed-2-0-pro-260328

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤128K $0.500; >128K $1.000 /M tokens |
| Output Price | ≤128K $3.000; >128K $6.000 /M tokens |
| Cache Read | ≤128K $0.100; >128K $0.200 /M tokens |
| Cache Write | ≤128K $0.008; >128K $0.008 /M tokens |

### seed-2-0-mini-260428

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤128K $0.100; >128K $0.200 /M tokens |
| Output Price | ≤128K $0.400; >128K $0.800 /M tokens |
| Cache Read | ≤128K $0.020; >128K $0.040 /M tokens |
| Cache Write | ≤128K $0.008; >128K $0.008 /M tokens |
| Audio Input | ≤128K $1.500; >128K $3.000 /M tokens |
| Input Audio Cache | ≤128K $0.300; >128K $0.600 /M tokens |

### seed-2-0-code-preview-260328

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤128K $0.500; >128K $1.000 /M tokens |
| Output Price | ≤128K $3.000; >128K $6.000 /M tokens |
| Cache Read | ≤128K $0.100; >128K $0.200 /M tokens |
| Cache Write | ≤128K $0.008; >128K $0.008 /M tokens |

### seed-2-0-lite-260428

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤128K $0.250; >128K $0.500 /M tokens |
| Output Price | ≤128K $2.000; >128K $4.000 /M tokens |
| Cache Read | ≤128K $0.050; >128K $0.100 /M tokens |
| Cache Write | ≤128K $0.008; >128K $0.008 /M tokens |
| Audio Input | ≤128K $3.750; >128K $7.500 /M tokens |
| Input Audio Cache | ≤128K $0.750; >128K $1.500 /M tokens |

### qwen3.6-flash

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤256K $0.171; >256K $0.686 /M tokens |
| Output Price | ≤256K $1.029; >256K $4.114 /M tokens |
| Cache Read | ≤256K $0.017; >256K $0.069 /M tokens |
| Cache Write | ≤256K $0.214; >256K $0.857 /M tokens |

### x-ai/grok-4.3

| 计费项 | 阶梯价 |
| --- | --- |
| Input Price | ≤200K $1.250; >200K $2.500 /M tokens |
| Output Price | ≤200K $2.500; >200K $5.000 /M tokens |
| Cache Read | ≤200K $0.200; >200K $0.400 /M tokens |


## 6. 全部模型价格表

按模型类型分组展示：

### Text 模型（98 个）

| 模型 | 计费模式 | Input Price | Output Price | Cache Read | 其他 |
| --- | --- | --- | --- | --- | --- |
| z-ai/glm-5.2-free `Free` | Pay as You Go | $0.0000 /M tokens | $0.0000 /M tokens | $0.0000 /M tokens |  |
| z-ai/glm-5.2 `60% off` | Pay as You Go | $0.4572 ~~$1.1430~~ /M tokens | $1.6000 ~~$4.0000~~ /M tokens | $0.1143 ~~$0.2858~~ /M tokens |  |
| moonshotai/kimi-k3 | Pay as You Go | $3.0000 /M tokens | $15.0000 /M tokens | $0.3000 /M tokens |  |
| openai/gpt-5.6-luna | Pay as You Go | ≤272K $1.000; >272K $2.000 /M tokens | ≤272K $6.000; >272K $9.000 /M tokens | ≤272K $0.100; >272K $0.200 /M tokens |  |
| openai/gpt-5.6-terra | Pay as You Go | ≤272K $2.500; >272K $5.000 /M tokens | ≤272K $15.000; >272K $22.000 /M tokens | ≤272K $0.250; >272K $0.500 /M tokens |  |
| openai/gpt-5.6-sol | Pay as You Go | ≤272K $5.000; >272K $10.000 /M tokens | ≤272K $30.000; >272K $45.000 /M tokens | ≤272K $0.500; >272K $1.000 /M tokens |  |
| x-ai/grok-4.5 | Pay as You Go | ≤200K $2.000; >200K $4.000 /M tokens | ≤200K $6.000; >200K $12.000 /M tokens | ≤200K $0.500; >200K $1.000 /M tokens |  |
| anthropic/claude-fable-5 | Pay as You Go | $10.0000 /M tokens | $50.0000 /M tokens | $1.0000 /M tokens | Cache Write: $12.5000 /M tokens |
| anthropic/claude-sonnet-5 | Pay as You Go | $2.0000 /M tokens | $10.0000 /M tokens | $0.2000 /M tokens |  |
| sakana/fugu-ultra | Pay as You Go | ≤272K $5.000; >272K $10.000 /M tokens | ≤272K $30.000; >272K $45.000 /M tokens | ≤272K $0.500; >272K $1.000 /M tokens |  |
| moonshotai/kimi-k2.7-code | Pay as You Go | $0.9286 /M tokens | $3.8571 /M tokens | $0.1857 /M tokens |  |
| x-ai/grok-build-0.1 | Pay as You Go | ≤200K $1.000; >200K $2.000 /M tokens | ≤200K $2.000; >200K $4.000 /M tokens | ≤200K $0.200; >200K $0.400 /M tokens |  |
| openai/gpt-5.5-pro | Pay as You Go | ≤272K $30.000; >272K $60.000 /M tokens | ≤272K $180.000; >272K $270.000 /M tokens |  |  |
| mistralai/mistral-small-2603 | Pay as You Go | $0.1500 /M tokens | $0.6000 /M tokens | $0.0150 /M tokens |  |
| mistralai/mistral-medium-3-5 | Pay as You Go | $1.5000 /M tokens | $7.5000 /M tokens |  |  |
| stepfun/step-3.7-flash | Pay as You Go | $0.2000 /M tokens | $1.1500 /M tokens | $0.0400 /M tokens |  |
| mistralai/voxtral-small-24b-2507 | Pay as You Go | $0.1000 /M tokens | $0.3000 /M tokens | $0.0100 /M tokens | Audio Input: $0.0060 /M tokens |
| qwen/qwen3.7-plus | Pay as You Go | ≤256K $0.400; >256K $1.200 /M tokens | ≤256K $1.600; >256K $4.800 /M tokens | ≤256K $0.080; >256K $0.240 /M tokens | Cache Write: ≤256K $0.500; >256K $1.500 /M tokens |
| MiniMax-M3 | Pay as You Go | ≤512K $0.300; >512K $0.600 /M tokens | ≤512K $1.200; >512K $2.400 /M tokens | ≤512K $0.060; >512K $0.120 /M tokens |  |
| anthropic/claude-opus-4.8-fast | Pay as You Go | $10.0000 /M tokens | $50.0000 /M tokens | $1.0000 /M tokens | Cache Write: $12.5000 /M tokens |
| anthropic/claude-opus-4.8 | Pay as You Go | $5.0000 /M tokens | $25.0000 /M tokens | $0.5000 /M tokens | Cache Write: $6.2500 /M tokens |
| seed-2-0-pro-260328 | Pay as You Go | ≤128K $0.500; >128K $1.000 /M tokens | ≤128K $3.000; >128K $6.000 /M tokens | ≤128K $0.100; >128K $0.200 /M tokens | Cache Write: ≤128K $0.008; >128K $0.008 /M tokens |
| seed-2-0-mini-260428 | Pay as You Go | ≤128K $0.100; >128K $0.200 /M tokens | ≤128K $0.400; >128K $0.800 /M tokens | ≤128K $0.020; >128K $0.040 /M tokens | Cache Write: ≤128K $0.008; >128K $0.008 /M tokens<br>Audio Input: ≤128K $1.500; >128K $3.000 /M tokens<br>Audio Completion Price: $0.6000 /M tokens<br>Input Audio Cache: ≤128K $0.300; >128K $0.600 /M tokens |
| seed-2-0-code-preview-260328 | Pay as You Go | ≤128K $0.500; >128K $1.000 /M tokens | ≤128K $3.000; >128K $6.000 /M tokens | ≤128K $0.100; >128K $0.200 /M tokens | Cache Write: ≤128K $0.008; >128K $0.008 /M tokens |
| seed-2-0-lite-260428 | Pay as You Go | ≤128K $0.250; >128K $0.500 /M tokens | ≤128K $2.000; >128K $4.000 /M tokens | ≤128K $0.050; >128K $0.100 /M tokens | Cache Write: ≤128K $0.008; >128K $0.008 /M tokens<br>Audio Input: ≤128K $3.750; >128K $7.500 /M tokens<br>Audio Completion Price: $1.5000 /M tokens<br>Input Audio Cache: ≤128K $0.750; >128K $1.500 /M tokens |
| openai/gpt-5.4-mini | Pay as You Go | $0.7500 /M tokens | $4.5000 /M tokens | $0.0750 /M tokens |  |
| qwen3.5-omni-plus | Pay as You Go | $1.0000 /M tokens | $5.7143 /M tokens |  | Audio Input: $7.5714 /M tokens<br>Audio Completion Price: $30.4286 /M tokens |
| openai/gpt-5.4-nano | Pay as You Go | $0.2000 /M tokens | $1.2500 /M tokens | $0.0200 /M tokens |  |
| qwen/qwen3.7-max | Pay as You Go | $1.2500 /M tokens | $3.7500 /M tokens | $0.2500 /M tokens |  |
| openai/gpt-oss-120b | Pay as You Go | $0.0390 /M tokens | $0.1800 /M tokens |  |  |
| qwen3.6-flash | Pay as You Go | ≤256K $0.171; >256K $0.686 /M tokens | ≤256K $1.029; >256K $4.114 /M tokens | ≤256K $0.017; >256K $0.069 /M tokens | Cache Write: ≤256K $0.214; >256K $0.857 /M tokens |
| google/gemma-4-26b-a4b-it | Pay as You Go | $0.0600 /M tokens | $0.3300 /M tokens |  |  |
| google/gemini-3.5-flash | Pay as You Go | $1.5000 /M tokens | $9.0000 /M tokens | $0.1500 /M tokens | Cache Write: $0.0833 /M tokens<br>Audio Input: $3.0000 /M tokens<br>Audio Completion Price: $0.3000 /M tokens |
| anthropic/claude-opus-4.7-fast | Pay as You Go | $30.0000 /M tokens | $150.0000 /M tokens | $3.0000 /M tokens |  |
| miromind/mirothinker-1-7-deepresearch | Pay as You Go | $4.0000 /M tokens | $25.0000 /M tokens |  |  |
| miromind/mirothinker-1-7-deepresearch-mini | Pay as You Go | $1.2500 /M tokens | $10.0000 /M tokens |  |  |
| tencent/hy3-preview | Pay as You Go | $0.0660 /M tokens | $0.2600 /M tokens | $0.0290 /M tokens |  |
| x-ai/grok-4.3 | Pay as You Go | ≤200K $1.250; >200K $2.500 /M tokens | ≤200K $2.500; >200K $5.000 /M tokens | ≤200K $0.200; >200K $0.400 /M tokens |  |
| nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free | Pay as You Go | $0.0000 /M tokens | $0.0000 /M tokens |  |  |
| openai/gpt-5.5 | Pay as You Go | ≤272K $5.000; >272K $10.000 /M tokens | ≤272K $30.000; >272K $45.000 /M tokens | ≤272K $0.500; >272K $1.000 /M tokens |  |
| xiaomi/mimo-v2.5-pro | Pay as You Go | $1.0000 /M tokens | $3.0000 /M tokens | $0.2000 /M tokens |  |
| z-ai/glm-5.1 | Pay as You Go | $1.0500 /M tokens | $3.5000 /M tokens | $0.5250 /M tokens |  |
| xiaomi/mimo-v2.5 | Pay as You Go | $0.4000 /M tokens | $2.0000 /M tokens | $0.0800 /M tokens |  |
| deepseek/deepseek-v4-pro | Pay as You Go | $0.4350 /M tokens | $0.8700 /M tokens | $0.0036 /M tokens |  |
| anthropic/claude-opus-4.7 | Pay as You Go | $5.0000 /M tokens | $25.0000 /M tokens | $0.5000 /M tokens | Cache Write: $6.2500 /M tokens |
| anthropic/claude-haiku-4.5 | Pay as You Go | $1.0000 /M tokens | $5.0000 /M tokens | $0.1000 /M tokens | Cache Write: $1.2500 /M tokens |
| anthropic/claude-opus-4.5 | Pay as You Go | $5.0000 /M tokens | $25.0000 /M tokens | $0.5000 /M tokens | Cache Write: $6.2500 /M tokens |
| anthropic/claude-opus-4.6 | Pay as You Go | $5.0000 /M tokens | $25.0000 /M tokens | $0.5000 /M tokens | Cache Write: $6.2500 /M tokens |
| anthropic/claude-sonnet-4 | Pay as You Go | ≤200K $3.000; >200K $6.000 /M tokens | ≤200K $15.000; >200K $22.500 /M tokens | ≤200K $0.300; >200K $0.600 /M tokens | Cache Write: ≤200K $3.750; >200K $7.500 /M tokens |
| anthropic/claude-sonnet-4.5 | Pay as You Go | ≤200K $3.000; >200K $6.000 /M tokens | ≤200K $15.000; >200K $22.500 /M tokens | ≤200K $0.300; >200K $0.600 /M tokens | Cache Write: ≤200K $3.750; >200K $7.500 /M tokens |
| anthropic/claude-sonnet-4.6 | Pay as You Go | $3.0000 /M tokens | $15.0000 /M tokens | $0.3000 /M tokens | Cache Write: $3.7500 /M tokens |
| moonshotai/kimi-k2.6 | Pay as You Go | $0.9500 /M tokens | $4.0000 /M tokens | $0.1600 /M tokens |  |
| openai/gpt-5.1-codex-mini | Pay as You Go | $0.2500 /M tokens | $2.0000 /M tokens | $0.0250 /M tokens |  |
| openai/gpt-5.2 | Pay as You Go | $1.7500 /M tokens | $14.0000 /M tokens | $0.1750 /M tokens |  |
| openai/gpt-5.1-codex-max | Pay as You Go | $1.2500 /M tokens | $10.0000 /M tokens | $0.1250 /M tokens |  |
| openai/gpt-5.3-codex | Pay as You Go | $1.7500 /M tokens | $14.0000 /M tokens | $0.1750 /M tokens |  |
| openai/gpt-5.2-codex | Pay as You Go | $1.7500 /M tokens | $14.0000 /M tokens | $0.1750 /M tokens |  |
| qwen/qwen3.5-flash | Pay as You Go | $0.1048 /M tokens | $0.4194 /M tokens |  |  |
| qwen/qwen3.6-plus | Pay as You Go | ≤256K $0.540; >256K $2.140 /M tokens | ≤256K $3.210; >256K $6.420 /M tokens |  |  |
| z-ai/glm-4.6 | Pay as You Go | $0.6000 /M tokens | $2.2000 /M tokens | $0.1100 /M tokens |  |
| z-ai/glm-4.7 | Pay as You Go | $0.6000 /M tokens | $2.2000 /M tokens | $0.1100 /M tokens |  |
| minimax/minimax-m2-her | Pay as You Go | $0.3000 /M tokens | $1.2000 /M tokens | $0.0300 /M tokens |  |
| google/gemini-3.1-flash-image-preview | Pay as You Go | $0.5000 /M tokens | $3.0000 /M tokens |  | Image Output Price: $60.000 /M tokens |
| deepseek/deepseek-v4-flash | Pay as You Go | $0.1400 /M tokens | $0.2800 /M tokens | $0.0028 /M tokens |  |
| minimax/minimax-m2.7 | Pay as You Go | $0.3000 /M tokens | $1.2000 /M tokens | $0.0600 /M tokens |  |
| stepfun/step-3.5-flash | Pay as You Go | $0.1000 /M tokens | $0.3000 /M tokens | $0.0200 /M tokens |  |
| xiaomi/mimo-v2-pro | Pay as You Go | ≤256K $1.000; >256K $2.000 /M tokens | ≤256K $3.000; >256K $6.000 /M tokens | ≤256K $0.200; >256K $0.400 /M tokens |  |
| xiaomi/mimo-v2-omni | Pay as You Go | $0.4000 /M tokens | $2.0000 /M tokens | $0.0800 /M tokens |  |
| xiaomi/mimo-v2-flash | Pay as You Go | $0.1000 /M tokens | $0.3000 /M tokens | $0.0100 /M tokens |  |
| qwen/qwen3.5-9b | Pay as You Go | $0.1000 /M tokens | $0.1500 /M tokens |  |  |
| openai/gpt-audio-mini | Pay as You Go | $0.6000 /M tokens | $2.4000 /M tokens |  | Audio Input: $10.0000 /M tokens<br>Audio Completion Price: $20.0000 /M tokens |
| openai/gpt-audio | Pay as You Go | $2.5000 /M tokens | $10.0000 /M tokens |  | Audio Input: $32.0000 /M tokens<br>Audio Completion Price: $64.0000 /M tokens |
| x-ai/grok-4.20-beta | Pay as You Go | ≤200K $2.000; >200K $4.000 /M tokens | ≤200K $6.000; >200K $12.000 /M tokens | ≤200K $0.200; >200K $0.400 /M tokens |  |
| google/gemini-2.5-flash-image | Pay as You Go | $0.3000 /M tokens | $2.5000 /M tokens |  | Image Output Price: $30.000 /M tokens |
| google/gemini-3-pro-image-preview | Pay as You Go | $2.0000 /M tokens | $12.0000 /M tokens |  | Image Output Price: $120.000 /M tokens |
| z-ai/glm-5 | Pay as You Go | $1.0000 /M tokens | $3.2000 /M tokens | $0.2000 /M tokens |  |
| z-ai/glm-4.5-air | Pay as You Go | $0.1300 /M tokens | $0.8500 /M tokens | $0.0250 /M tokens |  |
| x-ai/grok-4.1-fast | Pay as You Go | $0.2000 /M tokens | $0.5000 /M tokens | $0.0500 /M tokens |  |
| qwen/qwen3.5-plus-02-15 | Pay as You Go | ≤256K $0.260; >256K $0.325 /M tokens | ≤256K $1.560; >256K $1.950 /M tokens |  |  |
| qwen/qwen3.5-397b-a17b | Pay as You Go | $0.3900 /M tokens | $2.3400 /M tokens |  |  |
| qwen/qwen3.5-35b-a3b | Pay as You Go | $0.1625 /M tokens | $1.3000 /M tokens |  |  |
| qwen/qwen3.5-122b-a10b | Pay as You Go | $0.2600 /M tokens | $2.0800 /M tokens |  |  |
| qwen/qwen3-coder-next | Pay as You Go | $0.1200 /M tokens | $0.7500 /M tokens | $0.0600 /M tokens |  |
| z-ai/glm-4.6v | Pay as You Go | $0.3000 /M tokens | $0.9000 /M tokens |  |  |
| openai/gpt-5.4 | Pay as You Go | ≤272K $2.500; >272K $5.000 /M tokens | ≤272K $15.000; >272K $22.500 /M tokens | ≤272K $0.250; >272K $0.500 /M tokens |  |
| openai/gpt-5-mini | Pay as You Go | $0.2500 /M tokens | $2.0000 /M tokens | $0.0250 /M tokens |  |
| openai/gpt-4o-mini | Pay as You Go | $0.1500 /M tokens | $0.6000 /M tokens | $0.0750 /M tokens |  |
| moonshotai/kimi-k2.5 | Pay as You Go | $0.6000 /M tokens | $3.0000 /M tokens | $0.1000 /M tokens |  |
| minimax/minimax-m2.7-highspeed | Pay as You Go | $0.6000 /M tokens | $2.4000 /M tokens | $0.0600 /M tokens |  |
| mistralai/devstral-2512 | Pay as You Go | $0.4000 /M tokens | $2.0000 /M tokens | $0.0400 /M tokens |  |
| z-ai/glm-5-turbo | Pay as You Go | $1.2000 /M tokens | $4.0000 /M tokens | $0.2400 /M tokens |  |
| minimax/minimax-m2.1-highspeed | Pay as You Go | $0.6000 /M tokens | $2.4000 /M tokens | $0.0600 /M tokens |  |
| minimax/minimax-m2.5 | Pay as You Go | $0.3000 /M tokens | $1.2000 /M tokens | $0.0300 /M tokens |  |
| minimax/minimax-m2.1 | Pay as You Go | $0.3000 /M tokens | $1.2000 /M tokens | $0.0300 /M tokens |  |
| google/gemini-3.1-pro-preview | Pay as You Go | ≤200K $2.000; >200K $4.000 /M tokens | ≤200K $12.000; >200K $18.000 /M tokens | ≤200K $0.200; >200K $0.400 /M tokens | Cache Write: $0.375<br>Audio Input: ≤200K $2.000; >200K $4.000 /M tokens<br>Input Audio Cache: ≤200K $0.200; >200K $0.400 /M tokens |
| google/gemini-3-flash-preview | Pay as You Go | $0.5000 /M tokens | $3.0000 /M tokens | $0.0500 /M tokens | Cache Write: $0.0833 /M tokens<br>Audio Input: $1.0000 /M tokens |
| deepseek/deepseek-v3.2 | Pay as You Go | $0.2600 /M tokens | $0.3800 /M tokens | $0.1300 /M tokens |  |
| nvidia/nemotron-3-super-120b-a12b | Pay as You Go | $0.3000 /M tokens | $0.9000 /M tokens | $0.1000 /M tokens |  |

### Image 模型（8 个）

| 模型 | 计费模式 | Input Price | Output Price | Cache Read | 其他 |
| --- | --- | --- | --- | --- | --- |
| bytedance-seed/seedream-5.0-pro | Pay Per Request |  |  |  | Input: $0.003 /image<br>Output: $0.090 /image |
| google/gemini-3.1-flash-lite-image | Pay as You Go | $0.2500 /M tokens | $1.5000 /M tokens |  | Image Output Price: $30.000 /M tokens |
| bytedance-seed/seedream-5.0-lite | Pay Per Request |  |  |  | Model Price: $0.035 /image |
| microsoft/mai-image-2.5 | Pay as You Go | $5.0000 /M tokens | $47.0000 /M tokens |  | Image Input Price: $8.0000 /M tokens |
| openai/gpt-5.4-image-2 | Pay as You Go | $8.0000 /M tokens | $30.0000 /M tokens | $2.0000 /M tokens |  |
| bytedance-seed/seedream-4.5 | Pay Per Request |  |  |  | Model Price: $0.040 /image |
| openai/gpt-5-image-mini | Pay as You Go | $2.5000 /M tokens | $8.0000 /M tokens | $0.2500 /M tokens |  |
| openai/gpt-5-image | Pay as You Go | $10.0000 /M tokens | $40.0000 /M tokens | $2.5000 /M tokens |  |

### Video 模型（7 个）

| 模型 | 计费模式 | Input Price | Output Price | Cache Read | 其他 |
| --- | --- | --- | --- | --- | --- |
| kling-3.0-turbo | Pay as You Go |  |  |  | 1080P: $0.140 / s<br>720P: $0.112 / s |
| kling-v3-omni | Pay Per Request |  |  |  | 4k x audio x noVideo: $0.420 / s<br>4k x noAudio x noVideo: $0.420 / s<br>4k x noAudio x video: $0.420 / s<br>pro x audio x noVideo: $0.140 / s<br>pro x noAudio x noVideo: $0.112 / s<br>pro x noAudio x video: $0.168 / s<br>std x audio x noVideo: $0.112 / s<br>std x noAudio x noVideo: $0.084 / s<br>std x noAudio x video: $0.126 / s |
| happyhorse-1.0-i2v | Pay Per Request |  |  |  | 1080P: $0.240 / s<br>720P: $0.140 / s |
| happyhorse-1.0-t2v | Pay Per Request |  |  |  | 1080P: $0.240 / s<br>720P: $0.140 / s |
| kling-v3 | Pay Per Request |  |  |  | 4k x audio x noVoice: $0.420 / s<br>4k x noAudio x noVoice: $0.420 / s<br>pro x audio x noVoice: $0.168 / s<br>pro x noAudio x noVoice: $0.112 / s<br>pro x noAudio x voice: $0.112 / s<br>std x audio x noVoice: $0.126 / s<br>std x noAudio x noVoice: $0.084 / s<br>std x noAudio x voice: $0.084 / s |
| kling-v2-6 | Pay Per Request |  |  |  | pro x 10s x audio x noVoice: $1.400<br>pro x 10s x audio x voice: $1.680<br>pro x 10s x noAudio x noVoice: $0.700<br>pro x 10s x noAudio x voice: $0.700<br>pro x 5s x audio x noVoice: $0.700<br>pro x 5s x audio x voice: $0.840<br>pro x 5s x noAudio x noVoice: $0.350<br>pro x 5s x noAudio x voice: $0.350<br>std x 10s x noAudio x noVoice: $0.420<br>std x 10s x noAudio x voice: $0.420<br>std x 5s x noAudio x noVoice: $0.210<br>std x 5s x noAudio x voice: $0.210 |
| MiniMax-Hailuo-2.3 | Pay Per Request |  |  |  | 1080P 6s: $0.490<br>768P 10s: $0.560<br>768P 6s: $0.280 |

## 7. 平台演进对比（2026-06 vs 2026-07）

> 对比依据：`workspace/tokenrouter/tokenrouter-notes-20260715.md`（6 月初研究）与本文件（7 月中抓取）。

| 维度 | 2026-06 观察 | 2026-07 现状 | 演进说明 |
| --- | --- | --- | --- |
| 模型数量 | 87 个 | **113 个** | 新增 26 个模型，涨幅约 30% |
| 前端技术栈 | 自研 React SPA，`backend.palebluedot.ai/graphql` | **new-api 生态**，`generator: new-api` | 整体重构/迁移到 new-api，登录鉴权从 GraphQL `builderLogin` 变为常规邮箱密码 + Terms 复选框 |
| 模型广场路径 | `/console/pricing` | `/models` | 公开/独立页面，未登录也可浏览价格 |
| 模型卡片交互 | 2 列卡片，点击打开右侧 panel | 2 列卡片 + 分页，点击打开 **侧滑详情页（side sheet）** | 详情页新增 Python/cURL Quick Start |
| 计费展示 | 主要展示 Input/Output/Cache | 同上，但**新增更多阶梯价阈值** | 6 月多为 ≤256K；7 月出现 ≤128K / ≤200K / ≤256K / ≤272K 多种阈值 |
| 推广/促销 | 顶部 Banner「Top up 6, get 4 free (40% off)」 | Banner 变为「Top up 7, get 3 free (30% off)」；模型卡片出现 `Free` / `Test` / `HOT` / `60% off` 徽章 | 营销活动调整，模型级促销上线 |
| 模型类型 | Text / Image / Video / Audio 已有 | 新增更多 Video / Audio / Image 模型 | 多模态能力扩展 |
| 控制台导航 | Home / Dashboard / Models / Chat / API Keys / Usage Logs | 增加 **Management Keys** | 权限体系细分 |
| API 基地址 | 未明确记录 | `https://api.tokenrouter.com/v1` | 明确对外暴露统一 OpenAI 兼容端点 |

**核心结论**：TokenRouter 这一个月主要在做 **「模型扩容 + 前端重构到 new-api + 营销促销化」**；UI 模式（卡片、Drawer、Dashboard KPI）基本稳定，只是路径和细节有调整。

## 8. 架构与 UI 可借鉴点

### 8.1 产品定位
- **统一 API 层**：所有模型对外暴露同一套 OpenAI 兼容接口，降低接入成本。
- **模型广场**：独立的 `/models` 页面以卡片形式展示全部模型，支持按类型筛选、分页、搜索。
- **价格透明**：在模型卡片上直接展示 Input/Output/Cache 价格，点击卡片弹出侧滑详情页。

### 8.2 价格展示
- 卡片首屏即可见价格，促销模型使用彩色背景、徽章（Free/Test/HOT/60% off）突出显示。
- 原价与现价同框展示，原价带删除线。
- 详情页提供 `Python` / `cURL` quick start 代码片段，可直接复制。
- 对多模态/音视频模型，按请求维度（`/image`、`/s`）展示价格。

### 8.3 控制台设计
- 左侧导航：Home / Dashboard / Models / Chat / API Keys / Management Keys / Usage Logs。
- Dashboard 聚合：现金余额、代金券、总消费、请求数、Token 数、RPM/TPM、消费趋势、模型调用排行。
- 顶部常驻活动 Banner（如 "Top up 7, get 3 free"）。
- 登录后弹窗展示营销活动（30% off、Developer Credits）。

### 8.4 技术与交互架构：我们可以借鉴什么？

TokenRouter 的 UI 观感好于多数 admin 风格控制台，关键不在于「用了多复杂的技术」，而在于 **设计系统一致性、信息层级、交互模式统一**。以下是可落到我们项目中的具体借鉴点：

#### 1. 采用成熟的设计系统 / 组件库
- TokenRouter 使用 **Semi Design**（React 组件库），字体、间距、圆角、阴影、按钮、badge、卡片全部统一。
- 我们的 UI 目前较多自定义样式，容易出现「每个页面风格略有不同」的问题。
- **建议**：评估是否引入 Semi Design / Ant Design / 或其他一套完整组件库，统一按钮、表单、表格、卡片、Drawer、Modal、空状态、加载态。若无法整体替换，至少统一 **颜色变量、圆角、间距、字体**。

#### 2. 用「右侧 Drawer」替代大量 Modal
- TokenRouter 的「模型详情」、「Create API Key」全部走右侧 Drawer，**无 backdrop**，点外部即可关闭；列表上下文始终保留。
- 我们的早期设计保留了很多 Modal，导致用户在列表 ↔ 表单之间反复失去上下文。
- **建议**：
  - 创建/编辑/查看详情 → 优先用 Drawer
  - 只有「删除确认、支付、阻断式提示、合规必须确认」才用 Modal
  - Drawer 内可承载复杂表单（如 API Key 创建的三段式表单：Basic / Quota / Models）

#### 3. 卡片式信息陈列：关键信息前置
- TokenRouter 模型卡片一眼看到：名称、价格、计费模式、类型。
- 我们的 API Keys 已经卡片化，但 **模型/服务浏览页** 仍是表格。
- **建议**：把「模型选择、价格浏览、套餐选择」等用户需要对比的页面，从表格改为卡片网格；每个卡片只保留 3-5 个最关键字段，其余放 Drawer 详情。

#### 4. 视觉层次：促销/状态用颜色 + 徽章
- TokenRouter 的 `HOT`、`60% off`、`Free` 徽章、彩色促销卡片背景，让重要信息一眼可识别。
- 我们的状态标签（key status、model status）目前可能偏素。
- **建议**：
  - 用色块/徽章区分：active/free/promo/new/hot
  - 原价用删除线、现价用主色高亮
  - 顶部加固定营销 Banner（可关闭），用于活动/公告

#### 5. 空状态不要只放文字
- TokenRouter 的 Usage Logs 空状态用猫插画 + "No Results Found"，降低空页面的枯燥感。
- 我们的空状态目前多为纯文字「暂无数据」。
- **建议**：为常用空页面（无 key、无日志、无模型）增加 SVG 插画或图标 + 操作引导按钮。

#### 6. 表单内的「快捷选项」按钮组
- TokenRouter API Key 创建表单有「Never Expires / A Month / One Day / Custom Date」按钮组，减少用户选择成本。
- 我们的表单字段多为标准输入框/下拉框。
- **建议**：对常见选项（过期时间、配额周期、套餐档位）提供预设按钮组，点击即可填充，减少输入。

#### 7. 顶部 Profile Switcher
- TokenRouter 右上角头像下拉可切换 Personal / Enterprise 上下文，符合 SaaS 心智模型。
- 我们当前用侧栏三大区域（User / Enterprise / System Console）切换。
- **建议**：在右上角增加「当前组织/个人」下拉切换；侧栏保留两级导航（Admin Console / Personal），避免用户在不同控制台之间跳转时迷失。

#### 8. Dashboard：KPI 卡片 + 图表 tab
- TokenRouter Dashboard 顶部 4 个 KPI 卡，下方一个 Analysis 区块带 4 个 tab。
- 我们的 Dashboard 已有 8 个 stat-card，但图表和模型排行可以进一步聚合到同一区块，用 tab 切换。
- **建议**：把「消费趋势、模型调用排行、消费分布」合并为一个可切换的图表区块，减少页面纵向长度。

#### 9. Chat Playground
- TokenRouter 有内置 Chat UI（左历史 + 主输入区），是开发者高频功能，也是平台差异化卖点。
- 我们目前无此功能。
- **建议**：P1 优先级规划一个内置 Playground，让用户直接在控制台测试模型/Key，提升留存。

#### 10. 前端路由与 SPA 体验
- TokenRouter 是单页应用，页面切换无刷新；Drawer 滑入、卡片 hover 阴影、badge 动效都很自然。
- 我们的页面切换如果有明显刷新或白屏，会显「老气」。
- **建议**：
  - 若技术栈允许，尽量做 SPA 或局部刷新
  - 统一过渡动画（Drawer 滑入、卡片 hover、按钮反馈）
  - 减少页面跳转后的布局抖动

### 8.5 小结
TokenRouter 的 UI 优势可归纳为：**「统一的设计系统 + 卡片前置关键信息 + Drawer 保上下文 + 营销促销视觉化 + 开发者工具内置」**。我们不需要照搬，但可以从「组件库统一」和「Drawer 优先」这两点先入手，成本最低、见效最快。

## 9. 注意事项
- 价格数据来自公开模型广场页面，抓取时部分模型可能带有促销折扣。
- 阶梯价基于上下文长度（如 ≤128K、≤200K、≤256K、≤272K）划分，超过阈值后单价翻倍或按 1.5 倍计算。
- 免费/测试模型明确提示「Service stability and concurrency are not guaranteed」。
