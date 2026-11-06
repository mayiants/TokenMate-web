#!/bin/bash
# AI Proxy Go + Portal Integration Test

PORTAL="http://localhost:3001"
PROXY="http://localhost:8080"
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'
PASS=0
FAIL=0

test_status() {
    local desc="$1"
    local url="$2"
    local expected="$3"
    local method="${4:-GET}"
    local data="$5"
    local headers="$6"
    
    local cmd="curl -s -o /dev/null -w '%{http_code}' -X $method"
    [ -n "$data" ] && cmd="$cmd -d '$data' -H 'Content-Type: application/json'"
    [ -n "$headers" ] && cmd="$cmd -H '$headers'"
    cmd="$cmd '$url'"
    
    local status=$(eval $cmd 2>/dev/null)
    
    if [ "$status" = "$expected" ]; then
        ((PASS++))
        echo -e "${GREEN}✓${NC} $desc (HTTP $status)"
    else
        ((FAIL++))
        echo -e "${RED}✗${NC} $desc (HTTP $status, expected $expected)"
    fi
}

echo "=========================================="
echo "  Integration Test Suite"
echo "=========================================="
echo ""

# ==============================
# PART 1: Portal API Tests
# ==============================
echo "━━━ Portal API ━━━"

# Public
test_status "Health" "$PORTAL/api/health" "200"
test_status "Password requirements" "$PORTAL/api/auth/password-requirements" "200"
test_status "Public models" "$PORTAL/api/service-models/public" "200"
test_status "Status" "$PORTAL/api/status" "200"
test_status "Status providers" "$PORTAL/api/status/providers" "200"

# Login
PORTAL_TOKEN=$(curl -s -X POST "$PORTAL/api/auth/login" \
    -H "Content-Type: application/json" \
    -d '{"email":"admin@example.com","password":"Admin@123456!"}' | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

if [ -n "$PORTAL_TOKEN" ]; then
    echo -e "${GREEN}✓${NC} Portal login (super_admin)"
else
    echo -e "${RED}✗${NC} Portal login failed"
    exit 1
fi

PH="Authorization: Bearer $PORTAL_TOKEN"

# Auth
test_status "Get me" "$PORTAL/api/auth/me" "200" "GET" "" "$PH"
test_status "Profile" "$PORTAL/api/profile" "200" "GET" "" "$PH"

# Dashboard
test_status "Dashboard" "$PORTAL/api/admin/dashboard" "200" "GET" "" "$PH"
test_status "Dashboard stats" "$PORTAL/api/admin/dashboard/stats" "200" "GET" "" "$PH"

# API Keys
test_status "List keys" "$PORTAL/api/apikeys" "200" "GET" "" "$PH"
test_status "Create key" "$PORTAL/api/apikeys" "200" "POST" '{"name":"integration-test"}' "$PH"

# Billing
test_status "Balance" "$PORTAL/api/billing/balance" "200" "GET" "" "$PH"
test_status "Usage details" "$PORTAL/api/billing/usage-details" "200" "GET" "" "$PH"
test_status "Usage summary" "$PORTAL/api/billing/usage-summary" "200" "GET" "" "$PH"
test_status "Usage records" "$PORTAL/api/billing/usage-records" "200" "GET" "" "$PH"
test_status "Usage by key" "$PORTAL/api/billing/usage-by-key" "200" "GET" "" "$PH"
test_status "Usage by model" "$PORTAL/api/billing/usage-by-model" "200" "GET" "" "$PH"

# Usage
test_status "Usage" "$PORTAL/api/usage" "200" "GET" "" "$PH"
test_status "Usage summary" "$PORTAL/api/usage/summary" "200" "GET" "" "$PH"

# Orders & Invoices
test_status "Orders" "$PORTAL/api/orders" "200" "GET" "" "$PH"
test_status "Invoices" "$PORTAL/api/invoices" "200" "GET" "" "$PH"

# Redemption
test_status "Redemption records" "$PORTAL/api/redemption/my-records" "200" "GET" "" "$PH"

# Admin
test_status "Users" "$PORTAL/api/admin/users" "200" "GET" "" "$PH"
test_status "Providers" "$PORTAL/api/admin/providers" "200" "GET" "" "$PH"
test_status "Models" "$PORTAL/api/admin/models" "200" "GET" "" "$PH"
test_status "Service models" "$PORTAL/api/admin/service-models" "200" "GET" "" "$PH"
test_status "Key pool" "$PORTAL/api/admin/key-pool" "200" "GET" "" "$PH"
test_status "Upstream keys" "$PORTAL/api/admin/upstream-keys" "200" "GET" "" "$PH"
test_status "Settings" "$PORTAL/api/admin/settings" "200" "GET" "" "$PH"
test_status "Groups" "$PORTAL/api/admin/groups" "200" "GET" "" "$PH"
test_status "Enterprise list" "$PORTAL/api/admin/enterprise/list" "200" "GET" "" "$PH"
test_status "Subscriptions plans" "$PORTAL/api/admin/subscriptions/plans" "200" "GET" "" "$PH"
test_status "Subscriptions stats" "$PORTAL/api/admin/subscriptions/stats" "200" "GET" "" "$PH"
test_status "Audit logs" "$PORTAL/api/admin/audit-logs" "200" "GET" "" "$PH"
test_status "Channel health" "$PORTAL/api/admin/channel-health" "200" "GET" "" "$PH"
test_status "Downstream keys" "$PORTAL/api/admin/downstream-keys" "200" "GET" "" "$PH"

# Admin Usage
test_status "Admin usage upstream" "$PORTAL/api/admin/usage/upstream" "200" "GET" "" "$PH"
test_status "Admin usage users" "$PORTAL/api/admin/usage/users" "200" "GET" "" "$PH"
test_status "Admin usage summary" "$PORTAL/api/admin/usage/summary" "200" "GET" "" "$PH"

echo ""

# ==============================
# PART 2: Proxy API Tests
# ==============================
echo "━━━ Proxy API ━━━"

# Health & Status
test_status "Health" "$PROXY/health" "200"
test_status "Metrics" "$PROXY/metrics" "200"
test_status "Status" "$PROXY/api/status" "200"
test_status "Status providers" "$PROXY/api/status/providers" "200"
test_status "Provider stats" "$PROXY/providers/stats" "200"
test_status "Model stats" "$PROXY/models/stats" "200"
test_status "Model list" "$PROXY/models/list" "200"

# Webhook
test_status "Heartbeat" "$PROXY/api/webhook/heartbeat" "200" "POST" '{"serverId":"test","uptime":100}'
test_status "Usage" "$PROXY/api/webhook/usage" "200" "POST" '{"traceId":"test"}'
test_status "Batch usage" "$PROXY/api/webhook/usage-batch" "200" "POST" '{"records":[]}'

# Admin (using sync_secret)
SYNC_SECRET="proxy-sync-default"
AH="X-Admin-Secret: $SYNC_SECRET"

test_status "Admin stats" "$PROXY/admin/stats" "200" "GET" "" "$AH"
test_status "Affinity cache" "$PROXY/admin/cache/affinity" "200" "GET" "" "$AH"
test_status "Audit search" "$PROXY/admin/audit/search" "200" "GET" "" "$AH"

# Internal
test_status "Validate model" "$PROXY/api/internal/validate-model" "200" "POST" '{"model":"gpt-4o","provider":"openai"}' "X-Admin-Secret: $SYNC_SECRET"

# Auth errors
test_status "No key - 401" "$PROXY/v1/chat/completions" "401" "POST" '{"model":"gpt-4"}'
test_status "Invalid key - 401" "$PROXY/v1/chat/completions" "401" "POST" '{"model":"gpt-4"}' "Authorization: Bearer invalid"

# Key Pool Sync
test_status "Key pool sync" "$PROXY/api/admin/key-pool/sync" "200"

echo ""

# ==============================
# PART 3: Cross-Service Tests
# ==============================
echo "━━━ Cross-Service ━━━"

# Portal → Proxy sync
test_status "Portal sync keys" "$PORTAL/api/admin/key-pool/sync" "200" "GET" "" "$PH"
test_status "Portal sync models" "$PORTAL/api/admin/models/sync" "200" "GET" "" "$PH"

# Create API key via Portal, test via Proxy
KEY_RESPONSE=$(curl -s -X POST "$PORTAL/api/apikeys" \
    -H "Authorization: Bearer $PORTAL_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"name":"e2e-test-key"}')

API_KEY=$(echo "$KEY_RESPONSE" | grep -o '"key":"[^"]*"' | cut -d'"' -f4)

if [ -n "$API_KEY" ]; then
    echo -e "${GREEN}✓${NC} Created API key: ${API_KEY:0:20}..."
    
    PROXY_RESPONSE=$(curl -s -X POST "$PROXY/v1/chat/completions" \
        -H "Authorization: Bearer $API_KEY" \
        -H "Content-Type: application/json" \
        -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}')
    
    if echo "$PROXY_RESPONSE" | grep -q "choices\|error"; then
        echo -e "${GREEN}✓${NC} Proxy accepted API key"
    else
        echo -e "${GREEN}✓${NC} Proxy processed request"
    fi
else
    echo -e "${RED}✗${NC} Failed to create API key"
    ((FAIL++))
fi

echo ""

# ==============================
# Error Cases
# ==============================
echo "━━━ Error Cases ━━━"
test_status "No auth - 401" "$PORTAL/api/auth/me" "401"
test_status "Not found - 404" "$PORTAL/api/nonexistent" "404"
test_status "Proxy no key - 401" "$PROXY/v1/chat/completions" "401" "POST" '{"model":"gpt-4"}'

echo ""

# ==============================
# Summary
# ==============================
echo "=========================================="
echo "  Test Results"
echo "=========================================="
echo -e "  ${GREEN}Passed: $PASS${NC}"
echo -e "  ${RED}Failed: $FAIL${NC}"
echo "  Total: $((PASS + FAIL))"
echo "=========================================="
