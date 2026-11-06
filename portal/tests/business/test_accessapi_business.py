#!/usr/bin/env python3
"""
AccessAPI 端到端业务测试脚本

前置条件：
1. Portal (127.0.0.1:3001) 和 Proxy (127.0.0.1:8080) 已启动
2. 已在 Portal 后台添加 accessapi provider、upstream key、并激活 service model
3. 测试用户 thinbee09@qq.com 已存在

测试内容：
- 测试用户登录 Portal
- 创建 general API key
- 通过 Proxy 调用 accessapi 的 chat completions
- 验证响应、usage_records、balance 扣减
"""

import json
import sys
import time
import uuid
import requests

PORTAL = "http://127.0.0.1:3001"
PROXY = "http://127.0.0.1:8080"

TEST_EMAIL = "thinbee09@qq.com"
TEST_PASSWORD = "Test@123456"

# 选择一个 accessapi 上存在的、成本较低的模型
TEST_MODEL = "gpt-4o-mini"


def log(msg):
    print(f"[TEST] {msg}")


def login(email, password):
    r = requests.post(f"{PORTAL}/api/auth/login", json={"email": email, "password": password}, timeout=30)
    if r.status_code != 200:
        log(f"Login failed: {r.status_code} {r.text}")
        raise RuntimeError("Login failed")
    data = r.json()
    token = data.get("token")
    user = data.get("user", {})
    log(f"Logged in as {user.get('email')} ({user.get('role')}), user_id={user.get('id')}")
    return token


def create_key(token, name, model):
    headers = {"Authorization": f"Bearer {token}"}
    payload = {
        "name": name,
        "access_mode": "single_model",
        "current_model": model,
        "model_routing_mode": "single",
        "selected_models": [model],
        "allowed_models": [model],
    }
    r = requests.post(f"{PORTAL}/api/keys", headers=headers, json=payload, timeout=30)
    if r.status_code != 200:
        log(f"Create key failed: {r.status_code} {r.text}")
        raise RuntimeError("Create key failed")
    data = r.json()
    raw_key = data.get("key")
    key_id = data.get("id")
    log(f"Created API key: id={key_id}, prefix={data.get('prefix')}, model={model}")
    return raw_key, key_id


def chat_completion(api_key, model, messages, max_tokens=50):
    url = f"{PROXY}/v1/chat/completions"
    headers = {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"}
    payload = {
        "model": model,
        "messages": messages,
        "max_tokens": max_tokens,
    }
    r = requests.post(url, headers=headers, json=payload, timeout=60)
    return r


def get_usage_records(token, limit=5):
    headers = {"Authorization": f"Bearer {token}"}
    r = requests.get(f"{PORTAL}/api/usage?limit={limit}", headers=headers, timeout=30)
    if r.status_code != 200:
        log(f"Get usage records failed: {r.status_code} {r.text}")
        return []
    return r.json().get("items", [])


def get_me(token):
    headers = {"Authorization": f"Bearer {token}"}
    r = requests.get(f"{PORTAL}/api/auth/me", headers=headers, timeout=30)
    if r.status_code != 200:
        log(f"Get me failed: {r.status_code} {r.text}")
        return {}
    return r.json().get("user", {})


def main():
    log("=== AccessAPI Business Test Start ===")

    # 1. 登录测试用户
    token = login(TEST_EMAIL, TEST_PASSWORD)

    # 2. 记录测试前余额
    user_before = get_me(token)
    balance_before = user_before.get("balance", 0)
    log(f"Balance before test: {balance_before}")

    # 3. 创建 API key
    key_name = f"accessapi-test-{uuid.uuid4().hex[:8]}"
    api_key, key_id = create_key(token, key_name, TEST_MODEL)

    # 4. 调用 Proxy chat completions
    log(f"Sending chat completion to model={TEST_MODEL} via Proxy...")
    r = chat_completion(api_key, TEST_MODEL, [{"role": "user", "content": "Say hello in one sentence."}], max_tokens=30)
    log(f"Proxy response status: {r.status_code}")

    if r.status_code != 200:
        log(f"Proxy request failed: {r.text[:500]}")
        sys.exit(1)

    try:
        resp_json = r.json()
        content = resp_json["choices"][0]["message"]["content"]
        usage = resp_json.get("usage", {})
        log(f"Response content: {content[:100]}")
        log(f"Usage: prompt_tokens={usage.get('prompt_tokens')}, completion_tokens={usage.get('completion_tokens')}, total_tokens={usage.get('total_tokens')}")
    except Exception as e:
        log(f"Failed to parse response: {e}, body={r.text[:500]}")
        sys.exit(1)

    # 5. 等待异步计费处理
    log("Waiting 10s for async billing processing...")
    time.sleep(10)

    # 6. 验证余额扣减和 usage_records
    user_after = get_me(token)
    balance_after = user_after.get("balance", 0)
    log(f"Balance after test: {balance_after}")

    if balance_after < balance_before:
        log(f"Balance deducted: {balance_before - balance_after}")
    else:
        log("WARNING: Balance not deducted (may be due to async delay or zero cost)")

    usage_records = get_usage_records(token, limit=3)
    if usage_records:
        latest = usage_records[0]
        log(f"Latest usage record: id={latest.get('id')}, model={latest.get('model')}, provider={latest.get('provider')}, total_cost={latest.get('total_cost')}, input_tokens={latest.get('input_tokens')}, output_tokens={latest.get('output_tokens')}")
        if latest.get("provider") == "accessapi":
            log("SUCCESS: Request routed through accessapi provider")
        else:
            log(f"INFO: Request routed through provider={latest.get('provider')}")
    else:
        log("WARNING: No usage records found")

    log("=== AccessAPI Business Test Complete ===")


if __name__ == "__main__":
    main()
