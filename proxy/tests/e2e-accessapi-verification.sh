#!/usr/bin/env bash
# AI Proxy Go - AccessAPI End-to-End Verification Script
# Tests client → proxy → accessapi upstream full chain
# Usage: bash tests/e2e-accessapi-verification.sh [PROXY_URL] [USER_KEY]
#
# Prerequisites:
#   - Proxy running (default: http://localhost:8080)
#   - Valid user API key configured in Portal with access to accessapi models

set -e

PROXY_URL="${1:-http://localhost:8080}"
USER_KEY="${2:-}"
UPSTREAM_KEY="${UPSTREAM_KEY:-}"

if [[ -z "$UPSTREAM_KEY" ]]; then
  echo "Error: UPSTREAM_KEY is required. Set it as an environment variable."
  echo "Example: UPSTREAM_KEY=sk-xxx bash tests/e2e-accessapi-verification.sh"
  exit 1
fi

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

PASS=0
FAIL=0
LATENCY_DIRECT=()
LATENCY_PROXY=()

pass() { PASS=$((PASS + 1)); echo -e "${GREEN}✓ $1${NC}"; }
fail() { FAIL=$((FAIL + 1)); echo -e "${RED}✗ $1${NC}"; }
info() { echo -e "${BLUE}ℹ $1${NC}"; }
warn() { echo -e "${YELLOW}⚠ $1${NC}"; }

# Check dependencies
if ! command -v curl &> /dev/null; then
    echo "curl is required but not installed."
    exit 1
fi

if ! command -v python3 &> /dev/null; then
    echo "python3 is required but not installed."
    exit 1
fi

echo "=========================================="
echo "  AccessAPI E2E Verification"
echo "=========================================="
echo "  Proxy URL: $PROXY_URL"
echo "  User Key:  ${USER_KEY:0:20}..."
echo "=========================================="
echo ""

if [ -z "$USER_KEY" ]; then
    echo -e "${RED}Error: USER_KEY is required.${NC}"
    echo "Usage: bash tests/e2e-accessapi-verification.sh [PROXY_URL] [USER_KEY]"
    echo "Example: bash tests/e2e-accessapi-verification.sh http://localhost:8080 sk-wgit-xxx"
    exit 1
fi

# ============================================
# Helper: measure latency
# ============================================
measure_latency() {
    local url="$1"
    local headers="$2"
    local data="$3"
    curl -s -o /dev/null -w "%{time_total}" \
        -H "Content-Type: application/json" \
        -H "$headers" \
        -d "$data" \
        "$url" 2>/dev/null
}

# ============================================
# Section 1: Direct AccessAPI Health Check
# ============================================
echo "--- Section 1: Direct AccessAPI Verification ---"

info "Fetching model list from accessapi.ai..."
MODELS_RESPONSE=$(curl -s -H "Authorization: Bearer $UPSTREAM_KEY" \
    "https://accessapi.ai/v1/models" 2>/dev/null || echo "{}")
MODEL_COUNT=$(echo "$MODELS_RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d.get('data',[])))" 2>/dev/null || echo "0")
if [ "$MODEL_COUNT" -gt 0 ]; then
    pass "AccessAPI model list retrieved ($MODEL_COUNT models)"
else
    fail "Failed to retrieve AccessAPI model list"
fi

info "Testing direct accessapi → gpt-4o-mini..."
DIRECT_RESPONSE=$(curl -s -H "Authorization: Bearer $UPSTREAM_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hi in one word"}],"max_tokens":10}' \
    "https://accessapi.ai/v1/chat/completions" 2>/dev/null || echo "{}")
if echo "$DIRECT_RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); exit(0 if 'choices' in d else 1)" 2>/dev/null; then
    pass "Direct accessapi → gpt-4o-mini OK"
else
    fail "Direct accessapi → gpt-4o-mini failed"
fi

info "Testing direct accessapi → claude-opus-4-6..."
DIRECT_CLAUDE=$(curl -s -H "Authorization: Bearer $UPSTREAM_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"claude-opus-4-6","messages":[{"role":"user","content":"Say hi in one word"}],"max_tokens":10}' \
    "https://accessapi.ai/v1/chat/completions" 2>/dev/null || echo "{}")
if echo "$DIRECT_CLAUDE" | python3 -c "import json,sys; d=json.load(sys.stdin); exit(0 if 'choices' in d else 1)" 2>/dev/null; then
    pass "Direct accessapi → claude-opus-4-6 OK"
else
    warn "Direct accessapi → claude-opus-4-6 unavailable (channel saturated)"
fi

# ============================================
# Section 2: Proxy Health
# ============================================
echo ""
echo "--- Section 2: Proxy Health ---"

HEALTH=$(curl -s -o /dev/null -w "%{http_code}" "$PROXY_URL/health" 2>/dev/null)
if [ "$HEALTH" = "200" ]; then
    pass "Proxy health check OK"
else
    fail "Proxy health check failed (HTTP $HEALTH)"
    exit 1
fi

# ============================================
# Section 3: OpenAI Chat Completions via Proxy
# ============================================
echo ""
echo "--- Section 3: OpenAI Chat Completions via Proxy ---"

info "Test 3.1: Non-streaming gpt-4o-mini..."
RESPONSE=$(curl -s -w "\n---HTTP_CODE:%{http_code}\n---TIME:%{time_total}" \
    -H "Authorization: Bearer $USER_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hi in one word"}],"max_tokens":10}' \
    "$PROXY_URL/v1/chat/completions" 2>/dev/null)
HTTP_CODE=$(echo "$RESPONSE" | grep -- "---HTTP_CODE:" | cut -d: -f2)
TIME=$(echo "$RESPONSE" | grep -- "---TIME:" | cut -d: -f2)
LATENCY_PROXY+=("$TIME")
if [ "$HTTP_CODE" = "200" ]; then
    CONTENT=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['choices'][0]['message']['content'])" 2>/dev/null || echo "")
    pass "OpenAI Chat → gpt-4o-mini (${TIME}s): $CONTENT"
else
    fail "OpenAI Chat → gpt-4o-mini (HTTP $HTTP_CODE)"
fi

info "Test 3.2: Streaming gpt-4o-mini..."
STREAM_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $USER_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hi"}],"max_tokens":10,"stream":true}' \
    "$PROXY_URL/v1/chat/completions" 2>/dev/null)
if [ "$STREAM_RESPONSE" = "200" ]; then
    pass "OpenAI Streaming → gpt-4o-mini OK"
else
    fail "OpenAI Streaming → gpt-4o-mini (HTTP $STREAM_RESPONSE)"
fi

# ============================================
# Section 4: Anthropic Messages via Proxy
# ============================================
echo ""
echo "--- Section 4: Anthropic Messages via Proxy ---"

info "Test 4.1: Non-streaming Anthropic → gpt-4o-mini..."
RESPONSE=$(curl -s -w "\n---HTTP_CODE:%{http_code}\n---TIME:%{time_total}" \
    -H "Authorization: Bearer $USER_KEY" \
    -H "Content-Type: application/json" \
    -H "anthropic-version: 2023-06-01" \
    -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hi in one word"}],"max_tokens":10}' \
    "$PROXY_URL/v1/messages" 2>/dev/null)
HTTP_CODE=$(echo "$RESPONSE" | grep -- "---HTTP_CODE:" | cut -d: -f2)
TIME=$(echo "$RESPONSE" | grep -- "---TIME:" | cut -d: -f2)
if [ "$HTTP_CODE" = "200" ]; then
    CONTENT=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['content'][0]['text'])" 2>/dev/null || echo "")
    pass "Anthropic Messages → gpt-4o-mini (${TIME}s): $CONTENT"
else
    fail "Anthropic Messages → gpt-4o-mini (HTTP $HTTP_CODE)"
fi

info "Test 4.2: Streaming Anthropic → gpt-4o-mini..."
STREAM_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $USER_KEY" \
    -H "Content-Type: application/json" \
    -H "anthropic-version: 2023-06-01" \
    -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hi"}],"max_tokens":10,"stream":true}' \
    "$PROXY_URL/v1/messages" 2>/dev/null)
if [ "$STREAM_RESPONSE" = "200" ]; then
    pass "Anthropic Streaming → gpt-4o-mini OK"
else
    fail "Anthropic Streaming → gpt-4o-mini (HTTP $STREAM_RESPONSE)"
fi

info "Test 4.3: Anthropic with tool call → gpt-4o-mini..."
TOOL_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $USER_KEY" \
    -H "Content-Type: application/json" \
    -H "anthropic-version: 2023-06-01" \
    -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"What is the weather in Tokyo?"}],"max_tokens":50,"tools":[{"name":"get_weather","description":"Get weather","input_schema":{"type":"object","properties":{"location":{"type":"string"}}}}],"tool_choice":"auto"}' \
    "$PROXY_URL/v1/messages" 2>/dev/null)
if [ "$TOOL_RESPONSE" = "200" ]; then
    pass "Anthropic Tool Call → gpt-4o-mini OK"
else
    fail "Anthropic Tool Call → gpt-4o-mini (HTTP $TOOL_RESPONSE)"
fi

# ============================================
# Section 5: Responses API via Proxy
# ============================================
echo ""
echo "--- Section 5: Responses API via Proxy ---"

info "Test 5.1: Non-streaming Responses → gpt-4o-mini..."
RESPONSE=$(curl -s -w "\n---HTTP_CODE:%{http_code}\n---TIME:%{time_total}" \
    -H "Authorization: Bearer $USER_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-4o-mini","input":"Say hi in one word","max_output_tokens":10}' \
    "$PROXY_URL/v1/responses" 2>/dev/null)
HTTP_CODE=$(echo "$RESPONSE" | grep -- "---HTTP_CODE:" | cut -d: -f2)
TIME=$(echo "$RESPONSE" | grep -- "---TIME:" | cut -d: -f2)
if [ "$HTTP_CODE" = "200" ]; then
    CONTENT=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['output'][0]['content'][0]['text'])" 2>/dev/null || echo "")
    pass "Responses API → gpt-4o-mini (${TIME}s): $CONTENT"
else
    fail "Responses API → gpt-4o-mini (HTTP $HTTP_CODE)"
fi

info "Test 5.2: Streaming Responses → gpt-4o-mini..."
STREAM_RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $USER_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-4o-mini","input":"Say hi","max_output_tokens":10,"stream":true}' \
    "$PROXY_URL/v1/responses" 2>/dev/null)
if [ "$STREAM_RESPONSE" = "200" ]; then
    pass "Responses Streaming → gpt-4o-mini OK"
else
    fail "Responses Streaming → gpt-4o-mini (HTTP $STREAM_RESPONSE)"
fi

# ============================================
# Section 6: Claude Model via Proxy
# ============================================
echo ""
echo "--- Section 6: Claude Model via Proxy ---"

info "Test 6.1: OpenAI Chat → claude-opus-4-6..."
RESPONSE=$(curl -s -w "\n---HTTP_CODE:%{http_code}\n---TIME:%{time_total}" \
    -H "Authorization: Bearer $USER_KEY" \
    -H "Content-Type: application/json" \
    -d '{"model":"claude-opus-4-6","messages":[{"role":"user","content":"Say hi in one word"}],"max_tokens":10}' \
    "$PROXY_URL/v1/chat/completions" 2>/dev/null)
HTTP_CODE=$(echo "$RESPONSE" | grep -- "---HTTP_CODE:" | cut -d: -f2)
TIME=$(echo "$RESPONSE" | grep -- "---TIME:" | cut -d: -f2)
if [ "$HTTP_CODE" = "200" ]; then
    CONTENT=$(echo "$RESPONSE" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['choices'][0]['message']['content'])" 2>/dev/null || echo "")
    pass "OpenAI Chat → claude-opus-4-6 (${TIME}s): $CONTENT"
else
    warn "OpenAI Chat → claude-opus-4-6 unavailable (HTTP $HTTP_CODE)"
fi

# ============================================
# Summary
# ============================================
echo ""
echo "=========================================="
echo "  Test Results Summary"
echo "=========================================="
echo -e "  ${GREEN}Passed: $PASS${NC}"
echo -e "  ${RED}Failed: $FAIL${NC}"
echo "=========================================="

if [ $FAIL -gt 0 ]; then
    exit 1
fi
