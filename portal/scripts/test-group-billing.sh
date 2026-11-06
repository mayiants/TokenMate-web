#!/bin/bash
# ============================================
# User Group Pricing Ratio Billing Test v3
# Tests that group pricing_ratio correctly affects billing:
#   - "default-general" (ratio=1.0) → full price
#   - "test" (ratio=0.9) → 10% discount
# ============================================

set -e

PORTAL="http://127.0.0.1:3001"
PROXY="http://127.0.0.1:8080"
ADMIN_EMAIL="${ADMIN_EMAIL:-e2e-admin@example.com}"
ADMIN_PASS="${ADMIN_PASS:-E2E_AdminPass123!}"
MODEL="MiniMax-M2.5"
REDIS_PASS="${REDIS_PASS:-}"

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; YELLOW='\033[1;33m'; NC='\033[0m'
PASS=0; FAIL=0

log()   { echo -e "${CYAN}[$(date +%H:%M:%S)]${NC} $1"; }
pass()  { echo -e "  ${GREEN}PASS${NC} $1"; PASS=$((PASS+1)); }
fail()  { echo -e "  ${RED}FAIL${NC} $1"; FAIL=$((FAIL+1)); }

GRP_DEFAULT="1213d535-5267-4b64-ad62-1bcd93fd5c64"
GRP_TEST="75c44886-64f7-49a6-bc1d-edc18d55b7d4"
EMAIL1="u1test@example.com"
EMAIL2="u2test@example.com"

# Clear Redis rate limiter
log "Clearing rate limiters..."
docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" KEYS "*brute*" 2>/dev/null | grep -v Warning | while read k; do
    docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "$k" 2>/dev/null > /dev/null
done

# 1. Login as admin
log "Logging in as admin..."
TOKEN=$(curl -s -X POST "$PORTAL/api/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
[[ -z "$TOKEN" ]] && { fail "Admin login failed"; exit 1; }
pass "Admin login"

# Get admin password hash for compatibility
ADMIN_HASH=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT password_hash FROM users WHERE email='admin@example.com'" 2>/dev/null)

# 2. Create/ensure test users
log "Setting up test users..."
for pair in "$EMAIL1|u1|$GRP_DEFAULT" "$EMAIL2|u2|$GRP_TEST"; do
    email=$(echo "$pair" | cut -d'|' -f1)
    name=$(echo "$pair" | cut -d'|' -f2)
    grp=$(echo "$pair" | cut -d'|' -f3)

    # Upsert - insert if not exists, update group and clear deleted_at
    docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
        -c "INSERT INTO users (email, password_hash, name, group_id, role, preferred_currency) VALUES ('$email', '$ADMIN_HASH', '$name', '$grp', 'user', 'USD') ON CONFLICT (email) DO UPDATE SET password_hash = '$ADMIN_HASH', group_id = '$grp', deleted_at = NULL, updated_at = NOW()" > /dev/null 2>&1

    uid=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
        -c "SELECT id FROM users WHERE email='$email'" 2>/dev/null)

    if [[ "$grp" == "$GRP_DEFAULT" ]]; then U1_ID="$uid"; else U2_ID="$uid"; fi
done

# Verify groups
U1_GRP=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT g.name FROM users u JOIN user_groups g ON u.group_id=g.id WHERE u.email='$EMAIL1'" 2>/dev/null)
U2_GRP=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT g.name FROM users u JOIN user_groups g ON u.group_id=g.id WHERE u.email='$EMAIL2'" 2>/dev/null)
U1_RATIO=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT g.pricing_ratio FROM users u JOIN user_groups g ON u.group_id=g.id WHERE u.email='$EMAIL1'" 2>/dev/null)
U2_RATIO=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT g.pricing_ratio FROM users u JOIN user_groups g ON u.group_id=g.id WHERE u.email='$EMAIL2'" 2>/dev/null)

if [[ -n "$U1_ID" ]]; then pass "User $EMAIL1 in group $U1_GRP (ratio=$U1_RATIO)"; else fail "Failed to create $EMAIL1"; fi
if [[ -n "$U2_ID" ]]; then pass "User $EMAIL2 in group $U2_GRP (ratio=$U2_RATIO)"; else fail "Failed to create $EMAIL2"; fi
[[ -z "$U1_ID" || -z "$U2_ID" ]] && exit 1

# 3. Login as each user, create API keys
log "Creating API keys..."
for pair in "$EMAIL1|u1" "$EMAIL2|u2"; do
    email=$(echo "$pair" | cut -d'|' -f1)
    prefix=$(echo "$pair" | cut -d'|' -f2)

    # Clear any rate limit for this user
    docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "auth_brute:172.18.0.1" "auth_brute:::1" "auth_brute:127.0.0.1" 2>/dev/null > /dev/null

    TKN=$(curl -s -X POST "$PORTAL/api/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"$email\",\"password\":\"$ADMIN_PASS\"}" | \
        python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)

    if [[ -z "$TKN" ]]; then
        fail "Login failed for $email"
        continue
    fi

    RESP=$(curl -s -X POST "$PORTAL/api/apikeys" \
        -H "Authorization: Bearer $TKN" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"test-key-$prefix\",\"key_type\":\"general\"}" 2>/dev/null)

    KID=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('id',''))" 2>/dev/null)
    KKEY=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('key',''))" 2>/dev/null)

    if [[ "$prefix" == "u1" ]]; then
        U1_TOKEN="$TKN"; U1_KEY="$KKEY"; U1_KID="$KID"
    else
        U2_TOKEN="$TKN"; U2_KEY="$KKEY"; U2_KID="$KID"
    fi

    if [[ -n "$KID" && "$KID" != "None" ]]; then
        pass "Created API key for $email"
    else
        fail "Failed to create API key for $email"
    fi
done

# 4. Recharge via SQL (no admin recharge API available)
log "Recharging both users with 500000 micro-USD..."
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
    -c "UPDATE users SET recharge_balance = 500000, balance = 500000, updated_at = NOW() WHERE email IN ('$EMAIL1', '$EMAIL2')" > /dev/null
sleep 1

# Check balances
B1=$(curl -s "$PORTAL/api/billing/balance" -H "Authorization: Bearer $U1_TOKEN" | \
    python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('recharge_balance_quota','?'))" 2>/dev/null)
B2=$(curl -s "$PORTAL/api/billing/balance" -H "Authorization: Bearer $U2_TOKEN" | \
    python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('recharge_balance_quota','?'))" 2>/dev/null)
log "  u1 balance: $B1, u2 balance: $B2"

# 5. Make proxy calls
log "Making 5 proxy calls per user..."
for i in $(seq 1 5); do
    for pair_key in "$U1_KEY|u1" "$U2_KEY|u2"; do
        kkey=$(echo "$pair_key" | cut -d'|' -f1)
        CODE=$(curl -s -w "%{http_code}" -o /dev/null --max-time 30 -X POST "$PROXY/v1/chat/completions" \
            -H "Content-Type: application/json" \
            -H "Authorization: Bearer $kkey" \
            -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Count from 1 to $i\"}],\"max_tokens\":20}" 2>/dev/null)
        if [[ "$CODE" == "200" ]]; then echo -ne "${GREEN}.${NC}"; else echo -ne "${RED}x${NC}($CODE)"; fi
    done
done
echo ""
sleep 3

# 6. Check usage records for pricing_ratio
log "Checking usage records..."
for pair in "$EMAIL1|u1" "$EMAIL2|u2"; do
    email=$(echo "$pair" | cut -d'|' -f1)
    label=$(echo "$pair" | cut -d'|' -f2)

    CNT=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
        -c "SELECT COUNT(*) FROM usage_records WHERE user_id IN (SELECT id FROM users WHERE email='$email')" 2>/dev/null)
    COST=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
        -c "SELECT COALESCE(SUM(total_cost),0) FROM usage_records WHERE user_id IN (SELECT id FROM users WHERE email='$email')" 2>/dev/null)
    TOKS=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
        -c "SELECT COALESCE(SUM(input_tokens+output_tokens),0) FROM usage_records WHERE user_id IN (SELECT id FROM users WHERE email='$email')" 2>/dev/null)
    RATIOS=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
        -c "SELECT DISTINCT pricing_ratio_at_request FROM usage_records WHERE user_id IN (SELECT id FROM users WHERE email='$email')" 2>/dev/null)

    log "  $label ($email): ${CNT} records, cost=${COST}, tokens=${TOKS}, ratios=[${RATIOS}]"

    if [[ "$label" == "u1" ]]; then
        U1_CNT=$CNT; U1_COST=$COST; U1_TOKS=$TOKS; U1_RATIOS=$RATIOS
    else
        U2_CNT=$CNT; U2_COST=$COST; U2_TOKS=$TOKS; U2_RATIOS=$RATIOS
    fi
done

# 7. Verify results
if [[ "$U1_CNT" -gt 0 && "$U2_CNT" -gt 0 ]]; then
    pass "Both users have usage records"

    # Check pricing ratios
    if [[ "$U1_RATIOS" == "1.0000" ]]; then pass "u1 pricing_ratio = 1.0000 ✓"; else fail "u1 ratio expected 1.0000, got $U1_RATIOS"; fi
    if [[ "$U2_RATIOS" == "0.9000" ]]; then pass "u2 pricing_ratio = 0.9000 ✓"; else fail "u2 ratio expected 0.9000, got $U2_RATIOS"; fi

    # Compare cost per token
    if [[ "$U1_TOKS" -gt 0 && "$U2_TOKS" -gt 0 ]]; then
        U1_CPT=$(echo "scale=6; $U1_COST / $U1_TOKS" | bc 2>/dev/null)
        U2_CPT=$(echo "scale=6; $U2_COST / $U2_TOKS" | bc 2>/dev/null)
        RATIO=$(echo "scale=4; $U2_CPT / $U1_CPT" | bc 2>/dev/null)

        log "  Cost/token: u1=$U1_CPT, u2=$U2_CPT (ratio=$RATIO, expected ~0.9000)"
        if echo "$RATIO >= 0.85 && $RATIO <= 0.95" | bc -l | grep -q 1; then
            pass "Cost ratio $RATIO ≈ 0.9 — pricing_ratio confirmed applied ✓"
        elif echo "$RATIO > 0" | bc -l | grep -q 1; then
            fail "Cost ratio $RATIO, expected ~0.9000 (check pricing_ratio setting)"
        else
            fail "Could not compute cost ratio"
        fi
    fi
else
    fail "Missing usage records (u1=$U1_CNT, u2=$U2_CNT)"
    # Show what went wrong
    log "  Checking proxy connectivity..."
    echo -n "  Proxy health: "
    curl -s -o /dev/null -w "%{http_code}" "$PROXY/health"
    echo ""
    log "  Checking upstream key availability..."
    docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
        -c "SELECT provider, count(*), validation_status FROM upstream_keys WHERE enabled=true GROUP BY provider, validation_status" 2>/dev/null
fi

# 8. Check balance deductions
B1_AFTER=$(curl -s "$PORTAL/api/billing/balance" -H "Authorization: Bearer $U1_TOKEN" | \
    python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('recharge_balance_quota','?'))" 2>/dev/null)
B2_AFTER=$(curl -s "$PORTAL/api/billing/balance" -H "Authorization: Bearer $U2_TOKEN" | \
    python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('recharge_balance_quota','?'))" 2>/dev/null)

if [[ "$B1" != "?" && "$B2" != "?" ]]; then
    SPENT1=$((500000 - B1_AFTER))
    SPENT2=$((500000 - B2_AFTER))
    log "  u1 spent: $SPENT1, u2 spent: $SPENT2"
    if [[ "$SPENT2" -lt "$SPENT1" ]]; then
        pass "u2 (ratio=0.9) spent LESS than u1 (ratio=1.0) ✓"
    else
        fail "u2 spent $SPENT2 >= u1 spent $SPENT1 (expected u2 < u1)"
    fi
fi

# Summary
echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  Billing Ratio Test Summary${NC}"
echo -e "${CYAN}========================================${NC}"
echo -e "  ${GREEN}Passed:${NC} $PASS"
echo -e "  ${RED}Failed:${NC} $FAIL"
echo -e "  Total:  $((PASS + FAIL))"
echo ""

# Cleanup
log "Cleanup..."
for kid in "$U1_KID" "$U2_KID"; do
    [[ -n "$kid" ]] && curl -s -X DELETE "$PORTAL/api/apikeys/$kid" \
        -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1
done
# Soft delete test users
for email in "$EMAIL1" "$EMAIL2"; do
    docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
        -c "UPDATE users SET deleted_at = NOW(), updated_at = NOW() WHERE email = '$email'" > /dev/null 2>&1
done
log "Done"

exit $FAIL
