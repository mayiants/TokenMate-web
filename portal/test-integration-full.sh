#!/bin/bash
# ============================================
# AI Portal Go - Full Integration Test
# ============================================
# Simulates browser interactions: login, navigate, click, submit forms
# ============================================

set +e  # Don't exit on first error — collect all test results

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

PORTAL_URL="http://127.0.0.1:3001"
PROXY_URL="http://127.0.0.1:8080"
TEST_RESULTS=()
PASS_COUNT=0
FAIL_COUNT=0

# ============================================
# Test Helpers
# ============================================

log_test() {
    local name="$1"
    local status="$2"
    local detail="$3"
    
    if [[ "$status" == "PASS" ]]; then
        echo -e "${GREEN}✓${NC} $name"
        ((PASS_COUNT++))
    else
        echo -e "${RED}✗${NC} $name"
        echo -e "  ${RED}Detail: $detail${NC}"
        ((FAIL_COUNT++))
    fi
    TEST_RESULTS+=("$status: $name")
}

# ============================================
# HTTP Helpers (simulate browser)
# ============================================

COOKIE_FILE="/tmp/test-cookies.txt"
rm -f "$COOKIE_FILE"

# GET request (like browser navigation)
http_get() {
    local url="$1"
    curl -s -b "$COOKIE_FILE" -c "$COOKIE_FILE" -L "$url" 2>/dev/null
}

# POST request (like form submission)
http_post() {
    local url="$1"
    local data="$2"
    local content_type="${3:-application/x-www-form-urlencoded}"
    curl -s -b "$COOKIE_FILE" -c "$COOKIE_FILE" -L \
        -H "Content-Type: $content_type" \
        -d "$data" \
        "$url" 2>/dev/null
}

# POST JSON
http_post_json() {
    local url="$1"
    local json="$2"
    http_post "$url" "$json" "application/json"
}

# ============================================
# Test: Health Check
# ============================================
test_health() {
    echo -e "\n${YELLOW}=== Health Check ===${NC}"
    
    # Portal health
    local portal_health=$(curl -s "$PORTAL_URL/api/health")
    if echo "$portal_health" | grep -q '"status":"ok"'; then
        log_test "Portal health" "PASS"
    else
        log_test "Portal health" "FAIL" "$portal_health"
    fi
    
    # Proxy health
    local proxy_health=$(curl -s "$PROXY_URL/health")
    if echo "$proxy_health" | grep -q '"status":"healthy"'; then
        log_test "Proxy health" "PASS"
    else
        log_test "Proxy health" "FAIL" "$proxy_health"
    fi
}

# ============================================
# Test: Landing Page
# ============================================
test_landing() {
    echo -e "\n${YELLOW}=== Landing Page ===${NC}"
    
    local html=$(http_get "$PORTAL_URL/")
    
    # Check page loads
    if [[ -n "$html" ]] && echo "$html" | grep -qi "html"; then
        log_test "Landing page loads" "PASS"
    else
        log_test "Landing page loads" "FAIL" "No HTML returned"
    fi
}

# ============================================
# Test: Login Flow
# ============================================
test_login() {
    echo -e "\n${YELLOW}=== Login Flow ===${NC}"
    
    # Get login page
    local login_page=$(http_get "$PORTAL_URL/login")
    if [[ -n "$login_page" ]] && echo "$login_page" | grep -qi "email\|password\|登录"; then
        log_test "Login page renders" "PASS"
    else
        log_test "Login page renders" "FAIL" "Login form not found"
    fi
    
    # Submit login (admin@example.com / Admin123456)
    local login_result=$(http_post "$PORTAL_URL/api/auth/login" '{"email":"admin@example.com","password":"Admin@123456!"}')
    
    if echo "$login_result" | grep -q '"success":true\|"token"'; then
        log_test "Admin login succeeds" "PASS"
        # Save token for API calls
        export AUTH_TOKEN=$(echo "$login_result" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
    else
        log_test "Admin login succeeds" "FAIL" "$login_result"
    fi
}

# ============================================
# Test: Dashboard
# ============================================
test_dashboard() {
    echo -e "\n${YELLOW}=== Dashboard ===${NC}"
    
    local status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/dashboard")
    
    if [[ "$status" == "200" ]]; then
        log_test "Dashboard loads" "PASS"
    else
        log_test "Dashboard loads" "FAIL" "HTTP $status"
    fi
}

# ============================================
# Test: API Keys Page
# ============================================
test_apikeys() {
    echo -e "\n${YELLOW}=== API Keys ===${NC}"
    
    local status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/apikeys")
    
    if [[ "$status" == "200" ]]; then
        log_test "API Keys page loads" "PASS"
    else
        log_test "API Keys page loads" "FAIL" "HTTP $status"
    fi
    
    # Create new API key via API
    if [[ -n "$AUTH_TOKEN" ]]; then
        local create_result=$(curl -s -H "Authorization: Bearer $AUTH_TOKEN" \
            -H "Content-Type: application/json" \
            -d '{"name":"Test Key"}' \
            "$PORTAL_URL/api/keys")
        
        if echo "$create_result" | grep -q '"id"\|"key"'; then
            log_test "Create API key" "PASS"
            export TEST_KEY=$(echo "$create_result" | grep -o '"key":"[^"]*"' | cut -d'"' -f4)
        else
            log_test "Create API key" "FAIL" "$create_result"
        fi
    fi
}

# ============================================
# Test: Billing Page
# ============================================
test_billing() {
    echo -e "\n${YELLOW}=== Billing ===${NC}"
    
    local status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/billing")
    
    if [[ "$status" == "200" ]]; then
        log_test "Billing page loads" "PASS"
    else
        log_test "Billing page loads" "FAIL" "HTTP $status"
    fi
}

# ============================================
# Test: Subscriptions
# ============================================
test_subscriptions() {
    echo -e "\n${YELLOW}=== Subscriptions ===${NC}"
    
    # Public plans page
    local status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/subscriptions")
    
    if [[ "$status" == "200" ]]; then
        log_test "Subscriptions page loads" "PASS"
    else
        log_test "Subscriptions page loads" "FAIL" "HTTP $status"
    fi
    
    # My subscriptions
    local my_status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/subscriptions/my")
    if [[ "$my_status" == "200" ]]; then
        log_test "My subscriptions page loads" "PASS"
    else
        log_test "My subscriptions page loads" "FAIL"
    fi
}

# ============================================
# Test: Admin Pages
# ============================================
test_admin() {
    echo -e "\n${YELLOW}=== Admin Pages ===${NC}"
    
    local status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/admin")
    if [[ "$status" == "200" ]]; then log_test "Admin page loads" "PASS"; else log_test "Admin page loads" "FAIL" "HTTP $status"; fi
    
    status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/admin/users")
    if [[ "$status" == "200" ]]; then log_test "Admin users page" "PASS"; else log_test "Admin users page" "FAIL" "HTTP $status"; fi
    
    status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/admin/groups")
    if [[ "$status" == "200" ]]; then log_test "Admin groups page" "PASS"; else log_test "Admin groups page" "FAIL" "HTTP $status"; fi
    
    status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/admin/enterprise")
    if [[ "$status" == "200" ]]; then log_test "Admin enterprise page" "PASS"; else log_test "Admin enterprise page" "FAIL" "HTTP $status"; fi
    
    status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/admin/subscriptions")
    if [[ "$status" == "200" ]]; then log_test "Admin subscriptions page" "PASS"; else log_test "Admin subscriptions page" "FAIL" "HTTP $status"; fi
    
    status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/admin/settings")
    if [[ "$status" == "200" ]]; then log_test "Admin settings page" "PASS"; else log_test "Admin settings page" "FAIL" "HTTP $status"; fi
}

# ============================================
# Test: Enterprise Pages
# ============================================
test_enterprise() {
    echo -e "\n${YELLOW}=== Enterprise Pages ===${NC}"
    
    local status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/enterprise")
    if [[ "$status" == "200" ]]; then log_test "Enterprise dashboard" "PASS"; else log_test "Enterprise dashboard" "FAIL" "HTTP $status"; fi
    
    status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/enterprise/users")
    if [[ "$status" == "200" ]]; then log_test "Enterprise users page" "PASS"; else log_test "Enterprise users page" "FAIL" "HTTP $status"; fi
    
    status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/enterprise/keys")
    if [[ "$status" == "200" ]]; then log_test "Enterprise keys page" "PASS"; else log_test "Enterprise keys page" "FAIL" "HTTP $status"; fi
    
    status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/enterprise/billing")
    if [[ "$status" == "200" ]]; then log_test "Enterprise billing page" "PASS"; else log_test "Enterprise billing page" "FAIL" "HTTP $status"; fi
}

# ============================================
# Test: Orders Page
# ============================================
test_orders() {
    echo -e "\n${YELLOW}=== Orders ===${NC}"
    local status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/orders")
    if [[ "$status" == "200" ]]; then log_test "Orders page loads" "PASS"; else log_test "Orders page loads" "FAIL" "HTTP $status"; fi
}

test_profile() {
    echo -e "\n${YELLOW}=== Profile ===${NC}"
    local status=$(curl -s -o /dev/null -w "%{http_code}" -b "$COOKIE_FILE" -L "$PORTAL_URL/profile")
    if [[ "$status" == "200" ]]; then log_test "Profile page loads" "PASS"; else log_test "Profile page loads" "FAIL" "HTTP $status"; fi
}

# ============================================
# Test: Proxy API (with test key)
# ============================================
test_proxy_api() {
    echo -e "\n${YELLOW}=== Proxy API ===${NC}"
    
    if [[ -n "$TEST_KEY" ]]; then
        # Test chat completion (simulate mode)
        local response=$(curl -s \
            -H "Authorization: Bearer $TEST_KEY" \
            -H "Content-Type: application/json" \
            -d '{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"Hello"}]}' \
            "$PROXY_URL/v1/chat/completions")
        
        if echo "$response" | grep -q '"choices"\|"content"\|"message"'; then
            log_test "Proxy chat completion" "PASS"
        else
            log_test "Proxy chat completion" "FAIL" "$response"
        fi
    else
        log_test "Proxy chat completion" "FAIL" "No test key available"
    fi
}

# ============================================
# Test: API Endpoints
# ============================================
test_api_endpoints() {
    echo -e "\n${YELLOW}=== API Endpoints ===${NC}"
    
    if [[ -n "$AUTH_TOKEN" ]]; then
        # List API keys
        local keys=$(curl -s -H "Authorization: Bearer $AUTH_TOKEN" "$PORTAL_URL/api/keys")
        if echo "$keys" | grep -q '"keys"'; then
            log_test "API: List keys" "PASS"
        else
            log_test "API: List keys" "FAIL" "Response: ${keys:0:100}"
        fi
        
        # Get subscription plans
        local plans=$(curl -s "$PORTAL_URL/api/subscriptions/plans")
        if echo "$plans" | grep -q '"plans"'; then
            log_test "API: List plans" "PASS"
        else
            log_test "API: List plans" "FAIL"
        fi
        
        # Get profile (includes balance)
        local balance=$(curl -s -H "Authorization: Bearer $AUTH_TOKEN" "$PORTAL_URL/api/profile")
        if echo "$balance" | grep -q '"balance"'; then
            log_test "API: Get profile" "PASS"
        else
            log_test "API: Get profile" "FAIL" "Response: ${balance:0:100}"
        fi
    fi
}

# ============================================
# Main
# ============================================
main() {
    echo -e "${CYAN}========================================${NC}"
    echo -e "${CYAN}  AI Portal Go - Integration Test${NC}"
    echo -e "${CYAN}========================================${NC}"
    echo -e "${BLUE}Portal: $PORTAL_URL${NC}"
    echo -e "${BLUE}Proxy:  $PROXY_URL${NC}"
    
    # Run all tests
    test_health
    test_landing
    test_login
    test_dashboard
    test_apikeys
    test_billing
    test_subscriptions
    test_admin
    test_enterprise
    test_orders
    test_profile
    test_api_endpoints
    test_proxy_api
    
    # Summary
    echo -e "\n${CYAN}========================================${NC}"
    echo -e "${CYAN}  Test Summary${NC}"
    echo -e "${CYAN}========================================${NC}"
    echo -e "${GREEN}Passed: $PASS_COUNT${NC}"
    echo -e "${RED}Failed: $FAIL_COUNT${NC}"
    echo -e "${BLUE}Total:  $((PASS_COUNT + FAIL_COUNT))${NC}"
    
    if [[ $FAIL_COUNT -eq 0 ]]; then
        echo -e "\n${GREEN}✓ All tests passed!${NC}"
        exit 0
    else
        echo -e "\n${RED}✗ Some tests failed${NC}"
        exit 1
    fi
}

main
