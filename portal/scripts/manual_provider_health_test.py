#!/usr/bin/env python3
"""
手动验证多 provider 上行 key，通过本地 Proxy 生成 provider health stats。
用法:
    export ZHIPU_KEYS='...'
    export DEEPSEEK_KEYS='...'
    export MINIMAX_KEYS='...'
    export XIAOMI_KEYS='...'
    export KIMI_KEYS='...'
    python3 scripts/manual_provider_health_test.py
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
ENV_PATH = Path(__file__).resolve().parents[1] / ".env"

PROVIDER_CONFIG = {
    "zhipu": {
        "base_url": "https://open.bigmodel.cn/api/paas/v4",
        "models": ["glm-4", "glm-3-turbo", "glm-4-flash"],
        "test_model": "glm-4",
    },
    "deepseek": {
        "base_url": "https://api.deepseek.com/v1",
        "models": ["deepseek-chat", "deepseek-coder"],
        "test_model": "deepseek-chat",
    },
    "minimax": {
        "base_url": "https://api.minimax.chat/v1",
        "models": ["MiniMax-M3", "MiniMax-M2.5", "MiniMax-Text-01"],
        "test_model": "MiniMax-M3",
    },
    "xiaomi": {
        "base_url": "https://api.xiaomimimo.com/v1",
        "models": ["mimo-v2-flash", "mimo-v2-pro", "mimo-v2.5"],
        "test_model": "mimo-v2-flash",
    },
    "moonshot": {  # Kimi
        "base_url": "https://api.moonshot.cn/v1",
        "models": ["kimi-k2.5", "moonshot-v1-8k"],
        "test_model": "kimi-k2.5",
    },
    "accessapi": {
        "base_url": "https://accessapi.ai/v1",
        "models": [
            "gpt-4o-mini", "gpt-4o", "gpt-4-turbo", "gpt-4-32k", "gpt-4",
            "gpt-5.4-mini", "gpt-5-chat", "o1-mini", "o4-mini",
            "claude-sonnet-4-20250514", "claude-3-5-haiku-20241022",
            "gemini-2.5-pro", "gemini-1.5-pro", "gemini-1.5-flash",
            "dall-e-3", "text-embedding-3-small",
        ],
        "test_model": "gpt-4o-mini",
    },
}


def load_env():
    env = {}
    if ENV_PATH.exists():
        for line in ENV_PATH.read_text().splitlines():
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if "=" in line:
                k, v = line.split("=", 1)
                env[k] = v
    return env


ENV = load_env()
DB_PASSWORD = ENV.get("DB_PASSWORD", os.environ.get("DB_PASSWORD", ""))
ENCRYPTION_KEY = ENV.get("ENCRYPTION_KEY", os.environ.get("ENCRYPTION_KEY", ""))
PROXY_SYNC_SECRET = os.environ.get("PROXY_SYNC_SECRET", ENV.get("PORTAL_SYNC_SECRET", ""))


def log(msg):
    print(f"[HEALTH-TEST] {msg}")


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


def insert_upstream_key(provider, raw_key, base_url, models):
    key_encrypted = encrypt_key(raw_key)
    key_preview = raw_key[:12] + "..."
    models_json = json.dumps(models)
    sql = f"""
    INSERT INTO upstream_keys (
        provider, key_encrypted, key_preview, tier, base_url, models,
        enabled, validation_status, assignment_mode
    ) VALUES (
        '{provider}', '{key_encrypted}', '{key_preview}', 'default',
        '{base_url}', '{models_json}'::jsonb,
        true, 'valid', 'shared'
    ) RETURNING id::text;
    """
    return psql(sql)


def get_or_create_test_user():
    sql = f"SELECT id::text, balance FROM users WHERE email = '{TEST_EMAIL}' AND deleted_at IS NULL;"
    row = psql(sql)
    if row:
        parts = [p.strip() for p in row.split("|")]
        return {"id": parts[0], "balance": int(parts[1])}

    uid = str(uuid.uuid4())
    psql(f"""
        INSERT INTO users (id, name, email, password_hash, balance, status, role)
        VALUES ('{uid}'::uuid, 'Health Test', '{TEST_EMAIL}', 'dummy-hash', 100000000, 'active', 'user');
    """)
    log(f"Created test user {TEST_EMAIL} id={uid}")
    return {"id": uid, "balance": 100000000}


def create_api_key(user_id, model):
    raw_key = "sk-health-" + str(uuid.uuid4())
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
        'health-test-{model}', 'active', 'single_model',
        '{model}', 'single', '["{model}"]'::jsonb, '["{model}"]'::jsonb,
        60, 100000, 'general', 'token'
    ) RETURNING id::text;
    """
    key_id = psql(sql)
    return raw_key, key_id


def chat_completion(api_key, model, messages, max_tokens=15):
    url = f"{PROXY}/v1/chat/completions"
    headers = {"Authorization": f"Bearer {api_key}", "Content-Type": "application/json"}
    payload = {
        "model": model,
        "messages": messages,
        "max_tokens": max_tokens,
    }
    return requests.post(url, headers=headers, json=payload, timeout=60)


def sync_proxy_keys():
    url = f"{PROXY}/api/admin/key-pool/sync"
    headers = {"x-proxy-secret": PROXY_SYNC_SECRET or "manual-sync"}
    r = requests.get(url, headers=headers, timeout=30)
    if r.status_code == 200:
        data = r.json()
        total = data.get("exclusiveCount", 0) + data.get("sharedCount", 0)
        log(f"Proxy sync: HTTP {r.status_code} -> keys={total}")
    else:
        log(f"Proxy sync: HTTP {r.status_code} -> {r.text[:200]}")
    return r.status_code == 200


def run_request(provider, model, api_key):
    log(f"--- {provider}/{model} ---")
    try:
        r = chat_completion(api_key, model, [{"role": "user", "content": "hi"}], max_tokens=10)
        log(f"Proxy response: HTTP {r.status_code}")
        if r.status_code == 200:
            data = r.json()
            content = data["choices"][0]["message"].get("content", "")[:80]
            log(f"  content={content!r}")
        else:
            log(f"  body={r.text[:200]!r}")
    except Exception as e:
        log(f"Request exception: {e}")


def show_health_tables():
    log("--- provider_health_stats (latest) ---")
    rows = psql("""
        SELECT provider, request_count, failure_count, avg_latency_ms, status, availability, error_rate
        FROM provider_health_stats
        WHERE bucket >= NOW() - INTERVAL '10 minutes'
        ORDER BY provider, bucket DESC;
    """)
    print(rows or "(empty)")

    log("--- provider_health_stats_model (latest) ---")
    rows = psql("""
        SELECT provider, model, request_count, failure_count, availability
        FROM provider_health_stats_model
        WHERE bucket >= NOW() - INTERVAL '10 minutes'
        ORDER BY provider, model, bucket DESC;
    """)
    print(rows or "(empty)")

    log("--- model_group_health_stats (latest) ---")
    rows = psql("""
        SELECT model, group_id, request_count, failure_count, availability
        FROM model_group_health_stats
        WHERE bucket >= NOW() - INTERVAL '10 minutes'
        ORDER BY model, group_id, bucket DESC;
    """)
    print(rows or "(empty)")


def admin_token(email="admin@example.com", password="Admin@123456!"):
    url = f"{PORTAL}/api/auth/login"
    r = requests.post(url, json={"email": email, "password": password}, timeout=10)
    if r.status_code != 200:
        log(f"Admin login failed: HTTP {r.status_code} {r.text[:200]}")
        return None
    return r.json().get("token")


def verify_upstream_apis(token):
    for path in [
        "/api/status/upstream",
        "/api/admin/upstream-ops/health",
        "/api/admin/upstream-ops/health/providers",
        "/api/admin/upstream-ops/health/summary",
        "/api/admin/upstream-ops/errors",
    ]:
        url = f"{PORTAL}{path}"
        try:
            r = requests.get(url, headers={"Authorization": f"Bearer {token}"}, timeout=10)
            log(f"{path}: HTTP {r.status_code}")
            if r.status_code == 200:
                print(json.dumps(r.json(), indent=2, ensure_ascii=False)[:1200])
        except Exception as e:
            log(f"{path}: error {e}")


def main():
    env_map = {
        "zhipu": "ZHIPU_KEYS",
        "deepseek": "DEEPSEEK_KEYS",
        "minimax": "MINIMAX_KEYS",
        "xiaomi": "XIAOMI_KEYS",
        "moonshot": "KIMI_KEYS",
        "accessapi": "ACCESSAPI_KEYS",
    }
    provider_keys = {}
    for provider, env_name in env_map.items():
        keys = [k.strip() for k in os.environ.get(env_name, "").split(",") if k.strip()]
        if keys:
            provider_keys[provider] = keys

    if not provider_keys:
        log("Need at least one of ZHIPU_KEYS/DEEPSEEK_KEYS/MINIMAX_KEYS/XIAOMI_KEYS/KIMI_KEYS/ACCESSAPI_KEYS")
        sys.exit(1)

    # 清理本轮 provider 的旧 key，避免重复
    psql(f"DELETE FROM upstream_keys WHERE provider IN ({','.join(repr(p) for p in provider_keys)});")
    log(f"Cleaned old upstream_keys for {list(provider_keys)}")

    for provider, keys in provider_keys.items():
        cfg = PROVIDER_CONFIG[provider]
        for k in keys:
            insert_upstream_key(provider, k, cfg["base_url"], cfg["models"])
            log(f"Inserted {provider} key {k[:12]}...")

    if not sync_proxy_keys():
        log("WARNING: proxy sync failed, continuing anyway")

    user = get_or_create_test_user()
    log(f"Test user {TEST_EMAIL} id={user['id']} balance={user['balance']}")

    tests = [(p, PROVIDER_CONFIG[p]["test_model"]) for p in provider_keys]

    for provider, model in tests:
        raw_key, key_id = create_api_key(user["id"], model)
        log(f"Created api_key id={key_id} model={model}")
        run_request(provider, model, raw_key)

    # 等待 health webhook 批量 flush（默认 5s）
    log("Waiting 10s for health webhook flush...")
    time.sleep(10)

    show_health_tables()

    token = admin_token()
    if token:
        verify_upstream_apis(token)


if __name__ == "__main__":
    main()
