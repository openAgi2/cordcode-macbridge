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
REQUIRED_RESOLVED = {
    "OD-1": "hide-in-default-list-keep-by-id",
    "OD-2": "aggregate-global-list-keep-scoped-list",
    "OD-3": "keep-mapped-future-or-unsupported",
}
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
MD_ROW_RE = re.compile(r"^\| `([^`]+)` \| ([^|]+?) \| ([^|]+?) \|", re.M)


def load_map(path: Path) -> dict:
    return json.loads(path.read_text(encoding="utf-8"))


def row_decision_ids(row: dict) -> list[str]:
    v = row.get("ownerDecisionId")
    if v in (None, ""):
        return []
    if isinstance(v, list):
        return [str(x) for x in v if x]
    return [str(v)]


def parse_md_rows(md_text: str) -> dict[str, dict[str, str]]:
    out: dict[str, dict[str, str]] = {}
    for match in MD_ROW_RE.finditer(md_text):
        sid, disp, sample = (match.group(1).strip(), match.group(2).strip(), match.group(3).strip())
        if sid in {"id"} or disp == "disposition":
            continue
        out[sid] = {"disposition": disp, "gateASample": sample}
    return out


def problems(doc: dict, *, md_text: str | None = None) -> list[str]:
    bad: list[str] = []
    meta = doc.get("meta") if isinstance(doc.get("meta"), dict) else {}
    if meta.get("supportedNowMeaning") in (None, ""):
        bad.append("meta.supportedNowMeaning")
    meaning = str(meta.get("supportedNowMeaning") or "")
    extra = str(meta.get("supportedNowAndSourceOnly") or "") + " " + str(meta.get("gateSPreSampleRule") or "")
    scope_blob = meaning + " " + extra
    if "not implementation authorization" not in scope_blob.lower() and "not an implementation grant" not in scope_blob.lower():
        if "不是实施授权" not in scope_blob:
            bad.append("meta.missing-source-only-not-authorization")
    if "实现前补样本" not in str(meta.get("gateSPreSampleRule") or ""):
        bad.append("meta.missing-gateSPreSampleRule")
    if meta.get("productCodeFrozen") is not True:
        bad.append("meta.productCodeFrozen")
    if meta.get("opencodeVersion") != "1.18.18":
        bad.append("meta.opencodeVersion")
    if meta.get("sourceCommit") != "2cba7e227d":
        bad.append("meta.sourceCommit")
    if meta.get("gateACommit") != "aad4b24":
        bad.append("meta.gateACommit")

    blockers = meta.get("gateBExitBlockers")
    if meta.get("gateBExited") is True:
        if blockers not in ([], None):
            bad.append(f"exited-with-blockers={blockers}")
    elif blockers is None:
        bad.append("meta.gateBExitBlockers-missing")

    decisions = doc.get("ownerDecisions") if isinstance(doc.get("ownerDecisions"), list) else []
    decision_by_id: dict[str, dict] = {}
    for item in decisions:
        if isinstance(item, dict) and item.get("id"):
            decision_by_id[str(item.get("id"))] = item
    for needed, expected in REQUIRED_RESOLVED.items():
        item = decision_by_id.get(needed)
        if not item:
            bad.append(f"missing-owner-decision:{needed}")
            continue
        if item.get("status") != "resolved":
            bad.append(f"{needed}-not-resolved")
        if item.get("resolvedDecision") != expected:
            bad.append(f"{needed}-resolvedDecision={item.get('resolvedDecision')!r}")
        if not str(item.get("resolvedSummary") or "").strip():
            bad.append(f"{needed}-missing-resolvedSummary")

    if meta.get("gateBExited") is True:
        for needed in REQUIRED_RESOLVED:
            item = decision_by_id.get(needed) or {}
            if item.get("status") != "resolved" or item.get("resolvedDecision") != REQUIRED_RESOLVED[needed]:
                bad.append(f"exited-without-resolved:{needed}")

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
            if not isinstance(value, str) or value.strip() in EMPTY:
                bad.append(f"{sid}.empty.{field}")
        disp = row.get("disposition")
        if disp not in LEGAL:
            bad.append(f"{sid}.illegal-disposition={disp!r}")
        sample = str(row.get("gateASample") or "")
        if sample != "source-only" and not re.fullmatch(r"A\d+(,A\d+)*", sample.replace(" ", "")):
            bad.append(f"{sid}.gateASample={sample!r}")
        for od in row_decision_ids(row):
            item = decision_by_id.get(od)
            if not item:
                bad.append(f"{sid}.unknown-ownerDecisionId={od}")
            elif item.get("status") != "resolved":
                bad.append(f"{sid}.unresolved-ownerDecisionId={od}")
        rationale = str(row.get("rationale") or "")
        target = str(row.get("targetProductBehavior") or "")
        blob = rationale + " " + target + " " + str(row.get("dependencyOrGap") or "")
        if disp == "supported now" and re.search(r"current code already (fully )?supports", blob, re.I):
            bad.append(f"{sid}.supported-now-claims-current-complete")
        if disp == "supported now" and sample == "source-only":
            if row.get("gateSPreSampleGate") != "实现前补样本" and "实现前补样本" not in str(row.get("dependencyOrGap") or ""):
                bad.append(f"{sid}.source-only-missing-pre-sample-gate")

    dup = [k for k, n in Counter(ids).items() if n > 1]
    if dup:
        bad.append(f"duplicate-ids={dup}")
    missing = sorted(PLAN_REQUIRED - set(ids))
    if missing:
        bad.append("plan-surface-missing:" + ",".join(missing))

    list_row = by_id.get("sessions.list") or {}
    list_blob = " ".join(str(list_row.get(k) or "") for k in ("targetProductBehavior", "rationale"))
    if list_row:
        if not re.search(r"hides? rows with time\.archived|hides archived", list_blob, re.I):
            bad.append("sessions.list-missing-archive-hide")
        if not re.search(r"by-id", list_blob, re.I):
            bad.append("sessions.list-missing-by-id")
        if not re.search(r"aggregat|worktree", list_blob, re.I):
            bad.append("sessions.list-missing-aggregation")
        if not re.search(r"scoped", list_blob, re.I):
            bad.append("sessions.list-missing-scoped-list")

    archive = by_id.get("sessions.archive") or {}
    archive_blob = " ".join(str(archive.get(k) or "") for k in ("rationale", "targetProductBehavior"))
    if archive:
        if "archived" not in archive_blob.lower():
            bad.append("archive-missing-visibility-rule")
        if not re.search(r"hide", archive_blob, re.I):
            bad.append("sessions.archive-missing-hide-rule")
        if not re.search(r"by-id", archive_blob, re.I):
            bad.append("sessions.archive-missing-by-id")

    project = by_id.get("workspace.project") or {}
    project_blob = " ".join(str(project.get(k) or "") for k in ("targetProductBehavior", "rationale"))
    if project and not re.search(r"aggregat|worktree", project_blob, re.I):
        bad.append("workspace.project-missing-aggregation")

    od3 = decision_by_id.get("OD-3") or {}
    if od3.get("resolvedDecision") == "keep-mapped-future-or-unsupported":
        for sid in od3.get("affects") or []:
            row = by_id.get(sid) or {}
            if row.get("disposition") not in ("future", "deliberately unsupported"):
                bad.append(f"{sid}-od3-must-stay-future-or-unsupported")

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
    if nested and "dual-ingest" not in nested_blob.lower() and "dual ingest" not in nested_blob.lower():
        bad.append("nested_sync-missing-dual-ingest-ban")

    if md_text is not None:
        md_rows = parse_md_rows(md_text)
        for sid in ids:
            if sid not in md_text:
                bad.append(f"markdown-missing:{sid}")
            parsed = md_rows.get(sid)
            if not parsed:
                bad.append(f"markdown-missing-row:{sid}")
                continue
            if parsed["disposition"] != by_id[sid].get("disposition"):
                bad.append(f"markdown-disposition-drift:{sid}")
            if parsed["gateASample"] != by_id[sid].get("gateASample"):
                bad.append(f"markdown-sample-drift:{sid}")
        if "supported now" in md_text.lower() and "does not mean current" not in md_text.lower() and "不表示当前代码" not in md_text:
            if "Gate C will implement" not in md_text and "Gate C 将完成" not in md_text:
                bad.append("markdown-missing-supported-now-disclaimer")
        if "实现前补样本" not in md_text:
            bad.append("markdown-missing-pre-sample-gate")
        if "不是实施授权" not in md_text and "not implementation authorization" not in md_text.lower() and "not an implementation grant" not in md_text.lower():
            bad.append("markdown-missing-scope-not-authorization")

    return bad


def self_test() -> int:
    doc = load_map(MAP)
    orig = problems(doc, md_text=MD.read_text(encoding="utf-8") if MD.exists() else "")
    if orig:
        print("self-test FAIL: original map has problems", orig[:12], file=sys.stderr)
        return 1
    failures = []
    md_text = MD.read_text(encoding="utf-8") if MD.exists() else ""

    def expect(mut, label: str, *, md: str | None = None) -> None:
        mutated = mut(copy.deepcopy(doc))
        found = problems(mutated, md_text=md if md is not None else md_text)
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

    def exited_unresolved(d):
        d["meta"]["gateBExited"] = True
        d["meta"]["gateBExitBlockers"] = []
        for item in d["ownerDecisions"]:
            if item.get("id") == "OD-1":
                item["status"] = "pending"
                item["resolvedDecision"] = None
        return d

    def json_md_disposition_drift(d):
        for s in d["surfaces"]:
            if s.get("id") == "content.text":
                s["disposition"] = "future"
        return d

    expect(drop_list, "drop-sessions.list")
    expect(illegal, "illegal-disposition")
    expect(empty_rationale, "empty-rationale")
    expect(vision_now, "vision-supported-now")
    expect(always_permanent, "always-permanent")
    expect(exited_unresolved, "exited-unresolved")
    expect(json_md_disposition_drift, "json-md-disposition-drift")
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
        "ownerDecisions": [
            {
                "id": d.get("id"),
                "status": d.get("status"),
                "resolvedDecision": d.get("resolvedDecision"),
            }
            for d in (doc.get("ownerDecisions") or [])
            if isinstance(d, dict)
        ],
        "gateBExited": (doc.get("meta") or {}).get("gateBExited"),
        "gateBExitBlockers": (doc.get("meta") or {}).get("gateBExitBlockers"),
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
        f"future={counts.get('future', 0)}; "
        f"exited={report['gateBExited']}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
