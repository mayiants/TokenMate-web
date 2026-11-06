#!/usr/bin/env bash
# AI Portal Go - Integration Test Script
# Run: bash tests/test-portal-integration.sh
# Prerequisites: Portal running on localhost:3001

set -e

BASE_URL="${PORTAL_URL:-http://localhost:3001}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-Admin@123456!}"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASS=0
FAIL=0
SKIP=0
TOKEN=""

# Helper functions
pass() { ((PASS++)); echo -e "${GREEN}✓ $1${NC}"; }
fail() { ((FAIL++)); echo -e "${RED}✗ $1${NC}"; }
skip() { ((SKIP++)); echo -e "${YELLOW}⊘ $1${NC}"; }

# Login and get token
login() {
    echo "--- Login ---"
    local response=$(curl -s -X POST "$BASE_URL/api/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}")
    
    TOKEN=$(echo "$response" | jq -r '.token // empty')
    
    if [ -n "$TOKEN" ]; then
        pass "Login successful"
    else
        fail "Login failed"
        echo "$response" | jq .
        exit 1
    fi
}

# Auth headers
auth_header() {
    echo "Authorization: Bearer $TOKEN"
}

check_status() {
    local desc="$1"
    local url="$2"
    local expected="$3"
    local method="${4:-GET}"
    local data="$5"
    
    local cmd="curl -s -o /dev/null -w '%{http_code}' -X $method"
    
    if [ -n "$data" ]; then
        cmd="$cmd -d '$data' -H 'Content-Type: application/json'"
    fi
    
    if [[ "$url" == *"auth"* ]] || [[ "$url" == *"admin"* ]] || [[ "$url" == *"profile"* ]] || [[ "$url" == *"dashboard"* ]] || [[ "$url" == *"apikeys"* ]] || [[ "$url" == *"billing"* ]]; then
        cmd="$cmd -H 'Authorization: Bearer $TOKEN'"
    fi
    
    cmd="$cmd '$url'"
    
    local status=$(eval $cmd 2>/dev/null)
    
    if [ "$status" = "$expected" ]; then
        pass "$desc (HTTP $status)"
    else
        fail "$desc (HTTP $status, expected $expected)"
    fi
}

check_json() {
    local desc="$1"
    local url="$2"
    local method="${3:-GET}"
    local data="$4"
    
    local cmd="curl -s -X $method -H 'Authorization: Bearer $TOKEN'"
    
    if [ -n "$data" ]; then
        cmd="$cmd -d '$data' -H 'Content-Type: application/json'"
    fi
    
    cmd="$cmd '$url'"
    
    local response=$(eval $cmd 2>/dev/null)
    
    if echo "$response" | jq . > /dev/null 2>&1; then
        pass "$desc (valid JSON)"
    else
        fail "$desc (invalid JSON)"
        echo "$response"
    fi
}

echo "=========================================="
echo "  AI Portal Go - Integration Tests"
echo "  Target: $BASE_URL"
echo "=========================================="
echo ""

# ============================================
# 1. Health & Public Endpoints
# ============================================
echo "--- Health & Public ---"

check_status "Health check" "$BASE_URL/api/health" "200"
check_json "Health response" "$BASE_URL/api/health"
check_json "Password requirements" "$BASE_URL/api/auth/password-requirements"
check_json "Public models" "$BASE_URL/api/service-models/public"

echo ""

# ============================================
# 2. Login
# ============================================
login
echo ""

# ============================================
# 3. Auth Endpoints
# ============================================
echo "--- Auth Endpoints ---"

check_json "Get current user" "$BASE_URL/api/auth/me"
check_json "Profile" "$BASE_URL/api/profile"

echo ""

# ============================================
# 4. Dashboard
# ============================================
echo "--- Dashboard ---"

check_json "Dashboard" "$BASE_URL/api/dashboard"
check_json "Dashboard stats" "$BASE_URL/api/dashboard/stats"

echo ""

# ============================================
# 5. API Keys
# ============================================
echo "--- API Keys ---"

check_json "List API keys" "$BASE_URL/api/apikeys"
check_status "Create API key" "$BASE_URL/api/apikeys" "200" "POST" '{"name":"test-key"}'
check_json "API key validate" "$BASE_URL/api/apikeys/validate"

echo ""

# ============================================
# 6. Billing
# ============================================
echo "--- Billing ---"

check_json "Balance" "$BASE_URL/api/billing/balance"
check_json "Usage details" "$BASE_URL/api/billing/usage-details"
check_json "Usage summary" "$BASE_URL/api/billing/usage-summary"
check_json "Usage by key" "$BASE_URL/api/billing/usage-by-key"
check_json "Usage by model" "$BASE_URL/api/billing/usage-by-model"

echo ""

# ============================================
# 7. Usage
# ============================================
echo "--- Usage ---"

check_json "Usage list" "$BASE_URL/api/usage"
check_json "Usage summary" "$BASE_URL/api/usage/summary"

echo ""

# ============================================
# 8. Orders
# ============================================
echo "--- Orders ---"

check_json "List orders" "$BASE_URL/api/orders"

echo ""

# ============================================
# 9. Invoices
# ============================================
echo "--- Invoices ---"

check_json "List invoices" "$BASE_URL/api/invoices"

echo ""

# ============================================
# 10. Redemption
# ============================================
echo "--- Redemption ---"

check_json "My redemption records" "$BASE_URL/api/redemption/my-records"

echo ""

# ============================================
# 11. Settings
# ============================================
echo "--- Settings ---"

check_json "Settings" "$BASE_URL/api/settings"

echo ""

# ============================================
# 13. Admin Endpoints
# ============================================
echo "--- Admin Endpoints ---"

check_json "Admin dashboard" "$BASE_URL/api/admin/dashboard"
check_json "Admin dashboard stats" "$BASE_URL/api/admin/dashboard/stats"
check_json "Admin users" "$BASE_URL/api/admin/users"
check_json "Admin providers" "$BASE_URL/api/admin/providers"
check_json "Admin models" "$BASE_URL/api/admin/models"
check_json "Admin service models" "$BASE_URL/api/admin/service-models"
check_json "Admin key pool" "$BASE_URL/api/admin/key-pool"
check_json "Admin upstream keys" "$BASE_URL/api/admin/upstream-keys"
check_json "Admin settings" "$BASE_URL/api/admin/settings"
check_json "Admin groups" "$BASE_URL/api/admin/groups"
check_json "Admin enterprise" "$BASE_URL/api/admin/enterprise/list"
check_json "Admin subscriptions plans" "$BASE_URL/api/admin/subscriptions/plans"
check_json "Admin subscriptions stats" "$BASE_URL/api/admin/subscriptions/stats"
check_json "Admin redemption codes" "$BASE_URL/api/redemption/admin/codes"
check_json "Admin redemption records" "$BASE_URL/api/redemption/admin/records"
check_json "Admin redemption stats" "$BASE_URL/api/redemption/admin/stats"
check_json "Admin audit logs" "$BASE_URL/api/admin/audit-logs"
check_json "Admin channel health" "$BASE_URL/api/admin/channel-health"
check_json "Admin downstream keys" "$BASE_URL/api/admin/downstream-keys"

echo ""

# ============================================
# 14. Admin Usage
# ============================================
echo "--- Admin Usage ---"

check_json "Admin usage upstream" "$BASE_URL/api/admin/usage/upstream"
check_json "Admin usage upstream keys" "$BASE_URL/api/admin/usage/upstream-keys"
check_json "Admin usage users" "$BASE_URL/api/admin/usage/users"
check_json "Admin usage user keys" "$BASE_URL/api/admin/usage/user-keys"
check_json "Admin usage summary" "$BASE_URL/api/admin/usage/summary"

echo ""

# ============================================
# 15. Error Cases
# ============================================
echo "--- Error Cases ---"

check_status "No auth - should 401" "$BASE_URL/api/auth/me" "401"
check_status "Invalid token - should 401" "$BASE_URL/api/auth/me" "401" "GET" "" "Authorization: Bearer invalid"
check_status "Not found - should 404" "$BASE_URL/api/nonexistent" "404"

echo ""

# ============================================
# Summary
# ============================================
echo "=========================================="
echo "  Test Results"
echo "=========================================="
echo -e "  ${GREEN}Passed: $PASS${NC}"
echo -e "  ${RED}Failed: $FAIL${NC}"
echo -e "  ${YELLOW}Skipped: $SKIP${NC}"
echo "  Total: $((PASS + FAIL + SKIP))"
echo "=========================================="

if [ $FAIL -gt 0 ]; then
    exit 1
fi
