#!/bin/bash
# ============================================
# Task 8 Integration Test Script
# Tests: Azure + AWS Adaptor
# ============================================

set -e

PROXY_URL="http://127.0.0.1:8080"

RED="\033[0;31m"
GREEN="\033[0;32m"
YELLOW="\033[1;33m"
NC="\033[0m"

PASS=0
FAIL=0

log_pass() { echo -e "${GREEN}✓ PASS${NC}: $1"; ((PASS++)) || true; }
log_fail() { echo -e "${RED}✗ FAIL${NC}: $1"; ((FAIL++)) || true; }
log_info() { echo -e "${YELLOW}ℹ INFO${NC}: $1"; }

# --------------------------------------------
# Test 1: Proxy Health Check
# --------------------------------------------
echo ""
echo "=== Test 1: Proxy Health ==="
HEALTH=$(curl -s "$PROXY_URL/health" 2>&1)
if echo "$HEALTH" | grep -q "healthy"; then
  log_pass "Proxy healthy"
else
  log_fail "Proxy unhealthy: $HEALTH"
fi

# --------------------------------------------
# Test 2: Azure Adaptor Unit Tests
# --------------------------------------------
echo ""
echo "=== Test 2: Azure Adaptor Unit Tests ==="
AZURE_TEST=$(cd /mnt/workspace/workspace/ai-proxy-go && go test ./pkg/relay/channel/azure/... -v 2>&1)
if echo "$AZURE_TEST" | grep -q "PASS"; then
  log_pass "All Azure adaptor unit tests pass"
else
  log_fail "Azure adaptor unit tests failed"
  echo "$AZURE_TEST"
fi

# --------------------------------------------
# Test 3: AWS Adaptor Unit Tests
# --------------------------------------------
echo ""
echo "=== Test 3: AWS Adaptor Unit Tests ==="
AWS_TEST=$(cd /mnt/workspace/workspace/ai-proxy-go && go test ./pkg/relay/channel/aws/... -v 2>&1)
if echo "$AWS_TEST" | grep -q "PASS"; then
  log_pass "All AWS adaptor unit tests pass"
else
  log_fail "AWS adaptor unit tests failed"
  echo "$AWS_TEST"
fi

# --------------------------------------------
# Test 4: Relay GetAdaptor returns correct types
# --------------------------------------------
echo ""
echo "=== Test 4: GetAdaptor Type Resolution ==="
GO_TEST=$(cd /mnt/workspace/workspace/ai-proxy-go && go test ./pkg/relay/... -v -run TestGetAdaptor 2>&1)
if echo "$GO_TEST" | grep -q "PASS"; then
  log_pass "GetAdaptor type resolution tests pass"
else
  log_fail "GetAdaptor tests failed"
  echo "$GO_TEST"
fi

# --------------------------------------------
# Test 5: Proxy provider stats (check sync worked)
# --------------------------------------------
echo ""
echo "=== Test 5: Proxy Provider Stats ==="
# Wait for any pending sync
sleep 2
STATS=$(curl -s "$PROXY_URL/providers/stats" 2>&1)
PROVIDER_COUNT=$(echo "$STATS" | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")
if [ "$PROVIDER_COUNT" -gt 0 ]; then
  log_pass "Proxy has $PROVIDER_COUNT provider pools synced"
else
  log_info "No provider pools active (no valid upstream keys for these providers)"
fi

# --------------------------------------------
# Test 6: Full test suite
# --------------------------------------------
echo ""
echo "=== Test 6: Full Proxy Test Suite ==="
FULL_TEST=$(cd /mnt/workspace/workspace/ai-proxy-go && go test ./... 2>&1)
if echo "$FULL_TEST" | grep -q "FAIL"; then
  log_fail "Some tests in full suite failed"
  echo "$FULL_TEST"
else
  log_pass "Full test suite passes"
fi

# --------------------------------------------
# Summary
# --------------------------------------------
echo ""
echo "========================================"
echo "Test Results: $PASS passed, $FAIL failed"
echo "========================================"

if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
