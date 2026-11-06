#!/usr/bin/env python3
"""
为 Burst Call Test 准备测试用户和大量 API key。

- 使用已有用户 thinbee09@qq.com 和 16932408@qq.com
- 新建 burst-test-{1..N}@example.com 用户
- 每个用户创建 KEYS_PER_USER 个 general API key
- 所有 key 绑定到 accessapi 的可用模型池（随机分配 current_model）
"""

import hashlib
import os
import subprocess
import uuid
from pathlib import Path

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

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

# 配置
NEW_USERS = ["burst-test-1", "burst-test-2", "burst-test-3", "burst-test-4"]
EXISTING_USERS = ["thinbee09@qq.com", "16932408@qq.com"]
KEYS_PER_USER = 200
BALANCE = 100_000_000  # micro-USD

# accessapi 可用模型（从 probe 结果）
AVAILABLE_MODELS = [
    "claude-haiku-4-5-20251001-thinking",
    "claude-opus-4-1-20250805-thinking",
    "claude-opus-4-5-20251101-thinking",
    "claude-opus-4-6",
    "claude-opus-4-6-thinking",
    "claude-opus-4-7",
    "claude-opus-4-8",
    "claude-sonnet-4-20250514",
    "claude-sonnet-4-5-20250929",
    "claude-sonnet-4-5-20250929-thinking",
    "claude-sonnet-4-6",
    "gemini-2.5-pro",
    "gemini-3-pro-image-preview-t",
    "gemini-3.1-flash-image-preview",
    "gpt-5.4-mini",
    "gpt-5.4-mini-2026-03-17",
    "gpt-5.4-nano",
    "gpt-5.4-nano-2026-03-17",
    "gpt-5.5",
]


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
        print(f"psql error: {result.stderr}")
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


def create_user(email):
    sql = f"""
    INSERT INTO users (id, name, email, password_hash, role, status, balance, email_verified)
    VALUES (gen_random_uuid(), 'Burst Test', '{email}',
            '$2b$10$tXTIjt6DIYyNERcDiej8NO3chIBUU2uOYuafBWlNblTF2qmPrqhfe',
            'user', 'active', {BALANCE}, true)
    ON CONFLICT (email) WHERE deleted_at IS NULL DO UPDATE SET balance = EXCLUDED.balance, updated_at = NOW()
    RETURNING id::text;
    """
    out = psql(sql)
    # psql -t may include extra lines like 'INSERT 0 1'; take first uuid-like line
    for line in out.splitlines():
        line = line.strip()
        if len(line) == 36 and line.count('-') == 4:
            return line
    raise RuntimeError(f"Failed to parse user_id from: {out}")


def create_keys_for_user(user_id, count, start_idx=0):
    values = []
    for i in range(count):
        raw_key = "sk-wgit-" + str(uuid.uuid4())
        key_hash = hash_key(raw_key)
        key_encrypted = encrypt_key(raw_key)
        key_prefix = raw_key[:12] + "..."
        model = AVAILABLE_MODELS[(start_idx + i) % len(AVAILABLE_MODELS)]
        name = f"burst-{model}-{i}"
        values.append(
            f"('{user_id}'::uuid, '{key_hash}', '{key_prefix}', '{key_encrypted}', '{name}', 'active', 'single_model', "
            f"'{model}', 'single', '[\"{model}\"]'::jsonb, '[\"{model}\"]'::jsonb, 60, 100000, 'general', 'token')"
        )

    # 分批插入，每批 100 条
    batch_size = 100
    inserted = 0
    for batch_start in range(0, len(values), batch_size):
        batch = values[batch_start:batch_start + batch_size]
        sql = f"""
        INSERT INTO api_keys (
            user_id, key_hash, key_prefix, key_encrypted, name, status, access_mode,
            current_model, model_routing_mode, selected_models, allowed_models,
            rate_limit_rpm, rate_limit_tpm, key_type, billing_mode
        ) VALUES {','.join(batch)};
        """
        psql(sql)
        inserted += len(batch)
        print(f"  inserted {inserted}/{count}")


def main():
    print("=== Setting up burst test users and keys ===")

    all_users = []

    # 确保已有用户存在且余额充足
    for email in EXISTING_USERS:
        print(f"Ensuring existing user {email}...")
        user_id = create_user(email)
        print(f"  user_id={user_id}")
        all_users.append((email, user_id))

    # 创建新用户
    for username in NEW_USERS:
        email = f"{username}@example.com"
        print(f"Creating user {email}...")
        user_id = create_user(email)
        print(f"  user_id={user_id}")
        all_users.append((email, user_id))

    # 为每个用户创建 key
    for email, user_id in all_users:
        print(f"Creating {KEYS_PER_USER} keys for {email}...")
        create_keys_for_user(user_id, KEYS_PER_USER)

    # 汇总
    total = psql("SELECT COUNT(*) FROM api_keys WHERE name LIKE 'burst-%';")
    print(f"\nTotal burst test keys: {total}")
    print("Setup complete.")


if __name__ == "__main__":
    main()
