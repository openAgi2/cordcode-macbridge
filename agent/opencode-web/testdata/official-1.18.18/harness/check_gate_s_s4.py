#!/usr/bin/env python3
"""Fail if the Gate S S4 acceptance/test map is incomplete, cites phantom tests,
or blesses forbidden paths (raw fallback, dual ingest, todo-in-timeline, premature exit)."""

from __future__ import annotations

import copy
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
IOS_ROOT = ROOT.parent / "cordcode-ios"
DOC = ROOT / "docs" / "2026-08-20-opencode-web-ssv2-impact.json"
MD = ROOT / "docs" / "2026-08-20-opencode-web-ssv2-impact.md"

LAYER_IDS = (
    "s4.adapter",
    "s4.kernel-live",
    "s4.kernel-hydrate",
    "s4.reconnect",
    "s4.delivery",
    "s4.ios-ownership",
    "s4.ios-application",
    "s4.interaction",
    "s4.cross-repository",
)
ROW_FIELDS = (
    "id",
    "name",
    "affectedSlices",
    "invariant",
    "invariantTags",
    "producerProof",
    "reducerFenceProof",
    "deliveryClientProof",
    "existingTests",
    "plannedTests",
    "sampleEvidence",
    "negativeAssertion",
    "exitCondition",
    "status",
    "uncoveredBoundaries",
)
REQUIRED_TAGS = {
    "C3": ("correlation-only", "presentation-only", "submit-zero-writer"),
    "C4": ("single-normalization", "single-ingest", "nested-sync-skip", "reconnect-same-Kernel", "kernel-nil-seal"),
    "C6": ("permission-raw-no-timeline", "question-canonical-user-input", "todo-control-plane-only"),
}
PRE_SAMPLE_IDS = (
    "workspace.project",
    "content.reasoning",
    "observation.external_turns",
    "configuration.providers",
    "configuration.default_model",
    "sessions.rename",
    "sessions.delete",
    "selected_variant",
)
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
FORBIDDEN_PHRASES = (
    "raw/history fallback allowed",
    "dual ingest allowed",
    "second writer allowed",
    "timeout completion allowed",
    "inferred success allowed",
)
TODO_TIMELINE_PHRASES = (
    "todo 进入 SessionProjection",
    "todo into SessionProjection",
    "todo events enter SessionProjection",
    "todo as timeline parts allowed",
)
PASSED_WORDS = ("passed", "green", "succeeded")


def load() -> dict:
    return json.loads(DOC.read_text(encoding="utf-8"))


def _nonempty(value) -> bool:
    if value is None:
        return False
    if isinstance(value, (list, dict)):
        return len(value) > 0
    return bool(str(value).strip())


def _has_vision(text: str) -> bool:
    # word-boundary so "Revision" does not trip the vision-future rule
    return re.search(r"\bvision\b", text, re.IGNORECASE) is not None


def _resolve(entry: dict) -> Path | None:
    repo = entry.get("repo")
    file = entry.get("file")
    if not file:
        return None
    if repo == "ios":
        return IOS_ROOT / file
    return ROOT / file


def problems(doc: dict, md_text: str | None) -> list[str]:
    bad: list[str] = []
    meta = doc.get("meta") if isinstance(doc.get("meta"), dict) else {}
    if meta.get("s4Started") is not True:
        bad.append("s4Started-must-be-true")
    if meta.get("s4Completed") is not True:
        bad.append("s4Completed-must-be-true")
    # gateSExited may flip true ONLY when every exit condition holds (added at Gate S exit).
    # gateCStarted must still be false; product code stays frozen in the Gate S snapshot.
    if meta.get("gateSExited") not in (True, False):
        bad.append("gateSExited-must-be-boolean")
    if meta.get("gateCStarted") is not False:
        bad.append("gateCStarted-must-be-false")
    if meta.get("productCodeFrozen") is not True:
        bad.append("productCodeFrozen-must-be-true")

    rows = [r for r in (doc.get("s4") or []) if isinstance(r, dict)]
    ids = [r.get("id") for r in rows]
    if sorted(ids) != sorted(LAYER_IDS):
        bad.append(f"layer-ids-mismatch:{sorted(ids)}")
    if len(ids) != 9:
        bad.append(f"layer-count:{len(ids)}")
    if len(set(ids)) != len(ids):
        bad.append("layer-duplicate")
    for missing in set(LAYER_IDS) - set(ids):
        bad.append(f"layer-missing:{missing}")

    # affectedSlices union must cover C1–C7
    union: set[str] = set()
    for r in rows:
        for s in r.get("affectedSlices") or []:
            union.add(s)
    for cid in ("C1", "C2", "C3", "C4", "C5", "C6", "C7"):
        if cid not in union:
            bad.append(f"affectedSlices-missing:{cid}")

    by_id = {r.get("id"): r for r in rows}
    for lid in LAYER_IDS:
        row = by_id.get(lid)
        if not row:
            continue
        for field in ROW_FIELDS:
            if not _nonempty(row.get(field)):
                bad.append(f"{lid}.empty.{field}")
        for entry in row.get("existingTests") or []:
            if entry.get("status") != "existing":
                bad.append(f"{lid}.existing-test-not-marked-existing:{entry.get('symbol')}")
            path = _resolve(entry)
            if path is None or not path.is_file():
                bad.append(f"{lid}.existing-file-missing:{entry.get('file')}")
                continue
            if str(entry.get("symbol") or "") not in path.read_text(encoding="utf-8"):
                bad.append(f"{lid}.existing-symbol-not-found:{entry.get('symbol')}")
        planned_blob = json.dumps(row.get("plannedTests") or [], ensure_ascii=False).lower()
        for entry in row.get("plannedTests") or []:
            if entry.get("status") != "planned":
                bad.append(f"{lid}.planned-test-not-marked-planned:{entry.get('symbol')}")
        for word in PASSED_WORDS:
            if word in planned_blob:
                bad.append(f"{lid}.planned-test-claims-{word}")

    # special invariants per C slice (union of tags over rows touching that slice)
    for cid, tags in REQUIRED_TAGS.items():
        collected: set[str] = set()
        for r in rows:
            if cid in (r.get("affectedSlices") or []):
                collected.update(r.get("invariantTags") or [])
        for tag in tags:
            if tag not in collected:
                bad.append(f"{cid}-missing-tag:{tag}")

    # source-only capture-before-translator
    order_rows = doc.get("s4PreSampleOrder") or []
    order_ids = [g.get("id") for g in order_rows]
    for sid in PRE_SAMPLE_IDS:
        if sid not in order_ids:
            bad.append(f"pre-sample-order-missing:{sid}")
    for g in order_rows:
        if g.get("captureFirst") is not True:
            bad.append(f"pre-sample-not-capture-first:{g.get('id')}")
        order_text = str(g.get("order") or "")
        if "capture" not in order_text.lower() or "translator" not in order_text.lower():
            bad.append(f"pre-sample-order-text-bad:{g.get('id')}")
        if order_text.lower().find("translator") < order_text.lower().find("capture"):
            bad.append(f"pre-sample-translator-before-capture:{g.get('id')}")

    # cross-repository canonical-first
    cross = by_id.get("s4.cross-repository") or {}
    inv = str(cross.get("invariant") or "")
    for needle in ("canonical-first", "docs/protocol", "Go types", "iOS"):
        if needle not in inv:
            bad.append(f"cross-repository-missing:{needle}")

    # OD-3 + vision must not own implementation acceptance
    for r in rows:
        acceptance_blob = json.dumps(
            {"existing": r.get("existingTests"), "planned": r.get("plannedTests"), "slices": r.get("affectedSlices")},
            ensure_ascii=False,
        )
        for sid in OD3:
            if sid in acceptance_blob:
                bad.append(f"{r.get('id')}.od3-in-acceptance:{sid}")
        if _has_vision(acceptance_blob):
            bad.append(f"{r.get('id')}.vision-in-acceptance")
        allowed_negative = " ".join(
            str(r.get(k) or "") for k in ("negativeAssertion", "uncoveredBoundaries")
        )
        row_blob = json.dumps(r, ensure_ascii=False)
        for sid in OD3:
            if sid in row_blob and sid not in allowed_negative:
                bad.append(f"{r.get('id')}.od3-outside-negative:{sid}")
        if _has_vision(row_blob) and not _has_vision(allowed_negative):
            bad.append(f"{r.get('id')}.vision-outside-negative")

    # forbidden blessings anywhere in s4
    s4_blob = json.dumps(doc.get("s4") or [], ensure_ascii=False)
    low = s4_blob.lower()
    for phrase in FORBIDDEN_PHRASES:
        if phrase in low:
            bad.append(f"forbidden-allowed-phrase:{phrase}")
    # todo must not enter SessionProjection outside negative fields
    for r in rows:
        positive = " ".join(
            str(r.get(k) or "")
            for k in ("invariant", "producerProof", "reducerFenceProof", "deliveryClientProof", "exitCondition")
        )
        for phrase in TODO_TIMELINE_PHRASES:
            if phrase in positive:
                bad.append(f"{r.get('id')}.todo-into-timeline:{phrase}")

    if md_text is not None:
        for lid in LAYER_IDS:
            if lid not in md_text:
                bad.append(f"markdown-missing-layer:{lid}")
        for needle in (
            "canonical-first",
            "capture-before-translator",
            "submit-zero-writer",
            "nested-sync-skip",
            "reconnect-same-Kernel",
            "kernel-nil-seal",
            "vision remains future",
            "gateSExited=true",
            "gateCStarted=false",
            "productCodeFrozen=true",
        ):
            if needle not in md_text:
                bad.append(f"markdown-missing-needle:{needle}")
        for sid in PRE_SAMPLE_IDS:
            if sid not in md_text:
                bad.append(f"markdown-missing-pre-sample:{sid}")

    # Gate S exit ordering: gateSExited=true is legal only with every exit condition met.
    if meta.get("gateSExited") is True:
        layers = doc.get("s4") or []
        presample = doc.get("s4PreSampleOrder") or []
        unmet = []
        if meta.get("s4Completed") is not True:
            unmet.append("s4Completed")
        if len(layers) != 9:
            unmet.append(f"layers={len(layers)}")
        if len(presample) != 8:
            unmet.append(f"preSampleOrder={len(presample)}")
        if bad:
            unmet.append(f"other-problems={len(bad)}")
        if unmet:
            bad.append(f"gateSExited-with-unmet-conditions:{','.join(unmet)}")
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

    def drop_layer(d):
        d["s4"] = [r for r in d["s4"] if r.get("id") != "s4.reconnect"]
        return d

    def bad_symbol(d):
        d["s4"][0]["existingTests"][0]["symbol"] = "TestDoesNotExistAnywhere"
        return d

    def planned_as_passed(d):
        for r in d["s4"]:
            for t in r.get("plannedTests") or []:
                t["status"] = "passed"
                return d
        return d

    def c3_no_zero_writer(d):
        for r in d["s4"]:
            if r.get("id") == "s4.ios-application":
                r["invariantTags"] = [t for t in r["invariantTags"] if t != "submit-zero-writer"]
        return d

    def c4_raw_fallback(d):
        for r in d["s4"]:
            if r.get("id") == "s4.kernel-live":
                r["invariant"] = "raw/history fallback allowed for missing terminal"
        return d

    def todo_timeline(d):
        for r in d["s4"]:
            if r.get("id") == "s4.interaction":
                r["invariant"] = "todo 进入 SessionProjection timeline as parts"
        return d

    def drop_source_order(d):
        d["s4PreSampleOrder"] = [g for g in d["s4PreSampleOrder"] if g.get("id") != "selected_variant"]
        return d

    def no_canonical_first(d):
        for r in d["s4"]:
            if r.get("id") == "s4.cross-repository":
                r["invariant"] = "每个仓库自行加 wire field 即可"
        return d

    def exit_with_unmet_conditions(d):
        # Gate S has legitimately exited; yank a required condition while keeping
        # gateSExited=true — the checker must reject exit-with-unmet-conditions.
        d["meta"]["s4Completed"] = False
        return d

    def premature_gate_c(d):
        d["meta"]["gateCStarted"] = True
        return d

    expect(drop_layer, "drop-s4-layer")
    expect(bad_symbol, "existing-symbol-phantom")
    expect(planned_as_passed, "planned-masquerades-passed")
    expect(c3_no_zero_writer, "c3-missing-submit-zero-writer")
    expect(c4_raw_fallback, "c4-raw-history-fallback-allowed")
    expect(todo_timeline, "c6-todo-into-timeline")
    expect(drop_source_order, "drop-source-only-capture-order")
    expect(no_canonical_first, "cross-repo-missing-canonical-first")
    expect(exit_with_unmet_conditions, "gateSExited-with-unmet-conditions")
    expect(premature_gate_c, "premature-gateCStarted")
    if failures:
        print("self-test FAIL", failures, file=sys.stderr)
        return 1
    print("self-test PASS")
    return 0


def main() -> int:
    if "--self-test" in sys.argv:
        return self_test()
    if not DOC.exists() or not MD.exists():
        print("missing S4 doc", file=sys.stderr)
        return 1
    doc = load()
    md = MD.read_text(encoding="utf-8")
    bad = problems(doc, md)
    report = {
        "json": str(DOC),
        "markdown": str(MD),
        "layers": [r.get("id") for r in doc.get("s4") or []],
        "preSampleOrder": [g.get("id") for g in doc.get("s4PreSampleOrder") or []],
        "s4Started": (doc.get("meta") or {}).get("s4Started"),
        "s4Completed": (doc.get("meta") or {}).get("s4Completed"),
        "gateSExited": (doc.get("meta") or {}).get("gateSExited"),
        "gateCStarted": (doc.get("meta") or {}).get("gateCStarted"),
        "productCodeFrozen": (doc.get("meta") or {}).get("productCodeFrozen"),
        "problems": bad,
    }
    print(json.dumps(report, indent=2, ensure_ascii=False))
    if bad:
        print("gate-s s4 FAIL", bad[:24], file=sys.stderr)
        return 1
    print(
        f"gate-s s4 ok: layers={len(report['layers'])} "
        f"pre-sample-order={len(report['preSampleOrder'])} "
        f"s4Started={report['s4Started']} s4Completed={report['s4Completed']} "
        f"gateSExited={report['gateSExited']} gateCStarted={report['gateCStarted']} "
        f"productCodeFrozen={report['productCodeFrozen']}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
