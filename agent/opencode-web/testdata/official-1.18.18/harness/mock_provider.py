#!/usr/bin/env python3
"""Local OpenAI-compatible mock. Never contacts an external network."""

from __future__ import annotations

import json
import os
import re
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse

HOST = os.environ.get("OCW_MOCK_HOST", "127.0.0.1")
PORT = int(os.environ.get("OCW_MOCK_PORT", "4399"))
OUTSIDE_FILE = os.environ.get("OCW_OUTSIDE_FILE", "/tmp/ocw-gate-a-outside.txt")
LOG_PATH = os.environ.get("OCW_MOCK_LOG", "")

_lock = threading.Lock()
_requests: list[dict] = []
_a3_calls = 0
_a8_writes = 0
_a8_updates = 0


def _log(event: dict) -> None:
    if not LOG_PATH:
        return
    with open(LOG_PATH, "a", encoding="utf-8") as fh:
        fh.write(json.dumps(event, ensure_ascii=False) + "\n")


def _user_text(body: dict) -> str:
    chunks: list[str] = []
    for msg in body.get("messages") or []:
        if not isinstance(msg, dict):
            continue
        role = msg.get("role")
        content = msg.get("content")
        if role != "user":
            continue
        if isinstance(content, str):
            chunks.append(content)
        elif isinstance(content, list):
            for part in content:
                if isinstance(part, dict) and part.get("type") in ("text", "input_text"):
                    chunks.append(str(part.get("text") or ""))
    return "\n".join(chunks)


def _is_title(body: dict) -> bool:
    for msg in body.get("messages") or []:
        if not isinstance(msg, dict):
            continue
        content = msg.get("content")
        text = content if isinstance(content, str) else json.dumps(content, ensure_ascii=False)
        if re.search(r"\btitle\b", text, re.I) and len(text) < 4000:
            if "A1_" in text or "A2_" in text or "A3_" in text:
                continue
            if msg.get("role") in ("system", "user"):
                if "Generate" in text or "title" in text.lower():
                    return True
    return False


def _msg_has_tool_result(msg: dict) -> bool:
    if msg.get("role") in ("tool", "function"):
        return True
    content = msg.get("content")
    if isinstance(content, list):
        for part in content:
            if isinstance(part, dict) and part.get("type") in ("tool-result", "tool_result", "function"):
                return True
    return bool(msg.get("tool_call_id") or msg.get("toolCallId"))


def _latest_turn_has_tool_result(body: dict) -> bool:
    """Only the messages after the last user turn count.

    Earlier turns leave tool results in history; using the whole transcript
    would loop todowrite_update forever on the second A8 prompt.
    """
    messages = body.get("messages") or []
    last_user = -1
    for i, msg in enumerate(messages):
        if isinstance(msg, dict) and msg.get("role") == "user":
            last_user = i
    if last_user < 0:
        return False
    for msg in messages[last_user + 1 :]:
        if isinstance(msg, dict) and _msg_has_tool_result(msg):
            return True
    return False


def _content_part_types(body: dict) -> list[str]:
    types: list[str] = []
    for msg in body.get("messages") or []:
        if not isinstance(msg, dict):
            continue
        content = msg.get("content")
        if isinstance(content, str):
            types.append("text")
        elif isinstance(content, list):
            for part in content:
                if isinstance(part, dict) and part.get("type"):
                    types.append(str(part.get("type")))
                elif isinstance(part, str):
                    types.append("text")
        if msg.get("tool_calls"):
            types.append("tool_calls")
    return types


def _tool_names(body: dict) -> list[str]:
    names: list[str] = []
    for tool in body.get("tools") or []:
        if not isinstance(tool, dict):
            continue
        fn = tool.get("function") if isinstance(tool.get("function"), dict) else tool
        if isinstance(fn, dict) and fn.get("name"):
            names.append(str(fn.get("name")))
    return names


def _scenario(body: dict) -> str:
    text = _user_text(body)
    if _is_title(body):
        return "title"
    if _latest_turn_has_tool_result(body):
        return "after_tool"
    if "A3_PROVIDER_ERROR" in text:
        return "error"
    if "A4_SLOW_STREAM" in text:
        return "slow"
    if "A5_RECONNECT" in text:
        return "reconnect"
    if "A6_READ_OUTSIDE" in text:
        return "read_outside"
    if "A7_QUESTION" in text:
        return "question"
    if "A8_TODOWRITE_UPDATE" in text:
        return "todowrite_update"
    if "A8_TODOWRITE" in text:
        return "todowrite"
    return "text"


def _sse(handler: BaseHTTPRequestHandler, chunks: list[str], delay: float = 0.0) -> None:
    handler.close_connection = True
    handler.send_response(200)
    handler.send_header("Content-Type", "text/event-stream")
    handler.send_header("Cache-Control", "no-cache")
    handler.send_header("Connection", "close")
    handler.end_headers()
    for chunk in chunks:
        if delay:
            time.sleep(delay)
        handler.wfile.write(f"data: {chunk}\n\n".encode("utf-8"))
        handler.wfile.flush()
    handler.wfile.write(b"data: [DONE]\n\n")
    handler.wfile.flush()


def _text_chunks(text: str) -> list[str]:
    out = []
    first = True
    for i, ch in enumerate(text):
        delta = {"content": ch}
        if first:
            delta["role"] = "assistant"
            first = False
        out.append(
            json.dumps(
                {
                    "id": "chatcmpl-mock",
                    "object": "chat.completion.chunk",
                    "choices": [{"index": 0, "delta": delta, "finish_reason": None}],
                }
            )
        )
    out.append(
        json.dumps(
            {
                "id": "chatcmpl-mock",
                "object": "chat.completion.chunk",
                "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 8, "completion_tokens": len(text), "total_tokens": 8 + len(text)},
            }
        )
    )
    return out


def _tool_chunks(name: str, arguments: dict, call_id: str = "call_mock1") -> list[str]:
    payload = json.dumps(arguments, ensure_ascii=False)
    return [
        json.dumps(
            {
                "id": "chatcmpl-mock",
                "object": "chat.completion.chunk",
                "choices": [
                    {
                        "index": 0,
                        "delta": {
                            "role": "assistant",
                            "tool_calls": [
                                {
                                    "index": 0,
                                    "id": call_id,
                                    "type": "function",
                                    "function": {"name": name, "arguments": payload},
                                }
                            ],
                        },
                        "finish_reason": None,
                    }
                ],
            }
        ),
        json.dumps(
            {
                "id": "chatcmpl-mock",
                "object": "chat.completion.chunk",
                "choices": [{"index": 0, "delta": {}, "finish_reason": "tool_calls"}],
            }
        ),
    ]


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt: str, *args) -> None:
        return

    def _json(self, code: int, body: dict) -> None:
        raw = json.dumps(body).encode("utf-8")
        self.close_connection = True
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self) -> None:
        path = urlparse(self.path).path
        _log({"ts": time.time(), "method": "GET", "path": path, "host": self.headers.get("Host")})
        if path in ("/health", "/v1/health"):
            self._json(200, {"ok": True, "mock": True})
            return
        if path in ("/v1/models", "/models"):
            self._json(
                200,
                {
                    "object": "list",
                    "data": [{"id": "echo", "object": "model", "owned_by": "localmock"}],
                },
            )
            return
        if path == "/_debug/observations":
            with _lock:
                self._json(200, list(_requests))
            return
        self._json(404, {"error": {"message": f"not found: {path}"}})

    def do_POST(self) -> None:
        global _a3_calls, _a8_writes, _a8_updates
        path = urlparse(self.path).path
        if path == "/_debug/reset":
            with _lock:
                _requests.clear()
                _a3_calls = 0
                _a8_writes = 0
                _a8_updates = 0
            self._json(200, {"ok": True})
            return
        length = int(self.headers.get("Content-Length") or "0")
        raw = self.rfile.read(length) if length else b"{}"
        try:
            body = json.loads(raw.decode("utf-8") or "{}")
        except json.JSONDecodeError:
            body = {}
        rec = {
            "ts": time.time(),
            "method": "POST",
            "path": path,
            "host": self.headers.get("Host"),
            "scenario": None,
            "model": body.get("model") if isinstance(body, dict) else None,
            "keys": sorted(body.keys()) if isinstance(body, dict) else [],
            "userChars": len(_user_text(body)) if isinstance(body, dict) else 0,
            "contentPartTypes": _content_part_types(body) if isinstance(body, dict) else [],
            "toolNames": _tool_names(body) if isinstance(body, dict) else [],
            "latestTurnHasToolResult": _latest_turn_has_tool_result(body) if isinstance(body, dict) else False,
        }
        rec["hasImage"] = any(
            t in ("image_url", "image", "input_image") for t in rec["contentPartTypes"]
        )
        rec["hasFile"] = any(t in ("file", "input_file") for t in rec["contentPartTypes"])
        with _lock:
            _requests.append(rec)
        if path not in ("/v1/chat/completions", "/chat/completions"):
            rec["scenario"] = "unknown_path"
            _log(rec)
            self._json(404, {"error": {"message": f"not found: {path}"}})
            return
        scenario = _scenario(body)
        rec["scenario"] = scenario
        _log(rec)
        if scenario == "error":
            with _lock:
                _a3_calls += 1
                n = _a3_calls
            rec["a3Attempt"] = n
            _log(rec)
            if n <= 2:
                self._json(
                    500,
                    {
                        "error": {
                            "message": f"localmock retryable 500 attempt {n}",
                            "type": "server_error",
                            "code": "localmock_retryable",
                        }
                    },
                )
                return
            self._json(
                400,
                {
                    "error": {
                        "message": "localmock non-retryable provider rejection (A3)",
                        "type": "invalid_request_error",
                        "code": "localmock_rejected",
                    }
                },
            )
            return
        if scenario == "after_tool":
            _sse(self, _text_chunks("SANDBOX_OK"))
            return
        if scenario == "todowrite_update":
            with _lock:
                _a8_updates += 1
                n = _a8_updates
            if n > 1:
                _sse(self, _text_chunks("SANDBOX_OK"))
                return
            _sse(
                self,
                _tool_chunks(
                    "todowrite",
                    {
                        "todos": [
                            {"content": "capture A8", "status": "completed", "priority": "high"},
                            {"content": "complete A8", "status": "completed", "priority": "medium"},
                        ]
                    },
                    call_id="call_mock2",
                ),
            )
            return
        if scenario == "title":
            _sse(self, _text_chunks("Fixture session"))
            return
        if scenario == "slow":
            _sse(self, _text_chunks("SLOW_STREAM_ABCDEFGHIJKLMNOPQRSTUVWXYZ"), delay=0.25)
            return
        if scenario == "reconnect":
            _sse(self, _text_chunks("A5_PARTIAL_" + ("ABCDEFGHIJ" * 8)), delay=0.35)
            return
        if scenario == "read_outside":
            _sse(self, _tool_chunks("read", {"filePath": OUTSIDE_FILE}))
            return
        if scenario == "question":
            _sse(
                self,
                _tool_chunks(
                    "question",
                    {
                        "questions": [
                            {
                                "question": "Which fixture color?",
                                "header": "Color",
                                "options": [
                                    {"label": "red", "description": "Stop"},
                                    {"label": "green", "description": "Go"},
                                ],
                                "multiple": False,
                            }
                        ]
                    },
                ),
            )
            return
        if scenario == "todowrite":
            with _lock:
                _a8_writes += 1
                n = _a8_writes
            if n > 1:
                _sse(self, _text_chunks("SANDBOX_OK"))
                return
            _sse(
                self,
                _tool_chunks(
                    "todowrite",
                    {
                        "todos": [
                            {"content": "capture A8", "status": "pending", "priority": "high"},
                            {"content": "complete A8", "status": "in_progress", "priority": "medium"},
                        ]
                    },
                ),
            )
            return
        _sse(self, _text_chunks("SANDBOX_OK"))


def main() -> None:
    httpd = ThreadingHTTPServer((HOST, PORT), Handler)
    print(f"mock_provider listening on http://{HOST}:{PORT}", flush=True)
    httpd.serve_forever()


if __name__ == "__main__":
    main()
