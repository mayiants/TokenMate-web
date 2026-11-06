# approach Provider Validation — kimi-k3 & glm-5.3

Status: ACTIVE
Date: 2026-10-24 (09:50–10:30 UTC)
Scope: end-to-end validation of the `approach` upstream provider (models `kimi-k3`, `glm-5.3`) through the woodgit gateway; client-compatibility assessment (curl / raw OpenAI client / workbuddy-style simulation); RPM/TPM probing.

## Test setup

| Item | Value |
|---|---|
| User key | `sk-wgit-en-1f723e3a-...` (enterprise, group `cn-model`) |
| Group limits | concurrent = 10 (system group cap) |
| Upstream | `approach` — single key `sk-pmNd4...OvgY`, base `https://www.tokenservices.store:8443/v1`, portal `rpm_limit=300`, validation_status=valid |
| TR counterparts | `tokenrouter/kimi-k3` paused, `tokenrouter/glm-5.3` paused → all traffic lands on approach |
| Pricing (active) | kimi-k3 $3.0/$15.0 per 1M (in/out), cache $1.5, fixed $0.005; glm-5.3 $1.4/$4.4, cache $0.7, fixed $0.005 |
| Path | client → Cloudflare (api.woodgit.com, **proxied/orange**) → proxy EC2 → approach |

Harness: stdlib-Python OpenAI-compatible client (48 requests, 11 groups × 2 models), plus a workbuddy-style simulation (~100k-token context + 10 tool definitions + non-stream, matching the real workbuddy payload shape seen in production logs on 2026-10-15).

## Results per group

### glm-5.3

| Group | Result | Latency | Notes |
|---|---|---|---|
| g1 non-stream small | 200 | 3.2s | 24 in / 40 out, resp_model=`glm-5.3` |
| g2 stream small | 200 | TTFT 1.0s, 1.6s total | — |
| g3 non-stream ~9.7k ctx | 200 | **40.5s** | 9,685 in / 36 out |
| g4 stream ~9.7k ctx | 200 | **TTFT 47.2s** | TTFT ≈ total: tokens arrive in one burst |
| g5 non-stream ~40k ctx | **CF 524** | 125s | Cloudflare origin timeout (>100s) |
| g6 stream ~40k ctx | **CF 524** | 125s | same |
| g7 tool call non-stream | 200 | 2.2s | correct `tool_calls` (`get_weather{"city":"Beijing"}`), reasoning_content present |
| g8 tool call stream | 200 | TTFT 2.2s | tool_calls deltas reassembled correctly |
| g9 RPM burst 12 concurrent | 10×200, 2×429 | 3.8–7.5s | 429 = **our group concurrent cap (10)**, not upstream |
| g10 throughput 3× 16k ctx → 1500 out | 3×200 | 33–112s | TTFT≈total |
| g11 Anthropic `/v1/messages` | 200 | 7.5s | native passthrough works |

### kimi-k3

| Group | Result | Latency | Notes |
|---|---|---|---|
| g1 non-stream small | 200 | 2.9s | resp_model=**`qianfan-vl-v1`** (see Findings) |
| g2 stream small | 200 | TTFT 2.8s | — |
| g3 non-stream ~10.2k ctx | 200 | **43.3s** | 10,178 in / 2 out |
| g4 stream ~10.2k ctx | 200 | **TTFT 72.1s** | — |
| g5 non-stream ~40k ctx | **CF 524** | 126s | — |
| g6 stream ~40k ctx | **502 (HTML)** | 69s | CF cut mid-request |
| g7 tool call non-stream | 200 | 3.2s | correct `tool_calls`; reasoning_content present |
| g8 tool call stream | 200 | TTFT 2.9s | — |
| g9 RPM burst 12 concurrent | 10×200, 2×429 | 3.8–11s | group cap again; no upstream 429 observed |
| g10 throughput 3× 16k → 1500 out | 3×200 | 36–57s | **only 2–21 output tokens** despite max_tokens=1500 (upstream stopped early) |
| g11 Anthropic `/v1/messages` | 200 | 11.4s | resp_model=`kimi-k3` |

### workbuddy-style simulation (~100k ctx + tools, non-stream)

- glm-5.3 → **CF 524** at 125s (fallback-routed zhipu→approach; zhipu is only a prefix guess, no keys).
- kimi-k3 → 503 `no available upstream keys` — root-caused to key cooldown (see Bugs fixed).

## Billing verification (portal DB)

- glm-5.3: 20 records, 78,147 in / 481 out, **$0.174**
- kimi-k3: 20 records, 82,843 in / 165 out, **$0.262**
- 48 requests sent, 40 billed: all CF-524/502 failures **not billed** — "no success, no charge" confirmed.
- Streaming calls carry no usage in SSE chunks for this upstream, but proxy-side accounting recorded tokens correctly for every stream.

## Findings

1. **Upstream RPM/TPM limits NOT reachable from our side.** The binding constraint is our own group concurrent cap (10) — bursts trip `CONCURRENT_LIMIT_EXCEEDED` (429, proxy-side, ~0.6s fail-fast) before approach sees pressure. Observed sustained: 10 concurrent × ~5s each ≈ 120 req/min small-load without a single upstream 429. approach's true ceiling stays unknown; single-key rpm_limit=300 is configured but unverified against the real upstream limit. More keys from approach would raise the ceiling.
2. **Latency profile is the real constraint.** Small prompts: 2–3s. ≥8k context: 40–73s (TTFT ≈ total — approach buffers and emits in one burst, so streaming gives no incremental-delivery benefit). ≥40k context: exceeds Cloudflare's ~100s origin window → **CF 524 for every client**, including streaming.
3. **Cloudflare is still proxying api.woodgit.com** (orange cloud) despite the earlier grey-cloud plan. Any request >~100s dies at the CDN. For workbuddy-class clients (150k context, non-stream), this is a hard wall. Options: DNS-only (grey cloud) for api.woodgit.com, or a separate direct hostname for heavy clients.
4. **`kimi-k3` is not what it claims upstream.** approach answers with `resp_model=qianfan-vl-v1` — the request is actually served by a Qianfan VL model. glm-5.3 answers as `glm-5.3`. Proxy passes the upstream model name through to the client (billing is by requested model, so revenue is unaffected), but clients that validate the response model name will see a mismatch on k3. Also worth re-negotiating: we pay/price kimi-k3 as a $3/$15 model — if approach silently substitutes a cheaper backbone, margin analysis for k3 is based on unverified upstream reality.
5. **CF error 1010 blocks non-browser user agents.** The default `Python-urllib` UA is banned at the edge (403, browser-signature check). curl and browser UAs pass. Any automated client must set a normal UA — worth documenting for enterprise customers.
6. **glm-5.3 "primary provider zhipu" in fallback logs** is just `relay.GetProviderFromModel` prefix guessing; there are no zhipu keys. Harmless but noisy.
7. **Output capacity (corrected after follow-up probes):** the short outputs in g10 were a test artifact — the embedded question explicitly asked for a one-word answer. Follow-up probes demanding long essays show both models honor the full `max_tokens` budget with `finish_reason=length` at 4096 and 8192. Generation speed: kimi-k3 ~110–145 tok/s (8192 tokens in 56s), glm-5.3 ~54 tok/s (8192 tokens in 152s — streaming survives because TTFT <100s; a non-stream glm answer this long would die at Cloudflare). 16384-token probes also completed for both models. Practical ceiling for coding-agent turns is comfortable; very long glm-5.3 non-stream responses remain CF-bound.

## Bugs found and fixed during this validation (proxy)

1. `70bf77e` — `ModelHealth.RecordFailure` counted client cancellations (`context canceled`, `context deadline exceeded`, `use of closed network connection`) toward the 3-strike model disable with a **24h** recovery. Three client timeouts on 10-23 had taken glm-5.3 down platform-wide for 24h (single-key provider, TR paused → no fallback). Now filtered at the RecordFailure entry point, covering all handlers (chat/images/audio/video/embeddings/moderation). Regression test added.
2. `a19f25b` — `ReportKeyFailure` counted the same cancellation class toward the per-key failure counter (`Failures >= 3` → 60s cooldown; `Failures` never resets on success, only decays 1 per cooldown cycle). During the 2026-10-24 run, three CDN-cut 40k–100k requests put the only approach key into cooldown → 503 for **every** model on the provider. Cancellations are now ignored at key level; genuine upstream errors still count. Regression test added.

3. `e201e74` — `convert.ResponsesToOpenAI` rewrote every Responses-format tool as a function tool. Codex CLI sends a `web_search`-typed tool (no name); the conversion produced an empty-name function tool and approach/qianfan rejected the entire request (`the type of the tool can only be function` → every codex turn failed with `UPSTREAM_BAD_REQUEST`). Non-function tool types and nameless tools are now dropped during conversion (regression test added); after deploying, the captured codex payload replays 200 and codex CLI completes end-to-end.
4. **Codex CLI compatibility — 7 more fixes, one root cause.** After fixing #3, codex still failed with `stream disconnected before completion` (5/5 reconnects). A MITM tee plus codex's own `RUST_LOG=debug` revealed the true root cause: **our converted SSE events were not terminated by a blank line**, so codex's spec-compliant parser merged the whole stream into one event and failed JSON parsing (`trailing characters at line 2 column 1`). curl/AI-SDK parse leniently, which is why every other client worked. Fixes, in order of discovery (all deployed, proxy `main` up to `564d8fc`):
   - `7d46c75` — blank line after every converted SSE event (the root cause);
   - `3843fbe` — stop appending a duplicate malformed `response.completed` at `[DONE]`;
   - `64ba05f` — send the standard `data: [DONE]` terminator (bare close reads as disconnect);
   - `bdc373f` — include spec-required `created_at` in streamed Response objects;
   - `6d5ca8f` — give streamed output items their spec-required `id` (`msg_…`), plus `content: []` on add;
   - `163abf4` — carry the emitted items in `response.completed`'s `output` array (was always `[]`);
   - `564d8fc` — fold a turn's trailing assistant text message into the tool-carrying assistant message (codex orders history items function_call → message → function_call_output, which produced the invalid sequence assistant(tool_calls), assistant(text), tool(…) that upstreams reject on every turn ≥2), and keep `output_text` content parts instead of dropping them.

   **Verified end-to-end**: codex CLI (`codex exec`, wire_api=responses, model kimi-k3) now completes full agent loops through the gateway — multi-turn tool calls, history conversion, SSE — and created `codex_hello.py`. Note: codex's sandbox needs `--sandbox danger-full-access` in environments where bubblewrap cannot create network namespaces (unrelated to the gateway).
5. **approach upstream flakiness (no code fix):** under parallel load the upstream intermittently drops connections without a response (EOF / RemoteDisconnected, observed 3× on 2026-10-24 including a 20k-token system prompt that succeeded twice on retry). Our proxy maps these to 502; client-side retry (opencode's built-in retry) recovers. Worth reporting to approach; another argument for multiple keys.

**Residual risk:** the key-level `Failures` counter still never resets on success — it only decays via cooldown expiry. Over a long period, intermittent real errors can accumulate to 3 and cause short cooldowns. Consider resetting `Failures = 0` on a successful request in a future pass.

## Real-client simulation (2026-10-24 afternoon)

Installed the actual CLI clients locally (node v22) and ran them headless against `https://api.woodgit.com/v1` with the enterprise test key. This goes beyond HTTP-shape emulation: full agent loops with tool calls.

| Client | Protocol used | Model | Result |
|---|---|---|---|
| **opencode** 1.19.4 (`opencode run`, `@ai-sdk/openai-compatible`) | `/v1/chat/completions` + tools, stream | kimi-k3 | ✅ created `hello.py` via full agent loop; survived one upstream EOF flake with its own retry (Bad Gateway → retry → done) |
| **Kilo Code CLI** 7.8.17 (`kilo run --auto`, kilo.json) | `/v1/chat/completions` + tools, stream | kimi-k3 | ✅ wrote `kilo_hello.py`, **executed it**, verified output — cleanest run of the day |
| **OpenAI Codex CLI** v0.164 (`codex exec`, custom provider, wire_api=responses) | `/v1/responses` | kimi-k3 | ❌→✅ 8 proxy fixes later (see Bugs #3/#4): full agent loop verified, created `codex_hello.py`. Requires `--sandbox danger-full-access` where bubblewrap is restricted. Note codex no longer supports `wire_api = "chat"` at all. |
| **Kimi Code CLI** 2.3.1 (`kimi --prompt`, `~/.kimi-code/config.toml` provider type=openai) | `/v1/chat/completions` + tools | kimi-k3 | ✅ created `kimi_hello.py` — vendor's own CLI works against the gateway with a custom provider |
| **Tencent CodeBuddy CLI** (`codebuddy -p -y`, npm `@tencent-ai/codebuddy-code`) | OpenAI `/v1/chat/completions` (despite docs claiming Anthropic) | kimi-k3 | ✅ created `cb_hello.py`. Gotcha: `CODEBUDDY_BASE_URL` must include `/v1` — with the bare domain it POSTs `/chat/completions` which hits the Portal and dies with `403 CSRF`. |
| **ByteDance TRAE Agent CLI** (`trae-cli run`, bytedance/trae-agent, trae_config.yaml provider=openai) | `/v1/chat/completions` | kimi-k3 | ⚠️ gateway calls succeed (200 in proxy logs) but the agent loop silently exits without acting (empty `llm_interactions`, no assistant step recorded). Client-side defect in trae-agent, needs vendor debug. Config needs all of temperature/top_p/top_k/max_retries/parallel_tool_calls or ModelConfig validation crashes. |
| **Qoder CLI** | OpenAI-compatible or Responses (configurable) | — | not run: requires a Qoder account / personal access token; docs confirm full custom-provider support, same request shape as codex, so fixes #3/#4 apply to it as well |
| **GLM / CodeGeeX** (Zhipu) | — | — | ❌ no standalone CLI exists. GLM Coding Plan officially integrates via claude code / opencode / cline pointed at `open.bigmodel.cn` endpoints — i.e. our opencode test is exactly this integration class. ZCode IDE is GUI-only. |
| **Baidu Comate CLI** (`comatecli`, npm `@comate/comatecli`) | — | — | ❌ no custom endpoint / BYOK: models only from Comate platform account (enterprise custom models go through Baidu's management platform). Headless `comatecli run` works but cannot point at our gateway. |
| **iFlow CLI** (心流) | — | — | ⛔ discontinued 2026-04-17 per official README. The unscoped npm `iflow-cli` package is a fake (2 files, published after shutdown by an unrelated individual) — do not install. |
| **Kimi Work (Moonshot desktop agent)** | proprietary, OAuth-bound | — | ❌ cannot integrate: no custom endpoint support, GUI-only. (Kimi **Code** CLI does support custom OpenAI-compatible providers and works — see above.) |

Notes:
- Codex CLI **no longer supports `wire_api = "chat"`** (hard error, requires `responses`). Any customer on codex ≥2026 must have a working `/v1/responses` path — the convert mode now covers this for chat-only upstreams.
- opencode/Kilo send a browser-like UA and pass Cloudflare without issue; the CF error-1010 UA block only hit the stdlib-Python harness.
- Codex emits `include: ["reasoning.encrypted_content"]` and `reasoning: {summary: "auto"}` — ignored by the converter, harmless.

## Client compatibility summary

| Client shape | glm-5.3 | kimi-k3 |
|---|---|---|
| curl / OpenAI non-stream small | ✓ | ✓ (resp model name = qianfan-vl-v1) |
| OpenAI stream | ✓ | ✓ |
| tool_calls (both modes) | ✓ | ✓ |
| Anthropic `/v1/messages` | ✓ | ✓ |
| python-urllib default UA | ✗ CF 1010 | ✗ CF 1010 |
| workbuddy-class (100k+ ctx, non-stream) | ✗ CF 524 | ✗ CF 524 / cooldown (fixed) |
| workbuddy-class via stream + ≤100s budget | feasible | feasible |

## Recommendations

1. Ask approach for **more upstream keys** (≥4) — single-key + 60s-cooldown semantics makes the provider one failure away from a full outage.
2. Decide the Cloudflare posture for api.woodgit.com: grey-cloud it, or provision a separate direct (grey) hostname for heavy-context clients; document the ~100s wall.
3. Clarify with approach what model actually serves `kimi-k3` (qianfan-vl-v1 observed); re-check pricing/margin if it is a cheaper backbone.
4. Keep TR `glm-5.3`/`kimi-k3` paused but consider enabling one as a low-priority fallback to survive future approach outages.
5. Add `Failures = 0` on success (proxy key health) and expose per-key cooldown/health state in the portal keypool page.

---

## Day-2 regression — 2026-10-25 (04:00–05:10 UTC)

Repeat of the day-1 methodology against the **same** key (`dailyuse`, cn-model group, concurrent=10; TR counterparts still paused → all traffic on approach). 97 requests total, ~70 min wall.

| Phase | Requests | Content |
|---|---|---|
| Harness (11 groups × 2 models) | 48 | identical to day 1 |
| workbuddy-style 100k ctx + 10 tools, non-stream | 2 | day-1 replay |
| workbuddy-style 100k ctx + 10 tools, **stream** | 2 | day-1 had no stream variant |
| 20-way burst on kimi-k3 | 20 | day-1 only burst 12 |
| opencode 1.19.4 agent loop | 8 | expense-tracker task, **glm-5.3** |
| OpenAI Codex CLI v0.164 agent loop | 6 | same task, **kimi-k3** (/v1/responses) |
| Kimi Code CLI 2.3.1 agent loop | 11 | same task, **glm-5.3** (isolated `KIMI_CODE_HOME` copy — main TUI config untouched) |

### Day-1 vs day-2, per group

| Group | glm 10-24 | glm 10-25 | k3 10-24 | k3 10-25 |
|---|---|---|---|---|
| g1 small | 200, 3.2s | 200, 4.3s | 200, 2.9s | 200, 2.1s |
| g2 stream small | 200, 1.6s | 200, 3.0s | 200, 2.8s | 200, 1.9s |
| g3 8k non-stream | 200, **40.5s** | 200, **8.0s** | 200, **43.3s** | 200, **12.6s** |
| g4 8k stream | 200, 47.9s | **502 @65s** (CF cut) | 200, 72.9s | 200, **7.0s** |
| g5 40k non-stream | **524 @125s** | 200, **70.2s** | **524 @126s** | 200, **49.0s** |
| g6 40k stream | **524 @125s** | 200, **42.9s** | 502 @69s | 200, **50.0s** |
| g7 tool non-stream | 200, 2.2s | 200, 6.3s | 200, 3.2s | 200, 3.1s |
| g8 tool stream | 200, 2.8s | 200, 1.6s | 200, 2.9s | 200, 2.1s |
| g9 burst 12 | 10×200 + 2×429 | 10×200 + 2×429 | 10×200 + 2×429 | 10×200 + 2×429 |
| g10 16k→1500 out | 33–112s | 16–38s | 36–57s | 16–43s |
| g11 /v1/messages | 200, 7.5s | 200, 2.5s | 200, 11.4s | 200, 3.4s |

### What changed day-over-day

1. **Functional matrix: 100% consistent.** Every capability that worked on 10-24 worked on 10-25 (small/stream/8k/tools/burst/anthropic), with identical burst signatures (10 pass + 2 group-cap 429s) on both days. `resp_model` still `glm-5.3` / `qianfan-vl-v1` — the kimi-k3 backbone question is unchanged.
2. **Upstream latency dropped 3–10× overnight.** 8k ctx: 40–73s → 7–13s. 40k ctx: >125s (CF death) → 42–70s (success). 16k/1500-out throughput group: up to 2× faster. Same gateway, same key, same payload shapes — the delta is entirely on the approach side (yesterday's slowness was upstream load/capacity, not our stack). **Implication: approach latency is not stable enough to assume; treat it as an SLO to monitor (e.g. proxy latency metrics on these two models), not a one-off measurement.**
3. **The CF ~100s wall moved but did not disappear.** 40k passes now; **100k workbuddy-class still dies**: non-stream 524 @125s (both models); stream also dies (glm 524 @125s, k3 502 @110s) because approach buffers — TTFT ≈ total > 100s, so no bytes flow to keep Cloudflare's window open. Streaming is *not* a workaround for 100k-class contexts. The grey-cloud / direct-hostname decision (recommendation #2) is still open.
4. **Transient CF mid-stream cuts still occur.** glm g4 (8k stream) was cut at 64.9s with a CF 502 page while k3's identical request finished in 7s. Client-side retry remains mandatory for robustness.
5. **20-way burst on k3: 10×200 + 10×429 in 6.3s wall.** The group concurrent cap drains correctly under 2× oversubscription; 429s fail fast (~0.5s); zero upstream 429s. Our side remains the binding limit.
6. **Key health fixes validated in production conditions.** 8 CF-cut failures today (2×100k non-stream, 2×100k stream, glm g4, plus day-1's accumulation) triggered **no cooldown and no model disable** — `upstream_keys` row unchanged (`valid`, rpm_limit=300), zero cooldown events in proxy logs. The a19f25b/70bf77e fixes behave correctly under real failure traffic.

### Billing (portal DB, 2026-10-25, key 0ca861a5…)

| Model | Billed records | Input tok | Output tok | Billed |
|---|---|---|---|---|
| glm-5.3 | 40 | 685,669 | 30,055 | $0.8980 |
| kimi-k3 | 38 | 237,932 | 4,811 | $0.7555 |

78 billed of ~89 completed requests: every CF-cut (100k×4, glm g4), every 429 (2+2+10) unbilled — **"no success, no charge" holds on day 2**. glm output volume is 60× day-1's because the two agent loops generated real completions. Total day-2 spend $1.65.

### Client matrix, day 2

| Client | Model | LLM calls | Task result |
|---|---|---|---|
| opencode 1.19.4 | glm-5.3 | 8 | ✅ expense tracker, 17/17 pytest pass, self-verified |
| Codex CLI v0.164 (`--skip-git-repo-check --sandbox danger-full-access`) | kimi-k3 | 6 | ✅ same task, 9/9 pytest pass |
| Kimi Code CLI 2.3.1 (isolated `KIMI_CODE_HOME`) | glm-5.3 | 11 | ✅ same task, 21/21 pytest pass, end-to-end CLI smoke |

All three completed full agent loops (multi-turn tool calls, file writes, test execution, fixups) through the gateway with **zero client-side errors** — the day-1 proxy fixes (e201e74…564d8fc) hold on day 2 without regression. Codex note: the day-1 `trust_level` config was not honored in codex-task; `--skip-git-repo-check` was required.

### Incident note (test hygiene, no production impact)

`burst20.py` v1 imported `harness.py` for its `post()` helper — but `harness.py` runs the full 48-request suite at import time. It re-fired the suite with the redacted placeholder key (48× 401 `INVALID_API_KEY_FORMAT`, fail-fast at the edge, **never billed**, no upstream contact) and truncated `results.json` (day-1 evidence). Day-1 `results.json` was rebuilt from a verbatim dump taken earlier in the session and marked `_reconstructed: true` on every record; raw day-1 evidence also survives in this report's day-1 tables. New scripts (`harness-20261025.py`, `workbuddy_sim*-20261025.py`, `burst20.py`) are standalone copies.

### Day-2 conclusion

- **Functional consistency: confirmed.** Same key, same gateway, 24h later — every capability class behaves identically; the two proxy fix batches from 10-24 show no regression.
- **Performance consistency: not guaranteed by the provider.** 3–10× day-over-day latency swing observed with zero changes on our side. Recommendation: add proxy-side latency monitoring/alerting for approach models; re-run this harness after any approach-side "capacity upgrade" announcement; keep TR fallback paused-but-ready.
- **100k-class contexts remain unservable** through the orange-cloud hostname regardless of stream mode — grey-cloud decision needed.

---

## Evening run — 2026-10-25 17:05 UTC (third time point)

Same harness, same key, third period of day: day-1 09:53 → day-2 04:00 → **day-2 evening 17:05** (user request: characterize upstream performance across time periods). One config delta vs day-1/day-2: the `cn-model` group concurrent cap was raised 10→100 during the day-3 supplement, so the g9 burst no longer trips our side. Raw: `results-20261025-evening.json`, log `harness-evening.log`.

| Group | glm day1 09:53 | glm day2 04:00 | glm 17:05 | k3 day1 09:53 | k3 day2 04:00 | k3 17:05 |
|---|---|---|---|---|---|---|
| g1 small | 200, 3.2s | 200, 4.3s | 200, 2.2s | 200, 2.9s | 200, 2.1s | 200, 2.5s |
| g2 stream small | 200, 1.6s | 200, 3.0s | 200, 2.9s | 200, 2.8s | 200, 1.9s | 200, 2.0s |
| g3 8k | 200, 40.5s | 200, 8.0s | 200, 10.1s | 200, 43.3s | 200, 12.6s | 200, 6.0s |
| g4 8k stream | 200, 47.9s | 502 @65s | 200, 7.8s | 200, 72.9s | 200, 7.0s | 200, 11.9s |
| g5 40k | 524 @125s | 200, 70.2s | 200, 58.0s | 524 @126s | 200, 49.0s | 200, 52.4s |
| g6 40k stream | 524 @125s | 200, 42.9s | 200, 47.6s | 502 @69s | 200, 50.0s | 200, 57.7s |
| g7 tool | 200, 2.2s | 200, 6.3s | 200, 2.2s | 200, 3.2s | 200, 3.1s | 200, 3.3s |
| g8 tool stream | 200, 2.8s | 200, 1.6s | 200, 1.6s | 200, 2.9s | 200, 2.1s | 200, 2.1s |
| g9 burst 12 | 10×200+2×429 | 10×200+2×429 | **12×200, 0×429** | 10×200+2×429 | 10×200+2×429 | **12×200, 0×429** |
| g10 16k→1500 | 33–112s | 16–38s | 14–25s | 36–57s | 16–43s | 16–20s |
| g11 /v1/messages | 200, 7.5s | 200, 2.5s | 200, 2.0s | 200, 11.4s | 200, 3.4s | 200, 2.4s |

### Evening findings

1. **48/48 requests 200 — the first zero-failure run.** No CF 524/502 anywhere (40k groups at 48–58s, comfortably inside the ~100s wall). The transient CF mid-stream cuts seen in both earlier runs did not reappear.
2. **Evening peak-hour performance holds.** 17:05 UTC latency ≈ 04:00 UTC latency across every group (8k: 6–12s; 40k: ~1 min; small: 2–3s). Combined with day-2 morning, the fast tier has now held for ~13h of observation spanning off-peak and evening peak — day-1's 40–73s slowness increasingly looks like a transient upstream load episode rather than the baseline.
3. **g9 burst signature changed for a known reason.** 12/12 200 (was 10+2×429) because our group cap is now 100 — the 429s were always ours, not upstream's. Upstream absorbed 12 concurrent small requests in 2–8s without any throttle.
4. **Billing: 48/48 billed (no failures to exclude)** — glm-5.3 175,017 in / 580 out $0.2903; kimi-k3 184,213 in / 314 out $0.4081; total $0.70.
5. **Unchanged:** `resp_model` still `glm-5.3` / `qianfan-vl-v1`; prompt-caching and vision findings from the day-3 supplement stand.

### Three-period summary (for upstream SLA discussions)

| Metric | day1 09:53 | day2 04:00 | day2 17:05 |
|---|---|---|---|
| Failure count (of 48) | 5 (CF 524/502) | 3 (CF cut + 4×429 ours) | **0** |
| glm 8k latency | 40.5s | 8.0s | 10.1s |
| k3 8k latency | 43–73s | 7–13s | 6–12s |
| 40k context | unservable (CF) | 43–70s | 48–58s |
| Small-request latency | 2–3s | 2–4s | 2–3s |

- Functionality: 3/3 runs identical (every capability class 200).
- Latency: bimodal history — one slow period (day1 morning), then stable-fast for 13h+. One more slow-period observation would confirm the pattern; until then, treat day-1 as an outlier and monitor.
- The remaining hard limits are ours or CF's, not approach's: the 100k-context CF wall, and per-key fragility (single key).

---

## Day-3 supplementary — 2026-10-25 (05:50–06:15 UTC)

Targeted coverage of capability classes the day-1/day-2 harness did **not** exercise, plus a re-probe of the upstream rate-limit ceiling now that the `cn-model` group concurrent cap was raised **10 → 100** (the day-1/day-2 binding constraint). Same enterprise key (`sk-wgit-en-1f723e3a…`), TR counterparts still paused → all traffic on approach. Script: `workspace/approach-test/supplement-20261025.py`, raw results `supplement-results-20261025.json`. 16/18 PASS (the 2 non-PASS are correct behavior, see below).

### New capability coverage (gaps vs day-1/day-2)

| Test | glm-5.3 | kimi-k3 | Note |
|---|---|---|---|
| A1 `response_format: json_object` | ✅ 200, valid JSON | ✅ 200, valid JSON | JSON mode works on both |
| A2 `response_format: json_schema` (strict) | ✅ 200 (content not strict-JSON) | ✅ 200, valid JSON | glm returns 200 but doesn't strictly honor schema; k3 does |
| B multi-turn tool conversation | ✅ turn1 tool_calls → tool result → turn2 uses result | ✅ same | **not covered before** (harness was single-turn); multi-turn history conversion confirmed |
| C vision input (`image_url` data URL) | ✅ 200 | ✅ 200 | **new**: BOTH models accept image parts (consistent with k3 being served by `qianfan-vl`) |
| D1 streaming usage (`stream_options.include_usage`) | ✅ usage in SSE | ✅ usage in SSE | **corrects** day-1 note "streaming carries no usage": with include_usage set, usage *is* emitted in the final SSE chunk |
| D2 Anthropic `/v1/messages` **streaming** | ✅ SSE events | ✅ SSE events | day-1/2 only tested non-stream `/v1/messages` |
| D3 prompt caching (big stable system + `cache_control`, ×2) | ✅ 2nd call `cache_read=3584` | ✅ 2nd call `cache_read=3677` | **new & notable**: approach upstream **supports prompt caching** (returns cache_read tokens on repeat). Not previously tested on this provider. |
| D4 error boundary — empty messages | ✅ 400 `UPSTREAM_BAD_REQUEST` | ✅ 400 `UPSTREAM_BAD_REQUEST` | upstream rejects, proxy surfaces clean 400 |
| D5 error boundary — `max_tokens: 99999999` | 429 `TOKEN_RATE_LIMIT_EXCEEDED` | 429 `TOKEN_RATE_LIMIT_EXCEEDED` | **correct behavior** (not a defect): our pre-consume/TPM guard fast-rejects the absurd token budget in ~0.1s. Counted as non-PASS only because the test expected 200/400. |

### Upstream rate-limit re-probe (cap now 100 — updates Finding #1)

With the group concurrent cap raised to 100, the day-1/day-2 masking constraint is gone, so the upstream's own limits become observable for the first time:

| Probe | glm-5.3 | kimi-k3 |
|---|---|---|
| 30 concurrent, small | 30/30 200 | 30/30 200 |
| 50 concurrent, small | 50/50 200 | 50/50 200 |
| 80 concurrent, small | 80/80 200 (wall 17.2s) | 80/80 200 (wall 10.9s) |
| 40 concurrent × ~1.5k ctx (~60k instant TPM) | **33 × 200 + 7 × 429 `RATE_LIMIT_EXCEEDED`** | 40/40 200 |

- **First observation of an upstream rate limit.** The `RATE_LIMIT_EXCEEDED` code here is distinct from our proxy-side `CONCURRENT_LIMIT_EXCEEDED` / `TOKEN_RATE_LIMIT_EXCEEDED` — it is the upstream (approach) throttling. It appears for **glm-5.3 at ~40 concurrent × medium context** (token-pressure driven), while **kimi-k3 absorbed the same load with zero 429s** → k3 has more upstream headroom than glm at this provider. Small-request concurrency up to 80 is clean for both.
- This **updates day-1 Finding #1**: the upstream ceiling is no longer "unreachable from our side" — raising our cap to 100 exposed it. Practical takeaway: glm-5.3 is the first to throttle under concurrent medium-context load; size per-key limits / request more keys accordingly.

### Billing (these supplementary runs, approach)

- glm-5.3: 203 records, 60,718 in / 2,075 out, ≈ **$1.10**
- kimi-k3: 210 records, 86,785 in / 878 out, ≈ **$1.25**
- 429s (upstream throttle + our pre-consume guard) and 400s were not billed — "no success, no charge" continues to hold.

### Day-3 conclusion

- **Functional coverage is now broad**: JSON mode, strict schema, multi-turn tools, vision input, streaming usage, Anthropic streaming, prompt caching, and error boundaries all verified on both models — none of which the day-1/day-2 harness exercised. No functional defects found; the 2 non-PASS lines are correct guard/validation behavior.
- **Two notable new facts**: (1) approach **supports prompt caching** (cache_read confirmed) — relevant to cost modeling; (2) both models **accept image inputs** (k3 is a VL backbone).
- **Upstream rate limit is now characterized** (glm throttles first, ~40-concurrent medium-ctx; k3 has more headroom) — supersedes the earlier "limit unreachable" note after the cap-100 change.
- Residual items from day-1/day-2 unchanged: 100k-context CF ~100s wall, single-key fragility (request more keys), and the `kimi-k3 = qianfan-vl` backbone/pricing question.

---

## Day-4 afternoon run — 2026-10-26 06:14 UTC / 14:14 Beijing (fourth time point, with failover disambiguation)

User-requested re-run at Beijing afternoon peak, with an added requirement: **identify failover** — routing config is approach (P0) → tokenrouter (P1) for both models, so any approach failure silently degrades to TR unless disambiguated. Same harness, same key. Raw: `results-20261026-afternoon.json`, log `harness-afternoon.log`. CF proxy is **still orange-cloud** at test time (api.woodgit.com resolves to CF anycast, cf-ray present) — the ~100s CF wall applies.

### Per-group results (four-period comparison)

| Group | glm day1 09:53 | glm 10-25 04:00 | glm 10-25 17:05 | **glm 10-26 14:14** | k3 day1 09:53 | k3 10-25 04:00 | k3 10-25 17:05 | **k3 10-26 14:14** |
|---|---|---|---|---|---|---|---|---|
| g1 small | 200, 3.2s | 200, 4.3s | 200, 2.2s | 200, 4.9s | 200, 2.9s | 200, 2.1s | 200, 2.5s | 200, 3.7s |
| g2 stream small | 200, 1.6s | 200, 3.0s | 200, 2.9s | 200, ttft 2.1s | 200, 2.8s | 200, 1.9s | 200, 2.0s | 200, **ttft 26.1s** |
| g3 8k | 200, 40.5s | 200, 8.0s | 200, 10.1s | 200, 21.6s | 200, 43.3s | 200, 12.6s | 200, 6.0s | 200, 10.9s |
| g4 8k stream | 200, 47.9s | 502 @65s | 200, 7.8s | 200, ttft 16.6s | 200, 72.9s | 200, 7.0s | 200, 11.9s | 200, **ttft 53.6s** |
| g5 40k | 524 @125s | 200, 70.2s | 200, 58.0s | **524 @126s** | 524 @126s | 200, 49.0s | 200, 52.4s | 200, **124.5s (barely)** |
| g6 40k stream | 524 @125s | 200, 42.9s | 200, 47.6s | **524 @126s** | 502 @69s | 200, 50.0s | 200, 57.7s | **524 @125s** |
| g7 tool | 200, 2.2s | 200, 6.3s | 200, 2.2s | 200, 4.6s | 200, 3.2s | 200, 3.1s | 200, 3.3s | 200, 4.9s |
| g8 tool stream | 200, 2.8s | 200, 1.6s | 200, 1.6s | 200, ttft 2.5s | 200, 2.9s | 200, 1.9s | 200, 2.1s | 200, ttft 1.2s |
| g9 burst 12 | 10×200+2×429 | 10×200+2×429 | 12×200 | 12×200 (tail 34.2s) | 10×200+2×429 | 10×200+2×429 | 12×200 | 12×200 (tail 20.7s) |
| g10 16k→1500 | 33–112s | 16–38s | 14–25s | 23–60s | 36–57s | 16–43s | 16–20s | **76s / 5.5s / 7.4s — all 3 via TR failover** |
| g11 /v1/messages | 200, 7.5s | 200, 2.5s | 200, 2.0s | 200, 9.6s | 200, 11.4s | 200, 3.4s | 200, 2.4s | 200, 14.9s |

44/48 succeeded (91.7%); all 4 failures are CF 524 on 40k groups.

### Failover disambiguation (the point of this run)

Cross-referenced `usage_records` (`provider`, `is_fallback`, `latency_ms`, tokens) against client-side records, plus proxy logs:

| Client request | What actually served it | Evidence |
|---|---|---|
| glm all 22 successes | **approach**, zero failover | `provider=approach, is_fallback=f`, resp_model `glm-5.3` |
| glm g5/g6 40k ×2 → CF 524 | approach hung >100s; proxy logged `Failover retry (non-stream) → tokenrouter, last_status=0` **after** client disconnect | no TR billing rows → retry died on context cancel, **no ghost upstream cost** ✓ |
| k3 g1–g9, g11 (19 successes) | **approach** | `is_fallback=f`, resp_model `kimi-k3` |
| k3 g10 ×3 (16k, concurrent) | **approach connection-failed (`last_status=0`) → TR failover**, all 3 served by TR upstream `moonshotai/kimi-k3` | `provider=tokenrouter, is_fallback=t`; latencies 74.6 / 4.2 / 6.3s; resp_model would read `moonshotai/kimi-k3` |
| k3 g5 40k (124.5s, 200) | **approach** — barely inside the CF wall | `latency_ms=122511` in DB |

**Disambiguation cheat-sheet** (valid for both models): `resp_model` of `glm-5.3` / `kimi-k3` = approach; `z-ai/glm-5.3` / `moonshotai/kimi-k3` = tokenrouter. In portal DB, `usage_records.provider` + `is_fallback` is authoritative — no guessing needed.

### Billing (portal DB, 06:13–06:33 UTC window, test key only)

| Provider | Rows | In tok | Out tok | Cost |
|---|---|---|---|---|
| approach | 42 | 150,884 | 1,129 | $0.4845 |
| tokenrouter (failover) | 3 | 60,918 | 184 | $0.0922 |

Every recorded row billed (success → charge holds); the 4 CF-524 requests produced **no rows and no charge** — consistent with "no success, no charge".

### Day-4 findings

1. **Beijing afternoon peak is the worst of the four periods.** glm 8k at 17–22s (fast tier was 6–12s); k3 streaming degraded badly (ttft 26s on a tiny request, 54s on 8k); k3 g10 3-way concurrent 16k **connection-failed on approach outright** (first connection-level failure in 4 runs) and needed TR failover to complete.
2. **The CF 524 wall is back for 40k.** Yesterday evening 40k sat at 48–58s; this afternoon approach again pushed past 100s (glm ×2 hard-failed at 126s, k3 g5 survived at 124.5s by ~2s, k3 g6 failed). 40k+ contexts are unservable in slow periods as long as the domain stays orange-cloud.
3. **Failover works and bills correctly.** k3's 3 failed-to-approach requests completed on TR and were billed as TR (`is_fallback=t`). glm's post-disconnect retries self-terminated on context cancel with zero ghost cost — correct, but wasteful (see recommendations).
4. **Burst holds.** g9 12/12 on both models even at afternoon peak; tail latency climbs (34s / 21s) — throttling-by-queueing, not rejection.
5. resp_model mapping (`glm-5.3`/`kimi-k3` vs `z-ai/*`/`moonshotai/*`) is a reliable provider fingerprint for these two models.

### Four-period summary

| Metric | 10-24 09:53 | 10-25 04:00 | 10-25 17:05 | 10-26 14:14 (Beijing) |
|---|---|---|---|---|
| Failures (of 48) | 5 | 3 | 0 | 4 (all 40k/CF) |
| glm 8k | 40.5s | 8.0s | 10.1s | 21.6s |
| k3 8k | 43–73s | 7–13s | 6–12s | 11–55s, unstable |
| 40k context | unservable | 43–70s | 48–58s | **unservable (3 of 4 CF-cut)** |
| Upstream failures needing failover | 0 | 0 | 0 | 3 (k3 connection) |

- The "fast tier" held 10-25 04:00→17:05 but **did not hold into 10-26 afternoon** — latency is degrading again at Beijing peak, now bad enough to break k3 at connection level. The bimodal (slow morning / fast day) hypothesis from day-1 is not confirmed; pattern now looks like **capacity queuing under load** (afternoon worst, early morning best).
- **Recommendations (unchanged + new):**
  - (carry-over) 100k/40k contexts need either CF grey-cloud or a direct-origin hostname for large payloads — afternoon 40k is effectively down otherwise.
  - (carry-over) single approach key remains a SPOF; k3 connection failure under mild 3-way concurrency supports asking approach for a second key.
  - **(new) proxy: skip failover when `req.Context().Err() != nil`** — the glm 40k retries fired after CF already disconnected the client; they self-cancelled so no cost, but on a non-canceled path this pattern could double-bill a user whose client already gave up.
  - **(new) monitor `is_fallback=t` share per model** as an approach-health KPI — today it was 3/48; a rising share is the earliest signal of approach capacity loss before users report 524s.

---

## Incident note — 2026-10-26 ~08:52–09:05 UTC: approach glm-5.3 subscription suspension

A harness run at 08:52 UTC (Beijing 16:52) was intended as a fifth time point, but **approach suspended the glm-5.3 subscription mid-run** (`auth error(kind=2): SUBSCRIPTION_INACTIVE`), and most of the surviving traffic was served by TR failover. Per user decision, that run's **performance data is discarded as unrepresentative of approach** (raw files kept on disk: `results-20261026-latepm.json`, `harness-latepm.log`). The event facts below are retained — they matter for operations and for failover disambiguation.

### Event timeline (facts, all UTC 10-26)

- **08:51:52–08:53:46** — glm-5.3 fails with **403 `SUBSCRIPTION_INACTIVE`** on the approach key (`sk-pmNd4…OvgY`, base `tokenservices.store:8443`); kimi-k3 on the same key unaffected throughout (**model-level**, not key-level). 4xx is not retried, so the 5 failures reached clients as 403.
- **08:54:02** — proxy `keypool/model_health.go` disables glm-5.3@approach after 3 failures; `ModelRecoveryInterval = 24h`, in-memory only.
- **~08:54–09:05** — glm traffic serves via TR failover (correct, transparent, billed as TR). TR's glm/k3 latency in this window was excellent (0.4–2.6s) — noted only as TR capability, not approach data.
- **09:02 / 09:19** — two proxy restarts were needed to clear the 24h health lock. Even after the platform reactivated the subscription, the lock kept routing glm to TR until restarted. Post-restart smoke: glm-5.3 200 via approach (8.8s), kimi-k3 200 (3.3s). **Subscription confirmed restored** — approach baseline re-measured in the clean runs below.
- **Side observation:** after one upstream `incomplete_stream` anomaly on kimi-k3, proxy proactively routed that model's larger-context requests to TR for ~45s (key-selection skip, not failover-retry). Short-lived health down-weighting is proxy behavior, not an approach defect.

### Failover disambiguation — method correction (applies to all runs)

**The day-4 `resp_model` cheat-sheet is wrong and must not be used.** Both providers' response `model` fields collide:

| Requested | approach returns | tokenrouter returns |
|---|---|---|
| glm-5.3 | `glm-5.3` | `glm-5.3` (upstream `z-ai/glm-5.3` strips the prefix in responses) |
| kimi-k3 | `kimi-k3` / `qianfan-vl-v1` (varies) | `qianfan-vl-v1` (TR's `moonshotai/kimi-k3` is the same qianfan-vl backbone) |

Only **`usage_records.provider` + `is_fallback`** (portal DB) disambiguates reliably. Client-visible `resp_model` cannot distinguish providers for either model.

### Pricing correction (supersedes day-4 billing note)

TR pricing rules for both models **do exist**: `model_pricing_rules (tokenrouter, z-ai/glm-5.3, flat $1.4/$4.4, cache_hit $0.26, active)` and `(tokenrouter, moonshotai/kimi-k3, flat $3.0/$15.0, active)` — day-4's "no TR glm-5.3 rule" claim was a query artifact (TR rules use the *prefixed* product_id, approach the bare one). Prices are effectively identical across providers for both models; the real failover exposure is **TR quota/balance**, not unit price.

### Operational takeaways

1. model_health's 24h lock is too coarse for transient upstream auth incidents — twice today it required a manual proxy restart. Recommend shortening `ModelRecoveryInterval` to ~15 min with an automatic probe.
2. Failover UX proved good under a real incident: 114s of client-visible 403s, then transparent TR service; billing followed the serving provider throughout.
3. During approach-only characterization runs, pause the TR service_models (as done in the clean runs below); **restore them afterward** — they were re-activated after the clean runs completed.

---

## Day-5 clean runs — 2026-10-26 09:12 & 09:20 UTC (approach-only baselines, TR failover removed per user)

User decision: these runs exist to characterize **approach**, so TR failover makes the data meaningless. Paused TR service_models for both models (`moonshotai/kimi-k3`, `z-ai/glm-5.3` → paused; approach glm-5.3 subscription was reactivated by the platform in the meantime — verified by a 200 smoke at 09:19, `is_fallback=f`, 8.8s). A proxy restart was needed to clear the lingering 24h model-health lock on glm-5.3@approach (it kept routing to TR even after the subscription came back). **All data below is approach-only** — DB-verified: kimi-k3 24/24 `is_fallback=f`; glm 23 approach rows + 1 stray TR smoke row from before the restart. Raw: `results-20261026-k3clean.json`, `results-20261026-glmclean.json`.

### kimi-k3 @ approach, 17:12 Beijing (24/24 200)

| Group | day1 09:53 | day2 04:00 | day2 17:05 | day4 14:14 | **day5 clean 17:12** |
|---|---|---|---|---|---|
| g1 small | 2.9s | 2.1s | 2.5s | 3.7s | **2.9s** |
| g2 stream small | 2.8s | 1.9s | 2.0s | ttft 26.1s | **ttft 2.3s** |
| g3 8k | 43.3s | 12.6s | 6.0s | 10.9s | **16.9s** |
| g4 8k stream | 72.9s | 7.0s | 11.9s | ttft 53.6s | **ttft 9.3s** |
| g5 40k | 524 | 49.0s | 52.4s | 124.5s | **50.8s** |
| g6 40k stream | 502 | 50.0s | 57.7s | 524 | **ttft 71.0s** |
| g7 tool | 3.2s | 3.1s | 3.3s | 4.9s | **2.9s** |
| g8 tool stream | 2.9s | 2.1s | 2.1s | ttft 1.2s | **ttft 1.5s** |
| g9 burst 12 | 10+2×429 | 10+2×429 | 12×200 | 12×200 | **12×200 (1.8–9.0s)** |
| g10 16k→1500 | 36–57s | 16–43s | 16–20s | 76/5.5/7.4s (TR) | **84.0 / 22.2 / 34.6s (ttft 82.5/21.1/33.2)** |
| g11 /v1/messages | 11.4s | 3.4s | 2.4s | 14.9s | **5.2s** |

### glm-5.3 @ approach, 17:20 Beijing (22/24; both failures are the CF wall)

| Group | day1 09:53 | day2 04:00 | day2 17:05 | day4 14:14 | **day5 clean 17:20** |
|---|---|---|---|---|---|
| g1 small | 3.2s | 4.3s | 2.2s | 4.9s | **3.2s** |
| g2 stream small | 1.6s | 3.0s | 2.9s | ttft 2.1s | **ttft 2.5s** |
| g3 8k | 40.5s | 8.0s | 10.1s | 21.6s | **39.2s** |
| g4 8k stream | 47.9s | 502 | 7.8s | ttft 16.6s | **ttft 21.9s** |
| g5 40k | 524 | 70.2s | 58.0s | 524 | **524 @126s** |
| g6 40k stream | 524 | 42.9s | 47.6s | 524 | **502 @61s** |
| g7 tool | 2.2s | 6.3s | 2.2s | 4.6s | **4.6s** |
| g8 tool stream | 2.8s | 1.6s | 1.6s | ttft 2.5s | **ttft 1.8s** |
| g9 burst 12 | 10+2×429 | 10+2×429 | 12×200 | 12×200 | **12×200 (1.9–5.0s)** |
| g10 16k→1500 | 33–112s | 16–38s | 14–25s | 23–60s | **22.9 / 14.1 / 18.2s** |
| g11 /v1/messages | 7.5s | 2.5s | 2.0s | 9.6s | **1.9s** |

### Clean-run findings

1. **kimi-k3 @ approach is stable but not "optimized".** Every group works (24/24, including 40k at 51–72s — comfortably inside the CF wall), small/tool/burst latency matches the historical fast tier, and the day-4 anomalies (g2 ttft 26s, g4 ttft 54s, g10 connection failures) did not repeat. But 8k at 10–17s and 16k-out-1500 at 22–84s are the same as day-2 — the striking 40k≈9s numbers seen during the incident window (see incident note above) were **TR**, not an approach improvement. Correct the record with the platform owner if they were told "K3 got faster".
2. **glm-5.3 @ approach recovered from the suspension and performs at day-1 slow-tier levels**: 8k at 22–39s, 40k unservable under the CF orange-cloud wall (126s and 61s→502). Small/tool/burst are fast (2–5s). So approach's glm currently sits in the *slow* mode that day-2/2-evening runs showed — the "fast tier" was only ever observed on 10-25.
3. **The day-1 morning slowness is no longer an outlier — it's a mode.** Slow tier seen: day1 09:53, day4 14:14, day5 17:12/17:20. Fast tier seen: day2 04:00, day2 17:05. Pattern so far correlates with Beijing business hours (slow) vs off-peak/late-evening (fast) — consistent with capacity queuing, but the day2 17:05 fast observation cuts against a simple business-hours rule. More time points needed; a cron'd hourly probe would settle it.
4. **Billing holds**: k3 clean run $0.4957 (184k in / 956 out), glm clean run $0.1891 (78k in / 529 out) + $0.005 stray TR smoke row. Success-only charging verified again.
5. **Operational state:** TR `moonshotai/kimi-k3` and `z-ai/glm-5.3` were paused for these characterization runs and **restored to `active` afterward** (both confirmed active in `service_models`). model_health's 24h lock required two manual proxy restarts on 10-26; consider shortening `ModelRecoveryInterval` to ~15 min with a probe, as noted in the incident note above.

---

## Day-6 evening run — 2026-10-26 13:07 UTC / 21:07 Beijing (sixth time point — key-cooldown incident, partial data)

Beijing 21:00 run with TR paused again for approach-only measurement (restored to `active` afterward). The run was **interrupted mid-way by a proxy key-level cooldown incident**; usable approach data is partial. Raw: `results-20261026-evening6.json`, log `harness-evening6.log`.

### Timeline (UTC)

- **13:07:45–13:09:39** — glm g1–g4 succeed on approach (2.8 / 6.6 / 26.5 / 19.0s — 8k in slow tier, consistent with day-5).
- **13:11:31** — proxy: `Upstream request failed … Post https://www.tokenservices.store:8443/v1/chat/completions: EOF` ×3 → **`Key max failures reached, entering cooldown`** (key-level, whole `sk-pmNd4…` key, all models). Triggered around the glm 40k attempts (g5 502 @75s, g6 524 @125s — themselves CF-wall casualties).
- **13:12:12–13:12:25** — glm g7–g11: **18/18 requests 503** `no available upstream keys for model: glm-5.3` (key in cooldown, TR paused = zero fallback).
- **13:12:28** — kimi g1 also 503; **13:12:40** cooldown expires and kimi g2 succeeds (9.7s). From here kimi mostly works, but **slowly**: g3 8k **78.7s**, g4 8k stream ttft **43.4s**, g5/g6 40k **both CF 524 @127s**. g7–g11 recover to normal-small latency (3.6–9.4s; burst 12/12 in 3.2–22.8s).

### Usable data (approach-only, DB-verified `is_fallback=f` throughout)

| Group | glm day5 17:20 | **glm day6 21:07** | k3 day5 17:12 | **k3 day6 21:07** |
|---|---|---|---|---|
| g1 small | 3.2s | 200, 2.8s | 2.9s | **503** (key cooldown) |
| g2 stream small | ttft 2.5s | 200, ttft 5.8s | ttft 2.3s | 200, ttft 9.5s |
| g3 8k | 39.2s | 200, 26.5s | 16.9s | **200, 78.7s** |
| g4 8k stream | ttft 21.9s | 200, ttft 18.4s | ttft 9.3s | 200, ttft 43.4s |
| g5 40k | 524 | **502 @75s** | 50.8s | **524 @127s** |
| g6 40k stream | 502 | **524 @125s** | ttft 71.0s | **524 @127s** |
| g7 tool | 4.6s | **503** | 2.9s | 200, 5.6s |
| g8 tool stream | ttft 1.8s | **503** | ttft 1.5s | 200, ttft 3.4s |
| g9 burst 12 | 12×200 | **12×503** | 12×200 | 12×200 (3.2–22.8s) |
| g10 16k→1500 | 14–23s | **3×503** | 84/22/35s | 200 (slow) |
| g11 /v1/messages | 1.9s | **503** | 5.2s | 200 |

glm valid sample: g1–g4 only (plus two CF-wall 40k failures). kimi valid: g2–g11 minus the two 40k CF cuts.

### Day-6 findings

1. **Beijing 21:00 is the worst observed period.** kimi 8k at 43–79s (vs 10–17s at 17:12), 40k unservable (CF wall), glm 8k 19–26s. The slow tier now has a clear intraday gradient — morning (day1 09:53) bad, afternoon (14:14, 17:12/17:20) middling, 21:00 worst, then overnight/evening (10-25 04:00, 17:05) fast. This looks like progressive upstream capacity depletion through the Beijing day rather than a bimodal switch. An hourly cron probe (small + 8k) would nail the curve.
2. **Key-level cooldown is a real SPOF amplifier.** Three EOFs on the single approach key cooled down the *entire key* (all its models) for ~60s; with TR paused, that surfaced as 19 straight 503s. In production (TR active) the same event silently becomes mass failover — which is exactly why the 16:52 run's data was full of TR. **Two independent fixes are needed: (a) a second approach key, (b) cooldown scoped per (key, model) rather than per key for multi-model keys** — or at minimum, don't count transport EOFs from *other* models' giant requests against models that did nothing wrong (glm's 40k likely caused the cooldown that 503'd kimi).
3. Billing: all 29 successful requests recorded and billed on approach ($0.1495 in the second window + $0.12 in the first); 503/502/524 requests unbilled. Success-only charging intact.
4. TR `moonshotai/kimi-k3` / `z-ai/glm-5.3` re-activated after the run; production routing is approach-primary + TR-fallback again.

---

## Day-7 morning run — 2026-10-27 01:17 UTC / 09:17 Beijing (seventh time point — second zero-failure run, fast tier)

Beijing 09:00 Monday-morning run, TR paused for approach-only measurement (restored to `active` after). **48/48 succeeded — the second zero-failure run after day-2 evening.** Raw: `results-20261027-morning7.json`, log `harness-morning7.log`. DB-verified: all 48 rows `approach, is_fallback=f`; billed $0.793 (359k in / 1,555 out).

| Group | glm day6 21:07 | **glm day7 09:17** | k3 day6 21:07 | **k3 day7 09:17** |
|---|---|---|---|---|
| g1 small | 200, 2.8s | 200, 3.4s | 503* | 200, 2.4s |
| g2 stream small | 200, ttft 5.8s | 200, ttft 0.7s | 200, ttft 9.5s | 200, ttft 1.2s |
| g3 8k | 200, 26.5s | **200, 11.2s** | 200, 78.7s | **200, 13.1s** |
| g4 8k stream | 200, ttft 18.4s | **200, ttft 7.8s** | 200, ttft 43.4s | **200, ttft 19.8s** |
| g5 40k | 502 @75s | **200, 79.6s** | 524 @127s | **200, 73.3s** |
| g6 40k stream | 524 @125s | **200, 33.1s** | 524 @127s | **200, 55.3s** |
| g7 tool | 503* | 200, 4.5s | 200, 5.6s | 200, 2.4s |
| g8 tool stream | 503* | 200, ttft 0.6s | 200, ttft 3.4s | 200, ttft 1.2s |
| g9 burst 12 | 12×503* | 12×200 (1.6–3.0s) | 12×200 | 12×200 (1.3–31.4s) |
| g10 16k→1500 | 3×503* | 200 (18.7–32.5s) | 200 (slow) | 200 (21.6–77.1s) |
| g11 /v1/messages | 503* | 200, 3.3s | 200 | 200, 2.0s |

\* day-6 glm rows marked 503 were the key-cooldown incident window, not approach behavior.

### Seven-run intraday picture (final summary of this report)

| Beijing time | glm 8k | k3 8k | 40k | Failures / 48 | Tier |
|---|---|---|---|---|---|
| 10-24 09:53 | 40.5s | 43.3s | unservable | 5 | slow (outlier at the time; fits pattern retrospectively) |
| 10-25 04:00 | 8.0s | 12.6s | 43–70s | 3 | fast |
| 10-25 17:05 | 10.1s | 6–12s | 48–58s | 0 | fast |
| 10-26 14:14 | 21.6s | 10.9–53.6s | unservable | 4 | mid |
| 10-26 17:12/17:20 | 16.9–39.2s | 10–17s | 51–72s | 2 | mid |
| 10-26 21:07 | 19–26s | 43–79s | unservable | 22 (incident) | worst |
| **10-27 09:17** | **9–11s** | **13–21s** | **33–80s all pass** | **0** | fast |

1. **The diurnal cycle is now confirmed across 7 points**: approach capacity degrades through the Beijing day and recovers overnight. Morning (09:00) = fast tier + zero failures; 21:00 = worst observed. The only historical puzzle (day-1 morning slowness) now fits the pattern if upstream had residual load that day; treat it as the same cycle, not an anomaly.
2. **glm g6 40k at 33s vs g5 at 79.6s** — same context, 2.4× spread; 40k latency is high-variance even in fast tier, so CF-orange-cloud remains marginal for 40k (100s wall vs 33–80s response range).
3. k3 burst tail (31.4s) and g10 (up to 77s) show k3's throughput-oriented requests still queue behind something at 09:00 — minor, but it's the slowest k3 component in an otherwise fast run.
4. No key-cooldown incident this run (no 40k CF-cut EOF cascade — the two 40k groups completed inside the wall).
5. With the cycle established, the remaining approach-side asks to the platform owner are unchanged: **a second key** (SPOF + key-cooldown blast radius) and clarity on the **k3 = qianfan-vl backbone** naming/pricing. Ops-side: keep the CF grey-cloud decision on the table for 40k+, and consider the per-(key,model) cooldown scoping fix noted in day-6.

---

## Day-8 noon run — 2026-10-27 03:50 UTC / 11:50 Beijing (eighth time point — mid tier, first 502s; preceded by a platform incident)

Beijing ~12:00 run. TR paused for approach-only measurement as usual — but the run **initially failed 48/48 with 503 "no available upstream keys"**, exposing a platform defect unrelated to approach (see below). After repair the run was redone. Raw: `results-20261027-noon8.json`. DB-verified: 47/48 rows `approach, is_fallback=f`, $0.7436 billed.

### Platform incident: background re-validation evicted the only approach key (P0, ~20 min)

- Portal's 6-hourly background key-validation (`ValidateUpstreamKeysBackground`) selects keys whose `validation_at` is older than 24h. It re-validated the single approach key at **03:43:06**; the probe to `tokenservices.store:8443/v1/models` failed at network level, and the handler demoted the status to **`unknown`** (its catch-all for non-authoritative outcomes).
- Proxy sync (`/api/admin/key-pool/sync`) only ships keys with `validation_status IN ('valid','pending')` → the approach key silently disappeared from every proxy's keypool at the 03:44 sync.
- Effect: **glm-5.3 and kimi-k3 503'd platform-wide for all users** (48/48 harness requests 503 in 0.1s; any production traffic on these models would have failed identically; TR was paused for the test, but with TR active it would have silently absorbed everything — masking the outage).
- Recovery: manual `UPDATE upstream_keys SET validation_status='valid'` + next sync (60s). The key itself was healthy the whole time — it served 48/48 at 09:17 and again right after repair.
- **Root-cause fix (portal, committed same day):** network-level probe failures (`error`/`rate_limited`) no longer demote `validation_status` in either the background job (`internal/routes/admin_common.go`) or the batch endpoint (`internal/routes/admin_keypool.go`) — only authoritative outcomes (valid/invalid/pending) update the status; probes that never reached the upstream only stamp `validation_at` for the next retry. **This fix needs a portal rebuild to take effect** — until then, any single-key provider can be evicted by one transient probe failure, with up to a 6-hour recurrence window.

### Noon performance vs 09:17 (approach-only)

| Group | glm day7 09:17 | **glm day8 11:50** | k3 day7 09:17 | **k3 day8 11:50** |
|---|---|---|---|---|
| g1 small | 3.4s | 2.2s | 2.4s | 1.6s |
| g2 stream small | ttft 0.7s | ttft 0.9s | ttft 1.2s | ttft 1.9s |
| g3 8k | 11.2s | 200, 17.2s | 13.1s | **200, 49.2s** |
| g4 8k stream | ttft 7.8s | ttft 24.7s | ttft 19.8s | **502 @66.5s (CF page)** |
| g5 40k | 79.6s | 200, **106.8s** | 73.3s | 200, 72.5s |
| g6 40k stream | 33.1s | 200, **124.2s** | 55.3s | 200, 98.7s |
| g7 tool | 4.5s | **200, 32.1s** (outlier) | 2.4s | 200, 5.6s |
| g8 tool stream | ttft 0.6s | ttft 1.4s | ttft 1.2s | ttft 1.9s |
| g9 burst 12 | 12×200 (1.6–3.0s) | 12×200 (2.1–11.6s) | 12×200 (1.3–31.4s) | 12×200 (2.0–16.5s) |
| g10 16k→1500 | 18.7–32.5s | 55.8/28.7s + **502 @96s** | 21.6–77.1s | 17.7/15.8/**97.5s** |
| g11 /v1/messages | 3.3s | 5.6s | 2.0s | 4.7s |

46/48 succeeded; both failures are Cloudflare 502 error pages (one glm g10, one k3 g4 stream) — the first CF 502s observed on this platform (previous CF events were 524).

### Day-8 findings

1. **The diurnal decay is smooth, not stepwise.** 3 hours after the 09:17 zero-failure fast run: glm 8k 11→17–26s, k3 8k 13→49s, 40k 33–80s→72–124s. Latency roughly doubles every ~3–4h into the Beijing day, matching a capacity-queueing model. The 12:00 point already brushes the CF wall on unlucky requests.
2. glm g7 (tool call, small request) took **32.1s** while g8 (same-size streamed) took 2.0s — single-request outliers of this kind are upstream-side queuing, not a proxy defect.
3. **The 03:43 re-validation eviction is the most important operational finding of this report** — it can take any single-key provider down platform-wide on a transient probe failure, and until the committed fix is deployed, the exposure recurs every 6 hours. Deploy the portal rebuild; consider also making sync treat `unknown` keys as shippable-but-degraded (better: keep current filter + the status-preserving fix, which is now in place).
4. TR service_models re-activated after the run (production routing approach-primary + TR-fallback).

## Day-9 late-afternoon run — 2026-10-27 09:13 UTC / 17:13 Beijing (ninth time point — models decouple: k3 back to fast tier, glm hits the CF wall)

First run after the **portal rebuild was deployed** (267b5d1: status-preserving validation + soft-deleted keys excluded from probes — the day-8 P0 fix is now live). TR paused as usual, proxy synced 09:13:12Z. Raw: `results-20261027-afternoon9.json`. DB-verified: 46/46 rows `approach, is_fallback=f`, $0.7624 billed.

### Per-group results (vs noon)

| Group | glm day8 11:50 | **glm day9 17:13** | k3 day8 11:50 | **k3 day9 17:13** |
|---|---|---|---|---|
| g1 small | 2.2s | **524 @125.4s (CF)** | 1.6s | 3.4s |
| g2 stream small | ttft 0.9s | **ttft 30.8s** (32.8s) | ttft 1.9s | ttft 1.2s (2.0s) |
| g3 8k | 200, 17.2s | 200, 22.2s | **200, 49.2s** | **200, 8.2s** |
| g4 8k stream | ttft 24.7s | ttft 13.0s (14.1s) | 502 @66.5s | ttft 4.4s (5.5s) |
| g5 40k | 200, 106.8s | 200, 91.5s | 200, 72.5s | 200, 65.0s |
| g6 40k stream | 200, 124.2s | ttft 60.1s (61.1s) | 200, 98.7s | ttft 30.2s (31.4s) |
| g7 tool | 200, 32.1s (outlier) | 200, 8.1s | 200, 5.6s | 200, 5.9s |
| g8 tool stream | ttft 1.4s | ttft 5.4s (6.0s) | ttft 1.9s | ttft 1.6s (3.8s) |
| g9 burst 12 | 12×200, med 3.7s, max 11.6s | 12×200, med 18.1s, max 74.1s | 12×200, med 4.4s | **12×200, med 3.8s, max 5.5s** |
| g10 16k→1500 | 55.8/28.7s + 502 @96s | 2/3: 58.4/66.2s + **502 @60.7s** | 17.7/15.8/97.5s | **3/3, med 22.2s** |
| g11 /v1/messages | 5.6s | 33.5s | 4.7s | 2.1s |

46/48 succeeded. **Both failures are glm-5.3** (one CF 524 @125s on g1 small — a minimal request timing out is pure queueing; one CF 502 @60.7s on g10). k3 had **zero failures**.

### Day-9 findings

1. **The two models' capacity curves are decoupled.** At the same wall-clock point: k3 snapped back to the fast tier (8k 49.2→8.2s, burst med 16.5→3.8s, 40k 98.7→31.4s stream — as fast as the 09:17 baseline), while glm continued its smooth diurnal decay (8k 17→22s, small-stream ttft 0.9→30.8s, 40k 91–124s) and took both of the day's first failures. The "platform degrades in the afternoon" conclusion from day 8 was wrong as stated — it is **model-specific upstream capacity** (qianfan/Moonshot replenished or re-tuned k3 between 12:00 and 17:00; glm's provider did not). Capacity per model must be monitored independently.
2. **glm g1 failure is notable**: a minimal sync request dying at the CF 100s wall (524) while the identical k3 request took 3.4s — confirms glm's upstream queue at 17:00, not a proxy/gateway defect.
3. g9/g10 spread on glm (burst max 74.1s vs med 18.1s) shows queueing variance, consistent with the day-8 capacity model.
4. Platform side: the day-8 P0 fix verified live — validation cron (11:07Z run) skipped soft-deleted keys, `upstream_key_validation_log` shows no new spurious entries; portal running 267b5d1.
5. TR service_models re-activated after the run (09:31Z proxy sync); production routing approach-primary + TR-fallback restored.

## Day-10 new-key run — 2026-10-29 06:12 UTC / 14:12 Beijing (tenth time point — new approach key & endpoint, key-level cooldown incident)

User **replaced** the approach upstream key (old `baa89714` :8443 key gone): new key `bb41489c`, base `https://www.tokenservices.store/v1` (443, no port suffix), models exactly `["glm-5.3","kimi-k3"]`, rpm 3000, validated valid 06:02:48Z. TR paused as usual, proxy synced 06:11:12Z. Raw: `results-20261029-afternoon10.json`. DB-verified: 26 rows all `approach, is_fallback=f`, $0.2955 billed.

### Result: 26/48 — good news on speed, incident on resilience

**glm-5.3: 21/21 clean, and the new endpoint is materially faster than :8443.** 40k non-stream 59.1s (day-9: 91.5s), burst 12/12 med **3.6s** max 5.8s (day-9: med 18.1s max 74.1s), small/8k/tool groups 3–16s. Two glm 40k requests (g6 stream, g10) hit CF 502s (~61–71s) — same CF-wall behavior as before, but on glm they stayed within the failure budget.

**kimi-k3: 4/24.** g1–g4 fine (2.0–22.4s), then:
- g5 (40k sync): CF 502 @76.4s (06:19:42Z)
- g6 (40k stream): CF 502 @121s (06:21:46Z)
- g7 through g11 (18 requests): **instant 503 `no available upstream keys for model: kimi-k3 after 3 retries`** — proxy rejected without contacting upstream.

### Incident: 3 consecutive 5xx → key-level cooldown → provider-wide outage for the rest of the window

Proxy logs (fact chain):

```
06:19:42Z  Upstream error response 502 kimi-k3 (Bad Gateway, CF)
06:20:55Z  Key max failures reached, entering cooldown  failures=3   ← key-level, 60s
06:21:46Z  Upstream error response 502 kimi-k3 (stream_non_200) + cooldown re-armed, failures=4
```

- `ReportKeyFailure` (`pkg/keypool/health.go:397`): **any** non-quota, non-auth upstream failure — including CDN-level CF 502/524 that never reached the origin — increments a **key-level** counter; at **3 consecutive** failures the whole key enters 60s cooldown for **all models** (`MAX_FAILURES_REACHED`). Each subsequent failed probe re-arms it; only an upstream success resets the counter.
- The counter is reset by successes on ANY model (glm burst ran before k3 and kept resetting it), so the key survived glm's own 2 CF 502s — k3's two 40k CF 502s plus one more failure inside ~70s tripped it at 06:20:55.
- **Scoping:** the cooldown is key-level, NOT model-level — after 06:20:55, glm on approach would have 503'd too. glm escaped only because its groups had already finished at 06:17:33. With TR paused, both models were down platform-wide from 06:20:55 until the TR restore (06:22:12Z sync).
- This is the **third form of single-key SPOF** in two weeks (after day-8's portal validation eviction — fixed — and the pre-existing model_health 24h disable). Each layer is defensible in isolation; with exactly one key on a provider, every protective threshold becomes a kill switch.

### Day-10 findings

1. **New endpoint performance is real** — glm burst med 3.6s vs 18.1s (day-9, same 17:00 slot family); 40k ~40% faster. Same-origin different CF exposure (`:443` vs `:8443`). Retain the new key.
2. **40k prompts on approach now hit the CF wall on BOTH models intermittently** (glm g6/g10, k3 g5/g6 all CF 502/524 at 60–121s). The CF 100s origin timeout is the binding constraint for large contexts, not approach itself.
3. **Key-level `MAX_FAILURES` cooldown needs single-key-provider awareness**: 3 consecutive CDN errors = 1-minute full-provider outage, and a persistently erroring origin turns into a probe-fail/re-arm loop. Mitigations, in order of value: (a) a **second approach key** (requested since day-8 — would have absorbed this entire incident); (b) don't count CDN-edge 502/524 (error 52x from CF = origin never answered) toward the consecutive-failure counter the same as authoritative upstream 5xx — or count them per-model like `ModelHealth`; (c) shorter/退避式 cooldown for single-key providers. (b)/(c) are proxy changes — flag for later, do not block.
4. Production was protected the moment TR was un-paused (06:22:12Z) — failover chain works; the outage window only existed because the test had TR paused.
5. TR service_models re-activated after the run; production routing restored.

### Post-run events (same day, user absent — resolved 10-29 ~11:00 UTC)

1. **New approach key has insufficient upstream quota for kimi-k3.** Verification call at 06:29Z returned 403 `insufficient_user_quota` (剩余额度 ¥-8.75) from tokenservices.store; glm-5.3 on the SAME key kept working (bare "Say OK" 200s, ~10s). The relay denies kimi-k3 but admits glm — likely per-request cost estimation against a negative balance, or kimi-specific quota pool. **Action needed from platform owner: top up the approach account.**
2. **Mitigation applied:** `model_provider_routing` for kimi-k3 swapped to TR-primary / approach-secondary (glm unchanged, approach-primary). Proxy synced 06:37:12Z.
3. **Discovery: TR had been dead platform-wide for ~32h.** Last TR usage_record: **2026-10-28 02:33 UTC**; zero TR traffic for any user/model since. Symptom: every request for any TR-model collected zero TR candidates at selection time and fell through to lower-priority providers (kimi-k3 → approach with `is_fallback=t`; TR-only models like `mimo-v2.6-flash` → instant 503 "no available upstream keys").
   - Eliminated hypotheses: TR keys all `valid` in portal DB and shipped in every sync (55 keys incl. 32 TR); no TR key failure/cooldown/inactive events in proxy logs; `model_health` had no kimi-k3 disables; Redis-persisted pool showed 32 TR keys active+enabled; per-key `reset-runtime` on one TR key did NOT restore selection.
   - **Resolution: proxy container restart (11:00:54Z) restored TR immediately** — kimi-k3 → tokenrouter `is_fallback=f` (per the new routing), mimo → tokenrouter in 0.98s. Conclusion: **corrupt in-memory runtime state** excluded all TR keys from selection while every external signal (portal DB, sync payload, Redis persistence) looked healthy. The corrupting trigger at 10-28 02:33 is not identified — no TR error events in the log window; candidates include a stuck pool-level structure that `reset-runtime` doesn't clear (e.g. per-key `upstreamUsage` RPM windows, which reset-runtime does not touch).
4. **Open items:** (a) approach account top-up (platform owner); (b) proxy: make `reset-runtime` also clear `upstreamUsage` windows, or add an admin endpoint to dump per-provider selection state — without visibility, the only recovery tool is a full restart; (c) consider a periodic self-heal: if a provider has 0 selections across N minutes while sync ships N>0 keys, auto-reset runtime state; (d) second approach key still outstanding (SPOF #4 now demonstrated at the quota layer: one key's balance kills the model).
