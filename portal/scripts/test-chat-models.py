#!/usr/bin/env python3
"""Test chat models from shixin provider, handling 429 cooldown gracefully."""
import json, os, sys, time, subprocess, re
from datetime import datetime
from urllib.request import Request, urlopen
from urllib.error import HTTPError

PORTAL = "http://127.0.0.1:3001"
PROXY = "http://127.0.0.1:8080"
ADMIN_EMAIL = "admin@example.com"
ADMIN_PASS = "Admin@123456!"
REDIS_PASS = "e24b5de1602c5d2f0d43b930e83521729456ecfee66d642d4b7ea4895379bc0e"

COOLDOWN_WAIT = 65  # seconds to wait after 429

skip_prefixes = ("text-embedding-", "tts-", "whisper-", "dall-e-", "gpt-image-", "babbage-", "davinci-")

def api(method, path, data=None, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    body = json.dumps(data).encode() if data else None
    req = Request(f"{PORTAL}{path}", data=body, headers=headers, method=method)
    try:
        with urlopen(req) as r:
            return json.loads(r.read())
    except HTTPError as e:
        print(f"  API error {e.code}: {e.read().decode()[:200]}")
        return None

def login():
    r = api("POST", "/api/auth/login", {"email": ADMIN_EMAIL, "password": ADMIN_PASS})
    return r.get("token") if r else None

def create_test_key(token):
    r = api("POST", "/api/apikeys", {"name": "chat-model-test", "key_type": "general"}, token)
    return r.get("key"), r.get("id")

def delete_test_key(kid, token):
    api("DELETE", f"/api/apikeys/{kid}", token=token)

def fetch_models(token):
    r = api("GET", f"/api/admin/provider-models/fetch?provider=shixin", token=token)
    models = r.get("models", r.get("data", [])) if r else []
    ids = [m["id"] for m in models if m.get("id") and not m["id"].startswith(skip_prefixes)]
    return ids

def test_model(model, key):
    payload = json.dumps({
        "model": model,
        "messages": [{"role": "user", "content": "Hello! Who are you? Answer in one sentence."}],
        "max_tokens": 100
    }).encode()
    req = Request(f"{PROXY}/v1/chat/completions", data=payload,
                  headers={"Content-Type": "application/json", "Authorization": f"Bearer {key}"},
                  method="POST")
    start = time.time()
    try:
        with urlopen(req, timeout=30) as r:
            body = json.loads(r.read())
            dur = int((time.time() - start) * 1000)
            it = body.get("usage", {}).get("prompt_tokens", 0)
            ot = body.get("usage", {}).get("completion_tokens", 0)
            content = body.get("choices", [{}])[0].get("message", {}).get("content", "")[:200]
            return {"http_code": 200, "latency_ms": dur, "input_tokens": it, "output_tokens": ot, "content": content, "error": ""}
    except HTTPError as e:
        dur = int((time.time() - start) * 1000)
        body = e.read().decode()
        try:
            err_body = json.loads(body)
            err_msg = json.dumps(err_body.get("error", err_body))[:200]
        except:
            err_msg = body[:200]
        return {"http_code": e.code, "latency_ms": dur, "input_tokens": 0, "output_tokens": 0, "content": "", "error": err_msg}
    except Exception as e:
        dur = int((time.time() - start) * 1000)
        return {"http_code": 0, "latency_ms": dur, "input_tokens": 0, "output_tokens": 0, "content": "", "error": str(e)[:200]}

def clear_rate_limits():
    subprocess.run(
        ["docker", "exec", "ai-portal-redis", "redis-cli", "-a", REDIS_PASS, "KEYS", "*brute*"],
        capture_output=True
    )

def top_up_balance(uid):
    subprocess.run(
        ["docker", "exec", "ai-portal-postgres", "psql", "-U", "postgres", "-d", "ai_proxy_portal",
         "-c", f"UPDATE users SET recharge_balance=500000, balance=500000 WHERE id='{uid}'"],
        capture_output=True
    )

def main():
    print("=== Shixin Chat Models Test ===")
    print()
    
    token = login()
    if not token:
        print("FAIL: Login failed"); sys.exit(1)
    print(f"Login OK")
    
    models = fetch_models(token)
    print(f"Chat models to test: {len(models)}")
    if not models:
        print("No models to test"); return
    
    key, kid = create_test_key(token)
    if not key:
        print("FAIL: Create key failed"); sys.exit(1)
    print(f"Test key created: {key[:20]}...")
    
    admin_uid = subprocess.run(
        ["docker", "exec", "ai-portal-postgres", "psql", "-U", "postgres", "-d", "ai_proxy_portal",
         "-t", "-A", "-c", "SELECT id FROM users WHERE email='admin@example.com'"],
        capture_output=True, text=True
    ).stdout.strip()
    if admin_uid:
        top_up_balance(admin_uid)
    
    clear_rate_limits()
    
    results = []
    total = len(models)
    ok = bad = timeout = cooldown_hits = 0
    
    for i, model in enumerate(models, 1):
        print(f"  [{i}/{total}] {model} ... ", end="", flush=True)
        r = test_model(model, key)
        r["model"] = model
        results.append(r)
        
        if r["http_code"] == 200:
            print(f"OK {r['latency_ms']}ms in={r['input_tokens']} out={r['output_tokens']}")
            ok += 1
        elif r["http_code"] == 0:
            print(f"TIMEOUT {r['latency_ms']}ms err={r['error'][:80]}")
            timeout += 1
        else:
            is_429 = "429" in r["error"] or "负载已饱" in r["error"] or "too many" in r["error"].lower()
            if is_429:
                cooldown_hits += 1
                print(f"429 (cooling down {COOLDOWN_WAIT}s) err={r['error'][:60]}")
                time.sleep(COOLDOWN_WAIT)
            else:
                print(f"HTTP {r['http_code']} {r['latency_ms']}ms err={r['error'][:80]}")
                bad += 1
        
        time.sleep(0.5)
    
    # Cleanup
    if kid:
        delete_test_key(kid, token)
    
    print()
    print("=" * 50)
    print(f"  Results: {ok} OK, {bad} Failed, {timeout} Timeout, {cooldown_hits} cooldowns")
    print("=" * 50)
    print()
    
    # Generate report
    md = generate_report(results, models)
    
    # Save to file
    md_file = "/tmp/shixin-chat-test-report.md"
    with open(md_file, "w") as f:
        f.write(md)
    print(f"Report saved to {md_file}")

def generate_report(results, model_names):
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    total = len(results)
    success = [r for r in results if r.get("http_code") == 200]
    failed = [r for r in results if r.get("http_code") not in (0, 200)]
    timeouts = [r for r in results if r.get("http_code") == 0]
    sc = len(success)
    fc = len(failed)
    tc = len(timeouts)
    
    ti = sum(r.get("input_tokens", 0) for r in success)
    to = sum(r.get("output_tokens", 0) for r in success)
    
    latencies = [r["latency_ms"] for r in success if r.get("latency_ms")]
    avg_lat = sum(latencies) / len(latencies) if latencies else 0
    max_lat = max(latencies) if latencies else 0
    min_lat = min(latencies) if latencies else 0
    
    sr = f"{sc/total*100:.1f}" if total > 0 else "N/A"
    
    md = f"""# Shixin Chat Models 测试报告

**测试时间**: {now}
**Proxy**: `{PROXY}`
**测试模型总数**: {total}

## 总体统计

| 指标 | 值 |
|------|-----|
| 成功率 | {sr}% |
| 成功 | {sc} |
| 失败 | {fc} |
| 超时 | {tc} |
| 平均延迟 | {avg_lat:.0f}ms |
| 最低延迟 | {min_lat}ms |
| 最高延迟 | {max_lat}ms |
| 总输入 Token | {ti:,} |
| 总输出 Token | {to:,} |

"""
    
    if success:
        md += "## 成功的模型\n\n| 模型 | 延迟(ms) | 输入 Tokens | 输出 Tokens |\n|------|----------|-------------|-------------|\n"
        for r in sorted(success, key=lambda x: x["latency_ms"]):
            md += f"| `{r['model']}` | {r['latency_ms']} | {r.get('input_tokens',0)} | {r.get('output_tokens',0)} |\n"
        md += "\n"
    
    if failed:
        md += "## 失败的模型\n\n| 模型 | HTTP 状态 | 延迟(ms) | 错误信息 |\n|------|-----------|----------|----------|\n"
        for r in failed:
            err = (r.get("error") or "")[:100]
            md += f"| `{r['model']}` | {r['http_code']} | {r['latency_ms']} | {err} |\n"
        md += "\n"
    
    if timeouts:
        md += "## 超时的模型\n\n| 模型 | 延迟(ms) | 错误信息 |\n|------|----------|----------|\n"
        for r in timeouts:
            err = (r.get("error") or "")[:80]
            md += f"| `{r['model']}` | {r['latency_ms']} | {err} |\n"
        md += "\n"
    
    md += "## 全部模型详情\n\n| # | 模型 | 状态 | 延迟(ms) |\n|---|------|------|----------|\n"
    for i, r in enumerate(results, 1):
        if r["http_code"] == 200:
            status = "✅"
        elif r["http_code"] == 0:
            status = "⏰"
        else:
            status = "❌"
        md += f"| {i} | `{r['model']}` | {status} | {r['latency_ms']} |\n"
    
    md += "\n## 说明\n"
    md += f"- 遇到 accessapi 返回 429（负载饱和）时，等待 {COOLDOWN_WAIT}s 让 proxy cooldown 过期\n"
    md += "- 超时（HTTP 000）通常是 accessapi TLS 握手问题\n"
    md += "- 502 表示请求被代理路由到 accessapi 但 accessapi 返回了错误\n"
    
    return md

if __name__ == "__main__":
    main()
