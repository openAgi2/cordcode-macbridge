#!/usr/bin/env python3
"""Fail if the Gate S S1/S2 impact doc is incomplete or treats forbidden paths as allowed."""

from __future__ import annotations

import copy
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
DOC = ROOT / "docs" / "2026-08-20-opencode-web-ssv2-impact.json"
MD = ROOT / "docs" / "2026-08-20-opencode-web-ssv2-impact.md"

S1_REQUIRED = (
    "s1.opencode-facts",
    "s1.kernel-timeline",
    "s1.projectionstore",
    "s1.protocol",
)
S2_REQUIRED = (
    "s2.hydrate",
    "s2.live-direct-sse",
    "s2.nested-sync",
    "s2.reconnect",
    "s2.request-mutation-catalog",
    "s2.permission",
    "s2.question",
    "s2.todo",
    "s2.projection-delivery",
    "s2.ios-projection-apply",
)
S2_FIELDS = (
    "name",
    "producer",
    "mapper",
    "kernelOrControlEntry",
    "consumer",
    "onlyWriter",
    "forbiddenBypass",
    "currentSource",
    "status",
    "requiredPath",
    "forbiddenPath",
    "failurePresentation",
)
SOURCE_ONLY = (
    "sessions.rename",
    "sessions.delete",
    "content.reasoning",
    "workspace.project",
    "configuration.providers",
    "configuration.default_model",
    "observation.external_turns",
)
LEGAL_STATUS = {"satisfied-as-owner", "satisfied-as-architecture", "gap", "pending-C"}
FORBIDDEN_ALLOW_PHRASES = (
    "raw/history fallback allowed",
    "second writer allowed",
    "inferred success allowed",
    "timeout completion allowed",
    "legacy fallback allowed",
)


def load() -> dict:
    return json.loads(DOC.read_text(encoding="utf-8"))


def problems(doc: dict, md_text: str | None) -> list[str]:
    bad: list[str] = []
    meta = doc.get("meta") if isinstance(doc.get("meta"), dict) else {}
    if meta.get("s4Started") is not False:
        bad.append("s4-must-not-be-started")
    if meta.get("gateCStarted") is not False:
        bad.append("gate-c-must-not-be-started")
    if meta.get("productCodeFrozen") is not True:
        bad.append("product-code-not-frozen")
    authorities = meta.get("authoritiesRead") if isinstance(meta.get("authoritiesRead"), list) else []
    joined = " ".join(str(a) for a in authorities)
    for needle in (
        "Session Sync v2 架构路线护栏",
        "2026-07-24-single-source-multidevice-sync-design.md",
        "2026-07-26-session-sync-v2-cold-start-kernel-restart-plan.md",
        "docs/protocol",
        "GO_BRIDGE_ARCHITECTURE.md",
        "gate-b-capability-map.json",
        "source-first-convergence-plan.md",
    ):
        if needle not in joined:
            bad.append(f"authority-missing:{needle}")

    s1 = {row.get("id"): row for row in (doc.get("s1") or []) if isinstance(row, dict)}
    for sid in S1_REQUIRED:
        row = s1.get(sid)
        if not row:
            bad.append(f"s1-missing:{sid}")
            continue
        for field in ("concern", "target", "onlyWriter", "currentSource", "currentStatus"):
            if not str(row.get(field) or "").strip():
                bad.append(f"{sid}.empty.{field}")
        if row.get("currentStatus") not in LEGAL_STATUS:
            bad.append(f"{sid}.bad-status")
        src = str(row.get("currentSource") or "")
        if ":" not in src and "/" not in src:
            bad.append(f"{sid}.currentSource-not-file-symbol")

    if "ProjectionKernel" not in str((s1.get("s1.kernel-timeline") or {}).get("onlyWriter")):
        bad.append("s1.kernel-timeline-missing-kernel")
    if "ProjectionStore" not in str((s1.get("s1.projectionstore") or {}).get("onlyWriter")):
        bad.append("s1.projectionstore-missing-store")
    if "docs/protocol" not in str((s1.get("s1.protocol") or {}).get("onlyWriter")):
        bad.append("s1.protocol-missing-canonical-pack")
    facts = s1.get("s1.opencode-facts") or {}
    if "opencode serve" not in str(facts.get("onlyWriter") or "").lower() and "opencode serve" not in str(facts.get("target") or "").lower():
        bad.append("s1.opencode-facts-missing-serve-owner")

    s2 = {row.get("id"): row for row in (doc.get("s2") or []) if isinstance(row, dict)}
    for sid in S2_REQUIRED:
        row = s2.get(sid)
        if not row:
            bad.append(f"s2-missing:{sid}")
            continue
        for field in S2_FIELDS:
            if not str(row.get(field) or "").strip():
                bad.append(f"{sid}.empty.{field}")
        if row.get("status") not in LEGAL_STATUS:
            bad.append(f"{sid}.bad-status")

    perm = s2.get("s2.permission") or {}
    perm_blob = " ".join(str(perm.get(k) or "") for k in perm)
    if "messages[]" not in perm_blob and "messages[]" not in str(perm.get("forbiddenBypass") or ""):
        bad.append("permission-missing-messages-ban")
    if "Kernel" not in str(perm.get("onlyWriter") or "") and "Kernel" not in str(perm.get("kernelOrControlEntry") or ""):
        bad.append("permission-missing-kernel-canonical")

    q = s2.get("s2.question") or {}
    q_blob = " ".join(str(q.get(k) or "") for k in q)
    if "user_input_requested" not in q_blob or "user_input_resolved" not in q_blob:
        bad.append("question-missing-canonical-user_input")
    if "question_resolved" in q_blob and "not invent" not in q_blob.lower() and "must not" not in q_blob.lower() and "Do not invent" not in q_blob:
        if "must not re-enter" not in q_blob and "not re-enter" not in q_blob:
            bad.append("question-invents-question_resolved")

    todo = s2.get("s2.todo") or {}
    todo_blob = " ".join(str(todo.get(k) or "") for k in todo)
    if "control-plane" not in todo_blob.lower() and "control plane" not in todo_blob.lower():
        bad.append("todo-not-control-plane")
    if "timeline" not in todo_blob.lower():
        bad.append("todo-missing-not-timeline")
    if "no id" not in todo_blob.lower():
        bad.append("todo-missing-no-id")

    nested = s2.get("s2.nested-sync") or {}
    nested_blob = " ".join(str(nested.get(k) or "") for k in nested)
    if "dual ingest" not in nested_blob.lower() and "both advance" not in nested_blob.lower():
        bad.append("nested-sync-missing-single-ingest")
    live = s2.get("s2.live-direct-sse") or {}
    if "direct" not in json.dumps(live).lower():
        bad.append("live-missing-direct")
    if s2.get("s2.hydrate") and s2.get("s2.live-direct-sse") and s2.get("s2.reconnect"):
        if s2["s2.hydrate"].get("id") == s2["s2.live-direct-sse"].get("id"):
            bad.append("hydrate-live-not-separated")
    else:
        bad.append("hydrate-live-reconnect-not-all-present")

    hyd = s2.get("s2.hydrate") or {}
    hyd_blob = " ".join(str(hyd.get(k) or "") for k in hyd)
    for needle in ("live seq", "EventBuffer", "mailbox"):
        if needle.lower() not in hyd_blob.lower() and needle not in hyd_blob:
            # EventBuffer mentioned in forbiddenBypass
            if needle not in str(hyd.get("forbiddenBypass") or "") and needle not in str(hyd.get("forbiddenPath") or ""):
                bad.append(f"hydrate-missing-forbidden:{needle}")

    req = s2.get("s2.request-mutation-catalog") or {}
    if "HTTP 2xx" not in json.dumps(req) and "2xx" not in json.dumps(req):
        bad.append("request-domain-missing-http-2xx-ban")

    gates = {g.get("id"): g for g in (doc.get("sourceOnlyPreSampleGates") or []) if isinstance(g, dict)}
    for sid in SOURCE_ONLY:
        g = gates.get(sid)
        if not g:
            bad.append(f"source-only-missing:{sid}")
        elif g.get("gate") != "实现前补样本":
            bad.append(f"source-only-bad-gate:{sid}")

    inventory = doc.get("activeWriterInventory") if isinstance(doc.get("activeWriterInventory"), list) else []
    if len(inventory) < 6:
        bad.append("writer-inventory-too-small")
    sealed = [w for w in inventory if isinstance(w, dict) and w.get("allowed") is False]
    if not sealed:
        bad.append("writer-inventory-missing-sealed-paths")

    blob = json.dumps(doc, ensure_ascii=False).lower()
    for phrase in FORBIDDEN_ALLOW_PHRASES:
        if phrase in blob:
            bad.append(f"forbidden-allowed-phrase:{phrase}")
    # Allowed-path sections must not bless inferred success.
    for row in (doc.get("s2") or []):
        if not isinstance(row, dict):
            continue
        required = str(row.get("requiredPath") or "").lower()
        if "inferred success" in required or "history fallback" in required:
            bad.append(f"{row.get('id')}-required-path-allows-forbidden")
        fail = str(row.get("failurePresentation") or "").lower()
        if "inferred success" in fail and "never" not in fail and "no inferred" not in fail:
            bad.append(f"{row.get('id')}-failure-allows-inferred-success")

    if md_text is not None:
        for sid in list(S1_REQUIRED) + list(S2_REQUIRED) + list(SOURCE_ONLY):
            if sid not in md_text:
                bad.append(f"markdown-missing:{sid}")
        if "实现前补样本" not in md_text:
            bad.append("markdown-missing-pre-sample-gate")
        if "user_input_requested" not in md_text or "user_input_resolved" not in md_text:
            bad.append("markdown-missing-question-canonical")
        if "ProjectionKernel" not in md_text or "ProjectionStore" not in md_text:
            bad.append("markdown-missing-s1-writers")
        if "dual ingest" not in md_text.lower():
            bad.append("markdown-missing-dual-ingest-ban")
    return bad


def self_test() -> int:
    doc = load()
    md = MD.read_text(encoding="utf-8") if MD.exists() else ""
    orig = problems(doc, md)
    if orig:
        print("self-test FAIL original", orig[:12], file=sys.stderr)
        return 1
    failures = []

    def expect(mut, label: str) -> None:
        found = problems(mut(copy.deepcopy(doc)), md)
        ok = bool(found)
        print(f"  {label}: {found[:3]} {'OK' if ok else 'FAIL'}")
        if not ok:
            failures.append(label)

    def drop_hydrate(d):
        d["s2"] = [r for r in d["s2"] if r.get("id") != "s2.hydrate"]
        return d

    def drop_kernel(d):
        d["s1"] = [r for r in d["s1"] if r.get("id") != "s1.kernel-timeline"]
        return d

    def drop_source_only(d):
        d["sourceOnlyPreSampleGates"] = [g for g in d["sourceOnlyPreSampleGates"] if g.get("id") != "sessions.rename"]
        return d

    def bless_fallback(d):
        d["s2"][0]["requiredPath"] = "use history fallback and inferred success"
        return d

    expect(drop_hydrate, "drop-hydrate")
    expect(drop_kernel, "drop-s1-kernel")
    expect(drop_source_only, "drop-source-only")
    expect(bless_fallback, "bless-history-fallback")
    if failures:
        print("self-test FAIL", failures, file=sys.stderr)
        return 1
    print("self-test PASS")
    return 0


def main() -> int:
    if "--self-test" in sys.argv:
        return self_test()
    if not DOC.exists() or not MD.exists():
        print("missing S1/S2 doc", file=sys.stderr)
        return 1
    doc = load()
    md = MD.read_text(encoding="utf-8")
    bad = problems(doc, md)
    report = {
        "json": str(DOC),
        "markdown": str(MD),
        "s1": [r.get("id") for r in doc.get("s1") or []],
        "s2": [r.get("id") for r in doc.get("s2") or []],
        "sourceOnlyGates": [g.get("id") for g in doc.get("sourceOnlyPreSampleGates") or []],
        "s3Started": (doc.get("meta") or {}).get("s3Started"),
        "problems": bad,
    }
    print(json.dumps(report, indent=2, ensure_ascii=False))
    if bad:
        print("gate-s s1/s2 FAIL", bad[:20], file=sys.stderr)
        return 1
    print(
        f"gate-s s1/s2 ok: s1={len(report['s1'])} s2={len(report['s2'])} "
        f"source-only-gates={len(report['sourceOnlyGates'])}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
