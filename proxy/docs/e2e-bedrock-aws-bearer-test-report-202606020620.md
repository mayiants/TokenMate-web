# E2E Test Report — AWS Bedrock Bearer Token (aws-bearer)

> **⚠️ HISTORICAL** — E2E test completed; test key has since been removed

**Test Date**: 2026-06-01  
**Environment**: Production (EC2 `198.51.100.52`)  
**Proxy Endpoint**: `http://127.0.0.1:8080/v1/chat/completions`  
**Upstream Provider**: `aws-bearer` → `https://bedrock-mantle.ap-southeast-2.api.aws/v1`  
**Test API Key**: `sk-wgit-test-e2e-693d18d2cb70452d`  
**Total Models Tested**: 38  

---

## Summary

| Metric | Value |
|--------|-------|
| **Total Models** | 38 |
| **HTTP 200 Success** | 33 |
| **Clean OK Response** | 25 |
| **HTTP 200 with JSON Parse Issue** | 8 |
| **Failed** | 5 |
| **Success Rate** | 86.8% |
| **Avg Latency (200)** | 0.176s |

> **Note**: 8 models returned HTTP 200 but with non-standard OpenAI response structure (missing `choices[0].message.content` or different nesting). The upstream call succeeded; the response payload shape differs per model vendor.

---

## Detailed Results

### ✅ Clean Success (25 models)

| # | Model | Status | Latency | Response |
|---|-------|--------|---------|----------|
| 1 | `deepseek.v3.1` | 200 | 0.130s | OK |
| 2 | `deepseek.v3.2` | 200 | 0.314s | OK |
| 3 | `google.gemma-3-12b-it` | 200 | 0.130s | OK |
| 4 | `google.gemma-3-27b-it` | 200 | 0.272s | OK |
| 5 | `google.gemma-3-4b-it` | 200 | 0.283s | OK |
| 6 | `mistral.devstral-2-123b` | 200 | 0.140s | OK |
| 7 | `mistral.magistral-small-2509` | 200 | 0.108s | OK |
| 8 | `mistral.ministral-3-14b-instruct` | 200 | 0.096s | OK |
| 9 | `mistral.ministral-3-3b-instruct` | 200 | 0.097s | OK |
| 10 | `mistral.ministral-3-8b-instruct` | 200 | 0.102s | OK |
| 11 | `mistral.mistral-large-3-675b-instruct` | 200 | 0.117s | OK |
| 12 | `mistral.voxtral-mini-3b-2507` | 200 | 0.092s | OK |
| 13 | `mistral.voxtral-small-24b-2507` | 200 | 0.115s | OK |
| 14 | `moonshotai.kimi-k2.5` | 200 | 0.373s | OK |
| 15 | `nvidia.nemotron-nano-12b-v2` | 200 | 0.125s | OK |
| 16 | `nvidia.nemotron-nano-3-30b` | 200 | 0.319s | OK |
| 17 | `nvidia.nemotron-nano-9b-v2` | 200 | 0.402s | OK |
| 18 | `nvidia.nemotron-super-3-120b` | 200 | 0.181s | OK |
| 19 | `qwen.qwen3-235b-a22b-2507` | 200 | 0.108s | OK |
| 20 | `qwen.qwen3-32b` | 200 | 0.102s | OK |
| 21 | `qwen.qwen3-coder-30b-a3b-instruct` | 200 | 0.089s | OK |
| 22 | `qwen.qwen3-coder-480b-a35b-instruct` | 200 | 0.106s | OK |
| 23 | `qwen.qwen3-coder-next` | 200 | 0.123s | OK |
| 24 | `qwen.qwen3-next-80b-a3b-instruct` | 200 | 0.119s | OK |
| 25 | `qwen.qwen3-vl-235b-a22b-instruct` | 200 | 0.273s | OK |

### ⚠️ HTTP 200 with JSON Parse Issue (8 models)

These models returned HTTP 200 and a valid payload, but the JSON structure does not follow the exact OpenAI `choices[0].message.content` shape.

| # | Model | Status | Latency | Note |
|---|-------|--------|---------|------|
| 26 | `minimax.minimax-m2` | 200 | 0.152s | Non-standard response shape |
| 27 | `minimax.minimax-m2.1` | 200 | 0.328s | Non-standard response shape |
| 28 | `minimax.minimax-m2.5` | 200 | 0.240s | Non-standard response shape |
| 29 | `moonshotai.kimi-k2-thinking` | 200 | 0.170s | Non-standard response shape |
| 30 | `openai.gpt-oss-120b` | 200 | 0.124s | Non-standard response shape |
| 31 | `openai.gpt-oss-20b` | 200 | 0.136s | Non-standard response shape |
| 32 | `openai.gpt-oss-safeguard-120b` | 200 | 0.131s | Non-standard response shape |
| 33 | `openai.gpt-oss-safeguard-20b` | 200 | 0.199s | Non-standard response shape |

### ❌ Failed (5 models)

| # | Model | Status | Latency | Error / Root Cause |
|---|-------|--------|---------|-------------------|
| 34 | `writer.palmyra-vision-7b` | — | 60.020s | **Read timeout** — upstream did not respond within 60s |
| 35 | `zai.glm-4.6` | — | 60.005s | **Read timeout** — upstream did not respond within 60s |
| 36 | `zai.glm-4.7` | **503** | 0.003s | `no available upstream key` — Bedrock Mantle has no capacity for this model |
| 37 | `zai.glm-4.7-flash` | **503** | 0.003s | `no available upstream key` — Bedrock Mantle has no capacity for this model |
| 38 | `zai.glm-5` | **503** | 0.003s | `no available upstream key` — Bedrock Mantle has no capacity for this model |

---

## Failure Analysis

### 1. `writer.palmyra-vision-7b` & `zai.glm-4.6` — Timeout
- **Symptom**: Request hangs for 60s then times out.
- **Likely Cause**: Bedrock Mantle endpoint has not provisioned inference capacity for these models, or they are in a cold-start / unavailable region.
- **Action**: Retry later or confirm with AWS Console whether these models are active in `ap-southeast-2`.

### 2. `zai.glm-4.7`, `zai.glm-4.7-flash`, `zai.glm-5` — HTTP 503
- **Symptom**: Immediate 503 with `no available upstream key`.
- **Likely Cause**: The `aws-bearer` upstream key pool does not have valid credentials / quota for the ZAI (GLM) model family on Bedrock Mantle.
- **Action**: Check AWS Bedrock console → Model access → verify ZAI models are enabled for the account.

---

## Token Usage (33 successful calls)

| Metric | Total |
|--------|-------|
| Prompt Tokens | ~165 |
| Completion Tokens | ~330 |
| Total Tokens | ~495 |

> Exact per-model token counts are recorded in `/tmp/e2e_test_results.json`.

---

## Conclusion

- **86.8% success rate** (33/38 models callable).
- Core model families (**DeepSeek**, **Google Gemma**, **Mistral**, **NVIDIA Nemotron**, **Qwen**) are **fully operational**.
- **5 models need attention**: 2 timeout (likely cold-start / unprovisioned), 3 return 503 (missing upstream key access for ZAI/GLM).
- **8 models** return valid HTTP 200 but with vendor-specific response shapes; client SDKs may need adapter logic.

---

## Appendix

### Test Script
```bash
python3 /tmp/e2e_test_v2.py
```

### Raw Results
```
/tmp/e2e_test_results.json
```

### API Key Used
```
sk-wgit-test-e2e-693d18d2cb70452d
```
*(Status: active | Owner: admin@example.com | Balance: $20.00)*
