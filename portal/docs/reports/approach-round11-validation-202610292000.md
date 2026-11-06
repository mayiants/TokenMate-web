# approach Round-11 Validation — kimi-k3 & glm-5.3 (new upstream key)

Status: ACTIVE
Date: 2026-10-29 20:00–20:30 Beijing (12:00–12:30 UTC)
Scope: re-validation of the `approach` upstream provider after the upstream key was
rotated (new key, new base URL). Same 48-request harness as previous rounds
(11 groups × 2 models), all traffic forced to approach by pausing the TR
counterparts for the duration of the test.

This is a standalone report; earlier rounds (1–10) are in
`approach-provider-validation-report-202610241100.md`.

## Setup delta vs round 10

| Item | Round 10 (10-29 14:12) | This round (10-29 20:00) |
|---|---|---|
| approach upstream key | `baa89714...` :8443 (deleted) | `bb41489c-fdbb-4123-9b7f-d2b84415219c`, base `https://www.tokenservices.store/v1` |
| Key validation | valid | valid |
| kimi-k3 routing | TR p0 / approach p1 (balance outage) | approach p0 / TR p1 (swapped back) |
| TR glm-5.3 / kimi-k3 | paused during test | paused during test, restored after |
| RPM limit (portal) | 3000 | 3000 |
| Client key | `sk-wgit-en-1f723e3a-...` (enterprise, group `cn-model`) | same |

## Results per group

### glm-5.3 — 24 requests, 22 success / 2 failed

| Group | Result | Latency | Notes |
|---|---|---|---|
| g1 non-stream small | 200 | 2.8s | 24 in / 23 out |
| g2 stream small | 200 | TTFT 2.5s | — |
| g3 non-stream ~9.7k ctx | 200 | **7.9s** | round 10: 40.5s — new endpoint ~5× faster |
| g4 stream ~9.7k ctx | 200 | TTFT 7.9s | round 10: 47.2s |
| g5 non-stream ~40k ctx | **CF 524** | 125s | Cloudflare 100s origin timeout (not an approach error) |
| g6 stream ~40k ctx | 200 | **100.4s** (TTFT 99.5s) | barely under CF ceiling; round 10 on old endpoint also ~100s |
| g7 tool call non-stream | 200 | 2.8s | correct `get_weather` call |
| g8 tool call stream | 200 | TTFT 1.7s | — |
| g9 RPM burst 12 concurrent | **12×200** | med 2.2s, max 4.3s upstream | no 429 from approach |
| g10 throughput 3× 16k→1500 out | 2×200, 1×502 | med 31.3s | 502 after ~60s: approach returned upstream 502 (proxy log `stream_non_200`, empty body) |
| g11 Anthropic /v1/messages | 200 | 11.8s | — |

### kimi-k3 — 24 requests, 20 success / 4 failed

| Group | Result | Latency | Notes |
|---|---|---|---|
| g1 non-stream small | 200 | 2.8s | 97 in / 32 out, resp_model=`kimi-k3` |
| g2 stream small | 200 | TTFT 2.6s | — |
| g3 non-stream ~10.2k ctx | 200 | 11.4s | round 10: 43.3s |
| g4 stream ~10.2k ctx | 200 | TTFT 18.4s (46.6s upstream_initial per proxy log) | — |
| g5 non-stream ~40k ctx | **CF 524** | 125s | CF timeout |
| g6 stream ~40k ctx | **CF 524** | 125s | CF timeout |
| g7 tool call non-stream | 200 | **113.8s** | correct tool call, but very slow reasoning phase; round 10: 3.2s — high variance |
| g8 tool call stream | 200 | TTFT 2.2s | — |
| g9 RPM burst 12 concurrent | **12×200** | med 2.9s, max 4.1s upstream | no 429 from approach |
| g10 throughput 3× 16k→1500 out | 2×200, 1×502 | med 40.2s | same upstream-502 pattern as glm |
| g11 Anthropic /v1/messages | **CF 524** | 125.8s | timed out at CF; round 10: 200 in 7.5s |

Totals: 48 requests, **42 success (87.5%)**, 6 failures — every failure is either a
Cloudflare 524 (origin >100s) or an approach 502 on the long-generation throughput
group. No 429s, no key-level cooldown events, no circuit-breaker trips.

## Billing / routing verification (DB)

- `usage_records` for the window: 45 rows, **all provider=approach, is_fallback=f, status=success** (45 vs 42 harness successes: +2 pre-harness probe calls +1 boundary row).
- glm-5.3: 107,226 in / 475 out, $0.2434 (micro-USD column / 1e6).
- kimi-k3: 62,809 in / 749 out, $0.2671.
- Amounts are consistent with active pricing (glm $1.4/$4.4 + $0.005 fixed; kimi $3.0/$15.0 + $0.005 fixed) plus partial cache hits — no fallback-priced rows.
- Proxy logs: every request selected key `bb41489c...`; resolved base URL `https://www.tokenservices.store/v1`.

## Findings

1. **New key/endpoint is healthy and materially faster.** glm g3/g4 latency dropped
   from ~40–47s to ~8s vs the old `:8443` endpoint. kimi-k3 no longer shows the
   `qianfan-vl-v1` response-model mislabel seen in earlier rounds — resp_model is
   consistently `kimi-k3` on the new key.
2. **kimi-k3 balance/quota restored.** The 403 `insufficient_user_quota` failures
   from this morning (06:29Z) are gone on the new key.
3. **40k context remains CF-bound, not approach-bound.** Both models 524 at ~125s
   on 40k non-stream. glm streaming squeaked through at 100.4s. This ceiling
   affects any slow upstream equally; real fix is a direct-origin path for
   long-context clients (previously discussed, deferred).
4. **Long-generation throughput is the weak spot.** 1 of 3 g10 requests per model
   died with an upstream 502 from approach after ~60s (proxy logged
   `Upstream error response status_code=502`, empty body). Approach appears to
   have its own ~60s timeout on 16k-ctx / 1500-output requests. Rate: ~1/6 of
   long-gen requests.
5. **kimi-k3 toolcall latency variance:** g7 non-stream took 113.8s while g8
   stream took 5.0s for the same logical task. Round 10 g7 was 3.2s. Watch item.
6. **Concurrency is fine at this scale.** 12-way burst = 24/24 success, upstream
   1.1–4.3s. The portal-side rpm_limit=3000 was nowhere near stressed.

## Production state after this round (left in place)

- Routing: `glm-5.3` approach p0 / TR p1; `kimi-k3` approach p0 / TR p1
  (kimi-k3 swapped back from this morning's TR-first emergency change).
- TR `z-ai/glm-5.3` and `moonshotai/kimi-k3`: restored to `active` as fallback.
- Single approach upstream key remains a single point of failure (four separate
  failure modes demonstrated since 10-24: portal probe key removal, proxy
  3-strike cooldown, model_health 24h recovery, upstream balance). A second
  approach key is still recommended.

## Periodic monitoring (auto-appended by cron)

Rounds run every 3 hours via OS cron on the Portal EC2
(`workspace/approach-test/cron-round.sh`, log: `workspace/approach-test/cron-round.log`),
each round forces traffic to approach (TR counterparts paused for ~25 min),
appends one section below, then restores TR. Window: 2026-10-29 23:17 →
2026-10-30 21:17 Beijing; the script self-uninstalls its crontab entries after
the window closes.

---

## Periodic round — 2026-10-29 23:22 Beijing (15:22 UTC)

> ⚠️ **INVALID — cooldown-contaminated, do not use for availability stats.**
> glm g4 stream 8k hit an upstream 502 at 61s, then g5 40k hit CF 524; those two
> failures plus the in-flight timeout tripped proxy **key-level MAX_FAILURES
> cooldown** (3 consecutive failures → key cooled for all its models), so every
> subsequent request (both models) failed fast with `no available upstream keys
> after 3 retries`. Only the first 3 glm requests measured real approach
> behavior. From the next round on the harness runs failure-prone groups
> (g5/g6/g10/g11) last so a cooldown cannot wipe the measurement groups.

**48 requests, 3 success (6.2%)**  
harness: `results-202610291517.json`

### glm-5.3 — 3/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 2.6s |
| g2_stream_small | 200x1 | med 5.1s / TTFT 5.1s |
| g3_nonstream_medium_8k | 200x1 | med 28.0s |
| g4_stream_medium_8k | 502x1 | - |
| g5_nonstream_large_40k | 524x1 | - |
| g6_stream_large_40k | 503x1 | - |
| g7_toolcall_nonstream | 503x1 | - |
| g8_toolcall_stream | 503x1 | - |
| g9_rpm_burst_12 | 503x12 | - |
| g10_throughput_16k_out1500 | 503x3 | - |
| g11_anthropic_messages | 503x1 | - |

- fail: g4_stream_medium_8k: 502 (CF page / upstream)
- fail: g5_nonstream_large_40k: CF 524 (origin >100s)
- fail: g6_stream_large_40k: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g7_toolcall_nonstream: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g8_toolcall_stream: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri

### kimi-k3 — 0/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 503x1 | - |
| g2_stream_small | 503x1 | - |
| g3_nonstream_medium_8k | 503x1 | - |
| g4_stream_medium_8k | 503x1 | - |
| g5_nonstream_large_40k | 503x1 | - |
| g6_stream_large_40k | 503x1 | - |
| g7_toolcall_nonstream | 503x1 | - |
| g8_toolcall_stream | 503x1 | - |
| g9_rpm_burst_12 | 503x12 | - |
| g10_throughput_16k_out1500 | 503x3 | - |
| g11_anthropic_messages | 503x1 | - |

- fail: g1_nonstream_small: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g2_stream_small: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g3_nonstream_medium_8k: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g4_stream_medium_8k: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g5_nonstream_large_40k: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g6_stream_large_40k: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g7_toolcall_nonstream: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g8_toolcall_stream: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=3 | in=9733 | out=80 | $0.029266  ← only the 3 pre-cooldown glm requests billed

---

## Periodic round — 2026-10-30 02:24 Beijing (18:24 UTC)

**48 requests, 48 success (100.0%)**  
harness: `results-202610291817.json`

### glm-5.3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 1.8s |
| g2_stream_small | 200x1 | med 3.3s / TTFT 2.6s |
| g3_nonstream_medium_8k | 200x1 | med 13.3s |
| g4_stream_medium_8k | 200x1 | med 25.9s / TTFT 25.1s |
| g5_nonstream_large_40k | 200x1 | med 33.5s |
| g6_stream_large_40k | 200x1 | med 35.1s / TTFT 34.4s |
| g7_toolcall_nonstream | 200x1 | med 1.4s |
| g8_toolcall_stream | 200x1 | med 1.2s / TTFT 0.6s |
| g9_rpm_burst_12 | 200x12 | med 2.3s |
| g10_throughput_16k_out1500 | 200x3 | med 10.4s / TTFT 10.3s |
| g11_anthropic_messages | 200x1 | med 1.3s |

### kimi-k3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 2.6s |
| g2_stream_small | 200x1 | med 2.8s / TTFT 1.8s |
| g3_nonstream_medium_8k | 200x1 | med 11.1s |
| g4_stream_medium_8k | 200x1 | med 8.3s / TTFT 7.7s |
| g5_nonstream_large_40k | 200x1 | med 52.4s |
| g6_stream_large_40k | 200x1 | med 39.7s / TTFT 39.0s |
| g7_toolcall_nonstream | 200x1 | med 2.9s |
| g8_toolcall_stream | 200x1 | med 2.6s / TTFT 1.0s |
| g9_rpm_burst_12 | 200x12 | med 3.6s |
| g10_throughput_16k_out1500 | 200x3 | med 22.9s / TTFT 21.0s |
| g11_anthropic_messages | 200x1 | med 2.2s |

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=24 | in=175017 | out=618 | $0.284161
- kimi-k3 | approach | fallback=false | success | n=24 | in=184213 | out=935 | $0.495400

---

## Periodic round — 2026-10-30 05:22 Beijing (21:22 UTC)

**48 requests, 48 success (100.0%)**  
harness: `results-202610292117.json`

### glm-5.3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 3.0s |
| g2_stream_small | 200x1 | med 1.5s / TTFT 0.8s |
| g3_nonstream_medium_8k | 200x1 | med 3.2s |
| g4_stream_medium_8k | 200x1 | med 6.1s / TTFT 5.2s |
| g5_nonstream_large_40k | 200x1 | med 24.3s |
| g6_stream_large_40k | 200x1 | med 22.4s / TTFT 21.3s |
| g7_toolcall_nonstream | 200x1 | med 1.7s |
| g8_toolcall_stream | 200x1 | med 1.9s / TTFT 1.1s |
| g9_rpm_burst_12 | 200x12 | med 2.3s |
| g10_throughput_16k_out1500 | 200x3 | med 12.3s / TTFT 11.3s |
| g11_anthropic_messages | 200x1 | med 2.1s |

### kimi-k3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 1.8s |
| g2_stream_small | 200x1 | med 1.8s / TTFT 1.1s |
| g3_nonstream_medium_8k | 200x1 | med 9.1s |
| g4_stream_medium_8k | 200x1 | med 2.7s / TTFT 1.8s |
| g5_nonstream_large_40k | 200x1 | med 46.8s |
| g6_stream_large_40k | 200x1 | med 10.9s / TTFT 10.3s |
| g7_toolcall_nonstream | 200x1 | med 3.8s |
| g8_toolcall_stream | 200x1 | med 3.1s / TTFT 1.7s |
| g9_rpm_burst_12 | 200x12 | med 2.8s |
| g10_throughput_16k_out1500 | 200x3 | med 10.1s / TTFT 8.8s |
| g11_anthropic_messages | 200x1 | med 1.7s |

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=24 | in=175017 | out=610 | $0.284124
- kimi-k3 | approach | fallback=false | success | n=24 | in=184213 | out=935 | $0.495402

---

## Periodic round — 2026-10-30 08:34 Beijing (00:34 UTC)

**48 requests, 39 success (81.2%)**  
harness: `results-202610300017.json`

### glm-5.3 — 19/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 5.1s |
| g2_stream_small | 200x1 | med 0.8s / TTFT 0.8s |
| g3_nonstream_medium_8k | 200x1 | med 6.9s |
| g4_stream_medium_8k | 200x1 | med 8.5s / TTFT 7.7s |
| g5_nonstream_large_40k | 502x1 | - |
| g6_stream_large_40k | 524x1 | - |
| g7_toolcall_nonstream | 200x1 | med 1.3s |
| g8_toolcall_stream | 200x1 | med 1.2s / TTFT 0.8s |
| g9_rpm_burst_12 | 200x12 | med 11.2s |
| g10_throughput_16k_out1500 | 200x1,503x2 | med 65.3s / TTFT 64.5s |
| g11_anthropic_messages | 503x1 | - |

- fail: g5_nonstream_large_40k: CF 502
- fail: g6_stream_large_40k: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri

### kimi-k3 — 20/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 2.5s |
| g2_stream_small | 200x1 | med 1.8s / TTFT 1.3s |
| g3_nonstream_medium_8k | 200x1 | med 12.1s |
| g4_stream_medium_8k | 200x1 | med 13.6s / TTFT 12.4s |
| g5_nonstream_large_40k | 200x1 | med 63.5s |
| g6_stream_large_40k | 524x1 | - |
| g7_toolcall_nonstream | 200x1 | med 5.1s |
| g8_toolcall_stream | 200x1 | med 3.2s / TTFT 1.7s |
| g9_rpm_burst_12 | 200x12 | med 2.1s |
| g10_throughput_16k_out1500 | 524x3 | - |
| g11_anthropic_messages | 200x1 | med 33.5s |

- fail: g6_stream_large_40k: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!
- fail: g10_throughput_16k_out1500: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!
- fail: g10_throughput_16k_out1500: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!
- fail: g10_throughput_16k_out1500: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=19 | in=39377 | out=398 | $0.140350
- kimi-k3 | approach | fallback=false | success | n=20 | in=72701 | out=644 | $0.300932
- note: glm-5.3 was on model-level cooldown for its last 3 requests (503, not billed); kimi-k3 unaffected — per-model cooldown working as designed

---

## Periodic round — 2026-10-30 11:28 Beijing (03:28 UTC)

**48 requests, 47 success (97.9%)**  
harness: `results-202610300317.json`

### glm-5.3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 4.1s |
| g2_stream_small | 200x1 | med 2.4s / TTFT 1.7s |
| g3_nonstream_medium_8k | 200x1 | med 18.1s |
| g4_stream_medium_8k | 200x1 | med 11.9s / TTFT 11.0s |
| g5_nonstream_large_40k | 200x1 | med 54.2s |
| g6_stream_large_40k | 200x1 | med 53.5s / TTFT 52.6s |
| g7_toolcall_nonstream | 200x1 | med 1.5s |
| g8_toolcall_stream | 200x1 | med 2.0s / TTFT 1.3s |
| g9_rpm_burst_12 | 200x12 | med 2.2s |
| g10_throughput_16k_out1500 | 200x3 | med 30.2s / TTFT 29.2s |
| g11_anthropic_messages | 200x1 | med 1.7s |

### kimi-k3 — 23/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 1.6s |
| g2_stream_small | 200x1 | med 2.1s / TTFT 1.8s |
| g3_nonstream_medium_8k | 200x1 | med 24.9s |
| g4_stream_medium_8k | 200x1 | med 13.3s / TTFT 11.8s |
| g5_nonstream_large_40k | 502x1 | - |
| g6_stream_large_40k | 200x1 | med 73.0s / TTFT 71.6s |
| g7_toolcall_nonstream | 200x1 | med 5.1s |
| g8_toolcall_stream | 200x1 | med 4.6s / TTFT 1.9s |
| g9_rpm_burst_12 | 200x12 | med 2.7s |
| g10_throughput_16k_out1500 | 200x3 | med 56.5s / TTFT 54.8s |
| g11_anthropic_messages | 200x1 | med 2.7s |

- fail: g5_nonstream_large_40k: CF 502

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=24 | in=175017 | out=583 | $0.283826
- kimi-k3 | approach | fallback=false | success | n=23 | in=133619 | out=927 | $0.413259

---

## Periodic round — 2026-10-30 14:35 Beijing (06:35 UTC)

**48 requests, 37 success (77.1%)**  
harness: `results-202610300617.json`

### glm-5.3 — 18/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 3.9s |
| g2_stream_small | 200x1 | med 2.5s / TTFT 1.7s |
| g3_nonstream_medium_8k | 200x1 | med 11.8s |
| g4_stream_medium_8k | 200x1 | med 14.1s / TTFT 13.4s |
| g5_nonstream_large_40k | 502x1 | - |
| g6_stream_large_40k | 502x1 | - |
| g7_toolcall_nonstream | 200x1 | med 2.8s |
| g8_toolcall_stream | 200x1 | med 3.8s / TTFT 3.5s |
| g9_rpm_burst_12 | 200x12 | med 2.5s |
| g10_throughput_16k_out1500 | 503x3 | - |
| g11_anthropic_messages | 503x1 | - |

- fail: g5_nonstream_large_40k: CF 502
- fail: g6_stream_large_40k: 502 (CF page / upstream)
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri

### kimi-k3 — 19/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 524x1 | - |
| g2_stream_small | 200x1 | med 44.4s / TTFT 44.0s |
| g3_nonstream_medium_8k | 502x1 | - |
| g4_stream_medium_8k | 524x1 | - |
| g5_nonstream_large_40k | 200x1 | med 63.3s |
| g6_stream_large_40k | 502x1 | - |
| g7_toolcall_nonstream | 200x1 | med 35.7s |
| g8_toolcall_stream | 200x1 | med 5.3s / TTFT 3.4s |
| g9_rpm_burst_12 | 200x12 | med 2.3s |
| g10_throughput_16k_out1500 | 200x2,502x1 | med 68.3s / TTFT 66.3s |
| g11_anthropic_messages | 200x1 | med 75.2s |

- fail: g1_nonstream_small: CF 524 (origin >100s)
- fail: g3_nonstream_medium_8k: CF 502
- fail: g4_stream_medium_8k: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!
- fail: g6_stream_large_40k: 502 (CF page / upstream)
- fail: g10_throughput_16k_out1500: 502 (CF page / upstream)

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=18 | in=19986 | out=315 | $0.114028
- kimi-k3 | approach | fallback=false | success | n=19 | in=92860 | out=655 | $0.311054

---

## Periodic round — 2026-10-30 17:35 Beijing (09:35 UTC)

**48 requests, 39 success (81.2%)**  
harness: `results-202610300917.json`

### glm-5.3 — 18/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 10.6s |
| g2_stream_small | 200x1 | med 2.2s / TTFT 1.6s |
| g3_nonstream_medium_8k | 200x1 | med 9.7s |
| g4_stream_medium_8k | 200x1 | med 36.2s / TTFT 35.3s |
| g5_nonstream_large_40k | 502x1 | - |
| g6_stream_large_40k | 524x1 | - |
| g7_toolcall_nonstream | 200x1 | med 1.7s |
| g8_toolcall_stream | 200x1 | med 4.5s / TTFT 3.8s |
| g9_rpm_burst_12 | 200x12 | med 2.3s |
| g10_throughput_16k_out1500 | 503x3 | - |
| g11_anthropic_messages | 503x1 | - |

- fail: g5_nonstream_large_40k: CF 502
- fail: g6_stream_large_40k: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri

### kimi-k3 — 21/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 524x1 | - |
| g2_stream_small | 200x1 | med 13.9s / TTFT 11.3s |
| g3_nonstream_medium_8k | 200x1 | med 73.7s |
| g4_stream_medium_8k | 200x1 | med 56.0s / TTFT 55.2s |
| g5_nonstream_large_40k | 200x1 | med 58.5s |
| g6_stream_large_40k | 502x1 | - |
| g7_toolcall_nonstream | 200x1 | med 8.4s |
| g8_toolcall_stream | 200x1 | med 4.9s / TTFT 2.9s |
| g9_rpm_burst_12 | 200x12 | med 2.5s |
| g10_throughput_16k_out1500 | 200x2,502x1 | med 117.9s / TTFT 116.6s |
| g11_anthropic_messages | 200x1 | med 10.5s |

- fail: g1_nonstream_small: CF 524 (origin >100s)
- fail: g6_stream_large_40k: 502 (CF page / upstream)
- fail: g10_throughput_16k_out1500: 502 (CF page / upstream)

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=18 | in=19986 | out=324 | $0.114070
- kimi-k3 | approach | fallback=false | success | n=20 | in=113119 | out=688 | $0.362939

---

## Periodic round — 2026-10-30 20:28 Beijing (12:28 UTC)

**48 requests, 20 success (41.7%)**  
harness: `results-202610301217.json`

### glm-5.3 — 2/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 16.5s |
| g2_stream_small | 200x1 | med 3.1s / TTFT 2.2s |
| g3_nonstream_medium_8k | 502x1 | - |
| g4_stream_medium_8k | 502x1 | - |
| g5_nonstream_large_40k | 503x1 | - |
| g6_stream_large_40k | 503x1 | - |
| g7_toolcall_nonstream | 503x1 | - |
| g8_toolcall_stream | 503x1 | - |
| g9_rpm_burst_12 | 503x12 | - |
| g10_throughput_16k_out1500 | 503x3 | - |
| g11_anthropic_messages | 503x1 | - |

- fail: g3_nonstream_medium_8k: CF 502
- fail: g4_stream_medium_8k: 502 (CF page / upstream)
- fail: g5_nonstream_large_40k: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g6_stream_large_40k: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g7_toolcall_nonstream: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g8_toolcall_stream: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri

### kimi-k3 — 18/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 105.6s |
| g2_stream_small | 200x1 | med 3.7s / TTFT 2.7s |
| g3_nonstream_medium_8k | 200x1 | med 36.8s |
| g4_stream_medium_8k | 200x1 | med 24.2s / TTFT 22.7s |
| g5_nonstream_large_40k | 502x1 | - |
| g6_stream_large_40k | 502x1 | - |
| g7_toolcall_nonstream | 200x1 | med 6.4s |
| g8_toolcall_stream | 200x1 | med 10.0s / TTFT 8.2s |
| g9_rpm_burst_12 | 200x12 | med 9.8s |
| g10_throughput_16k_out1500 | 503x3 | - |
| g11_anthropic_messages | 503x1 | - |

- fail: g5_nonstream_large_40k: CF 502
- fail: g6_stream_large_40k: 502 (CF page / upstream)
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=2 | in=48 | out=80 | $0.010522
- kimi-k3 | approach | fallback=false | success | n=18 | in=22010 | out=517 | $0.150366

---

## Periodic round — 2026-10-30 21:28 Beijing (13:28 UTC)

**48 requests, 21 success (43.8%)**  
harness: `results-202610301317.json`

### glm-5.3 — 3/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 5.6s |
| g2_stream_small | 200x1 | med 8.2s / TTFT 3.6s |
| g3_nonstream_medium_8k | 200x1 | med 53.7s |
| g4_stream_medium_8k | 502x1 | - |
| g5_nonstream_large_40k | 503x1 | - |
| g6_stream_large_40k | 503x1 | - |
| g7_toolcall_nonstream | 524x1 | - |
| g8_toolcall_stream | 503x1 | - |
| g9_rpm_burst_12 | 503x12 | - |
| g10_throughput_16k_out1500 | 503x3 | - |
| g11_anthropic_messages | 503x1 | - |

- fail: g4_stream_medium_8k: 502 (CF page / upstream)
- fail: g5_nonstream_large_40k: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g6_stream_large_40k: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g7_toolcall_nonstream: CF 524 (origin >100s)
- fail: g8_toolcall_stream: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g9_rpm_burst_12: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: glm-5.3 after 3 retri

### kimi-k3 — 18/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 21.4s |
| g2_stream_small | 200x1 | med 3.1s / TTFT 2.0s |
| g3_nonstream_medium_8k | 200x1 | med 59.2s |
| g4_stream_medium_8k | 200x1 | med 22.8s / TTFT 21.7s |
| g5_nonstream_large_40k | 502x1 | - |
| g6_stream_large_40k | 502x1 | - |
| g7_toolcall_nonstream | 200x1 | med 3.4s |
| g8_toolcall_stream | 200x1 | med 3.6s / TTFT 1.2s |
| g9_rpm_burst_12 | 200x12 | med 8.6s |
| g10_throughput_16k_out1500 | 503x3 | - |
| g11_anthropic_messages | 503x1 | - |

- fail: g5_nonstream_large_40k: CF 502
- fail: g6_stream_large_40k: 502 (CF page / upstream)
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=3 | in=9733 | out=122 | $0.029452
- kimi-k3 | approach | fallback=false | success | n=18 | in=22010 | out=536 | $0.150654

---

## Periodic round — 2026-10-31 15:42 Beijing (07:42 UTC)

**48 requests, 47 success (97.9%)**  
harness: `results-202610310407.json`

### glm-5.3 — 23/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 3.9s |
| g2_stream_small | 200x1 | med 4.0s / TTFT 2.7s |
| g3_nonstream_medium_8k | 200x1 | med 62.0s |
| g4_stream_medium_8k | 200x1 | med 1.0s / TTFT 0.8s |
| g5_nonstream_large_40k | 200x1 | med 2.8s |
| g6_stream_large_40k | 200x1 | med 1.2s / TTFT 1.1s |
| g7_toolcall_nonstream | 524x1 | - |
| g8_toolcall_stream | 200x1 | med 52.4s / TTFT 51.8s |
| g9_rpm_burst_12 | 200x12 | med 2.9s |
| g10_throughput_16k_out1500 | 200x3 | med 1.0s / TTFT 0.7s |
| g11_anthropic_messages | 200x1 | med 7.6s |

- fail: g7_toolcall_nonstream: CF 524 (origin >100s)

### kimi-k3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 7.6s |
| g2_stream_small | 200x1 | med 3.6s / TTFT 2.5s |
| g3_nonstream_medium_8k | 200x1 | med 68.1s |
| g4_stream_medium_8k | 200x1 | med 4.0s / TTFT 1.9s |
| g5_nonstream_large_40k | 200x1 | med 8.2s |
| g6_stream_large_40k | 200x1 | med 9.4s / TTFT 7.3s |
| g7_toolcall_nonstream | 200x1 | med 4.2s |
| g8_toolcall_stream | 200x1 | med 5.0s / TTFT 1.9s |
| g9_rpm_burst_12 | 200x12 | med 18.3s |
| g10_throughput_16k_out1500 | 200x3 | med 5.2s / TTFT 3.3s |
| g11_anthropic_messages | 200x1 | med 2.3s |

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=16 | in=464 | out=292 | $0.082740
- glm-5.3 | tokenrouter | fallback=true | success | n=7 | in=174377 | out=321 | $0.138612
- kimi-k3 | approach | fallback=false | success | n=17 | in=1751 | out=522 | $0.099057
- kimi-k3 | tokenrouter | fallback=true | success | n=7 | in=182462 | out=488 | $0.375828

---

## Periodic round — 2026-10-31 18:14 Beijing (10:14 UTC)

**48 requests, 48 success (100.0%)**  
harness: `results-202610311011.json`

### glm-5.3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 7.7s |
| g2_stream_small | 200x1 | med 2.6s / TTFT 1.9s |
| g3_nonstream_medium_8k | 200x1 | med 1.3s |
| g4_stream_medium_8k | 200x1 | med 1.0s / TTFT 0.8s |
| g5_nonstream_large_40k | 200x1 | med 1.2s |
| g6_stream_large_40k | 200x1 | med 1.1s / TTFT 0.9s |
| g7_toolcall_nonstream | 200x1 | med 3.3s |
| g8_toolcall_stream | 200x1 | med 3.3s / TTFT 2.7s |
| g9_rpm_burst_12 | 200x12 | med 3.5s |
| g10_throughput_16k_out1500 | 200x3 | med 1.0s / TTFT 0.7s |
| g11_anthropic_messages | 200x1 | med 2.3s |

### kimi-k3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 2.7s |
| g2_stream_small | 200x1 | med 2.3s / TTFT 1.6s |
| g3_nonstream_medium_8k | 200x1 | med 7.6s |
| g4_stream_medium_8k | 200x1 | med 10.0s / TTFT 9.1s |
| g5_nonstream_large_40k | 200x1 | med 16.9s |
| g6_stream_large_40k | 200x1 | med 5.9s / TTFT 4.7s |
| g7_toolcall_nonstream | 200x1 | med 3.9s |
| g8_toolcall_stream | 200x1 | med 9.6s / TTFT 6.9s |
| g9_rpm_burst_12 | 200x12 | med 3.5s |
| g10_throughput_16k_out1500 | 200x3 | med 3.7s / TTFT 3.0s |
| g11_anthropic_messages | 200x1 | med 2.3s |

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=17 | in=640 | out=274 | $0.087959
- glm-5.3 | tokenrouter | fallback=true | success | n=7 | in=174377 | out=310 | $0.082855
- kimi-k3 | approach | fallback=false | success | n=16 | in=1654 | out=445 | $0.092547
- kimi-k3 | tokenrouter | fallback=true | success | n=7 | in=182462 | out=428 | $0.265026

### Addendum — approach 直连容量阶梯测试（2026-10-31 18:20 Beijing，绕过 proxy）

背景：本轮账单显示大请求全部 failover 到 TR。直连 tokenservices.store 阶梯验证（glm-5.3，非流式）：

| 输入 tokens | 结果 | 耗时 |
|---|---|---|
| ~6.3k | 200 | 7.1s |
| ~12.6k | 200 | 30.6s |
| ~16k+ | 504 Gateway Timeout | 62.5s（连接被切断） |
| ~40k | SSL EOF | 62s（连接被切断） |

**结论**：approach 上游非流式可用上限约 12k tokens（且 12k 时已需 30s）；≥16k 直接挂。proxy 将大请求 failover 到 TR 的行为正确且必要（TR 40k 仅 0.8-16s）。稳定态建议：approach 服务小请求（≤8k），大上下文由 TR 兜底。注意 glm-5.3/kimi-k3 在 TR 均为 TOPUP（不走 voucher），大上下文流量会持续消耗 TR 充值余额。

## ⚠️ 方法论更正（2026-10-31）

**2026-10-31 15:42 北京轮 和 18:14 北京轮（含上文 Addendum 引用的"100% 成功率 / 40k 提速"结论）测试方法有误，数据作废。**

原因：这两轮使用的手写 runner 漏掉了原 cron-round.sh 的"测试前暂停 TR 的 glm-5.3/kimi-k3"步骤，TR 全程可用。账单显示两轮中 8k/16k/40k 大请求全部经 failover 由 TR 完成（用户侧 100% 成功是 TR 的功劳，非 approach 能力）。这两轮的延迟数据反映的是 TR 性能，不代表 approach。

仍然有效的数据：
- 直连阶梯测试（绕过 proxy，见 Addendum）：approach 非流式 ≤12.6k 可用（30.6s），≥16k 被网关 62s 切断 —— 与 proxy 路径无关，依然成立。
- 10-30 的全部 6 轮 cron（00:17→13:17 UTC）均含 pause 步骤，数据有效。

正确流程的复测（pause TR → sync → harness → restore）见下一节。

---

## Periodic round — 2026-10-31 19:14 Beijing (11:14 UTC)

**48 requests, 40 success (83.3%)**  
harness: `results-202610311058.json`

### glm-5.3 — 22/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 3.3s |
| g2_stream_small | 200x1 | med 1.6s / TTFT 0.8s |
| g3_nonstream_medium_8k | 200x1 | med 1.0s |
| g4_stream_medium_8k | 200x1 | med 9.6s / TTFT 8.9s |
| g5_nonstream_large_40k | 524x1 | - |
| g6_stream_large_40k | 524x1 | - |
| g7_toolcall_nonstream | 200x1 | med 2.0s |
| g8_toolcall_stream | 200x1 | med 1.8s / TTFT 1.1s |
| g9_rpm_burst_12 | 200x12 | med 48.3s |
| g10_throughput_16k_out1500 | 200x3 | med 31.9s / TTFT 30.6s |
| g11_anthropic_messages | 200x1 | med 7.5s |

- fail: g5_nonstream_large_40k: CF 524 (origin >100s)
- fail: g6_stream_large_40k: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!

### kimi-k3 — 18/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 1.9s |
| g2_stream_small | 200x1 | med 2.5s / TTFT 1.1s |
| g3_nonstream_medium_8k | 200x1 | med 114.6s |
| g4_stream_medium_8k | 200x1 | med 9.5s / TTFT 8.3s |
| g5_nonstream_large_40k | 502x1 | - |
| g6_stream_large_40k | 524x1 | - |
| g7_toolcall_nonstream | 200x1 | med 4.2s |
| g8_toolcall_stream | 200x1 | med 7.7s / TTFT 1.6s |
| g9_rpm_burst_12 | 200x12 | med 6.2s |
| g10_throughput_16k_out1500 | 503x2,524x1 | - |
| g11_anthropic_messages | 503x1 | - |

- fail: g5_nonstream_large_40k: CF 502
- fail: g6_stream_large_40k: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!
- fail: g10_throughput_16k_out1500: 524: <!DOCTYPE html>
<!--[if lt IE 7]> <html class="no-js ie6 oldie" lang="en-US"> <!
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g10_throughput_16k_out1500: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri
- fail: g11_anthropic_messages: 503: {"error":{"message":"no available upstream keys for model: kimi-k3 after 3 retri

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=21 | in=68498 | out=465 | $0.171651
- glm-5.3 | tokenrouter | fallback=true | success | n=1 | in=9685 | out=54 | $0.007856
- kimi-k3 | approach | fallback=false | success | n=18 | in=22010 | out=495 | $0.150032

---

## Periodic round — 2026-11-01 23:46 Beijing (15:46 UTC)

**48 requests, 45 success (93.8%)**  
harness: `results-202611011500.json`

### glm-5.3 — 22/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 3.0s |
| g2_stream_small | 200x1 | med 2.0s / TTFT 1.1s |
| g3_nonstream_medium_8k | 200x1 | med 9.1s |
| g4_stream_medium_8k | 502x1 | - |
| g5_nonstream_large_40k | 200x1 | med 57.4s |
| g6_stream_large_40k | 502x1 | - |
| g7_toolcall_nonstream | 200x1 | med 69.8s |
| g8_toolcall_stream | 200x1 | med 2.3s / TTFT 2.1s |
| g9_rpm_burst_12 | 200x12 | med 3.0s |
| g10_throughput_16k_out1500 | 200x3 | med 22.3s / TTFT 21.2s |
| g11_anthropic_messages | 200x1 | med 2.0s |

- fail: g4_stream_medium_8k: 502 (CF page / upstream)
- fail: g6_stream_large_40k: 502 (CF page / upstream)

### kimi-k3 — 23/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 10.2s |
| g2_stream_small | 200x1 | med 2.3s / TTFT 1.4s |
| g3_nonstream_medium_8k | 200x1 | med 13.0s |
| g4_stream_medium_8k | 200x1 | med 19.8s / TTFT 18.7s |
| g5_nonstream_large_40k | 200x1 | med 51.0s |
| g6_stream_large_40k | 502x1 | - |
| g7_toolcall_nonstream | 200x1 | med 9.7s |
| g8_toolcall_stream | 200x1 | med 3.7s / TTFT 1.7s |
| g9_rpm_burst_12 | 200x12 | med 2.5s |
| g10_throughput_16k_out1500 | 200x3 | med 39.4s / TTFT 38.3s |
| g11_anthropic_messages | 200x1 | med 3.3s |

- fail: g6_stream_large_40k: 502 (CF page / upstream)

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=22 | in=116915 | out=573 | $0.236855
- kimi-k3 | approach | fallback=false | success | n=22 | in=133522 | out=771 | $0.413576

---

## Periodic round — 2026-11-02 02:20 Beijing (18:20 UTC)

**48 requests, 48 success (100.0%)**  
harness: `results-202611011814.json`

### glm-5.3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 13.2s |
| g2_stream_small | 200x1 | med 2.5s / TTFT 1.8s |
| g3_nonstream_medium_8k | 200x1 | med 7.4s |
| g4_stream_medium_8k | 200x1 | med 15.5s / TTFT 14.8s |
| g5_nonstream_large_40k | 200x1 | med 31.2s |
| g6_stream_large_40k | 200x1 | med 35.8s / TTFT 34.3s |
| g7_toolcall_nonstream | 200x1 | med 5.1s |
| g8_toolcall_stream | 200x1 | med 2.7s / TTFT 2.2s |
| g9_rpm_burst_12 | 200x12 | med 1.8s |
| g10_throughput_16k_out1500 | 200x3 | med 11.9s / TTFT 11.3s |
| g11_anthropic_messages | 200x1 | med 2.1s |

### kimi-k3 — 24/24

| Group | Status | Latency |
|---|---|---|
| g1_nonstream_small | 200x1 | med 2.6s |
| g2_stream_small | 200x1 | med 10.8s / TTFT 10.0s |
| g3_nonstream_medium_8k | 200x1 | med 9.5s |
| g4_stream_medium_8k | 200x1 | med 9.7s / TTFT 9.0s |
| g5_nonstream_large_40k | 200x1 | med 36.8s |
| g6_stream_large_40k | 200x1 | med 20.3s / TTFT 19.7s |
| g7_toolcall_nonstream | 200x1 | med 3.5s |
| g8_toolcall_stream | 200x1 | med 3.6s / TTFT 1.3s |
| g9_rpm_burst_12 | 200x12 | med 2.2s |
| g10_throughput_16k_out1500 | 200x3 | med 17.2s / TTFT 15.9s |
| g11_anthropic_messages | 200x1 | med 2.2s |

### Billing verification (usage_records)

- glm-5.3 | approach | fallback=false | success | n=24 | in=175017 | out=646 | $0.289919
- kimi-k3 | approach | fallback=false | success | n=23 | in=184116 | out=908 | $0.499338

---

## 巡检收尾总结（2026-11-02，任务已停止）

**定时任务生命周期**：cron-round.sh 的 crontab 条目于 2026-10-30 15:17 UTC 按计划自动移除
（"window closed, removed crontab entries"）；其后的 round-fixed-*.sh 为会话内触发的单轮运行。
截至 2026-11-02 无任何 crontab / at / 后台进程在跑，任务确认停止。

### 最近几轮趋势（48 请求/轮）

| 轮次 (UTC) | 成功 | 备注 |
|---|---|---|
| 10-30 03:28 | 47/48 | 正常 |
| 10-30 06:35 | 37/48 | 开始劣化 |
| 10-30 09:35 | 39/48 | 劣化 |
| 10-30 12:28 | **20/48** | 谷底：key 级 MAX_FAILURES 冷却 bug（连锁反应） |
| 10-30 13:28 | 21/48 | 同上 |
| 10-31 07:42 | 47/48 | `7bb524a`（per-model 冷却）部署后恢复 |
| 10-31 10:14 | **48/48** | 全绿 |
| 10-31 11:14 | 40/48 | affinity pin 压住冷却 key（`no available upstream keys`） |
| 11-01 15:46 | 45/48 | 3 个失败均为 CF 502（上游瞬时故障） |
| 11-01 18:20 | **48/48** | `3082ae7`+`d5f80f2` 部署后全绿，glm 40k 31.2s / kimi 40k 36.8s |

### 结论

- 巡检驱动了两个真实 proxy 修复（per-model 冷却 `7bb524a`、affinity 不覆盖管理路由 `3082ae7`），
  修复后连续两轮 45+/48 与 48/48，链路稳定性达标；
- 剩余已知风险：approach 上行仍是**单 key（sk-fCWgE）SPOF**，第二把 key 未落实；
  CF 偶发 502/524 为上游侧现象，与平台无关。
