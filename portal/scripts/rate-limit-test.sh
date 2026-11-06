#!/bin/bash
# ============================================
# Rate Limit Performance Test
# ============================================
# Tests the Proxy rate limiter with simulated concurrent users
#
# Usage:
#   ./rate-limit-test.sh [users] [rpm_per_user] [duration_seconds]
#   ./rate-limit-test.sh 1000 60 60   # 1000 users, 60 RPM each, 60 seconds
#   ./rate-limit-test.sh 2000 60 60   # 2000 users, 60 RPM each
#   ./rate-limit-test.sh 5000 60 60   # 5000 users, 60 RPM each
# ============================================

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# Configuration
USERS="${1:-1000}"
RPM_PER_USER="${2:-60}"
DURATION="${3:-60}"
PROXY_URL="http://127.0.0.1:8080"
PORTAL_URL="http://127.0.0.1:3001"

# Test state
TOTAL_REQUESTS=0
SUCCESS_REQUESTS=0
RATE_LIMITED=0
FAILED_REQUESTS=0
TOTAL_LATENCY_MS=0

echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  Rate Limit Performance Test${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""
echo -e "${BLUE}Configuration:${NC}"
echo -e "  Users:           ${YELLOW}${USERS}${NC}"
echo -e "  RPM per user:    ${YELLOW}${RPM_PER_USER}${NC}"
echo -e "  Duration:        ${YELLOW}${DURATION}s${NC}"
echo -e "  Expected RPS:    ${YELLOW}$((USERS * RPM_PER_USER / 60))${NC} requests/second"
echo -e "  Total expected:  ${YELLOW}$((USERS * RPM_PER_USER / 60 * DURATION))${NC} requests"
echo ""

# Check services are running
echo -e "${BLUE}Checking services...${NC}"
if ! curl -s "${PROXY_URL}/health" > /dev/null 2>&1; then
    echo -e "${RED}✗ Proxy not running at ${PROXY_URL}${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Proxy is running${NC}"

if ! curl -s "${PORTAL_URL}/api/health" > /dev/null 2>&1; then
    echo -e "${RED}✗ Portal not running at ${PORTAL_URL}${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Portal is running${NC}"
echo ""

# Create test API keys
echo -e "${BLUE}Creating test API keys...${NC}"

# Get admin token
ADMIN_TOKEN=$(curl -s -X POST "${PORTAL_URL}/api/auth/login" \
    -H "Content-Type: application/json" \
    -d '{"email":"admin@example.com","password":"Admin@123456!"}' | jq -r '.token // .access_token // empty')

if [ -z "$ADMIN_TOKEN" ] || [ "$ADMIN_TOKEN" = "null" ]; then
    echo -e "${RED}✗ Failed to get admin token${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Got admin token${NC}"

# Create test user if not exists
TEST_USER_ID=$(curl -s "${PORTAL_URL}/api/admin/users?email=test-perf@example.com" \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" | jq -r '.users[0].id // empty')

if [ -z "$TEST_USER_ID" ] || [ "$TEST_USER_ID" = "null" ]; then
    TEST_USER_ID=$(curl -s -X POST "${PORTAL_URL}/api/admin/users" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        -H "Content-Type: application/json" \
        -d '{"email":"test-perf@example.com","name":"Performance Test User","password":"Test123456"}' | jq -r '.id // empty')
    echo -e "${GREEN}✓ Created test user: ${TEST_USER_ID}${NC}"
else
    echo -e "${GREEN}✓ Using existing test user: ${TEST_USER_ID}${NC}"
fi

# Create API keys for each "user" (we'll use a smaller set and rotate)
KEY_COUNT=$((USERS > 100 ? 100 : USERS))
echo -e "${BLUE}Creating ${KEY_COUNT} API keys...${NC}"

KEYS_FILE="/tmp/perf-test-keys.txt"
> "$KEYS_FILE"

for i in $(seq 1 $KEY_COUNT); do
    KEY_RESPONSE=$(curl -s -X POST "${PORTAL_URL}/api/apikeys" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"perf-test-${i}\",\"rate_limit_rpm\":${RPM_PER_USER},\"rate_limit_tpm\":100000}")
    KEY=$(echo "$KEY_RESPONSE" | jq -r '.key // .api_key // empty')
    if [ -n "$KEY" ] && [ "$KEY" != "null" ]; then
        echo "$KEY" >> "$KEYS_FILE"
    fi
done

KEYS_CREATED=$(wc -l < "$KEYS_FILE")
echo -e "${GREEN}✓ Created ${KEYS_CREATED} API keys${NC}"

if [ "$KEYS_CREATED" -eq 0 ]; then
    echo -e "${RED}✗ No API keys created${NC}"
    exit 1
fi

echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  Starting Load Test${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# Create temp directory for results
RESULTS_DIR="/tmp/perf-test-results"
rm -rf "$RESULTS_DIR"
mkdir -p "$RESULTS_DIR"

# Function to run a single user's requests
run_user() {
    local user_id=$1
    local key_file=$2
    local duration=$3
    local rpm=$4
    local results_file=$5

    local key=$(sed -n "$((user_id % $(wc -l < "$key_file") + 1))p" "$key_file")
    local requests_per_second=$((rpm / 60))
    local delay_ms=$((1000 / requests_per_second))

    local start_time=$(date +%s)
    local end_time=$((start_time + duration))

    local user_requests=0
    local user_success=0
    local user_rate_limited=0
    local user_failed=0
    local user_latency_total=0

    while [ $(date +%s) -lt $end_time ]; do
        local req_start=$(date +%s%3N)

        response=$(curl -s -w "\n%{http_code}" -X POST "${PROXY_URL}/v1/chat/completions" \
            -H "Authorization: Bearer ${key}" \
            -H "Content-Type: application/json" \
            -d '{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"hi"}],"max_tokens":5}' 2>/dev/null)

        local req_end=$(date +%s%3N)
        local latency=$((req_end - req_start))

        http_code=$(echo "$response" | tail -1)

        user_requests=$((user_requests + 1))
        user_latency_total=$((user_latency_total + latency))

        case $http_code in
            200)
                user_success=$((user_success + 1))
                ;;
            429)
                user_rate_limited=$((user_rate_limited + 1))
                ;;
            *)
                user_failed=$((user_failed + 1))
                ;;
        esac

        # Small delay to not overwhelm the system
        sleep 0.001
    done

    echo "${user_id},${user_requests},${user_success},${user_rate_limited},${user_failed},${user_latency_total}" >> "$results_file"
}

# Run parallel users using background processes
echo -e "${BLUE}Launching ${USERS} concurrent users...${NC}"

START_TIME=$(date +%s)

# Launch users in batches to avoid fork bomb
BATCH_SIZE=100
BATCHES=$((USERS / BATCH_SIZE + 1))

for batch in $(seq 0 $((BATCHES - 1))); do
    batch_start=$((batch * BATCH_SIZE))
    batch_end=$((batch_start + BATCH_SIZE))
    [ $batch_end -gt $USERS ] && batch_end=$USERS

    for user_id in $(seq $((batch_start + 1)) $batch_end); do
        run_user $user_id "$KEYS_FILE" $DURATION $RPM_PER_USER "$RESULTS_DIR/results_${user_id}.csv" &
    done

    # Small delay between batches
    sleep 0.1

    # Progress update
    if [ $((batch % 10)) -eq 0 ]; then
        echo -e "${BLUE}  Launched $((batch_end)) / ${USERS} users...${NC}"
    fi
done

echo -e "${YELLOW}Waiting for test to complete...${NC}"
wait

END_TIME=$(date +%s)
ACTUAL_DURATION=$((END_TIME - START_TIME))

echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  Test Results${NC}"
echo -e "${CYAN}========================================${NC}"
echo ""

# Aggregate results
TOTAL_REQUESTS=0
SUCCESS_REQUESTS=0
RATE_LIMITED=0
FAILED_REQUESTS=0
TOTAL_LATENCY_MS=0

for f in "$RESULTS_DIR"/*.csv; do
    if [ -f "$f" ]; then
        IFS=',' read -r uid req success rate_limited failed latency < <(cat "$f")
        TOTAL_REQUESTS=$((TOTAL_REQUESTS + req))
        SUCCESS_REQUESTS=$((SUCCESS_REQUESTS + success))
        RATE_LIMITED=$((RATE_LIMITED + rate_limited))
        FAILED_REQUESTS=$((FAILED_REQUESTS + failed))
        TOTAL_LATENCY_MS=$((TOTAL_LATENCY_MS + latency))
    fi
done

# Calculate metrics
if [ $TOTAL_REQUESTS -gt 0 ]; then
    AVG_LATENCY_MS=$((TOTAL_LATENCY_MS / TOTAL_REQUESTS))
    RPS=$((TOTAL_REQUESTS / ACTUAL_DURATION))
    SUCCESS_RATE=$(echo "scale=2; $SUCCESS_REQUESTS * 100 / $TOTAL_REQUESTS" | bc)
    RATE_LIMIT_RATE=$(echo "scale=2; $RATE_LIMITED * 100 / $TOTAL_REQUESTS" | bc)
else
    AVG_LATENCY_MS=0
    RPS=0
    SUCCESS_RATE=0
    RATE_LIMIT_RATE=0
fi

echo -e "${BLUE}Test Summary:${NC}"
echo ""
echo -e "  Duration:           ${YELLOW}${ACTUAL_DURATION}s${NC}"
echo -e "  Total Requests:     ${YELLOW}${TOTAL_REQUESTS}${NC}"
echo -e "  Successful (200):   ${GREEN}${SUCCESS_REQUESTS}${NC} (${SUCCESS_RATE}%)"
echo -e "  Rate Limited (429): ${RED}${RATE_LIMITED}${NC} (${RATE_LIMIT_RATE}%)"
echo -e "  Failed:             ${RED}${FAILED_REQUESTS}${NC}"
echo ""
echo -e "${BLUE}Performance Metrics:${NC}"
echo ""
echo -e "  Requests/sec:       ${YELLOW}${RPS}${NC}"
echo -e "  Avg Latency:        ${YELLOW}${AVG_LATENCY_MS}ms${NC}"
echo ""

# Check if rate limiter is working
echo -e "${BLUE}Rate Limiter Analysis:${NC}"
echo ""

EXPECTED_RPS=$((USERS * RPM_PER_USER / 60))
if [ $RATE_LIMITED -gt 0 ]; then
    echo -e "  ${GREEN}✓ Rate limiter is working (${RATE_LIMITED} requests were rate limited)${NC}"

    # Check if rate limiting is reasonable (should be some but not too many)
    if [ $(echo "$RATE_LIMIT_RATE > 5" | bc) -eq 1 ]; then
        echo -e "  ${YELLOW}⚠ High rate limiting (${RATE_LIMIT_RATE}%) - users may be exceeding limits${NC}"
    else
        echo -e "  ${GREEN}✓ Rate limiting within expected range${NC}"
    fi
else
    echo -e "  ${YELLOW}⚠ No rate limiting detected - check if rate limits are properly configured${NC}"
fi

# Performance assessment
echo ""
echo -e "${BLUE}Performance Assessment:${NC}"
echo ""

if [ $RPS -ge 100000 ]; then
    echo -e "  ${GREEN}✓ Excellent: ${RPS} RPS (100K+)${NC}"
elif [ $RPS -ge 50000 ]; then
    echo -e "  ${GREEN}✓ Very Good: ${RPS} RPS (50K+)${NC}"
elif [ $RPS -ge 10000 ]; then
    echo -e "  ${YELLOW}⚠ Good: ${RPS} RPS (10K+)${NC}"
else
    echo -e "  ${RED}✗ Needs improvement: ${RPS} RPS (<10K)${NC}"
fi

if [ $AVG_LATENCY_MS -lt 10 ]; then
    echo -e "  ${GREEN}✓ Excellent latency: ${AVG_LATENCY_MS}ms${NC}"
elif [ $AVG_LATENCY_MS -lt 50 ]; then
    echo -e "  ${GREEN}✓ Good latency: ${AVG_LATENCY_MS}ms${NC}"
elif [ $AVG_LATENCY_MS -lt 100 ]; then
    echo -e "  ${YELLOW}⚠ Acceptable latency: ${AVG_LATENCY_MS}ms${NC}"
else
    echo -e "  ${RED}✗ High latency: ${AVG_LATENCY_MS}ms${NC}"
fi

# Cleanup
echo ""
echo -e "${BLUE}Cleaning up...${NC}"
rm -rf "$RESULTS_DIR"

# Optionally delete test keys
# while read key; do
#     curl -s -X DELETE "${PORTAL_URL}/api/apikeys/${key}" \
#         -H "Authorization: Bearer ${ADMIN_TOKEN}" > /dev/null
# done < "$KEYS_FILE"

rm -f "$KEYS_FILE"

echo -e "${GREEN}✓ Test completed${NC}"
