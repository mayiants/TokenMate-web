#!/usr/bin/env bash
# AI Proxy Go - End-to-end test for internal request/response transforms.
#
# This script stands up the real Go proxy process, a mock Portal, and a mock
# upstream provider. It then exercises the P1-P5 plugin/normalization paths
# and asserts on the bodies that the upstream mock actually receives.
#
# Run from the repository root:
#   bash tests/e2e/e2e-internal-transform.sh

set -euo pipefail

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

PASS=0
FAIL=0

pass() { echo -e "${GREEN}✓ $1${NC}"; PASS=$((PASS+1)); }
fail() { echo -e "${RED}✗ $1${NC}"; FAIL=$((FAIL+1)); }

# -----------------------------------------------------------------------------
# Dependency checks
# -----------------------------------------------------------------------------
for cmd in go python3 curl; do
    if ! command -v "$cmd" &>/dev/null; then
        echo -e "${RED}Missing required command: $cmd${NC}"
        exit 1
    fi
done

# -----------------------------------------------------------------------------
# Paths and temp workspace
# -----------------------------------------------------------------------------
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d -t ai-proxy-e2e.XXXXXX)"
CAPTURE_DIR="$TMP_DIR/captures"
RETENTION_DIR="$TMP_DIR/retention"
mkdir -p "$CAPTURE_DIR"
mkdir -p "$RETENTION_DIR"

cd "$PROJECT_ROOT"

# -----------------------------------------------------------------------------
# Port selection
# -----------------------------------------------------------------------------
free_port() {
    python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

PROXY_PORT="$(free_port)"
PORTAL_PORT="$(free_port)"
UPSTREAM_PORT="$(free_port)"

PROXY_URL="http://127.0.0.1:$PROXY_PORT"
PORTAL_URL="http://127.0.0.1:$PORTAL_PORT"
UPSTREAM_URL="http://127.0.0.1:$UPSTREAM_PORT"

# -----------------------------------------------------------------------------
# Secrets
# -----------------------------------------------------------------------------
ADMIN_SECRET="$(openssl rand -hex 32)"
SYNC_SECRET="$(openssl rand -hex 32)"
export ENCRYPTION_KEY="$(openssl rand -hex 32)"

# -----------------------------------------------------------------------------
# Cleanup
# -----------------------------------------------------------------------------
PIDS=()
cleanup() {
    echo ""
    echo "--- Cleaning up ---"
    for pid in "${PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill -TERM "$pid" 2>/dev/null || true
        fi
    done
    # Give processes a moment to shut down gracefully.
    sleep 1
    for pid in "${PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            kill -KILL "$pid" 2>/dev/null || true
        fi
    done
    if [ $FAIL -eq 0 ]; then
        rm -rf "$TMP_DIR"
    else
        echo -e "${YELLOW}Temp directory kept for debugging: $TMP_DIR${NC}"
    fi
}
trap cleanup EXIT

# -----------------------------------------------------------------------------
# Build proxy
# -----------------------------------------------------------------------------
echo "--- Building ai-proxy-go ---"
go build -o "$TMP_DIR/ai-proxy-go" ./cmd/server
pass "Built ai-proxy-go"

# -----------------------------------------------------------------------------
# Start mock upstream
# -----------------------------------------------------------------------------
echo "--- Starting mock upstream on $UPSTREAM_PORT ---"
CAPTURE_DIR="$CAPTURE_DIR" python3 tests/e2e/mock_upstream.py "$UPSTREAM_PORT" >"$TMP_DIR/mock_upstream.log" 2>&1 &
UPSTREAM_PID=$!
PIDS+=("$UPSTREAM_PID")

for i in {1..30}; do
    if curl -s "http://127.0.0.1:$UPSTREAM_PORT/_health" >/dev/null 2>&1; then
        break
    fi
    sleep 0.2
done
if ! curl -s "http://127.0.0.1:$UPSTREAM_PORT/_health" >/dev/null 2>&1; then
    fail "Mock upstream did not start"
    exit 1
fi
pass "Mock upstream ready"

# -----------------------------------------------------------------------------
# Start mock portal
# -----------------------------------------------------------------------------
echo "--- Starting mock portal on $PORTAL_PORT ---"
MOCK_UPSTREAM_URL="$UPSTREAM_URL" python3 tests/e2e/mock_portal.py "$PORTAL_PORT" >"$TMP_DIR/mock_portal.log" 2>&1 &
PORTAL_PID=$!
PIDS+=("$PORTAL_PID")

for i in {1..30}; do
    if curl -s "http://127.0.0.1:$PORTAL_PORT/api/apikeys/validate" -H "Authorization: Bearer probe" >/dev/null 2>&1; then
        break
    fi
    sleep 0.2
done
if ! curl -s "http://127.0.0.1:$PORTAL_PORT/api/apikeys/validate" -H "Authorization: Bearer probe" >/dev/null 2>&1; then
    fail "Mock portal did not start"
    exit 1
fi
pass "Mock portal ready"

# -----------------------------------------------------------------------------
# Write E2E config
# -----------------------------------------------------------------------------
cat >"$TMP_DIR/config.yaml" <<EOF
server:
  port: $PROXY_PORT
  mode: release

database:
  host: ""
  port: 5432
  name: ai_proxy
  user: postgres
  password: ""
  max_connections: 5

redis:
  host: ""
  port: 6379

portal:
  url: $PORTAL_URL
  sync_secret: $SYNC_SECRET
  sync_interval: 60s
  dev_mode: true

auth:
  jwt_secret: ""
  jwt_issuer: ai-proxy-portal

rate_limit:
  enabled: false

cache:
  affinity_ttl: 1h
  affinity:
    enabled: true
    max_entries: 1000
    default_ttl: 1h
    redis_enabled: false

audit:
  enabled: false

billing:
  pre_consume_enabled: false

admin:
  secret: $ADMIN_SECRET

simulate:
  enabled: false

routing:
  strategy: priority
  failover_enabled: false
  max_retries: 1
  timeout_ms: 30000

retention:
  enabled: true
  directory: $RETENTION_DIR
  max_age: 720h
  batch_size: 1
  flush_every: 1s
  capture_all_headers: false

plugin:
  enabled: true
  stream_options_injector: true
  claude_code_cch_normalizer: true
  claude_code_tracking_remover: true
  responses_prompt_cache_key: true
EOF
pass "Wrote E2E config"

# -----------------------------------------------------------------------------
# Start proxy
# -----------------------------------------------------------------------------
echo "--- Starting ai-proxy-go on $PROXY_PORT ---"
cd "$TMP_DIR"
./ai-proxy-go >"$TMP_DIR/proxy.log" 2>&1 &
PROXY_PID=$!
cd "$PROJECT_ROOT"
PIDS+=("$PROXY_PID")

for i in {1..60}; do
    if curl -s "$PROXY_URL/health" >/dev/null 2>&1; then
        break
    fi
    sleep 0.5
done
if ! curl -s "$PROXY_URL/health" >/dev/null 2>&1; then
    fail "ai-proxy-go did not start"
    echo "--- Proxy log tail ---"
    tail -50 "$TMP_DIR/proxy.log" || true
    exit 1
fi
pass "ai-proxy-go ready"

# -----------------------------------------------------------------------------
# Force an immediate key-pool sync so keys are available before tests run.
# -----------------------------------------------------------------------------
echo "--- Syncing keys from mock portal ---"
sync_resp="$(curl -s -X GET "$PROXY_URL/api/admin/key-pool/sync" -H "x-proxy-secret: $SYNC_SECRET")"
if ! python3 - "$sync_resp" <<'PY'
import json, sys
try:
    data = json.loads(sys.argv[1])
    ok = isinstance(data.get('keys'), list) and len(data['keys']) > 0
except Exception:
    ok = False
sys.exit(0 if ok else 1)
PY
then
    fail "Key-pool sync did not return keys: $sync_resp"
    exit 1
fi
pass "Key-pool synced"

# Small sleep to let the model registry finish its initial sync too.
sleep 1

# -----------------------------------------------------------------------------
# Test helpers
# -----------------------------------------------------------------------------
api_key="sk-e2e-api-key-12345"

latest_capture() {
    local label="$1"
    echo "$CAPTURE_DIR/${label}_latest.json"
}

json_extract() {
    python3 - "$1" "$2" <<'PY'
import json, sys
file, path = sys.argv[1], sys.argv[2]
data = json.load(open(file))
for key in path.split('.'):
    if isinstance(data, dict):
        data = data.get(key, '')
    else:
        data = ''
print('' if data is None else str(data))
PY
}

assert_json() {
    local desc="$1"
    local file="$2"
    local query="$3"
    local expected="$4"
    local actual
    actual="$(json_extract "$file" "$query")"
    if [ "$actual" == "$expected" ]; then
        pass "$desc"
    else
        fail "$desc (expected '$expected', got '$actual')"
    fi
}

assert_contains() {
    local desc="$1"
    local file="$2"
    local needle="$3"
    if grep -q "$needle" "$file"; then
        pass "$desc"
    else
        fail "$desc (missing '$needle')"
    fi
}

assert_not_contains() {
    local desc="$1"
    local file="$2"
    local needle="$3"
    if grep -q "$needle" "$file"; then
        fail "$desc (unexpected '$needle')"
    else
        pass "$desc"
    fi
}

# -----------------------------------------------------------------------------
# P1: stream_options injection on OpenAI streaming chat completions
# -----------------------------------------------------------------------------
echo ""
echo "--- Test P1: stream_options injection ---"
resp_code="$(curl -s -o /dev/null -w '%{http_code}' \
    -X POST "$PROXY_URL/v1/chat/completions" \
    -H "Authorization: Bearer $api_key" \
    -H "Content-Type: application/json" \
    -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":true}')"
if [ "$resp_code" == "200" ]; then pass "Proxy returned 200"; else fail "Proxy returned $resp_code"; fi

latest_chat="$(latest_capture openai_chat)"
assert_json "stream_options.include_usage is true" "$latest_chat" 'stream_options.include_usage' 'True'
assert_json "Model is still gpt-4o" "$latest_chat" 'model' 'gpt-4o'

# -----------------------------------------------------------------------------
# P2/P3: Claude Code billing/tracking normalization on Claude upstream
# -----------------------------------------------------------------------------
echo ""
echo "--- Test P2/P3: Claude Code cch + date normalization ---"
claude_system='x-anthropic-billing-header: cch=random-cache-id-42;\n# currentDate\nToday'"'"'s date is 2026/06/28.\nYou are helpful.'

resp_code="$(curl -s -o /dev/null -w '%{http_code}' \
    -X POST "$PROXY_URL/v1/chat/completions" \
    -H "Authorization: Bearer $api_key" \
    -H "Content-Type: application/json" \
    -d "{\"model\":\"claude-3-sonnet\",\"messages\":[{\"role\":\"system\",\"content\":\"$claude_system\"},{\"role\":\"user\",\"content\":\"Hi\"}],\"max_tokens\":100}")"
if [ "$resp_code" == "200" ]; then pass "Proxy returned 200"; else fail "Proxy returned $resp_code"; fi

latest_claude="$(latest_capture anthropic_messages)"
assert_contains "cch normalized to A1234" "$latest_claude" 'cch=A1234'
assert_not_contains "original random cch removed" "$latest_claude" 'random-cache-id-42'
assert_contains "date normalized to ISO-like" "$latest_claude" 'Today'"'"'s date is 2026-06-28.'
assert_not_contains "slash date removed" "$latest_claude" '2026/06/28'

# -----------------------------------------------------------------------------
# P5: prompt_cache_key injection on Responses API passthrough
# -----------------------------------------------------------------------------
echo ""
echo "--- Test P5: Responses API prompt_cache_key injection ---"
resp_code="$(curl -s -o /dev/null -w '%{http_code}' \
    -X POST "$PROXY_URL/v1/responses" \
    -H "Authorization: Bearer $api_key" \
    -H "Content-Type: application/json" \
    -d '{"model":"o1","input":"Hi"}')"
if [ "$resp_code" == "200" ]; then pass "Proxy returned 200"; else fail "Proxy returned $resp_code"; fi

latest_responses="$(latest_capture responses)"
assert_contains "prompt_cache_key injected" "$latest_responses" '"prompt_cache_key"'
assert_contains "prompt_cache_key uses api key id + user" "$latest_responses" 'e2e-key:e2e-user'

# -----------------------------------------------------------------------------
# Retention: captured transformed request bodies
# -----------------------------------------------------------------------------
echo ""
echo "--- Test retention capture ---"
sleep 2
retention_count="$(find "$RETENTION_DIR" -type f -name '*.json.gz' 2>/dev/null | wc -l)"
if [ "$retention_count" -gt 0 ]; then pass "Retention files written ($retention_count)"; else fail "No retention files found"; fi

retention_request_type_count() {
    local request_type="$1"
    python3 - "$request_type" "$RETENTION_DIR" <<'PY'
import glob, json, gzip, sys
request_type, directory = sys.argv[1], sys.argv[2]
count = 0
for path in glob.glob(f"{directory}/**/*.json.gz", recursive=True):
    try:
        with gzip.open(path, "rt", encoding="utf-8") as f:
            rec = json.load(f)
    except Exception:
        continue
    if rec.get("metadata", {}).get("request_type") == request_type:
        count += 1
print(count)
PY
}

# Verify that each major request type produced a retention record. The
# transformed upstream bodies are asserted via the mock upstream captures;
# retention proves the capture pipeline is wired end-to-end.
for rt in chat_completions_stream chat_completions responses_api_passthrough; do
    cnt="$(retention_request_type_count "$rt")"
    if [ "$cnt" -gt 0 ]; then
        pass "Retention captured $rt"
    else
        fail "Retention missing $rt"
    fi
done

# -----------------------------------------------------------------------------
# Summary
# -----------------------------------------------------------------------------
echo ""
echo "=========================================="
echo "  E2E Internal Transform Results"
echo "=========================================="
echo -e "  ${GREEN}Passed: $PASS${NC}"
echo -e "  ${RED}Failed: $FAIL${NC}"
echo "=========================================="

if [ $FAIL -gt 0 ]; then
    echo ""
    echo "--- Proxy log tail ---"
    tail -80 "$TMP_DIR/proxy.log" || true
    exit 1
fi
