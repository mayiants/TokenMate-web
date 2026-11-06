#!/bin/bash
# ============================================
# Provider-by-provider API key test
# Tests all user keys against each upstream provider
# ============================================
set +e

PORTAL_URL="http://127.0.0.1:3001"
PROXY_URL="http://127.0.0.1:8080"
OUTPUT_FILE="/mnt/workspace/workspace/ai-portal-go/docs/provider-test-results-$(date +%Y%m%d-%H%M%S).md"

declare -A USERS
USERS["e2e-admin@example.com"]="E2E_AdminPass123!"
USERS["e2e-user@example.com"]="E2E_TestPass123!"
USERS["e2e-enterprise@example.com"]="E2E_TestPass123!"

# One test model per provider (pick widely available model)
declare -A PROVIDER_MODELS
PROVIDER_MODELS["laozhang"]="deepseek-v4-flash"
PROVIDER_MODELS["accessapi"]="claude-sonnet-4-6"
PROVIDER_MODELS["deepseek"]="deepseek-v4-flash"
PROVIDER_MODELS["alibaba"]="qwen3.6-flash"

login() {
    local email="$1" password="$2"
    curl -s -H "Content-Type: application/json" \
        -d "{\"email\":\"$email\",\"password\":\"$password\"}" \
        "$PORTAL_URL/api/auth/login" | python3 -c "import json,sys; print(json.load(sys.stdin).get('token',''))" 2>/dev/null
}

create_key() {
    local token="$1" name="$2" key_type="${3:-general}"
    curl -s -H "Authorization: Bearer $token" -H "Content-Type: application/json" \
        -d "{\"name\":\"$name\",\"key_type\":\"$key_type\"}" \
        "$PORTAL_URL/api/keys" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('key',''))" 2>/dev/null
}

test_model() {
    local key="$1" model="$2"
    local start=$(date +%s%N)
    local resp=$(curl -s -w "\n%{time_total}" --max-time 30 \
        -H "Authorization: Bearer $key" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"$model\",\"messages\":[{\"role\":\"user\",\"content\":\"Say hello in one word\"}],\"max_tokens\":20}" \
        "$PROXY_URL/v1/chat/completions" 2>/dev/null)
    local seconds=$(echo "$resp" | tail -1)
    local body=$(echo "$resp" | sed '$d')
    
    local status="FAIL"
    local content=""
    local prompt_tokens=0 completion_tokens=0 total_tokens=0
    local latency_ms=0
    
    if [[ -n "$body" ]]; then
        local parse_result=$(echo "$body" | python3 -c "
import json,sys
try:
    d=json.load(sys.stdin)
    if 'choices' in d:
        c=d['choices'][0]
        msg=c.get('message',{})
        content=msg.get('content','') or ''
        usage=d.get('usage',{})
        pt=usage.get('prompt_tokens',0)
        ct=usage.get('completion_tokens',0)
        tt=usage.get('total_tokens',0)
        print(f'OK|{content[:100]}|{pt}|{ct}|{tt}')
    else:
        err=d.get('error',{})
        print(f'ERR|{err.get(\"message\",\"\")[:150]}|0|0|0')
except:
    print(f'PARSE|{str(sys.stdin.read())[:100]}|0|0|0')
" 2>/dev/null)
        
        status=$(echo "$parse_result" | cut -d'|' -f1)
        content=$(echo "$parse_result" | cut -d'|' -f2)
        prompt_tokens=$(echo "$parse_result" | cut -d'|' -f3)
        completion_tokens=$(echo "$parse_result" | cut -d'|' -f4)
        total_tokens=$(echo "$parse_result" | cut -d'|' -f5)
        
        case "$status" in
            OK) status="PASS" ;;
            *) status="FAIL" ;;
        esac
    fi
    
    latency_ms=$(python3 -c "print(int(${seconds:-0} * 1000))" 2>/dev/null)
    
    echo "$status|$latency_ms|$prompt_tokens|$completion_tokens|$total_tokens|$content"
}

echo "Starting provider test at $(date)"
echo "================================================================="

# Build markdown report
cat > "$OUTPUT_FILE" << 'HEADER'
# Provider API Key Test Report

> Generated: __DATE__

## Summary

Tests all user API keys against each upstream provider (laozhang, accessapi, deepseek, alibaba).

HEADER

sed -i "s/__DATE__/$(date)/" "$OUTPUT_FILE"

TOTAL_TESTS=0
PASS_COUNT=0
FAIL_COUNT=0

for user_email in "${!USERS[@]}"; do
    password="${USERS[$user_email]}"
    user_short="${user_email%%@*}"
    
    echo "" | tee -a "$OUTPUT_FILE"
    echo "## User: $user_email" | tee -a "$OUTPUT_FILE"
    echo "" | tee -a "$OUTPUT_FILE"
    echo "| Provider | Model | Status | Latency | Prompt Tok | Compl Tok | Total Tok | Content |" | tee -a "$OUTPUT_FILE"
    echo "|----------|-------|--------|---------|-----------|----------|-----------|---------|" | tee -a "$OUTPUT_FILE"
    
    echo "  Logging in as $user_email ..."
    TOKEN=$(login "$user_email" "$password")
    if [[ -z "$TOKEN" ]]; then
        echo "  FAIL: Could not login!" | tee -a "$OUTPUT_FILE"
        continue
    fi
    echo "  Login OK"
    
    # Create one test key per key type
    echo "  Creating test keys ..."
    GENERAL_KEY=$(create_key "$TOKEN" "provider-test-general" "general")
    ENTERPRISE_KEY=""
    
    # Only create enterprise key if user is enterprise member
    has_ent=$(curl -s -H "Authorization: Bearer $TOKEN" "$PORTAL_URL/api/apikeys/validate?key=$GENERAL_KEY" 2>/dev/null | python3 -c "import json,sys; d=json.load(sys.stdin); print('true' if d.get('key_type')=='general' else 'false')" 2>/dev/null)
    
    echo "  General key: ${GENERAL_KEY:0:20}..."
    
    if [[ -n "$GENERAL_KEY" ]]; then
        KEY="$GENERAL_KEY"
        KEY_LABEL="general"
        
        # Validate the key first
        echo "  Validating key..."
        VALID=$(curl -s -H "Authorization: Bearer $KEY" "$PORTAL_URL/api/apikeys/validate" | python3 -c "import json,sys; print(json.load(sys.stdin).get('valid','false'))" 2>/dev/null)
        if [[ "$VALID" != "true" ]]; then
            echo "  Key validation failed, trying without group restrictions..."
            # Try again, maybe need to wait for cache
            sleep 1
        fi
        
        # Test against each provider
        for provider in laozhang accessapi deepseek alibaba; do
            model="${PROVIDER_MODELS[$provider]}"
            
            printf "  %-12s %-25s " "$provider" "$model"
            result=$(test_model "$KEY" "$model")
            status=$(echo "$result" | cut -d'|' -f1)
            latency=$(echo "$result" | cut -d'|' -f2)
            pt=$(echo "$result" | cut -d'|' -f3)
            ct=$(echo "$result" | cut -d'|' -f4)
            tt=$(echo "$result" | cut -d'|' -f5)
            content=$(echo "$result" | cut -d'|' -f6- | sed 's/|/ /g')
            
            if [[ "$status" == "PASS" ]]; then
                echo "✓ PASS (${latency}ms, ${tt} tokens)"
                ((PASS_COUNT++))
            else
                echo "✗ FAIL — ${content:0:80}"
                ((FAIL_COUNT++))
            fi
            ((TOTAL_TESTS++))
            
            echo "| $provider | $model | $status | ${latency}ms | $pt | $ct | $tt | ${content:0:50} |" >> "$OUTPUT_FILE"
            
            sleep 0.5
        done
    fi
    
    echo "" | tee -a "$OUTPUT_FILE"
done

# Summary
echo "" | tee -a "$OUTPUT_FILE"
echo "---" | tee -a "$OUTPUT_FILE"
echo "" | tee -a "$OUTPUT_FILE"
echo "## Overall Results" | tee -a "$OUTPUT_FILE"
echo "" | tee -a "$OUTPUT_FILE"
echo "| Metric | Value |" | tee -a "$OUTPUT_FILE"
echo "|--------|-------|" | tee -a "$OUTPUT_FILE"
echo "| Total Tests | $TOTAL_TESTS |" | tee -a "$OUTPUT_FILE"
echo "| Passed | $PASS_COUNT |" | tee -a "$OUTPUT_FILE"
echo "| Failed | $FAIL_COUNT |" | tee -a "$OUTPUT_FILE"
echo "| Pass Rate | $(python3 -c "print(f'{$PASS_COUNT}/{$TOTAL_TESTS}' if $TOTAL_TESTS>0 else 'N/A')") ($(python3 -c "print(f'{($PASS_COUNT*100//$TOTAL_TESTS) if $TOTAL_TESTS>0 else 0}%')")) |" | tee -a "$OUTPUT_FILE"

echo ""
echo "================================================================="
echo "Report saved to: $OUTPUT_FILE"