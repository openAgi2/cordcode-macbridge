#!/usr/bin/env python3
"""Classify A1–A10 samples from raw http/sse/reload fields.

Summary fields (correlation/assertions/captureStatus) are cross-checked when
present but are never the primary success evidence.
"""

from __future__ import annotations

import copy
import json
import sys
import tempfile
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parent.parent
SAMPLES = HERE / "samples"

SCENARIOS = {
    "A1": "a1-first-healthy-text.sanitized.json",
    "A2": "a2-follow-up.sanitized.json",
    "A3": "a3-provider-error.sanitized.json",
    "A4": "a4-abort.sanitized.json",
    "A5": "a5-sse-reconnect.sanitized.json",
    "A6": "a6-permission.sanitized.json",
    "A7": "a7-question.sanitized.json",
    "A8": "a8-todos.sanitized.json",
    "A9": "a9-prompt-parts.sanitized.json",
    "A10": "a10-session-listing.sanitized.json",
}


def load(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def leaks(doc: dict) -> list[str]:
    dumped = json.dumps(doc)
    hits = []
    if "gatea-pass" in dumped or "OPENCODE_SERVER_PASSWORD" in dumped:
        hits.append("secret")
    if "/Users/jacklee" in dumped:
        hits.append("host-path")
    if "Basic " in dumped and "Authorization" in dumped:
        hits.append("authorization")
    return hits


def common(doc: dict) -> list[str]:
    bad = []
    meta = doc.get("meta") or {}
    if meta.get("opencodeVersion") != "1.18.18":
        bad.append("version")
    if meta.get("sourceCommit") != "2cba7e227d":
        bad.append("commit")
    for key in ("scenario", "opencodeVersion", "sourceCommit"):
        if not meta.get(key):
            bad.append(f"meta.{key}")
    for key in ("source", "http", "sanitization"):
        if key not in doc:
            bad.append(key)
    if "sse" not in doc and "sseBefore" not in doc:
        bad.append("sse")
    if "reload" not in doc:
        bad.append("reload")
    bad.extend(leaks(doc))
    return bad


def payload(frame: Any) -> dict:
    if not isinstance(frame, dict):
        return {}
    ev = frame.get("event") if isinstance(frame.get("event"), dict) else frame
    if isinstance(ev.get("payload"), dict):
        return ev["payload"]
    return ev if isinstance(ev, dict) else {}


def direct_payloads(frames: list) -> list[dict]:
    out = []
    for frame in frames or []:
        p = payload(frame)
        if p.get("type") and p.get("type") != "sync":
            out.append(p)
    return out


def session_of(p: dict) -> str | None:
    props = p.get("properties") if isinstance(p.get("properties"), dict) else {}
    if props.get("sessionID"):
        return props.get("sessionID")
    info = props.get("info") if isinstance(props.get("info"), dict) else {}
    if info.get("sessionID"):
        return info.get("sessionID")
    if info.get("id") and p.get("type", "").startswith("session."):
        return info.get("id")
    return p.get("sessionID")


def status_seq(frames: list, sid: str) -> list[str]:
    seq = []
    for p in direct_payloads(frames):
        if session_of(p) not in (None, sid):
            continue
        if p.get("type") == "session.status":
            props = p.get("properties") if isinstance(p.get("properties"), dict) else {}
            st = props.get("status")
            if isinstance(st, dict) and st.get("type"):
                seq.append(st.get("type"))
            elif isinstance(st, str):
                seq.append(st)
        elif p.get("type") == "session.idle":
            seq.append("session.idle")
    return seq


def has_busy_then_idle(frames: list, sid: str) -> bool:
    seq = status_seq(frames, sid)
    if "busy" not in seq:
        return False
    idle_at = next((i for i, t in enumerate(seq) if t in ("idle", "session.idle")), -1)
    busy_at = next((i for i, t in enumerate(seq) if t == "busy"), -1)
    return busy_at != -1 and idle_at != -1 and busy_at < idle_at


def idle_count(frames: list, sid: str) -> int:
    return sum(1 for t in status_seq(frames, sid) if t in ("idle", "session.idle"))


def retry_count(frames: list, sid: str) -> int:
    return sum(1 for t in status_seq(frames, sid) if t == "retry")


def http_rows(doc: dict) -> list[dict]:
    return [h for h in (doc.get("http") or []) if isinstance(h, dict)]


def creates(doc: dict) -> list[dict]:
    return [
        h
        for h in http_rows(doc)
        if h.get("method") == "POST" and h.get("path") == "/session" and h.get("status") in (200, 201)
    ]


def prompt_asyncs(doc: dict) -> list[dict]:
    return [
        h
        for h in http_rows(doc)
        if h.get("method") == "POST" and str(h.get("path") or "").endswith("/prompt_async")
    ]


def reload_messages(doc: dict) -> list[dict]:
    msgs = (doc.get("reload") or {}).get("messages")
    return msgs if isinstance(msgs, list) else []


def msg_role(item: dict) -> str | None:
    info = item.get("info") if isinstance(item.get("info"), dict) else {}
    return info.get("role")


def msg_id(item: dict) -> str | None:
    info = item.get("info") if isinstance(item.get("info"), dict) else {}
    return info.get("id")


def assistant_error(item: dict) -> dict | None:
    info = item.get("info") if isinstance(item.get("info"), dict) else {}
    err = info.get("error")
    return err if isinstance(err, dict) else None


def assistant_finish(item: dict) -> Any:
    info = item.get("info") if isinstance(item.get("info"), dict) else {}
    return info.get("finish")


def compute_synthesized_healthy(messages: list) -> bool:
    for item in messages:
        if msg_role(item) != "assistant":
            continue
        err = assistant_error(item)
        finish = assistant_finish(item)
        if finish in ("stop", "completed") and not err:
            return True
    return False


def ids_of(items: Any) -> list[str]:
    if not isinstance(items, list):
        return []
    return [i.get("id") for i in items if isinstance(i, dict) and i.get("id")]


def mismatch(summary: Any, computed: Any, label: str, bad: list[str]) -> None:
    if summary is None:
        return
    if summary != computed:
        bad.append(f"summary-mismatch:{label}")


def classify_a1(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    prompts = prompt_asyncs(doc)
    if not created:
        bad.append("missing-create")
    if len(prompts) < 1:
        bad.append("missing-prompt")
        return "partial", bad
    body = prompts[0].get("body") or {}
    for key in ("messageID", "agent", "model", "parts"):
        if key not in body:
            bad.append(f"prompt-missing-{key}")
    model = body.get("model") if isinstance(body.get("model"), dict) else {}
    if not model.get("providerID") or not model.get("modelID"):
        bad.append("prompt-model")
    parts = body.get("parts") if isinstance(body.get("parts"), list) else []
    if not any(isinstance(p, dict) and p.get("type") == "text" for p in parts):
        bad.append("prompt-text-part")
    sid = (created[0].get("response") or {}).get("id") if created else None
    path = str(prompts[0].get("path") or "")
    if sid and sid not in path:
        bad.append("prompt-session-mismatch")
    messages = reload_messages(doc)
    roles = [msg_role(m) for m in messages]
    if roles != ["user", "assistant"]:
        bad.append(f"roles={roles}")
    users = [m for m in messages if msg_role(m) == "user"]
    if not users or msg_id(users[0]) != body.get("messageID"):
        bad.append("messageID-not-persisted")
    frames = doc.get("sse") or []
    if not has_busy_then_idle(frames, sid or ""):
        bad.append("no-busy-then-idle")
    if not any(payload(f).get("type") == "sync" for f in frames):
        bad.append("no-nested-sync")
    corr = doc.get("correlation") or {}
    mismatch(corr.get("clientIdPersisted"), msg_id(users[0]) == body.get("messageID") if users else False, "clientIdPersisted", bad)
    return ("captured" if not bad else "partial"), bad


def classify_a2(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    prompts = prompt_asyncs(doc)
    if not created:
        bad.append("missing-create")
    if len(prompts) < 2:
        bad.append("need-two-prompts")
        return "partial", bad
    mids = []
    for prompt in prompts[:2]:
        body = prompt.get("body") or {}
        for key in ("messageID", "agent", "model", "parts"):
            if key not in body:
                bad.append(f"prompt-missing-{key}")
        mids.append(body.get("messageID"))
    if len(set(mids)) != 2:
        bad.append("prompt-ids-not-distinct")
    sid = (created[0].get("response") or {}).get("id") if created else None
    for prompt in prompts[:2]:
        if sid and sid not in str(prompt.get("path") or ""):
            bad.append("follow-up-session-changed")
    messages = reload_messages(doc)
    roles = [msg_role(m) for m in messages]
    if roles != ["user", "assistant", "user", "assistant"]:
        bad.append(f"roles={roles}")
    users = [m for m in messages if msg_role(m) == "user"]
    user_ids = [msg_id(m) for m in users]
    if user_ids != mids:
        bad.append(f"user-ids={user_ids} prompt-ids={mids}")
    frames = doc.get("sse") or []
    if idle_count(frames, sid or "") < 2:
        bad.append("idle-count=" + str(idle_count(frames, sid or "")))
    if not has_busy_then_idle(frames, sid or ""):
        bad.append("no-busy-then-idle")
    corr = doc.get("correlation") or {}
    mismatch(corr.get("bothTurnsIdle"), idle_count(frames, sid or "") >= 2, "bothTurnsIdle", bad)
    mismatch(corr.get("sessionUnchanged"), True, "sessionUnchanged", bad)
    return ("captured" if not bad else "partial"), bad


def classify_a4(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    prompts = prompt_asyncs(doc)
    aborts = [
        h
        for h in http_rows(doc)
        if h.get("method") == "POST" and str(h.get("path") or "").endswith("/abort")
    ]
    if not created or not prompts or not aborts:
        bad.append("missing-http-order")
        return "partial", bad
    http = http_rows(doc)
    prompt_i = http.index(prompts[0])
    abort_i = http.index(aborts[0])
    if prompt_i >= abort_i:
        bad.append("abort-before-prompt")
    if aborts[0].get("status") not in (200, 204) or aborts[0].get("response") is not True:
        bad.append("abort-http")
    sid = (created[0].get("response") or {}).get("id")
    frames = doc.get("sse") or []
    seq = status_seq(frames, sid or "")
    busy_at = next((i for i, t in enumerate(seq) if t == "busy"), -1)
    err_payloads = [
        p
        for p in direct_payloads(frames)
        if p.get("type") == "session.error" and session_of(p) in (None, sid)
    ]
    idle_at = next((i for i, t in enumerate(seq) if t in ("idle", "session.idle")), -1)
    if busy_at == -1:
        bad.append("never-busy")
        return "blocked", bad
    if not err_payloads:
        bad.append("missing-session-error")
    else:
        err = (err_payloads[0].get("properties") or {}).get("error")
        if not (isinstance(err, dict) and err.get("name")):
            bad.append("session-error-empty")
    if idle_at == -1 or busy_at >= idle_at:
        bad.append("busy-not-before-idle")
    messages = reload_messages(doc)
    assistants = [m for m in messages if msg_role(m) == "assistant"]
    if not assistants:
        bad.append("no-assistant")
    else:
        err = assistant_error(assistants[0]) or {}
        name = err.get("name")
        msg = (err.get("data") or {}).get("message") if isinstance(err.get("data"), dict) else None
        if name != "MessageAbortedError" or msg != "Aborted":
            bad.append(f"assistant-error={err}")
        if assistant_finish(assistants[0]) in ("stop", "completed"):
            bad.append("healthy-finish-on-abort")
    computed_synth = compute_synthesized_healthy(messages)
    assertions = doc.get("assertions") or {}
    mismatch(assertions.get("synthesizedHealthyCompleted"), computed_synth, "synthesizedHealthyCompleted", bad)
    if computed_synth:
        bad.append("synthesized-healthy")
    status_map = (doc.get("reload") or {}).get("status")
    if isinstance(status_map, dict) and sid in status_map:
        st = status_map.get(sid)
        if isinstance(st, dict) and st.get("type") not in (None, "idle"):
            bad.append("status-map-still-active")
    return ("captured" if not bad else "partial"), bad


def classify_a10(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    w1_roots, children, w2_roots = [], [], []
    for h in created:
        resp = h.get("response") if isinstance(h.get("response"), dict) else {}
        body = h.get("body") if isinstance(h.get("body"), dict) else {}
        directory = str(resp.get("directory") or "")
        if body.get("parentID"):
            children.append(resp)
        elif "workspace2" in directory:
            w2_roots.append(resp)
        else:
            w1_roots.append(resp)
    if len(w1_roots) < 4:
        bad.append(f"root-count={len(w1_roots)}")
    if len(children) != 1:
        bad.append("child-count")
    if len(w2_roots) != 1:
        bad.append("dir2-count")
    patches = [h for h in http_rows(doc) if h.get("method") == "PATCH"]
    if not patches:
        bad.append("missing-archive-patch")
        archived_id = None
    else:
        archived_id = str(patches[0].get("path") or "").rsplit("/", 1)[-1]
        if archived_id not in {r.get("id") for r in w1_roots}:
            bad.append("archived-not-a-root")
    child_id = children[0].get("id") if children else None
    w1_root_ids = [r.get("id") for r in w1_roots]
    w2_id = w2_roots[0].get("id") if w2_roots else None

    def lists_with(limit: str) -> list[list[str]]:
        found = []
        for h in http_rows(doc):
            if h.get("method") != "GET" or h.get("path") != "/session":
                continue
            q = h.get("query") or {}
            if str(q.get("roots")) == "true" and str(q.get("limit")) == str(limit):
                found.append(ids_of(h.get("response")))
        return found

    lim1 = lists_with("1")
    lim2 = lists_with("2")
    lim3 = lists_with("3")
    lim10 = lists_with("10")
    if not lim1 or len(lim1[0]) != 1:
        bad.append("limit1")
    if not lim2 or len(lim2[0]) != 2:
        bad.append("limit2")
    if not lim3 or len(lim3[0]) != 3:
        bad.append("limit3")
    w1_lim10 = next((lst for lst in lim10 if set(w1_root_ids) <= set(lst)), None)
    w2_lim10 = next((lst for lst in lim10 if w2_id in lst), None)
    if w1_lim10 is None:
        bad.append("limit10-missing-roots")
    else:
        extra = [i for i in w1_lim10 if i not in w1_root_ids]
        if extra:
            bad.append(f"limit10-extras={extra}")
        if child_id in w1_lim10:
            bad.append("child-in-roots")
        if set(w1_lim10) != set(w1_root_ids):
            bad.append("limit10-not-exact-roots")
    children_http = [
        h
        for h in http_rows(doc)
        if h.get("method") == "GET" and str(h.get("path") or "").endswith("/children")
    ]
    child_ids = ids_of(children_http[0].get("response")) if children_http else []
    if child_id not in child_ids:
        bad.append("children-endpoint")
    by_id = [
        h
        for h in http_rows(doc)
        if h.get("method") == "GET" and archived_id and str(h.get("path") or "").endswith("/" + archived_id)
    ]
    if not by_id or by_id[0].get("status") != 200:
        bad.append("archived-by-id")
    else:
        time_obj = (by_id[0].get("response") or {}).get("time") if isinstance(by_id[0].get("response"), dict) else None
        if not (isinstance(time_obj, dict) and "archived" in time_obj and time_obj.get("archived") is not None):
            bad.append("archived-timestamp")
    if w1_lim10 is not None and archived_id and archived_id not in w1_lim10:
        bad.append("archived-missing-from-list")
    if w2_lim10 is None:
        bad.append("dir2-list-missing")
    elif w2_lim10 != [w2_id]:
        bad.append(f"dir2-not-isolated={w2_lim10}")
    baseline = (doc.get("reload") or {}).get("baseline")
    if isinstance(baseline, dict):
        if ids_of(baseline.get("workspace1")) or ids_of(baseline.get("workspace2")):
            bad.append("baseline-not-empty")
    else:
        bad.append("missing-baseline")
    assertions = doc.get("assertions") or {}
    mismatch(assertions.get("directoriesIsolated"), w2_lim10 == [w2_id] if w2_lim10 is not None else False, "directoriesIsolated", bad)
    return ("captured" if not bad else "partial"), bad


def same_session(p: dict, sid: str | None) -> bool:
    if not sid:
        return False
    return session_of(p) == sid


def a3_event_seq(frames: list, sid: str) -> list[str]:
    """Target-session direct events only. Title/other sessions cannot contribute retries."""
    seq = []
    for p in direct_payloads(frames):
        if not same_session(p, sid):
            continue
        if p.get("type") == "session.status":
            props = p.get("properties") if isinstance(p.get("properties"), dict) else {}
            if props.get("sessionID") != sid:
                continue
            st = props.get("status")
            typ = st.get("type") if isinstance(st, dict) else st
            if typ:
                seq.append(str(typ))
        elif p.get("type") == "session.error":
            seq.append("session.error")
        elif p.get("type") == "session.idle":
            seq.append("session.idle")
    return seq


def a3_terminal_error(frames: list, sid: str) -> dict | None:
    last = None
    for p in direct_payloads(frames):
        if p.get("type") != "session.error" or not same_session(p, sid):
            continue
        last = (p.get("properties") or {}).get("error")
    return last if isinstance(last, dict) else None


def classify_a3(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    prompts = prompt_asyncs(doc)
    if not created or not prompts:
        bad.append("missing-http")
        return "partial", bad
    sid = (created[0].get("response") or {}).get("id")
    if not sid or sid not in str(prompts[0].get("path") or ""):
        bad.append("prompt-session-mismatch")
        return "partial", bad
    seq = a3_event_seq(doc.get("sse") or [], sid)
    busy_at = next((i for i, t in enumerate(seq) if t == "busy"), -1)
    retry_idx = [i for i, t in enumerate(seq) if t == "retry"]
    err_at = next((i for i, t in enumerate(seq) if t == "session.error"), -1)
    idle_at = next((i for i, t in enumerate(seq) if t in ("idle", "session.idle")), -1)
    if busy_at == -1:
        bad.append("missing-busy")
    if len(retry_idx) < 2:
        bad.append("retry-count=" + str(len(retry_idx)))
    if err_at == -1:
        bad.append("missing-session-error")
    if idle_at == -1:
        bad.append("missing-idle")
    if retry_idx and busy_at != -1 and retry_idx[0] < busy_at:
        bad.append("retry-before-busy")
    if err_at != -1 and retry_idx and err_at < retry_idx[-1]:
        bad.append("error-before-retries")
    if idle_at != -1 and err_at != -1 and idle_at < err_at:
        bad.append("idle-before-error")
    err = a3_terminal_error(doc.get("sse") or [], sid) or {}
    data = err.get("data") if isinstance(err.get("data"), dict) else {}
    if err:
        if err.get("name") != "APIError":
            bad.append("error-name")
        if data.get("statusCode") != 400:
            bad.append("error-statusCode")
        if data.get("isRetryable") is not False:
            bad.append("error-retryable")
    messages = reload_messages(doc)
    assistants = [m for m in messages if msg_role(m) == "assistant"]
    if not assistants:
        bad.append("no-assistant")
    else:
        rerr = assistant_error(assistants[0]) or {}
        rdata = rerr.get("data") if isinstance(rerr.get("data"), dict) else {}
        if rerr.get("name") != "APIError" or rdata.get("statusCode") != 400 or rdata.get("isRetryable") is not False:
            bad.append("reload-error-mismatch")
        if assistant_finish(assistants[0]) in ("stop", "completed"):
            bad.append("healthy-finish")
    status_map = (doc.get("reload") or {}).get("status")
    if isinstance(status_map, dict) and sid in status_map:
        bad.append("status-map-still-present")
    return ("captured" if not bad else "partial"), bad


A5_EXPECTED_TEXT = "A5_PARTIAL_" + ("ABCDEFGHIJ" * 8)


def _disconnect_busy(snap: Any, sid: str | None) -> bool:
    if not isinstance(snap, dict) or not sid:
        return False
    st = snap.get(sid)
    if isinstance(st, dict):
        return st.get("type") == "busy"
    return False


def a5_status_seq(frames: list, sid: str) -> list[str]:
    seq = []
    for p in direct_payloads(frames):
        if not same_session(p, sid):
            continue
        if p.get("type") == "session.status":
            props = p.get("properties") if isinstance(p.get("properties"), dict) else {}
            st = props.get("status")
            typ = st.get("type") if isinstance(st, dict) else st
            if typ:
                seq.append(str(typ))
        elif p.get("type") == "session.idle":
            seq.append("session.idle")
    return seq


def classify_a5(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    prompts = prompt_asyncs(doc)
    if not created or not prompts:
        bad.append("missing-http")
        return "partial", bad
    sid = (created[0].get("response") or {}).get("id")
    if not sid or sid not in str(prompts[0].get("path") or ""):
        bad.append("prompt-session-mismatch")
        return "partial", bad
    before = doc.get("sseBefore") or []
    after = doc.get("sseAfterReconnect") or []
    if not before:
        bad.append("missing-sse-before")
    if not after:
        bad.append("missing-sse-after")
    seq_before = a5_status_seq(before, sid)
    if "busy" not in seq_before:
        bad.append("disconnect-without-busy")
    delta_before = any(
        p.get("type") == "message.part.delta" and same_session(p, sid) for p in direct_payloads(before)
    )
    if not delta_before:
        bad.append("disconnect-without-partial")
    snap = (doc.get("reload") or {}).get("statusAtDisconnect")
    if not _disconnect_busy(snap, sid):
        bad.append("status-at-disconnect-not-busy")
    after_direct = [p for p in direct_payloads(after)]
    if not after_direct or after_direct[0].get("type") != "server.connected":
        bad.append("reconnect-first-not-connected")
    live_delta = False
    seen_connected = False
    created_after = False
    for p in after_direct:
        if p.get("type") == "server.connected":
            seen_connected = True
            continue
        if p.get("type") == "session.created":
            created_after = True
        if seen_connected and p.get("type") == "message.part.delta" and same_session(p, sid):
            live_delta = True
    if not live_delta:
        bad.append("reconnect-without-live-delta")
    if created_after and not live_delta:
        bad.append("session-created-is-not-replay")
    after_seq = a5_status_seq(after, sid)
    if "idle" not in after_seq and "session.idle" not in after_seq:
        bad.append("second-sse-missing-terminal")
    messages = reload_messages(doc)
    roles = [msg_role(m) for m in messages]
    if roles != ["user", "assistant"]:
        bad.append(f"roles={roles}")
    assistants = [m for m in messages if msg_role(m) == "assistant"]
    if not assistants:
        bad.append("no-assistant")
    else:
        if assistant_finish(assistants[0]) != "stop":
            bad.append("assistant-finish")
        texts = []
        for part in assistants[0].get("parts") or []:
            if isinstance(part, dict) and part.get("type") == "text":
                texts.append(part.get("text") or "")
        joined = "".join(texts)
        if joined != A5_EXPECTED_TEXT:
            bad.append("assistant-text-incomplete")
    status_map = (doc.get("reload") or {}).get("status")
    if isinstance(status_map, dict) and sid in status_map:
        bad.append("status-map-still-present")
    return ("captured" if not bad else "partial"), bad


def http_messages_for(doc: dict, sid: str) -> list[dict]:
    suffix = f"/session/{sid}/message"
    rows = [
        h
        for h in http_rows(doc)
        if h.get("method") == "GET" and str(h.get("path") or "") == suffix and h.get("status") == 200
    ]
    if not rows:
        return []
    resp = rows[-1].get("response")
    return resp if isinstance(resp, list) else []


def http_status_map(doc: dict) -> dict:
    rows = [
        h
        for h in http_rows(doc)
        if h.get("method") == "GET" and str(h.get("path") or "") == "/session/status" and h.get("status") == 200
    ]
    if not rows:
        return {}
    resp = rows[-1].get("response")
    return resp if isinstance(resp, dict) else {}


def asked_props(p: dict) -> dict:
    props = p.get("properties") if isinstance(p.get("properties"), dict) else {}
    return props or p


def sse_of_type(frames: list, typ: str, sid: str | None = None) -> list[dict]:
    out = []
    for p in direct_payloads(frames):
        if p.get("type") != typ:
            continue
        if sid is not None and not same_session(p, sid) and asked_props(p).get("sessionID") != sid:
            continue
        out.append(p)
    return out


def pending_for_sid(resp: Any, sid: str) -> list[dict]:
    if not isinstance(resp, list):
        return []
    return [p for p in resp if isinstance(p, dict) and p.get("sessionID") == sid]


def classify_a6(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    frames = doc.get("sse") or []
    asked_all = sse_of_type(frames, "permission.asked")
    if not asked_all:
        return "blocked", bad + ["no-permission-asked"]
    created = creates(doc)
    if len(created) < 3:
        bad.append("need-three-sessions")
        return "partial", bad
    http = http_rows(doc)
    replies = [
        h
        for h in http
        if h.get("method") == "POST"
        and "/session/" in str(h.get("path") or "")
        and "/permissions/" in str(h.get("path") or "")
    ]
    by_response: dict[str, dict] = {}
    for h in replies:
        body = h.get("body") if isinstance(h.get("body"), dict) else {}
        resp = body.get("response")
        if resp in ("once", "always", "reject") and resp not in by_response:
            by_response[resp] = h
    for needed in ("once", "always", "reject"):
        if needed not in by_response:
            bad.append(f"missing-reply-{needed}")

    def slice_for(reply: dict | None) -> tuple[str | None, list[dict], list[dict]]:
        if not reply:
            return None, [], []
        path = str(reply.get("path") or "")
        sid = None
        parts = path.split("/")
        if len(parts) >= 3 and parts[1] == "session":
            sid = parts[2]
        idx = http.index(reply) if reply in http else -1
        before = http[:idx] if idx >= 0 else []
        after = http[idx:] if idx >= 0 else []
        return sid, before, after

    def check_flow(kind: str) -> None:
        reply = by_response.get(kind)
        sid, before, after = slice_for(reply)
        if not sid:
            return
        prompt = next(
            (
                h
                for h in reversed(before)
                if h.get("method") == "POST" and str(h.get("path") or "") == f"/session/{sid}/prompt_async"
            ),
            None,
        )
        if not prompt or prompt.get("status") not in (200, 204):
            bad.append(f"{kind}-missing-prompt")
        asked = sse_of_type(frames, "permission.asked", sid)
        if not asked:
            bad.append(f"{kind}-missing-asked")
            return
        req = asked_props(asked[0])
        for key in ("id", "sessionID", "permission"):
            if key not in req:
                bad.append(f"{kind}-asked-missing-{key}")
        pending_gets = [h for h in before if h.get("method") == "GET" and h.get("path") == "/permission"]
        if not pending_gets:
            bad.append(f"{kind}-missing-pending-get")
        else:
            pending = pending_for_sid(pending_gets[-1].get("response"), sid)
            if not pending:
                bad.append(f"{kind}-pending-empty")
            elif req.get("id") and pending[0].get("id") != req.get("id"):
                bad.append(f"{kind}-pending-id-mismatch")
        if reply and reply.get("status") not in (200, 204):
            bad.append(f"{kind}-reply-http")
        replied = sse_of_type(frames, "permission.replied", sid)
        if not replied:
            bad.append(f"{kind}-missing-replied")
        after_gets = [h for h in after[1:] if h.get("method") == "GET" and h.get("path") == "/permission"]
        if after_gets:
            leftover = pending_for_sid(after_gets[0].get("response"), sid)
            if leftover:
                bad.append(f"{kind}-pending-not-cleared")
        else:
            bad.append(f"{kind}-missing-pending-after")
        seq = a5_status_seq(frames, sid)
        if "idle" not in seq and "session.idle" not in seq:
            bad.append(f"{kind}-missing-idle")
        msgs = http_messages_for(doc, sid)
        assistants = [m for m in msgs if msg_role(m) == "assistant"]
        if kind == "reject":
            for m in assistants:
                if assistant_finish(m) in ("stop", "completed") and not assistant_error(m):
                    bad.append("reject-healthy-finish")
        elif kind == "once":
            if not any(assistant_finish(m) == "stop" for m in assistants):
                bad.append("once-did-not-continue")
        elif kind == "always":
            prompts_for = [
                h
                for h in http
                if h.get("method") == "POST" and str(h.get("path") or "") == f"/session/{sid}/prompt_async"
            ]
            if len(prompts_for) < 2:
                bad.append("always-missing-followup")
            # Live result: asked-again is observed when this session has >1 permission.asked.
            # Do not fail closed either way; both outcomes are valid captured facts.

    for kind in ("once", "always", "reject"):
        check_flow(kind)
    return ("captured" if not bad else "partial"), bad


def classify_a7(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    frames = doc.get("sse") or []
    asked_all = sse_of_type(frames, "question.asked")
    if not asked_all:
        return "blocked", bad + ["no-question-asked"]
    if any(p.get("type") in ("question_resolved", "question.resolved") for p in direct_payloads(frames)):
        bad.append("invented-question-resolved")
    for p in asked_all:
        req = asked_props(p)
        if "questions" not in req:
            bad.append("asked-missing-questions")
        if req.get("permission") and "questions" not in req:
            bad.append("permission-mapped-as-question")
    created = creates(doc)
    if len(created) < 2:
        bad.append("need-two-sessions")
        return "partial", bad
    http = http_rows(doc)
    replies = [h for h in http if h.get("method") == "POST" and str(h.get("path") or "").endswith("/reply")]
    rejects = [h for h in http if h.get("method") == "POST" and str(h.get("path") or "").endswith("/reject")]
    if not replies:
        bad.append("missing-reply")
        return "partial", bad
    if not rejects:
        bad.append("missing-reject")

    def sid_from_create(row: dict) -> str | None:
        resp = row.get("response") if isinstance(row.get("response"), dict) else {}
        return resp.get("id")

    reply_sid = sid_from_create(created[0])
    reject_sid = sid_from_create(created[1]) if len(created) > 1 else None

    def check_pending(sid: str, before_action: list[dict], after_action: list[dict], label: str) -> dict | None:
        gets = [h for h in before_action if h.get("method") == "GET" and h.get("path") == "/question"]
        if not gets:
            bad.append(f"{label}-missing-pending-get")
            return None
        pending = pending_for_sid(gets[-1].get("response"), sid)
        if not pending:
            bad.append(f"{label}-pending-empty")
            return None
        after_gets = [h for h in after_action if h.get("method") == "GET" and h.get("path") == "/question"]
        if not after_gets:
            bad.append(f"{label}-missing-pending-after")
        else:
            leftover = pending_for_sid(after_gets[0].get("response"), sid)
            if leftover:
                bad.append(f"{label}-pending-not-cleared")
        return pending[0]

    reply = replies[0]
    reply_idx = http.index(reply)
    if not reply_sid:
        bad.append("reply-missing-sid")
    else:
        asked = sse_of_type(frames, "question.asked", reply_sid)
        if not asked:
            bad.append("reply-missing-asked")
        req = check_pending(reply_sid, http[:reply_idx], http[reply_idx + 1 :], "reply")
        answers = (reply.get("body") or {}).get("answers") if isinstance(reply.get("body"), dict) else None
        if not (isinstance(answers, list) and answers and all(isinstance(row, list) for row in answers)):
            bad.append("answers-not-string-array-array")
        elif req:
            questions = req.get("questions") if isinstance(req.get("questions"), list) else []
            if len(answers) != len(questions):
                bad.append("answers-question-order")
            elif questions:
                q0 = questions[0] if isinstance(questions[0], dict) else {}
                options = q0.get("options") if isinstance(q0.get("options"), list) else []
                labels = [o.get("label") for o in options if isinstance(o, dict)]
                if answers[0] and answers[0][0] not in labels:
                    bad.append("answer-not-in-options")
        if reply.get("status") not in (200, 204):
            bad.append("reply-http")
        if not sse_of_type(frames, "question.replied", reply_sid):
            bad.append("missing-replied-event")
        seq = a5_status_seq(frames, reply_sid)
        if "idle" not in seq and "session.idle" not in seq:
            bad.append("reply-missing-idle")
        msgs = http_messages_for(doc, reply_sid)
        if not any(assistant_finish(m) == "stop" for m in msgs if msg_role(m) == "assistant"):
            bad.append("reply-did-not-continue")

    if rejects and reject_sid:
        reject = rejects[0]
        reject_idx = http.index(reject)
        asked = sse_of_type(frames, "question.asked", reject_sid)
        if not asked:
            bad.append("reject-missing-asked")
        check_pending(reject_sid, http[:reject_idx], http[reject_idx + 1 :], "reject")
        if reject.get("status") not in (200, 204):
            bad.append("reject-http")
        if not sse_of_type(frames, "question.rejected", reject_sid):
            bad.append("missing-rejected-event")
        seq = a5_status_seq(frames, reject_sid)
        if "idle" not in seq and "session.idle" not in seq:
            bad.append("reject-missing-idle")
        msgs = http_messages_for(doc, reject_sid)
        for m in msgs:
            if msg_role(m) == "assistant" and assistant_finish(m) in ("stop", "completed") and not assistant_error(m):
                bad.append("reject-healthy-finish")
    return ("captured" if not bad else "partial"), bad


def _todo_items(value: Any) -> list[dict]:
    if isinstance(value, list):
        return [i for i in value if isinstance(i, dict)]
    return []


def classify_a8(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    prompts = prompt_asyncs(doc)
    if not created or not prompts:
        bad.append("missing-http")
        return "partial", bad
    sid = (created[0].get("response") or {}).get("id")
    frames = doc.get("sse") or []
    updated = sse_of_type(frames, "todo.updated", sid)
    if not updated:
        return "blocked", bad + ["no-todo-updated"]
    todos_http = [
        h
        for h in http_rows(doc)
        if h.get("method") == "GET" and str(h.get("path") or "") == f"/session/{sid}/todo"
    ]
    if len(todos_http) < 2:
        bad.append("need-two-todo-gets")
    snapshots = [_todo_items(h.get("response")) for h in todos_http]
    event_snaps = [_todo_items((p.get("properties") or {}).get("todos")) for p in updated]
    if snapshots:
        first = snapshots[0]
        if not first:
            bad.append("empty-todo-list")
        matching_event = any(items == first for items in event_snaps)
        if first and not matching_event:
            bad.append("todo-get-event-mismatch")
        keys: set[str] = set()
        for snap in snapshots + event_snaps:
            for item in snap:
                keys.update(item.keys())
                if "id" in item:
                    bad.append("invented-id-present")
        extra = sorted(k for k in keys if k not in ("content", "status", "priority"))
        if extra:
            bad.append(f"unexpected-todo-keys={extra}")
        if len(snapshots) >= 2 and snapshots[0] == snapshots[-1]:
            bad.append("todo-no-replacement-update")
        elif len(event_snaps) >= 2 and event_snaps[0] == event_snaps[-1] and (
            len(snapshots) < 2 or snapshots[0] == snapshots[-1]
        ):
            bad.append("todo-no-replacement-update")
    seq = a5_status_seq(frames, sid or "")
    if "idle" not in seq and "session.idle" not in seq:
        bad.append("missing-idle")
    return ("captured" if not bad else "partial"), bad


def _part_kind(body: dict) -> str | None:
    parts = body.get("parts") if isinstance(body.get("parts"), list) else []
    typed = [p for p in parts if isinstance(p, dict)]
    has_agent = any(p.get("type") == "agent" for p in typed)
    files = [p for p in typed if p.get("type") == "file"]
    if has_agent:
        return "agent"
    if files:
        f0 = files[0]
        mime = str(f0.get("mime") or "")
        if f0.get("source"):
            return "fileMention"
        if mime.startswith("image/"):
            return "image"
        return "file"
    if any(p.get("type") == "text" for p in typed):
        return "text"
    return None


def classify_a9(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    prompts = prompt_asyncs(doc)
    if not created or not prompts:
        bad.append("missing-http")
        return "partial", bad
    frames = doc.get("sse") or []
    sub: dict[str, str] = {}
    wanted = ("text", "file", "fileMention", "image", "agent")
    found: dict[str, dict] = {}
    for prompt in prompts:
        body = prompt.get("body") if isinstance(prompt.get("body"), dict) else {}
        kind = _part_kind(body)
        if kind and kind not in found:
            found[kind] = prompt
    reload = doc.get("reload") or {}
    for name in wanted:
        prompt = found.get(name)
        if not prompt:
            sub[name] = "missing"
            bad.append(f"{name}-missing")
            continue
        path = str(prompt.get("path") or "")
        sid = path.split("/")[2] if path.startswith("/session/") else None
        accepted = prompt.get("status") in (200, 204)
        msgs = http_messages_for(doc, sid) if sid else []
        user = next((m for m in msgs if msg_role(m) == "user"), None)
        persisted = user.get("parts") if isinstance(user, dict) else []
        types = [p.get("type") for p in persisted if isinstance(p, dict)]
        want_type = {"text": "text", "file": "file", "fileMention": "file", "image": "file", "agent": "agent"}[name]
        source_ok = True
        if name == "fileMention":
            source_ok = any(isinstance(p, dict) and p.get("type") == "file" and p.get("source") for p in persisted)
        if name == "agent":
            source_ok = any(isinstance(p, dict) and p.get("type") == "agent" and p.get("source") for p in persisted)
        idle_ok = False
        if sid:
            seq = a5_status_seq(frames, sid)
            idle_ok = "idle" in seq or "session.idle" in seq
        mock_obs = []
        item = reload.get(name) if isinstance(reload.get(name), dict) else {}
        if isinstance(item.get("mockObservations"), list):
            mock_obs = item.get("mockObservations")
        mock_ok = True
        if not mock_obs:
            mock_ok = False
            bad.append(f"{name}-mock-observation-missing")
        if not accepted:
            sub[name] = "blocked"
            bad.append(f"{name}-rejected")
            continue
        if want_type not in types or not source_ok:
            sub[name] = "partial"
            bad.append(f"{name}-not-persisted")
            continue
        if not idle_ok:
            sub[name] = "partial"
            bad.append(f"{name}-missing-idle")
            continue
        if not mock_ok:
            sub[name] = "partial"
            continue
        sub[name] = "captured"
    for name, part_status in sub.items():
        if part_status != "captured":
            bad.append(f"part:{name}={part_status}")
    captured_n = sum(1 for v in sub.values() if v == "captured")
    blocked_n = sum(1 for v in sub.values() if v == "blocked")
    real_bad = [x for x in bad if not x.startswith("part:")]
    if captured_n == len(wanted) and not real_bad:
        status = "captured"
    elif blocked_n == len(wanted) and captured_n == 0:
        status = "blocked"
    else:
        status = "partial"
        if captured_n and blocked_n:
            bad.append("mixed-part-results")
    return status, bad


CLASSIFIERS = {
    "A1": classify_a1,
    "A2": classify_a2,
    "A3": classify_a3,
    "A4": classify_a4,
    "A5": classify_a5,
    "A6": classify_a6,
    "A7": classify_a7,
    "A8": classify_a8,
    "A9": classify_a9,
    "A10": classify_a10,
}


def classify(sid: str, doc: dict) -> tuple[str, list[str]]:
    fn = CLASSIFIERS.get(sid)
    if not fn:
        bad = common(doc)
        if (doc.get("meta") or {}).get("captureStatus") == "blocked":
            return "blocked", bad + ["marked-blocked"]
        return "partial", bad + ["no-scenario-classifier-yet"]
    status, bad = fn(doc)
    summary = (doc.get("meta") or {}).get("captureStatus")
    if summary and summary != status:
        bad = bad + [f"summary-mismatch:captureStatus:{summary}!={status}"]
        if status == "captured":
            status = "partial"
    return status, bad


def _mutate_prompt_id(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for h in out.get("http") or []:
        if str(h.get("path") or "").endswith("/prompt_async") and isinstance(h.get("body"), dict):
            h["body"]["messageID"] = "msg_mutated_not_real"
            break
    return out


def _strip_idle(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    kept = []
    for frame in out.get("sse") or []:
        p = payload(frame)
        if p.get("type") in ("session.idle",) or (
            p.get("type") == "session.status"
            and ((p.get("properties") or {}).get("status") or {}).get("type") == "idle"
        ):
            continue
        kept.append(frame)
    out["sse"] = kept
    return out


def _mutate_abort_error(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for m in (out.get("reload") or {}).get("messages") or []:
        info = m.get("info") if isinstance(m.get("info"), dict) else None
        if info and info.get("role") == "assistant":
            info["error"] = {"name": "SomeOtherError", "data": {"message": "nope"}}
            info["finish"] = "stop"
    return out


def _mutate_a10_child(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for h in out.get("http") or []:
        if h.get("method") == "GET" and str(h.get("path") or "").endswith("/children"):
            h["response"] = []
    return out


def _a3_drop_retry(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    dropped = False
    kept = []
    for frame in out.get("sse") or []:
        p = payload(frame)
        st = ((p.get("properties") or {}).get("status") or {})
        if not dropped and p.get("type") == "session.status" and st.get("type") == "retry":
            dropped = True
            continue
        kept.append(frame)
    out["sse"] = kept
    return out


def _a3_retryable_true(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for frame in out.get("sse") or []:
        p = payload(frame)
        if p.get("type") == "session.error":
            err = (p.get("properties") or {}).get("error") or {}
            data = err.get("data") if isinstance(err.get("data"), dict) else {}
            data["isRetryable"] = True
            err["data"] = data
            (p.get("properties") or {})["error"] = err
    for m in (out.get("reload") or {}).get("messages") or []:
        info = m.get("info") if isinstance(m.get("info"), dict) else None
        if info and info.get("role") == "assistant" and isinstance(info.get("error"), dict):
            data = info["error"].setdefault("data", {})
            if isinstance(data, dict):
                data["isRetryable"] = True
    return out


def _a3_drop_errors(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    out["sse"] = [f for f in (out.get("sse") or []) if payload(f).get("type") != "session.error"]
    for m in (out.get("reload") or {}).get("messages") or []:
        info = m.get("info") if isinstance(m.get("info"), dict) else None
        if info and info.get("role") == "assistant":
            info.pop("error", None)
    return out


def _a3_finish_stop(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for m in (out.get("reload") or {}).get("messages") or []:
        info = m.get("info") if isinstance(m.get("info"), dict) else None
        if info and info.get("role") == "assistant":
            info["finish"] = "stop"
    return out


def _a5_drop_live_delta(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    out["sseAfterReconnect"] = [
        frame
        for frame in (out.get("sseAfterReconnect") or [])
        if payload(frame).get("type") != "message.part.delta"
    ]
    return out


def _a5_drop_second_terminal(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    kept = []
    for frame in out.get("sseAfterReconnect") or []:
        p = payload(frame)
        st = ((p.get("properties") or {}).get("status") or {})
        if p.get("type") == "session.idle" or (p.get("type") == "session.status" and st.get("type") == "idle"):
            continue
        kept.append(frame)
    out["sseAfterReconnect"] = kept
    out.setdefault("reload", {})["status"] = {}
    return out


def _a5_disconnect_idle(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    snap = (out.get("reload") or {}).get("statusAtDisconnect")
    if isinstance(snap, dict):
        out["reload"]["statusAtDisconnect"] = {k: {"type": "idle"} for k in snap}
    return out


def _a5_truncate_text(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for m in (out.get("reload") or {}).get("messages") or []:
        info = m.get("info") if isinstance(m.get("info"), dict) else None
        if info and info.get("role") == "assistant":
            info["finish"] = None
            for part in m.get("parts") or []:
                if isinstance(part, dict) and part.get("type") == "text":
                    part["text"] = (part.get("text") or "")[:8]
    return out


def _a6_drop_asked(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for h in out.get("http") or []:
        body = h.get("body")
        if isinstance(body, dict) and body.get("response") == "once":
            body["response"] = "nope"
            break
    return out


def _a6_drop_replied(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    out["sse"] = [f for f in (out.get("sse") or []) if payload(f).get("type") != "permission.replied"]
    return out


def _a7_scramble_answers(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for h in out.get("http") or []:
        if str(h.get("path") or "").endswith("/reply") and isinstance(h.get("body"), dict):
            h["body"]["answers"] = "red"
            break
    return out


def _a7_drop_rejected(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    out["sse"] = [f for f in (out.get("sse") or []) if payload(f).get("type") != "question.rejected"]
    return out


def _a8_inject_id(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for h in out.get("http") or []:
        if h.get("method") == "GET" and str(h.get("path") or "").endswith("/todo") and isinstance(h.get("response"), list):
            h["response"] = [{**item, "id": "invented"} if isinstance(item, dict) else item for item in h["response"]]
    return out


def _a8_same_todos(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    first = None
    for h in out.get("http") or []:
        if h.get("method") == "GET" and str(h.get("path") or "").endswith("/todo") and isinstance(h.get("response"), list):
            if first is None:
                first = h.get("response")
            else:
                h["response"] = copy.deepcopy(first)
    for frame in out.get("sse") or []:
        p = payload(frame)
        if p.get("type") == "todo.updated" and first is not None:
            props = p.get("properties") if isinstance(p.get("properties"), dict) else {}
            props["todos"] = copy.deepcopy(first)
    return out


def _a9_drop_file_parts(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    for h in out.get("http") or []:
        if h.get("method") != "GET" or not str(h.get("path") or "").endswith("/message"):
            continue
        resp = h.get("response")
        if not isinstance(resp, list):
            continue
        for m in resp:
            if not isinstance(m, dict):
                continue
            if (m.get("info") or {}).get("role") != "user":
                continue
            m["parts"] = [p for p in (m.get("parts") or []) if not (isinstance(p, dict) and p.get("type") == "file")]
    return out


def _mutate_a10_dir(doc: dict) -> dict:
    out = copy.deepcopy(doc)
    extra = {"id": "ses_injected_other_dir", "directory": "/tmp/ocw-gate-a/workspace"}
    for h in reversed(out.get("http") or []):
        q = h.get("query") or {}
        if h.get("method") == "GET" and h.get("path") == "/session" and str(q.get("limit")) == "10":
            if isinstance(h.get("response"), list):
                h["response"] = list(h["response"]) + [extra]
                break
    return out


def self_test() -> int:
    failures = []
    cases = [
        ("A1", "a1-first-healthy-text.sanitized.json", _mutate_prompt_id, "messageID"),
        ("A1", "a1-first-healthy-text.sanitized.json", _strip_idle, "idle"),
        ("A2", "a2-follow-up.sanitized.json", _mutate_prompt_id, "messageID"),
        ("A2", "a2-follow-up.sanitized.json", _strip_idle, "idle"),
        ("A4", "a4-abort.sanitized.json", _mutate_abort_error, "abort-error"),
        ("A3", "a3-provider-error.sanitized.json", _a3_drop_retry, "drop-retry"),
        ("A3", "a3-provider-error.sanitized.json", _a3_retryable_true, "retryable-true"),
        ("A3", "a3-provider-error.sanitized.json", _a3_drop_errors, "drop-errors"),
        ("A3", "a3-provider-error.sanitized.json", _a3_finish_stop, "finish-stop"),
        ("A3", "a3-provider-error.sanitized.json", _strip_idle, "idle"),
        ("A5", "a5-sse-reconnect.sanitized.json", _a5_drop_live_delta, "drop-live-delta"),
        ("A5", "a5-sse-reconnect.sanitized.json", _a5_drop_second_terminal, "drop-second-terminal"),
        ("A5", "a5-sse-reconnect.sanitized.json", _a5_disconnect_idle, "disconnect-idle"),
        ("A5", "a5-sse-reconnect.sanitized.json", _a5_truncate_text, "truncate-text"),
        ("A10", "a10-session-listing.sanitized.json", _mutate_a10_child, "child/roots"),
        ("A10", "a10-session-listing.sanitized.json", _mutate_a10_dir, "directory-id"),
        ("A6", "a6-permission.sanitized.json", _a6_drop_asked, "drop-asked"),
        ("A6", "a6-permission.sanitized.json", _a6_drop_replied, "drop-replied"),
        ("A7", "a7-question.sanitized.json", _a7_scramble_answers, "scramble-answers"),
        ("A7", "a7-question.sanitized.json", _a7_drop_rejected, "drop-rejected"),
        ("A8", "a8-todos.sanitized.json", _a8_inject_id, "inject-id"),
        ("A8", "a8-todos.sanitized.json", _a8_same_todos, "same-todos"),
        ("A9", "a9-prompt-parts.sanitized.json", _a9_drop_file_parts, "drop-file-parts"),
    ]
    print("self-test mutations:")
    for sid, name, mut, label in cases:
        path = SAMPLES / name
        if not path.exists():
            print(f"  SKIP {sid} {label}: fixture missing")
            continue
        original = load(path)
        orig_status, _ = classify(sid, original)
        mutated = mut(original)
        new_status, problems = classify(sid, mutated)
        ok = orig_status == "captured" and new_status in ("partial", "blocked")
        if orig_status != "captured":
            ok = new_status != "captured"
        print(f"  {sid} {label}: {orig_status} -> {new_status} problems={problems[:6]} {'OK' if ok else 'FAIL'}")
        if not ok:
            failures.append(f"{sid}:{label}")
        with tempfile.TemporaryDirectory() as td:
            Path(td, name).write_text(json.dumps(mutated), encoding="utf-8")
    if failures:
        print("self-test FAIL", failures, file=sys.stderr)
        return 1
    print("self-test PASS")
    return 0


def main() -> int:
    if "--self-test" in sys.argv:
        return self_test()
    captured, partial, blocked, missing = [], [], [], []
    details = {}
    for sid, name in SCENARIOS.items():
        path = SAMPLES / name
        if not path.exists():
            missing.append(sid)
            continue
        doc = load(path)
        status, bad = classify(sid, doc)
        details[sid] = {"status": status, "file": str(path), "problems": bad}
        if status == "captured":
            captured.append(sid)
        elif status == "blocked":
            blocked.append(sid)
        else:
            partial.append(sid)
    print(
        json.dumps(
            {
                "captured": captured,
                "partial": partial,
                "blocked": blocked,
                "missing": missing,
                "details": details,
            },
            indent=2,
        )
    )
    if "--require-all" in sys.argv and (missing or partial or blocked):
        return 1
    hard = [
        sid
        for sid, info in details.items()
        if any(p in info["problems"] for p in ("secret", "host-path", "authorization"))
    ]
    return 1 if hard else 0


if __name__ == "__main__":
    raise SystemExit(main())
