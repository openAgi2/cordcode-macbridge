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

    def clone(self, directory: str) -> "Client":
        other = Client.__new__(Client)
        other.base = self.base
        other.auth = self.auth
        other.directory = directory
        other.http = []
        return other

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
        out = {}
        for k, v in value.items():
            nk = sanitize(k, table, workspace) if isinstance(k, str) else k
            out[nk] = sanitize(v, table, workspace)
        return out
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


LEAK_MARKERS = (
    "Authorization",
    "gatea-pass",
    "OPENCODE_SERVER_PASSWORD",
    "/Users/jacklee",
    "api_key",
    "apiKey",
    "BEGIN PRIVATE KEY",
)


def leak_scan(obj: Any) -> list[str]:
    dumped = json.dumps(obj)
    hits = []
    if "Basic " in dumped and "Authorization" in dumped:
        hits.append("authorization-header")
    for marker in LEAK_MARKERS:
        if marker in dumped and marker != "Authorization":
            hits.append(marker)
    if "127.0.0.1:4096" in dumped:
        hits.append("owner-managed-serve")
    return hits


def write_sample(path: Path, doc: dict[str, Any], workspace: str) -> None:
    table: dict[str, str] = {}
    sanitized = sanitize(doc, table, workspace)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(sanitized, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    raw = json.loads(json.dumps(doc))
    leaks = leak_scan(raw)
    raw_path = path.with_name(path.name.replace(".sanitized.json", ".raw.json"))
    if leaks:
        rejected = Path("/tmp/ocw-gate-a-raw-rejected")
        rejected.mkdir(parents=True, exist_ok=True)
        dest = rejected / raw_path.name
        dest.write_text(json.dumps(raw, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
        print(f"raw withheld from repo ({leaks}); wrote {dest}", file=sys.stderr)
        return
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


def last_session_status(frames: list[dict[str, Any]], sid: str) -> str | None:
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


def wait_idle(sse: SSE, sid: str, timeout: float) -> tuple[bool, str | None]:
    ok = sse.wait_until(lambda frames: last_session_status(frames, sid) == "idle", timeout)
    return ok, last_session_status(sse.frames, sid)


def idle_count(frames: list[dict[str, Any]], sid: str) -> int:
    n = 0
    for frame in frames:
        ev = frame.get("event") if isinstance(frame, dict) else None
        payload = ev.get("payload") if isinstance(ev, dict) and isinstance(ev.get("payload"), dict) else ev
        if not isinstance(payload, dict) or payload.get("type") != "session.status":
            continue
        props = payload.get("properties") if isinstance(payload.get("properties"), dict) else {}
        if props.get("sessionID") not in (None, sid):
            continue
        st = props.get("status")
        typ = st.get("type") if isinstance(st, dict) else st
        if typ == "idle":
            n += 1
    return n


def parse_messages(messages: Any) -> list[dict[str, Any]]:
    out = []
    if not isinstance(messages, list):
        return out
    for item in messages:
        if not isinstance(item, dict):
            continue
        info = item.get("info") if isinstance(item.get("info"), dict) else {}
        texts = [
            part.get("text")
            for part in (item.get("parts") or [])
            if isinstance(part, dict) and part.get("type") == "text" and part.get("text")
        ]
        out.append(
            {
                "id": info.get("id"),
                "role": info.get("role"),
                "finish": info.get("finish"),
                "error": info.get("error"),
                "texts": texts,
                "partTypes": [
                    part.get("type") for part in (item.get("parts") or []) if isinstance(part, dict)
                ],
            }
        )
    return out


SOURCE_PROMPT = {
    "ui": "packages/app/src/utils/server-compat.ts:163-169 create; packages/app/src/utils/server-compat.ts:200-230 promptAsync",
    "server": "packages/opencode/src/server/routes/instance/httpapi/handlers/session.ts promptAsync; packages/opencode/src/session/prompt.ts PromptInput",
    "sse": "packages/app/src/context/server-sdk.tsx v1 global.event; skips payload.type==sync at line 284",
    "reducer": "packages/app/src/context/global-sync/event-reducer.ts",
}
SANITIZATION = {
    "replaced": ["session/message/part/event ids", "absolute workspace paths", "epoch timestamps > 1e9"],
    "kept": ["all JSON keys", "event types", "part types", "HTTP method/path/status"],
}


def compute_synthesized_healthy_from_parsed(assistants: list[dict[str, Any]]) -> bool:
    for m in assistants:
        if m.get("finish") in ("stop", "completed") and not m.get("error"):
            return True
    return False


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
    if sse._error and not sse.frames:
        raise RuntimeError(f"SSE not connected: {sse._error}")
    status, created, _ = c.request("POST", "/session", body={})
    if status >= 300 or not isinstance(created, dict):
        raise RuntimeError(f"create failed: {status} {created}")
    sid = created["id"]
    first = prompt_body("A2_FIRST SANDBOX_OK")
    first_mid = first["messageID"]
    code1, resp1, _ = c.request("POST", f"/session/{sid}/prompt_async", body=first, timeout=20)
    if code1 not in (200, 204):
        raise RuntimeError(f"first prompt_async HTTP {code1}: {resp1}")
    ok1, last1 = wait_idle(sse, sid, 40)
    if not ok1:
        raise RuntimeError(f"A2 first turn did not idle (last={last1})")
    _, after_first, _ = c.request("GET", f"/session/{sid}/message")
    first_parsed = parse_messages(after_first)
    if [m["role"] for m in first_parsed] != ["user", "assistant"]:
        raise RuntimeError(f"A2 first-turn baseline roles={ [m['role'] for m in first_parsed] }")
    if first_parsed[0]["id"] != first_mid:
        raise RuntimeError("A2 first client messageID did not persist")
    if not first_parsed[1]["texts"]:
        raise RuntimeError("A2 first assistant text empty")
    follow = prompt_body("A2_FOLLOW_UP second turn")
    follow_mid = follow["messageID"]
    if follow_mid == first_mid:
        raise RuntimeError("A2 follow-up reused the first messageID")
    idles_before = idle_count(sse.frames, sid)
    code2, resp2, _ = c.request("POST", f"/session/{sid}/prompt_async", body=follow, timeout=20)
    if code2 not in (200, 204):
        raise RuntimeError(f"follow-up prompt_async HTTP {code2}: {resp2}")
    saw_second_busy = sse.wait_until(lambda frames: last_session_status(frames, sid) == "busy", 15)
    ok2 = sse.wait_until(lambda frames: idle_count(frames, sid) > idles_before, 40)
    last2 = last_session_status(sse.frames, sid)
    if not ok2:
        raise RuntimeError(
            f"A2 follow-up did not produce a second idle (sawBusy={saw_second_busy} last={last2} idleCount={idle_count(sse.frames, sid)} before={idles_before})"
        )
    time.sleep(0.4)
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    _, info, _ = c.request("GET", f"/session/{sid}")
    _, status_map, _ = c.request("GET", "/session/status")
    parsed = parse_messages(messages)
    roles = [m["role"] for m in parsed]
    ids = [m["id"] for m in parsed]
    if roles != ["user", "assistant", "user", "assistant"]:
        raise RuntimeError(f"A2 expected user/assistant/user/assistant, got {roles}")
    if ids[0] != first_mid or ids[2] != follow_mid:
        raise RuntimeError(f"A2 client IDs did not match persisted users first={ids[0]} follow={ids[2]}")
    if isinstance(info, dict) and info.get("id") not in (None, sid):
        raise RuntimeError("A2 session id changed")
    if idle_count(sse.frames, sid) < 2:
        raise RuntimeError(f"A2 expected two idle terminals, idleCount={idle_count(sse.frames, sid)}")
    first_texts = " ".join(first_parsed[0]["texts"])
    follow_texts = " ".join(parsed[2]["texts"])
    if "A2_FIRST" not in first_texts or "A2_FOLLOW_UP" not in follow_texts:
        raise RuntimeError("A2 user texts did not keep first/follow-up order")
    if parsed[0]["id"] == parsed[2]["id"]:
        raise RuntimeError("A2 duplicated the first user message")
    write_sample(
        out / "a2-follow-up.sanitized.json",
        {
            "meta": {
                "scenario": "A2",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "firstPromptHttpStatus": code1,
                "followPromptHttpStatus": code2,
                "variantSent": False,
                "captureStatus": "captured",
            },
            "source": SOURCE_PROMPT,
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sseClassification": classify_sse(sse.frames),
            "sse": sse.frames,
            "reload": {"messages": messages, "session": info, "status": status_map, "afterFirstTurn": after_first},
            "correlation": {
                "sessionID": sid,
                "sessionUnchanged": True,
                "firstClientMessageID": first_mid,
                "followClientMessageID": follow_mid,
                "persistedUserIDs": [ids[0], ids[2]],
                "persistedRoles": roles,
                "idleCount": idle_count(sse.frames, sid),
                "bothTurnsIdle": True,
            },
            "bridgeMapping": {
                "decision": "stable messageID correlation only",
                "notAServerEvent": "official Web optimistic echo is client-local UI, not an OpenCode server event",
                "notAnIOSWriter": "do not create an iOS second timeline writer from optimistic echo",
                "nestedSync": "retained as evidence only; v1 Web skips payload.type==sync",
            },
            "sanitization": SANITIZATION,
        },
        workspace,
    )


def _retry_count(frames: list[dict[str, Any]], sid: str) -> int:
    n = 0
    for frame in frames:
        ev = frame.get("event") if isinstance(frame, dict) else None
        payload = ev.get("payload") if isinstance(ev, dict) and isinstance(ev.get("payload"), dict) else ev
        if not isinstance(payload, dict) or payload.get("type") != "session.status":
            continue
        props = payload.get("properties") if isinstance(payload.get("properties"), dict) else {}
        if props.get("sessionID") not in (None, sid):
            continue
        st = props.get("status")
        typ = st.get("type") if isinstance(st, dict) else st
        if typ == "retry":
            n += 1
    return n


def capture_a3(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    status, created, _ = c.request("POST", "/session", body={})
    if status >= 300 or not isinstance(created, dict):
        raise RuntimeError(f"create failed: {status}")
    sid = created["id"]
    body = prompt_body("A3_PROVIDER_ERROR")
    code, resp, _ = c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    if code not in (200, 204):
        raise RuntimeError(f"prompt_async HTTP {code}: {resp}")
    sse.wait_until(lambda frames: _retry_count(frames, sid) >= 1, 30)
    reached_idle, last = wait_idle(sse, sid, 150)
    time.sleep(0.4)
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    _, info, _ = c.request("GET", f"/session/{sid}")
    _, status_map, _ = c.request("GET", "/session/status")
    retries = _retry_count(sse.frames, sid)
    parsed = parse_messages(messages)
    assistants = [m for m in parsed if m["role"] == "assistant"]
    capture_status = "captured" if reached_idle and retries >= 2 and assistants else "partial"
    write_sample(
        out / "a3-provider-error.sanitized.json",
        {
            "meta": {
                "scenario": "A3",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "captureStatus": capture_status,
                "promptHttpStatus": code,
                "note": "mock returns two retryable HTTP 500 then one non-retryable HTTP 400; 150s idle bound",
            },
            "source": SOURCE_PROMPT,
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sseClassification": classify_sse(sse.frames),
            "sse": sse.frames,
            "reload": {"messages": messages, "session": info, "status": status_map},
            "derived": {
                "retryStatusCount": retries,
                "reachedIdle": reached_idle,
                "lastStatus": last,
                "assistantError": [m.get("error") for m in assistants],
                "assistantFinish": [m.get("finish") for m in assistants],
            },
            "bridgeMapping": {
                "decision": "retry is not idle; terminal is captured session.error and/or assistant info.error",
                "nestedSync": "retained as evidence only; v1 Web skips payload.type==sync",
            },
            "sanitization": SANITIZATION,
        },
        workspace,
    )
    if capture_status != "captured":
        print(
            f"A3 classified {capture_status}: retries={retries} idle={reached_idle} last={last}",
            file=sys.stderr,
        )


def capture_a4(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    status, created, _ = c.request("POST", "/session", body={})
    if status >= 300 or not isinstance(created, dict):
        raise RuntimeError(f"create failed: {status} {created}")
    sid = created["id"]
    body = prompt_body("A4_SLOW_STREAM")
    code, resp, _ = c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    if code not in (200, 204):
        raise RuntimeError(f"prompt_async HTTP {code}: {resp}")
    saw_busy = sse.wait_until(
        lambda frames: last_session_status(frames, sid) == "busy"
        or "message.part.delta" in event_types(frames),
        15,
    )
    if not saw_busy:
        raise RuntimeError(
            f"A4 mock/provider never entered busy or delta; cannot abort a live turn (last={last_session_status(sse.frames, sid)})"
        )
    types_before = list(event_types(sse.frames))
    status_before = last_session_status(sse.frames, sid)
    abort_code, abort_resp, _ = c.request("POST", f"/session/{sid}/abort", body=None, timeout=10)
    reached_idle, last_after = wait_idle(sse, sid, 20)
    time.sleep(0.4)
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    _, info, _ = c.request("GET", f"/session/{sid}")
    _, status_map, _ = c.request("GET", "/session/status")
    parsed = parse_messages(messages)
    assistants = [m for m in parsed if m["role"] == "assistant"]
    synthesized_healthy = any(m.get("finish") in ("stop", "completed") and m.get("error") in (None, {}) for m in assistants) and reached_idle and "A4" in json.dumps(assistants)
    synth = compute_synthesized_healthy_from_parsed(assistants)
    capture_status = "captured" if abort_code in (200, 204) and reached_idle and not synth else "partial"
    write_sample(
        out / "a4-abort.sanitized.json",
        {
            "meta": {
                "scenario": "A4",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "captureStatus": capture_status,
                "promptHttpStatus": code,
                "abortHttpStatus": abort_code,
                "abortResponse": abort_resp,
            },
            "source": {
                "ui": "packages/app/src/utils/server-compat.ts:197-198 session.interrupt → legacy().session.abort",
                "server": "POST /session/:id/abort handlers/session.ts SessionHttpApi.abort → SessionPrompt.cancel; returns true",
                "sse": "packages/app/src/context/server-sdk.tsx v1 global.event",
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sseClassification": classify_sse(sse.frames),
            "sse": sse.frames,
            "reload": {"messages": messages, "session": info, "status": status_map},
            "assertions": {
                "busyOrDeltaBeforeAbort": True,
                "statusBeforeAbort": status_before,
                "typesBeforeAbort": types_before[-12:],
                "abortHttpStatus": abort_code,
                "reachedIdleAfterAbort": reached_idle,
                "statusAfterAbort": last_after,
                "statusMapHasSession": isinstance(status_map, dict) and sid in status_map,
                "assistantCount": len(assistants),
                "assistantFinish": [m.get("finish") for m in assistants],
                "assistantError": [m.get("error") for m in assistants],
                "assistantPartTypes": [m.get("partTypes") for m in assistants],
                "assistantTexts": [m.get("texts") for m in assistants],
                "synthesizedHealthyCompleted": compute_synthesized_healthy_from_parsed(assistants),
            },
            "bridgeMapping": {
                "decision": "abort must converge to the captured non-running server state; do not synthesize a healthy completed assistant",
                "nestedSync": "retained as evidence only; v1 Web skips payload.type==sync",
            },
            "sanitization": SANITIZATION,
        },
        workspace,
    )
    if capture_status != "captured":
        print(
            f"A4 classified {capture_status}: abort HTTP {abort_code} idle={reached_idle} last={last_after}",
            file=sys.stderr,
        )


def capture_a5(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    status, created, _ = c.request("POST", "/session", body={})
    if status >= 300 or not isinstance(created, dict):
        raise RuntimeError(f"create failed: {status}")
    sid = created["id"]
    body = prompt_body("A5_RECONNECT keep streaming")
    code, resp, _ = c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    if code not in (200, 204):
        raise RuntimeError(f"prompt_async HTTP {code}: {resp}")
    ready = sse.wait_until(
        lambda frames: last_session_status(frames, sid) == "busy"
        and "message.part.delta" in event_types(frames),
        20,
    )
    if not ready:
        raise RuntimeError(
            f"A5 never saw busy+partial (last={last_session_status(sse.frames, sid)} types={event_types(sse.frames)[-8:]})"
        )
    _, status_at_disconnect, _ = c.request("GET", "/session/status")
    disconnect_busy = isinstance(status_at_disconnect, dict) and (status_at_disconnect.get(sid) or {}).get("type") == "busy"
    if not disconnect_busy:
        raise RuntimeError(f"A5 /session/status was not busy at disconnect: {status_at_disconnect}")
    frames_before = list(sse.frames)
    types_before = list(event_types(sse.frames))
    sse.stop()
    time.sleep(0.2)
    sse2 = SSE(c)
    sse2.start()
    sse2.wait_until(lambda frames: bool(frames) or bool(sse2._error), 5)
    reached_idle, last = wait_idle(sse2, sid, 40)
    if not reached_idle:
        deadline = time.time() + 20
        while time.time() < deadline:
            _, sm, _ = c.request("GET", "/session/status")
            if not (isinstance(sm, dict) and sid in sm and (sm.get(sid) or {}).get("type") == "busy"):
                break
            time.sleep(0.3)
    time.sleep(0.3)
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    _, info, _ = c.request("GET", f"/session/{sid}")
    _, status_final, _ = c.request("GET", "/session/status")
    after_types = event_types(sse2.frames)
    replayed_deltas = "message.part.delta" in after_types
    capture_status = "captured" if disconnect_busy else "partial"
    if last != "idle" and isinstance(status_final, dict) and sid in status_final:
        capture_status = "partial"
    write_sample(
        out / "a5-sse-reconnect.sanitized.json",
        {
            "meta": {
                "scenario": "A5",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "captureStatus": capture_status,
                "promptHttpStatus": code,
                "sse1Error": sse._error,
                "sse2Error": sse2._error,
                "firstAfterReconnect": after_types[:5],
            },
            "source": {
                "ui": "packages/app/src/context/server-sdk.tsx:268-308 reconnect loop; v1 skips sync",
                "server": "GET /global/event; no assumed replay buffer",
            },
            "http": c.http,
            "sseBefore": frames_before,
            "sseAfterReconnect": sse2.frames,
            "sseEventTypesBefore": types_before,
            "sseEventTypesAfter": after_types,
            "reload": {
                "messages": messages,
                "session": info,
                "status": status_final,
                "statusAtDisconnect": status_at_disconnect,
            },
            "derived": {
                "statusAtDisconnectBusy": disconnect_busy,
                "reconnectSawIdle": last == "idle",
                "reconnectFirstDirectTypes": after_types[:8],
                "serverReplayedDeltas": replayed_deltas,
                "note": "replay is observed, not assumed; v1 mapping still skips nested sync",
            },
            "bridgeMapping": {
                "decision": "reconnect recovers via server messages/status and later Kernel rehydrate; Gate A records server facts only",
                "nestedSync": "retained as evidence only; v1 Web skips payload.type==sync",
                "notAnIOSWriter": "do not history-merge or raw-write the iOS timeline",
            },
            "sanitization": SANITIZATION,
        },
        workspace,
    )
    sse2.stop()
    if capture_status != "captured":
        print(f"A5 classified {capture_status}: last={last}", file=sys.stderr)


def _count_type(frames: list[dict[str, Any]], typ: str, start: int = 0) -> int:
    n = 0
    for i, t in enumerate(event_types(frames)):
        if i < start:
            continue
        if t == typ:
            n += 1
    return n


def capture_a6(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    def one(response: str, extra_follow: bool) -> dict[str, Any]:
        mark = len(sse.frames)
        st, created, _ = c.request("POST", "/session", body={})
        if st >= 300 or not isinstance(created, dict):
            raise RuntimeError(f"A6 create failed {st}")
        sid = created["id"]
        body = prompt_body("A6_READ_OUTSIDE")
        code, resp, _ = c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
        asked = sse.wait_until(lambda frames: _count_type(frames, "permission.asked", mark) >= 1, 20)
        time.sleep(0.3)
        _, pending, _ = c.request("GET", "/permission")
        pending_for = [p for p in pending if isinstance(pending, list) and isinstance(p, dict) and p.get("sessionID") == sid]
        if not asked or not pending_for:
            wait_idle(sse, sid, 10)
            return {
                "sessionID": sid,
                "asked": False,
                "promptHttpStatus": code,
                "pending": pending,
                "blocked": True,
            }
        req = pending_for[0]
        pid = req.get("id")
        rcode, rresp, _ = c.request(
            "POST",
            f"/session/{sid}/permissions/{pid}",
            body={"response": response},
            timeout=15,
        )
        sse.wait_until(lambda frames: _count_type(frames, "permission.replied", mark) >= 1, 10)
        _, pending_after, _ = c.request("GET", "/permission")
        idle1, last1 = wait_idle(sse, sid, 25)
        asked_again = None
        follow = None
        if extra_follow:
            mark2 = len(sse.frames)
            follow_body = prompt_body("A6_READ_OUTSIDE")
            fcode, _, _ = c.request("POST", f"/session/{sid}/prompt_async", body=follow_body, timeout=20)
            asked_again = sse.wait_until(lambda frames: _count_type(frames, "permission.asked", mark2) >= 1, 12)
            wait_idle(sse, sid, 25)
            follow = {"promptHttpStatus": fcode, "askedAgain": asked_again}
        _, messages, _ = c.request("GET", f"/session/{sid}/message")
        _, status_map, _ = c.request("GET", "/session/status")
        return {
            "sessionID": sid,
            "asked": True,
            "blocked": False,
            "promptHttpStatus": code,
            "pendingRequest": req,
            "replyHttpStatus": rcode,
            "replyResponse": rresp,
            "replyBody": {"response": response},
            "pendingAfter": pending_after,
            "idle": idle1,
            "lastStatus": last1,
            "follow": follow,
            "messages": messages,
            "status": status_map,
        }

    # reject first: an earlier always would persist the pattern for the
    # workspace and suppress later asks.
    reject = one("reject", False)
    once = one("once", False)
    always = one("always", True)
    blocked = once.get("blocked") or always.get("blocked") or reject.get("blocked")
    write_sample(
        out / "a6-permission.sanitized.json",
        {
            "meta": {
                "scenario": "A6",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "captureStatus": "blocked" if blocked else "captured",
            },
            "source": {
                "ui": "packages/app/src/utils/server-compat.ts:496-503 permission.respond; session-permission-dock.tsx",
                "server": "POST /session/:id/permissions/:id {response: once|always|reject}; GET /permission; events permission.asked/replied",
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"once": once, "always": always, "reject": reject},
            "bridgeMapping": {
                "decision": "permission is control-plane plus Kernel-canonical state; raw permission must not write iOS messages[]",
                "v1Reply": "session-scoped {response}",
            },
            "sanitization": SANITIZATION,
        },
        workspace,
    )
    if blocked:
        print("A6 blocked: permission.asked not observed for at least one subscenario", file=sys.stderr)


def capture_a7(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    def one(kind: str) -> dict[str, Any]:
        mark = len(sse.frames)
        st, created, _ = c.request("POST", "/session", body={})
        sid = created["id"]
        body = prompt_body("A7_QUESTION")
        code, _, _ = c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
        asked = sse.wait_until(lambda frames: _count_type(frames, "question.asked", mark) >= 1, 20)
        time.sleep(0.3)
        _, pending, _ = c.request("GET", "/question")
        pending_for = [p for p in pending if isinstance(pending, list) and isinstance(p, dict) and p.get("sessionID") == sid]
        if not asked or not pending_for:
            wait_idle(sse, sid, 10)
            return {"sessionID": sid, "asked": False, "blocked": True, "pending": pending}
        req = pending_for[0]
        qid = req.get("id")
        if kind == "reply":
            answers = [["red"]]
            rcode, rresp, _ = c.request("POST", f"/question/{qid}/reply", body={"answers": answers}, timeout=15)
            sse.wait_until(lambda frames: _count_type(frames, "question.replied", mark) >= 1, 10)
            action = {"path": f"/question/{qid}/reply", "body": {"answers": answers}, "status": rcode, "response": rresp}
        else:
            rcode, rresp, _ = c.request("POST", f"/question/{qid}/reject", timeout=15)
            sse.wait_until(lambda frames: _count_type(frames, "question.rejected", mark) >= 1, 10)
            action = {"path": f"/question/{qid}/reject", "status": rcode, "response": rresp}
        _, pending_after, _ = c.request("GET", "/question")
        idle, last = wait_idle(sse, sid, 25)
        _, messages, _ = c.request("GET", f"/session/{sid}/message")
        _, status_map, _ = c.request("GET", "/session/status")
        return {
            "sessionID": sid,
            "asked": True,
            "blocked": False,
            "promptHttpStatus": code,
            "pendingRequest": req,
            "action": action,
            "pendingAfter": pending_after,
            "idle": idle,
            "lastStatus": last,
            "messages": messages,
            "status": status_map,
        }

    reply = one("reply")
    reject = one("reject")
    blocked = reply.get("blocked") or reject.get("blocked")
    write_sample(
        out / "a7-question.sanitized.json",
        {
            "meta": {
                "scenario": "A7",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "captureStatus": "blocked" if blocked else "captured",
            },
            "source": {
                "ui": "packages/app/src/pages/session/composer/session-question-dock.tsx reply answers:string[][]",
                "server": "GET /question; POST /question/:id/reply {answers:string[][]}; POST /question/:id/reject; events asked/replied/rejected",
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"reply": reply, "reject": reject},
            "bridgeMapping": {
                "decision": "question is distinct from permission; canonical path is user_input_requested/resolved; do not invent question_resolved",
            },
            "sanitization": SANITIZATION,
        },
        workspace,
    )
    if blocked:
        print("A7 blocked: question.asked not observed", file=sys.stderr)


def _todo_updated_count(frames: list[dict[str, Any]], sid: str, start: int = 0) -> int:
    n = 0
    for i, frame in enumerate(frames):
        if i < start:
            continue
        ev = frame.get("event") if isinstance(frame, dict) else None
        payload = ev.get("payload") if isinstance(ev, dict) and isinstance(ev.get("payload"), dict) else ev
        if not isinstance(payload, dict) or payload.get("type") != "todo.updated":
            continue
        props = payload.get("properties") if isinstance(payload.get("properties"), dict) else {}
        if props.get("sessionID") not in (None, sid):
            continue
        n += 1
    return n


def capture_a8(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    st, created, _ = c.request("POST", "/session", body={})
    sid = created["id"]
    mark = len(sse.frames)
    body = prompt_body("A8_TODOWRITE")
    c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
    updated = sse.wait_until(
        lambda frames: _todo_updated_count(frames, sid, mark) >= 1 or _todo_updated_count(frames, sid, mark) > 20,
        20,
    )
    if _todo_updated_count(sse.frames, sid, mark) > 20:
        print("A8 warning: todo.updated flood on first write; stopping extra wait", file=sys.stderr)
    time.sleep(0.4)
    _, todos1, _ = c.request("GET", f"/session/{sid}/todo")
    if not updated:
        wait_idle(sse, sid, 10)
        write_sample(
            out / "a8-todos.sanitized.json",
            {
                "meta": {
                    "scenario": "A8",
                    "opencodeVersion": OPENCODE_VERSION,
                    "sourceCommit": SOURCE_COMMIT,
                    "captureStatus": "blocked",
                },
                "source": {"server": "GET /session/:id/todo; event todo.updated; tool todowrite"},
                "http": c.http,
                "sse": sse.frames,
                "reload": {"todosAfterFirst": todos1},
                "bridgeMapping": {
                    "controlPlane": True,
                    "stableIdentity": "Gate B",
                    "notTimeline": True,
                },
                "sanitization": SANITIZATION,
            },
            workspace,
        )
        print("A8 blocked: todo.updated not observed", file=sys.stderr)
        return
    wait_idle(sse, sid, 20)
    mark2 = len(sse.frames)
    body2 = prompt_body("A8_TODOWRITE_UPDATE")
    c.request("POST", f"/session/{sid}/prompt_async", body=body2, timeout=20)
    sse.wait_until(
        lambda frames: _todo_updated_count(frames, sid, mark2) >= 1 or _todo_updated_count(frames, sid, mark2) > 20,
        20,
    )
    if _todo_updated_count(sse.frames, sid, mark2) > 20:
        print("A8 warning: todo.updated flood on update; stopping extra wait", file=sys.stderr)
    wait_idle(sse, sid, 20)
    _, todos2, _ = c.request("GET", f"/session/{sid}/todo")
    _, messages, _ = c.request("GET", f"/session/{sid}/message")
    keys = []
    if isinstance(todos2, list) and todos2 and isinstance(todos2[0], dict):
        keys = sorted(todos2[0].keys())
    write_sample(
        out / "a8-todos.sanitized.json",
        {
            "meta": {
                "scenario": "A8",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "captureStatus": "captured",
            },
            "source": {
                "ui": "packages/app/src/pages/session.tsx todo(); session-todo-dock.tsx",
                "server": "packages/opencode/src/session/todo.ts persist {content,status,priority} no id; GET /session/:id/todo; event todo.updated",
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {"todosAfterFirst": todos1, "todosAfterUpdate": todos2, "messages": messages, "itemKeys": keys},
            "bridgeMapping": {
                "controlPlane": True,
                "stableIdentity": "left to Gate B; do not invent hash/content/position ids",
                "notTimeline": "todo events must not enter SessionProjection timeline",
            },
            "sanitization": SANITIZATION,
        },
        workspace,
    )


def _mock_base() -> str:
    return os.environ.get("OCW_MOCK_BASE", "http://127.0.0.1:4399")


def fetch_mock_observations() -> list[dict[str, Any]]:
    url = _mock_base().rstrip("/") + "/_debug/observations"
    try:
        with urlopen(url, timeout=2) as resp:
            data = json.loads(resp.read().decode("utf-8") or "[]")
            return data if isinstance(data, list) else []
    except Exception as exc:  # noqa: BLE001
        return [{"error": str(exc)}]


def reset_mock_observations() -> None:
    url = _mock_base().rstrip("/") + "/_debug/reset"
    try:
        req = Request(url, data=b"{}", method="POST", headers={"Content-Type": "application/json"})
        with urlopen(req, timeout=2) as resp:
            resp.read()
    except Exception:
        pass


def capture_a9(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    readme = Path(workspace) / "README.md"
    png = Path(workspace) / "pixel.png"
    png.write_bytes(
        bytes.fromhex(
            "89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000a49444154789c63000100000500010d0a2db40000000049454e44ae426082"
        )
    )

    def run(label: str, extra: list[dict] | None, text: str) -> dict[str, Any]:
        reset_mock_observations()
        st, created, _ = c.request("POST", "/session", body={})
        sid = created["id"]
        body = prompt_body(text, extra_parts=extra)
        code, resp, _ = c.request("POST", f"/session/{sid}/prompt_async", body=body, timeout=20)
        accepted = code in (200, 204)
        if accepted:
            wait_idle(sse, sid, 25)
        _, messages, _ = c.request("GET", f"/session/{sid}/message") if accepted else (0, None, {})
        user_parts = []
        if isinstance(messages, list) and messages:
            user = next((m for m in messages if (m.get("info") or {}).get("role") == "user"), None)
            if user:
                user_parts = user.get("parts") or []
        _, status_map, _ = c.request("GET", "/session/status") if accepted else (0, None, {})
        mock_obs = [
            rec
            for rec in fetch_mock_observations()
            if isinstance(rec, dict) and rec.get("path") in ("/v1/chat/completions", "/chat/completions")
        ]
        return {
            "label": label,
            "sessionID": sid,
            "promptHttpStatus": code,
            "promptResponse": resp,
            "accepted": accepted,
            "promptBody": body,
            "persistedUserParts": user_parts,
            "messages": messages,
            "status": status_map,
            "mockObservations": mock_obs,
        }

    text = run("text", None, "A9_TEXT_ONLY")
    file_plain = run(
        "file",
        [{"type": "file", "mime": "text/plain", "url": "file://" + str(readme), "filename": "README.md"}],
        "A9_FILE_PART",
    )
    file_mention = run(
        "fileMention",
        [
            {
                "type": "file",
                "mime": "text/plain",
                "url": "file://" + str(readme),
                "filename": "README.md",
                "source": {
                    "type": "file",
                    "text": {"value": "README.md", "start": 0, "end": 9},
                    "path": str(readme),
                },
            }
        ],
        "A9_FILE_MENTION README.md",
    )
    image = run(
        "image",
        [
            {
                "type": "file",
                "mime": "image/png",
                "url": "file://" + str(png),
                "filename": "pixel.png",
            }
        ],
        "A9_IMAGE_PART",
    )
    agent = run(
        "agent",
        [{"type": "agent", "name": "plan", "source": {"value": "@plan", "start": 0, "end": 5}}],
        "A9_AGENT_PART @plan",
    )
    write_sample(
        out / "a9-prompt-parts.sanitized.json",
        {
            "meta": {
                "scenario": "A9",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "captureStatus": "captured",
            },
            "source": {
                "ui": "packages/app/src/utils/server-compat.ts:207-228 text/file/agent parts",
                "server": "packages/opencode/src/session/prompt.ts PromptInput.parts",
            },
            "http": c.http,
            "sseEventTypes": event_types(sse.frames),
            "sse": sse.frames,
            "reload": {
                "text": text,
                "file": file_plain,
                "fileMention": file_mention,
                "image": image,
                "agent": agent,
            },
            "bridgeMapping": {
                "decision": "each part type is independently captured; unsupported parts fail closed later in C3",
            },
            "sanitization": SANITIZATION,
        },
        workspace,
    )


def _ids(items: Any) -> list[str]:
    if not isinstance(items, list):
        return []
    out = []
    for item in items:
        if isinstance(item, dict) and item.get("id"):
            out.append(item["id"])
    return out


def capture_a10(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    ws2 = str(Path(workspace).parent / "workspace2")
    if not Path(ws2).is_dir():
        raise RuntimeError(f"second directory missing: {ws2}")
    c2 = c.clone(ws2)
    _, baseline1, _ = c.request("GET", "/session", query={"roots": "true", "limit": "10"})
    _, baseline2, _ = c2.request("GET", "/session", query={"roots": "true", "limit": "10"})
    if _ids(baseline1) or _ids(baseline2):
        raise RuntimeError(f"A10 sandbox not empty baseline w1={_ids(baseline1)} w2={_ids(baseline2)}")
    roots = []
    for i in range(4):
        code, created, _ = c.request("POST", "/session", body={"title": f"root-{i}"})
        if code >= 300 or not isinstance(created, dict):
            raise RuntimeError(f"root create {i} failed: {code}")
        roots.append(created["id"])
    parent = roots[0]
    child_code, child, _ = c.request("POST", "/session", body={"parentID": parent, "title": "child-0"})
    if child_code >= 300 or not isinstance(child, dict):
        raise RuntimeError(f"child create failed: {child_code}")
    archive_ts = 1787180000123
    arch_id = roots[1]
    patch_code, archived, _ = c.request("PATCH", f"/session/{arch_id}", body={"time": {"archived": archive_ts}})
    dir2_code, dir2_session, _ = c2.request("POST", "/session", body={"title": "other-dir-root"})
    if dir2_code >= 300 or not isinstance(dir2_session, dict):
        raise RuntimeError(f"workspace2 create failed: {dir2_code}")

    _, listed_default, _ = c.request("GET", "/session")
    _, listed_roots_over, _ = c.request("GET", "/session", query={"roots": "true", "limit": "2"})
    _, listed_roots_at, _ = c.request("GET", "/session", query={"roots": "true", "limit": "3"})
    _, listed_roots_under, _ = c.request("GET", "/session", query={"roots": "true", "limit": "10"})
    _, listed_limit1, _ = c.request("GET", "/session", query={"roots": "true", "limit": "1"})
    _, children, _ = c.request("GET", f"/session/{parent}/children")
    get_code, got, _ = c.request("GET", f"/session/{arch_id}")
    _, listed_dir2, _ = c2.request("GET", "/session", query={"roots": "true", "limit": "10"})

    default_ids = _ids(listed_default)
    over_ids = _ids(listed_roots_over)
    at_ids = _ids(listed_roots_at)
    under_ids = _ids(listed_roots_under)
    dir2_ids = _ids(listed_dir2)
    child_ids = _ids(children)
    archived_in_default = arch_id in default_ids
    archived_in_roots = arch_id in under_ids
    child_in_roots = child.get("id") in under_ids
    dir_leak = any(i in dir2_ids for i in roots) or dir2_session["id"] in default_ids

    write_sample(
        out / "a10-session-listing.sanitized.json",
        {
            "meta": {
                "scenario": "A10",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "captureStatus": "captured",
            },
            "source": {
                "ui": "packages/app/src/context/global-sync/session-load.ts:5-26 roots+limit; archived hidden by event-reducer.ts:149-161 on session.updated when time.archived is set",
                "server": "packages/opencode/src/server/routes/instance/httpapi/groups/session.ts ListQuery roots/limit; GET /session/:id; GET /session/:id/children; PATCH time.archived",
            },
            "http": c.http + c2.http,
            "sseEventTypes": event_types(sse.frames),
            "sseClassification": classify_sse(sse.frames),
            "sse": sse.frames,
            "reload": {
                "workspace1": workspace,
                "workspace2": ws2,
                "baseline": {"workspace1": baseline1, "workspace2": baseline2},
                "rootIDs": roots,
                "child": child,
                "archivePatchStatus": patch_code,
                "archivePatch": archived,
                "listedDefault": listed_default,
                "listedRootsLimit1": listed_limit1,
                "listedRootsLimit2": listed_roots_over,
                "listedRootsLimit3": listed_roots_at,
                "listedRootsLimit10": listed_roots_under,
                "children": children,
                "archivedGetStatus": get_code,
                "archivedGet": got,
                "listedDir2": listed_dir2,
            },
            "assertions": {
                "defaultCount": len(default_ids),
                "rootsLimit1Count": len(_ids(listed_limit1)),
                "rootsLimit2Count": len(over_ids),
                "rootsLimit3Count": len(at_ids),
                "rootsLimit10Count": len(under_ids),
                "childCreateStatus": child_code,
                "childIDs": child_ids,
                "childAppearsInRootsLimit10": child_in_roots,
                "archivedStillInDefaultList": archived_in_default,
                "archivedStillInRootsList": archived_in_roots,
                "archivedByIdStatus": get_code,
                "archivedByIdHasTimestamp": isinstance(got, dict)
                and isinstance(got.get("time"), dict)
                and bool(got.get("time", {}).get("archived")),
                "dir2Count": len(dir2_ids),
                "directoriesIsolated": not dir_leak,
                "officialWebHidesArchived": "event-reducer.ts splices archived sessions out of the home list on session.updated; API list is a separate fact",
                "cordcodeAggregationIsGateB": True,
            },
            "bridgeMapping": {
                "decision": "Gate A records official list/get/archive shapes only",
                "notDecidedHere": "whether CordCode aggregates multiple directories or displays archived rows is a Gate B product disposition",
            },
            "sanitization": SANITIZATION,
        },
        workspace,
    )
    if dir_leak:
        raise RuntimeError("A10 directories leaked session ids across workspaces")
    if get_code != 200:
        raise RuntimeError(f"A10 archived by-id get failed: {get_code}")


def _git_worktree(root: Path, name: str) -> str:
    import subprocess

    d = root / name
    d.mkdir(parents=True, exist_ok=True)
    subprocess.run(["git", "init", "-q"], cwd=d, check=True)
    (d / "README.md").write_text(name + "\n")
    env_git = ["git", "-C", str(d), "-c", "user.name=gatea", "-c", "user.email=gatea@invalid"]
    subprocess.run(env_git + ["add", "."], check=True)
    subprocess.run(env_git + ["commit", "-qm", "wp init"], check=True)
    return str(d)


def capture_wp(c: Client, sse: SSE, out: Path, workspace: str) -> None:
    """Workspace.project registry sample (directive-003 Phase 0).

    Observed facts (1.18.18 @2cba7e227d): a plain non-git directory folds
    into the global pseudo-project (worktree='/', directory appended to
    global.sandboxes — core/project.ts resolve + project.ts:243-244); only a
    git worktree with at least one commit becomes its own project row
    (core/project.ts:105-119 rootCommits/remote derive the id). The capture
    therefore registers two harness-owned git worktrees, then deletes one
    (harness-owned dir only) to observe whether the server registry keeps the
    entry — registry truth vs local existence overlay.
    """
    import shutil

    root = Path(workspace).parent
    ws2 = str(root / "workspace2")
    if not Path(ws2).is_dir():
        raise RuntimeError(f"second directory missing: {ws2}")
    c2 = c.clone(ws2)

    # Baseline: opening non-git harness workspaces registers only the global
    # pseudo-project (the directories fold into global.sandboxes).
    c2.request("GET", "/session", query={"roots": "true", "limit": "1"})
    code0, base_projects, _ = c.request("GET", "/project", query={})
    if code0 != 200 or not isinstance(base_projects, list):
        raise RuntimeError(f"GET /project baseline failed: {code0}")

    g1 = _git_worktree(root, "wsgit1")
    g2 = _git_worktree(root, "wsgit2")
    cg1 = c.clone(g1)
    cg2 = c.clone(g2)
    s1_code, s1, _ = cg1.request("POST", "/session", body={"title": "wp-git-1"})
    s2_code, s2, _ = cg2.request("POST", "/session", body={"title": "wp-git-2"})
    if s1_code >= 300 or s2_code >= 300:
        raise RuntimeError(f"git-worktree session create failed: {s1_code}/{s2_code}")
    _, after_create, _ = c.request("GET", "/project", query={})
    if not isinstance(after_create, list):
        raise RuntimeError("GET /project after create was not a list")

    shutil.rmtree(g2, ignore_errors=True)
    _, after_delete, _ = c.request("GET", "/project", query={})
    if not isinstance(after_delete, list):
        raise RuntimeError("GET /project after delete was not a list")

    def worktrees(items: Any) -> list[str]:
        return [str(item.get("worktree") or "") for item in items if isinstance(item, dict)]

    def entry_map(items: Any) -> dict[str, dict[str, Any]]:
        outm: dict[str, dict[str, Any]] = {}
        for item in items:
            if isinstance(item, dict) and item.get("id"):
                outm[str(item["id"])] = item
        return outm

    import os as _os

    def real(p: str) -> str:
        return _os.path.realpath(p)

    wts = worktrees(after_create)
    if real(g1) not in wts or real(g2) not in wts:
        raise RuntimeError(f"git worktrees not registered as projects: {wts}")
    distinct = [w for w in wts if w not in ("/", "")]
    if len(distinct) < 2:
        raise RuntimeError(f"need >=2 distinct worktree entries, got {wts}")

    presence = [
        {
            "id": bool(item.get("id")),
            "worktree": bool(item.get("worktree")),
            "vcs": "vcs" in item,
            "time": "time" in item,
            "sandboxes": "sandboxes" in item,
        }
        for item in after_create
        if isinstance(item, dict)
    ]

    write_sample(
        out / "wp-workspace-project.sanitized.json",
        {
            "meta": {
                "scenario": "WP",
                "opencodeVersion": OPENCODE_VERSION,
                "sourceCommit": SOURCE_COMMIT,
                "captureStatus": "captured",
            },
            "source": {
                "ui": "packages/app/src/utils/server-compat.ts:304 legacy().project.list() (data ?? [])",
                "server": "packages/opencode/src/server/routes/instance/httpapi/handlers/project.ts:15-17 list -> Project.list; packages/opencode/src/project/project.ts:336 list (DB rows), :35-56 fromRow, :217 global pseudo-project worktree='/', :243-244 non-worktree directory folds into sandboxes; packages/core/src/project.ts:105-119 git rootCommits/remote derive project id",
            },
            "http": [
                {"step": "baseline", "method": "GET", "path": "/project", "status": code0},
                {"step": "git1-session", "method": "POST", "path": "/session", "status": s1_code},
                {"step": "git2-session", "method": "POST", "path": "/session", "status": s2_code},
                {"step": "after-create", "method": "GET", "path": "/project", "status": 200},
                {"step": "after-delete", "method": "GET", "path": "/project", "status": 200},
            ],
            "baseline": {"count": len(base_projects), "worktrees": worktrees(base_projects)},
            "afterCreate": {
                "count": len(after_create),
                "worktrees": wts,
                "gitWorktreesRegistered": [real(g1) in wts, real(g2) in wts],
                "distinctNonGlobalWorktrees": len(distinct),
                "realpathNote": "registry stores os.path.realpath worktrees (/private/tmp on macOS); git worktrees registered only with >=1 commit",
            },
            "afterDelete": {
                "count": len(after_delete),
                "worktrees": worktrees(after_delete),
                "deletedGitWorktreeStillRegistered": real(g2) in worktrees(after_delete),
                "deletedDirectory": g2,
                "note": "harness-owned temp dir; observation only — CordCode missing-worktree handling is a client-side visibility overlay, never a server claim",
            },
            "fieldPresenceObserved": presence,
            "rawPayloads": {
                "baseline": base_projects,
                "afterCreate": after_create,
                "afterDelete": after_delete,
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
    "wp": capture_wp,
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
