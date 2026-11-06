#!/bin/bash
# ============================================
# AI Platform Go - End-to-End Platform Test
# ============================================
# 验证项：
#   1. Portal / Proxy 健康状态
#   2. Portal Redis 与 Proxy Redis 物理分离
#   3. Proxy 从 Portal 同步 keypool / models
#   4. Service model 激活并同步到 Proxy
#   5. 创建 API key 后可通过 Proxy 完成真实 upstream 调用
#
# 前置条件：
#   - ./start.sh mvp 已启动
#   - 已配置至少一个可用 upstream AWS key（amazon / amazon-mantle）
#   - Python3 可用（用于解析 JSON）
# ============================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
PROXY_DIR="${PROXY_DIR:-${PROJECT_DIR}/../ai-proxy-go}"

cd "${PROJECT_DIR}"

# 加载配置
if [[ -f ".env.go-platform" ]]; then
    set -a
    source .env.go-platform
    set +a
fi

PORTAL_URL="http://127.0.0.1:${PORTAL_PORT:-3001}"
PROXY_URL="http://127.0.0.1:${PROXY_PORT:-8080}"
ADMIN_EMAIL="${ADMIN_EMAIL:-admin@example.com}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:-Admin@123456!}"

REDIS_PASSWORD="${REDIS_PASSWORD:-}"
PROXY_REDIS_PASSWORD="${PROXY_REDIS_PASSWORD:-${REDIS_PASSWORD}}"

declare -i PASS=0
declare -i FAIL=0

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log_test() {
    local name="$1"
    local status="$2"
    local detail="${3:-}"
    if [[ "$status" == "PASS" ]]; then
        echo -e "${GREEN}✓${NC} $name"
        PASS=$((PASS + 1))
    else
        echo -e "${RED}✗${NC} $name"
        if [[ -n "$detail" ]]; then
            echo -e "  ${RED}Detail: $detail${NC}"
        fi
        FAIL=$((FAIL + 1))
    fi
}

json_get() {
    # $1 is a Python expression evaluated against the loaded JSON object `d`
    python3 -c 'import sys, json; d=json.load(sys.stdin); print(eval(sys.argv[1]), end="")' "$1"
}

# ============================================
# 1. Health Check
# ============================================
test_health() {
    echo -e "\n${CYAN}=== Health Check ===${NC}"

    local portal_health proxy_health
    portal_health=$(curl -s "$PORTAL_URL/api/health" || true)
    if echo "$portal_health" | grep -q '"status":"ok"'; then
        log_test "Portal health" "PASS"
    else
        log_test "Portal health" "FAIL" "$portal_health"
    fi

    proxy_health=$(curl -s "$PROXY_URL/health" || true)
    if echo "$proxy_health" | grep -q '"status":"healthy"'; then
        log_test "Proxy health" "PASS"
    else
        log_test "Proxy health" "FAIL" "$proxy_health"
    fi
}

# ============================================
# 2. Redis 分离检查
# ============================================
test_redis_separation() {
    echo -e "\n${CYAN}=== Redis Separation ===${NC}"

    local portal_ping proxy_ping portal_keys proxy_keys
    portal_ping=$(docker exec ai-portal-redis redis-cli -a "$REDIS_PASSWORD" ping 2>/dev/null || true)
    proxy_ping=$(docker exec ai-proxy-redis redis-cli -a "$PROXY_REDIS_PASSWORD" ping 2>/dev/null || true)

    if [[ "$portal_ping" == "PONG" ]]; then
        log_test "Portal Redis reachable" "PASS"
    else
        log_test "Portal Redis reachable" "FAIL" "$portal_ping"
    fi

    if [[ "$proxy_ping" == "PONG" ]]; then
        log_test "Proxy Redis reachable" "PASS"
    else
        log_test "Proxy Redis reachable" "FAIL" "$proxy_ping"
    fi

    portal_keys=$(docker exec ai-portal-redis redis-cli -a "$REDIS_PASSWORD" --scan --pattern '*' 2>/dev/null | wc -l)
    proxy_keys=$(docker exec ai-proxy-redis redis-cli -a "$PROXY_REDIS_PASSWORD" --scan --pattern '*' 2>/dev/null | wc -l)

    echo -e "  ${BLUE}Portal Redis keys: $portal_keys${NC}"
    echo -e "  ${BLUE}Proxy Redis keys:  $proxy_keys${NC}"

    if docker exec ai-proxy-redis redis-cli -a "$PROXY_REDIS_PASSWORD" --scan --pattern 'ai-proxy:keypool:keys:*' 2>/dev/null | grep -q 'ai-proxy:keypool:keys'; then
        log_test "Proxy Redis contains keypool snapshot" "PASS"
    else
        log_test "Proxy Redis contains keypool snapshot" "FAIL"
    fi

    if docker exec ai-portal-redis redis-cli -a "$REDIS_PASSWORD" --scan --pattern 'ai-proxy:keypool:keys:*' 2>/dev/null | grep -q 'ai-proxy:keypool:keys'; then
        log_test "Portal Redis does NOT contain keypool snapshot" "FAIL" "keypool data leaked into Portal Redis"
    else
        log_test "Portal Redis does NOT contain keypool snapshot" "PASS"
    fi
}

# ============================================
# 3. Key Pool 同步检查
# ============================================
test_keypool_sync() {
    echo -e "\n${CYAN}=== Key Pool Sync ===${NC}"

    local sync_resp key_count providers
    sync_resp=$(curl -s -H "x-proxy-secret: ${PROXY_SYNC_SECRET}" "$PROXY_URL/api/admin/key-pool/sync" || true)
    key_count=$(echo "$sync_resp" | json_get 'len(d.get("keys") or [])')

    echo -e "  ${BLUE}Synced upstream keys: $key_count${NC}"

    if [[ "$key_count" -ge 1 ]]; then
        log_test "Proxy keypool sync returned keys" "PASS"
    else
        log_test "Proxy keypool sync returned keys" "FAIL" "$sync_resp"
    fi

    providers=$(curl -s "$PROXY_URL/providers/stats" | json_get '", ".join(d.keys())')
    echo -e "  ${BLUE}Providers: $providers${NC}"

    if echo "$providers" | grep -qE 'amazon|amazon-mantle'; then
        log_test "AWS providers present in Proxy" "PASS"
    else
        log_test "AWS providers present in Proxy" "FAIL" "$providers"
    fi
}

# ============================================
# 4. 准备测试余额（直接更新 DB，避免 gift_balance 不计入验证）
# ============================================
ensure_test_balance() {
    echo -e "\n${CYAN}=== Ensure Admin Balance ===${NC}"

    local current_balance
    current_balance=$(docker exec -e PGPASSWORD="$DB_PASSWORD" ai-portal-postgres \
        psql -U postgres -d ai_proxy_portal -t -A -c "SELECT COALESCE(balance,0) FROM users WHERE email='${ADMIN_EMAIL}'" 2>/dev/null | tr -d '[:space:]')

    echo -e "  ${BLUE}Admin balance: $current_balance${NC}"

    if [[ -z "$current_balance" ]] || [[ "$current_balance" -lt 1000000 ]]; then
        docker exec -e PGPASSWORD="$DB_PASSWORD" ai-portal-postgres \
            psql -U postgres -d ai_proxy_portal -c "UPDATE users SET balance = 5000000 WHERE email='${ADMIN_EMAIL}';" >/dev/null 2>&1 || true
        log_test "Admin balance topped up" "PASS"
    else
        log_test "Admin balance sufficient" "PASS"
    fi
}

# ============================================
# 5. Portal 登录并创建 API Key
# ============================================
login_and_create_key() {
    echo -e "\n${CYAN}=== Portal Login & API Key ===${NC}"

    local login_resp token key_resp test_key
    login_resp=$(curl -s -X POST "$PORTAL_URL/api/auth/login" \
        -H 'Content-Type: application/json' \
        -d "{\"email\":\"${ADMIN_EMAIL}\",\"password\":\"${ADMIN_PASSWORD}\"}")

    token=$(echo "$login_resp" | json_get 'd.get("token","")')
    if [[ -z "$token" ]]; then
        log_test "Admin login" "FAIL" "$login_resp"
        return 1
    fi
    log_test "Admin login" "PASS"

    key_resp=$(curl -s -H "Authorization: Bearer $token" \
        -H 'Content-Type: application/json' \
        -d '{"name":"e2e-platform-test"}' \
        "$PORTAL_URL/api/keys")

    TEST_API_KEY=$(echo "$key_resp" | json_get 'd.get("key","")')
    if [[ -z "$TEST_API_KEY" ]]; then
        log_test "Create API key" "FAIL" "$key_resp"
        return 1
    fi
    echo -e "  ${BLUE}Test API key: ${TEST_API_KEY:0:24}...${NC}"
    log_test "Create API key" "PASS"

    # 通过全局变量回传，避免命令替换捕获日志输出
    TOKEN="$token"
    export TOKEN TEST_API_KEY
}

# ============================================
# 6. 从上游 Key 同步并激活 Service Models
# ============================================
activate_service_models() {
    echo -e "\n${CYAN}=== Activate Service Models ===${NC}"

    local providers=("amazon-us-east-1" "amazon-mantle-us-east-1")
    local -a activated_models=()

    for provider in "${providers[@]}"; do
        local preview models_json selected body resp
        preview=$(curl -s -H "Authorization: Bearer $TOKEN" \
            -H 'Content-Type: application/json' \
            -X POST "$PORTAL_URL/api/admin/service-models/sync-from-key?preview=true" \
            -d "{\"provider\":\"$provider\"}")

        local total
        total=$(echo "$preview" | json_get 'len(d.get("models",[]))')
        if [[ "$total" -eq 0 ]]; then
            echo -e "  ${YELLOW}⚠ No models previewed for $provider${NC}"
            continue
        fi

        # 优先选择明确支持 /v1/chat/completions 且成本较低的模型
        selected=$(python3 - "$preview" <<'PY'
import sys, json
preview_json = sys.argv[1]
if not preview_json:
    print(json.dumps([]))
    sys.exit(0)
d = json.loads(preview_json)
models = d.get("models", [])
preferred = [
    "amazon.nova-2-lite-v1:0",
    "amazon.nova-lite-v1:0",
    "amazon.nova-micro-v1:0",
    "anthropic.claude-3-haiku-20240307-v1:0",
    "meta.llama3-8b-instruct-v1:0",
    "meta.llama3-1-8b-instruct-v1:0",
    "meta.llama3-2-3b-instruct-v1:0",
    "mistral.ministral-3-8b-instruct",
    "mistral.mistral-small-2402-v1:0",
    "ai21.jamba-1-5-mini-v1:0",
    "deepseek.r1-v1:0",
    "deepseek.v3.2",
    "qwen.qwen3-coder-30b-a3b-instruct",
]
for prefix in preferred:
    for m in models:
        pid = m.get("product_id", "")
        if prefix in pid:
            print(json.dumps([{"product_id": pid, "api_model_name": m.get("api_model_name", pid), "name": pid}]))
            sys.exit(0)
# fallback: first model (may not support chat)
m = models[0]
print(json.dumps([{"product_id": m.get("product_id"), "api_model_name": m.get("api_model_name", m.get("product_id")), "name": m.get("product_id")}]))
PY
)
        body="{\"provider\":\"$provider\",\"activate\":true,\"models\":$selected}"
        resp=$(curl -s -H "Authorization: Bearer $TOKEN" \
            -H 'Content-Type: application/json' \
            -X POST "$PORTAL_URL/api/admin/service-models/sync-from-key" \
            -d "$body")

        local added total_sync
        added=$(echo "$resp" | json_get 'd.get("added",0)')
        total_sync=$(echo "$resp" | json_get 'd.get("total",0)')
        if [[ "$added" -ge 1 ]] || [[ "$total_sync" -ge 1 ]]; then
            model_pid=$(echo "$selected" | json_get 'd[0]["product_id"]')
            model_api=$(echo "$selected" | json_get 'd[0]["api_model_name"]')
            activated_models+=("$provider:$model_api")
            echo -e "  ${BLUE}Activated $provider -> $model_pid (${model_api})${NC}"
            log_test "Activate models for $provider" "PASS"
        else
            log_test "Activate models for $provider" "FAIL" "$resp"
        fi
    done

    if [[ ${#activated_models[@]} -eq 0 ]]; then
        echo -e "${RED}✗ No service models activated; aborting upstream test${NC}"
        return 1
    fi

    # 使用第一个被激活的模型（通常是 amazon-us-east-1 的 Nova）做 chat completion 测试
    TEST_MODEL=${activated_models[0]#*:}
}

# ============================================
# 7. 重启 Proxy 以重新同步 models，等待健康
# ============================================
restart_proxy_and_wait() {
    echo -e "\n${CYAN}=== Restart Proxy to Sync Models ===${NC}"

    if [[ -f "$PROXY_DIR/docker-compose.proxy.yml" ]]; then
        (cd "$PROXY_DIR" && docker compose -f docker-compose.proxy.yml restart proxy >/dev/null 2>&1)
    else
        echo -e "  ${YELLOW}⚠ Proxy compose not found at $PROXY_DIR; skipping restart${NC}"
        return 1
    fi

    local i
    for i in {1..30}; do
        local health
        health=$(curl -s "$PROXY_URL/health" || true)
        if echo "$health" | grep -q '"status":"healthy"'; then
            log_test "Proxy restarted and healthy" "PASS"
            return 0
        fi
        sleep 2
    done
    log_test "Proxy restarted and healthy" "FAIL" "timeout"
    return 1
}

# ============================================
# 8. Model Registry 检查
# ============================================
test_model_registry() {
    echo -e "\n${CYAN}=== Model Registry Sync ===${NC}"

    local model_list providers
    model_list=$(curl -s "$PROXY_URL/models/list" || true)
    providers=$(echo "$model_list" | json_get '", ".join(d.get("providers",[]))')

    echo -e "  ${BLUE}Model registry providers: $providers${NC}"

    if echo "$providers" | grep -qE 'amazon|amazon-mantle'; then
        log_test "Model registry synced active providers" "PASS"
    else
        log_test "Model registry synced active providers" "FAIL" "$model_list"
    fi
}

# ============================================
# 9. 通过 Proxy 调用上游（真实请求）
# ============================================
test_proxy_upstream() {
    echo -e "\n${CYAN}=== Proxy Upstream Call ===${NC}"

    if [[ -z "$TEST_API_KEY" ]] || [[ -z "$TEST_MODEL" ]]; then
        log_test "Proxy chat completion" "FAIL" "Missing API key or model"
        return 1
    fi

    echo -e "  ${BLUE}Model: $TEST_MODEL${NC}"

    local response http_code
    response=$(curl -s -w '\nHTTP:%{http_code}' \
        -H "Authorization: Bearer $TEST_API_KEY" \
        -H 'Content-Type: application/json' \
        -d "{\"model\":\"$TEST_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Hello\"}],\"max_tokens\":10,\"stream\":false}" \
        "$PROXY_URL/v1/chat/completions")

    http_code=$(echo "$response" | tail -1 | sed 's/HTTP://')
    body=$(echo "$response" | sed '$d')

    echo -e "  ${BLUE}HTTP: $http_code${NC}"

    if [[ "$http_code" == "200" ]] && echo "$body" | grep -q '"choices"'; then
        local content
        content=$(echo "$body" | json_get 'd.get("choices",[{}])[0].get("message",{}).get("content","")')
        echo -e "  ${BLUE}Assistant: $content${NC}"
        log_test "Proxy chat completion" "PASS"
    else
        log_test "Proxy chat completion" "FAIL" "$body"
    fi
}

# ============================================
# Main
# ============================================
main() {
    echo -e "${CYAN}========================================${NC}"
    echo -e "${CYAN}  AI Platform Go - E2E Platform Test${NC}"
    echo -e "${CYAN}========================================${NC}"
    echo -e "${BLUE}Portal: $PORTAL_URL${NC}"
    echo -e "${BLUE}Proxy:  $PROXY_URL${NC}"

    test_health
    test_redis_separation
    test_keypool_sync
    ensure_test_balance

    login_and_create_key || exit 1
    if [[ -z "$TOKEN" ]]; then
        echo -e "\n${RED}Aborting: cannot login${NC}"
        exit 1
    fi

    activate_service_models
    restart_proxy_and_wait
    test_model_registry
    test_proxy_upstream

    echo -e "\n${CYAN}========================================${NC}"
    echo -e "${CYAN}  Test Summary${NC}"
    echo -e "${CYAN}========================================${NC}"
    echo -e "${GREEN}Passed: $PASS${NC}"
    echo -e "${RED}Failed: $FAIL${NC}"
    echo -e "${BLUE}Total:  $((PASS + FAIL))${NC}"

    if [[ $FAIL -eq 0 ]]; then
        echo -e "\n${GREEN}✓ All E2E checks passed!${NC}"
        exit 0
    else
        echo -e "\n${RED}✗ Some E2E checks failed${NC}"
        exit 1
    fi
}

main "$@"
