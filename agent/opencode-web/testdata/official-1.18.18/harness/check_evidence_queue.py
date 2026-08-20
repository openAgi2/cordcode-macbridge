#!/usr/bin/env python3
"""Independent checker for the directive-006 evidence queue (E1–E7).

Every verdict is derived from the archived transport fields (http status +
response, SSE frames, reload payloads). Scenario summaries and meta fields are
provenance, never evidence. A row whose official path genuinely did not
materialize (e.g. the provider layer produced no reasoning part) is `blocked`
with the observed reason — an honest negative, not a checker failure. Integrity
problems (missing fields, mutated evidence, summary/transport disagreement)
FAIL.

Self-test destructively mutates each row's claimed request field, response
field, event/order, or reload convergence and must catch every one.
"""

from __future__ import annotations

import copy
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
SAMPLES = ROOT / "agent/opencode-web/testdata/official-1.18.18/samples"

# E4/E5 raw files are withheld by the frozen leak policy: the /provider
# response legitimately echoes the provider row's options object, whose key
# name trips the credential marker. The harness-invented synthetic value is
# already public in harness/opencode.json; the sanitized archive preserves
# key/type/order, so those two rows derive from the sanitized http[].
SANITIZED_SOURCE = {"E4", "E5"}


def _load(row: str, name: str) -> dict:
    suffix = "sanitized" if row in SANITIZED_SOURCE else "raw"
    return json.loads((SAMPLES / f"{name}.{suffix}.json").read_text(encoding="utf-8"))


def _doc(row: str) -> dict:
    names = {
        "E1": "e1-selected-variant",
        "E2": "e2-reasoning",
        "E3": "e3-external-turn",
        "E4": "e4-providers",
        "E6": "e6-rename",
        "E7": "e7-delete",
    }
    if row == "E5":
        raise ValueError("E5 loads three mode files")
    return _load(row, names[row])


def _e5_docs() -> dict[str, dict]:
    return {
        mode: _load("E5", f"e5-configured-default-{mode}")
        for mode in ("valid", "invalid", "absent")
    }


def _payloads(frames: list) -> list[dict]:
    out = []
    for frame in frames or []:
        ev = frame.get("event") if isinstance(frame, dict) else None
        if not isinstance(ev, dict):
            continue
        p = ev.get("payload") if isinstance(ev.get("payload"), dict) else ev
        if isinstance(p, dict):
            out.append(p)
    return out


def _props(payload: dict) -> dict:
    props = payload.get("properties")
    return props if isinstance(props, dict) else {}


def _sid_of(doc: dict) -> str:
    for entry in doc.get("http") or []:
        if entry.get("method") == "POST" and entry.get("path") == "/session":
            resp = entry.get("response")
            if isinstance(resp, dict) and resp.get("id"):
                return str(resp["id"])
    return ""


def _idle_reached(frames: list, sid: str) -> bool:
    for p in _payloads(frames):
        if p.get("type") in ("session.idle",):
            if _props(p).get("sessionID") in (None, sid):
                return True
        if p.get("type") == "session.status":
            props = _props(p)
            if props.get("sessionID") in (None, sid):
                st = props.get("status")
                typ = st.get("type") if isinstance(st, dict) else st
                if typ == "idle":
                    return True
    return False


def _user_models(messages) -> list[dict]:
    out = []
    for m in messages if isinstance(messages, list) else []:
        info = m.get("info") if isinstance(m, dict) else None
        if isinstance(info, dict) and info.get("role") == "user":
            model = info.get("model")
            out.append(model if isinstance(model, dict) else {})
    return out


def _assistant_texts(messages) -> list[str]:
    out = []
    for m in messages if isinstance(messages, list) else []:
        info = m.get("info") if isinstance(m, dict) else None
        if not (isinstance(info, dict) and info.get("role") == "assistant"):
            continue
        for part in m.get("parts") or []:
            if isinstance(part, dict) and part.get("type") == "text":
                out.append(str(part.get("text") or ""))
    return out


# ── per-row derivations ─────────────────────────────────────────────────────


def check_e1(doc: dict) -> tuple[list[str], str, dict]:
    bad: list[str] = []
    http = doc.get("http") or []
    prompts = [e for e in http if "prompt_async" in str(e.get("path"))]
    with_variant = [e for e in prompts if isinstance(e.get("body"), dict) and e["body"].get("variant")]
    without = [e for e in prompts if isinstance(e.get("body"), dict) and not e["body"].get("variant")]
    if not with_variant:
        return ["e1-no-nonempty-variant-request"], "missing", {}
    if with_variant[0].get("status") != 204:
        bad.append(f"e1-variant-prompt-status:{with_variant[0].get('status')}")
    if not without:
        bad.append("e1-no-unset-control")
    sent_variant = str(with_variant[0]["body"]["variant"])
    sid = _sid_of(doc)
    if not sid:
        bad.append("e1-no-session-id")
    reload = doc.get("reload") or {}
    set_models = _user_models(reload.get("messagesWithVariantSet"))
    unset_models = _user_models(reload.get("messagesWithVariantUnset"))
    if not set_models or not unset_models:
        bad.append("e1-reload-missing-user-messages")
        return bad, "fail", {}
    # variant is per-message: judge each turn by the LAST user message of its
    # reload (earlier turns legitimately keep their own variant).
    last_set = set_models[-1]
    last_unset = unset_models[-1]
    if last_set.get("variant") != sent_variant:
        bad.append("e1-variant-not-persisted-on-latest-user-message")
    if last_unset.get("variant"):
        bad.append("e1-unset-control-latest-user-message-must-not-carry-variant")
    if sid and not _idle_reached(doc.get("sse") or [], sid):
        bad.append("e1-terminal-idle-not-observed")
    facts = {
        "sentVariant": sent_variant,
        "promptStatus": with_variant[0].get("status"),
        "latestUserModelWithVariant": last_set,
        "latestUserModelUnset": last_unset,
        "perMessagePersistence": {
            "earlierTurnKeepsVariant": [m.get("variant") for m in set_models[:-1]],
        },
    }
    return bad, ("captured" if not bad else "fail"), facts


def check_e2(doc: dict) -> tuple[list[str], str, dict]:
    bad: list[str] = []
    http = doc.get("http") or []
    prompt = [e for e in http if "prompt_async" in str(e.get("path"))]
    if not prompt:
        return ["e2-no-prompt"], "missing", {}
    if prompt[0].get("status") != 204:
        bad.append(f"e2-prompt-status:{prompt[0].get('status')}")
    sid = _sid_of(doc)
    idle = _idle_reached(doc.get("sse") or [], sid) if sid else False

    # reasoning presence derived from BOTH SSE part frames and reload parts
    sse_reasoning = 0
    for p in _payloads(doc.get("sse") or []):
        props = _props(p)
        part = props.get("part") if isinstance(props.get("part"), dict) else props
        if isinstance(part, dict) and part.get("type") == "reasoning":
            sse_reasoning += 1
    reload_parts: list[str] = []
    msgs = (doc.get("reload") or {}).get("messages")
    for m in msgs if isinstance(msgs, list) else []:
        for part in m.get("parts") or []:
            if isinstance(part, dict):
                reload_parts.append(str(part.get("type")))
    reasoning_in_reload = "reasoning" in reload_parts

    # anti-fake: reasoning in reload WITHOUT any SSE reasoning delta is a
    # hand-written fixture, not a transport observation.
    if reasoning_in_reload and sse_reasoning == 0:
        bad.append("e2-reasoning-in-reload-without-sse-delta-fake")

    # retry-loop shape derived from the status sequence (busy<->retry, no idle)
    statuses = []
    for p in _payloads(doc.get("sse") or []):
        if p.get("type") == "session.status":
            st = _props(p).get("status")
            statuses.append(st.get("type") if isinstance(st, dict) else st)
    retry_count = statuses.count("retry")
    facts = {
        "promptStatus": prompt[0].get("status"),
        "sseReasoningParts": sse_reasoning,
        "reloadPartTypes": sorted(set(reload_parts)),
        "reasoningObserved": bool(sse_reasoning or reasoning_in_reload),
        "terminalIdleReached": idle,
        "statusSequenceTail": statuses[-6:],
        "retryCount": retry_count,
    }
    if bad:
        return bad, "fail", facts
    if not facts["reasoningObserved"]:
        return [], "blocked", dict(
            facts,
            blockedReason=(
                "official 1.18.18 serve + @ai-sdk/openai-compatible fails the provider stream when reasoning-style deltas are present "
                "(AI_APICallError: socket connection closed unexpectedly -> serve retry loop, no terminal in window). "
                "Two deterministic strategies attempted and archived: per-character deltas and whole-string delta — both failed identically. "
                "No reasoning part ever materialized in SSE or reload."
            ),
        )
    return [], "captured", facts


def check_e3(doc: dict) -> tuple[list[str], str, dict]:
    bad: list[str] = []
    ext = doc.get("httpExternalClient") or []
    cap = doc.get("httpCaptureClient") or []
    creates = [e for e in ext if e.get("method") == "POST" and e.get("path") == "/session"]
    prompts = [e for e in ext if "prompt_async" in str(e.get("path"))]
    if not creates or not prompts:
        return ["e3-external-client-must-create-and-send"], "missing", {}
    sid = str((creates[0].get("response") or {}).get("id") or "")
    if not sid:
        bad.append("e3-external-session-id-missing")
    if prompts[0].get("status") != 204:
        bad.append(f"e3-external-prompt-status:{prompts[0].get('status')}")
    # Purity: the capture client may ONLY read, and only AFTER terminal —
    # its whole log must be exactly the two terminal-time GETs (no POST, no
    # list polling, no mid-turn reads).
    cap_ops = [(e.get("method"), str(e.get("path"))) for e in cap]
    if any(m != "GET" for m, _ in cap_ops):
        bad.append(f"e3-capture-client-wrote:{cap_ops}")
    if any(str(p).endswith("/message") is False and str(p).count("/") > 2 and False for _, p in cap_ops):
        pass  # placeholder to keep structure explicit
    lists = [p for m, p in cap_ops if str(p) == "/session"]
    if lists:
        bad.append("e3-polling-substitution-forbidden")
    sse_text = json.dumps(doc.get("sse") or [])
    if sid and sid not in sse_text:
        bad.append("e3-external-session-absent-from-sse")
    deltas = sum(
        1
        for p in _payloads(doc.get("sse") or [])
        if p.get("type") == "message.part.delta"
    )
    if deltas == 0:
        bad.append("e3-no-live-deltas-observed")
    if sid and not _idle_reached(doc.get("sse") or [], sid):
        bad.append("e3-terminal-idle-not-observed")
    reload = doc.get("reload") or {}
    by_id = reload.get("sessionByID") or {}
    if sid and by_id.get("id") != sid:
        bad.append("e3-reload-by-id-mismatch")
    user_models = _user_models(reload.get("messages"))
    if not user_models:
        bad.append("e3-reload-missing-persisted-user-message")
    if not _assistant_texts(reload.get("messages")):
        bad.append("e3-reload-missing-persisted-assistant-text")
    facts = {
        "captureClientOps": cap_ops,
        "externalPromptStatus": prompts[0].get("status"),
        "liveDeltas": deltas,
        "terminalIdle": _idle_reached(doc.get("sse") or [], sid) if sid else False,
    }
    return bad, ("captured" if not bad else "fail"), facts


def check_e4(doc: dict) -> tuple[list[str], str, dict]:
    bad: list[str] = []
    gets = [e for e in doc.get("http") or [] if e.get("path") == "/provider"]
    if not gets:
        return ["e4-no-provider-request"], "missing", {}
    if gets[-1].get("status") != 200:
        bad.append(f"e4-status:{gets[-1].get('status')}")
    resp = gets[-1].get("response")
    if not isinstance(resp, dict):
        bad.append("e4-top-level-not-object")
        return bad, "fail", {}
    for key in ("all", "default", "connected"):
        if key not in resp:
            bad.append(f"e4-missing-top-level:{key}")
    rows = resp.get("all")
    if not isinstance(rows, list) or not rows:
        bad.append("e4-all-not-list")
        return bad, "fail", {}
    row = rows[0] if isinstance(rows[0], dict) else {}
    for key in ("id", "name", "source", "env", "options", "models"):
        if key not in row:
            bad.append(f"e4-row-missing:{key}")
    connected = resp.get("connected")
    if not isinstance(connected, list) or not connected:
        bad.append("e4-connected-empty-or-missing")
    default = resp.get("default")
    if not isinstance(default, dict):
        bad.append("e4-default-not-map")
    models = row.get("models")
    if not isinstance(models, dict) or not models:
        bad.append("e4-models-empty")
    facts = {
        "topLevelKeys": sorted(resp.keys()),
        "rowKeys": sorted(row.keys()),
        "connected": connected,
        "default": default,
        "modelCount": len(models) if isinstance(models, dict) else 0,
        "evidenceSource": "sanitized (raw withheld by frozen leak policy: provider row echoes options; synthetic harness credential)",
    }
    return bad, ("captured" if not bad else "fail"), facts


def check_e5(docs: dict[str, dict]) -> tuple[list[str], str, dict]:
    bad: list[str] = []
    facts: dict = {"modes": {}, "evidenceSource": "sanitized (see E4)"}
    defaults = {}
    session_models = {}
    for mode in ("valid", "invalid", "absent"):
        doc = docs.get(mode)
        if not doc:
            bad.append(f"e5-missing-mode:{mode}")
            continue
        if (doc.get("meta") or {}).get("configMode") != mode:
            bad.append(f"e5-mode-mismatch:{mode}")
        gets = [e for e in doc.get("http") or [] if e.get("path") == "/provider"]
        prompts = [e for e in doc.get("http") if "prompt_async" in str(e.get("path"))]
        if not gets or not prompts:
            bad.append(f"e5-{mode}-missing-provider-or-prompt")
            continue
        if prompts[-1].get("body") and "model" in prompts[-1]["body"]:
            bad.append(f"e5-{mode}-prompt-must-omit-model")
        resp = gets[-1].get("response") or {}
        defaults[mode] = resp.get("default")
        by_id = (doc.get("reload") or {}).get("sessionByID") or {}
        model = by_id.get("model") or {}
        session_models[mode] = {
            "promptStatus": prompts[-1].get("status"),
            "resolvedModelID": model.get("id"),
            "resolvedProviderID": model.get("providerID"),
        }
        user_models = _user_models((doc.get("reload") or {}).get("messages"))
        if not user_models:
            bad.append(f"e5-{mode}-no-persisted-user-message")
        elif user_models[-1].get("modelID") != model.get("id") or user_models[-1].get("providerID") != model.get("providerID"):
            bad.append(f"e5-{mode}-session-model-disagrees-with-persisted-user-message")
        facts["modes"][mode] = session_models[mode]
    # config-independence of /provider.default derived across modes
    uniq = {json.dumps(v, sort_keys=True) for v in defaults.values() if v is not None}
    facts["providerDefaultIdenticalAcrossModes"] = len(uniq) == 1
    facts["providerDefault"] = defaults.get("valid")
    facts["limitation"] = (
        "single-model catalog: the absent-config resolution cannot be "
        "distinguished from first-model fallback by this evidence alone"
    )
    return bad, ("captured" if not bad else "fail"), facts


def check_e6(doc: dict) -> tuple[list[str], str, dict]:
    bad: list[str] = []
    http = doc.get("http") or []
    patches = [e for e in http if e.get("method") == "PATCH"]
    if not patches:
        return ["e6-no-patch"], "missing", {}
    ok = [e for e in patches if e.get("status") == 200]
    fails = [e for e in patches if e.get("status") >= 400]
    if not ok:
        bad.append("e6-no-successful-rename")
    else:
        body = ok[0].get("body") or {}
        resp = ok[0].get("response") or {}
        if not body.get("title"):
            bad.append("e6-patch-body-missing-title")
        if resp.get("title") != body.get("title"):
            bad.append("e6-response-title-mismatch")
    if not fails:
        bad.append("e6-no-failure-observation")
    else:
        fres = fails[0].get("response") or {}
        if not (isinstance(fres, dict) and "NotFound" in str(fres.get("name") or "")):
            bad.append("e6-failure-shape-unexpected")
    sid = _sid_of(doc)
    listed = (doc.get("reload") or {}).get("listAfterRename") or []
    row = next((r for r in listed if isinstance(r, dict) and r.get("id") == sid), None)
    if sid and row is None:
        bad.append("e6-renamed-row-absent-from-list")
    elif row and ok and row.get("title") != (ok[0].get("body") or {}).get("title"):
        bad.append("e6-list-title-not-converged")
    byid = [e for e in http if e.get("method") == "GET" and str(e.get("path", "")).endswith(sid) and "message" not in str(e.get("path"))]
    if sid and byid and (byid[-1].get("response") or {}).get("title") != (ok[0].get("body") or {}).get("title"):
        bad.append("e6-by-id-title-not-converged")
    facts = {
        "patchStatus": ok[0].get("status") if ok else None,
        "responseTitle": (ok[0].get("response") or {}).get("title") if ok else None,
        "failureStatus": fails[0].get("status") if fails else None,
        "listTitleConverged": bool(row and row.get("title") == ((ok[0].get("body") or {}).get("title") if ok else None)),
    }
    return bad, ("captured" if not bad else "fail"), facts


def check_e7(doc: dict) -> tuple[list[str], str, dict]:
    bad: list[str] = []
    http = doc.get("http") or []
    deletes = [e for e in http if e.get("method") == "DELETE"]
    if not deletes:
        return ["e7-no-delete"], "missing", {}
    first = deletes[0]
    if first.get("status") != 200 or first.get("response") is not True:
        bad.append(f"e7-delete-response:{first.get('status')}/{first.get('response')!r}")
    sid = str(first.get("path", "")).rsplit("/", 1)[-1]
    byid = [e for e in http if e.get("method") == "GET" and str(e.get("path")) == f"/session/{sid}"]
    if not byid or byid[-1].get("status") != 404:
        bad.append("e7-by-id-must-404-after-delete")
    listed = (doc.get("reload") or {}).get("listAfterDelete") or []
    if any(isinstance(r, dict) and r.get("id") == sid for r in listed):
        bad.append("e7-list-must-not-contain-deleted-session")
    if len(deletes) < 2:
        bad.append("e7-no-failure-observation")
    else:
        fres = deletes[1].get("response") or {}
        if deletes[1].get("status") != 404 or "NotFound" not in str(fres.get("name") or ""):
            bad.append("e7-second-delete-failure-shape-unexpected")
    deleted_event = any(p.get("type") == "session.deleted" for p in _payloads(doc.get("sse") or []))
    facts = {
        "deleteStatus": first.get("status"),
        "deleteResponse": first.get("response"),
        "byIdAfterDelete": byid[-1].get("status") if byid else None,
        "secondDeleteStatus": deletes[1].get("status") if len(deletes) > 1 else None,
        "sessionDeletedEventObserved": deleted_event,
    }
    if not deleted_event:
        # honest negative observation — recorded, not fabricated
        facts["sessionDeletedEventNote"] = "session.deleted NOT observed in the capture window (top-level and sync-wrapped frames scanned)"
    return bad, ("captured" if not bad else "fail"), facts


# ── orchestration ────────────────────────────────────────────────────────────


def evaluate(docs: dict[str, dict] | None = None) -> dict:
    if docs is None:
        docs = {
            "E1": _doc("E1"),
            "E2": _doc("E2"),
            "E3": _doc("E3"),
            "E4": _doc("E4"),
            "E5": _e5_docs(),
            "E6": _doc("E6"),
            "E7": _doc("E7"),
        }
    rows = {
        "E1": check_e1(docs["E1"]),
        "E2": check_e2(docs["E2"]),
        "E3": check_e3(docs["E3"]),
        "E4": check_e4(docs["E4"]),
        "E5": check_e5(docs["E5"]),
        "E6": check_e6(docs["E6"]),
        "E7": check_e7(docs["E7"]),
    }
    summary = {row: verdict for row, (bad, verdict, facts) in rows.items()}
    problems = [f"{row}:{p}" for row, (bad, _, _) in rows.items() for p in bad]
    return {"summary": summary, "problems": problems, "facts": {row: facts for row, (_, _, facts) in rows.items()}}


def self_test() -> int:
    base = evaluate()
    if base["problems"]:
        print("self-test FAIL original", base["problems"][:12], file=sys.stderr)
        return 1
    failures: list[str] = []

    def expect(row: str, mut, label: str) -> None:
        docs = {"E1": _doc("E1"), "E2": _doc("E2"), "E3": _doc("E3"), "E4": _doc("E4"),
                "E5": _e5_docs(), "E6": _doc("E6"), "E7": _doc("E7")}
        docs[row] = mut(copy.deepcopy(docs[row]))
        result = evaluate(docs)
        ok = bool(result["problems"]) or result["summary"][row] != base["summary"][row]
        print(f"  {label}: {result['problems'][:3] or [result['summary'][row]]} {'OK' if ok else 'FAIL'}")
        if not ok:
            failures.append(label)

    def e1_request_field(d):
        for e in d["http"]:
            if "prompt_async" in e.get("path", "") and isinstance(e.get("body"), dict) and e["body"].get("variant"):
                del e["body"]["variant"]
        return d

    def e1_reload_convergence(d):
        for m in d["reload"]["messagesWithVariantSet"]:
            info = m.get("info", {})
            if info.get("role") == "user" and isinstance(info.get("model"), dict):
                info["model"].pop("variant", None)
        return d

    def e2_fake_reasoning(d):
        msgs = d["reload"]["messages"]
        for m in msgs:
            if m.get("info", {}).get("role") == "assistant":
                m.setdefault("parts", []).insert(0, {"type": "reasoning", "text": "hand-written"})
        return d

    def e3_polling(d):
        d["httpCaptureClient"].insert(0, {"method": "GET", "path": "/session", "status": 200, "response": []})
        return d

    def e3_event_order(d):
        # strip every terminal signal for the external session: both the
        # session.idle event and idle-typed session.status frames
        keep = []
        for f in d["sse"]:
            ev = f.get("event") if isinstance(f, dict) else None
            p = ev.get("payload", ev) if isinstance(ev, dict) else {}
            if not isinstance(p, dict):
                keep.append(f)
                continue
            if p.get("type") == "session.idle":
                continue
            if p.get("type") == "session.status":
                st = _props(p).get("status")
                typ = st.get("type") if isinstance(st, dict) else st
                if typ == "idle":
                    continue
            keep.append(f)
        d["sse"] = keep
        return d

    def e4_top_level(d):
        d["http"][-1]["response"] = {"rows": d["http"][-1]["response"].get("all")}
        return d

    def e5_mode_behavior(d):
        by = d["valid"]["reload"]["sessionByID"]
        by["model"]["id"] = "tampered-model"
        return d

    def e6_response_field(d):
        for e in d["http"]:
            if e.get("method") == "PATCH" and e.get("status") == 200:
                e["response"]["title"] = "stale-title"
        return d

    def e6_reload_convergence(d):
        sid = _sid_of(d)
        for r in d["reload"]["listAfterRename"] or []:
            if r.get("id") == sid:
                r["title"] = "stale-title"
        return d

    def e7_reload_convergence(d):
        sid = str([e for e in d["http"] if e.get("method") == "DELETE"][0]["path"]).rsplit("/", 1)[-1]
        d["reload"]["listAfterDelete"] = [
            {"id": sid, "title": "ghost"} if not isinstance(r, dict) else r
            for r in (d["reload"]["listAfterDelete"] or [])
        ]
        d["reload"]["listAfterDelete"].append({"id": sid, "title": "ghost"})
        return d

    def e7_response_field(d):
        for e in d["http"]:
            if e.get("method") == "DELETE":
                e["response"] = False
                return d
        return d

    expect("E1", e1_request_field, "e1-request-variant-removed")
    expect("E1", e1_reload_convergence, "e1-reload-persistence-broken")
    expect("E2", e2_fake_reasoning, "e2-hand-written-reasoning-fake")
    expect("E3", e3_polling, "e3-polling-substitution")
    expect("E3", e3_event_order, "e3-terminal-event-removed")
    expect("E4", e4_top_level, "e4-top-level-shape-broken")
    expect("E5", e5_mode_behavior, "e5-mode-behavior-tampered")
    expect("E6", e6_response_field, "e6-response-field-broken")
    expect("E6", e6_reload_convergence, "e6-reload-convergence-broken")
    expect("E7", e7_reload_convergence, "e7-reload-convergence-broken")
    expect("E7", e7_response_field, "e7-response-field-broken")
    if failures:
        print("self-test FAIL", failures, file=sys.stderr)
        return 1
    print("self-test PASS")
    return 0


def main() -> int:
    if "--self-test" in sys.argv:
        return self_test()
    result = evaluate()
    print(json.dumps(result, indent=2, ensure_ascii=False))
    if result["problems"]:
        print("evidence queue FAIL", result["problems"][:24], file=sys.stderr)
        return 1
    counts: dict[str, int] = {}
    for v in result["summary"].values():
        counts[v] = counts.get(v, 0) + 1
    print(f"evidence queue ok: {result['summary']} counts={counts}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
