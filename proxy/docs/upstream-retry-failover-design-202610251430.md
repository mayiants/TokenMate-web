# In-Request Upstream Retry & Failover — Design

**Doc:** upstream-retry-failover-design-202610251430
**Created:** 2026-10-25 14:30 UTC
**Status:** IMPLEMENTED (chat stream + non-stream; commit 4858ad7, deployed 2026-10-25). chat + anthropic->openai + /v1/responses non-stream done; raw-HTTP streaming passthroughs remain follow-up.
**Author:** platform

---

## 0. Scope & relationship to the existing fallback doc

There is an **existing, fully-implemented** mechanism documented in
[`cross-provider-fallback-routing-202605161900.md`](./cross-provider-fallback-routing-202605161900.md):
when the preferred provider has **no available key**, `resolveProviderAndKey`
already picks a key from any other provider that serves the model, ordered by
the admin-configured `model_provider_routing.priority`. That is **key-selection
fallback** — it runs **once, before the upstream request is sent**.

**This doc designs a different, missing capability: _in-request_ retry &
failover _after_ the upstream request has been sent and the upstream returned a
retryable error (429 / 5xx / 524 / transport EOF).** Today there is none — the
proxy classifies the upstream error and returns it to the client immediately
(`ClassifyUpstreamError` → 502/429). This is the root cause of clients
(workbuddy / opencode / codex) seeing 429/502/timeout when a single flaky
third-party relay (e.g. `approach`/tokenservices) hiccups.

The two mechanisms compose: key-selection fallback decides the **ordered
candidate list**; in-request failover **walks down that same list** on retryable
errors.

---

## 1. Goals / non-goals

**Goals**
- On a retryable upstream error, transparently re-dispatch the request to the
  next candidate (another key on the same provider, then the next provider by
  admin priority) within the same client request.
- Respect idempotency: never retry after any response bytes have been sent to
  the client (streaming that has already emitted a chunk is non-retryable).
- Honor the upstream's `Retry-After` on 429 (already implemented at the
  key-health layer — `ReportKeyRateLimited`).
- Correct cost attribution and statistics when failover changes provider.

**Non-goals**
- No change to **client-facing billing** (see §4 — billing is by logical model).
- No background health-probing / active recovery loop (recovery is lazy — §5).
- No cross-request stickiness to a failover target (per decision — §6).

---

## 2. Retry/failover decision

### 2.1 Candidate ordering (reuse existing logic)

The candidate list is exactly what `resolveProviderAndKey` + `smartRoute`
already produce, in this precedence:

1. **Tier progression** (user priority → key Priority bucket; within a tier all
   providers are tried before crossing tiers).
2. **Circuit-broken providers excluded.**
3. **Admin manual routing** `model_provider_routing.priority` ascending — e.g.
   glm-5.3 / kimi-k3 are `approach=0, tokenrouter=1`, so failover is
   **approach first, then TR** once TR is re-enabled.
4. If no manual routing: `smartRoute` (health → affinity → tier → latency →
   quota → LRU).
5. Affinity (`preferredKeyID`) short-circuit.

Failover **walks this ordered list**, so it automatically obeys the operator's
configured provider priority with no new ordering concept.

### 2.2 Which errors are retryable

| Upstream signal | Retry? | Rationale |
|---|---|---|
| 429 Too Many Requests | ✅ next key/provider | rate limited; honor Retry-After, cool this (key,model) |
| 500 / 502 / 503 / 504 | ✅ | transient upstream error |
| 524 (CDN origin timeout) | ✅ (pre-first-byte only) | the slow-upstream case; next provider may be faster |
| transport EOF / RemoteDisconnected / connection reset | ✅ | the `approach` flake class observed 2026-10-24 |
| 400 / 404 / 422 (bad request / model not found) | ❌ | deterministic; retrying wastes quota |
| 401 / 403 (auth) | ❌ retry provider, but key is marked | key/model-level auth issue, not transient |
| context canceled / deadline (client gone) | ❌ | client left; already excluded from key health |

### 2.3 Idempotency gate (critical)

- **Non-streaming:** retry freely until a response is written. The upstream call
  is buffered server-side, so a failed attempt has emitted nothing to the client.
- **Streaming:** retry **only if no SSE byte has been flushed to the client
  yet** (i.e. the upstream returned its error status *before* the stream
  started, or the transport died before first byte). Once any delta is flushed,
  the request is **non-retryable** — surface the error as today. A boolean
  `wroteFirstByte` guards this.

### 2.4 Exclusion of the failed candidate

Each attempt adds the just-failed `(provider, keyID)` to a per-request exclusion
set so the retry loop never reselects the same failed key. The affinity
short-circuit (§2.1.5) is **skipped for retries** so a sticky-but-failing key is
not chosen again.

### 2.5 Bounds

- `max_attempts` (config, default **3**: the initial attempt + 2 retries).
- Global per-request deadline respected (don't retry past the client/CDN
  timeout window; a 524-class slow upstream that already burned ~100s must not
  trigger another 100s attempt).
- Backoff: honor `Retry-After` for 429; a small fixed jitter (e.g. 200ms) for
  5xx/EOF; none if switching to a different provider (no need to wait).

---

## 3. Config knobs (new)

```yaml
routing:
  failover:
    enabled: true            # master switch
    max_attempts: 3          # total attempts incl. the first
    retry_statuses: [429, 500, 502, 503, 504, 524]
    retry_on_transport_error: true   # EOF / reset / RemoteDisconnected
    per_attempt_jitter_ms: 200
```

Defaults are conservative and the feature is behind `enabled` so it can be
rolled back without a redeploy if it misbehaves.

---

## 4. Billing & statistics impact

### 4.1 Client-facing billing — **UNCHANGED**

Client charges are computed on the **logical requested model**
(`model_pricing_rules` × group `pricing_ratio`), not on the upstream provider.
A request for `glm-5.3` is billed as `glm-5.3` no matter which provider/tier
actually served it. **No billing change is needed or wanted** — price stays
predictable for the customer, and failover is an internal reliability concern.

A failed attempt that was **not** billed (no success) is already handled by the
"no success, no charge" path (verified across the approach validation). Only the
**final successful** attempt is billed; intermediate failed attempts are not.

### 4.2 Cost / reconciliation — **must attribute to the serving provider**

Our **cost** (what we pay upstream) is keyed `provider:model`
(`billing.GetPricing`) and recorded per upstream key in `upstream_key_usage`.
When failover changes the provider, the usage/cost must attribute to the
**provider/key that actually served the successful response** — which the
existing `recordUpstreamUsageAsync(upstreamKey.ID, provider, …)` already does
because it is called with the *selected* provider/key at completion time.

Required to make this correct & observable:

1. **Mark failover on the usage record.** `usage_records` already has
   `is_fallback` and `original_track`. On an in-request failover, set
   `is_fallback = true` and record the originally-intended provider (reuse
   `original_track` semantics or add a `fallback_from` note). This lets
   reconciliation distinguish "served by primary" vs "served by failover".
2. **Pre-consume vs settle provider must match the serving provider.**
   Pre-consume deducts an estimate before the call; if failover switches
   provider, the **settle** (final) must use the provider/key that actually
   served, so cost reconciliation lands on the right upstream. The
   `SettleBuffer` already settles with the final provider — confirm the
   pre-consume/settle provider pairing during implementation so cost isn't
   recorded against the wrong upstream.
3. **Dashboards/reconciliation**: surface "requested model" vs "serving
   provider" (the k3 = qianfan-vl backbone question is the same theme) so margin
   analysis is accurate when traffic silently failed over to a cheaper/pricier
   upstream.

**Net:** no schema change strictly required (fields exist); the work is
ensuring failover writes `is_fallback=true` + correct serving provider, and that
pre-consume/settle agree on the serving provider.

---

## 5. Recovery / fail-back timing

Recovery is **lazy and automatic — no background prober.** A key/model that was
cooled during failover re-enters the candidate set the moment its cooldown
expires, evaluated on the **next real request**:

| Cooldown cause | Duration | Fail-back trigger |
|---|---|---|
| 429 rate limit | upstream `Retry-After` (default 60s, capped 10m) | next request after expiry |
| 3-strike failure | 60s | next request after expiry |
| daily quota | until next UTC midnight | next request after expiry |

Because candidate ordering is by admin `priority`, once the higher-priority
key/provider's cooldown expires it is **preferred again automatically** — the
request that follows expiry picks it. So fail-back from a demoted tier/provider
back to premium/primary happens **within one cooldown cycle** (e.g. ≤60s for the
common cases), at the granularity of "the next request after the cooldown
clears." No explicit re-promotion step is needed.

The `Failures = 0 on success` fix (2026-10-25) ensures that once the primary is
chosen again and succeeds, its failure counter resets, so it won't be
spuriously re-demoted by stale accumulated failures.

---

## 6. Failover scope decision (operator-approved)

**Failover applies ONLY to the request that triggered it.** It does **not**
pin subsequent requests to the failover target. The next request re-evaluates
the full candidate list (priority + cooldown + affinity) from scratch, so:

- As soon as the primary/premium cooldown expires, the next request returns to
  it automatically (fail-back within one cooldown cycle — §5).
- No new stickiness state is introduced; affinity continues to work as today for
  *successful* paths, but a retry within a request skips the affinity
  short-circuit for the failed key (§2.4).

This keeps the model simple and avoids "stuck on the degraded upstream" drift.

---

## 7. Implementation sketch

- Wrap the current single-shot dispatch in `handleChatCompletions` /
  `handleCompletions` / `handleClaudeMessages` (and the passthrough variants) in
  an attempt loop driven by a shared `dispatchWithFailover` helper that:
  1. resolves a candidate (excluding the per-request failed set),
  2. sends upstream,
  3. on retryable error + idempotency-OK + attempts remaining → record
     key-health (ReportKeyFailure / ReportKeyRateLimited), add to exclusion set,
     loop;
  4. else → settle + return (success or final error).
- Streaming: move the retry decision to *before first flushed byte*; keep the
  existing streaming path unchanged after first byte.
- Reuse `resolveProviderAndKey` with an added `excluded map[...]bool` param.
- Add `is_fallback=true` + serving-provider write on the usage record when
  attempt index > 0.
- Config under `routing.failover` (§3); feature-flagged.

Estimated: ~150–200 lines in `internal/server` + the `excluded` param on
`resolveProviderAndKey`; regression tests for: 429→next provider, 5xx→next key,
EOF→next provider, non-retryable 400 passthrough, streaming-after-first-byte
non-retry, max_attempts bound, and `is_fallback` marking.

---

## 8. Open questions for review

1. `max_attempts` default **3** OK, or 2?
2. For 524-class (slow upstream already burned ~100s): skip retry entirely to
   avoid doubling latency, or allow exactly one switch to a *different* provider?
   (proposed: allow one cross-provider switch only, never a same-provider retry
   after a 100s burn.)
3. Do we want a per-model override of `failover.enabled` (e.g. only for
   glm-5.3/kimi-k3 on the flaky `approach`), or global only?


---

## 9. Implementation status (2026-10-25, commit 4858ad7)

**Decisions applied (operator-approved):** max_attempts=3; 524 slow-upstream allows
up to **2 cross-provider switches**; `failover.enabled` with per-model control via
`failover.disabled_models`; failover applies to the triggering request only.

**Shipped:**
- `config.FailoverConfig` + viper defaults (enabled=true, max_attempts=3,
  retry_statuses=[429,500,502,503,504,524], retry_on_transport_error=true,
  slow_upstream_status=524, slow_upstream_max_switch=2, disabled_models=[]).
- `resolveProviderAndKeyExcluding(excluded)` — per-request exclusion of failed
  (provider:keyID); affinity short-circuit skipped on retry. `resolveProviderAndKey`
  delegates (no call-site churn).
- `internal/server/failover.go` `failoverState.shouldRetry` — per-model disable,
  max_attempts, retryable status set, 524 cross-provider cap, transport gate,
  first-byte idempotency gate. 8 unit tests.
- Wired into **streaming** (`handleStreamingResponse`, pre-first-byte) and
  **non-streaming** (`handleChatCompletions`, pre-response) chat paths. Marks
  `is_fallback=true`; cost attributes to the serving provider.

**Verified:** build/vet/gofmt clean; `make test` ALL PASS; transform E2E 21/0;
CI green; deployed (health 200). Healthy requests (glm-5.3/kimi-k3 × stream/non-stream)
all 200 with **no spurious "Failover retry"** (the `is_fallback=true` on healthy
requests is the pre-existing zhipu/moonshot→approach label, not a failover event).
Dynamic cross-provider switch could not be live-tested while TR is paused (approach
is the sole provider); decision logic is covered by the unit tests.

**Follow-up (not yet wrapped):** Anthropic `/v1/messages` passthrough
(`handleAnthropicPassthrough`), Anthropic→OpenAI (`handleAnthropicToOpenAI`), and
`/v1/responses`. Same pattern applies.


---

## 10. Extension (2026-10-25, commit 0d40f30)

- **primaryProvider source fixed**: now `GetRoutingOrder(model)[0]` when routing is
  configured, else the model-name heuristic. Stops mislabeling healthy requests as
  is_fallback=true (glm-5.3 was guessed "zhipu" with no zhipu key). Verified in prod:
  healthy glm-5.3/kimi-k3 now report **is_fallback=false**; real failover still marks true.
- **Failover extended** to `handleAnthropicToOpenAI` (non-stream) and `/v1/responses`
  non-stream — same pre-response adaptor-rebuild pattern.
- **Live failover verified** (TR active): breaking approach base_url → transport error →
  glm-5.3 streaming auto-failed-over to tokenrouter (200, is_fallback=true,
  "Failover retry (streaming) to_provider=tokenrouter" in logs); restored, approach healthy.
- **Still follow-up**: raw-HTTP streaming passthroughs — Anthropic `/v1/messages`
  native passthrough (`handleAnthropicPassthrough`, sharedStreamingClient.Do) and
  `/v1/responses` raw streaming (sharedStreamingClient.Do). These build a raw
  *http.Request*; failover there must rebuild it with the next provider's
  base_url/auth AND ensure the target is Anthropic-native (format-compatible),
  else the passthrough body is invalid for a convert-only upstream. Deferred to
  avoid a format-mismatch bug.

---

## 11. Failover coverage status (as of 2026-10-25, commit 0d40f30)

Dispatch sub-paths and whether in-request failover is wired, with ~7-day traffic
for scope. "convert" = adaptor-based (OpenAI↔Anthropic↔…); "passthrough" = raw
`*http.Request` forwarded unchanged (no adaptor, preserves cache_control/reasoning).

| Path | Sub-mode | Failover | ~7d traffic | Class |
|---|---|---|---|---|
| `/v1/chat/completions` | non-stream | ✅ yes | main | — |
| `/v1/chat/completions` | stream | ✅ yes | main | — |
| `/v1/messages` → convert (non-native upstream) | non-stream | ✅ yes | low | — |
| `/v1/messages` → convert (non-native upstream) | stream (`handleAnthropicStreamingResponse`) | ❌ no | low | **A** |
| `/v1/messages` → passthrough (native upstream) | non-stream (`handleAnthropicPassthrough`) | ❌ no | **620** | **B** |
| `/v1/messages` → passthrough (native upstream) | stream (`handleAnthropicPassthrough`) | ❌ no | **268** | **B** |
| `/v1/responses` → convert | non-stream | ✅ yes | 3 | — |
| `/v1/responses` → convert | stream (`handleResponsesStreaming`) | ❌ no | ~69 | **A** |
| `/v1/responses` → passthrough (responses-only model) | non-stream (`handleResponsesPassthrough`) | ❌ no | part | **B** |
| `/v1/responses` → passthrough (responses-only model) | stream (`handleResponsesPassthroughStreaming`) | ❌ no | part | **B** |

Routing triggers:
- `/v1/messages`: passthrough iff `isAnthropicNativeProvider(provider)`
  (`anthropic`, `openrouter`, `tokenrouter`); else convert.
- `/v1/responses`: passthrough iff the model's synced capability is
  `responses=true && chat_completions=false` (Responses-only); else convert.

Gap classes:
- **Class A — adaptor-based convert streaming** (`handleAnthropicStreamingResponse`,
  `handleResponsesStreaming`): same safe pattern as the shipped chat streaming
  path (re-resolve excluding failed key, rebuild adaptor+body, re-send pre-first
  byte). Low risk. **Do first.**
- **Class B — raw-HTTP passthrough** (`handleAnthropicPassthrough` both modes =
  the largest `/v1/messages` traffic; `handleResponsesPassthrough(Streaming)`):
  failover must rebuild the raw `*http.Request` with the next provider's
  base_url + auth scheme AND guarantee the next candidate is format-compatible
  (Anthropic-native for `/v1/messages`; Responses-native for `/v1/responses`),
  otherwise the unchanged body is invalid for a convert-only upstream. Requires a
  native-only target filter. Evaluate risk (cross-provider passthrough
  inconsistency; AWS Bedrock is the most trustworthy claude upstream, the rest
  are third-party relays) before implementing.

---

## 12. Class B risk assessment (2026-10-25)

Concern: different upstreams may handle `/v1/messages` passthrough
inconsistently (AWS Bedrock is the most trustworthy claude upstream; the rest
are third-party relays).

**Finding — AWS is NOT in the passthrough path at all.** `/v1/messages`
passthrough only targets `isAnthropicNativeProvider` = {anthropic, openrouter,
tokenrouter}. AWS claude runs on a **separate Bedrock track**
(`resolveBedrockProviderAndKey` + AWS adaptor + SigV4; providers
`amazon-us-east-1`, `amazon-mantle-*`) and never takes the raw `/v1/messages`
passthrough. `laozhang` is also not native, so laozhang claude via `/v1/messages`
goes through convert (`handleAnthropicToOpenAI`, which already has failover).

**Implication:** the passthrough failover candidate pool is exactly
{anthropic, OR, TR} — all verified to accept standard Anthropic `/v1/messages`
with cache_control, all Bearer-authed (anthropic native uses x-api-key; already
branched). The AWS format-inconsistency worry does not apply here.

**Residual risk (low):**
1. raw `*http.Request` rebuild with the next provider's base_url + auth — direct,
   reuses the existing auth branch.
2. must filter failover targets to native-only (skip non-native candidates) so
   an unchanged Anthropic body is never sent to a convert-only upstream.
3. streaming passthrough idempotency: retry pre-first-byte only (same as class A).

**Conclusion:** Class B is safe to implement with a native-only target filter.
High value: `/v1/messages` passthrough is ~888 req/7d (juxin opus/claude main
traffic) with no failover today. Recommended, pending operator go-ahead.

---

## 13. Class B implemented (2026-10-25, commits 6bc6060 + f81ee5d)

Agreed plan confirmed: AWS claude runs on a separate Bedrock track and is never a
passthrough failover target; failover among native providers {anthropic, OR, TR}
only (OR↔TR interchangeable per model_provider_routing priority 0↔1).

- **`/v1/messages` passthrough** (`handleAnthropicPassthrough`, stream + non-stream):
  failover restricted to Anthropic-native providers via
  `resolveProviderAndKeyFiltered(nativeOK)`. Rebuilds the raw `*http.Request`
  (`buildAnthropicPassthroughReq`: base+`/messages`, x-api-key for native Anthropic
  else Bearer) and re-points the body model to the new provider's name
  (`rewriteAnthropicBodyModel`, from a `baseBody` captured before the first rewrite).
- **`/v1/responses` passthrough** (`handleResponsesPassthrough` + streaming):
  `responsesPassthroughFailover` only switches to responses-native candidates
  (post-resolve `shouldUsePassthrough` check), else skips that candidate.

**Coverage is now complete** — every dispatch path has in-request failover:
chat (stream/non-stream), anthropic→openai convert (stream/non-stream), responses
convert (stream/non-stream), /v1/messages passthrough (stream/non-stream),
/v1/responses passthrough (stream/non-stream).

**Verification:** build/vet/gofmt + `make test` + transform E2E + golangci-lint (CI)
all pass; deployed (health 200); glm-5.3/kimi-k3 regression 200. The dynamic
passthrough cross-provider switch was **not** live-exercised because the only
available test key lacks claude access and prod ACLs were left unmodified; the
logic is covered by unit tests and is the identical failoverState/resolver
pattern already live-verified on the chat and responses-convert paths (breaking
approach → automatic switch to tokenrouter, is_fallback=true).
