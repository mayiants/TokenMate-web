#!/usr/bin/bash
# AI Portal Go - Quick Integration Test
# Run: bash tests/test-portal-quick.sh

BASE="${PORTAL_URL:-http://localhost:3001}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-Admin@123456!}"
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'
PASS=0
FAIL=0

test_endpoint() {
    local method=$1
    local path=$2
    local expected=$3
    local data=$4
    local token=$5
    
    local cmd="curl -s -m 10 -o /dev/null -w '%{http_code}' -X $method"
    
    if [ -n "$data" ]; then
        cmd="$cmd -d '$data' -H 'Content-Type: application/json'"
    fi
    
    if [ -n "$token" ]; then
        cmd="$cmd -H 'Authorization: Bearer $token'"
    fi
    
    cmd="$cmd '$BASE$path'"
    
    local status=$(eval $cmd 2>/dev/null)
    
    if [ "$status" = "$expected" ]; then
        ((PASS++))
        echo -e "${GREEN}✓ $method $path -> $status${NC}"
    else
        ((FAIL++))
        echo -e "${RED}✗ $method $path -> $status (expected $expected)${NC}"
    fi
}

echo "=== AI Portal Go - Quick Test ==="
echo ""

# Public endpoints
echo "--- Public ---"
test_endpoint GET "/api/health" "200"
test_endpoint GET "/api/auth/password-requirements" "200"
test_endpoint GET "/api/service-models/public" "200"

# Login
echo ""
echo "--- Login ---"
RESPONSE=$(curl -s -m 10 -X POST "$BASE/api/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}")
TOKEN=$(echo "$RESPONSE" | jq -r '.token // empty')

if [ -n "$TOKEN" ]; then
    echo -e "${GREEN}✓ Login successful${NC}"
    ((PASS++))
else
    echo -e "${RED}✗ Login failed${NC}"
    ((FAIL++))
    echo "Response: $RESPONSE"
    exit 1
fi

# Auth endpoints
echo ""
echo "--- Auth ---"
test_endpoint GET "/api/auth/me" "200" "" "$TOKEN"
test_endpoint GET "/api/profile" "200" "" "$TOKEN"

# Dashboard
echo ""
echo "--- Dashboard ---"
test_endpoint GET "/api/admin/dashboard/charts/consumption-trend" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/dashboard/stats" "200" "" "$TOKEN"

# API Keys
echo ""
echo "--- API Keys ---"
test_endpoint GET "/api/apikeys" "200" "" "$TOKEN"
test_endpoint POST "/api/apikeys" "200" '{"name":"test"}' "$TOKEN"

# Billing
echo ""
echo "--- Billing ---"
test_endpoint GET "/api/billing/balance" "200" "" "$TOKEN"
test_endpoint GET "/api/billing/usage-details" "200" "" "$TOKEN"
test_endpoint GET "/api/billing/usage-summary" "200" "" "$TOKEN"

# Usage
echo ""
echo "--- Usage ---"
test_endpoint GET "/api/usage" "200" "" "$TOKEN"
test_endpoint GET "/api/usage/summary" "200" "" "$TOKEN"

# Orders
echo ""
echo "--- Orders ---"
test_endpoint GET "/api/orders" "200" "" "$TOKEN"

# Invoices
echo ""
echo "--- Invoices ---"
test_endpoint GET "/api/invoices/profiles" "200" "" "$TOKEN"

# Redemption
echo ""
echo "--- Redemption ---"
test_endpoint GET "/api/voucher/records" "200" "" "$TOKEN"

# Admin
echo ""
echo "--- Admin ---"
test_endpoint GET "/api/admin/dashboard" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/users" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/providers" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/models" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/service-models" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/key-pool" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/upstream-keys" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/settings" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/groups" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/enterprise" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/subscriptions/plans" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/subscriptions/stats" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/audit-logs" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/channel-health" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/downstream-keys" "200" "" "$TOKEN"

# Admin Usage
echo ""
echo "--- Admin Usage ---"
test_endpoint GET "/api/admin/usage/upstream" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/usage/users" "200" "" "$TOKEN"
test_endpoint GET "/api/admin/usage/summary" "200" "" "$TOKEN"

# Error cases
echo ""
echo "--- Error Cases ---"
test_endpoint GET "/api/auth/me" "401"
test_endpoint GET "/api/nonexistent" "404"

# Summary
echo ""
echo "=========================================="
echo -e "  ${GREEN}Passed: $PASS${NC}"
echo -e "  ${RED}Failed: $FAIL${NC}"
echo "  Total: $((PASS + FAIL))"
echo "=========================================="

# Fail the script (and CI) when any endpoint check failed — previously the
# script always exited 0, making the smoke test a gate that could not fail.
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
