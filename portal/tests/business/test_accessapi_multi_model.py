#!/usr/bin/env python3
"""
AccessAPI 多模型端到端业务测试

基于 probe_accessapi_models.py 的结果，选择可用的模型，通过本地 Proxy 逐个调用，
验证：响应成功、provider=accessapi、生成 usage_records、用户余额扣减。
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

# 从探测结果中挑选的几个代表性模型（成本由低到高）
DEFAULT_MODELS = [
    "gpt-5.4-nano",
    "gpt-5.4-mini",
    "claude-sonnet-4-20250514",
    "gemini-2.5-pro",
    "gemini-3.1-flash-image-preview",
]

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
    print(f"[MULTI] {msg}")


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
    sql = f"SELECT id::text, balance FROM users WHERE email = '{TEST_EMAIL}' AND deleted_at IS NULL;"
    row = psql(sql)
    if not row:
        raise RuntimeError(f"Test user {TEST_EMAIL} not found")
    parts = [p.strip() for p in row.split("|")]
    return {"id": parts[0], "balance": int(parts[1])}


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
        'accessapi-multi-test-{model}', 'active', 'single_model',
        '{model}', 'single', '["{model}"]'::jsonb, '["{model}"]'::jsonb,
        60, 100000, 'general', 'token'
    ) RETURNING id::text;
    """
    key_id = psql(sql)
    return raw_key, key_id


def chat_completion(api_key, model, messages, max_tokens=30):
    url = f"{PROXY}/v1/chat/completions"
    headers = {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"}
    payload = {
        "model": model,
        "messages": messages,
        "max_tokens": max_tokens,
    }
    return requests.post(url, headers=headers, json=payload, timeout=60)


def get_latest_usage(user_id, model):
    sql = f"""
    SELECT id::text, model, provider, input_tokens, output_tokens, total_cost, latency_ms, created_at
    FROM usage_records
    WHERE user_id = '{user_id}'::uuid AND model = '{model}'
    ORDER BY created_at DESC LIMIT 1;
    """
    row = psql(sql)
    if not row:
        return None
    parts = [p.strip() for p in row.split("|")]
    keys = ["id", "model", "provider", "input_tokens", "output_tokens", "total_cost", "latency_ms", "created_at"]
    return dict(zip(keys, parts))


def get_user_balance(user_id):
    sql = f"SELECT balance FROM users WHERE id = '{user_id}'::uuid;"
    row = psql(sql)
    return int(row.strip())


def test_model(user_id, model):
    log(f"--- Testing model: {model} ---")
    raw_key, key_id = create_api_key(user_id, model)

    balance_before = get_user_balance(user_id)

    r = chat_completion(
        raw_key,
        model,
        [{"role": "user", "content": "Say hello in one sentence."}],
        max_tokens=30,
    )

    if r.status_code != 200:
        log(f"  Proxy FAIL: HTTP {r.status_code} {r.text[:300]}")
        return {"model": model, "success": False, "error": f"HTTP {r.status_code} {r.text[:300]}"}

    try:
        resp = r.json()
        content = resp["choices"][0]["message"]["content"]
        usage = resp.get("usage", {})
        log(f"  Proxy OK: content_len={len(content)}, prompt={usage.get('prompt_tokens')}, completion={usage.get('completion_tokens')}")
    except Exception as e:
        log(f"  Proxy FAIL: parse error {e}")
        return {"model": model, "success": False, "error": str(e)}

    # 等待异步计费
    time.sleep(8)

    latest = get_latest_usage(user_id, model)
    if not latest:
        log("  Billing FAIL: no usage record")
        return {"model": model, "success": False, "error": "no usage record"}

    balance_after = get_user_balance(user_id)
    deducted = balance_before - balance_after
    provider = latest["provider"]
    log(f"  Usage OK: provider={provider}, total_cost={latest['total_cost']}, deducted={deducted}")

    if provider != "accessapi":
        return {"model": model, "success": False, "error": f"provider={provider}, expected accessapi"}

    if deducted <= 0:
        return {"model": model, "success": False, "error": "balance not deducted"}

    return {
        "model": model,
        "success": True,
        "provider": provider,
        "input_tokens": int(latest["input_tokens"]),
        "output_tokens": int(latest["output_tokens"]),
        "total_cost": int(latest["total_cost"]),
        "deducted": deducted,
        "latency_ms": int(latest["latency_ms"]) if latest["latency_ms"] else None,
    }


def main():
    log("=== AccessAPI Multi-Model E2E Test Start ===")

    # 如果传入参数，使用传入的模型列表
    models = sys.argv[1:] if len(sys.argv) > 1 else DEFAULT_MODELS

    user = get_test_user()
    user_id = user["id"]
    log(f"Test user: {TEST_EMAIL}, id={user_id}, initial_balance={user['balance']}")

    results = []
    for model in models:
        try:
            result = test_model(user_id, model)
            results.append(result)
        except Exception as e:
            log(f"  Exception: {e}")
            results.append({"model": model, "success": False, "error": str(e)})
        time.sleep(1)

    success_count = sum(1 for r in results if r["success"])
    fail_count = len(results) - success_count

    log("=== Summary ===")
    log(f"Total models: {len(results)}, Success: {success_count}, Failed: {fail_count}")
    for r in results:
        status = "PASS" if r["success"] else "FAIL"
        if r["success"]:
            log(f"  [{status}] {r['model']}: cost={r['total_cost']}, deducted={r['deducted']}, latency={r['latency_ms']}ms")
        else:
            log(f"  [{status}] {r['model']}: {r.get('error', '')}")

    final_balance = get_user_balance(user_id)
    log(f"Final balance: {final_balance}, total deducted: {user['balance'] - final_balance}")

    # 保存报告
    report = {
        "test_time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "test_user": TEST_EMAIL,
        "user_id": user_id,
        "initial_balance": user["balance"],
        "final_balance": final_balance,
        "total_deducted": user["balance"] - final_balance,
        "models_tested": results,
        "summary": {"total": len(results), "success": success_count, "failed": fail_count},
    }
    out_path = Path(__file__).resolve().parent / "accessapi_multi_model_report.json"
    with open(out_path, "w") as f:
        json.dump(report, f, indent=2)
    log(f"Report saved to {out_path}")

    if fail_count > 0:
        sys.exit(1)


if __name__ == "__main__":
    main()
