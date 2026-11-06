#!/usr/bin/env python3
"""Minimal mock Portal for ai-proxy-go E2E tests.

Endpoints:
  GET  /api/apikeys/validate       -> validate any bearer token
  GET  /api/admin/key-pool/sync    -> return mock keys/providers/routing
  GET  /api/admin/models/sync      -> return mock service models
  POST /api/webhook/usage-batch    -> accept usage reports
  POST /api/webhook/heartbeat      -> accept heartbeats
"""
import json
import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


def make_response(data):
    return json.dumps(data).encode("utf-8")


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        # Suppress verbose request logging; failures still go to stderr via exceptions.
        pass

    def _send_json(self, status, data):
        body = make_response(data)
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _read_body(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0:
            return b""
        return self.rfile.read(length)

    def do_GET(self):
        upstream = os.environ.get("MOCK_UPSTREAM_URL", "http://localhost:9091")
        openai_base = f"{upstream}/v1"
        anthropic_base = f"{upstream}/anthropic/v1"

        if self.path == "/api/apikeys/validate":
            self._send_json(200, {
                "valid": True,
                "user_id": "e2e-user",
                "api_key_id": "e2e-key",
                "role": "user",
                "access_mode": "standard",
                "allowed_models": ["gpt-4o", "claude-3-sonnet", "o1"],
                "rate_limit_rpm": 100000,
                "rate_limit_tpm": 100000000,
                "balance": 1000.0,
                "quota_exceeded": False,
                "quota_window": "",
                "group_id": "e2e-group",
                "key_type": "enterprise",
                "current_model": "",
                "billing_mode": "postpaid",
                "pricing_ratio": 1.0,
                "group_priority": [],
                "models": [],
                "model_routing_mode": "single",
                "selected_models": [],
            })
            return

        if self.path == "/api/admin/key-pool/sync":
            self._send_json(200, {
                "keys": [
                    {
                        "id": "openai-key1",
                        "provider": "openai",
                        "key": "sk-mock-openai",
                        "key_preview": "sk...openai",
                        "tier": "standard",
                        "priority": "normal",
                        "baseUrl": "",
                        "models": ["gpt-4o", "o1"],
                        "enabled": True,
                        "assignmentMode": "manual",
                        "isShared": False,
                    },
                    {
                        "id": "anthropic-key1",
                        "provider": "anthropic",
                        "key": "sk-mock-anthropic",
                        "key_preview": "sk...anthropic",
                        "tier": "standard",
                        "priority": "normal",
                        "baseUrl": "",
                        "models": ["claude-3-sonnet"],
                        "enabled": True,
                        "assignmentMode": "manual",
                        "isShared": False,
                    },
                ],
                "providers": [
                    {"id": "openai", "name": "OpenAI", "baseUrl": openai_base, "enabled": True},
                    {"id": "anthropic", "name": "Anthropic", "baseUrl": anthropic_base, "enabled": True},
                ],
                "model_routing": [
                    {"model_name": "gpt-4o", "provider": "openai", "priority": 1, "enabled": True},
                    {"model_name": "o1", "provider": "openai", "priority": 1, "enabled": True},
                    {"model_name": "claude-3-sonnet", "provider": "anthropic", "priority": 1, "enabled": True},
                ],
                "model_name_map": [
                    {
                        "provider": "openai",
                        "product_id": "gpt-4o",
                        "api_model_name": "gpt-4o",
                        "capabilities": {"chat_completions": True, "responses": False},
                    },
                    {
                        "provider": "openai",
                        "product_id": "o1",
                        "api_model_name": "o1",
                        "capabilities": {"chat_completions": False, "responses": True},
                    },
                    {
                        "provider": "anthropic",
                        "product_id": "claude-3-sonnet",
                        "api_model_name": "claude-3-sonnet",
                        "capabilities": {"chat_completions": True, "responses": False},
                    },
                ],
                "provider_health": [],
                "key_health": [],
                "smart_routing": {},
            })
            return

        if self.path == "/api/admin/models/sync":
            self._send_json(200, {
                "models": [
                    {
                        "id": "gpt-4o",
                        "provider": "openai",
                        "productId": "gpt-4o",
                        "apiModelName": "gpt-4o",
                        "status": "active",
                        "inputPrice": 0,
                        "outputPrice": 0,
                        "validationStatus": "valid",
                        "showInStore": True,
                    },
                    {
                        "id": "o1",
                        "provider": "openai",
                        "productId": "o1",
                        "apiModelName": "o1",
                        "status": "active",
                        "inputPrice": 0,
                        "outputPrice": 0,
                        "validationStatus": "valid",
                        "showInStore": True,
                    },
                    {
                        "id": "claude-3-sonnet",
                        "provider": "anthropic",
                        "productId": "claude-3-sonnet",
                        "apiModelName": "claude-3-sonnet",
                        "status": "active",
                        "inputPrice": 0,
                        "outputPrice": 0,
                        "validationStatus": "valid",
                        "showInStore": True,
                    },
                ]
            })
            return

        self._send_json(404, {"error": "not found"})

    def do_POST(self):
        self._read_body()
        if self.path.startswith("/api/webhook/"):
            self._send_json(200, {"ok": True})
            return
        self._send_json(404, {"error": "not found"})


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9090
    server = HTTPServer(("127.0.0.1", port), Handler)
    print(f"mock-portal-ready:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
