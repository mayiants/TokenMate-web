#!/bin/bash
# ============================================
# Group Pricing Ratio Billing Test
# Tests ratio=6.0 vs ratio=1.0 billing
# 10 user keys × round-robin × all providers
# ============================================

set -e

PORTAL="http://127.0.0.1:3001"
PROXY="http://127.0.0.1:8080"
ADMIN_EMAIL="${ADMIN_EMAIL:-e2e-admin@example.com}"
ADMIN_PASS="${ADMIN_PASS:-E2E_AdminPass123!}"
REDIS_PASS="${REDIS_PASS:-}"
REQUESTS_PER_USER=10  # per upstream key

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; YELLOW='\033[1;33m'; NC='\033[0m'
PASS=0; FAIL=0

RESULTS="/tmp/billing-test-results-$$.txt"
rm -f "$RESULTS"

log()   { echo -e "${CYAN}[$(date +%H:%M:%S)]${NC} $1"; }
pass()  { echo -e "  ${GREEN}PASS${NC} $1"; PASS=$((PASS+1)); }
fail()  { echo -e "  ${RED}FAIL${NC} $1"; FAIL=$((FAIL+1)); }

GRP_DEFAULT="1213d535-5267-4b64-ad62-1bcd93fd5c64"
GRP_PREMIUM="0d553315-b0f7-438d-9560-4fa851c48297"  # ratio=6.0

# Clear Redis rate limiter
docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" KEYS "*brute*" 2>/dev/null | grep -v Warning | while read k; do
    docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "$k" 2>/dev/null > /dev/null
done

# Login admin
TOKEN=$(curl -s -X POST "$PORTAL/api/auth/login" -H "Content-Type: application/json" \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
[[ -z "$TOKEN" ]] && { fail "Admin login failed"; exit 1; }
pass "Admin login"

ADMIN_HASH=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT password_hash FROM users WHERE email='admin@example.com'" 2>/dev/null)

# Get upstream keys + models
mapfile -t UK_PROV < <(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT provider FROM upstream_keys WHERE enabled=true AND validation_status='valid' GROUP BY provider ORDER BY provider" 2>/dev/null)

NUM_PROVIDERS=${#UK_PROV[@]}
log "Providers: ${UK_PROV[*]}"

# Helper: get model for provider
get_model() {
    case "$1" in
        minimax) echo "MiniMax-M2.5" ;;
        moonshot) echo "kimi-k2.5" ;;
        xiaomi) echo "mimo-v2.5" ;;
        shixin) echo "gpt-4o-mini" ;;
        xunfei) echo "astron-code-latest" ;;
        *) echo "MiniMax-M2.5" ;;
    esac
}

# Create test users: 10 in ratio=1.0 group, 10 in ratio=6.0 group
log "Creating 20 test users (10 ratio=1.0 + 10 ratio=6.0)..."
declare -A USER_TOKENS USER_KEYS USER_KIDS

for grp_pair in "control|$GRP_DEFAULT|1.0|ctrl" "premium|$GRP_PREMIUM|6.0|prem"; do
    grp_label=$(echo "$grp_pair" | cut -d'|' -f1)
    grp_id=$(echo "$grp_pair" | cut -d'|' -f2)
    grp_ratio=$(echo "$grp_pair" | cut -d'|' -f3)
    grp_prefix=$(echo "$grp_pair" | cut -d'|' -f4)

    for i in $(seq 1 10); do
        email="${grp_prefix}${i}@billingtest.com"
        name="${grp_prefix}${i}"

        # Upsert user
        docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
            -c "INSERT INTO users (email, password_hash, name, group_id, role, preferred_currency) VALUES ('$email', '$ADMIN_HASH', '$name', '$grp_id', 'user', 'USD') ON CONFLICT (email) DO UPDATE SET group_id='$grp_id', password_hash='$ADMIN_HASH', deleted_at=NULL, updated_at=NOW()" > /dev/null 2>&1

        # Login
        docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "auth_brute:172.18.0.1" 2>/dev/null > /dev/null
        TKN=$(curl -s -X POST "$PORTAL/api/auth/login" -H "Content-Type: application/json" \
            -d "{\"email\":\"$email\",\"password\":\"$ADMIN_PASS\"}" | \
            python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)

        # Create API key
        RESP=$(curl -s -X POST "$PORTAL/api/apikeys" -H "Authorization: Bearer $TKN" \
            -H "Content-Type: application/json" \
            -d "{\"name\":\"billing-key-${grp_prefix}${i}\",\"key_type\":\"general\"}" 2>/dev/null)
        KID=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
        KKEY=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('key',''))" 2>/dev/null)

        USER_TOKENS[$email]="$TKN"
        USER_KEYS[$email]="$KKEY"
        USER_KIDS[$email]="$KID"
    done
    log "  Created 10 users in group $grp_label (ratio=$grp_ratio)"
done

# Recharge all users
log "Recharging all users with 500000 micro-USD..."
for email in "${!USER_KEYS[@]}"; do
    uid=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
        -c "SELECT id FROM users WHERE email='$email'" 2>/dev/null)
    docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
        -c "UPDATE users SET recharge_balance=500000, balance=500000 WHERE id='$uid'" > /dev/null 2>&1
done
sleep 1

# Record pre-test balances
log "Recording pre-test balances..."
for email in "${!USER_TOKENS[@]}"; do
    BAL=$(curl -s "$PORTAL/api/billing/balance" -H "Authorization: Bearer ${USER_TOKENS[$email]}" | \
        python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('recharge_balance_quota','?'))" 2>/dev/null)
    echo "BEFORE|$email|$BAL" >> "$RESULTS"
done

# ROUND-ROBIN test: each user → each provider → 10 requests
log ""
log "Running round-robin billing test..."
log "  20 users × $NUM_PROVIDERS providers × $REQUESTS_PER_USER requests = $((20 * NUM_PROVIDERS * REQUESTS_PER_USER)) total"
echo ""

# Collect all user emails into arrays
CONTROL_USERS=()
PREM_USERS=()
for email in "${!USER_KEYS[@]}"; do
    if [[ "$email" == ctrl* ]]; then CONTROL_USERS+=("$email"); else PREM_USERS+=("$email"); fi
done

TOTAL_REQUESTS=0
GOOD=0
BAD=0

for user_email in "${CONTROL_USERS[@]}" "${PREM_USERS[@]}"; do
    user_key="${USER_KEYS[$user_email]}"
    user_label=$(echo "$user_email" | cut -d'@' -f1)
    is_prem=0; [[ "$user_email" == prem* ]] && is_prem=1

    for prov_idx in $(seq 0 $((NUM_PROVIDERS - 1))); do
        provider="${UK_PROV[$prov_idx]}"
        MODEL=$(get_model "$provider")

        for req_num in $(seq 1 $REQUESTS_PER_USER); do
            START=$(date +%s%N)
            RESP=$(curl -s -w "\n%{http_code}" --max-time 60 -X POST "$PROXY/v1/chat/completions" \
                -H "Content-Type: application/json" \
                -H "Authorization: Bearer $user_key" \
                -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Count from 1 to $req_num\"}],\"max_tokens\":20}" 2>/dev/null)
            CODE=$(echo "$RESP" | tail -1)
            BODY=$(echo "$RESP" | sed '$d')
            END=$(date +%s%N)
            DURATION=$(( (END - START) / 1000000 ))

            echo "REQ|$user_label|$provider|$MODEL|req${req_num}|$CODE|${DURATION}ms" >> "$RESULTS"
            TOTAL_REQUESTS=$((TOTAL_REQUESTS + 1))
            if [[ "$CODE" == "200" ]]; then GOOD=$((GOOD+1)); else BAD=$((BAD+1)); fi
            echo -ne "\r  Progress: $TOTAL_REQUESTS requests  OK=$GOOD  FAIL=$BAD"
        done
    done
    echo ""
done
echo ""

sleep 3  # Wait for async billing

# Fetch usage records
log "Fetching usage records..."
echo ""
echo "=========================================="
echo "  Cost Comparison: ratio=1.0 vs ratio=6.0"
echo "=========================================="
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "
SELECT 
    g.name as user_group,
    g.pricing_ratio,
    ur.provider,
    COUNT(*) as calls,
    SUM(ur.input_tokens+ur.output_tokens) as total_tokens,
    SUM(ur.total_cost) as total_cost,
    ROUND(SUM(ur.total_cost)::numeric / NULLIF(SUM(ur.input_tokens+ur.output_tokens),0), 4) as cost_per_token
FROM usage_records ur
JOIN users u ON ur.user_id = u.id
JOIN user_groups g ON u.group_id = g.id
WHERE u.email LIKE '%@billingtest.com'
GROUP BY g.name, g.pricing_ratio, ur.provider
ORDER BY g.pricing_ratio, ur.provider
" 2>/dev/null

echo ""
log "Aggregate cost summary:"
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "
SELECT 
    g.name,
    g.pricing_ratio,
    COUNT(*) as total_calls,
    SUM(ur.total_cost) as total_cost,
    SUM(ur.input_tokens+ur.output_tokens) as total_tokens
FROM usage_records ur
JOIN users u ON ur.user_id = u.id
JOIN user_groups g ON u.group_id = g.id
WHERE u.email LIKE '%@billingtest.com'
GROUP BY g.name, g.pricing_ratio
ORDER BY g.pricing_ratio
" 2>/dev/null

# Cost ratio check
echo ""
C1=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT SUM(total_cost) FROM usage_records ur JOIN users u ON ur.user_id=u.id JOIN user_groups g ON u.group_id=g.id WHERE u.email LIKE '%@billingtest.com' AND g.pricing_ratio=1.0" 2>/dev/null)
C6=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT SUM(total_cost) FROM usage_records ur JOIN users u ON ur.user_id=u.id JOIN user_groups g ON u.group_id=g.id WHERE u.email LIKE '%@billingtest.com' AND g.pricing_ratio=6.0" 2>/dev/null)

C1=${C1:-0}
C6=${C6:-0}

if [[ "$C1" -gt 0 && "$C6" -gt 0 ]]; then
    RATIO=$(echo "scale=4; $C6 / $C1" | bc 2>/dev/null)
    log "Control (ratio=1.0) total cost: $C1"
    log "Premium (ratio=6.0) total cost: $C6"
    log "Cost ratio (premium/control): $RATIO (expected ~6.0)"
    if echo "$RATIO >= 5.5 && $RATIO <= 6.5" | bc -l | grep -q 1; then
        pass "pricing_ratio=6.0 confirmed: cost ratio ≈ $RATIO"
    else
        fail "Cost ratio $RATIO, expected ~6.0"
    fi
fi

# Check balance deductions
log ""
log "Balance deductions (recharge_balance spent):"
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "
SELECT 
    g.name,
    g.pricing_ratio,
    COUNT(u.id) as users,
    SUM(500000 - u.recharge_balance) as total_spent,
    ROUND(AVG(500000 - u.recharge_balance), 2) as avg_spent
FROM users u
JOIN user_groups g ON u.group_id = g.id
WHERE u.email LIKE '%@billingtest.com'
GROUP BY g.name, g.pricing_ratio
" 2>/dev/null

# Write report file
log ""
log "Writing report..."
cat > /tmp/billing-test-report.md << EOF
# Group Billing Ratio Test Report

## Configuration
- Control group: \`default-general\` (pricing_ratio=1.0)
- Premium group: \`premise\` (pricing_ratio=6.0)
- Users: 10 per group
- Upstream providers: ${UK_PROV[*]}
- Requests per user per provider: $REQUESTS_PER_USER
- Total requests: $TOTAL_REQUESTS

## Results
- Successful requests: $GOOD
- Failed requests: $BAD
EOF

log "Report saved to /tmp/billing-test-report.md"
log "Cleanup..."

# Cleanup API keys
for email in "${!USER_KIDS[@]}"; do
    kid="${USER_KIDS[$email]}"
    [[ -n "$kid" ]] && curl -s -X DELETE "$PORTAL/api/apikeys/$kid" -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1
done
# Cleanup users
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
    -c "UPDATE users SET deleted_at=NOW() WHERE email LIKE '%@billingtest.com'" > /dev/null 2>&1

log "Done"

echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  Billing Test Summary${NC}"
echo -e "${CYAN}========================================${NC}"
echo -e "  ${GREEN}Passed:${NC} $PASS"
echo -e "  ${RED}Failed:${NC} $FAIL"
echo -e "  Total:  $((PASS + FAIL))"
echo ""
