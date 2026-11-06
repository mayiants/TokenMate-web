#!/bin/bash
# ============================================
# Multi-Provider Billing Ratio Test
# Tests pricing_ratio across ALL providers
#   - u1 (group: default-general, ratio=1.0)
#   - u2 (group: test, ratio=0.9)
# ============================================

set -e

PORTAL="http://127.0.0.1:3001"
PROXY="http://127.0.0.1:8080"
ADMIN_EMAIL="${ADMIN_EMAIL:-e2e-admin@example.com}"
ADMIN_PASS="${ADMIN_PASS:-E2E_AdminPass123!}"
REDIS_PASS="${REDIS_PASS:-}"

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; YELLOW='\033[1;33m'; NC='\033[0m'
PASS=0; FAIL=0

log()   { echo -e "${CYAN}[$(date +%H:%M:%S)]${NC} $1"; }
pass()  { echo -e "  ${GREEN}PASS${NC} $1"; PASS=$((PASS+1)); }
fail()  { echo -e "  ${RED}FAIL${NC} $1"; FAIL=$((FAIL+1)); }

GRP_DEFAULT="1213d535-5267-4b64-ad62-1bcd93fd5c64"
GRP_TEST="75c44886-64f7-49a6-bc1d-edc18d55b7d4"
EMAIL1="u1multi@example.com"
EMAIL2="u2multi@example.com"

# Models to test (one per provider)
declare -A MODELS
MODELS[minimax]="MiniMax-M2.5"
MODELS[moonshot]="kimi-k2.5"
MODELS[xiaomi]="mimo-v2.5"
MODELS[shixin]="gpt-4o-mini"
MODELS[xunfei]="astron-code-latest"

# Clear Redis rate limiter
docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" KEYS "*brute*" 2>/dev/null | grep -v Warning | while read k; do
    docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "$k" 2>/dev/null > /dev/null
done

# Login as admin
log "Logging in as admin..."
TOKEN=$(curl -s -X POST "$PORTAL/api/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
[[ -z "$TOKEN" ]] && { fail "Admin login failed"; exit 1; }
pass "Admin login"

ADMIN_HASH=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT password_hash FROM users WHERE email='admin@example.com'" 2>/dev/null)

# Create/ensure test users
log "Setting up test users..."
for pair in "$EMAIL1|u1|$GRP_DEFAULT" "$EMAIL2|u2|$GRP_TEST"; do
    email=$(echo "$pair" | cut -d'|' -f1)
    name=$(echo "$pair" | cut -d'|' -f2)
    grp=$(echo "$pair" | cut -d'|' -f3)
    docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
        -c "INSERT INTO users (email, password_hash, name, group_id, role, preferred_currency) VALUES ('$email', '$ADMIN_HASH', '$name', '$grp', 'user', 'USD') ON CONFLICT (email) DO UPDATE SET group_id = '$grp', password_hash = '$ADMIN_HASH', deleted_at = NULL, updated_at = NOW()" > /dev/null 2>&1
done

# Login as u1, u2 and create API keys
U1_TOKEN="" U2_TOKEN=""
U1_KEY="" U2_KEY=""
U1_KID="" U2_KID=""
for pair in "$EMAIL1|u1" "$EMAIL2|u2"; do
    email=$(echo "$pair" | cut -d'|' -f1); prefix=$(echo "$pair" | cut -d'|' -f2)
    docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "auth_brute:172.18.0.1" "auth_brute:::1" "auth_brute:127.0.0.1" 2>/dev/null > /dev/null
    TKN=$(curl -s -X POST "$PORTAL/api/auth/login" \
        -H "Content-Type: application/json" \
        -d "{\"email\":\"$email\",\"password\":\"$ADMIN_PASS\"}" | \
        python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
    RESP=$(curl -s -X POST "$PORTAL/api/apikeys" \
        -H "Authorization: Bearer $TKN" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"multi-test-$prefix\",\"key_type\":\"general\"}" 2>/dev/null)
    KID=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
    KKEY=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('key',''))" 2>/dev/null)
    if [[ "$prefix" == "u1" ]]; then U1_TOKEN="$TKN"; U1_KEY="$KKEY"; U1_KID="$KID"; else U2_TOKEN="$TKN"; U2_KEY="$KKEY"; U2_KID="$KID"; fi
    [[ -n "$KID" && "$KID" != "None" ]] && pass "Created API key for $email" || fail "Failed API key for $email"
done

# Recharge
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
    -c "UPDATE users SET recharge_balance = 500000, balance = 500000, updated_at = NOW() WHERE email IN ('$EMAIL1', '$EMAIL2')" > /dev/null
sleep 1

# Test each provider/model
log ""
log "=========================================="
log "  Testing ALL providers"
log "=========================================="

U1_TOTAL_COST=0
U2_TOTAL_COST=0
U1_TOTAL_TOKS=0
U2_TOTAL_TOKS=0

for PROVIDER in minimax moonshot xiaomi shixin xunfei; do
    MODEL="${MODELS[$PROVIDER]}"
    log ""
    log "--- Provider: $PROVIDER (model: $MODEL) ---"
    
    # u1 call
    START1=$(date +%s%N)
    R1=$(curl -s -w "\n%{http_code}" --max-time 60 -X POST "$PROXY/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $U1_KEY" \
        -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hello\"}],\"max_tokens\":10}" 2>/dev/null)
    C1=$(echo "$R1" | tail -1)
    END1=$(date +%s%N)
    D1=$(( (END1 - START1) / 1000000 ))
    B1=$(echo "$R1" | sed '$d')
    
    # u2 call
    START2=$(date +%s%N)
    R2=$(curl -s -w "\n%{http_code}" --max-time 60 -X POST "$PROXY/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $U2_KEY" \
        -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hello\"}],\"max_tokens\":10}" 2>/dev/null)
    C2=$(echo "$R2" | tail -1)
    END2=$(date +%s%N)
    D2=$(( (END2 - START2) / 1000000 ))
    B2=$(echo "$R2" | sed '$d')
    
    echo -n "  u1 (ratio=1.0): "
    if [[ "$C1" == "200" ]]; then echo -e "${GREEN}HTTP $C1${NC} (${D1}ms)"; else echo -e "${RED}HTTP $C1${NC}"; fi
    
    echo -n "  u2 (ratio=0.9): "
    if [[ "$C2" == "200" ]]; then echo -e "${GREEN}HTTP $C2${NC} (${D2}ms)"; else echo -e "${RED}HTTP $C2${NC}"; fi
    
    if [[ "$C1" != "200" ]]; then echo "  u1 error: $(echo "$B1" | head -c 150)"; fi
    if [[ "$C2" != "200" ]]; then echo "  u2 error: $(echo "$B2" | head -c 150)"; fi
    
    sleep 2
done

# Fetch usage records for all provider tests
log ""
log "=========================================="
log "  Usage Records Summary"
log "=========================================="

for pair in "$EMAIL1|u1" "$EMAIL2|u2"; do
    email=$(echo "$pair" | cut -d'|' -f1); label=$(echo "$pair" | cut -d'|' -f2)
    echo ""
    log "User $label ($email):"
    docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "
        SELECT ur.provider, ur.model, COUNT(*) as calls, 
               SUM(ur.input_tokens+ur.output_tokens) as total_tokens,
               SUM(ur.total_cost) as total_cost,
               ur.pricing_ratio_at_request
        FROM usage_records ur 
        WHERE ur.user_id IN (SELECT id FROM users WHERE email='$email')
        GROUP BY ur.provider, ur.model, ur.pricing_ratio_at_request
        ORDER BY ur.provider
    " 2>/dev/null
done

# Cost comparison
log ""
log "=========================================="
log "  Cost Comparison (ratio=1.0 vs 0.9)"
log "=========================================="
echo ""

docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "
SELECT 
    ur.provider,
    ur.model,
    SUM(CASE WHEN u.email='$EMAIL1' THEN ur.total_cost ELSE 0 END) as u1_cost,
    SUM(CASE WHEN u.email='$EMAIL2' THEN ur.total_cost ELSE 0 END) as u2_cost,
    SUM(CASE WHEN u.email='$EMAIL2' THEN ur.total_cost ELSE 0 END)::float / 
      NULLIF(SUM(CASE WHEN u.email='$EMAIL1' THEN ur.total_cost ELSE 0 END), 0) as actual_ratio
FROM usage_records ur
JOIN users u ON ur.user_id = u.id
WHERE u.email IN ('$EMAIL1', '$EMAIL2')
GROUP BY ur.provider, ur.model
ORDER BY ur.provider
" 2>/dev/null

# Check if ratio is correctly ~0.9 for each provider
log "Verifying pricing_ratio across providers..."
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A -c "
SELECT ur.provider,
    SUM(CASE WHEN u.email='$EMAIL1' THEN ur.total_cost ELSE 0 END) as c1,
    SUM(CASE WHEN u.email='$EMAIL2' THEN ur.total_cost ELSE 0 END) as c2
FROM usage_records ur
JOIN users u ON ur.user_id = u.id
WHERE u.email IN ('$EMAIL1', '$EMAIL2')
GROUP BY ur.provider
ORDER BY ur.provider
" 2>/dev/null | while IFS='|' read -r prov c1 c2; do
    if [[ "$c1" -gt 0 && "$c2" -gt 0 ]]; then
        RATIO=$(echo "scale=4; $c2 / $c1" | bc 2>/dev/null)
        if echo "$RATIO >= 0.85 && $RATIO <= 0.95" | bc -l | grep -q 1; then
            pass "$prov: cost ratio = $RATIO (~0.9 ✓)"
        elif echo "$RATIO > 0" | bc -l | grep -q 1; then
            fail "$prov: cost ratio = $RATIO (expected ~0.9)"
        fi
    elif [[ "$c1" -eq 0 ]]; then
        fail "$prov: no usage records for u1"
    elif [[ "$c2" -eq 0 ]]; then
        fail "$prov: no usage records for u2"
    fi
done

# Summary
echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  Multi-Provider Test Summary${NC}"
echo -e "${CYAN}========================================${NC}"
echo -e "  ${GREEN}Passed:${NC} $PASS"
echo -e "  ${RED}Failed:${NC} $FAIL"
echo -e "  Total:  $((PASS + FAIL))"
echo ""

# Cleanup
log "Cleanup..."
for kid in "$U1_KID" "$U2_KID"; do
    [[ -n "$kid" ]] && curl -s -X DELETE "$PORTAL/api/apikeys/$kid" -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1
done
for email in "$EMAIL1" "$EMAIL2"; do
    docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
        -c "UPDATE users SET deleted_at = NOW(), updated_at = NOW() WHERE email = '$email'" > /dev/null 2>&1
done
log "Done"
