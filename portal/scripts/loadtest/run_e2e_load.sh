#!/bin/bash
# E2E Load Test — create keys, call proxy, track tokens & cache
# Usage: bash scripts/loadtest/run_e2e_load.sh [key_count] [--clean]
set -e

COUNT="${1:-10}"
PORTAL="http://127.0.0.1:3001"
PROXY="http://127.0.0.1:8080"
ADMIN_EMAIL="${ADMIN_EMAIL:-e2e-admin@example.com}"
ADMIN_PASSWORD="Admin@123456!"

# Models to alternate between
RUNTIME_MODEL="anthropic.claude-sonnet-4-6"   # via amazon (AK/SK)
MANTLE_MODEL="deepseek.v3.1"                  # via amazon-mantle-ap-southeast-2 (Bearer)

echo "============================================"
echo " E2E Load Test — $COUNT keys"
echo " Runtime: $RUNTIME_MODEL"
echo " Mantle:  $MANTLE_MODEL"
echo "============================================"

# Step 1: Login
echo ""
echo "[1/4] Logging in..."
TOKEN=$(curl -s "$PORTAL/api/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASSWORD\"}" \
  | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
if [ -z "$TOKEN" ]; then echo "LOGIN FAILED"; exit 1; fi
echo "  Token: ${TOKEN:0:20}..."

# Step 2: Create API keys
echo ""
echo "[2/4] Creating $COUNT keys..."
KEYS_FILE=$(mktemp)
for i in $(seq 1 $COUNT); do
  result=$(curl -s -X POST "$PORTAL/api/apikeys" \
    -H "Authorization: Bearer $TOKEN" \
    -H 'Content-Type: application/json' \
    -d "{\"name\":\"loadtest-$i\",\"access_mode\":\"all_models\"}" 2>/dev/null)
  key=$(echo "$result" | python3 -c "import sys,json; print(json.load(sys.stdin).get('key',''))" 2>/dev/null)
  if [ -n "$key" ]; then
    echo "$key" >> "$KEYS_FILE"
  fi
  if [ $((i % 50)) -eq 0 ]; then echo "  Created $i keys..."; fi
done
CREATED=$(wc -l < "$KEYS_FILE")
echo "  Created $CREATED / $COUNT keys"

# Step 3: Run requests
echo ""
echo "[3/4] Sending requests..."
RESULTS=$(mktemp)
TOTAL_REQUESTS=0
TOTAL_SUCCESS=0
TOTAL_TOKENS=0
TOTAL_PROMPT=0
TOTAL_COMPLETION=0
CACHE_HITS=0
CACHE_MISSES=0
RUNTIME_OK=0
RUNTIME_FAIL=0
MANTLE_OK=0
MANTLE_FAIL=0

idx=0
while IFS= read -r api_key; do
  idx=$((idx + 1))
  # Alternate models
  if [ $((idx % 2)) -eq 0 ]; then
    model="$RUNTIME_MODEL"
    track="runtime"
  else
    model="$MANTLE_MODEL"
    track="mantle"
  fi

  TOTAL_REQUESTS=$((TOTAL_REQUESTS + 1))

  resp=$(curl -s -X POST "$PROXY/v1/chat/completions" \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $api_key" \
    -d "{\"model\":\"$model\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hello in one word\"}],\"max_tokens\":10}" \
    --max-time 20 2>/dev/null)

  # Parse response
  content=$(echo "$resp" | python3 -c "
import sys,json
d=json.load(sys.stdin)
if 'choices' in d:
    print(d['choices'][0]['message']['content'].strip())
elif 'error' in d:
    print('ERROR:'+json.dumps(d['error']))
else:
    print('UNKNOWN')
" 2>/dev/null)

  usage=$(echo "$resp" | python3 -c "
import sys,json
d=json.load(sys.stdin)
u=d.get('usage',{})
print(f\"{u.get('prompt_tokens',0)}|{u.get('completion_tokens',0)}|{u.get('total_tokens',0)}\")
" 2>/dev/null)

  pt=$(echo "$usage" | cut -d'|' -f1)
  ct=$(echo "$usage" | cut -d'|' -f2)
  tt=$(echo "$usage" | cut -d'|' -f3)

  if [ -n "$content" ] && [[ "$content" != ERROR* ]] && [[ "$content" != UNKNOWN* ]]; then
    TOTAL_SUCCESS=$((TOTAL_SUCCESS + 1))
    if [ "$track" = "runtime" ]; then RUNTIME_OK=$((RUNTIME_OK + 1)); else MANTLE_OK=$((MANTLE_OK + 1)); fi
  else
    if [ "$track" = "runtime" ]; then RUNTIME_FAIL=$((RUNTIME_FAIL + 1)); else MANTLE_FAIL=$((MANTLE_FAIL + 1)); fi
  fi

  TOTAL_PROMPT=$((TOTAL_PROMPT + pt))
  TOTAL_COMPLETION=$((TOTAL_COMPLETION + ct))
  TOTAL_TOKENS=$((TOTAL_TOKENS + tt))

  # Progress
  if [ $((idx % 25)) -eq 0 ]; then
    echo "  Progress: $idx/$CREATED (ok=$TOTAL_SUCCESS fail=$((TOTAL_REQUESTS-TOTAL_SUCCESS)))"
  fi
done < "$KEYS_FILE"

# Step 4: Report
echo ""
echo "[4/4] Results"
echo "============================================"
echo "  Total requests:    $TOTAL_REQUESTS"
echo "  Success:           $TOTAL_SUCCESS ($(( TOTAL_SUCCESS * 100 / TOTAL_REQUESTS ))%)"
echo "  Failed:            $((TOTAL_REQUESTS - TOTAL_SUCCESS))"
echo ""
echo "  --- By Track ---"
echo "  Runtime (AK/SK):   $RUNTIME_OK ok, $RUNTIME_FAIL fail"
echo "  Mantle (Bearer):   $MANTLE_OK ok, $MANTLE_FAIL fail"
echo ""
echo "  --- Token Usage ---"
echo "  Total tokens:      $TOTAL_TOKENS"
echo "  Prompt tokens:     $TOTAL_PROMPT"
echo "  Completion tokens: $TOTAL_COMPLETION"
echo "  Avg tokens/req:    $(( TOTAL_TOKENS / (TOTAL_SUCCESS > 0 ? TOTAL_SUCCESS : 1) ))"
echo "============================================"

# Cleanup
if [ "${2:-}" = "--clean" ]; then
  echo ""
  echo "Cleaning up test keys..."
  python3 -c "
import json,subprocess,sys
# Get all keys and delete loadtest ones
result = subprocess.run(['curl','-s',\"$PORTAL/api/apikeys\",'-H','Authorization: Bearer $TOKEN'],capture_output=True,text=True)
keys = json.loads(result.stdout) if result.stdout else []
if isinstance(keys, dict): keys = keys.get('keys', [])
for k in keys:
    if 'loadtest' in k.get('name',''):
        subprocess.run(['curl','-s','-X','DELETE',f\"$PORTAL/api/apikeys/{k['id']}\",'-H','Authorization: Bearer $TOKEN'],capture_output=True)
print(f'Cleaned {len(keys)} keys')
" 2>/dev/null || true
fi

rm -f "$KEYS_FILE" "$RESULTS"
