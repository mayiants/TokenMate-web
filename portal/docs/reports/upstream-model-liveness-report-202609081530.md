# Upstream Model Liveness Probe Report

Status: ACTIVE
Date: 2026-09-08
Tool: `scripts/probe-upstream-models` (portal repo)

## Why

The catalog aggregates 160+ models from seven upstream providers. Several
turned out to be unservable (TR "No available channel", reseller group empty,
deprecated names) — see the MiniMax-H3 and dead-model investigations in
HANDOVER (2026-09-06/08). This report is the first full active probe of every
catalog model.

## Method

One minimal direct request per model to its upstream, bypassing the platform
(a dead model then costs nothing and never reaches user billing):

- text: `POST /chat/completions`, `max_tokens=16` (some providers reject <16)
- embedding: `POST /embeddings`, one-token input
- image/video/audio: skipped (a real generation costs money) — 44 models
- providers without OpenAI-compatible APIs (Bedrock, Mantle): skipped — 32 models
- upstream keys are rotated per request; 429s retry on the next key

Classification: `ok` = HTTP 200; `dead` = "No available channel" /
model_not_found (safe to pause); `error` = anything else (needs judgment).

## Results (round 3, 2026-09-08 15:20 UTC)

- ok=102, dead=2, error=18, skipped=44 (1 residual 429 on a free model that
  is rate-limited but alive)

### Paused — unambiguously dead (17)

tokenrouter (8):

- miromind/mirothinker-1-7-deepresearch{,-mini} — no channel
- claude-opus-4.7-fast — "No endpoints found for anthropic"
- deepseek/deepseek-v3.2 — renamed away; TR now serves deepseek-v4-pro /
  v4-flash / v4-flash-vision-exp (the v4 entries probe ok separately)
- openai/gpt-5 — "invalid model ID"
- xiaomi/mimo-v2-{flash,omni,pro} — "This model has been deprecated"

laozhang (9): all claude-* (fable-5, opus-4-6/4-6-thinking/4-7/4-7-thinking/4-8,
sonnet-4-6/4-6-thinking/sonnet-5) — reseller answers 503 "Current group ...
has no available channel". laozhang glm-5.2 is fine. Reactivate with one SQL
if the reseller restores the channel.

Paused earlier same day: gemini-3.1-flash-lite-image, gpt-5-image,
gpt-5-image-mini, mai-image-2.5 (TR no channel), openai/gpt-5.4-image-2
(alive on TR chat but unreachable from both our endpoints),
qwen/qwen3.8-max-free, deepseek/deepseek-v4-pro-0813-free, happyhorse-1.0-i2v.

### Alive but needs attention (kept active)

- huawei openpangu-2.0-flash — auth 403: the upstream key likely lacks access
  to this model; check with the provider or remove the model.
- openai/gpt-5.3-codex — TR serves it on `/v1/responses` only; chat returns
  404. Users get a clean error, no billing. Consider native responses-route
  support or pause.
- nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free — free-model rate limit
  (429) at probe time; alive.
- gpt-audio / gpt-audio-mini — TR has channels; requires audio modality.
  Proper call path still untested (no audio test scenario yet).
- 8 video models — not probed (media cost); all billing_mode=media and
  chat-endpoint-guarded. Verify individually before promoting to customers.
- 32 Bedrock/Mantle models — not probed (no OpenAI-compatible API); reconcile
  against `aws bedrock list-foundation-models` instead.

## Recommended operating procedure

1. New model onboarding: must pass `go run ./scripts/probe-upstream-models
   -provider <p>` before activation.
2. Weekly: full probe + `docs/reports/` update; `-pause-dead` for the
   unambiguous dead only.
3. Media models: probe on demand only (`-media` flag), never in bulk.
4. Bedrock/Mantle: separate reconciliation via the AWS model-list API.
5. Wire passive health: proxy already reports model failures
   (`ReportModelFailure`); surface sustained-failure models in admin UI.
