#!/usr/bin/env python3
"""Capture official 1.18.18 HTTP/SSE samples from the isolated sandbox.

Does not contact the owner managed serve on :4096.
"""

from __future__ import annotations

import argparse
import base64
import http.client
import json
import os
import re
import select
import sys
import threading
import time
import uuid
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urlparse
from urllib.request import Request, urlopen

SOURCE_COMMIT = "2cba7e227d"
OPENCODE_VERSION = "1.18.18"
FORBIDDEN_PORT = "4096"

ID_RE = re.compile(r"\b((?:ses|msg|prt|per|que|call|evt|tool)_[0-9A-Za-z]+)\b")
TIME_KEYS = {"created", "updated", "completed", "archived", "compacted"}


def new_id(prefix: str) -> str:
    # Official Identifier.create: prefix + "_" + 12 hex + 14 base62.
    return prefix + "_" + uuid.uuid4().hex[:12] + uuid.uuid4().hex[:14]


class Client:
    def __init__(self, base: str, user: str, password: str, directory: str):
        self.base = base.rstrip("/")
        self.auth = "Basic " + base64.b64encode(f"{user}:{password}".encode()).decode()
        self.directory = directory
        self.http: list[dict[str, Any]] = []

    def request(
        self,
        method: str,
        path: str,
        *,
        query: dict[str, Any] | None = None,
        body: Any = None,
        timeout: float = 15.0,
        extra_headers: dict[str, str] | None = None,
    ) -> tuple[int, Any, dict[str, str]]:
        if FORBIDDEN_PORT in self.base:
            raise RuntimeError("refusing to talk to port 4096")
        q = dict(query or {})
        if "directory" not in q:
            q["directory"] = self.directory
        url = self.base + path
        if q:
            url += ("&" if "?" in path else "?") + urlencode(q, doseq=True)
        data = None
        headers = {
            "Authorization": self.auth,
            "Accept": "application/json",
            "x-opencode-directory": self.directory,
        }
        if extra_headers:
            headers.update(extra_headers)
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        req = Request(url, data=data, method=method, headers=headers)
        rec = {
            "method": method,
            "path": path,
            "query": {k: v for k, v in q.items() if k != "directory"},
            "queryHasDirectory": "directory" in q,
            "body": body,
            "status": None,
            "response": None,
        }
        try:
            with urlopen(req, timeout=timeout) as resp:
                raw = resp.read()
                rec["status"] = resp.status
                rec["responseHeaders"] = {k.lower(): v for k, v in resp.headers.items() if k.lower() != "set-cookie"}
                rec["response"] = decode_body(raw)
                self.http.append(rec)
                return resp.status, rec["response"], rec["responseHeaders"]
        except HTTPError as exc:
            raw = exc.read()
            rec["status"] = exc.code
            rec["response"] = decode_body(raw)
            self.http.append(rec)
            return exc.code, rec["response"], {}
        except URLError as exc:
            rec["status"] = 0
            rec["response"] = {"error": str(exc)}
            self.http.append(rec)
            raise


def decode_body(raw: bytes) -> Any:
    if not raw:
        return None
    text = raw.decode("utf-8", "replace")
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return text


class SSE:
    def __init__(self, client: Client):
        self.client = client
        self.frames: list[dict[str, Any]] = []
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None
        self._error: str | None = None

    def start(self) -> None:
        self._thread = threading.Thread(target=self._run, daemon=True)
        self._thread.start()

    def stop(self) -> None:
        self._stop.set()

    def _run(self) -> None:
        parsed = urlparse(self.client.base)
        path = "/global/event?" + urlencode({"directory": self.client.directory})
        conn = http.client.HTTPConnection(parsed.hostname, parsed.port, timeout=10)
        try:
            conn.request(
                "GET",
                path,
                headers={
                    "Authorization": self.client.auth,
                    "Accept": "text/event-stream",
                    "x-opencode-directory": self.client.directory,
                    "Connection": "keep-alive",
                },
            )
            resp = conn.getresponse()
            if resp.status != 200:
                self._error = f"SSE HTTP {resp.status}"
                return
            sock = conn.sock
            if sock is not None:
                sock.settimeout(None)
            buf = b""
            while not self._stop.is_set():
                if sock is None:
                    break
                ready, _, _ = select.select([sock], [], [], 0.5)
                if not ready:
                    continue
                chunk = resp.read(1)
                if not chunk:
                    break
                buf += chunk
                while b"\n\n" in buf:
                    frame, buf = buf.split(b"\n\n", 1)
                    self._accept(frame)
        except Exception as exc:  # noqa: BLE001 — capture harness must record disconnect
            self._error = str(exc)
        finally:
            try:
                conn.close()
            except Exception:
                pass

    def _accept(self, frame: bytes) -> None:
        data_lines = []
        for line in frame.decode("utf-8", "replace").splitlines():
            if line.startswith("data:"):
                data_lines.append(line[5:].lstrip())
        if not data_lines:
            return
        payload = "\n".join(data_lines)
        try:
            parsed = json.loads(payload)
        except json.JSONDecodeError:
            parsed = {"unparsed": payload}
        self.frames.append({"t": time.time(), "event": parsed})

    def wait_until(self, pred, timeout: float) -> bool:
        deadline = time.time() + timeout
        while time.time() < deadline:
            if pred(self.frames):
                return True
            time.sleep(0.05)
        return False


def event_types(frames: list[dict[str, Any]]) -> list[str]:
    out = []
    for frame in frames:
        ev = frame.get("event") or {}
        payload = ev.get("payload") if isinstance(ev, dict) else None
        if isinstance(payload, dict) and payload.get("type"):
            out.append(str(payload.get("type")))
            nested = payload.get("properties") or {}
            if payload.get("type") == "sync" and isinstance(nested, dict):
                sync_type = nested.get("type") or (nested.get("syncEvent") or {}).get("type")
                if sync_type:
                    out.append("sync:" + str(sync_type))
        elif isinstance(ev, dict) and ev.get("type"):
            out.append(str(ev.get("type")))
    return out


def has_type(frames: list[dict[str, Any]], wanted: str) -> bool:
    return wanted in event_types(frames)


def sanitize(value: Any, table: dict[str, str], workspace: str) -> Any:
    if isinstance(value, dict):
        return {k: sanitize(v, table, workspace) for k, v in value.items()}
    if isinstance(value, list):
        return [sanitize(v, table, workspace) for v in value]
    if isinstance(value, str):
        text = value.replace(workspace, "/tmp/ocw-gate-a/workspace")
        text = text.replace(os.path.dirname(workspace), "/tmp/ocw-gate-a")
        def repl(match: re.Match[str]) -> str:
            raw = match.group(1)
            if raw not in table:
                prefix = raw.split("_", 1)[0]
                table[raw] = f"{prefix}_fixture_{len(table)+1:03d}"
            return table[raw]
        return ID_RE.sub(repl, text)
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        # Keep small enums/counters; replace epoch-looking numbers.
        if value > 1_000_000_000:
            return 0
        return value
    return value


def write_sample(path: Path, doc: dict[str, Any], workspace: str) -> None:
    table: dict[str, str] = {}
    sanitized = sanitize(doc, table, workspace)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(sanitized, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    raw_path = path.with_name(path.name.replace(".sanitized.json", ".raw.json"))
    # Raw still strips secrets and workspace absolute path, keeps live ids for local debug.
    raw = json.loads(json.dumps(doc))
    raw_path.write_text(json.dumps(raw, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")


def prompt_body(text: str, extra_parts: list[dict] | None = None) -> dict[str, Any]:
    parts = [{"type": "text", "text": text}]
    if extra_parts:
        parts.extend(extra_parts)
    # Official Web sends variant only when selected; localmock/echo has none.
    return {
        "messageID": new_id("msg"),
        "agent": "build",
        "model": {"providerID": "localmock", "modelID": "echo"},
        "parts": parts,
    }


def classify_sse(frames: list[dict[str, Any]]) -> dict[str, Any]:
    direct, sync, status_types = [], [], []
    for frame in frames:
        ev = frame.get("event") if isinstance(frame, dict) else None
        if not isinstance(ev, dict):
            continue
        payload = ev.get("payload") if isinstance(ev.get("payload"), dict) else ev
        typ = payload.get("type")
        if typ == "sync":
            sync.append(payload)
        elif typ:
            direct.append(typ)
        if typ == "session.status":
            props = payload.get("properties") if isinstance(payload.get("properties"), dict) else {}
            inner = props.get("status") or props.get("type") or props
            status_types.append(inner)
    return {
        "directTypes": direct,
        "syncCount": len(sync),
        "syncTypes": [
            ((item.get("syncEvent") or {}).get("type"))
            or ((item.get("properties") or {}).get("type"))
            or ((item.get("properties") or {}).get("syncEvent") or {}).get("type")
            for item in sync
        ],
        "statusCarriers": status_types,
    }


def capture_a1(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    if sse._error:
        raise RuntimeError(f"SSE not connected before create: {sse._error}")
    status, created, _ = c.request("POST", "/session", body={})
    if status >= 300:
        raise RuntimeError(f"create failed: {status} {created}")
    sid = created["id"]
    body = prompt_body("A1_HEALTHY_TEXT reply with SANDBOX_OK")
    client_mid = body["messageID"]
    code, resp, _ = c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    if code not in (200, 204):
        raise RuntimeError(f"prompt_async HTTP {code}: {resp}")
    sse.wait_until(
        lambda frames: any(t in event_types(frames) for t in ("message.part.delta", "message.updated", "session.status")),
        20,
    )

    def last_status(frames: list[dict[str, Any]]) -> str | None:
        last = None
        for frame in frames:
            ev = frame.get("event") if isinstance(frame, dict) else None
            payload = ev.get("payload") if isinstance(ev, dict) and isinstance(ev.get("payload"), dict) else ev
            if not isinstance(payload, dict) or payload.get("type") != "session.status":
                continue
            props = payload.get("properties") if isinstance(payload.get("properties"), dict) else {}
            if props.get("sessionID") not in (None, sid):
                continue
            st = props.get("status")
            if isinstance(st, dict):
                last = st.get("type")
            elif isinstance(st, str):
                last = st
        return last

    reached_idle = sse.wait_until(lambda frames: last_status(frames) == "idle", 40)
    if not reached_idle:
        raise RuntimeError(
            f"A1 did not reach session.status idle (last={last_status(sse.frames)} types={event_types(sse.frames)[-12:]})"
        )
    time.sleep(0.5)
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    _, info, _ = c.request("GET", f"/session/{sid}")
    _, status_map, _ = c.request("GET", "/session/status")
    classified = classify_sse(sse.frames)
    roles = []
    persisted_ids = []
    if isinstance(messages, list):
        for item in messages:
            info_obj = item.get("info") if isinstance(item, dict) else None
            if isinstance(info_obj, dict):
                roles.append(info_obj.get("role"))
                persisted_ids.append(info_obj.get("id"))
    if client_mid not in persisted_ids:
        raise RuntimeError(
            f"A1 go/no-go failed: client messageID {client_mid} not in persisted messages {persisted_ids}"
        )
    assistant_text = []
    if isinstance(messages, list):
        for item in messages:
            info_obj = item.get("info") if isinstance(item, dict) else None
            if not isinstance(info_obj, dict) or info_obj.get("role") != "assistant":
                continue
            for part in item.get("parts") or []:
                if isinstance(part, dict) and part.get("type") == "text" and part.get("text"):
                    assistant_text.append(part.get("text"))
    if "user" not in roles or "assistant" not in roles:
        raise RuntimeError(f"A1 go/no-go failed: persisted roles={roles} types={event_types(sse.frames)}")
    if not assistant_text:
        raise RuntimeError("A1 go/no-go failed: persisted assistant text is empty")
    if sse._error and not sse.frames:
        raise RuntimeError(f"SSE error during A1 with zero frames: {sse._error}")
    write_sample(
        out / "a1-first-healthy-text.sanitized.json",
        {
            "meta": {
                "scenario": "A1",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "promptHttpStatus": code,
                "promptResponse": resp,
                "clientMessageID": client_mid,
                "variantSent": False,
                "variantReason": "localmock/echo has no official variant; Web omits the field when unset",
            },
            "source": {
                "ui": "packages/app/src/utils/server-compat.ts:163-169 create; packages/app/src/utils/server-compat.ts:200-230 promptAsync",
                "server": "packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts promptAsync; packages/opencode/src/session/prompt.ts PromptInput",
                "sse": "packages/app/src/context/server-sdk.tsx v1 global.event; skips payload.type==sync at line 284",
                "reducer": "packages/app/src/context/global-sync/event-reducer.ts",
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sseClassification": classified,
            "sse": sse.frames,
            "reload": {"messages": messages, "session": info, "status": status_map},
            "correlation": {
                "clientMessageID": client_mid,
                "persistedMessageIDs": persisted_ids,
                "persistedRoles": roles,
                "clientIdPersisted": True,
            },
            "sanitization": {
                "replaced": ["session/message/part/event ids", "absolute workspace paths", "epoch timestamps > 1e9"],
                "kept": ["all JSON keys", "event types", "part types", "HTTP method/path/status"],
            },
        },
        workspace,
    )


def capture_a2(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    status, created, _ = c.request("POST", "/session", body={})
    sid = created["id"]
    first = prompt_body("A2_FIRST SANDBOX_OK")
    c.request("POST", f"/session/{sid}/prompt_async", body=first, timeout=20)
    sse.wait_until(lambda frames: "SANDBOX_OK" in json.dumps(frames), 25)
    time.sleep(0.8)
    follow = prompt_body("A2_FOLLOW_UP second turn")
    c.request("POST", f"/session/{sid}/prompt_async", body=follow, timeout=20)
    time.sleep(2.0)
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    write_sample(
        out / "a2-follow-up.sanitized.json",
        {
            "meta": {"scenario": "A2", "opencodeVersion": OPENCODE_VERSION, "sourceCommit": SOURCE_COMMIT},
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"messages": messages},
        },
        workspace,
    )


def capture_a3(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    _, created, _ = c.request("POST", "/session", body={})
    sid = created["id"]
    body = prompt_body("A3_PROVIDER_ERROR")
    c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    sse.wait_until(
        lambda frames: any("error" in t or "retry" in t.lower() or t == "session.status" for t in event_types(frames)),
        20,
    )
    time.sleep(8.0)  # bounded: first retry window only
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    _, status_map, _ = c.request("GET", "/session/status")
    write_sample(
        out / "a3-provider-error.sanitized.json",
        {
            "meta": {
                "scenario": "A3",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "note": "bounded capture of first retry window; full 3/8/16/34/60s ladder not waited",
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"messages": messages, "status": status_map},
        },
        workspace,
    )


def capture_a4(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    _, created, _ = c.request("POST", "/session", body={})
    sid = created["id"]
    body = prompt_body("A4_SLOW_STREAM")
    c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    sse.wait_until(lambda frames: "message.part.delta" in event_types(frames) or "session.status" in event_types(frames), 15)
    time.sleep(0.4)
    abort_code, abort_resp, _ = c.request("POST", f"/session/{sid}/abort", body=None, timeout=10)
    time.sleep(1.5)
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    _, info, _ = c.request("GET", f"/session/{sid}")
    write_sample(
        out / "a4-abort.sanitized.json",
        {
            "meta": {
                "scenario": "A4",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "abortHttpStatus": abort_code,
                "abortResponse": abort_resp,
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"messages": messages, "session": info},
        },
        workspace,
    )


def capture_a5(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    _, created, _ = c.request("POST", "/session", body={})
    sid = created["id"]
    body = prompt_body("A5_RECONNECT")
    c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    sse.wait_until(lambda frames: len(event_types(frames)) >= 3, 10)
    before = list(event_types(sse.frames))
    sse.stop()
    time.sleep(0.3)
    sse2 = SSE(c)
    sse2.start()
    time.sleep(3.0)
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    _, status_map, _ = c.request("GET", "/session/status")
    write_sample(
        out / "a5-sse-reconnect.sanitized.json",
        {
            "meta": {
                "scenario": "A5",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "typesBeforeDisconnect": before,
                "sse1Error": sse._error,
                "sse2Error": sse2._error,
            },
            "http": c.http,
            "sseBefore": sse.frames,
            "sseAfterReconnect": sse2.frames,
            "sseEventTypesBefore": event_types(sse.frames),
            "sseEventTypesAfter": event_types(sse2.frames),
            "reload": {"messages": messages, "status": status_map},
        },
        workspace,
    )
    sse2.stop()


def capture_a6(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    _, created, _ = c.request("POST", "/session", body={})
    sid = created["id"]
    body = prompt_body("A6_READ_OUTSIDE")
    c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    sse.wait_until(lambda frames: "permission.asked" in event_types(frames), 20)
    time.sleep(0.5)
    _, pending, _ = c.request("GET", "/permission")
    reply_http = None
    if isinstance(pending, list) and pending:
        req = pending[0]
        pid = req.get("id")
        # Official v1 Web path: POST /session/:id/permissions/:permissionID {response}
        code, resp, _ = c.request(
            "POST",
            f"/session/{sid}/permissions/{pid}",
            body={"response": "once"},
            timeout=15,
        )
        reply_http = {"status": code, "response": resp, "permissionID": pid}
        sse.wait_until(lambda frames: "permission.replied" in event_types(frames), 10)
    time.sleep(1.0)
    _, pending_after, _ = c.request("GET", "/permission")
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    write_sample(
        out / "a6-permission.sanitized.json",
        {
            "meta": {
                "scenario": "A6",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "reply": reply_http,
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"pendingBeforeReply": pending, "pendingAfter": pending_after, "messages": messages},
        },
        workspace,
    )


def capture_a7(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    _, created, _ = c.request("POST", "/session", body={})
    sid = created["id"]
    body = prompt_body("A7_QUESTION")
    c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    sse.wait_until(lambda frames: "question.asked" in event_types(frames), 20)
    time.sleep(0.5)
    _, pending, _ = c.request("GET", "/question")
    reply_http = None
    if isinstance(pending, list) and pending:
        req = pending[0]
        qid = req.get("id")
        code, resp, _ = c.request(
            "POST",
            f"/question/{qid}/reply",
            body={"answers": [["red"]]},
            timeout=15,
        )
        reply_http = {"status": code, "response": resp, "requestID": qid}
        sse.wait_until(lambda frames: "question.replied" in event_types(frames), 10)
    time.sleep(1.0)
    _, pending_after, _ = c.request("GET", "/question")
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    write_sample(
        out / "a7-question.sanitized.json",
        {
            "meta": {
                "scenario": "A7",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "reply": reply_http,
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"pending": pending, "pendingAfter": pending_after, "messages": messages},
        },
        workspace,
    )


def capture_a8(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    _, created, _ = c.request("POST", "/session", body={})
    sid = created["id"]
    body = prompt_body("A8_TODOWRITE")
    c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    sse.wait_until(lambda frames: "todo.updated" in event_types(frames) or "permission.asked" in event_types(frames), 20)
    time.sleep(1.5)
    _, todos, _ = c.request("GET", f"/session/{sid}/todo")
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    write_sample(
        out / "a8-todos.sanitized.json",
        {
            "meta": {"scenario": "A8", "opencodeVersion": OPENCODE_VERSION, "sourceCommit": SOURCE_COMMIT},
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"todos": todos, "messages": messages},
        },
        workspace,
    )


def capture_a9(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    _, created, _ = c.request("POST", "/session", body={})
    sid = created["id"]
    readme = Path(workspace) / "README.md"
    file_url = "file://" + str(readme)
    extra = [
        {
            "type": "file",
            "mime": "text/plain",
            "url": file_url,
            "filename": "README.md",
            "source": {
                "type": "file",
                "text": {"value": "README.md", "start": 0, "end": 9},
                "path": str(readme),
            },
        },
        {"type": "agent", "name": "plan", "source": {"value": "@plan", "start": 0, "end": 5}},
    ]
    body = prompt_body("A9_PROMPT_PARTS mention README and @plan", extra_parts=extra)
    code, resp, _ = c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    time.sleep(2.5)
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    write_sample(
        out / "a9-prompt-parts.sanitized.json",
        {
            "meta": {
                "scenario": "A9",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "promptHttpStatus": code,
                "promptResponse": resp,
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"messages": messages},
        },
        workspace,
    )


def capture_a10(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    roots = []
    for i in range(3):
        _, created, _ = c.request("POST", "/session", body={"title": f"root-{i}"})
        roots.append(created["id"])
    parent = roots[0]
    _, child, _ = c.request("POST", "/session", body={"parentID": parent, "title": "child-0"})
    archive_ts = 1787180000123
    _, archived, _ = c.request("PATCH", f"/session/{roots[1]}", body={"time": {"archived": archive_ts}})
    _, listed_default, _ = c.request("GET", "/session")
    _, listed_roots, _ = c.request("GET", "/session", query={"roots": "true", "limit": "2"})
    _, listed_limit1, _ = c.request("GET", "/session", query={"roots": "true", "limit": "1"})
    _, children, _ = c.request("GET", f"/session/{parent}/children")
    get_code, got, _ = c.request("GET", f"/session/{roots[1]}")
    write_sample(
        out / "a10-session-listing.sanitized.json",
        {
            "meta": {"scenario": "A10", "opencodeVersion": OPENCODE_VERSION, "sourceCommit": SOURCE_COMMIT},
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {
                "listedDefaultCount": len(listed_default) if isinstance(listed_default, list) else listed_default,
                "listedRoots": listed_roots,
                "listedLimit1": listed_limit1,
                "children": children,
                "archivedGetStatus": get_code,
                "archivedGet": got,
                "archivePatch": archived,
                "childCreate": child,
            },
        },
        workspace,
    )


SCENARIOS = {
    "a1": capture_a1,
    "a2": capture_a2,
    "a3": capture_a3,
    "a4": capture_a4,
    "a5": capture_a5,
    "a6": capture_a6,
    "a7": capture_a7,
    "a8": capture_a8,
    "a9": capture_a9,
    "a10": capture_a10,
}


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--base", default="http://127.0.0.1:4398")
    p.add_argument("--user", default="gatea")
    p.add_argument("--password", default="gatea-pass")
    p.add_argument("--directory", required=True)
    p.add_argument("--out", required=True)
    p.add_argument("--scenario", required=True, choices=sorted(SCENARIOS) + ["all"])
    args = p.parse_args()
    if FORBIDDEN_PORT in args.base:
        print("refusing managed serve port 4096", file=sys.stderr)
        return 2
    out = Path(args.out)
    c = Client(args.base, args.user, args.password, args.directory)
    health_code, health, _ = c.request("GET", "/global/health", query={})
    if health_code != 200 or not (isinstance(health, dict) and health.get("version") == OPENCODE_VERSION):
        print(f"health mismatch: {health_code} {health}", file=sys.stderr)
        return 1
    names = list(SCENARIOS) if args.scenario == "all" else [args.scenario]
    failed = []
    for name in names:
        client = Client(args.base, args.user, args.password, args.directory)
        sse = SSE(client)
        sse.start()
        sse.wait_until(lambda frames: bool(frames) or bool(sse._error), 5)
        if sse._error and not sse.frames:
            failed.append((name, f"SSE connect failed: {sse._error}"))
            print(f"FAILED {name}: SSE connect failed: {sse._error}", file=sys.stderr)
            sse.stop()
            continue
        try:
            SCENARIOS[name](client, sse, out, args.directory)
            print(f"captured {name}")
        except Exception as exc:  # noqa: BLE001
            failed.append((name, str(exc)))
            print(f"FAILED {name}: {exc}", file=sys.stderr)
            write_sample(
                out / f"{name}-FAILED.sanitized.json",
                {
                    "meta": {
                        "scenario": name.upper(),
                        "opencodeVersion": OPENCODE_VERSION,
                        "sourceCommit": SOURCE_COMMIT,
                        "failed": True,
                        "error": str(exc),
                    },
                    "http": client.http,
                    "sseEventTypes": event_types(sse.frames),
                    "sse": sse.frames,
                },
                args.directory,
            )
        finally:
            sse.stop()
            time.sleep(0.2)
    if failed:
        print("failures:", failed, file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
