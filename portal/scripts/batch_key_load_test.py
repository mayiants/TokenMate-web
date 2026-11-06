#!/usr/bin/env python3
"""
Batch API Key Creation & Load Test
- Creates N user API keys via Portal
- Tests each key via Proxy chat completion
- Excludes Claude models
"""

import concurrent.futures
import json
import sys
import time
import urllib.request
import urllib.error

PORTAL_URL = "http://127.0.0.1:3001"
PROXY_URL = "http://127.0.0.1:8080"
ADMIN_TOKEN = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoiNzk4MWVjNGYtMGFhOC00MmNjLThlODgtODE0MjIwMGZjYzlmIiwiZW1haWwiOiJhZG1pbkBleGFtcGxlLmNvbSIsInJvbGUiOiJzdXBlcl9hZG1pbiIsImV4cCI6MTc4MzE2ODMzNCwiaWF0IjoxNzgwNTc2MzM0fQ.JMXBuPfJEh6wwukBVCABxNoi2D0xn-PXsunX4ERAQxI"

TEST_MODEL = "deepseek.v3.2"
NUM_KEYS = 100
MAX_WORKERS_CREATE = 10
MAX_WORKERS_TEST = 20


def api_call(method, url, headers=None, body=None):
    req = urllib.request.Request(url, method=method)
    if headers:
        for k, v in headers.items():
            req.add_header(k, v)
    if body:
        req.add_header("Content-Type", "application/json")
        req.data = json.dumps(body).encode("utf-8")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.status, resp.read().decode("utf-8"), None
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8"), None
    except Exception as e:
        return 0, "", str(e)


def create_key(idx):
    """Create a single API key via Portal"""
    url = f"{PORTAL_URL}/api/apikeys"
    headers = {
        "Authorization": f"Bearer {ADMIN_TOKEN}",
        "Content-Type": "application/json",
    }
    body = {
        "name": f"load-test-key-{idx:04d}",
        "current_model": TEST_MODEL,
        "key_type": "general",
        "access_mode": "single_model",
    }
    status, resp, err = api_call("POST", url, headers, body)
    if status == 200:
        data = json.loads(resp)
        return {"idx": idx, "key": data["key"], "id": data["id"], "success": True}
    return {"idx": idx, "success": False, "status": status, "error": err or resp[:200]}


def test_key(key_info):
    """Test a single API key via Proxy chat completion"""
    key = key_info["key"]
    url = f"{PROXY_URL}/v1/chat/completions"
    headers = {
        "Authorization": f"Bearer {key}",
        "Content-Type": "application/json",
    }
    body = {
        "model": TEST_MODEL,
        "messages": [{"role": "user", "content": "Say hi in 3 words"}],
        "max_tokens": 20,
    }
    start = time.time()
    status, resp, err = api_call("POST", url, headers, body)
    latency = time.time() - start

    result = {
        "idx": key_info["idx"],
        "key_prefix": key[:20] + "...",
        "status": status,
        "latency": round(latency, 3),
        "error": err,
    }

    if status == 200:
        try:
            data = json.loads(resp)
            content = data.get("choices", [{}])[0].get("message", {}).get("content")
            result["content"] = content
            result["model"] = data.get("model")
            result["tokens"] = data.get("usage", {}).get("total_tokens", 0)
        except Exception as e:
            result["parse_error"] = str(e)
    else:
        result["response"] = resp[:300]

    return result


def main():
    print(f"=== Batch Key Load Test ===")
    print(f"Keys to create: {NUM_KEYS}")
    print(f"Test model: {TEST_MODEL}")
    print()

    # Phase 1: Create keys
    print("[Phase 1] Creating API keys...")
    created_keys = []
    failed_creates = []
    start = time.time()

    with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS_CREATE) as ex:
        futures = {ex.submit(create_key, i): i for i in range(NUM_KEYS)}
        for future in concurrent.futures.as_completed(futures):
            res = future.result()
            if res["success"]:
                created_keys.append(res)
            else:
                failed_creates.append(res)
            if (res["idx"] + 1) % 10 == 0:
                print(f"  Created: {len(created_keys)}, Failed: {len(failed_creates)}")

    create_time = time.time() - start
    print(f"\nCreated: {len(created_keys)}, Failed: {len(failed_creates)} in {create_time:.1f}s")
    if failed_creates:
        print(f"First failure: {failed_creates[0]}")

    if not created_keys:
        print("No keys created, aborting.")
        sys.exit(1)

    # Phase 2: Test keys
    print(f"\n[Phase 2] Testing {len(created_keys)} keys via Proxy...")
    results = []
    failed_tests = []
    start = time.time()

    with concurrent.futures.ThreadPoolExecutor(max_workers=MAX_WORKERS_TEST) as ex:
        futures = {ex.submit(test_key, k): k for k in created_keys}
        for future in concurrent.futures.as_completed(futures):
            res = future.result()
            results.append(res)
            if res["status"] != 200:
                failed_tests.append(res)
            if (len(results)) % 10 == 0:
                print(f"  Tested: {len(results)}, Failed: {len(failed_tests)}")

    test_time = time.time() - start

    # Phase 3: Report
    print(f"\n{'='*50}")
    print("RESULTS SUMMARY")
    print(f"{'='*50}")
    print(f"Keys created: {len(created_keys)} / {NUM_KEYS}")
    print(f"Create time:  {create_time:.1f}s ({len(created_keys)/create_time:.1f} keys/s)")
    print(f"Tests run:    {len(results)}")
    print(f"Success:      {len([r for r in results if r['status']==200])}")
    print(f"Failed:       {len(failed_tests)}")
    print(f"Test time:    {test_time:.1f}s ({len(results)/test_time:.1f} req/s)")

    if results:
        latencies = [r["latency"] for r in results if r["status"] == 200]
        if latencies:
            print(f"Avg latency:  {sum(latencies)/len(latencies):.3f}s")
            print(f"Min latency:  {min(latencies):.3f}s")
            print(f"Max latency:  {max(latencies):.3f}s")

    if failed_tests:
        print(f"\n--- Sample failures ---")
        for r in failed_tests[:5]:
            print(f"  Key {r['idx']}: HTTP {r['status']} latency={r['latency']}s err={r.get('error','')[:100]} resp={r.get('response','')[:100]}")

    # Save results
    with open("/tmp/batch_key_test_results.json", "w") as f:
        json.dump({"created": created_keys, "results": results, "failed_creates": failed_creates}, f, indent=2)
    print(f"\nResults saved to /tmp/batch_key_test_results.json")


if __name__ == "__main__":
    main()
