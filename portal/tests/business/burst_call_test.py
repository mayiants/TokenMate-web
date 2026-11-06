#!/usr/bin/env python3
"""
AccessAPI Burst Call Test

使用大量 user key 轮询访问 accessapi 的不同模型，测试整体接入能力、
Proxy 路由、限流反馈、错误恢复等。

前置条件：
- setup_burst_test.py 已运行，生成 1200 个测试 key
- accessapi 不可用模型已暂停
"""

import hashlib
import json
import os
import random
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

import requests
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

PROXY = "http://127.0.0.1:8080"
ENV_PATH = Path(__file__).resolve().parents[2] / ".env"

# 测试参数
TOTAL_REQUESTS = 500
CONCURRENCY = 50
REQUEST_TIMEOUT = 30
MAX_TOKENS = 20


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
    print(f"[BURST] {msg}")


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


def decrypt_key(encrypted_hex):
    key = hashlib.sha256(ENCRYPTION_KEY.encode()).digest()
    data = bytes.fromhex(encrypted_hex)
    nonce = data[:12]
    ciphertext = data[12:]
    aesgcm = AESGCM(key)
    return aesgcm.decrypt(nonce, ciphertext, None).decode()


def get_burst_keys():
    """获取所有 burst 测试 key 的原始字符串"""
    log("Loading burst test keys from DB...")
    sql = "SELECT key_encrypted FROM api_keys WHERE name LIKE 'burst-%' AND deleted_at IS NULL;"
    out = psql(sql)
    keys = []
    for line in out.splitlines():
        line = line.strip()
        if line:
            keys.append(decrypt_key(line))
    log(f"Loaded {len(keys)} keys")
    return keys


def get_active_models():
    """获取 accessapi 当前 active 模型"""
    log("Loading active accessapi models...")
    sql = "SELECT product_id FROM service_models WHERE provider = 'accessapi' AND status = 'active' AND deleted_at IS NULL;"
    out = psql(sql)
    models = [line.strip() for line in out.splitlines() if line.strip()]
    log(f"Loaded {len(models)} active models")
    return models


def make_request(key, model, idx):
    url = f"{PROXY}/v1/chat/completions"
    headers = {"Authorization": f"Bearer {key}", "Content-Type": "application/json"}
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": "Say hello briefly."}],
        "max_tokens": MAX_TOKENS,
    }
    start = time.time()
    try:
        r = requests.post(url, headers=headers, json=payload, timeout=REQUEST_TIMEOUT)
        latency = time.time() - start
        result = {
            "idx": idx,
            "status": r.status_code,
            "latency_ms": round(latency * 1000, 2),
            "model": model,
            "error": None,
        }
        if r.status_code == 200:
            try:
                data = r.json()
                usage = data.get("usage", {})
                result["prompt_tokens"] = usage.get("prompt_tokens")
                result["completion_tokens"] = usage.get("completion_tokens")
                result["total_tokens"] = usage.get("total_tokens")
            except Exception:
                pass
        else:
            try:
                result["error"] = r.json().get("error", {}).get("message", r.text[:200])
            except Exception:
                result["error"] = r.text[:200]
        return result
    except requests.exceptions.Timeout:
        return {"idx": idx, "status": -1, "latency_ms": round((time.time() - start) * 1000, 2), "model": model, "error": "timeout"}
    except Exception as e:
        return {"idx": idx, "status": -2, "latency_ms": round((time.time() - start) * 1000, 2), "model": model, "error": str(e)}


def run_burst(keys, models):
    log(f"Starting burst test: total={TOTAL_REQUESTS}, concurrency={CONCURRENCY}")
    results = []
    start_time = time.time()

    with ThreadPoolExecutor(max_workers=CONCURRENCY) as executor:
        futures = []
        for i in range(TOTAL_REQUESTS):
            key = random.choice(keys)
            model = random.choice(models)
            futures.append(executor.submit(make_request, key, model, i))

        for future in as_completed(futures):
            results.append(future.result())
            if len(results) % 50 == 0:
                log(f"  completed {len(results)}/{TOTAL_REQUESTS}")

    duration = time.time() - start_time
    return results, duration


def analyze(results, duration):
    total = len(results)
    success = [r for r in results if r["status"] == 200]
    failed = [r for r in results if r["status"] != 200]

    status_counts = {}
    error_counts = {}
    model_counts = {}
    model_errors = {}
    latencies = []

    for r in results:
        status_counts[r["status"]] = status_counts.get(r["status"], 0) + 1
        model_counts[r["model"]] = model_counts.get(r["model"], 0) + 1
        latencies.append(r["latency_ms"])
        if r["status"] != 200:
            err = r.get("error") or f"status_{r['status']}"
            error_counts[err] = error_counts.get(err, 0) + 1
            model_errors[r["model"]] = model_errors.get(r["model"], 0) + 1

    latencies.sort()
    avg_lat = sum(latencies) / len(latencies) if latencies else 0
    p50 = latencies[int(len(latencies) * 0.5)] if latencies else 0
    p95 = latencies[int(len(latencies) * 0.95)] if latencies else 0
    p99 = latencies[int(len(latencies) * 0.99)] if latencies else 0

    log("=== Burst Test Summary ===")
    log(f"Duration: {duration:.2f}s")
    log(f"Total requests: {total}")
    log(f"Success (200): {len(success)} ({len(success)/total*100:.1f}%)")
    log(f"Failed: {len(failed)} ({len(failed)/total*100:.1f}%)")
    log(f"Throughput: {total/duration:.1f} req/s")
    log(f"Latency: avg={avg_lat:.0f}ms, p50={p50:.0f}ms, p95={p95:.0f}ms, p99={p99:.0f}ms")

    log("Status code distribution:")
    for status, count in sorted(status_counts.items()):
        log(f"  {status}: {count}")

    if error_counts:
        log("Top errors:")
        for err, count in sorted(error_counts.items(), key=lambda x: -x[1])[:10]:
            log(f"  {count}x: {err[:120]}")

    if model_errors:
        log("Models with errors:")
        for model, count in sorted(model_errors.items(), key=lambda x: -x[1]):
            log(f"  {model}: {count} errors")

    report = {
        "test_time": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "total_requests": total,
        "concurrency": CONCURRENCY,
        "duration_sec": round(duration, 2),
        "throughput_rps": round(total / duration, 2),
        "success_count": len(success),
        "success_rate": round(len(success) / total * 100, 2),
        "failed_count": len(failed),
        "status_distribution": status_counts,
        "error_distribution": error_counts,
        "latency_ms": {
            "avg": round(avg_lat, 2),
            "p50": p50,
            "p95": p95,
            "p99": p99,
            "min": latencies[0] if latencies else 0,
            "max": latencies[-1] if latencies else 0,
        },
        "model_request_counts": model_counts,
        "model_error_counts": model_errors,
        "raw_results": results,
    }

    out_path = Path(__file__).resolve().parent / "burst_call_report.json"
    with open(out_path, "w") as f:
        json.dump(report, f, indent=2)
    log(f"Report saved to {out_path}")

    return report


def main():
    log("=== AccessAPI Burst Call Test Start ===")
    keys = get_burst_keys()
    models = get_active_models()
    if len(keys) < 10 or len(models) < 1:
        log("Not enough keys or models. Run setup_burst_test.py first.")
        sys.exit(1)

    results, duration = run_burst(keys, models)
    analyze(results, duration)
    log("=== AccessAPI Burst Call Test Complete ===")


if __name__ == "__main__":
    main()
