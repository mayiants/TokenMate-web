#!/bin/bash
# ============================================
# AI Platform Go — Load Test Runner
# ============================================
# Quick wrapper for the Go-based E2E load test tool.
#
# Usage:
#   bash tests/test-loadtest.sh burst      # 500 users burst
#   bash tests/test-loadtest.sh sustained  # 500 concurrent for 30s
#   bash tests/test-loadtest.sh rampup     # ramp from 1 to 500 over 60s
#   bash tests/test-loadtest.sh all        # run all three
#
# Environment:
#   API_KEY        Required — API key(s), comma-separated for multi-key
#   PROXY_URL      Proxy URL (default: http://127.0.0.1:8080)
#   MODEL          Model to test (default: gpt-5.5)
#   USERS          Burst users (default: 500)
#   CONCURRENCY    Sustained concurrency (default: 500)
#   DURATION       Sustained/rampup duration (default: 30s)
#   MAX_TOKENS     Max tokens per response (default: 10)
#   STREAM         "true" for streaming (default: false)
# ============================================

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_DIR"

# Load env
if [[ -f ".env.go-platform" ]]; then
    set -a; source .env.go-platform; set +a
fi

PROXY_URL="${PROXY_URL:-http://127.0.0.1:${PROXY_PORT:-8080}}"
MODEL="${MODEL:-gpt-5.5}"
USERS="${USERS:-500}"
CONCURRENCY="${CONCURRENCY:-500}"
DURATION="${DURATION:-30s}"
MAX_TOKENS="${MAX_TOKENS:-10}"
STREAM_FLAG=""
if [[ "${STREAM:-false}" == "true" ]]; then
    STREAM_FLAG="-stream"
fi

if [[ -z "${API_KEY:-}" ]]; then
    echo "Error: API_KEY environment variable is required"
    echo "  export API_KEY=sk-wgit-xxx"
    echo "  # or for multi-key:"
    echo "  export API_KEY=sk-wgit-key1,sk-wgit-key2,sk-wgit-key3"
    exit 1
fi

# Build if needed
BINARY="$PROJECT_DIR/tests/loadtest/loadtest"
if [[ ! -f "$BINARY" ]] || [[ "$PROJECT_DIR/tests/loadtest/main.go" -nt "$BINARY" ]]; then
    echo "Building load test tool..."
    go build -o "$BINARY" ./tests/loadtest/ || exit 1
fi

MODE="${1:-burst}"

run_test() {
    local mode="$1"
    shift
    echo ""
    echo "================================================"
    echo "  Running: $mode"
    echo "================================================"
    "$BINARY" \
        -mode="$mode" \
        -proxy="$PROXY_URL" \
        -key="$API_KEY" \
        -model="$MODEL" \
        -max-tokens="$MAX_TOKENS" \
        $STREAM_FLAG \
        "$@"
}

case "$MODE" in
    burst)
        run_test burst -users="$USERS"
        ;;
    sustained)
        run_test sustained -concurrency="$CONCURRENCY" -duration="$DURATION"
        ;;
    rampup)
        run_test rampup -max-concurrency="$CONCURRENCY" -duration="${DURATION:-60s}"
        ;;
    all)
        run_test burst -users="$USERS"
        echo ""
        sleep 2
        run_test sustained -concurrency="$CONCURRENCY" -duration="$DURATION"
        echo ""
        sleep 2
        run_test rampup -max-concurrency="$CONCURRENCY" -duration="${DURATION:-60s}"
        ;;
    *)
        echo "Usage: $0 {burst|sustained|rampup|all}"
        echo ""
        echo "Examples:"
        echo "  API_KEY=sk-wgit-xxx $0 burst"
        echo "  API_KEY=sk-wgit-xxx USERS=100 $0 burst"
        echo "  API_KEY=sk-wgit-xxx CONCURRENCY=200 DURATION=60s $0 sustained"
        echo "  API_KEY=sk-wgit-xxx $0 all"
        exit 1
        ;;
esac
