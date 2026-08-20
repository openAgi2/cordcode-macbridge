#!/usr/bin/env python3
"""Fail if the Gate B capability map has implicit, missing, or illegal dispositions."""

from __future__ import annotations

import copy
import json
import re
import sys
from collections import Counter
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
MAP = ROOT / "docs" / "2026-08-20-opencode-web-gate-b-capability-map.json"
MD = ROOT / "docs" / "2026-08-20-opencode-web-gate-b-capability-map.md"

LEGAL = {"supported now", "deliberately unsupported", "not applicable", "future"}
REQUIRED_FIELDS = (
    "id",
    "group",
    "surface",
    "disposition",
    "officialUi",
    "officialServer",
    "gateASample",
    "currentCordCodePath",
    "targetProductBehavior",
    "ssv2Ownership",
    "ssv2Domain",
    "wireDescriptorImpact",
    "rationale",
    "dependencyOrGap",
)
# Plan §5 minimum surfaces, expanded where the owner brief required splits.
PLAN_REQUIRED = {
    "sessions.list",
    "sessions.get",
    "sessions.create",
    "sessions.rename",
    "sessions.archive",
    "sessions.delete",
    "sessions.children",
    "sessions.fork",
    "sessions.share",
    "sessions.unshare",
    "turns.prompt",
    "turns.prompt_async",
    "turns.command",
    "turns.shell",
    "turns.abort",
    "turns.summarize",
    "turns.revert",
    "turns.unrevert",
    "content.text",
    "content.reasoning",
    "content.tool",
    "content.file.persist",
    "content.file.vision",
    "content.image.persist",
    "content.image.vision",
    "content.file_mention",
    "content.agent_mention",
    "content.patch",
    "content.snapshot",
    "content.step_markers",
    "interaction.permission.once",
    "interaction.permission.always",
    "interaction.permission.reject",
    "interaction.question.reply",
    "interaction.question.reject",
    "interaction.todo",
    "workspace.project",
    "workspace.path",
    "workspace.file_list",
    "workspace.file_read",
    "workspace.file_search",
    "workspace.session_diff",
    "workspace.vcs",
    "configuration.providers",
    "configuration.models",
    "configuration.default_model",
    "configuration.agents",
    "configuration.variants",
    "observation.status",
    "observation.global_events",
    "observation.direct_sse",
    "observation.nested_sync",
    "observation.external_turns",
    "observation.reconnect",
    "observation.catalog_refresh",
}
EMPTY = {"", "-", "TODO", "tbd", "n/a wait", "implicit"}


def load_map(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def problems(doc: dict, *, md_text: str | None = None) -> list[str]:
    bad: list[str] = []
    meta = doc.get("meta") if isinstance(doc.get("meta"), dict) else {}
    if meta.get("supportedNowMeaning") in (None, ""):
        bad.append("meta.supportedNowMeaning")
    if meta.get("productCodeFrozen") is not True:
        bad.append("meta.productCodeFrozen")
    if meta.get("opencodeVersion") != "1.18.18":
        bad.append("meta.opencodeVersion")
    if meta.get("sourceCommit") != "2cba7e227d":
        bad.append("meta.sourceCommit")
    if meta.get("gateACommit") != "aad4b24":
        bad.append("meta.gateACommit")

    decisions = doc.get("ownerDecisions") if isinstance(doc.get("ownerDecisions"), list) else []
    decision_ids = {d.get("id") for d in decisions if isinstance(d, dict)}
    for needed in ("OD-1", "OD-2", "OD-3"):
        if needed not in decision_ids:
            bad.append(f"missing-owner-decision:{needed}")

    surfaces = doc.get("surfaces") if isinstance(doc.get("surfaces"), list) else []
    if not surfaces:
        return bad + ["no-surfaces"]

    ids: list[str] = []
    by_id: dict[str, dict] = {}
    for i, row in enumerate(surfaces):
        if not isinstance(row, dict):
            bad.append(f"row[{i}]-not-object")
            continue
        sid = row.get("id")
        if not sid:
            bad.append(f"row[{i}]-missing-id")
            continue
        ids.append(sid)
        by_id[sid] = row
        for field in REQUIRED_FIELDS:
            if field not in row:
                bad.append(f"{sid}.missing-field.{field}")
                continue
            value = row.get(field)
            if field == "dependencyOrGap":
                if value is None or not isinstance(value, str):
                    bad.append(f"{sid}.{field}-not-string")
                continue
            if field == "ownerDecisionId":
                continue
            if not isinstance(value, str) or value.strip() in EMPTY:
                bad.append(f"{sid}.empty.{field}")
        disp = row.get("disposition")
        if disp not in LEGAL:
            bad.append(f"{sid}.illegal-disposition={disp!r}")
        sample = str(row.get("gateASample") or "")
        if sample != "source-only" and not re.fullmatch(r"A\d+(,A\d+)*", sample.replace(" ", "")):
            bad.append(f"{sid}.gateASample={sample!r}")
        od = row.get("ownerDecisionId")
        if od not in (None, "") and od not in decision_ids:
            bad.append(f"{sid}.unknown-ownerDecisionId={od}")
        rationale = str(row.get("rationale") or "")
        target = str(row.get("targetProductBehavior") or "")
        blob = rationale + " " + target + " " + str(row.get("dependencyOrGap") or "")
        if disp == "supported now" and re.search(r"current code already (fully )?supports", blob, re.I):
            bad.append(f"{sid}.supported-now-claims-current-complete")

    dup = [k for k, n in Counter(ids).items() if n > 1]
    if dup:
        bad.append(f"duplicate-ids={dup}")
    missing = sorted(PLAN_REQUIRED - set(ids))
    if missing:
        bad.append("plan-surface-missing:" + ",".join(missing))

    always = by_id.get("interaction.permission.always") or {}
    always_blob = " ".join(
        str(always.get(k) or "") for k in ("rationale", "targetProductBehavior", "dependencyOrGap")
    )
    if always:
        if not re.search(r"isolated serve|same pattern|askedAgain", always_blob, re.I):
            bad.append("permission.always-missing-isolated-serve-bound")
        if re.search(r"permanent|across restart|cross-restart|跨重启", always_blob, re.I):
            bad.append("permission.always-overclaimed-permanence")

    reject = by_id.get("interaction.permission.reject") or {}
    reject_blob = " ".join(str(reject.get(k) or "") for k in ("rationale", "targetProductBehavior"))
    if reject:
        if "tool-calls" not in reject_blob:
            bad.append("permission.reject-missing-tool-calls")
        if re.search(r"healthy (stop|completed|completion)", reject_blob, re.I):
            if "not a healthy" not in reject_blob.lower() and "not healthy" not in reject_blob.lower():
                bad.append("permission.reject-claimed-healthy-complete")

    for qid in ("interaction.question.reply", "interaction.question.reject"):
        row = by_id.get(qid) or {}
        blob = " ".join(str(row.get(k) or "") for k in ("rationale", "targetProductBehavior", "ssv2Ownership", "ssv2Domain"))
        if row and "user_input" not in blob:
            bad.append(f"{qid}-missing-canonical-user_input")
        if "question_resolved" in blob.lower():
            allowed = (
                "do not invent" in blob.lower()
                or "not invent" in blob.lower()
                or "never advertise question_resolved" in blob.lower()
                or "not question_resolved" in blob.lower()
            )
            if not allowed:
                bad.append(f"{qid}-invents-question_resolved")

    todo = by_id.get("interaction.todo") or {}
    todo_blob = " ".join(str(todo.get(k) or "") for k in ("rationale", "targetProductBehavior", "ssv2Domain", "ssv2Ownership"))
    if todo:
        if "control-plane" not in todo_blob.lower() and "control plane" not in todo_blob.lower():
            bad.append("todo-not-control-plane")
        if "timeline" not in todo_blob.lower():
            bad.append("todo-missing-not-timeline")
        if re.search(r"hash id|position id|invent.*id", todo_blob, re.I) and "do not invent" not in todo_blob.lower() and "not invent" not in todo_blob.lower():
            bad.append("todo-silent-id-adaptation")
        if todo.get("disposition") == "supported now" and re.search(r"\bno id\b", todo_blob, re.I) is None:
            bad.append("todo-supported-without-no-id")

    for vid in ("content.file.vision", "content.image.vision"):
        row = by_id.get(vid) or {}
        if row.get("disposition") == "supported now":
            bad.append(f"{vid}-cannot-be-supported-now")

    nested = by_id.get("observation.nested_sync") or {}
    if nested.get("disposition") == "supported now":
        bad.append("nested_sync-cannot-be-supported-now")
    nested_blob = " ".join(str(nested.get(k) or "") for k in ("rationale", "targetProductBehavior"))
    if nested and "dual" not in nested_blob.lower() and "both" not in nested_blob.lower():
        if "never dual" not in nested_blob.lower():
            # require dual-ingest prohibition
            if "dual-ingest" not in nested_blob.lower() and "dual ingest" not in nested_blob.lower():
                bad.append("nested_sync-missing-dual-ingest-ban")

    archive = by_id.get("sessions.archive") or {}
    archive_blob = " ".join(str(archive.get(k) or "") for k in ("rationale", "targetProductBehavior"))
    if archive and "archived" not in archive_blob.lower():
        bad.append("archive-missing-visibility-rule")

    if md_text is not None:
        for sid in ids:
            if sid not in md_text:
                bad.append(f"markdown-missing:{sid}")
        if "supported now" in md_text.lower() and "does not mean current" not in md_text.lower() and "不表示当前代码" not in md_text:
            if "Gate C will implement" not in md_text and "Gate C 将完成" not in md_text:
                bad.append("markdown-missing-supported-now-disclaimer")

    return bad


def self_test() -> int:
    doc = load_map(MAP)
    orig = problems(doc, md_text=MD.read_text(encoding="utf-8") if MD.exists() else "")
    if orig:
        print("self-test FAIL: original map has problems", orig[:12], file=sys.stderr)
        return 1
    failures = []

    def expect(mut, label: str) -> None:
        mutated = mut(copy.deepcopy(doc))
        found = problems(mutated)
        ok = bool(found)
        print(f"  {label}: problems={found[:4]} {'OK' if ok else 'FAIL'}")
        if not ok:
            failures.append(label)

    def drop_list(d):
        d["surfaces"] = [s for s in d["surfaces"] if s.get("id") != "sessions.list"]
        return d

    def illegal(d):
        for s in d["surfaces"]:
            if s.get("id") == "turns.abort":
                s["disposition"] = "maybe later"
        return d

    def empty_rationale(d):
        for s in d["surfaces"]:
            if s.get("id") == "content.text":
                s["rationale"] = ""
        return d

    def vision_now(d):
        for s in d["surfaces"]:
            if s.get("id") == "content.image.vision":
                s["disposition"] = "supported now"
        return d

    def always_permanent(d):
        for s in d["surfaces"]:
            if s.get("id") == "interaction.permission.always":
                s["rationale"] = "always is a permanent cross-restart grant"
                s["targetProductBehavior"] = "permanent authorization across restart"
                s["dependencyOrGap"] = ""
        return d

    expect(drop_list, "drop-sessions.list")
    expect(illegal, "illegal-disposition")
    expect(empty_rationale, "empty-rationale")
    expect(vision_now, "vision-supported-now")
    expect(always_permanent, "always-permanent")
    if failures:
        print("self-test FAIL", failures, file=sys.stderr)
        return 1
    print("self-test PASS")
    return 0


def main() -> int:
    if "--self-test" in sys.argv:
        return self_test()
    if not MAP.exists():
        print(f"missing map: {MAP}", file=sys.stderr)
        return 1
    doc = load_map(MAP)
    md_text = MD.read_text(encoding="utf-8") if MD.exists() else ""
    if not MD.exists():
        print(f"missing markdown: {MD}", file=sys.stderr)
        return 1
    bad = problems(doc, md_text=md_text)
    counts = Counter(s.get("disposition") for s in doc.get("surfaces") or [] if isinstance(s, dict))
    report = {
        "file": str(MAP),
        "surfaceCount": len(doc.get("surfaces") or []),
        "byDisposition": dict(counts),
        "planRequired": len(PLAN_REQUIRED),
        "ownerDecisions": [d.get("id") for d in (doc.get("ownerDecisions") or []) if isinstance(d, dict)],
        "gateBExited": (doc.get("meta") or {}).get("gateBExited"),
        "problems": bad,
    }
    print(json.dumps(report, indent=2, ensure_ascii=False))
    if bad:
        print("gate-b map FAIL", bad[:20], file=sys.stderr)
        return 1
    print(
        f"gate-b map ok: {report['surfaceCount']} surfaces; "
        f"supported now={counts.get('supported now', 0)} "
        f"deliberately unsupported={counts.get('deliberately unsupported', 0)} "
        f"not applicable={counts.get('not applicable', 0)} "
        f"future={counts.get('future', 0)}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
