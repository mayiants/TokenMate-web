# TR vs laozhang Provider Comparison & Routing Recommendation

> Status: ACTIVE
> Date: 2026-09-09
> Data: live `/v1/models` + `/api/pricing` from both providers (TR 136 models, laozhang 231), production keys. Text prices = $/1M tokens, low tier where tiered.

## TL;DR

- **Overlap (46 text models): laozhang cheaper on 17, TR on 4, equal on 25.** laozhang wins the qwen/glm/gemini-flash families by 30–75%; TR's few wins are decisive (gpt-oss-120b is 13× cheaper on TR).
- **Capacity is the decisive operational axis**: laozhang sustains ~965 RPM/key with zero throttling; TR caps at ~8–15 RPM per (key, model). For equal-price models, prefer laozhang and keep TR as fallback.
- **Coverage is complementary**: TR-only = latest claude (fable-5.1/opus-5), gpt-image-2.5, gemini-embedding, hy4, MiniMax/kling video. laozhang-only = seedream/sora/flux image gen, cheap qwen/glm variants, grok-imagine.

## Price comparison — overlapping text models (low tier, $/1M in/out)

| Family | Model | TR | laozhang | Prefer |
|---|---|---|---|---|
| qwen | qwen3.5-flash | 0.105/0.419 | **0.027/0.274** | LZ (4×) |
| qwen | qwen3.5-35b-a3b | 0.163/1.300 | **0.060/0.480** | LZ (2.7×) |
| qwen | qwen3.5-397b-a17b | 0.390/2.340 | **0.180/1.080** | LZ (2.2×) |
| qwen | qwen3.6-plus | 0.540/3.210 | **0.274/1.644** | LZ (2×) |
| qwen | qwen3.6-flash | 0.171/1.029 | 0.164/0.986 | LZ (≈equal, slight) |
| z-ai | glm-5 | 1.000/3.200 | **0.560/2.520** | LZ |
| z-ai | glm-5.2 | 1.400/4.400 | **1.142/3.997** | LZ |
| z-ai | glm-4.6/4.7 | 0.600/2.200 | 0.500–0.600/2.16–2.20 | ≈equal |
| google | gemini-3.8-flash | 1.350/6.750 | **0.750/3.750** | LZ (1.8×) |
| google | gemini-3.7-flash | 0.750/3.750 | 0.750/3.750 | equal |
| google | gemini-3.5/3.6-flash | 1.500/7.5–9.0 | 1.500/7.5–9.0 | equal |
| google | gemini-3.1-pro | 4.000/18.000 | **2.000/12.000** | LZ |
| openai | gpt-5.x (mini/nano/5.2/5.4) | — | same numbers | equal |
| openai | gpt-5.6-sol | 5.000/30.000 | **4.000/20.000** | LZ |
| openai | gpt-6-astra | 10.000/50.000 | 10.000/50.000 | equal |
| openai | **gpt-oss-120b** | **0.039/0.180** | 0.500/2.000 | **TR (13×)** |
| deepseek | v4-flash / flash-vision | 0.440/1.320 | 0.440/1.320 | equal |
| deepseek | v4-pro | **1.320/3.960** | 1.740/3.480 | TR (in) / LZ (out) ≈equal |
| x-ai | grok-4.3/4.5/4.6 | 1.25–2.00/2.5–6.0 | same | equal |
| byteplus | seed-2.0 series | same | same | equal |
| misc | step-3.5-flash, gpt-4o-mini | same | same | equal |
| misc | glm-4.5-air | **0.130/0.850** | 0.200/1.000 | TR |
| misc | deepseek-v3.2 | 0.260/0.380 | 0.280/0.420 | ≈TR |

## Unique coverage

**TR-only (86)** — keep TR as the sole source:
- Latest Anthropic: `anthropic/claude-fable-5.1` (10/50), `anthropic/claude-opus-5`, haiku-4.5 — laozhang's claude line stopped at 4.8 and its claude keys were 503-dead earlier (verify before relying).
- `openai/gpt-image-2.5-flare/sunburst` (image), `google/gemini-embedding-2/001`, `tencent/hy4-preview`, `MiniMax-H3`/`kling-*`/`happyhorse` video.
- `openai/gpt-oss-120b` at 0.039/0.180 — cheapest strong model either side.
- `-free` models: gimmick, do not onboard.

**laozhang-only (181)** — keep laozhang as the sole source:
- Image gen: `seedream-4.5/5.0`, `sora-character/2-character`, `flux-kontext-*`, `grok-imagine-image*` (per-image fixed, $0.025–0.055 cost).
- Cheap text: `glm-4.5-flash` (0.005 ratio — but **404 on our current key**, see risks), `qwen3.5-flash-2026-02-23`, `gpt-5-nano`, `qwen-vl-ocr`.
- `multimodal-embedding-v1`, `text-embedding-3-small` ($0.02/1M), `text-embedding-v4`.

## Operational axes

| Axis | TR | laozhang |
|---|---|---|
| RPM per key | **8–15 per (key, model)**, 1-min sliding, failures count | **no practical cap** (965 RPM sustained, zero 429) |
| Rate-limit headers | none | none |
| Dead/unstable models | high (24 paused to date; free models unusable) | low so far; claude line had 503 "reseller group" outage 09-08 |
| Tier semantics | marginal brackets (`tiered_pricing`, matches our `tiered` mode) | whole-request context switch (`tiered_expr`, matches our new `tiered_context` mode) |
| Key variance | per-key model lists vary | **keys are group-scoped** — one of our two laozhang keys sees only 1 model (glm-5.2); the other sees all 231 |

## Recommendation

1. **Overlap + laozhang cheaper** (qwen3.5/3.6, glm-5/5.2, gemini-3.8-flash, gemini-3.1-pro, gpt-5.6-sol): route **laozhang primary**. Implement via group model→provider mapping or keypool priority per model.
2. **Overlap + equal price** (25 models: most gpt-5.x, gemini-3.5/3.6/3.7-flash, deepseek-v4-flash, grok-4.x): route **laozhang primary for capacity** (no RPM cap), TR secondary for redundancy/failover.
3. **TR-only unique** (claude latest, gpt-image-2.5, gemini-embedding, video): TR only — mitigate RPM by key multiplication (each +key adds ~15 RPM on hot models).
4. **Cost exceptions**: `gpt-oss-120b` and `glm-4.5-air` route TR (dramatically cheaper). `deepseek-v4-pro` is a wash.
5. **laozhang-only** (seedream/sora/flux image, cheap qwen/glm variants): laozhang only.
6. **Risks to watch**: (a) laozhang key group-scoping — before adding keys, verify model visibility; (b) TR free models — never onboard; (c) laozhang claude line instability — monitor before making it primary for claude.
