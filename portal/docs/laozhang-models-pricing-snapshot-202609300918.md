# laozhang 模型价格总表（快照 2026-09-30）

> 状态：ACTIVE — 数据来源：老张API `/api/pricing`（控制台 token 读取，252 条）× docs.laozhang.ai/models 页面（更新于 2026-09-29 09:31 UTC+8）交叉核对。实际扣费以 LZ 控制台和调用日志为准。
> 用途：模型入驻、定价核对、路由决策参考。

## 价格换算规则

- new-api 计费口径 `$1 = 500,000 quota`，`model_ratio` 为 **per-token** quota 倍率：
  - 输入 $/1M = `model_ratio × 2`；输出 $/1M = `model_ratio × completion_ratio × 2`；缓存读 $/1M = `cache_ratio × 2`
- per-request 模型（quota_type=1）：`model_price` 字段直接为美元/次
- `billing_mode=tiered_expr`：按单次请求 input+output 总长选档、整请求按该档计价（与 portal `tiered_context` 语义一致），阈值与表达式见附录
- 非 default 分组调用时按标价 × 分组倍率计费（Wan ×0.15、SeeDance2 ×0.18、GPT_Image_2_Enterprise ×1.2）

## 总览

- API 共 252 条（页面口径在线 249 个，差异为分组/展示口径）；纯 default 分组 **223** 个；含其他分组 29 个
- 按量 222 个 / 按次 30 个；阶梯计价 37 个

| 厂商 | 模型数 |
|---|---|
| OpenAI | 99 |
| 阿里巴巴 | 76 |
| Google | 19 |
| 字节跳动 | 13 |
| xAI | 13 |
| 智谱 | 10 |
| DeepSeek | 8 |
| Black Forest Labs | 5 |
| Moonshot | 5 |
| MiniMax | 2 |
| 美团 | 1 |
| 阶跃星辰 | 1 |

## 全量模型价格表

图例：✅=纯 default 组（default 令牌可直接调用）；🏷=含其他分组（列出组名）。

### OpenAI（99）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `dall-e-3` | ✅ | 入 $40 · 出 $40 /1M |  | image-generation,openai |
| `gpt-3.5-turbo` | ✅ | 入 $0.5 · 出 $1.5 /1M |  | openai |
| `gpt-3.5-turbo-0125` | ✅ | 入 $0.5 · 出 $1.5 /1M |  | openai |
| `gpt-3.5-turbo-0613` | ✅ | 入 $1.5 · 出 $1.95 /1M |  | openai |
| `gpt-3.5-turbo-1106` | ✅ | 入 $1 · 出 $2 /1M |  | openai |
| `gpt-3.5-turbo-16k` | ✅ | 入 $3 · 出 $3.9 /1M |  | openai |
| `gpt-3.5-turbo-16k-0613` | ✅ | 入 $3 · 出 $3.9 /1M |  | openai |
| `gpt-3.5-turbo-instruct` | ✅ | 入 $1.5 · 出 $1.95 /1M |  | openai |
| `gpt-4.1` | ✅ | 入 $2 · 出 $8 · 缓存 $0.5 /1M |  | openai |
| `gpt-4.1-2025-04-14` | ✅ | 入 $2 · 出 $8 · 缓存 $0.5 /1M |  | openai |
| `gpt-4.1-mini` | ✅ | 入 $0.4 · 出 $1.6 · 缓存 $0.5 /1M |  | openai |
| `gpt-4.1-mini-2025-04-14` | ✅ | 入 $0.4 · 出 $1.6 · 缓存 $0.5 /1M |  | openai |
| `gpt-4.1-nano` | ✅ | 入 $0.1 · 出 $0.4 · 缓存 $0.5 /1M |  | openai |
| `gpt-4.1-nano-2025-04-14` | ✅ | 入 $0.1 · 出 $0.4 · 缓存 $0.5 /1M |  | openai |
| `gpt-4o` | ✅ | 入 $2.5 · 出 $10 · 缓存 $1 /1M |  | openai |
| `gpt-4o-2024-05-13` | ✅ | 入 $5 · 出 $15 /1M |  | openai |
| `gpt-4o-2024-08-06` | ✅ | 入 $2.5 · 出 $10 · 缓存 $1 /1M |  | openai |
| `gpt-4o-2024-11-20` | ✅ | 入 $2.5 · 出 $10 · 缓存 $1 /1M |  | openai |
| `gpt-4o-audio-preview` | ✅ | 入 $2.5 · 出 $10 /1M |  | openai |
| `gpt-4o-mini` | ✅ | 入 $0.15 · 出 $0.6 · 缓存 $1 /1M |  | openai |
| `gpt-4o-mini-2024-07-18` | ✅ | 入 $0.15 · 出 $0.6 · 缓存 $1 /1M |  | openai |
| `gpt-4o-mini-audio-preview` | ✅ | 入 $2 · 出 $8 /1M |  | openai |
| `gpt-4o-mini-transcribe` | ✅ | 入 $1.5 · 出 $6 /1M |  | openai |
| `gpt-4o-mini-tts` | ✅ | 入 $1.2 · 出 $18 /1M |  | openai |
| `gpt-4o-transcribe` | ✅ | 入 $8 · 出 $16 /1M |  | openai |
| `gpt-5` | ✅ | 入 $1.25 · 出 $10 · 缓存 $0.2 /1M |  | openai |
| `gpt-5-2025-08-07` | ✅ | 入 $1.25 · 出 $10 · 缓存 $0.2 /1M |  | openai |
| `gpt-5-chat` | ✅ | 入 $1.25 · 出 $10 · 缓存 $0.2 /1M |  | openai |
| `gpt-5-mini` | ✅ | 入 $0.25 · 出 $2 · 缓存 $0.2 /1M |  | openai |
| `gpt-5-mini-2025-08-07` | ✅ | 入 $0.25 · 出 $2 · 缓存 $0.2 /1M |  | openai |
| `gpt-5-nano` | ✅ | 入 $0.05 · 出 $0.4 · 缓存 $0.2 /1M |  | openai |
| `gpt-5-nano-2025-08-07` | ✅ | 入 $0.05 · 出 $0.4 · 缓存 $0.2 /1M |  | openai |
| `gpt-5-pro` | ✅ | 入 $15 · 出 $120 /1M |  | openai |
| `gpt-5-pro-2025-10-06` | ✅ | 入 $15 · 出 $120 /1M |  | openai |
| `gpt-5.1` | ✅ | 入 $1.25 · 出 $10 · 缓存 $0.2 /1M |  | openai |
| `gpt-5.1-2025-11-13` | ✅ | 入 $1.25 · 出 $10 · 缓存 $0.2 /1M |  | openai |
| `gpt-5.1-thinking` | ✅ | 入 $1.25 · 出 $10 · 缓存 $0.2 /1M |  | openai |
| `gpt-5.2` | ✅ | 入 $1.75 · 出 $14 · 缓存 $0.2 /1M |  | openai |
| `gpt-5.2-2025-12-11` | ✅ | 入 $1.75 · 出 $14 · 缓存 $0.2 /1M |  | openai |
| `gpt-5.4` | ✅ | 入 $2.5 · 出 $15 · 缓存 $0.2 /1M | 阈值 271999 | openai |
| `gpt-5.4-mini` | ✅ | 入 $0.75 · 出 $4.5 · 缓存 $0.2 /1M |  | openai |
| `gpt-5.4-nano` | ✅ | 入 $0.2 · 出 $1.25 · 缓存 $0.2 /1M |  | openai |
| `gpt-5.4-pro` | ✅ | 入 $30 · 出 $180 /1M | 阈值 271999 | openai |
| `gpt-5.5` | ✅ | 入 $5 · 出 $30 · 缓存 $0.2 /1M | 阈值 271999 | openai |
| `gpt-5.5-pro` | ✅ | 入 $30 · 出 $180 /1M | 阈值 271999 | openai |
| `gpt-5.6-luna` | ✅ | 入 $0.2 · 出 $1.2 · 缓存 $0.2 /1M | 阈值 272000 | openai |
| `gpt-5.6-sol` | ✅ | 入 $4 · 出 $20 · 缓存 $0.2 /1M | 阈值 272000 | openai |
| `gpt-5.6-terra` | ✅ | 入 $2 · 出 $12 · 缓存 $0.2 /1M | 阈值 272000 | openai |
| `gpt-6-astra` | ✅ | 入 $10 · 出 $50 · 缓存 $0.2 /1M | 阈值 272000 | openai |
| `gpt-6-luna` | ✅ | 入 $0.1 · 出 $0.5 · 缓存 $0.2 /1M | 阈值 272000 | openai |
| `gpt-6-sol` | ✅ | 入 $2 · 出 $10 · 缓存 $0.2 /1M | 阈值 272000 | openai |
| `gpt-image-1` | 🏷 GPT_Image_2_Enterprise,default,sora_official | 入 $5 · 出 $40 · 缓存 $0.5 /1M |  | image-generation,openai |
| `gpt-image-1-mini` | 🏷 GPT_Image_2_Enterprise,default,sora_official | 入 $2 · 出 $8 · 缓存 $0.2 /1M |  | image-generation,openai |
| `gpt-image-1.5` | 🏷 GPT_Image_2_Enterprise,default,sora_official | 入 $5 · 出 $32 · 缓存 $0.5 /1M |  | image-generation,openai |
| `gpt-image-1.5-2025-12-16` | ✅ | 入 $5 · 出 $32 · 缓存 $0.5 /1M |  | image-generation,openai |
| `gpt-image-2` | 🏷 GPT_Image_2_Enterprise,default,gpt_image_2_reverse,sora_official | **$0.03 / 次** | 见附录 | image-generation,openai |
| `gpt-image-2-all` | 🏷 default,gpt_image_2_reverse | **$0.03 / 次** |  | image-generation,openai |
| `gpt-image-2-vip` | 🏷 default,gpt_image_2_reverse | **$0.03 / 次** |  | image-generation,openai |
| `gpt-image-2-web` | 🏷 default,gpt_image_2_reverse | **$0.03 / 次** |  | image-generation,openai |
| `gpt-image-2.5-flare` | 🏷 GPT_Image_2_Enterprise,sora_official | **$0.2 / 次** | 见附录 | image-generation,openai |
| `gpt-image-2.5-flare-2026-09-08` | 🏷 GPT_Image_2_Enterprise,sora_official | 入 $5 · 出 $30 · 缓存 $0.5 /1M |  | image-generation,openai |
| `gpt-image-2.5-flare-vip` | 🏷 default,gpt_image_2_reverse | **$0.03 / 次** |  | image-generation,openai |
| `gpt-image-2.5-sunburst` | 🏷 GPT_Image_2_Enterprise,sora_official | **$0.2 / 次** | 见附录 | image-generation,openai |
| `gpt-image-2.5-sunburst-2026-09-08` | 🏷 GPT_Image_2_Enterprise,sora_official | 入 $5 · 出 $30 · 缓存 $0.5 /1M |  | image-generation,openai |
| `gpt-image-2.5-sunburst-vip` | 🏷 default,gpt_image_2_reverse | **$0.03 / 次** |  | image-generation,openai |
| `gpt-image-2.5-web` | 🏷 default,gpt_image_2_reverse | **$0.03 / 次** |  | image-generation,openai |
| `gpt-oss-120b` | ✅ | 入 $0.5 · 出 $2 /1M |  | openai |
| `gpt-oss-20b` | ✅ | 入 $0.1 · 出 $0.4 /1M |  | openai |
| `o1` | ✅ | 入 $15 · 出 $60 · 缓存 $1 /1M |  | openai |
| `o1-2024-12-17` | ✅ | 入 $15 · 出 $60 · 缓存 $1 /1M |  | openai |
| `o1-mini` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $1 /1M |  | openai |
| `o1-mini-2024-09-12` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $1 /1M |  | openai |
| `o1-preview` | ✅ | 入 $15 · 出 $60 · 缓存 $1 /1M |  | openai |
| `o1-preview-2024-09-12` | ✅ | 入 $15 · 出 $60 · 缓存 $1 /1M |  | openai |
| `o1-pro` | ✅ | 入 $180 · 出 $720 /1M |  | openai |
| `o1-pro-2025-03-19` | ✅ | 入 $180 · 出 $720 /1M |  | openai |
| `o3` | ✅ | 入 $3 · 出 $12 · 缓存 $0.5 /1M |  | openai |
| `o3-2025-04-16` | ✅ | 入 $3 · 出 $12 · 缓存 $0.5 /1M |  | openai |
| `o3-mini` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $1 /1M |  | openai |
| `o3-mini-2025-01-31` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $1 /1M |  | openai |
| `o3-mini-2025-01-31-high` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $1 /1M |  | openai |
| `o3-mini-2025-01-31-low` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $1 /1M |  | openai |
| `o3-mini-2025-01-31-medium` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $1 /1M |  | openai |
| `o3-mini-low` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $1 /1M |  | openai |
| `o3-mini-medium` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $1 /1M |  | openai |
| `o3-pro` | ✅ | 入 $20 · 出 $80 /1M |  | openai-response |
| `o3-pro-2025-06-10` | ✅ | 入 $20 · 出 $80 /1M |  | openai-response |
| `o4-mini` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $0.5 /1M |  | openai |
| `o4-mini-2025-04-16` | ✅ | 入 $1.1 · 出 $4.4 · 缓存 $0.5 /1M |  | openai |
| `omni-moderation-2024-09-26` | ✅ | 入 $0.2 · 出 $0.2 /1M |  | openai |
| `omni-moderation-latest` | ✅ | 入 $0.2 · 出 $0.2 /1M |  | openai |
| `sora-2` | 🏷 sora_official | **$0.15 / 次** |  | openai |
| `sora-2-pro` | 🏷 sora_official | **$0.8 / 次** |  | openai |
| `text-embedding-3-large` | ✅ | 入 $0.13 · 出 $0.13 /1M |  | openai,embeddings |
| `text-embedding-3-small` | ✅ | 入 $0.02 · 出 $0.02 /1M |  | openai,embeddings |
| `text-embedding-ada-002` | ✅ | 入 $0.1 · 出 $0.1 /1M |  | openai,embeddings |
| `tts-1` | ✅ | 入 $30 · 出 $30 /1M |  | openai |
| `tts-1-hd` | ✅ | 入 $60 · 出 $60 /1M |  | openai |
| `whisper-1` | ✅ | 入 $60 · 出 $0 /1M |  | openai |

### 阿里巴巴（76）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `qvq-max` | ✅ | 入 $1.2 · 出 $4.8 /1M |  | openai |
| `qvq-max-2025-05-15` | ✅ | 入 $1 · 出 $4 /1M |  | openai |
| `qvq-max-latest` | ✅ | 入 $1.2 · 出 $4.8 /1M |  | openai |
| `qvq-plus` | ✅ | 入 $0.28 · 出 $0.7 /1M |  | openai |
| `qvq-plus-2025-05-15` | ✅ | 入 $0.28 · 出 $0.7 /1M |  | openai |
| `qwen-long` | ✅ | 入 $0.07 · 出 $0.28 /1M |  | openai |
| `qwen-max` | ✅ | 入 $1.6 · 出 $6.4 /1M |  | openai |
| `qwen-max-latest` | ✅ | 入 $1.6 · 出 $6.4 /1M |  | openai |
| `qwen-max-longcontext` | ✅ | 入 $3.2 · 出 $3.2 /1M |  | openai |
| `qwen-mt-plus` | ✅ | 入 $2 · 出 $8 /1M |  | openai |
| `qwen-mt-turbo` | ✅ | 入 $0.2 · 出 $0.5 /1M |  | openai |
| `qwen-plus` | ✅ | 入 $0.4 · 出 $1.2 /1M | 阈值 128000, 256000 | openai |
| `qwen-plus-2025-07-14` | ✅ | 入 $0.4 · 出 $4 /1M |  | openai |
| `qwen-plus-2025-09-11` | ✅ | 入 $0.4 · 出 $4 /1M | 阈值 128000, 256000 | openai |
| `qwen-plus-latest` | ✅ | 入 $0.4 · 出 $1.2 /1M | 阈值 128000, 256000 | openai |
| `qwen-turbo` | ✅ | 入 $0.2 · 出 $0.6 /1M |  | openai |
| `qwen-turbo-2025-07-15` | ✅ | 入 $0.2 · 出 $1.6 /1M |  | openai |
| `qwen-turbo-latest` | ✅ | 入 $0.2 · 出 $0.6 /1M |  | openai |
| `qwen-vl-max` | ✅ | 入 $0.2 · 出 $0.8 /1M |  | openai |
| `qwen-vl-max-latest` | ✅ | 入 $0.2 · 出 $0.8 /1M |  | openai |
| `qwen-vl-ocr` | ✅ | 入 $0.72 · 出 $0.72 /1M |  | openai |
| `qwen-vl-ocr-2025-11-20` | ✅ | 入 $0.044 · 出 $0.07348 /1M |  | openai |
| `qwen-vl-ocr-latest` | ✅ | 入 $0.044 · 出 $0.07348 /1M |  | openai |
| `qwen-vl-plus` | ✅ | 入 $0.2 · 出 $0.6 /1M |  | openai |
| `qwen-vl-plus-latest` | ✅ | 入 $0.2 · 出 $0.6 /1M |  | openai |
| `qwen2-72b-instruct` | ✅ | 入 $2.8572 · 出 $2.8572 /1M |  | openai |
| `qwen3-235b-a22b` | ✅ | 入 $1 · 出 $10 /1M |  | openai |
| `qwen3-235b-a22b-instruct-2507` | ✅ | 入 $1 · 出 $10 /1M |  | openai |
| `qwen3-235b-a22b-thinking-2507` | ✅ | 入 $1.6 · 出 $12.8 /1M |  | openai |
| `qwen3-30b-a3b` | ✅ | 入 $0.2 · 出 $2 /1M |  | openai |
| `qwen3-30b-a3b-instruct-2507` | ✅ | 入 $0.2 · 出 $0.8 /1M |  | openai |
| `qwen3-30b-a3b-thinking-2507` | ✅ | 入 $0.2 · 出 $2.4 /1M |  | openai |
| `qwen3-32b` | ✅ | 入 $0.4 · 出 $4 /1M |  | openai |
| `qwen3-coder-480b-a35b-instruct` | ✅ | 入 $3 · 出 $15 /1M |  | openai |
| `qwen3-coder-flash` | ✅ | 入 $0.5 · 出 $5 /1M | 阈值 32000, 128000, 256000 | openai |
| `qwen3-coder-plus` | ✅ | 入 $5 · 出 $20 /1M | 阈值 32000, 128000, 256000 | openai |
| `qwen3-coder-plus-2025-07-22` | ✅ | 入 $2 · 出 $20 /1M | 阈值 32000, 128000, 256000 | openai |
| `qwen3-coder-plus-2025-09-23` | ✅ | 入 $2 · 出 $20 /1M | 阈值 32000, 128000, 256000 | openai |
| `qwen3-max` | ✅ | 入 $1.2 · 出 $6 /1M | 阈值 32000, 128000 | openai |
| `qwen3-max-2025-09-23` | ✅ | 入 $1.2 · 出 $6 /1M | 阈值 32000, 128000 | openai |
| `qwen3-max-preview` | ✅ | 入 $1.2 · 出 $6 /1M | 阈值 32000, 128000 | openai |
| `qwen3-next-80b-a3b-instruct` | ✅ | 入 $0.15 · 出 $1.2 /1M |  | openai |
| `qwen3-omni-flash` | ✅ | 入 $2 · 出 $20 /1M |  | openai |
| `qwen3-omni-flash-2025-09-15` | ✅ | 入 $2 · 出 $20 /1M |  | openai |
| `qwen3-vl-235b-a22b-instruct` | ✅ | 入 $0.3 · 出 $3 /1M |  | openai |
| `qwen3-vl-235b-a22b-thinking` | ✅ | 入 $0.3 · 出 $3 /1M |  | openai |
| `qwen3-vl-30b-a3b-thinking` | ✅ | 入 $0.12 · 出 $1.2 /1M |  | openai |
| `qwen3-vl-32b-thinking` | ✅ | 入 $0.3 · 出 $3 /1M |  | openai |
| `qwen3-vl-flash` | ✅ | 入 $0.1 · 出 $0.8 /1M |  | openai |
| `qwen3-vl-flash-2025-10-15` | ✅ | 入 $0.1 · 出 $0.8 /1M |  | openai |
| `qwen3-vl-plus` | ✅ | 入 $0.3 · 出 $2.4 /1M | 阈值 32000, 128000 | openai |
| `qwen3-vl-plus-2025-09-23` | ✅ | 入 $0.3 · 出 $2.4 /1M | 阈值 32000, 128000 | openai |
| `qwen3.5-122b-a10b` | ✅ | 入 $0.12 · 出 $0.96 /1M |  | openai |
| `qwen3.5-27b` | ✅ | 入 $0.09 · 出 $0.72 /1M |  | openai |
| `qwen3.5-35b-a3b` | ✅ | 入 $0.06 · 出 $0.48 /1M |  | openai |
| `qwen3.5-397b-a17b` | ✅ | 入 $0.18 · 出 $1.08 /1M |  | openai |
| `qwen3.5-flash` | ✅ | 入 $0.03 · 出 $0.3 /1M | 阈值 128000, 256000 | openai |
| `qwen3.5-flash-2026-02-23` | ✅ | 入 $0.03 · 出 $0.3 /1M | 阈值 128000, 256000 | openai |
| `qwen3.5-plus` | ✅ | 入 $0.114 · 出 $0.684 /1M | 阈值 128000, 256000 | openai |
| `qwen3.5-plus-2026-02-15` | ✅ | 入 $0.114 · 出 $0.684 /1M | 阈值 128000, 256000 | openai |
| `qwen3.6-flash` | ✅ | 入 $75 · 出 $75 /1M | 阈值 256000 | openai |
| `qwen3.6-max-preview` | ✅ | 入 $75 · 出 $75 /1M | 阈值 128000 | openai |
| `qwen3.6-plus` | ✅ | 入 $0.3 · 出 $1.8 /1M | 阈值 256000 | openai |
| `qwq-32b` | ✅ | 入 $0.4 · 出 $1.2 /1M |  | openai |
| `qwq-plus` | ✅ | 入 $0.8 · 出 $2.4 /1M |  | openai |
| `qwq-plus-2025-03-05` | ✅ | 入 $0.8 · 出 $2.4 /1M |  | openai |
| `qwq-plus-latest` | ✅ | 入 $0.8 · 出 $2.4 /1M |  | openai |
| `text-embedding-v4` | ✅ | 入 $0.07 · 出 $0.07 /1M |  | openai,embeddings |
| `wan2.6-i2v` | 🏷 Wan | 入 $1.2 · 出 $1.2 /1M |  | openai |
| `wan2.6-r2v` | 🏷 Wan | 入 $75 · 出 $75 /1M |  | openai |
| `wan2.6-r2v-flash` | 🏷 Wan | 入 $75 · 出 $75 /1M |  | openai |
| `wan2.6-t2v` | 🏷 Wan | 入 $1.2 · 出 $1.2 /1M |  | openai |
| `wan2.7-i2v` | 🏷 Wan | 入 $1.2 · 出 $1.2 /1M |  | openai |
| `wan2.7-r2v` | 🏷 Wan | 入 $75 · 出 $75 /1M |  | openai |
| `wan2.7-t2v` | 🏷 Wan | 入 $1.2 · 出 $1.2 /1M |  | openai |
| `wan2.7-videoedit` | 🏷 Wan | 入 $75 · 出 $75 /1M |  | openai |

### Google（19）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `gemini-2.5-flash` | ✅ | 入 $0.3 · 出 $2.4 · 缓存 $0.2 /1M |  | gemini,openai |
| `gemini-2.5-flash-image` | ✅ | **$0.02 / 次** |  | gemini,openai |
| `gemini-2.5-flash-lite` | ✅ | 入 $0.1 · 出 $0.4 · 缓存 $0.2 /1M |  | gemini,openai |
| `gemini-2.5-flash-nothinking` | ✅ | 入 $0.3 · 出 $2.4 · 缓存 $0.2 /1M |  | gemini,openai |
| `gemini-2.5-pro` | ✅ | 入 $1.25 · 出 $10 · 缓存 $0.2 /1M | 阈值 200000 | gemini,openai |
| `gemini-3-flash-preview` | ✅ | 入 $0.44 · 出 $2.64 · 缓存 $0.2 /1M |  | gemini,openai |
| `gemini-3-pro-image` | ✅ | **$0.09 / 次** |  | gemini,openai |
| `gemini-3-pro-image-preview` | ✅ | **$0.09 / 次** |  | gemini,openai |
| `gemini-3.1-flash-image` | ✅ | **$0.055 / 次** |  | gemini,openai |
| `gemini-3.1-flash-image-preview` | ✅ | **$0.055 / 次** |  | gemini,openai |
| `gemini-3.1-flash-lite` | ✅ | 入 $0.25 · 出 $1.5 · 缓存 $0.2 /1M |  | gemini,openai |
| `gemini-3.1-flash-lite-image` | ✅ | **$0.025 / 次** |  | gemini,openai |
| `gemini-3.1-flash-lite-preview` | ✅ | 入 $0.25 · 出 $1.5 · 缓存 $0.2 /1M |  | gemini,openai |
| `gemini-3.1-pro-preview` | ✅ | 入 $2 · 出 $12 · 缓存 $0.2 /1M | 阈值 200000 | gemini,openai |
| `gemini-3.5-flash` | ✅ | 入 $1.5 · 出 $9 · 缓存 $0.2 /1M |  | gemini,openai |
| `gemini-3.5-flash-lite` | ✅ | 入 $0.3 · 出 $2.502 · 缓存 $0.2 /1M |  | gemini,openai |
| `gemini-3.6-flash` | ✅ | 入 $1.5 · 出 $7.5 /1M |  | gemini,openai |
| `gemini-3.7-flash` | ✅ | 入 $0.75 · 出 $3.75 /1M |  | gemini,openai |
| `gemini-3.8-flash` | ✅ | 入 $0.75 · 出 $3.75 · 缓存 $0.2 /1M |  | gemini,openai |

### 字节跳动（13）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `doubao-seedance-2-0-260128` | 🏷 SeeDance2 | 入 $46 · 出 $46 /1M |  | openai |
| `doubao-seedance-2-0-fast-260128` | 🏷 SeeDance2 | 入 $37 · 出 $37 /1M |  | openai |
| `doubao-seedance-2-0-mini-260615` | 🏷 SeeDance2 | 入 $23 · 出 $23 /1M |  | openai |
| `doubao-seedance-2-5-260628` | 🏷 SeeDance2 | 入 $70 · 出 $70 /1M |  | openai |
| `seed-2-0-code-preview-260328` | ✅ | 入 $0.5 · 出 $3 /1M |  | openai |
| `seed-2-0-lite-260428` | ✅ | 入 $0.25 · 出 $2 /1M |  | openai |
| `seed-2-0-mini-260428` | ✅ | 入 $0.1 · 出 $0.4 /1M |  | openai |
| `seed-2-0-pro-260328` | ✅ | 入 $0.5 · 出 $3 /1M |  | openai |
| `seedream-4-0-250828` | ✅ | **$0.035 / 次** |  | openai |
| `seedream-4-5-251128` | ✅ | **$0.045 / 次** |  | openai |
| `seedream-5-0-260128` | ✅ | **$0.035 / 次** |  | openai |
| `seedream-5-0-flash-260915` | ✅ | **$0.018 / 次** |  | openai |
| `seedream-5-0-pro-260628` | ✅ | **$0.12 / 次** |  | openai |

### xAI（13）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `grok-3` | ✅ | 入 $3 · 出 $15 /1M |  | openai |
| `grok-3-mini` | ✅ | 入 $0.3 · 出 $1.8 /1M |  | openai |
| `grok-4-1-fast-non-reasoning` | ✅ | 入 $0.2 · 出 $0.5 /1M |  | openai |
| `grok-4-1-fast-reasoning` | ✅ | 入 $0.2 · 出 $0.5 /1M |  | openai |
| `grok-4-fast-non-reasoning` | ✅ | 入 $0.2 · 出 $0.5 /1M |  | openai |
| `grok-4-fast-reasoning` | ✅ | 入 $0.2 · 出 $0.5 /1M |  | openai |
| `grok-4.3` | ✅ | 入 $1.25 · 出 $2.5 · 缓存 $0.32 /1M |  | openai |
| `grok-4.5` | ✅ | 入 $2 · 出 $6 · 缓存 $0.5 /1M |  | openai |
| `grok-4.6` | ✅ | 入 $75 · 出 $75 /1M | 阈值 204800 | openai |
| `grok-4.7` | ✅ | 入 $2 · 出 $6 /1M | 阈值 200000 | openai |
| `grok-imagine-image` | ✅ | **$0.025 / 次** |  | openai |
| `grok-imagine-image-2.0` | ✅ | **$0.055 / 次** |  | openai |
| `grok-imagine-image-quality` | ✅ | **$0.045 / 次** |  | openai |

### 智谱（10）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `glm-4.5` | ✅ | 入 $0.5 · 出 $2 /1M |  | openai |
| `glm-4.5-air` | ✅ | 入 $0.2 · 出 $1 /1M |  | openai |
| `glm-4.5-flash` | ✅ | 入 $0.01 · 出 $0.04 /1M |  | openai |
| `glm-4.5v` | ✅ | 入 $0.5 · 出 $1.5 /1M |  | openai |
| `glm-4.6` | ✅ | 入 $0.5 · 出 $2 /1M |  | openai |
| `glm-4.6v` | ✅ | 入 $0.28 · 出 $0.84 /1M |  | openai |
| `glm-4.7` | ✅ | 入 $0.6 · 出 $2.16 /1M |  | openai |
| `glm-5` | ✅ | 入 $0.56 · 出 $2.52 /1M |  | openai |
| `glm-5.1` | ✅ | 入 $0.84 · 出 $3.36 /1M | 阈值 32768 | openai |
| `glm-5.2` | 🏷 claude_code,default | 入 $1.142 · 出 $3.997 /1M |  | openai,anthropic |

### DeepSeek（8）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `DeepSeek-V3.2-Exp-nothinking` | ✅ | 入 $0.3 · 出 $0.45 /1M |  | openai |
| `DeepSeek-V3.2-Exp-thinking` | ✅ | 入 $0.3 · 出 $0.45 /1M |  | openai |
| `deepseek-r1` | ✅ | 入 $0.57 · 出 $2.28 · 缓存 $0.5 /1M |  | openai |
| `deepseek-v3.2` | ✅ | 入 $0.28 · 出 $0.42 /1M |  | openai |
| `deepseek-v3.2-thinking` | ✅ | 入 $0.28 · 出 $0.42 /1M |  | openai |
| `deepseek-v4-flash` | ✅ | 入 $0.44 · 出 $1.32 · 缓存 $0.4 /1M |  | openai,anthropic |
| `deepseek-v4-flash-vision-exp` | ✅ | 入 $0.44 · 出 $1.32 · 缓存 $0.06667 /1M |  | openai,anthropic |
| `deepseek-v4-pro` | ✅ | 入 $1.74 · 出 $3.48 · 缓存 $0.1667 /1M |  | openai,anthropic |

### Black Forest Labs（5）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `flux-2-flex` | ✅ | **$0.06 / 次** |  | image-generation,openai |
| `flux-2-max` | ✅ | **$0.07 / 次** |  | image-generation,openai |
| `flux-2-pro` | ✅ | **$0.03 / 次** |  | image-generation,openai |
| `flux-kontext-max` | ✅ | **$0.07 / 次** |  | image-generation,openai |
| `flux-kontext-pro` | ✅ | **$0.035 / 次** |  | image-generation,openai |

### Moonshot（5）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `kimi-k2` | ✅ | 入 $0.56 · 出 $2.24 /1M |  | openai |
| `kimi-k2-128k` | ✅ | 入 $0.56 · 出 $2.24 /1M |  | openai |
| `kimi-k2-instruct` | ✅ | 入 $0.56 · 出 $0.56 /1M |  | openai |
| `kimi-k2.5` | ✅ | 入 $0.6 · 出 $3.15 · 缓存 $0.3333 /1M |  | openai |
| `kimi-k2.6` | ✅ | 入 $0.95 · 出 $4 · 缓存 $0.3368 /1M |  | openai |

### MiniMax（2）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `MiniMax-M2.1` | ✅ | 入 $0.3 · 出 $1.2 /1M |  | openai |
| `MiniMax-M2.5` | ✅ | 入 $0.3 · 出 $1.2 /1M |  | openai |

### 美团（1）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `longcat-flash-chat` | ✅ | 入 $0.25 · 出 $0.5 /1M |  | openai |

### 阶跃星辰（1）

| 模型 ID | 分组 | 价格 | 阶梯 | 端点 |
|---|---|---|---|---|
| `step-3.5-flash` | ✅ | 入 $0.1 · 出 $0.3 /1M |  | openai |

## 附录：tiered_expr 表达式原文

- `gemini-2.5-pro`: `len <= 200000 ? tier("≤ 200K", p * 1.25 + c * 10 + cr * 0.125) : tier("> 200K", p * 2.5 + c * 15 + cr * 0.25)`
- `gemini-3.1-pro-preview`: `len <= 200000 ? tier("≤ 200K", p * 2 + c * 12 + cr * 0.2) : tier("> 200K", p * 4 + c * 18 + cr * 0.4)`
- `glm-5.1`: `len <= 32768 ? tier("≤ 32768", p * 0.84 + c * 3.36) : tier("> 32768", p * 1.14 + c * 3.99)`
- `gpt-5.4`: `len <= 271999 ? tier("≤ 271999", p * 2.5 + c * 15 + cr * 0.25) : tier("> 271999", p * 5 + c * 22.5 + cr * 0.5)`
- `gpt-5.4-pro`: `len <= 271999 ? tier("≤ 271999", p * 30 + c * 180) : tier("> 271999", p * 60 + c * 270)`
- `gpt-5.5`: `len <= 271999 ? tier("≤ 271999", p * 5 + c * 30 + cr * 0.5) : tier("> 271999", p * 10 + c * 45 + cr * 1)`
- `gpt-5.5-pro`: `len <= 271999 ? tier("≤ 271999", p * 30 + c * 180) : tier("> 271999", p * 60 + c * 270)`
- `gpt-5.6-luna`: `len <= 272000 ? tier("≤ 272K", p * 0.2 + c * 1.2 + cr * 0.02 + cc * 0.25) : tier("> 272K", p * 0.4 + c * 1.8 + cr * 0.04 + cc * 0.5)`
- `gpt-5.6-sol`: `len <= 272000 ? tier("≤ 272K", p * 4 + c * 20 + cr * 0.4 + cc * 5) : tier("> 272K", p * 8 + c * 30 + cr * 0.8 + cc * 10)`
- `gpt-5.6-terra`: `len <= 272000 ? tier("≤ 272K", p * 2 + c * 12 + cr * 0.2 + cc * 2.5) : tier("> 272K", p * 4 + c * 18 + cr * 0.4 + cc * 5)`
- `gpt-6-astra`: `len <= 272000 ? tier("≤ 272K", p * 10 + c * 50 + cr * 1 + cc * 12.5) : tier("> 272K", p * 20 + c * 75 + cr * 2 + cc * 25)`
- `gpt-6-luna`: `len <= 272000 ? tier("≤ 272K", p * 0.1 + c * 0.5 + cr * 0.01 + cc * 0.125) : tier("> 272K", p * 0.2 + c * 0.75 + cr * 0.02 + cc * 0.25)`
- `gpt-6-sol`: `len <= 272000 ? tier("≤ 272K", p * 2 + c * 10 + cr * 0.2 + cc * 2.5) : tier("> 272K", p * 4 + c * 15 + cr * 0.4 + cc * 5)`
- `gpt-image-2`: `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`
- `gpt-image-2.5-flare`: `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`
- `gpt-image-2.5-sunburst`: `tier("standard", p * 5 + cr * 1.25 + img * 8 + img_cr * 2 + c * 30)`
- `grok-4.6`: `len <= 204800 ? tier("≤ 204800", p * 2 + c * 6) : tier("204801–512K", p * 4 + c * 12)`
- `grok-4.7`: `len <= 200000 ? tier("≤ 200K", p * 2 + c * 6) : tier("> 200K", p * 4 + c * 12)`
- `qwen-plus`: `len <= 128000 ? tier("≤ 128K", p * 0.109589041096 + c * 0.27397260274) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 0.328767123288 + c * 2.7397260274) : tier("256001–1M", p * 0.657534246575 + c * 6.57534246575)`
- `qwen-plus-2025-09-11`: `len <= 128000 ? tier("≤ 128K", p * 0.109589041096 + c * 0.27397260274) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 0.328767123288 + c * 2.7397260274) : tier("256001–1M", p * 0.657534246575 + c * 6.57534246575)`
- `qwen-plus-latest`: `len <= 128000 ? tier("≤ 128K", p * 0.109589041096 + c * 0.27397260274) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 0.328767123288 + c * 2.7397260274) : tier("256001–1M", p * 0.657534246575 + c * 6.57534246575)`
- `qwen3-coder-flash`: `len <= 32000 ? tier("≤ 32K", p * 0.13698630137 + c * 0.547945205479) : len >= 32001 && len <= 128000 ? tier("32001–128K", p * 0.205479452055 + c * 0.821917808219) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 0.342465753425 + c * 1.3698630137) : tier("256001–1M", p * 0.684931506849 + c * 3.42465753425)`
- `qwen3-coder-plus`: `len <= 32000 ? tier("≤ 32K", p * 0.547945205479 + c * 2.19178082192) : len >= 32001 && len <= 128000 ? tier("32001–128K", p * 0.821917808219 + c * 3.28767123288) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 1.3698630137 + c * 5.47945205479) : tier("256001–1M", p * 2.7397260274 + c * 27.397260274)`
- `qwen3-coder-plus-2025-07-22`: `len <= 32000 ? tier("≤ 32K", p * 0.547945205479 + c * 2.19178082192) : len >= 32001 && len <= 128000 ? tier("32001–128K", p * 0.821917808219 + c * 3.28767123288) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 1.3698630137 + c * 5.47945205479) : tier("256001–1M", p * 2.7397260274 + c * 27.397260274)`
- `qwen3-coder-plus-2025-09-23`: `len <= 32000 ? tier("≤ 32K", p * 0.547945205479 + c * 2.19178082192) : len >= 32001 && len <= 128000 ? tier("32001–128K", p * 0.821917808219 + c * 3.28767123288) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 1.3698630137 + c * 5.47945205479) : tier("256001–1M", p * 2.7397260274 + c * 27.397260274)`
- `qwen3-max`: `len <= 32000 ? tier("≤ 32K", p * 0.342465753425 + c * 1.3698630137) : len >= 32001 && len <= 128000 ? tier("32001–128K", p * 0.547945205479 + c * 2.19178082192) : tier("128001–256K", p * 0.958904109589 + c * 3.83561643836)`
- `qwen3-max-2025-09-23`: `len <= 32000 ? tier("≤ 32K", p * 0.821917808219 + c * 3.28767123288) : len >= 32001 && len <= 128000 ? tier("32001–128K", p * 1.3698630137 + c * 5.47945205479) : tier("128001–256K", p * 2.05479452055 + c * 8.21917808219)`
- `qwen3-max-preview`: `len <= 32000 ? tier("≤ 32K", p * 0.821917808219 + c * 3.28767123288) : len >= 32001 && len <= 128000 ? tier("32001–128K", p * 1.3698630137 + c * 5.47945205479) : tier("128001–256K", p * 2.05479452055 + c * 8.21917808219)`
- `qwen3-vl-plus`: `len <= 32000 ? tier("≤ 32K", p * 0.13698630137 + c * 1.3698630137) : len >= 32001 && len <= 128000 ? tier("32001–128K", p * 0.205479452055 + c * 2.05479452055) : tier("128001–256K", p * 0.41095890411 + c * 4.1095890411)`
- `qwen3-vl-plus-2025-09-23`: `len <= 32000 ? tier("≤ 32K", p * 0.13698630137 + c * 1.3698630137) : len >= 32001 && len <= 128000 ? tier("32001–128K", p * 0.205479452055 + c * 2.05479452055) : tier("128001–256K", p * 0.41095890411 + c * 4.1095890411)`
- `qwen3.5-flash`: `len <= 128000 ? tier("≤ 128K", p * 0.027397260274 + c * 0.27397260274) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 0.109589041096 + c * 1.09589041096) : tier("256001–1M", p * 0.164383561644 + c * 1.64383561644)`
- `qwen3.5-flash-2026-02-23`: `len <= 128000 ? tier("≤ 128K", p * 0.027397260274 + c * 0.27397260274) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 0.109589041096 + c * 1.09589041096) : tier("256001–1M", p * 0.164383561644 + c * 1.64383561644)`
- `qwen3.5-plus`: `len <= 128000 ? tier("≤ 128K", p * 0.109589041096 + c * 0.657534246575) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 0.27397260274 + c * 1.64383561644) : tier("256001–1M", p * 0.547945205479 + c * 3.28767123288)`
- `qwen3.5-plus-2026-02-15`: `len <= 128000 ? tier("≤ 128K", p * 0.109589041096 + c * 0.657534246575) : len >= 128001 && len <= 256000 ? tier("128001–256K", p * 0.27397260274 + c * 1.64383561644) : tier("256001–1M", p * 0.547945205479 + c * 3.28767123288)`
- `qwen3.6-flash`: `len <= 256000 ? tier("≤ 256K", p * 0.164383561644 + c * 0.986301369863) : tier("256001–1M", p * 0.657534246575 + c * 3.94520547945)`
- `qwen3.6-max-preview`: `len <= 128000 ? tier("≤ 128K", p * 1.23287671233 + c * 7.39726027397) : tier("128001–256K", p * 2.05479452055 + c * 12.3287671233)`
- `qwen3.6-plus`: `len <= 256000 ? tier("≤ 256K", p * 0.27397260274 + c * 1.64383561644) : tier("256001–1M", p * 1.09589041096 + c * 6.57534246575)`

## 备注

- **Claude 系全线下线**（LZ 页面 FAQ：资源不足暂时下线，恢复看公告）——与我方 09-27 起 pause 的状态一致，恢复后按 HANDOVER 流程 unpause。
- glm-5.2 同时在 claude_code + default 组，default 可调用。
- gpt-image-1/1-mini/1.5/2 有按量+按次双线路；Enterprise 组 = 官方价 ×1.2，sora_official 组官转按 token。
- 视频模型（wan2.x / seedance）按任务或时长计费，详见 LZ Wan / Seedance 2 文档。
- 页面口径 249 vs API 252：API 含多分组条目（如 gpt-image-2 按次线），以 API 为准。

---
生成：2026-09-30 09:18 UTC；采集：`GET https://api.laozhang.ai/api/pricing`（Authorization: 控制台 token）。