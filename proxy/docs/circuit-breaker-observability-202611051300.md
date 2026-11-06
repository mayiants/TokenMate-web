# Circuit Breaker Observability — design & postmortem addendum

Status: ACTIVE (implemented, deployed 2026-11-05, proxy `5a10665`+`09e7e01`)
Related: `cross-provider-fallback-routing-202605161900.md`, `proxy-routing-and-prompt-cache-202607141007.md`

## Background: the 31-hour tokenrouter silence

2026-11-04 03:47 → 2026-11-05 10:35 UTC, every `tokenrouter` upstream key was
excluded from routing while TR itself was healthy and billing the whole time
(TR Management API export proves their last billed request before our restart
was 03:47:48 and zero gap on their side). Trigger: a deepseek-v4.1-flash burst
03:45–03:47 in which TR billed ~11 requests that our proxy never completed
(large completions, 7k–15k tokens) — mid-stream/transport failures with no
usage trail. Five provider-level failures opened the breaker; it never
self-healed because every half-open probe kept failing the same way. The same
signature matches the 2026-10-28 "TR 32h silence, root cause unknown".

Why this was invisible: breaker state lived only in process memory — no logs,
no metrics, no state dump. Failover absorbed every request, so clients and
billing showed nothing. A day of shadow failover went unnoticed.

## What changed

- `pkg/circuitbreaker`: `SetTransitionHook` (fired on record-driven
  Closed→Open and HalfOpen→Closed transitions), consecutive half-open
  probe-failure counter (`ProbeFailures`), `BreakerInfo` snapshot, `Reset()`.
- `internal/server`: `WireBreakerObservability(logger)` logs every trip
  (warn, with probe-failure count) and recovery (info); ≥3 consecutive probe
  failures escalates to a loud "possible transport wedge — investigate or
  reset" warning. Wired from `NewHandlerService` (global zap logger is never
  replaced in main, so it cannot be used here).
- `GET /admin/keypool/selection-state` response now includes
  `circuit_breakers: {<provider>: {state, probe_failures}}`.
- New HMAC-admin endpoint `POST /admin/circuit-breakers/:provider/reset` —
  force-closes one provider's breaker. Recovery without a proxy restart;
  if the provider is still failing the breaker re-trips immediately and the
  logs now show it. (Resetting also clears per-key/model runtime state is NOT
  covered — use the existing per-key reset-runtime endpoint for that.)
- Relay shared HTTP client pool: `MaxIdleConns` 100→200,
  `MaxIdleConnsPerHost` 20→50 (aligns with the earlier performance analysis).

Operational notes

- Breaker config unchanged: threshold 5 consecutive failures, 30s reset,
  half-open admits 1 probe. The design intentionally stays fail-fast.
- A provider sitting excluded is now discoverable in two ways: the
  selection-state dump, and the OPEN/wedge warnings in logs.
- If a wedge warning fires: check TR/upstream health from THEIR side first
  (for TR use the Management API export — see HANDOVER), then reset the
  breaker, then watch whether it re-trips.

## Tests

`pkg/circuitbreaker/breaker_test.go`: transition hook (fires on real
transitions only), probe-failure counting across trips and reset on close,
`Reset()`, Status shape. Full `go test -race ./...` green; deployed and
verified live (reset endpoint 200, dump shows `closed`).
