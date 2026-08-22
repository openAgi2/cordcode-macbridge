#!/usr/bin/env python3
"""Phase 0 样本采集用的本地 mock Responses API provider（仅标准库）。

边界声明（设计 §12 / CLAUDE.md source-first 纪律）：
- 本 server mock 的是 **模型 provider 上游**（OpenAI Responses API），不是 app-server。
- 与 app-server 之间的全部 JSON-RPC 样本均由官方二进制 codex-cli 0.149.0-alpha.4 真实产生；
  本 server 只控制上游脚本（流式 delta 数、reasoning、exec_command 调用、失败路径）。
- 内容全部为合成标记文本（MOCK:*），无用户数据。

用法：python3 mock_provider.py [port]  → 监听 127.0.0.1，打印实际端口。
"""
import json
import sys
import threading
import time
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CALL_COUNTER = {"n": 0}


def sse(events):
    out = []
    for ev in events:
        kind = ev.get("type", "")
        out.append(f"event: {kind}")
        if len(ev) > 1 or kind == "":
            out.append(f"data: {json.dumps(ev, ensure_ascii=False)}")
        out.append("")
    return "\n".join(out) + "\n"


def resp_id():
    return "resp_mock_" + uuid.uuid4().hex[:12]


def item_id():
    return "item_mock_" + uuid.uuid4().hex[:8]


def ev_created(rid):
    return {"type": "response.created", "response": {"id": rid}}


def ev_completed(rid, in_tok=111, out_tok=222):
    return {
        "type": "response.completed",
        "response": {
            "id": rid,
            "usage": {
                "input_tokens": in_tok,
                "input_tokens_details": None,
                "output_tokens": out_tok,
                "output_tokens_details": None,
                "total_tokens": in_tok + out_tok,
            },
        },
    }


def ev_msg_added(mid):
    return {
        "type": "response.output_item.added",
        "item": {
            "type": "message",
            "role": "assistant",
            "id": mid,
            "content": [{"type": "output_text", "text": ""}],
        },
    }


def ev_msg_done(mid, text):
    return {
        "type": "response.output_item.done",
        "item": {
            "type": "message",
            "role": "assistant",
            "id": mid,
            "content": [{"type": "output_text", "text": text}],
        },
    }


def ev_text_delta(delta):
    return {"type": "response.output_text.delta", "delta": delta}


def ev_reasoning(rid, summary, raw):
    import base64
    enc = base64.b64encode((("b" * 64) + raw).encode()).decode()
    return {
        "type": "response.output_item.done",
        "item": {
            "type": "reasoning",
            "id": item_id(),
            "summary": [{"type": "summary_text", "text": s} for s in summary],
            "encrypted_content": enc,
        },
    }


def ev_function_call(call_id, name, arguments):
    return {
        "type": "response.output_item.done",
        "item": {
            "type": "function_call",
            "call_id": call_id,
            "name": name,
            "arguments": json.dumps(arguments, ensure_ascii=False),
        },
    }


def extract_last_user_text(body):
    try:
        items = body.get("input", [])
        if isinstance(items, str):
            return items
        for it in reversed(items):
            if isinstance(it, dict) and it.get("role") == "user":
                content = it.get("content", [])
                if isinstance(content, str):
                    return content
                for c in reversed(content):
                    if isinstance(c, dict) and c.get("type") in ("input_text", "text"):
                        return c.get("text", "")
    except Exception:
        pass
    return ""


def has_function_call_output(body):
    try:
        return any(
            isinstance(it, dict) and it.get("type") == "function_call_output"
            for it in body.get("input", [])
            if isinstance(it, dict)
        )
    except Exception:
        return False


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):  # 静默默认访问日志
        pass

    def do_POST(self):
        if self.path.rstrip("/") not in ("/v1/responses", "/responses"):
            self.send_error(404)
            return
        slow = False
        length = int(self.headers.get("Content-Length", "0"))
        try:
            body = json.loads(self.rfile.read(length) or b"{}")
        except Exception:
            body = {}
        import os as _os
        if not _os.environ.get("CODEXWEB_P0_DEBUG"):
            _lf = None
        else:
            _lf = open("/tmp/codexweb-p0-provider-req.log", "a")
        if _lf:
                _lf.write(json.dumps({
                    "path": self.path,
                    "input_types": [it.get("type") if isinstance(it, dict) else str(it)[:30] for it in (body.get("input") or [])],
                    "last_user": extract_last_user_text(body)[:80],
                    "tool_names": [tool.get("name") for tool in (body.get("tools") or []) if isinstance(tool, dict)],
                    "function_outputs": [it.get("output") for it in (body.get("input") or []) if isinstance(it, dict) and it.get("type") == "function_call_output"],
                }) + "\n")
        text = extract_last_user_text(body) or ""
        CALL_COUNTER["n"] += 1
        call_n = CALL_COUNTER["n"]

        events = [ev_created(resp_id())]
        rid = events[0]["response"]["id"]
        chunk_count = 10
        if text.startswith("MOCK:REASON"):
            payload = text[len("MOCK:REASON"):].strip() or "reasoned"
            events.append(ev_reasoning(rid, ["mock summary one", "mock summary two"], "raw thinking"))
            mid = item_id()
            events.append(ev_msg_added(mid))
            full = ""
            for i in range(chunk_count):
                d = f"{payload}({i}) "
                full += d
                events.append(ev_text_delta(d))
            events.append(ev_msg_done(mid, full.strip()))
            events.append(ev_completed(rid, 120, 200))
        elif text.startswith("MOCK:ASK1") or text.startswith("MOCK:ASK3"):
            multi = text.startswith("MOCK:ASK3")
            if has_function_call_output(body):
                mid = item_id()
                events.append(ev_msg_added(mid))
                events.append(ev_text_delta("thanks for your answers"))
                events.append(ev_msg_done(mid, "thanks for your answers"))
                events.append(ev_completed(rid, 140, 40))
            else:
                questions = [{
                    "id": "confirm_path",
                    "header": "Confirm",
                    "question": "Proceed with the plan?",
                    "options": [
                        {"label": "Yes (Recommended)", "description": "Continue the current plan."},
                        {"label": "No", "description": "Stop and revisit the approach."},
                    ],
                }]
                if multi:
                    q2 = {
                        "id": "free_text_note",
                        "header": "Note",
                        "question": "Any extra note?",
                        "options": [
                            {"label": "No note (Recommended)", "description": "Continue without an extra note."},
                            {"label": "Add note", "description": "Provide a free-form note using Other."},
                        ],
                    }
                    q3q = {
                        "id": "pick_size",
                        "header": "Size",
                        "question": "Which size?",
                        "options": [
                            {"label": "Small", "description": "small"},
                            {"label": "Large", "description": "large"},
                        ],
                    }
                    questions.append(q2)
                    questions.append(q3q)
                events.append(ev_function_call("call_ask_" + uuid.uuid4().hex[:8], "request_user_input", {"questions": questions}))
                events.append(ev_completed(rid, 100, 10))
        elif text.startswith("MOCK:PATCH"):
            patch = "*** Begin Patch\n*** Add File: newfile.txt\n+phase0 content\n*** End Patch\n"
            if has_function_call_output(body):
                mid = item_id()
                events.append(ev_msg_added(mid))
                events.append(ev_text_delta("patch applied or denied"))
                events.append(ev_msg_done(mid, "patch applied or denied"))
                events.append(ev_completed(rid, 150, 40))
            else:
                events.append(ev_function_call("call_patch_" + uuid.uuid4().hex[:8], "exec_command", {"cmd": f"apply_patch <<'EOF'\n{patch}EOF\n"}))
                events.append(ev_completed(rid, 100, 10))
        elif text.startswith("MOCK:PERM"):
            if has_function_call_output(body):
                mid = item_id()
                events.append(ev_msg_added(mid))
                events.append(ev_text_delta("permissions granted or denied"))
                events.append(ev_msg_done(mid, "permissions granted or denied"))
                events.append(ev_completed(rid, 170, 40))
            else:
                args = {"reason": "Select a workspace root", "permissions": {"file_system": {"write": [".", "../shared"]}}}
                events.append(ev_function_call("call_perm_" + uuid.uuid4().hex[:8], "request_permissions", args))
                events.append(ev_completed(rid, 100, 10))
        elif text.startswith("MOCK:NET"):
            if has_function_call_output(body):
                mid = item_id()
                events.append(ev_msg_added(mid))
                events.append(ev_text_delta("network result received"))
                events.append(ev_msg_done(mid, "network result received"))
                events.append(ev_completed(rid, 160, 40))
            else:
                events.append(ev_function_call("call_net_" + uuid.uuid4().hex[:8], "exec_command", {"cmd": "curl -s --max-time 5 http://127.0.0.1:1/healthz"}))
                events.append(ev_completed(rid, 100, 10))
        elif text.startswith("MOCK:CMD:"):
            cmd = text[len("MOCK:CMD:"):].strip() or "echo mock"
            if has_function_call_output(body):
                mid = item_id()
                events.append(ev_msg_added(mid))
                events.append(ev_text_delta("command executed via mock provider"))
                events.append(ev_msg_done(mid, "command executed via mock provider"))
                events.append(ev_completed(rid, 130, 30))
            else:
                events.append(ev_function_call("call_mock_" + uuid.uuid4().hex[:8], "exec_command", {"cmd": cmd}))
                events.append(ev_completed(rid, 100, 10))
        elif text.startswith("MOCK:FAIL"):
            events = [
                ev_created(rid),
                {"type": "response.failed", "response": {"id": rid, "error": {"code": "mock_error", "message": "mock provider failure"}}},
            ]
        elif text.startswith("MOCK:USAGE"):
            mid = item_id()
            events.append(ev_msg_added(mid))
            events.append(ev_text_delta("usage sample"))
            events.append(ev_msg_done(mid, "usage sample"))
            events.append(ev_completed(rid, 12345, 6789))
        elif text.startswith("MOCK:SLOW"):
            slow = True
            payload = text[len("MOCK:SLOW"):].strip() or "slow"
            rid2 = rid
            events = [ev_created(rid2)]
            mid = item_id()
            events.append(ev_msg_added(mid))
            for i in range(40):
                events.append(ev_text_delta(f"{payload}[{i}] "))
            events.append(ev_msg_done(mid, "slow completed"))
            events.append(ev_completed(rid2, 99, 99))
        else:
            payload = text or "mock default answer"
            mid = item_id()
            events.append(ev_msg_added(mid))
            full = ""
            for i in range(chunk_count):
                d = f"{payload}<{i}> "
                full += d
                events.append(ev_text_delta(d))
            events.append(ev_msg_done(mid, full.strip()))
            events.append(ev_completed(rid, 111, 222))

        if slow:
            # 无 Content-Length + Connection: close 流式分块写（MOCK:SLOW）
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Connection", "close")
            self.end_headers()
            for ev in events:
                try:
                    self.wfile.write((f"event: {ev.get('type','')}\ndata: {json.dumps(ev, ensure_ascii=False)}\n\n").encode())
                    self.wfile.flush()
                except Exception:
                    return
                time.sleep(0.4)
            return
        payload = sse(events).encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_GET(self):
        if self.path.rstrip("/") in ("/healthz", "/health"):
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", "2")
            self.end_headers()
            self.wfile.write(b"ok")
        else:
            self.send_error(404)


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 0
    srv = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    print(srv.server_address[1], flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()
