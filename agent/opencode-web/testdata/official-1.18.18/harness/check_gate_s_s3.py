#!/usr/bin/env python3
"""Fail if Gate S S3 C1–C7 impact records are incomplete or bless forbidden paths."""

from __future__ import annotations

import copy
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
DOC = ROOT / "docs" / "2026-08-20-opencode-web-ssv2-impact.json"
MD = ROOT / "docs" / "2026-08-20-opencode-web-ssv2-impact.md"

C_IDS = ("C1", "C2", "C3", "C4", "C5", "C6", "C7")
EIGHT_FIELDS = (
    "truthOwner",
    "onlyWriter",
    "transactionDomain",
    "newDataPath",
    "activeWriteInventory",
    "failurePresentation",
    "antiDoubleWriteProof",
    "preSampleGate",
)
EXTRA_FIELDS = (
    "gateBSurfaces",
    "gateASamples",
    "currentPaths",
    "plannedAdd",
    "plannedModify",
    "plannedSeal",
    "plannedDelete",
    "protocolChange",
    "plannedTestsMac",
    "plannedTestsIOS",
    "outOfScope",
)
SOURCE_ONLY_SLICE = {
    "sessions.rename": "C7",
    "sessions.delete": "C7",
    "content.reasoning": "C4",
    "workspace.project": "C2",
    "configuration.providers": "C5",
    "configuration.default_model": "C5",
    "observation.external_turns": "C4",
}
OD3 = (
    "sessions.fork",
    "sessions.share",
    "sessions.unshare",
    "sessions.children",
    "turns.command",
    "turns.shell",
    "turns.summarize",
    "turns.revert",
    "turns.unrevert",
    "workspace.session_diff",
    "workspace.vcs",
    "workspace.file_list",
    "workspace.file_read",
    "workspace.file_search",
)
FORBIDDEN_ALLOW_PHRASES = (
    "raw/history fallback allowed",
    "second writer allowed",
    "inferred success allowed",
    "timeout completion allowed",
    "legacy fallback allowed",
    "direct+sync dual ingest allowed",
    "dual ingest allowed",
)


def load() -> dict:
    return json.loads(DOC.read_text(encoding="utf-8"))


def _nonempty(value) -> bool:
    if value is None:
        return False
    if isinstance(value, (list, dict)):
        return len(value) > 0
    return bool(str(value).strip())


def _blob(row: dict) -> str:
    return json.dumps(row, ensure_ascii=False)


def problems(doc: dict, md_text: str | None) -> list[str]:
    bad: list[str] = []
    meta = doc.get("meta") if isinstance(doc.get("meta"), dict) else {}
    if meta.get("s4Started") is not False:
        bad.append("s4Started-must-be-false")
    if meta.get("gateCStarted") is not False:
        bad.append("gateCStarted-must-be-false")
    if meta.get("productCodeFrozen") is not True:
        bad.append("productCodeFrozen-must-be-true")
    if meta.get("s3Started") is not True:
        bad.append("s3Started-must-be-true")

    rows = [r for r in (doc.get("s3") or []) if isinstance(r, dict)]
    ids = [r.get("id") for r in rows]
    if ids != list(C_IDS):
        bad.append(f"c-slice-ids-mismatch:{ids}")
    if len(ids) != 7:
        bad.append(f"c-slice-count:{len(ids)}")
    if len(set(ids)) != len(ids):
        bad.append("c-slice-duplicate")
    for missing in set(C_IDS) - set(ids):
        bad.append(f"c-slice-missing:{missing}")

    by_id = {r.get("id"): r for r in rows}
    for cid in C_IDS:
        row = by_id.get(cid)
        if not row:
            continue
        for field in EIGHT_FIELDS:
            if not _nonempty(row.get(field)):
                bad.append(f"{cid}.empty.{field}")
        for field in EXTRA_FIELDS:
            if not _nonempty(row.get(field)):
                bad.append(f"{cid}.empty.{field}")
        surfaces = row.get("gateBSurfaces")
        samples = row.get("gateASamples")
        if not isinstance(surfaces, list) or not surfaces:
            bad.append(f"{cid}.gateBSurfaces-not-list")
        if not isinstance(samples, list) or not samples:
            bad.append(f"{cid}.gateASamples-not-list")
        proto = str(row.get("protocolChange") or "")
        if "no protocol change" in proto.lower():
            pass
        else:
            low = proto.lower()
            if "canonical-first" not in low and "canonical first" not in low:
                bad.append(f"{cid}.protocol-missing-canonical-first")
            if "docs/protocol" not in proto:
                bad.append(f"{cid}.protocol-missing-docs-protocol")
            if "ios" not in low:
                bad.append(f"{cid}.protocol-missing-ios-mirror")

    c3 = by_id.get("C3") or {}
    c3_blob = _blob(c3)
    for needle in ("correlation-only", "presentation-only", "no iOS writer"):
        if needle not in c3_blob:
            bad.append(f"C3-missing-{needle}")
    only = str(c3.get("onlyWriter") or "").lower()
    if "ios optimistic writer" in only or "optimistic writer" in only:
        if "not" not in only and "correlation-only" not in str(c3.get("onlyWriter") or ""):
            bad.append("C3-messageID-ios-optimistic-writer")

    c4 = by_id.get("C4") or {}
    c4_blob = _blob(c4)
    for needle, label in (
        ("unique pre-Kernel normalization point", "unique-normalization"),
        ("unique Kernel ingest", "unique-kernel-ingest"),
        ("nested sync skip", "nested-sync-skip"),
        ("kernel==nil", "kernel-nil-seal"),
        ("reconnect same Kernel", "reconnect-same-kernel"),
    ):
        if needle not in c4_blob:
            bad.append(f"C4-missing-{label}")
    if "dual ingest" in c4_blob.lower() and "forbidden" not in c4_blob.lower() and "never" not in c4_blob.lower():
        bad.append("C4-dual-ingest-not-forbidden")
    for field in ("newDataPath", "activeWriteInventory", "antiDoubleWriteProof"):
        val = str(c4.get(field) or "").lower()
        if "dual ingest allowed" in val or "direct+sync dual ingest allowed" in val:
            bad.append("C4-allows-direct-sync-dual-ingest")

    c6 = by_id.get("C6") or {}
    c6_blob = _blob(c6)
    perm_ok = "messages[]" in c6_blob and "permission" in c6_blob.lower() and "Kernel" in c6_blob
    if not perm_ok:
        bad.append("C6-permission-ownership-incomplete")
    if "user_input_requested" not in c6_blob or "user_input_resolved" not in c6_blob:
        bad.append("C6-question-missing-canonical-user_input")
    if "question_resolved" in c6_blob and "not invent" not in c6_blob.lower() and "do not invent" not in c6_blob.lower():
        bad.append("C6-invents-question_resolved")
    if "control-plane" not in c6_blob.lower() and "control plane" not in c6_blob.lower():
        bad.append("C6-todo-not-control-plane")
    if "SessionProjection" not in c6_blob:
        bad.append("C6-todo-missing-not-SessionProjection")
    todo_path = str(c6.get("newDataPath") or "") + " " + str(c6.get("onlyWriter") or "")
    if "todo" in todo_path.lower() and "sessionprojection" in todo_path.lower():
        if "must not enter" not in todo_path.lower() and "not enter" not in todo_path.lower() and "not SessionProjection" not in todo_path:
            bad.append("C6-todo-placed-in-timeline")

    c7 = by_id.get("C7") or {}
    out = str(c7.get("outOfScope") or "")
    if "OD-3" not in out and "keep-mapped-future-or-unsupported" not in out:
        bad.append("C7-missing-OD-3")
    for sid in OD3:
        if sid not in out:
            bad.append(f"C7-od3-missing:{sid}")
    implemented = set(c7.get("gateBSurfaces") or [])
    for sid in OD3:
        if sid in implemented:
            bad.append(f"C7-implements-od3:{sid}")

    gates = {
        g.get("id"): g
        for g in (doc.get("sourceOnlyPreSampleGates") or [])
        if isinstance(g, dict)
    }
    for sid, slice_id in SOURCE_ONLY_SLICE.items():
        g = gates.get(sid)
        if not g:
            bad.append(f"source-only-missing:{sid}")
        else:
            if g.get("gate") != "实现前补样本":
                bad.append(f"source-only-bad-gate:{sid}")
            if g.get("cSlice") != slice_id:
                bad.append(f"source-only-wrong-slice:{sid}:{g.get('cSlice')}")
        row = by_id.get(slice_id) or {}
        pre = str(row.get("preSampleGate") or "")
        if sid not in pre or "实现前补样本" not in pre:
            bad.append(f"source-only-not-in-slice:{sid}->{slice_id}")

    blob = json.dumps(doc, ensure_ascii=False).lower()
    for phrase in FORBIDDEN_ALLOW_PHRASES:
        if phrase in blob:
            bad.append(f"forbidden-allowed-phrase:{phrase}")

    if md_text is not None:
        for cid in C_IDS:
            if f"### {cid} " not in md_text and f"## {cid} " not in md_text and f"| {cid} |" not in md_text:
                if cid not in md_text:
                    bad.append(f"markdown-missing:{cid}")
        for needle in (
            "correlation-only",
            "presentation-only",
            "no iOS writer",
            "unique pre-Kernel normalization point",
            "unique Kernel ingest",
            "nested sync skip",
            "kernel==nil",
            "reconnect same Kernel",
            "user_input_requested",
            "user_input_resolved",
            "实现前补样本",
            "s4Started=false",
            "productCodeFrozen=true",
        ):
            if needle not in md_text:
                bad.append(f"markdown-missing-needle:{needle}")
        for sid in SOURCE_ONLY_SLICE:
            if sid not in md_text:
                bad.append(f"markdown-missing-source-only:{sid}")
        if "raw/history fallback allowed" in md_text.lower():
            bad.append("markdown-allows-raw-history-fallback")
    return bad


def self_test() -> int:
    doc = load()
    md = MD.read_text(encoding="utf-8") if MD.exists() else ""
    orig = problems(doc, md)
    if orig:
        print("self-test FAIL original", orig[:16], file=sys.stderr)
        return 1
    failures: list[str] = []

    def expect(mut, label: str) -> None:
        found = problems(mut(copy.deepcopy(doc)), md)
        ok = bool(found)
        print(f"  {label}: {found[:4]} {'OK' if ok else 'FAIL'}")
        if not ok:
            failures.append(label)

    def drop_slice(d):
        d["s3"] = [r for r in d["s3"] if r.get("id") != "C4"]
        return d

    def drop_field(d):
        for r in d["s3"]:
            if r.get("id") == "C1":
                r["truthOwner"] = ""
        return d

    def c3_optimistic(d):
        for r in d["s3"]:
            if r.get("id") == "C3":
                r["onlyWriter"] = "iOS optimistic writer uses messageID to confirm the active timeline"
                r["newDataPath"] = "iOS writes messages[] from local messageID before SSE"
        return d

    def c4_dual(d):
        for r in d["s3"]:
            if r.get("id") == "C4":
                r["newDataPath"] = "direct+sync dual ingest allowed into Kernel"
        return d

    def todo_timeline(d):
        for r in d["s3"]:
            if r.get("id") == "C6":
                r["newDataPath"] = "todo events enter SessionProjection timeline as parts"
                r["onlyWriter"] = "Kernel writes todo into SessionProjection"
        return d

    def drop_source_only(d):
        d["sourceOnlyPreSampleGates"] = [
            g for g in d["sourceOnlyPreSampleGates"] if g.get("id") != "sessions.rename"
        ]
        for r in d["s3"]:
            if r.get("id") == "C7":
                r["preSampleGate"] = "sessions.delete is 实现前补样本. Archive is A10."
        return d

    expect(drop_slice, "drop-c-slice")
    expect(drop_field, "drop-eight-field")
    expect(c3_optimistic, "c3-optimistic-writer")
    expect(c4_dual, "c4-dual-ingest-allowed")
    expect(todo_timeline, "c6-todo-in-timeline")
    expect(drop_source_only, "drop-source-only-gate")
    if failures:
        print("self-test FAIL", failures, file=sys.stderr)
        return 1
    print("self-test PASS")
    return 0


def main() -> int:
    if "--self-test" in sys.argv:
        return self_test()
    if not DOC.exists() or not MD.exists():
        print("missing S3 doc", file=sys.stderr)
        return 1
    doc = load()
    md = MD.read_text(encoding="utf-8")
    bad = problems(doc, md)
    report = {
        "json": str(DOC),
        "markdown": str(MD),
        "cSlices": [r.get("id") for r in doc.get("s3") or []],
        "sourceOnlyGates": [g.get("id") for g in doc.get("sourceOnlyPreSampleGates") or []],
        "s3Started": (doc.get("meta") or {}).get("s3Started"),
        "s4Started": (doc.get("meta") or {}).get("s4Started"),
        "gateCStarted": (doc.get("meta") or {}).get("gateCStarted"),
        "productCodeFrozen": (doc.get("meta") or {}).get("productCodeFrozen"),
        "problems": bad,
    }
    print(json.dumps(report, indent=2, ensure_ascii=False))
    if bad:
        print("gate-s s3 FAIL", bad[:24], file=sys.stderr)
        return 1
    print(
        f"gate-s s3 ok: slices={len(report['cSlices'])} "
        f"source-only-gates={len(report['sourceOnlyGates'])} "
        f"s4Started={report['s4Started']} gateCStarted={report['gateCStarted']} "
        f"productCodeFrozen={report['productCodeFrozen']}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
