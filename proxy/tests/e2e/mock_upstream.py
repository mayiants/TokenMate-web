#!/usr/bin/env python3
"""Mock upstream LLM provider for ai-proxy-go E2E tests.

Records every request body to the capture directory and returns a minimal
valid response so the proxy can complete the request/response cycle.
"""
import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, HTTPServer


def capture_dir():
    return os.environ.get("CAPTURE_DIR", os.getcwd())


def save_capture(label, body):
    d = capture_dir()
    os.makedirs(d, exist_ok=True)
    idx = 1
    while True:
        path = os.path.join(d, f"{label}_{idx:03d}.json")
        if not os.path.exists(path):
            break
        idx += 1
    with open(path, "wb") as f:
        f.write(body)
    # Also write a stable "latest" file for easier shell assertions.
    latest = os.path.join(d, f"{label}_latest.json")
    with open(latest, "wb") as f:
        f.write(body)
    return path


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass

    def _send(self, status, body, content_type="application/json"):
        if isinstance(body, str):
            body = body.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _read_body(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length <= 0:
            return b""
        return self.rfile.read(length)

    def _openai_chat_response(self, body):
        req = json.loads(body.decode("utf-8")) if body else {}
        is_stream = req.get("stream") is True
        if is_stream:
            chunk = {
                "id": "chatcmpl-e2e",
                "object": "chat.completion.chunk",
                "created": int(time.time()),
                "model": req.get("model", "gpt-4o"),
                "choices": [
                    {"index": 0, "delta": {"role": "assistant", "content": "Hello"}, "finish_reason": None}
                ],
            }
            sse = f"data: {json.dumps(chunk)}\n\ndata: [DONE]\n\n"
            return sse.encode("utf-8"), "text/event-stream"

        resp = {
            "id": "chatcmpl-e2e",
            "object": "chat.completion",
            "created": int(time.time()),
            "model": req.get("model", "gpt-4o"),
            "choices": [
                {
                    "index": 0,
                    "message": {"role": "assistant", "content": "Hello"},
                    "finish_reason": "stop",
                }
            ],
            "usage": {"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12},
        }
        return json.dumps(resp).encode("utf-8"), "application/json"

    def _anthropic_messages_response(self, body):
        req = json.loads(body.decode("utf-8")) if body else {}
        resp = {
            "id": "msg-e2e",
            "type": "message",
            "role": "assistant",
            "model": req.get("model", "claude-3-sonnet"),
            "content": [{"type": "text", "text": "Hello"}],
            "stop_reason": "end_turn",
            "usage": {"input_tokens": 10, "output_tokens": 2},
        }
        return json.dumps(resp).encode("utf-8"), "application/json"

    def _responses_response(self, body):
        req = json.loads(body.decode("utf-8")) if body else {}
        resp = {
            "id": "resp-e2e",
            "object": "response",
            "created_at": int(time.time()),
            "model": req.get("model", "o1"),
            "status": "completed",
            "output": [
                {
                    "type": "message",
                    "role": "assistant",
                    "content": [{"type": "output_text", "text": "Hello"}],
                }
            ],
            "usage": {"input_tokens": 10, "output_tokens": 2, "total_tokens": 12},
        }
        return json.dumps(resp).encode("utf-8"), "application/json"

    def do_GET(self):
        if self.path == "/_health":
            self._send(200, b'{"ok":true}')
            return
        self._send(404, b'{"error":"not found"}')

    def do_POST(self):
        body = self._read_body()
        path = self.path

        if path == "/v1/chat/completions":
            save_capture("openai_chat", body)
            resp_body, ctype = self._openai_chat_response(body)
            self._send(200, resp_body, ctype)
            return

        if path == "/anthropic/v1/messages":
            save_capture("anthropic_messages", body)
            resp_body, ctype = self._anthropic_messages_response(body)
            self._send(200, resp_body, ctype)
            return

        if path == "/v1/responses":
            save_capture("responses", body)
            resp_body, ctype = self._responses_response(body)
            self._send(200, resp_body, ctype)
            return

        self._send(404, b'{"error":"not found"}')


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 9091
    server = HTTPServer(("127.0.0.1", port), Handler)
    print(f"mock-upstream-ready:{port}", flush=True)
    server.serve_forever()


if __name__ == "__main__":
    main()
