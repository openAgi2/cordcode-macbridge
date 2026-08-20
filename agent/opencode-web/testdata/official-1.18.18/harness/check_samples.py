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


def classify_a3(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    prompts = prompt_asyncs(doc)
    if not created or not prompts:
        bad.append("missing-http")
        return "partial", bad
    sid = (created[0].get("response") or {}).get("id")
    frames = doc.get("sse") or []
    if retry_count(frames, sid or "") < 2:
        bad.append("retry-count=" + str(retry_count(frames, sid or "")))
    if not has_busy_then_idle(frames, sid or ""):
        bad.append("no-busy-then-idle")
    err_payloads = [p for p in direct_payloads(frames) if p.get("type") == "session.error"]
    messages = reload_messages(doc)
    assistants = [m for m in messages if msg_role(m) == "assistant"]
    reload_err = assistant_error(assistants[0]) if assistants else None
    if not err_payloads and not reload_err:
        bad.append("missing-final-error")
    if not assistants:
        bad.append("no-assistant")
    return ("captured" if not bad else "partial"), bad


def classify_a5(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    created = creates(doc)
    prompts = prompt_asyncs(doc)
    if not created or not prompts:
        bad.append("missing-http")
        return "partial", bad
    sid = (created[0].get("response") or {}).get("id")
    before = doc.get("sseBefore") or []
    after = doc.get("sseAfterReconnect") or doc.get("sseAfter") or []
    if not before:
        bad.append("missing-sse-before")
    seq_before = status_seq(before, sid or "")
    if "busy" not in seq_before:
        bad.append("disconnect-without-busy")
    partial = any(
        p.get("type") in ("message.part.delta", "message.part.updated") and session_of(p) in (None, sid)
        for p in direct_payloads(before)
    )
    if not partial:
        bad.append("disconnect-without-partial")
    snap = (doc.get("reload") or {}).get("statusAtDisconnect") or (doc.get("meta") or {}).get("statusAtDisconnect")
    if isinstance(snap, dict):
        st = snap.get(sid) if sid in snap else snap
        typ = st.get("type") if isinstance(st, dict) else None
        if typ and typ != "busy":
            bad.append(f"status-at-disconnect={typ}")
    else:
        bad.append("missing-status-at-disconnect")
    after_idle = has_busy_then_idle(after, sid or "") or "idle" in status_seq(after, sid or "") or "session.idle" in status_seq(after, sid or "")
    reload_status = (doc.get("reload") or {}).get("status")
    reload_idle = isinstance(reload_status, dict) and sid not in reload_status
    if not after_idle and not reload_idle:
        bad.append("reconnect-no-terminal")
    messages = reload_messages(doc)
    if [msg_role(m) for m in messages][:1] != ["user"]:
        bad.append("reload-roles")
    return ("captured" if not bad else "partial"), bad


CLASSIFIERS = {
    "A1": classify_a1,
    "A2": classify_a2,
    "A3": classify_a3,
    "A4": classify_a4,
    "A5": classify_a5,
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
        ("A10", "a10-session-listing.sanitized.json", _mutate_a10_child, "child/roots"),
        ("A10", "a10-session-listing.sanitized.json", _mutate_a10_dir, "directory-id"),
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
        ok = orig_status == "captured" and new_status == "partial"
        # A10 current fixture may already be partial; mutation must not stay captured.
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
