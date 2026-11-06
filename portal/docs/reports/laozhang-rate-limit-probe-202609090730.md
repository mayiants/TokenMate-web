# laozhang Upstream Rate-Limit Probe Report

> Status: ACTIVE
> Date: 2026-09-09 07:30 UTC
> Method: direct-to-upstream bursts with the production `laozhang` upstream key (same methodology as `tr-upstream-rate-limit-probe-202609051500.md`)

## Objective

Determine laozhang per-key RPM/TPM practical limits (TR comparison baseline: 8–15 RPM per (key, model), 1-min sliding window, failures counted).

## Model used

`gpt-5-nano` (cheapest available chat model for this key: model_ratio 0.025 → $0.05/1M in, $0.40/1M out). Note: `glm-4.5-flash` (ratio 0.005) is listed in `/v1/models` and `/api/pricing` but returns 404 `model_not_found` for this key — cheapest *usable* model is gpt-5-nano. Probe cost < $0.01 total.

## Results

| Phase | Load | Result |
|---|---|---|
| A | 60 requests, 30-way parallel (~7s) | 60× HTTP 200, zero 429 |
| B | 200 requests, 100-way parallel (~14s) | 200× HTTP 200, zero 429 |
| C | 50 workers × 60s sustained | **1,008 requests ≈ 965 RPM sustained, 100% 200**, p50 latency ~5.4s (upstream queueing, not throttling) |
| D | 10 parallel × ~4.7K-token prompts (~47K TPM burst) | 10× HTTP 200, 3s total |

- **No `x-ratelimit-*` / `retry-after` response headers at all**; gateway is openresty + one-api/shell-api.
- No 429/5xx observed at any point; errors would likely surface as account-level quota exhaustion, not per-minute windows.

## Conclusion

laozhang has **no practical per-key RPM cap in the TR sense** — at least ≥965 RPM sustained per key with no throttling. Throughput ceiling is latency/queue-bound (~50 concurrent in-flight per key in our test), not rate-limit-bound. TPM dimension not meaningfully probed (47K TPM burst passed instantly); given per-token billing and no observed windows, large TPM is unlikely to be capped either — monitor account quota instead.

## Operational implications

- laozhang-routed models (gemini-3.x-flash, grok-4.6, gpt-6-astra, claude family, deepseek-v4-flash-vision) can absorb large customer traffic without the per-(key,model) RPM fragmentation that plagues TR (single hotspot model on TR caps at ~15×keycount RPM).
- Keypool rpm_limit for laozhang keys can stay 0/unlimited; watch daily account quota instead.
- TR remains the bottleneck provider for hot models — RPM mitigation there is still key multiplication (see TR probe report).
