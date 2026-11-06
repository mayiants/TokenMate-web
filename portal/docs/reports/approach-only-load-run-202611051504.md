# approach-only Load Run #2 — 2026-11-05 15:04 (CST)

Status: HISTORICAL (TR restored to active after this run)
Harness: workspace/approach-test/harness-run-202611050704.py (same validated
harness, real key sk-wgit-en-1f723e3a-…, cn-model). 48 calls, approach-only
(tokenrouter glm-5.3/kimi-k3 paused during the run, restored to active after).

## Result: overall 33/48 success — notably WORSE than run #1 (~39/48)

New failures vs run #1 on previously-passing groups, plus 503
"no available upstream keys for model: … after 3 retries":

| group | glm-5.3 | kimi-k3 |
|---|---|---|
| non-stream small | 200 3.9s | 200 8.6s |
| stream small | 200 1.5s | 200 3.5s |
| non-stream medium 8k | **502** 71.6s | **524** 126s |
| stream medium 8k | **524** 126s | 200 105s |
| tool-call non-stream | 200 93.6s (!) | 200 4.8s |
| tool-call stream | 200 2.2s | 200 5.2s |
| burst ×12 | 12/12 200, avg **15.2s** max 32s | 12/12 200, avg 3.8s |
| large 40k (stream+non) | 524 / 524 | 524 / 524 |
| throughput 16k ×3 | **0/3** (502,503,503) | **0/3** (502,503,503) |
| anthropic /v1/messages | **503** | **503** |

usage_records: 33 success all provider=approach; the 15 failures (502/503/524)
mostly produced no billed record (edge/no-key errors).

## Reading
- The **503 "no available upstream keys"** is the key signal: approach has a
  SINGLE upstream key. Under this run's load the proxy's key-health/circuit
  logic pulled the key (saturation / upstream 5xx), so whole groups 503'd with
  no key to route to.
- Elevated latencies even on small/burst/tool-call (glm burst avg 2.4s→15.2s;
  a tool-call at 93.6s) indicate approach upstream was under more pressure / less
  healthy at 15:04 than at the 11:17 run.
- 40k large-context still fails at the CF ~125s edge timeout (unchanged,
  structural).
- Conclusion reinforced: approach alone is NOT a reliable sole provider for
  glm-5.3/kimi-k3 under sustained/concurrent + large-context load — the single
  key is the bottleneck. More approach keys would raise the ceiling. tokenrouter
  restored as co-provider after this run.
