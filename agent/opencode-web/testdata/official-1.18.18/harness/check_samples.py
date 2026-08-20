#!/usr/bin/env python3
"""Classify A1–A10 sanitized samples as captured / partial / blocked / missing.

File existence is not captured. Scenario-specific assertions must hold.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

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


def roles(messages) -> list[str]:
    out = []
    if not isinstance(messages, list):
        return out
    for item in messages:
        info = (item or {}).get("info") or {}
        if info.get("role"):
            out.append(info["role"])
    return out


def classify_a1(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    corr = doc.get("correlation") or {}
    msgs = (doc.get("reload") or {}).get("messages")
    r = roles(msgs)
    if r != ["user", "assistant"]:
        bad.append(f"roles={r}")
    if not corr.get("clientIdPersisted"):
        bad.append("clientIdPersisted")
    types = doc.get("sseEventTypes") or []
    if "session.idle" not in types and not any(
        isinstance(x, dict) and x.get("type") == "idle" for x in (doc.get("sseClassification") or {}).get("statusCarriers") or []
    ):
        bad.append("no-idle")
    if "sync" not in types:
        bad.append("no-nested-sync")
    return ("captured" if not bad else "partial"), bad


def classify_a2(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    corr = doc.get("correlation") or {}
    mapping = doc.get("bridgeMapping") or {}
    msgs = (doc.get("reload") or {}).get("messages")
    r = roles(msgs)
    if r != ["user", "assistant", "user", "assistant"]:
        bad.append(f"roles={r}")
    if not corr.get("sessionUnchanged"):
        bad.append("sessionChanged")
    if corr.get("firstClientMessageID") != (corr.get("persistedUserIDs") or [None, None])[0]:
        bad.append("first-id")
    if corr.get("followClientMessageID") != (corr.get("persistedUserIDs") or [None, None])[-1]:
        bad.append("follow-id")
    if corr.get("firstClientMessageID") == corr.get("followClientMessageID"):
        bad.append("reused-id")
    if not corr.get("bothTurnsIdle"):
        bad.append("missing-idle")
    if mapping.get("decision") != "stable messageID correlation only":
        bad.append("mapping")
    if "optimistic" in json.dumps(mapping).lower() and "not" not in json.dumps(mapping).lower():
        bad.append("optimistic-as-server")
    return ("captured" if not bad else "partial"), bad


def classify_a4(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    assertions = doc.get("assertions") or {}
    meta = doc.get("meta") or {}
    if not assertions.get("busyOrDeltaBeforeAbort"):
        bad.append("never-busy")
        return "blocked", bad
    if meta.get("abortHttpStatus") not in (200, 204):
        bad.append("abort-http")
    if not assertions.get("reachedIdleAfterAbort"):
        bad.append("not-idle-after-abort")
    if assertions.get("synthesizedHealthyCompleted"):
        bad.append("synthesized-healthy")
    if bad:
        return "partial", bad
    return "captured", bad


def classify_a10(doc: dict) -> tuple[str, list[str]]:
    bad = common(doc)
    assertions = doc.get("assertions") or {}
    if assertions.get("rootsLimit1Count") != 1:
        bad.append("limit1")
    if not assertions.get("archivedByIdHasTimestamp"):
        bad.append("archived-by-id")
    if assertions.get("directoriesIsolated") is not True:
        bad.append("dir-isolation")
    if assertions.get("cordcodeAggregationIsGateB") is not True:
        bad.append("premature-product-decision")
    if assertions.get("childCreateStatus") not in (200, 201):
        bad.append("child")
    return ("captured" if not bad else "partial"), bad


CLASSIFIERS = {
    "A1": classify_a1,
    "A2": classify_a2,
    "A4": classify_a4,
    "A10": classify_a10,
}


def classify(sid: str, doc: dict) -> tuple[str, list[str]]:
    fn = CLASSIFIERS.get(sid)
    if not fn:
        bad = common(doc)
        if bad:
            return "partial", bad
        if (doc.get("meta") or {}).get("captureStatus") == "blocked":
            return "blocked", ["marked-blocked"]
        if (doc.get("meta") or {}).get("failed"):
            return "partial", ["failed-meta"]
        return "partial", ["no-scenario-classifier-yet"]
    return fn(doc)


def main() -> int:
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
    print(json.dumps(
        {
            "captured": captured,
            "partial": partial,
            "blocked": blocked,
            "missing": missing,
            "details": details,
        },
        indent=2,
    ))
    if "--require-all" in sys.argv and (missing or partial or blocked):
        return 1
    # Shape/secret errors in classified-captured rows still fail.
    hard = [sid for sid, info in details.items() if any(p in info["problems"] for p in ("secret", "host-path", "authorization"))]
    return 1 if hard else 0


if __name__ == "__main__":
    raise SystemExit(main())
