#!/bin/bash
# ============================================
# AI Platform Go — Local CI Runner
# ============================================
# Mirrors the GitHub Actions fast-gate/regression checks plus the new
# platform E2E tests, and runs them against the current working tree.
#
# Usage:
#   bash scripts/run-local-ci.sh
#
# Environment overrides:
#   PROXY_DIR          path to ai-proxy-go (default: ../ai-proxy-go)
#   SKIP_LINT_ENGLISH  set to 1 to skip Vale-based English lint
#   SKIP_TAILWIND      set to 1 to skip Tailwind rebuild
#   SKIP_GOVCULN       set to 1 to skip govulncheck (slow)
#   SKIP_LIVE_E2E      set to 1 to skip tests that need the running stack
# ============================================

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
PROXY_DIR="${PROXY_DIR:-$PROJECT_DIR/../ai-proxy-go}"

# 让后续需要认证/地址的脚本能读到当前启动栈的配置
if [[ -f "$PROJECT_DIR/.env.go-platform" ]]; then
    set -a
    # shellcheck source=/dev/null
    source "$PROJECT_DIR/.env.go-platform"
    set +a
fi

PASS=0
FAIL=0
SKIPPED=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

step() {
    local name="$1"
    shift
    echo -e "\n${BLUE}=== $name ===${NC}"
    if "$@" >/tmp/ci-step-$$.log 2>&1; then
        echo -e "${GREEN}✓${NC} $name"
        PASS=$((PASS + 1))
    else
        echo -e "${RED}✗${NC} $name"
        FAIL=$((FAIL + 1))
        tail -n 60 /tmp/ci-step-$$.log | sed 's/^/    /'
    fi
}

step_skip() {
    local name="$1"
    echo -e "\n${YELLOW}⊘ $name (skipped)${NC}"
    SKIPPED=$((SKIPPED + 1))
}

run_in_dir() {
    local dir="$1"
    shift
    (cd "$dir" && "$@")
}

# ============================================
# Portal static / build checks
# ============================================

portal_build() {
    run_in_dir "$PROJECT_DIR" go build -o /tmp/portal-ci ./cmd/server
}

portal_vet() {
    run_in_dir "$PROJECT_DIR" go vet ./...
}

portal_fmt() {
    local files
    files="$(run_in_dir "$PROJECT_DIR" gofmt -l .)"
    if [[ -n "$files" ]]; then
        echo "Unformatted files:"
        echo "$files"
        return 1
    fi
}

portal_schema_check() {
    run_in_dir "$PROJECT_DIR" bash scripts/check_schema_vs_code.sh
}

portal_i18n_audit() {
    run_in_dir "$PROJECT_DIR" python3 scripts/i18n_full_audit.py --strict
}

portal_tailwind() {
    if [[ "${SKIP_TAILWIND:-0}" == "1" ]]; then
        return 0
    fi
    run_in_dir "$PROJECT_DIR" bash scripts/rebuild-tailwind.sh
}

portal_lint_english() {
    if [[ "${SKIP_LINT_ENGLISH:-0}" == "1" ]]; then
        return 0
    fi
    run_in_dir "$PROJECT_DIR" node scripts/lint-english.js --gate
}

portal_unit_tests() {
    run_in_dir "$PROJECT_DIR" go test ./internal/middleware/... ./internal/web/... ./pkg/... ./tests/performance/... -v
}

portal_govulncheck() {
    if [[ "${SKIP_GOVCULN:-0}" == "1" ]]; then
        return 0
    fi
    run_in_dir "$PROJECT_DIR" govulncheck ./...
}

# ============================================
# Proxy static / build / unit checks
# ============================================

proxy_build() {
    run_in_dir "$PROXY_DIR" go build -o /tmp/ai-proxy-go-ci ./cmd/server
}

proxy_vet() {
    run_in_dir "$PROXY_DIR" go vet ./...
}

proxy_fmt() {
    local files
    files="$(run_in_dir "$PROXY_DIR" gofmt -l .)"
    if [[ -n "$files" ]]; then
        echo "Unformatted files:"
        echo "$files"
        return 1
    fi
}

proxy_lint() {
    run_in_dir "$PROXY_DIR" golangci-lint run
}

proxy_unit_tests() {
    run_in_dir "$PROXY_DIR" go test -v -race ./...
}

proxy_govulncheck() {
    if [[ "${SKIP_GOVCULN:-0}" == "1" ]]; then
        return 0
    fi
    run_in_dir "$PROXY_DIR" govulncheck ./...
}

proxy_e2e_transform() {
    run_in_dir "$PROXY_DIR" bash tests/e2e/e2e-internal-transform.sh
}

# ============================================
# Live stack integration / E2E checks
# ============================================

stack_healthy() {
    curl -sf http://127.0.0.1:3001/api/health >/dev/null 2>&1 && \
    curl -sf http://127.0.0.1:8080/health >/dev/null 2>&1
}

live_e2e_platform() {
    run_in_dir "$PROJECT_DIR" bash tests/test-e2e-platform.sh
}

live_portal_quick() {
    run_in_dir "$PROJECT_DIR" bash tests/test-portal-quick.sh
}

live_integration_all() {
    run_in_dir "$PROJECT_DIR" bash tests/test-integration-all.sh
}

live_proxy_integration() {
    run_in_dir "$PROXY_DIR" bash tests/test-integration.sh
}

# ============================================
# Main
# ============================================

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}  AI Platform Go — Local CI Runner${NC}"
echo -e "${BLUE}========================================${NC}"
echo "Portal dir: $PROJECT_DIR"
echo "Proxy dir:  $PROXY_DIR"

step "Portal: go build" portal_build
step "Portal: go vet" portal_vet
step "Portal: gofmt" portal_fmt
step "Portal: schema drift check" portal_schema_check
step "Portal: i18n full audit" portal_i18n_audit
step "Portal: Tailwind CSS rebuild" portal_tailwind
step "Portal: English copy lint" portal_lint_english
step "Portal: unit tests (no DB)" portal_unit_tests
step "Portal: govulncheck" portal_govulncheck

step "Proxy: go build" proxy_build
step "Proxy: go vet" proxy_vet
step "Proxy: gofmt" proxy_fmt
step "Proxy: golangci-lint" proxy_lint
step "Proxy: unit tests" proxy_unit_tests
step "Proxy: govulncheck" proxy_govulncheck
step "Proxy: internal transform E2E" proxy_e2e_transform

if [[ "${SKIP_LIVE_E2E:-0}" == "1" ]]; then
    step_skip "Live stack E2E (SKIP_LIVE_E2E=1)"
elif stack_healthy; then
    echo -e "\n${GREEN}Live stack detected on 127.0.0.1:3001 / 127.0.0.1:8080${NC}"
    # Override Docker-internal URLs for host-based test scripts
    export PORTAL_URL="http://127.0.0.1:${PORTAL_PORT:-3001}"
    export PROXY_URL="http://127.0.0.1:${PROXY_PORT:-8080}"
    step "Live: platform E2E (Redis + sync + upstream)" live_e2e_platform
    # 把 platform E2E 创建的 key 透传给 proxy 集成测试
    if [[ -n "${TEST_API_KEY:-}" ]]; then
        export API_KEY="$TEST_API_KEY"
    fi
    step "Live: portal quick integration" live_portal_quick
    step "Live: full integration suite" live_integration_all
    step "Live: proxy integration" live_proxy_integration
else
    step_skip "Live stack E2E (no healthy services on 127.0.0.1:3001 / 127.0.0.1:8080)"
fi

echo -e "\n${BLUE}========================================${NC}"
echo -e "${BLUE}  Local CI Summary${NC}"
echo -e "${BLUE}========================================${NC}"
echo -e "${GREEN}Passed:  $PASS${NC}"
echo -e "${RED}Failed:  $FAIL${NC}"
echo -e "${YELLOW}Skipped: $SKIPPED${NC}"

if [[ $FAIL -eq 0 ]]; then
    echo -e "\n${GREEN}✓ Local CI passed${NC}"
    exit 0
else
    echo -e "\n${RED}✗ Local CI failed${NC}"
    exit 1
fi
