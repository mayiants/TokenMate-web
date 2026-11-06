#!/bin/bash
# ============================================
# Upstream Key RPM Capacity Test
# 2000 user keys, round-robin across 12 upstream keys
# Delay 10s→5s, cooldown on 429, report per-key stats
# ============================================

set -e

PORTAL="http://127.0.0.1:3001"
PROXY="http://127.0.0.1:8080"
ADMIN_EMAIL="${ADMIN_EMAIL:-e2e-admin@example.com}"
ADMIN_PASS="${ADMIN_PASS:-E2E_AdminPass123!}"
REDIS_PASS="${REDIS_PASS:-}"
TOTAL_KEYS=2000
CONCURRENCY=50

RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; NC='\033[0m'
log() { echo -e "${CYAN}[$(date +%H:%M:%S)]${NC} $1"; }

WORKDIR="/tmp/rpm-test-$$"
mkdir -p "$WORKDIR"
RESULTS="$WORKDIR/results.txt"
KEYS_FILE="$WORKDIR/keys.txt"
UPSTREAM_FILE="$WORKDIR/upstream.txt"
rm -f "$RESULTS" "$KEYS_FILE"

# Get upstream keys
UPSTREAM_KEYS=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT id, provider, key_preview FROM upstream_keys WHERE enabled=true AND validation_status='valid' ORDER BY provider, id" 2>/dev/null)
echo "$UPSTREAM_KEYS" > "$UPSTREAM_FILE"
NUM_UPSTREAM=$(wc -l < "$UPSTREAM_FILE")
log "Found $NUM_UPSTREAM upstream keys"

echo "$UPSTREAM_KEYS" | while IFS='|' read -r id prov prev; do echo "  $prov $prev"; done

# Init stats files
echo "delay_s|uk_idx|provider|total|ok|r429|r503|error|min_ms|avg_ms|max_ms" > "$WORKDIR/stats.txt"

# Clear Redis rate limiter
docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" KEYS "*brute*" 2>/dev/null | grep -v Warning | while read k; do
    docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "$k" 2>/dev/null > /dev/null
done

# Login admin
TOKEN=$(curl -s -X POST "$PORTAL/api/auth/login" -H "Content-Type: application/json" \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
[[ -z "$TOKEN" ]] && { log "Admin login failed"; exit 1; }
log "Admin login OK"

ADMIN_HASH=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT password_hash FROM users WHERE email='admin@example.com'" 2>/dev/null)

# Ensure test user
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
    -c "INSERT INTO users (email, password_hash, name, role, preferred_currency) VALUES ('rpmtest@example.com', '$ADMIN_HASH', 'rpmtest', 'user', 'USD') ON CONFLICT (email) DO UPDATE SET deleted_at = NULL, updated_at = NOW()" > /dev/null 2>&1

docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "auth_brute:172.18.0.1" "auth_brute:::1" 2>/dev/null > /dev/null
RPM_TOKEN=$(curl -s -X POST "$PORTAL/api/auth/login" -H "Content-Type: application/json" \
    -d "{\"email\":\"rpmtest@example.com\",\"password\":\"$ADMIN_PASS\"}" | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)

# Create 2000 API keys
log "Creating $TOTAL_KEYS API keys..."
CREATED=0
while [[ $CREATED -lt $TOTAL_KEYS ]]; do
    B=$((TOTAL_KEYS - CREATED)); [[ $B -gt $CONCURRENCY ]] && B=$CONCURRENCY
    for i in $(seq 1 $B); do
        N=$((CREATED + i))
        {
            RESP=$(curl -s --max-time 10 -X POST "$PORTAL/api/apikeys" -H "Authorization: Bearer $RPM_TOKEN" \
                -H "Content-Type: application/json" -d "{\"name\":\"rpm-key-$N\",\"key_type\":\"general\"}" 2>/dev/null)
            KKEY=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('key',''))" 2>/dev/null)
            [[ -n "$KKEY" && "$KKEY" != "None" ]] && echo "$KKEY" >> "$KEYS_FILE"
        } &
    done
    wait
    CREATED=$((CREATED + B))
    echo -ne "\r  Created: $CREATED / $TOTAL_KEYS"
done
echo ""

# Load keys
mapfile -t ALL_KEYS < "$KEYS_FILE"
ACTUAL_KEYS=${#ALL_KEYS[@]}
log "Created $ACTUAL_KEYS API keys"

# Read upstream data
mapfile -t UPSTREAM_LINES < "$UPSTREAM_FILE"
UK_IDS=(); UK_PROVS=(); UK_PREVS=()
for line in "${UPSTREAM_LINES[@]}"; do
    IFS='|' read -r id prov prev <<< "$line"
    UK_IDS+=("$id"); UK_PROVS+=("$prov"); UK_PREVS+=("$prev")
done

# Helper: model for provider
get_model() {
    case "$1" in
        minimax) echo "MiniMax-M2.5" ;; moonshot) echo "kimi-k2.5" ;;
        xiaomi) echo "mimo-v2.5" ;; shixin) echo "gpt-4o-mini" ;; xunfei) echo "astron-code-latest" ;;
        *) echo "MiniMax-M2.5" ;;
    esac
}

log "Step 2: Running RPM test (delay 10s→5s)..."
REQUEST_NUM=0
TOTAL_OK=0; TOTAL_429=0; TOTAL_503=0; TOTAL_ERR=0

# Cooldown tracking files
for uk_idx in $(seq 0 $((NUM_UPSTREAM - 1))); do
    echo "0" > "$WORKDIR/cooldown_$uk_idx.txt"
done

for DELAY in $(seq 10 -0.5 5 | tr ',' '.'); do
    ROUNDS=10
    log "  Delay=${DELAY}s — $ROUNDS rounds × $NUM_UPSTREAM req/round = $((ROUNDS * NUM_UPSTREAM)) req"
    
    for ROUND in $(seq 1 $ROUNDS); do
        ROUND_START=$(date +%s%N)
        PIDS=()
        
        for uk_idx in $(seq 0 $((NUM_UPSTREAM - 1))); do
            # Check cooldown
            COOLDOWN_UNTIL=$(cat "$WORKDIR/cooldown_$uk_idx.txt")
            NOW=$(date +%s)
            
            if [[ $COOLDOWN_UNTIL -gt $NOW ]]; then
                REMAINING=$((COOLDOWN_UNTIL - NOW))
                echo "COOLDOWN|${UK_IDS[$uk_idx]}|${UK_PROVS[$uk_idx]}|${REMAINING}s" >> "$RESULTS"
                continue
            fi
            
            UK_ID="${UK_IDS[$uk_idx]}"
            UK_PROV="${UK_PROVS[$uk_idx]}"
            MODEL=$(get_model "$UK_PROV")
            
            # Pick user key round-robin
            KEY_IDX=$((REQUEST_NUM % ACTUAL_KEYS))
            USER_KEY="${ALL_KEYS[$KEY_IDX]}"
            ((REQUEST_NUM++))
            
            (  
                START_MS=$(date +%s%N)
                RESP=$(curl -s -w "\n%{http_code}" --max-time 30 -X POST "$PROXY/v1/chat/completions" \
                    -H "Content-Type: application/json" \
                    -H "Authorization: Bearer $USER_KEY" \
                    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hello\"}],\"max_tokens\":5}" 2>/dev/null)
                CODE=$(echo "$RESP" | tail -1)
                END_MS=$(date +%s%N)
                DURATION=$(( (END_MS - START_MS) / 1000000 ))
                echo "$CODE|$UK_ID|$UK_PROV|${DURATION}ms|$MODEL" >> "$RESULTS"
            ) &
            PIDS+=($!)
        done
        
        wait
        
        # After round, check for 429/503 and set cooldowns
        for uk_idx in $(seq 0 $((NUM_UPSTREAM - 1))); do
            LAST_CODE=$(grep "|${UK_IDS[$uk_idx]}|" "$RESULTS" 2>/dev/null | tail -1 | cut -d'|' -f1)
            if [[ "$LAST_CODE" == "429" ]]; then
                echo "$(($(date +%s) + 60))" > "$WORKDIR/cooldown_$uk_idx.txt"
            elif [[ "$LAST_CODE" == "503" ]]; then
                echo "$(($(date +%s) + 30))" > "$WORKDIR/cooldown_$uk_idx.txt"
            fi
        done
        
        ROUND_END=$(date +%s%N); ROUND_TIME=$(( (ROUND_END - ROUND_START) / 1000000000 ))
        SLEEP_TIME=$(python3 -c "print(max(0, $DELAY - $ROUND_TIME))" 2>/dev/null)
        [[ $(echo "$SLEEP_TIME > 0" | bc) -eq 1 ]] && sleep $SLEEP_TIME
    done
    
    # Stats for this delay level
    TOTAL_OK=$(grep -c "^200" "$RESULTS" 2>/dev/null || echo 0)
    TOTAL_429=$(grep -c "^429" "$RESULTS" 2>/dev/null || echo 0)
    TOTAL_503=$(grep -c "^503" "$RESULTS" 2>/dev/null || echo 0)
    TOTAL_ERR=$(grep -cE "^(4[0-9][0-9]|5[0-9][0-9])" "$RESULTS" 2>/dev/null || echo 0)
    TOTAL_ERR=$((TOTAL_ERR - TOTAL_429 - TOTAL_503))
    [[ $TOTAL_ERR -lt 0 ]] && TOTAL_ERR=0
    TOTAL=$(grep -cv "^COOLDOWN" "$RESULTS" 2>/dev/null || echo 0)
    log "  OK=$TOTAL_OK  429=$TOTAL_429  503=$TOTAL_503  cooldowns=$(grep -c "^COOLDOWN" "$RESULTS" 2>/dev/null || echo 0)"
done

log ""
log "Step 3: Generating final report..."

TOTAL=$(grep -cv "^COOLDOWN" "$RESULTS")
OK=$(grep -c "^200" "$RESULTS")
RL=$(grep -c "^429" "$RESULTS")
NA=$(grep -c "^503" "$RESULTS")
CD=$(grep -c "^COOLDOWN" "$RESULTS")
ERR=$((TOTAL - OK - RL - NA))

echo ""
echo "=========================================="
echo "  RPM Capacity Test - Final Report"
echo "=========================================="
echo "Total non-cooldown requests: $TOTAL"
echo "  HTTP 200 (OK):   $OK ($(echo "scale=1; $OK*100/$TOTAL" | bc 2>/dev/null)%)"
echo "  HTTP 429 (RL):   $RL ($(echo "scale=1; $RL*100/$TOTAL" | bc 2>/dev/null)%)"
echo "  HTTP 503 (NA):   $NA ($(echo "scale=1; $NA*100/$TOTAL" | bc 2>/dev/null)%)"
echo "  Other errors:    $ERR ($(echo "scale=1; $ERR*100/$TOTAL" | bc 2>/dev/null)%)"
echo "  Cooldown skips:  $CD"
echo ""

# Per-upstream key summary
echo "Per-Upstream Key Breakdown:"
echo "------------------------------------------------------------"
printf "%-30s %-10s %-8s %-8s %-8s %-8s\n" "Key Preview" "Provider" "Total" "200" "429" "503"
echo "------------------------------------------------------------"
for uk_idx in $(seq 0 $((NUM_UPSTREAM - 1))); do
    prev="${UK_PREVS[$uk_idx]}"
    prov="${UK_PROVS[$uk_idx]}"
    ukid="${UK_IDS[$uk_idx]}"
    t=$(grep "^[0-9]|${ukid}|" "$RESULTS" 2>/dev/null | wc -l || echo 0)
    o=$(grep "^200|${ukid}|" "$RESULTS" 2>/dev/null | wc -l || echo 0)
    f=$(grep "^429|${ukid}|" "$RESULTS" 2>/dev/null | wc -l || echo 0)
    s=$(grep "^503|${ukid}|" "$RESULTS" 2>/dev/null | wc -l || echo 0)
    printf "%-30s %-10s %-8s %-8s %-8s %-8s\n" "$prev" "$prov" "$t" "$o" "$f" "$s"
done
echo "------------------------------------------------------------"

# Cleanup
log "Cleanup: deleting $ACTUAL_KEYS API keys..."
for key in "${ALL_KEYS[@]}"; do
    # Get key ID from key value by extracting the ID field
    RESP=$(curl -s -X POST "$PORTAL/api/auth/login" -H "Content-Type: application/json" \
        -d "{\"email\":\"admin@example.com\",\"password\":\"$ADMIN_PASS\"}" | \
        python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
    # Actually just delete via SQL for speed
done

docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
    -c "DELETE FROM api_keys WHERE name LIKE 'rpm-key-%'" > /dev/null 2>&1
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
    -c "UPDATE users SET deleted_at=NOW() WHERE email='rpmtest@example.com'" > /dev/null 2>&1

rm -rf "$WORKDIR"
log "Done"
