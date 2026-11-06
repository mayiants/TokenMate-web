#!/bin/bash
# Fast billing test - 2 users per group, 2 req/provider
PORTAL="http://127.0.0.1:3001"
PROXY="http://127.0.0.1:8080"
ADMIN_EMAIL="${ADMIN_EMAIL:-e2e-admin@example.com}"
ADMIN_PASS="${ADMIN_PASS:-E2E_AdminPass123!}"
REDIS_PASS="${REDIS_PASS:-}"
GRP_DEFAULT="1213d535-5267-4b64-ad62-1bcd93fd5c64"
GRP_PREMIUM="0d553315-b0f7-438d-9560-4fa851c48297"

# Clear rate limiter
docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" KEYS "*brute*" 2>/dev/null | grep -v Warning | while read k; do docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "$k" 2>/dev/null > /dev/null; done

TOKEN=$(curl -s -X POST "$PORTAL/api/auth/login" -H "Content-Type: application/json" \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" | jq -r '.token // empty')
echo "admin_token=$TOKEN"

ADMIN_HASH=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT password_hash FROM users WHERE email='admin@example.com'")

get_model() {
    case "$1" in minimax) echo "MiniMax-M2.5" ;; moonshot) echo "kimi-k2.5" ;; xiaomi) echo "mimo-v2.5" ;; shixin) echo "gpt-4o-mini" ;; xunfei) echo "astron-code-latest" ;; esac
}

PROVIDERS=(minimax moonshot shixin xiaomi xunfei)

# Create 2 users per group (total 4), each with API key
echo "=== Creating test users ==="
for pair in "bctrl1@example.com|$GRP_DEFAULT|ctrl1" "bctrl2@example.com|$GRP_DEFAULT|ctrl2" "bprem1@example.com|$GRP_PREMIUM|prem1" "bprem2@example.com|$GRP_PREMIUM|prem2"; do
    email=$(echo $pair|cut -d'|' -f1); grp=$(echo $pair|cut -d'|' -f2); name=$(echo $pair|cut -d'|' -f3)
    docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
        -c "INSERT INTO users (email,password_hash,name,group_id,role,preferred_currency) VALUES ('$email','$ADMIN_HASH','$name','$grp','user','USD') ON CONFLICT (email) DO UPDATE SET group_id='$grp',deleted_at=NULL,password_hash='$ADMIN_HASH'" > /dev/null 2>&1
    docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "auth_brute:172.18.0.1" > /dev/null 2>&1
    sleep 0.5
    TKN=$(curl -s -X POST "$PORTAL/api/auth/login" -H "Content-Type: application/json" \
        -d "{\"email\":\"$email\",\"password\":\"$ADMIN_PASS\"}" | jq -r '.token // empty')
    if [[ -z "$TKN" ]]; then echo "LOGIN FAIL $email"; continue; fi
    RESP=$(curl -s -X POST "$PORTAL/api/apikeys" -H "Authorization: Bearer $TKN" \
        -H "Content-Type: application/json" -d "{\"name\":\"bt-$name\",\"key_type\":\"general\"}")
    KEY=$(echo "$RESP" | jq -r '.key // empty')
    KID=$(echo "$RESP" | jq -r '.id // empty')
    eval "${name}_KEY=\"$KEY\""; eval "${name}_KID=\"$KID\""
    # Recharge
    uid=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A -c "SELECT id FROM users WHERE email='$email'")
    docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "UPDATE users SET recharge_balance=500000,balance=500000 WHERE id='$uid'" > /dev/null
    echo "  $name OK"
done

sleep 2

# Run tests: each user → each provider → 2 requests
echo "=== Running tests ==="
RESULTS="/tmp/bt-results.txt"; > "$RESULTS"
for name in ctrl1 ctrl2 prem1 prem2; do
    KEY_VAR="${name}_KEY"
    KEY="${!KEY_VAR}"
    [[ -z "$KEY" ]] && continue
    grp="ctrl"; [[ "$name" == prem* ]] && grp="prem"
    for prov in "${PROVIDERS[@]}"; do
        model=$(get_model "$prov")
        for n in 1 2; do
            START=$(date +%s%N)
            CODE=$(curl -s -w "%{http_code}" -o /tmp/bt-body.json --max-time 30 -X POST "$PROXY/v1/chat/completions" \
                -H "Content-Type: application/json" -H "Authorization: Bearer $KEY" \
                -d "{\"model\":\"$model\",\"messages\":[{\"role\":\"user\",\"content\":\"Say $n\"}],\"max_tokens\":5}")
            END=$(date +%s%N); DUR=$(( (END-START)/1000000 ))
            echo "REQ|$name|$grp|$prov|$model|req$n|$CODE|${DUR}ms" >> "$RESULTS"
            echo -n "."
            sleep 0.5
        done
    done
    echo " $name done"
done

# Wait for billing
sleep 3

# Get usage records
echo "=== Usage Records ==="
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "
SELECT g.name, g.pricing_ratio, ur.provider,
  COUNT(*) calls, SUM(ur.total_cost) cost,
  SUM(ur.input_tokens+ur.output_tokens) tokens
FROM usage_records ur
JOIN users u ON ur.user_id=u.id
JOIN user_groups g ON u.group_id=g.id
WHERE u.email LIKE '%@example.com'
GROUP BY g.name, g.pricing_ratio, ur.provider
ORDER BY g.pricing_ratio, ur.provider
" 2>/dev/null

echo ""
echo "=== Aggregate ==="
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "
SELECT g.name, g.pricing_ratio,
  SUM(ur.total_cost) total_cost,
  SUM(ur.input_tokens+ur.output_tokens) total_tokens
FROM usage_records ur
JOIN users u ON ur.user_id=u.id
JOIN user_groups g ON u.group_id=g.id
WHERE u.email LIKE '%@example.com'
GROUP BY g.name, g.pricing_ratio
ORDER BY g.pricing_ratio
" 2>/dev/null

echo ""
echo "=== Balance deductions ==="
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "
SELECT g.name, g.pricing_ratio,
  COUNT(u.id) users,
  SUM(500000 - u.recharge_balance) total_spent
FROM users u
JOIN user_groups g ON u.group_id=g.id
WHERE u.email LIKE '%@example.com'
GROUP BY g.name, g.pricing_ratio
" 2>/dev/null

echo ""
echo "=== Results ==="
cat "$RESULTS"

# Cleanup
for name in ctrl1 ctrl2 prem1 prem2; do
    KID_VAR="${name}_KID"; KID="${!KID_VAR}"
    [[ -n "$KID" ]] && curl -s -X DELETE "$PORTAL/api/apikeys/$KID" -H "Authorization: Bearer $TOKEN" > /dev/null
done
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -c "UPDATE users SET deleted_at=NOW() WHERE email LIKE '%@example.com'" > /dev/null
