# approach-only Load Run — 2026-11-05 11:17 (CST)

Status: ACTIVE
Harness: workspace/approach-test/harness-run-202611050317.py (copy of the
validated 202611011814 harness). Key: sk-wgit-en-1f723e3a-… (enterprise, group
cn-model, user jedi@aynbay.com). Base: https://api.woodgit.com.

Setup: tokenrouter service_models `z-ai/glm-5.3` + `moonshotai/kimi-k3` set to
`status='paused'` so both logical models route to the `approach` upstream only.
Verified: 43/43 billed usage_records in the window served by provider=approach,
**0 via tokenrouter**.

48 client calls (24/model): small/medium(8k)/large(40k) × stream & non-stream,
tool-call (stream+non-stream), 12-way concurrent burst, 16k-context throughput
(×3 out=1500), and Anthropic /v1/messages.

## Results (client-observed)

| group | glm-5.3 | kimi-k3 |
|---|---|---|
| non-stream small | 200, 8.5s | 200, 2.8s |
| stream small | 200, 1.7s | 200, 5.4s |
| non-stream medium 8k | 200, 23.4s | 200, 21.7s |
| stream medium 8k | 200, 16.2s | 200, 16.8s |
| tool-call non-stream | 200, 2.4s | 200, 3.9s |
| tool-call stream | 200, 2.0s | 200, 5.3s |
| **burst ×12 (concurrency)** | **12/12 200**, avg 2.4s, max 3.7s | **12/12 200**, avg 7.9s, max 8.1s |
| non-stream large 40k | **524** (125s CF timeout) | **524** (125s) |
| stream large 40k | **524** (125s) | **524** (125s) |
| throughput 16k out=1500 (×3) | 3/3 200, avg 34s, max 51s | 2/3 200 (+1 **502**), avg 60s, max 83s |
| anthropic /v1/messages | 200, 2.4s | 200, 3.1s |

Client success: glm-5.3 20/24, kimi-k3 19/24. All failures are the
large-context (40k) and one 16k-throughput call.

## Reading

- **Small / tool-call / Anthropic / 12-way burst: solid.** Concurrency of 12 was
  fully served (no proxy CONCURRENT_LIMIT and no upstream 429), latencies 2–8s.
- **Medium 8k ≈ 16–23s**, acceptable but not fast.
- **Large 40k fails at the edge**: both stream and non-stream hit CloudFlare 524
  at ~125s — the upstream is still generating past the ~100s edge wall-clock.
  usage_records show these as NOT billed/approach-success from our side, i.e.
  it's a latency/timeout ceiling on big contexts, not approach rejecting the
  request. Matches the 2026-10-24 validation report finding (approach upstream is
  slow; CF ~100s wall caps 40k requests).
- **16k throughput (out=1500)**: mostly 200 but slow (34–83s); kimi-k3 saw one
  502.

## Net
approach handles normal interactive + tool + moderate-concurrency traffic for
glm-5.3/kimi-k3 fine; it is NOT reliable for very large (40k) contexts or long
high-output generations through the CF edge (524/502). Revert plan: set the two
tokenrouter service_models back to `active` and re-sync when the TR comparison
is done.
