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
    capture_status = "captured" if abort_code in (200, 204) and reached_idle else "partial"
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
                "synthesizedHealthyCompleted": False,
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
