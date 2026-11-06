#!/usr/bin/env python3
"""
AccessAPI 端到端业务测试脚本（DB 直接创建 key 版本）

由于当前 Portal login 接口在 Redis key 不存在时返回 503（brute force 保护对 redis.Nil 处理不当），
此脚本绕过 login，直接操作数据库创建 API key，然后调用 Proxy 验证完整链路。

前置条件：
1. Portal 和 Proxy 已启动
2. 已添加 accessapi provider、upstream key、并激活 service model
3. 测试用户 thinbee09@qq.com 已存在
"""

import hashlib
import json
import os
import subprocess
import sys
import time
import uuid
from pathlib import Path

import requests
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

PORTAL = "http://127.0.0.1:3001"
PROXY = "http://127.0.0.1:8080"

TEST_EMAIL = "thinbee09@qq.com"
TEST_MODEL = "gpt-5.4-nano"

# 从 ai-portal-go/.env 读取
ENV_PATH = Path(__file__).resolve().parents[2] / ".env"


def load_env():
    env = {}
    for line in ENV_PATH.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if "=" in line:
            k, v = line.split("=", 1)
            env[k] = v
    return env


ENV = load_env()
DB_PASSWORD = ENV["DB_PASSWORD"]
ENCRYPTION_KEY = ENV["ENCRYPTION_KEY"]


def log(msg):
    print(f"[TEST] {msg}")


def psql(sql):
    cmd = [
        "docker", "exec", "-i", "ai-portal-postgres",
        "psql", "-U", "postgres", "-d", "ai_proxy_portal",
        "-t", "-c", sql,
    ]
    env = os.environ.copy()
    env["PGPASSWORD"] = DB_PASSWORD
    result = subprocess.run(cmd, capture_output=True, text=True, env=env)
    if result.returncode != 0:
        log(f"psql error: {result.stderr}")
        raise RuntimeError("psql failed")
    return result.stdout.strip()


def encrypt_key(raw_key):
    key = hashlib.sha256(ENCRYPTION_KEY.encode()).digest()
    nonce = os.urandom(12)
    aesgcm = AESGCM(key)
    ct = aesgcm.encrypt(nonce, raw_key.encode(), None)
    return (nonce + ct).hex()


def hash_key(raw_key):
    return hashlib.sha256(raw_key.encode()).hexdigest()


def get_test_user():
    sql = f"SELECT id::text, balance, gift_balance FROM users WHERE email = '{TEST_EMAIL}' AND deleted_at IS NULL;"
    row = psql(sql)
    if not row:
        raise RuntimeError(f"Test user {TEST_EMAIL} not found")
    parts = [p.strip() for p in row.split("|")]
    return {"id": parts[0], "balance": int(parts[1]), "gift_balance": int(parts[2])}


def create_api_key(user_id, model):
    raw_key = "sk-wgit-" + str(uuid.uuid4())
    key_hash = hash_key(raw_key)
    key_encrypted = encrypt_key(raw_key)
    key_prefix = raw_key[:12] + "..."

    sql = f"""
    INSERT INTO api_keys (
        user_id, key_hash, key_prefix, key_encrypted, name, status, access_mode,
        current_model, model_routing_mode, selected_models, allowed_models,
        rate_limit_rpm, rate_limit_tpm, key_type, billing_mode
    ) VALUES (
        '{user_id}'::uuid, '{key_hash}', '{key_prefix}', '{key_encrypted}',
        'accessapi-e2e-test', 'active', 'single_model',
        '{model}', 'single', '["{model}"]'::jsonb, '["{model}"]'::jsonb,
        60, 100000, 'general', 'token'
    ) RETURNING id::text;
    """
    key_id = psql(sql)
    log(f"Created API key in DB: id={key_id}, prefix={key_prefix}")
    return raw_key, key_id


def chat_completion(api_key, model, messages, max_tokens=50):
    url = f"{PROXY}/v1/chat/completions"
    headers = {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"}
    payload = {
        "model": model,
        "messages": messages,
        "max_tokens": max_tokens,
    }
    return requests.post(url, headers=headers, json=payload, timeout=60)


def get_latest_usage(user_id):
    sql = f"""
    SELECT id::text, model, provider, input_tokens, output_tokens, total_cost,
           latency_ms, created_at
    FROM usage_records
    WHERE user_id = '{user_id}'::uuid
    ORDER BY created_at DESC LIMIT 1;
    """
    row = psql(sql)
    if not row:
        return None
    parts = [p.strip() for p in row.split("|")]
    keys = ["id", "model", "provider", "input_tokens", "output_tokens", "total_cost",
            "latency_ms", "created_at"]
    return dict(zip(keys, parts))


def get_user_balance(user_id):
    sql = f"SELECT balance, gift_balance FROM users WHERE id = '{user_id}'::uuid;"
    row = psql(sql)
    parts = [p.strip() for p in row.split("|")]
    return {"balance": int(parts[0]), "gift_balance": int(parts[1])}


def main():
    log("=== AccessAPI E2E Test Start ===")

    user = get_test_user()
    user_id = user["id"]
    log(f"Test user: {TEST_EMAIL}, id={user_id}, balance={user['balance']}")

    balance_before = user["balance"]

    raw_key, key_id = create_api_key(user_id, TEST_MODEL)

    log(f"Calling Proxy chat completions with model={TEST_MODEL}...")
    r = chat_completion(
        raw_key,
        TEST_MODEL,
        [{"role": "user", "content": "Say hello in one sentence."}],
        max_tokens=30,
    )
    log(f"Proxy response status: {r.status_code}")

    if r.status_code != 200:
        log(f"Proxy request failed: {r.text[:1000]}")
        sys.exit(1)

    try:
        resp_json = r.json()
        content = resp_json["choices"][0]["message"]["content"]
        usage = resp_json.get("usage", {})
        log(f"Assistant: {content[:120]}")
        log(f"Usage: prompt={usage.get('prompt_tokens')}, completion={usage.get('completion_tokens')}, total={usage.get('total_tokens')}")
    except Exception as e:
        log(f"Failed to parse response: {e}, body={r.text[:500]}")
        sys.exit(1)

    log("Waiting 10s for async billing...")
    time.sleep(10)

    latest = get_latest_usage(user_id)
    if latest:
        log(f"Latest usage record: model={latest['model']}, provider={latest['provider']}, "
            f"input={latest['input_tokens']}, output={latest['output_tokens']}, "
            f"total_cost={latest['total_cost']}, latency={latest['latency_ms']}ms")
        if latest["provider"] == "accessapi":
            log("SUCCESS: Request routed through accessapi provider")
        else:
            log(f"INFO: Routed through provider={latest['provider']}")
    else:
        log("WARNING: No usage record found")

    balance_after = get_user_balance(user_id)["balance"]
    log(f"Balance: before={balance_before}, after={balance_after}, deducted={balance_before - balance_after}")

    if balance_after < balance_before:
        log("SUCCESS: Balance deducted")
    else:
        log("WARNING: Balance not deducted")

    log("=== AccessAPI E2E Test Complete ===")


if __name__ == "__main__":
    main()
