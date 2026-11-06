#!/bin/bash
# ============================================
# Batch 2000 API Key Generation + Proxy Test
# Usage: bash scripts/batch-key-test.sh
# ============================================

set -e

PORTAL="http://127.0.0.1:3001"
PROXY="http://127.0.0.1:8080"
ADMIN_EMAIL="${ADMIN_EMAIL:-e2e-admin@example.com}"
ADMIN_PASS="${ADMIN_PASS:-E2E_AdminPass123!}"
MODEL="MiniMax-M2.5"
TOTAL_KEYS=2000
CONCURRENCY=50
RESULTS_FILE="/tmp/batch-key-test-results-$$.txt"
KEYS_FILE="/tmp/batch-key-test-keys-$$.txt"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
PASS=0; FAIL=0

rm -f "$RESULTS_FILE" "$KEYS_FILE"
log() { echo -e "${CYAN}[$(date +%H:%M:%S)]${NC} $1"; }
cleanup() {
    log "Deleting ${TOTAL_KEYS} test keys..."
    while IFS='|' read -r kid _; do
        curl -s -X DELETE "$PORTAL/api/apikeys/$kid" \
            -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1
    done < "$KEYS_FILE"
    rm -f "$RESULTS_FILE" "$KEYS_FILE"
}
trap cleanup EXIT

# 1. Login
log "Logging in as admin..."
TOKEN=$(curl -s -X POST "$PORTAL/api/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
[[ -z "$TOKEN" ]] && { echo -e "${RED}Login failed${NC}"; exit 1; }
log "Login OK"

# 2. Create keys (in parallel batches)
log "Creating $TOTAL_KEYS API keys..."
CREATED=0
BATCH=0
while [[ $CREATED -lt $TOTAL_KEYS ]]; do
    THIS_BATCH=$((TOTAL_KEYS - CREATED))
    [[ $THIS_BATCH -gt $CONCURRENCY ]] && THIS_BATCH=$CONCURRENCY

    for i in $(seq 1 $THIS_BATCH); do
        N=$((CREATED + i))
        {
            RESP=$(curl -s --max-time 10 -X POST "$PORTAL/api/apikeys" \
                -H "Authorization: Bearer $TOKEN" \
                -H "Content-Type: application/json" \
                -d "{\"name\":\"batch-test-$N\",\"key_type\":\"general\"}" 2>/dev/null)
            KID=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('id',''))" 2>/dev/null)
            KKEY=$(echo "$RESP" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('key',''))" 2>/dev/null)
            if [[ -n "$KID" && "$KID" != "None" ]]; then
                echo "$KID|$KKEY" >> "$KEYS_FILE"
            else
                echo "|" >> "$KEYS_FILE"
            fi
        } &
    done
    wait

    CREATED=$((CREATED + THIS_BATCH))
    BATCH=$((BATCH+1))
    KEY_COUNT=$(wc -l < "$KEYS_FILE" 2>/dev/null || echo 0)
    echo -ne "\r  Created: $KEY_COUNT / $TOTAL_KEYS    "
done
echo ""

ACTUAL_KEYS=$(wc -l < "$KEYS_FILE")
log "Created $ACTUAL_KEYS keys successfully"

# 3. Test each key against proxy (in parallel batches)
log "Testing $ACTUAL_KEYS keys against proxy ($MODEL)..."
TESTED=0
BATCH=0
while [[ $TESTED -lt $ACTUAL_KEYS ]]; do
    THIS_BATCH=$((ACTUAL_KEYS - TESTED))
    [[ $THIS_BATCH -gt $CONCURRENCY ]] && THIS_BATCH=$CONCURRENCY

    # Read next THIS_BATCH lines
    KEYS=()
    KIDS=()
    while IFS='|' read -r kid kkey; do
        KEYS+=("$kkey")
        KIDS+=("$kid")
    done < <(tail -n +$((TESTED+1)) "$KEYS_FILE" | head -$THIS_BATCH)

    for kkey in "${KEYS[@]}"; do
        {
            if [[ -z "$kkey" ]]; then
                echo "FAIL|no_key" >> "$RESULTS_FILE"
            else
                START=$(date +%s%N)
                RESP=$(curl -s -w "\n%{http_code}" --max-time 30 -X POST "$PROXY/v1/chat/completions" \
                    -H "Content-Type: application/json" \
                    -H "Authorization: Bearer $kkey" \
                    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hello in one word\"}],\"max_tokens\":5}" 2>/dev/null)
                CODE=$(echo "$RESP" | tail -1)
                BODY=$(echo "$RESP" | sed '$d')
                END=$(date +%s%N)
                DURATION=$(( (END - START) / 1000000 ))
                if [[ "$CODE" == "200" ]]; then
                    echo "PASS|${DURATION}ms" >> "$RESULTS_FILE"
                else
                    MSG=$(echo "$BODY" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('error',{}).get('message',''))" 2>/dev/null | head -c 80)
                    echo "FAIL|HTTP $CODE $MSG" >> "$RESULTS_FILE"
                fi
            fi
        } &
    done
    wait

    TESTED=$((TESTED + THIS_BATCH))
    BATCH=$((BATCH+1))
    # Count results so far
    TOTAL_RESULTS=$(wc -l < "$RESULTS_FILE" 2>/dev/null || echo 0)
    PASSES=$(grep -c "^PASS" "$RESULTS_FILE" 2>/dev/null || echo 0)
    FAILS=$(grep -c "^FAIL" "$RESULTS_FILE" 2>/dev/null || echo 0)
    echo -ne "\r  Tested: $TOTAL_RESULTS / $ACTUAL_KEYS  Pass: $PASSES  Fail: $FAILS    "
done
echo ""

# 4. Summary
TOTAL_RESULTS=$(wc -l < "$RESULTS_FILE")
PASSES=$(grep -c "^PASS" "$RESULTS_FILE" 2>/dev/null || echo 0)
FAILS=$(grep -c "^FAIL" "$RESULTS_FILE" 2>/dev/null || echo 0)

echo ""
echo -e "${CYAN}========================================${NC}"
echo -e "${CYAN}  Test Summary${NC}"
echo -e "${CYAN}========================================${NC}"
echo -e "  ${GREEN}Pass:${NC}  $PASSES"
echo -e "  ${RED}Fail:${NC}  $FAILS"
echo -e "  Total: $TOTAL_RESULTS"

if [[ $FAILS -gt 0 ]]; then
    echo ""
    echo -e "${RED}Failed details (first 10):${NC}"
    grep "^FAIL" "$RESULTS_FILE" | head -10 | while IFS='|' read -r status detail; do
        echo -e "  ${RED}✗${NC} $detail"
    done
fi

if [[ $TOTAL_RESULTS -gt 0 ]]; then
    AVG=$(grep "^PASS" "$RESULTS_FILE" | sed 's/PASS|//; s/ms//' | awk '{sum+=$1; count+=1} END {if(count>0) printf "%.0f", sum/count}')
    echo ""
    echo -e "  Avg response: ${CYAN}${AVG}ms${NC}"
fi
echo ""
