#!/usr/bin/env python3
"""
AI Platform Performance Test Script
Creates test data and runs load tests through the real proxy middleware chain.

Usage:
  python3 test/perf-test-202605141900.py --keys 1000
  python3 test/perf-test-202605141900.py --keys 2000
  python3 test/perf-test-202605141900.py --keys 5000
"""

import argparse
import json
import os
import sys
import time
import threading
import statistics
import concurrent.futures
import urllib.request
import urllib.error

# Configuration
PORTAL_URL = os.environ.get("PORTAL_URL", "http://127.0.0.1:3001")
PROXY_URL = os.environ.get("PROXY_URL", "http://127.0.0.1:8080")
ADMIN_EMAIL = os.environ.get("ADMIN_EMAIL", "admin@example.com")
ADMIN_PASSWORD = os.environ.get("ADMIN_PASSWORD", "Admin@123456!")

# Test parameters
SIMULATE_LATENCY_MS = int(os.environ.get("SIMULATE_LATENCY_MS", "0"))  # 0 for max throughput test


def get_admin_token():
    """Login as admin and get JWT token"""
    data = json.dumps({"email": ADMIN_EMAIL, "password": ADMIN_PASSWORD}).encode()
    req = urllib.request.Request(
        f"{PORTAL_URL}/api/auth/login",
        data=data,
        headers={"Content-Type": "application/json"},
        method="POST"
    )
    with urllib.request.urlopen(req) as resp:
        result = json.loads(resp.read())
    return result["token"]


def create_test_data(admin_token, num_keys):
    """Create provider, models, upstream key, and user API keys"""
    headers = {
        "Authorization": f"Bearer {admin_token}",
        "Content-Type": "application/json"
    }

    # 1. Create provider (if not exists)
    print("  Creating provider 'simulate'...")
    try:
        data = json.dumps({
            "id": "simulate",
            "name": "Simulate Provider",
            "base_url": "https://api.simulate.local/v1",
            "enabled": True
        }).encode()
        req = urllib.request.Request(
            f"{PORTAL_URL}/api/admin/providers",
            data=data, headers=headers, method="POST"
        )
        with urllib.request.urlopen(req) as resp:
            print(f"  Provider created: {json.loads(resp.read())}")
    except urllib.error.HTTPError as e:
        if e.code == 409 or e.code == 400:
            print("  Provider already exists, skipping")
        else:
            print(f"  Provider creation error: {e.code} {e.read().decode()}")

    # 2. Create service models
    models_to_create = [
        {
            "provider": "simulate",
            "product_id": "gpt-4o",
            "api_model_name": "gpt-4o",
            "input_price": 2.5,
            "output_price": 10.0,
            "status": "active",
            "show_in_store": True,
            "aimodels_data": {"name": "GPT-4o", "description": "GPT-4o simulate model"}
        },
        {
            "provider": "simulate",
            "product_id": "claude-3.5-sonnet",
            "api_model_name": "claude-3-5-sonnet-20241022",
            "input_price": 3.0,
            "output_price": 15.0,
            "status": "active",
            "show_in_store": True,
            "aimodels_data": {"name": "Claude 3.5 Sonnet", "description": "Claude 3.5 Sonnet simulate model"}
        }
    ]

    for model in models_to_create:
        print(f"  Creating model '{model['product_id']}'...")
        try:
            data = json.dumps(model).encode()
            req = urllib.request.Request(
                f"{PORTAL_URL}/api/admin/service-models",
                data=data, headers=headers, method="POST"
            )
            with urllib.request.urlopen(req) as resp:
                print(f"  Model created: {json.loads(resp.read())}")
        except urllib.error.HTTPError as e:
            if e.code == 409 or e.code == 400:
                print(f"  Model '{model['product_id']}' already exists, skipping")
            else:
                print(f"  Model creation error: {e.code} {e.read().decode()}")

    # 3. Ensure admin has balance
    print("  Ensuring admin has sufficient balance...")
    try:
        # Get admin user ID
        req = urllib.request.Request(
            f"{PORTAL_URL}/api/admin/users",
            headers=headers
        )
        with urllib.request.urlopen(req) as resp:
            users = json.loads(resp.read()).get("users", [])
            admin_user = users[0]
            admin_id = admin_user["id"]
            admin_balance = admin_user.get("balance", 0)

        if admin_balance < 10000:
            data = json.dumps({"balance": 100000}).encode()
            req = urllib.request.Request(
                f"{PORTAL_URL}/api/admin/users/{admin_id}",
                data=data, headers=headers, method="PATCH"
            )
            with urllib.request.urlopen(req) as resp:
                print(f"  Balance updated: {json.loads(resp.read())}")
        else:
            print(f"  Balance already sufficient: {admin_balance}")
    except Exception as e:
        print(f"  Balance update error: {e}")

    # 4. Create user API keys
    print(f"  Creating {num_keys} API keys...")
    api_keys = []
    batch_size = 50
    created = 0

    for batch_start in range(0, num_keys, batch_size):
        batch_end = min(batch_start + batch_size, num_keys)
        for i in range(batch_start, batch_end):
            try:
                data = json.dumps({"name": f"perf-test-{i:05d}"}).encode()
                req = urllib.request.Request(
                    f"{PORTAL_URL}/api/apikeys",
                    data=data, headers=headers, method="POST"
                )
                with urllib.request.urlopen(req) as resp:
                    result = json.loads(resp.read())
                    key = result.get("key", "")
                    if key:
                        api_keys.append(key)
                    created += 1
            except urllib.error.HTTPError as e:
                print(f"  Key creation error at {i}: {e.code}")
            except Exception as e:
                print(f"  Key creation error at {i}: {e}")

        if batch_end % 500 == 0 or batch_end == num_keys:
            print(f"  Created {created}/{num_keys} keys...")

    print(f"  Successfully created {len(api_keys)} API keys")
    return api_keys


def run_load_test(api_keys, num_requests, concurrency, model="gpt-4o"):
    """Run load test through the real proxy middleware chain"""
    print(f"\n  Load test: {num_requests} requests, {concurrency} concurrent, model={model}")

    latencies = []
    success_count = 0
    error_count = 0
    rate_limit_count = 0
    auth_error_count = 0
    simulate_count = 0

    request_body = json.dumps({
        "model": model,
        "messages": [{"role": "user", "content": "hello"}]
    }).encode()

    def send_request(key_idx):
        key = api_keys[key_idx % len(api_keys)]
        start_time = time.time()

        req = urllib.request.Request(
            f"{PROXY_URL}/v1/chat/completions",
            data=request_body,
            headers={
                "Authorization": f"Bearer {key}",
                "Content-Type": "application/json"
            },
            method="POST"
        )

        try:
            with urllib.request.urlopen(req) as resp:
                body = resp.read().decode()
                latency = time.time() - start_time
                result = json.loads(body)

                # Check if it's a simulate response
                if result.get("model") == model or "Simulated" in str(result.get("choices", "")):
                    simulate_count_local = 1
                else:
                    simulate_count_local = 0

                return {
                    "success": True,
                    "latency": latency,
                    "status": resp.status,
                    "simulate": simulate_count_local,
                    "body": result
                }
        except urllib.error.HTTPError as e:
            latency = time.time() - start_time
            body = e.read().decode()
            try:
                result = json.loads(body)
                error_code = result.get("error", {}).get("code", "")
            except:
                error_code = ""
                result = {}

            if e.code == 429:
                return {"success": False, "latency": latency, "status": 429, "rate_limit": True, "error_code": error_code}
            elif e.code == 401:
                return {"success": False, "latency": latency, "status": 401, "auth_error": True, "error_code": error_code}
            else:
                return {"success": False, "latency": latency, "status": e.code, "error_code": error_code}
        except Exception as e:
            latency = time.time() - start_time
            return {"success": False, "latency": latency, "status": 0, "error": str(e)}

    start_time = time.time()

    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as executor:
        futures = []
        for i in range(num_requests):
            futures.append(executor.submit(send_request, i))

        for future in concurrent.futures.as_completed(futures):
            result = future.result()
            latencies.append(result["latency"])

            if result["success"]:
                success_count += 1
                if result.get("simulate"):
                    simulate_count += 1
            else:
                error_count += 1
                if result.get("rate_limit"):
                    rate_limit_count += 1
                elif result.get("auth_error"):
                    auth_error_count += 1

    elapsed = time.time() - start_time
    rps = num_requests / elapsed if elapsed > 0 else 0

    # Calculate latency distribution
    latencies.sort()
    p50 = latencies[int(len(latencies) * 0.50)] if latencies else 0
    p90 = latencies[int(len(latencies) * 0.90)] if latencies else 0
    p99 = latencies[int(len(latencies) * 0.99)] if latencies else 0
    avg = statistics.mean(latencies) if latencies else 0
    min_lat = latencies[0] if latencies else 0
    max_lat = latencies[-1] if latencies else 0

    results = {
        "total_requests": num_requests,
        "concurrency": concurrency,
        "model": model,
        "elapsed_seconds": elapsed,
        "rps": rps,
        "success_count": success_count,
        "error_count": error_count,
        "rate_limit_count": rate_limit_count,
        "auth_error_count": auth_error_count,
        "simulate_count": simulate_count,
        "latency": {
            "min_ms": min_lat * 1000,
            "avg_ms": avg * 1000,
            "p50_ms": p50 * 1000,
            "p90_ms": p90 * 1000,
            "p99_ms": p99 * 1000,
            "max_ms": max_lat * 1000,
        }
    }

    print(f"  Results:")
    print(f"    RPS: {rps:.1f}")
    print(f"    Success: {success_count}, Errors: {error_count}")
    print(f"    Rate limited (429): {rate_limit_count}")
    print(f"    Auth errors (401): {auth_error_count}")
    print(f"    Simulate responses: {simulate_count}")
    print(f"    Latency P50: {p50*1000:.1f}ms, P90: {p90*1000:.1f}ms, P99: {p99*1000:.1f}ms")
    print(f"    Latency Min: {min_lat*1000:.1f}ms, Max: {max_lat*1000:.1f}ms, Avg: {avg*1000:.1f}ms")

    return results


def check_metrics():
    """Check Portal and Proxy metrics"""
    print("\n  === Portal Metrics ===")
    try:
        req = urllib.request.Request(f"{PORTAL_URL}/metrics")
        with urllib.request.urlopen(req) as resp:
            metrics = resp.read().decode()
            for line in metrics.split("\n"):
                if not line.startswith("#") and line.strip():
                    print(f"    {line}")
    except Exception as e:
        print(f"    Error: {e}")

    print("\n  === Proxy Metrics ===")
    try:
        req = urllib.request.Request(f"{PROXY_URL}/metrics")
        with urllib.request.urlopen(req) as resp:
            metrics = resp.read().decode()
            for line in metrics.split("\n"):
                if not line.startswith("#") and line.strip():
                    print(f"    {line}")
    except Exception as e:
        print(f"    Error: {e}")


def check_health():
    """Check Portal and Proxy health"""
    print("\n  === Portal Health ===")
    try:
        req = urllib.request.Request(f"{PORTAL_URL}/api/health")
        with urllib.request.urlopen(req) as resp:
            health = json.loads(resp.read())
            print(f"    Status: {health.get('status')}")
            deps = health.get("dependencies", {})
            for dep, info in deps.items():
                print(f"    {dep}: {info.get('status', 'unknown')}")
    except Exception as e:
        print(f"    Error: {e}")

    print("\n  === Proxy Health ===")
    try:
        req = urllib.request.Request(f"{PROXY_URL}/health")
        with urllib.request.urlopen(req) as resp:
            health = json.loads(resp.read())
            print(f"    Status: {health.get('status')}")
            deps = health.get("dependencies", {})
            for dep, info in deps.items():
                print(f"    {dep}: {info.get('status', info if isinstance(info, str) else 'unknown')}")
    except Exception as e:
        print(f"    Error: {e}")


def main():
    parser = argparse.ArgumentParser(description="AI Platform Performance Test")
    parser.add_argument("--keys", type=int, default=100, help="Number of API keys to create")
    parser.add_argument("--requests", type=int, default=1000, help="Total requests per test")
    parser.add_argument("--concurrency", type=int, default=50, help="Concurrent workers")
    parser.add_argument("--skip-setup", action="store_true", help="Skip data setup (use existing keys)")
    parser.add_argument("--model", type=str, default="gpt-4o", help="Model to test")
    args = parser.parse_args()

    print("=" * 60)
    print("  AI Platform Performance Test")
    print("=" * 60)
    print(f"  Portal: {PORTAL_URL}")
    print(f"  Proxy:  {PROXY_URL}")
    print(f"  Keys:   {args.keys}")
    print(f"  Requests per test: {args.requests}")
    print(f"  Concurrency: {args.concurrency}")
    print(f"  Model: {args.model}")
    print()

    # Step 1: Health check
    print("[Step 1] Health Check")
    check_health()

    # Step 2: Create test data
    print("\n[Step 2] Creating Test Data")
    admin_token = get_admin_token()
    print(f"  Admin token obtained (length: {len(admin_token)})")

    if not args.skip_setup:
        api_keys = create_test_data(admin_token, args.keys)
    else:
        # Load existing keys from file or get from API
        print("  Skipping setup, using existing keys...")
        # Get keys from API
        headers = {"Authorization": f"Bearer {admin_token}"}
        req = urllib.request.Request(f"{PORTAL_URL}/api/apikeys", headers=headers)
        with urllib.request.urlopen(req) as resp:
            result = json.loads(resp.read())
            # We can't get full keys from list, need to create new ones
            print("  Note: Cannot retrieve full key values from list API")
            print("  Creating new keys instead...")
            api_keys = create_test_data(admin_token, args.keys)

    if len(api_keys) == 0:
        print("  ERROR: No API keys available for testing")
        sys.exit(1)

    print(f"  Available keys for testing: {len(api_keys)}")

    # Step 3: Wait for proxy to sync
    print("\n[Step 3] Waiting for Proxy Sync")
    print("  Waiting 5 seconds for proxy to sync keys...")
    time.sleep(5)

    # Verify a key works
    test_key = api_keys[0]
    print(f"  Testing key: {test_key[:20]}...")
    request_body = json.dumps({
        "model": args.model,
        "messages": [{"role": "user", "content": "hello"}]
    }).encode()
    req = urllib.request.Request(
        f"{PROXY_URL}/v1/chat/completions",
        data=request_body,
        headers={
            "Authorization": f"Bearer {test_key}",
            "Content-Type": "application/json"
        },
        method="POST"
    )
    try:
        with urllib.request.urlopen(req) as resp:
            result = json.loads(resp.read().decode())
            print(f"  Test response: model={result.get('model')}, status={resp.status}")
            if result.get("choices"):
                content = result["choices"][0].get("message", {}).get("content", "")
                print(f"  Content: {content[:50]}...")
    except urllib.error.HTTPError as e:
        print(f"  Test failed: {e.code} {e.read().decode()[:100]}")
        sys.exit(1)

    # Step 4: Run performance tests
    print("\n[Step 4] Performance Tests")

    all_results = []

    # Test 1: Low concurrency (10 workers)
    print("\n  --- Test 1: Low Concurrency (10 workers) ---")
    r1 = run_load_test(api_keys, args.requests, 10, args.model)
    all_results.append(("low_concurrency_10", r1))

    # Test 2: Medium concurrency (50 workers)
    print("\n  --- Test 2: Medium Concurrency (50 workers) ---")
    r2 = run_load_test(api_keys, args.requests, 50, args.model)
    all_results.append(("medium_concurrency_50", r2))

    # Test 3: High concurrency (100 workers)
    print("\n  --- Test 3: High Concurrency (100 workers) ---")
    r3 = run_load_test(api_keys, args.requests, 100, args.model)
    all_results.append(("high_concurrency_100", r3))

    # Test 4: Max concurrency (200 workers)
    print("\n  --- Test 4: Max Concurrency (200 workers) ---")
    r4 = run_load_test(api_keys, min(args.requests * 2, 5000), 200, args.model)
    all_results.append(("max_concurrency_200", r4))

    # Step 5: Check metrics after load
    print("\n[Step 5] Metrics After Load Test")
    check_metrics()

    # Step 6: Generate report
    print("\n[Step 6] Performance Report Summary")
    print("=" * 60)
    print(f"  Test Date: {time.strftime('%Y-%m-%d %H:%M:%S')}")
    print(f"  Keys: {len(api_keys)}, Model: {args.model}")
    print(f"  Simulate Mode: Enabled (latency={SIMULATE_LATENCY_MS}ms)")
    print()

    print(f"  {'Test':<25} {'RPS':>10} {'P50(ms)':>10} {'P90(ms)':>10} {'P99(ms)':>10} {'Success':>10} {'429':>6} {'401':>6}")
    print("-" * 80)
    for name, r in all_results:
        print(f"  {name:<25} {r['rps']:>10.1f} {r['latency']['p50_ms']:>10.1f} {r['latency']['p90_ms']:>10.1f} {r['latency']['p99_ms']:>10.1f} {r['success_count']:>10} {r['rate_limit_count']:>6} {r['auth_error_count']:>6}")

    print()
    print("  Real middleware chain exercised:")
    print("    - Auth middleware (API key validation via Portal)")
    print("    - Rate limit middleware (RPM/RPH/RPD checks)")
    print("    - Affinity cache lookup")
    print("    - Key pool selection")
    print("    - Circuit breaker check")
    print("    - Simulate mode response")
    print("    - Token usage recording")
    print("=" * 60)

    # Save results to JSON
    report_file = f"test/perf-test-results-{time.strftime('%Y%m%d%H%M')}.json"
    os.makedirs("test", exist_ok=True)
    with open(report_file, "w") as f:
        json.dump({
            "test_date": time.strftime("%Y-%m-%d %H:%M:%S"),
            "portal_url": PORTAL_URL,
            "proxy_url": PROXY_URL,
            "num_keys": len(api_keys),
            "model": args.model,
            "simulate_latency_ms": SIMULATE_LATENCY_MS,
            "results": {name: r for name, r in all_results}
        }, f, indent=2)
    print(f"\n  Results saved to: {report_file}")


if __name__ == "__main__":
    main()