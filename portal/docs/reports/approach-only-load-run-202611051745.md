# approach-only Load Run #3 — 2026-11-05 17:45 (CST)

Status: HISTORICAL (TR restored to active after this run)
Harness: workspace/approach-test/harness-run-202611051745.py (identical to
run #2's validated harness, 48 calls). Key: `sk-wgit-en-1f723e3a-…`
(dailyuse, enterprise, group cn-model, user jedi@aynbay.com — user-specified,
same key as runs #1/#2). Base: https://api.woodgit.com.
Setup: tokenrouter `z-ai/glm-5.3` + `moonshotai/kimi-k3` paused → both
logical models route to `approach` only; verified via probe usage_record
(provider=approach).

## Result: glm-5.3 0/24 (total failure), kimi-k3 20/24

| group | glm-5.3 (run #1 → #2 → **#3**) | kimi-k3 (run #1 → #2 → **#3**) |
|---|---|---|
| non-stream small | 8.5s → 3.9s → **524 (125s)** | 2.8s → 8.6s → **200 23.4s** |
| stream small | 1.7s → 1.5s → **503** | 5.4s → 3.5s → **200 3.8s** |
| non-stream medium 8k | 23.4s → 502 → **503** | 21.7s → 524 → **200 26.3s** |
| stream medium 8k | 16.2s → 524 → **503** | 16.8s → 200 105s → **200 5.3s** |
| tool-call non-stream | 2.4s → 200 93.6s → **503** | 3.9s → 4.8s → **200 5.0s** |
| tool-call stream | 2.0s → 2.2s → **503** | 5.3s → 5.2s → **200 6.1s** |
| burst ×12 | 12/12 → 12/12 → **0/12 (503)** | 12/12 → 12/12 → **12/12 200, avg 11s** |
| large 40k (ns / s) | 524/524 → 524/524 → **503/503** | 524/524 → 524/524 → **502 / 524** |
| throughput 16k ×3 | 3/3 → 0/3 → **0/3 (503)** | 2/3 → 0/3 → **1/3 (2×524, 1×200 54s)** |
| anthropic /v1/messages | 2.4s → 503 → **503** | 3.1s → 503 → **200 3.9s** |

usage_records (key dailyuse, window 09:44–10:00 UTC): **glm-5.3 zero records
in the entire run** — the first call hung at the upstream >125s (CF 524,
no usage row), then every subsequent glm call failed fast at the proxy with
503 `no available upstream keys for model: glm-5.3 after 3 retries`.
kimi-k3: 20 success rows (avg 12.6s), failures are the usual CF 524/502
large-context/throughput walls.

## What happened (mechanism)

1. Run opened with `g1_nonstream_small` — the approach **glm-5.3 endpoint
   hung** (no response within CF's 125s; no usage row was ever written).
2. The proxy counts that as an upstream failure on its **single** approach
   key and puts (key, glm-5.3) into cooldown → with tokenrouter paused there
   is no other candidate → every later glm request 503s immediately.
3. kimi-k3 on the **same key** kept working all run (its model-level health
   was fine) — confirming the wedge is at the upstream *model endpoint*
   level, not the approach account/key/network.

## Cross-run trend (same harness, same key, same day)

- **glm-5.3 monotonically degrading**: full pass at 11:17 → partial +
  key-pull at 15:04 → total outage at 17:45. Approach's glm-5.3 upstream was
  already unhealthy (daily aggregate avg latency ~20s, errors accumulating).
- **kimi-k3 interactive traffic stable across all three runs** (small /
  tool-call / 12-burst all pass); only the structural CF-timeout walls
  (40k context, long 16k-output) fail consistently.

## Post-run verification

- TR `glm-5.3`/`kimi-k3` restored to `active`; proxy re-synced (~75s).
- Probe glm-5.3 → 200 in 2.4s, usage_record provider=**approach** (priority 0
  intact) — approach's glm endpoint recovered after the load stopped; no
  residual production risk.

## Net

approach as the **sole** provider for glm-5.3 is not production-viable: one
wedged model endpoint + one key = 100% glm outage, and the endpoint has now
demonstrated load-induced wedging three times in one day (progressively
worse). kimi-k3 interactive loads are fine on approach. Recommendations
unchanged and now stronger: (a) keep tokenrouter as active co-provider for
both models; (b) add a second approach upstream key to break the SPOF;
(c) treat 40k-context / long-generation traffic as requiring TR/AWS paths
until approach latency or the CF wall-clock ceiling changes.
