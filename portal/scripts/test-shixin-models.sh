#!/bin/bash
# ============================================
# Shixin (AccessAPI) 全模型延迟测试 v2
# 通过 fetchProviderModels API 获取真实模型列表，逐一请求并记录指标
# ============================================

PORTAL="http://127.0.0.1:3001"
PROXY="http://127.0.0.1:8080"
ADMIN_EMAIL="${ADMIN_EMAIL:-e2e-admin@example.com}"
ADMIN_PASS="${ADMIN_PASS:-E2E_AdminPass123!}"
REDIS_PASS="${REDIS_PASS:-}"

RESULTS_DIR="/tmp/shixin-test-$$"
RESULTS_FILE="$RESULTS_DIR/results.json"
MODELS_FILE="$RESULTS_DIR/models.json"
MD_FILE="/tmp/shixin-test-report.md"
rm -rf "$RESULTS_DIR"
mkdir -p "$RESULTS_DIR"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

log()   { echo -e "${CYAN}[$(date +%H:%M:%S)]${NC} $1"; }
pass()  { echo -e "  ${GREEN}PASS${NC} $1"; }
fail()  { echo -e "  ${RED}FAIL${NC} $1"; }

# === Step 1: Login ===
log "Logging in as admin..."
TOKEN=$(curl -s -X POST "$PORTAL/api/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\":\"$ADMIN_EMAIL\",\"password\":\"$ADMIN_PASS\"}" | \
    python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
if [[ -z "$TOKEN" ]]; then fail "Admin login failed"; exit 1; fi
pass "Admin login"

# === Step 2: Fetch real model list from accessapi ===
log "Fetching model list from accessapi (via portal fetchProviderModels)..."
FETCH_RESULT=$(curl -s --max-time 60 "$PORTAL/api/admin/provider-models/fetch?provider=shixin" \
    -H "Authorization: Bearer $TOKEN" 2>/dev/null)

echo "$FETCH_RESULT" | python3 -c "
import sys, json
d = json.load(sys.stdin)
models = d.get('models', d.get('data', []))
ids = [m['id'] for m in models if m.get('id')]
with open('$MODELS_FILE', 'w') as f:
    json.dump(ids, f)
print(f'Fetched {len(ids)} models')
" 2>/dev/null

if [[ ! -f "$MODELS_FILE" ]]; then
    fail "Failed to fetch model list"
    exit 1
fi

MODELS=()
while IFS= read -r m; do
    MODELS+=("$m")
done < <(python3 -c "import json; [print(m) for m in json.load(open('$MODELS_FILE'))]" 2>/dev/null)

MODEL_COUNT=${#MODELS[@]}
log "Total models from accessapi: $MODEL_COUNT"
echo ""

# === Step 3: Clear rate limits ===
log "Clearing rate limiters..."
docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" KEYS "*brute*" 2>/dev/null | grep -v Warning | while read k; do
    docker exec ai-portal-redis redis-cli -a "$REDIS_PASS" DEL "$k" 2>/dev/null > /dev/null
done

# === Step 4: Create test API key ===
log "Creating test API key..."
KEY_RESP=$(curl -s -X POST "$PORTAL/api/apikeys" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"name":"shixin-model-test","key_type":"general"}')
TEST_KEY=$(echo "$KEY_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('key',''))" 2>/dev/null)
TEST_KID=$(echo "$KEY_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)

if [[ -z "$TEST_KEY" || "$TEST_KEY" == "None" ]]; then
    fail "Failed to create test API key"
    exit 1
fi
pass "Created test API key (id=${TEST_KID:0:8})"

# Recharge admin balance
ADMIN_UID=$(docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal -t -A \
    -c "SELECT id FROM users WHERE email='admin@example.com'" 2>/dev/null)
docker exec ai-portal-postgres psql -U postgres -d ai_proxy_portal \
    -c "UPDATE users SET recharge_balance=500000, balance=500000 WHERE id='$ADMIN_UID'" > /dev/null 2>&1

# === Step 5: Test each model ===
echo "[" > "$RESULTS_FILE"

INDEX=0
GOOD=0
BAD=0
TIMEOUT_COUNT=0

log "Testing $MODEL_COUNT models against proxy (timeout=30s each)..."
log "Note: accessapi.ai has known TLS compatibility issues (Go crypto/tls handshake fails intermittently)"
echo ""

for model in "${MODELS[@]}"; do
    INDEX=$((INDEX + 1))
    # Skip non-chat models (embeddings, tts, image, whisper, babbage, davinci)
    case "$model" in
        text-embedding*|tts-*|whisper*|dall-e*|gpt-image*|babbage*|davinci*)
            echo -e "  [$INDEX/$MODEL_COUNT] $model ... ${YELLOW}SKIP (non-chat)${NC}"
            # Write a skipped entry
            python3 -c "
import json
entry = {'model': '$model', 'http_code': 0, 'latency_ms': 0, 'input_tokens': 0, 'output_tokens': 0, 'content': 'SKIPPED (non-chat model)', 'error': 'skipped'}
with open('$RESULTS_FILE', 'a') as f:
    json.dump(entry, f); f.write(',\n')" 2>/dev/null
            continue
            ;;
    esac

    echo -ne "  [$INDEX/$MODEL_COUNT] $model ... "

    START=$(date +%s%N)
    RESP=$(curl -s -w "\n%{http_code}" --max-time 30 -X POST "$PROXY/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -H "Authorization: Bearer $TEST_KEY" \
        -d '{"model":"'"$model"'","messages":[{"role":"user","content":"Hello! Who are you? Answer in one sentence."}],"max_tokens":100}' 2>/dev/null)
    END=$(date +%s%N)
    DURATION_MS=$(( (END - START) / 1000000 ))
    CODE=$(echo "$RESP" | tail -1)
    BODY=$(echo "$RESP" | sed '$d')

    # Parse response via Python
    PARSED=$(echo "$BODY" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    c = d.get('choices', [{}])[0].get('message', {}).get('content', '')[:200]
    it = d.get('usage', {}).get('prompt_tokens', 0)
    ot = d.get('usage', {}).get('completion_tokens', 0)
    e = d.get('error', {})
    em = e.get('message', '')[:150] if isinstance(e, dict) else str(e)[:150]
    print(json.dumps({'content': c, 'input_tokens': it, 'output_tokens': ot, 'error': em}))
except Exception as ex:
    print(json.dumps({'content': 'PARSE_ERROR', 'input_tokens': 0, 'output_tokens': 0, 'error': str(ex)[:150]}))
" 2>/dev/null)

    CONTENT=$(echo "$PARSED" | python3 -c "import sys,json; print(json.load(sys.stdin).get('content',''))" 2>/dev/null)
    INPUT_TOKENS=$(echo "$PARSED" | python3 -c "import sys,json; print(json.load(sys.stdin).get('input_tokens',0))" 2>/dev/null)
    OUTPUT_TOKENS=$(echo "$PARSED" | python3 -c "import sys,json; print(json.load(sys.stdin).get('output_tokens',0))" 2>/dev/null)
    ERROR_MSG=$(echo "$PARSED" | python3 -c "import sys,json; print(json.load(sys.stdin).get('error',''))" 2>/dev/null)

    if [[ "$CODE" == "200" ]]; then
        echo -e "${GREEN}OK${NC} ${DURATION_MS}ms in=${INPUT_TOKENS} out=${OUTPUT_TOKENS}"
        GOOD=$((GOOD+1))
    else
        echo -e "${RED}HTTP $CODE${NC} ${DURATION_MS}ms err=${ERROR_MSG:0:80}"
        BAD=$((BAD+1))
        if [[ "$CODE" == "000" || -z "$CODE" ]]; then
            TIMEOUT_COUNT=$((TIMEOUT_COUNT+1))
        fi
    fi

    # Write JSON entry (use Python to avoid shell escaping issues)
    python3 << PYEOF
import json
entry = {
    'model': '''$model''',
    'http_code': $CODE,
    'latency_ms': $DURATION_MS,
    'input_tokens': $INPUT_TOKENS,
    'output_tokens': $OUTPUT_TOKENS,
    'content': '''$CONTENT''',
    'error': '''$ERROR_MSG'''
}
with open('$RESULTS_FILE', 'a') as f:
    json.dump(entry, f)
    f.write(',\n')
PYEOF

    sleep 0.3
done

# Close JSON array
sed -i '$ s/,$//' "$RESULTS_FILE"
echo "]" >> "$RESULTS_FILE"

# Cleanup test key
curl -s -X DELETE "$PORTAL/api/apikeys/$TEST_KID" \
    -H "Authorization: Bearer $TOKEN" > /dev/null 2>&1

echo ""
log "=========================================="
log "  Results: $GOOD OK, $BAD Failed ($TIMEOUT_COUNT timeout)"
log "=========================================="
echo ""

# === Step 6: Generate Markdown Report ===
log "Generating markdown report..."

export RESULTS_FILE MD_FILE PROXY

python3 << 'PYEOF'
import json, os
from datetime import datetime

results_file = os.environ['RESULTS_FILE']
md_file = os.environ['MD_FILE']
proxy = os.environ.get('PROXY', 'http://127.0.0.1:8080')

with open(results_file) as f:
    data = json.load(f)

total = len(data)
success = [r for r in data if r.get('http_code') == 200]
failed = [r for r in data if r.get('http_code') != 0 and r.get('http_code') != 200]
skipped = [r for r in data if r.get('http_code') == 0]
timeouts = [r for r in data if r.get('http_code') in ('000', 0) and r.get('error') != 'skipped']
sc = len(success)
fc = len(failed)
sk = len(skipped)

# Filter content models for stats
chat_success = [r for r in success if r.get('input_tokens', 0) > 0 or r.get('output_tokens', 0) > 0]

ti = sum(r.get('input_tokens', 0) for r in success)
to = sum(r.get('output_tokens', 0) for r in success)

if chat_success:
    latencies = [r['latency_ms'] for r in chat_success]
    avg_lat = sum(latencies) / len(latencies)
    max_lat = max(latencies)
    min_lat = min(latencies)
else:
    avg_lat = max_lat = min_lat = 0

fast = [r for r in chat_success if r['latency_ms'] < 5000]
medium = [r for r in chat_success if 5000 <= r['latency_ms'] < 15000]
slow = [r for r in chat_success if r['latency_ms'] >= 15000]

lines = []
now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
sr = f"{sc/total*100:.1f}" if total > 0 else "N/A"

lines.append(f"""# Shixin (AccessAPI) 全模型延迟测试报告

**测试时间**: {now}
**Proxy**: `{proxy}`
**测试模型总数**: {total}（跳过 {sk} 个非对话模型）
**成功请求**: {sc}
**失败请求**: {fc}

---

## 总体统计

| 指标 | 值 |
|------|-----|
| 成功率 | {sr}% |
| 平均延迟 | {avg_lat:.0f}ms |
| 最低延迟 | {min_lat}ms |
| 最高延迟 | {max_lat}ms |
| 总输入 Token | {ti:,} |
| 总输出 Token | {to:,} |

""")

lines.append("## 延迟分布\n")
if sc > 0:
    lines.append(f"| 区间 | 数量 | 占比 |")
    lines.append(f"|------|------|------|")
    lines.append(f"| < 5s | {len(fast)} | {len(fast)/sc*100:.1f}% |")
    lines.append(f"| 5s ~ 15s | {len(medium)} | {len(medium)/sc*100:.1f}% |")
    lines.append(f"| > 15s | {len(slow)} | {len(slow)/sc*100:.1f}% |")
lines.append("")

if failed:
    lines.append("## 失败的模型\n")
    lines.append("| 模型 | HTTP 状态 | 延迟(ms) | 错误信息 |")
    lines.append("|------|-----------|----------|----------|")
    for r in failed:
        err = (r.get('error') or '')[:80] or 'N/A'
        lines.append(f"| `{r['model']}` | {r['http_code']} | {r['latency_ms']} | {err} |")
    lines.append("")

lines.append("## 全部模型详情（按延迟排序）\n")
sorted_all = sorted(data, key=lambda x: x['latency_ms'])
lines.append("| # | 模型 | 状态 | 延迟(ms) | 输入 Tokens | 输出 Tokens | 响应摘要 |")
lines.append("|---|------|------|----------|-------------|-------------|----------|")
for i, r in enumerate(sorted_all, 1):
    if r.get('http_code') == 0:
        status = "⏭️"
    elif r['http_code'] == 200:
        status = "✅"
    else:
        status = "❌"
    cp = (r.get('content') or '-')[:60].replace('\n', ' ')
    lines.append(f"| {i} | `{r['model']}` | {status} | {r['latency_ms']} | {r.get('input_tokens',0)} | {r.get('output_tokens',0)} | {cp} |")

lines.append("")

lines.append("## 结论\n")
if sc == 0:
    lines.append("所有对话模型请求均失败，请检查 accessapi 上游连接状态。\n")
elif slow:
    sp = len(slow)/sc*100
    lines.append(f"- 共 {len(slow)} 个模型延迟超过 15s，占成功的 {sp:.0f}%\n")
    if avg_lat > 10000:
        lines.append(f"- **平均延迟 {avg_lat:.0f}ms，整体延迟较高**\n")
    else:
        lines.append(f"- 平均延迟 {avg_lat:.0f}ms，大部分模型响应正常\n")
    lines.append("- 建议检查 accessapi 服务端负载或网络链路\n")
else:
    lines.append(f"- 所有模型请求成功，平均延迟 {avg_lat:.0f}ms\n")
    if avg_lat < 5000:
        lines.append("- 延迟表现良好\n")
    else:
        lines.append("- 延迟在可接受范围内\n")

with open(md_file, 'w') as f:
    f.write('\n'.join(lines))

print(f"Report saved to {md_file}")
PYEOF

# Show report summary
echo ""
log "=== Quick Summary ==="
echo ""
if [[ -f "$MD_FILE" ]]; then
    grep -E "^\| [0-9]" "$MD_FILE" | head -5
    echo "  ..."
    echo ""
    log "Full report: $MD_FILE"
fi

rm -rf "$RESULTS_DIR"
log "Done"
