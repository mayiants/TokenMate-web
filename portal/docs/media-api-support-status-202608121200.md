# Media API Support Status

> Status: ACTIVE  
> Created: 2026-08-12 12:00 UTC  
> Scope: ai-proxy-go media endpoints (image / video / audio) and upstream coverage

This doc describes the current support for non-text media APIs in the WOODGIT platform: which endpoints exist, how they bill, which upstream providers/models are wired, and what is still missing.

---

## 1. Supported REST Endpoints

All endpoints live under the standard OpenAI-compatible base path (`/v1` by default).

| Endpoint | Method | Billing | Async polling | Notes |
|---|---|---|---|---|
| `/v1/images/generations` | POST | Per request (`fixed_request`) | `GET /v1/images/generations/:task_id` | OpenAI-compatible. Detects upstream async envelopes and returns a unified `{task_id, type, created}` envelope. |
| `/v1/video/generations` | POST | Per request (`fixed_request`) | `GET /v1/video/generations/:task_id` | TokenRouter-style payload. Requires `model` query param on poll. |
| `/v1/audio/transcriptions` | POST | Per request (`fixed_request`) | n/a | Whisper-only. Hard-coded to provider `openai` and model `whisper-1`. |
| `/v1/audio/translations` | POST | Per request (`fixed_request`) | n/a | Whisper-only. Hard-coded to provider `openai` and model `whisper-1`. |

### Async envelope format

When an upstream returns a task reference, the proxy normalizes it to:

```json
{
  "task_id": "task_xxx",
  "type": "video.generation",
  "created": 1234567890
}
```

Poll endpoints forward the upstream status payload unchanged. TokenRouter video polling expects `?model=<model>` because the upstream key pool resolution needs the logical model name.

---

## 2. Image Generation

### Request format

OpenAI `POST /v1/images/generations`:

```json
{
  "model": "bytedance-seed/seedream-5.0-lite",
  "prompt": "a cat",
  "size": "1920x1920",
  "n": 1
}
```

### Supported upstreams

- **TokenRouter**: any model whose `provider` resolves to a TokenRouter upstream key. The proxy forwards to `POST <upstream_base>/images/generations`.
- **AWS Bedrock**: attempted first when the routing track is `bedrock`; falls back to TokenRouter if the model is not available on Bedrock.
- **Amazon Mantle**: attempted first when track is `mantle`; falls back to TokenRouter.

### Billing

`Billing.CalculateImageCost(model, size, quality, n)` is called. Current implementation uses the flat `fixed_request` price stored in `model_pricing_rules`; size/quality multipliers are not yet applied.

### Known limitations

- Image models with **resolution-dependent prices** (e.g. `Output Pixels <= 236W:$0.045 / image`, `Output Pixels > 236W:$0.090 / image`) cannot be expressed accurately by a single `fixed_request` field. The portal admin UI currently shows one flat price.
- `size` minimums are enforced by upstreams (e.g. `seedream-5.0-lite` requires ≥ 3,686,400 pixels), not by the proxy.

---

## 3. Video Generation

### Request format

TokenRouter `POST /v1/video/generations`:

```json
{
  "model": "kling-v3",
  "prompt": "a robot dancing",
  "size": "1280x720",
  "duration": 5
}
```

Optional fields accepted and forwarded as-is: `image`, `images`, `audios`, `videos`, `first_frame_image`, `input_reference`, `metadata`.

### Supported upstreams

- **TokenRouter** only. The proxy resolves the provider/key and forwards to `POST <upstream_base>/video/generations`.

### Billing

Same per-request path as images. The proxy calls `CalculateImageCost` (legacy name) with `size` and `n=1`. Flat `fixed_request` price is used.

### Known limitations

- Video models with **per-second** or **per-block** pricing (e.g. `MiniMax-H3`: `2k:$0.130 / s`, `768P:$0.090 / s`) are flattened to one `fixed_request` value. Duration and resolution are not factored into billing yet.
- Audio/image references inside a video request are forwarded but not separately billed.

---

## 4. Audio

### Currently supported: transcription / translation only

`POST /v1/audio/transcriptions` and `POST /v1/audio/translations` are implemented, but:

- They are **hard-coded to OpenAI Whisper** (`model=whisper-1`).
- They do **not** route through provider resolution or key pools by model name; they call `getUpstreamKey("openai", ...)` directly.
- They require the key's allowed-models list to contain `whisper-1`.

### Not yet supported

- **Audio generation / text-to-speech (TTS)**: no `POST /v1/audio/speech` endpoint.
- **Native audio input to chat models**: chat completions forward audio URLs/arrays upstream but do not count audio tokens separately.
- **Non-OpenAI audio models** (e.g. AWS Nova Sonic, Azure Speech, ElevenLabs) are not wired.

---

## 5. AWS / Bedrock Media Caveats

### Bedrock image models

The proxy has a `resolveBedrockProviderAndKey` path for image generation, but the upstream payload must be OpenAI-compatible because the proxy forwards to `<base>/images/generations`. Bedrock native image APIs (`InvokeModel` with Nova Canvas / Titan Image) require a different request/response shape and are **not** currently supported.

### AWS Nova Sonic (audio)

AWS Nova Sonic is a **bidirectional streaming audio model** (WebSocket / `InvokeModelWithResponseStream`). It is **not supported** by the current proxy architecture because:

1. The proxy is request/response HTTP based; Nova Sonic requires a persistent WebSocket or streaming RPC session.
2. Pricing is per input audio second + output audio second + text tokens, which does not map to the existing `per_token`/`per_request` schemas.
3. Credentialing uses AWS SigV4 on Bedrock endpoints, not a static bearer token.

Adding Nova Sonic would require a new WebSocket gateway module, a separate Bedrock SigV4 signer, and a new billing unit (audio seconds + text tokens).

### Amazon Mantle

Mantle is routed like Bedrock but uses a different key pool. Image generation falls back to TokenRouter if the model is unavailable.

---

## 6. Model Classification in Portal

- Primary `model_type` is stored in `service_models.aimodels_data->>'type'` and `capabilities->>'type'`.
- Multi-type models keep **text as primary** and list additional modalities in `capabilities->'supported_types'` (e.g. `["text", "image"]`).
- The public Models Gallery type filter matches **primary type only**.

Current TokenRouter media counts (primary type):

| Type | Count |
|---|---|
| text | 104 |
| image | 8 |
| video | 8 |
| audio | 0 (audio-capable models are text-primary + supported_types audio) |

---

## 7. Recommended Next Steps

1. **Variant pricing schema**: add `capabilities.pricing.variants` (resolution / duration / sample-rate tiers) so image/video/audio models can bill accurately.
2. **`POST /v1/audio/speech`**: add TTS endpoint with provider resolution.
3. **Native audio in chat completions**: separate audio token pricing for models like `gpt-audio-mini`.
4. **AWS Nova Sonic**: design a WebSocket/SigV4 gateway if there is product demand.
5. **Video with audio references**: optionally bill referenced audio assets separately or fold them into the per-video price.
