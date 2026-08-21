#!/usr/bin/env python3
"""Gate the official 1.18.18 /project registry sample (directive-006 WP-FIX).

Evidence ownership: EVERY asserted fact is derived from the archived raw
HTTP log — each GET /project entry carries its own status and response
payload. The afterCreate/afterDelete blocks and meta.captureStatus are
NON-AUTHORITATIVE copies: tampering with a copy cannot change the derived
result, but any copy/raw disagreement is an explicit `summary-mismatch` FAIL.
"""

from __future__ import annotations

import copy
import json
import os
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
RAW = ROOT / "agent/opencode-web/testdata/official-1.18.18/samples/wp-workspace-project.raw.json"
SANITIZED = ROOT / "agent/opencode-web/testdata/official-1.18.18/samples/wp-workspace-project.sanitized.json"

MIN_DISTINCT_WORKTREES = 2


def load() -> dict:
    return json.loads(RAW.read_text(encoding="utf-8"))


def _project_responses(http: list) -> list[dict]:
    """The /project GETs in order, each {status, response} — the truth source."""
    out = []
    for entry in http:
        if entry.get("method") == "GET" and entry.get("path") == "/project":
            out.append(entry)
    return out


def _worktrees(response) -> list[str]:
    if not isinstance(response, list):
        return []
    return [str(i.get("worktree") or "") for i in response if isinstance(i, dict)]


def derive(doc: dict) -> dict:
    """Derive every fact from http[]. Returns derived state (no pass/fail)."""
    http = doc.get("http") or []
    gets = _project_responses(http)
    phases = [g.get("response") for g in gets]
    statuses = [g.get("status") for g in gets]
    baseline, after_create, after_delete = (phases + [None, None, None])[:3]

    ids: list[str] = []
    worktrees: list[str] = []
    if isinstance(after_create, list):
        for item in after_create:
            if isinstance(item, dict):
                pid = item.get("id")
                ids.append(pid if isinstance(pid, str) else "")
                wt = item.get("worktree")
                worktrees.append(wt if isinstance(wt, str) else "")
    distinct = [w for w in worktrees if w not in ("", "/")]
    baseline_wts = set(_worktrees(baseline))

    clones = doc.get("httpClones") or {}
    clone_posts = 0
    for log in clones.values():
        for entry in log or []:
            if entry.get("method") == "POST" and entry.get("path") == "/session":
                clone_posts += 1

    deleted_dir = ((doc.get("afterDelete") or {}).get("deletedDirectory")) or ""
    deleted_real = os.path.realpath(deleted_dir) if deleted_dir else ""
    deleted_still = deleted_real in _worktrees(after_delete) if after_delete is not None else None

    return {
        "statuses": statuses,
        "project_get_count": len(gets),
        "baseline_is_list": isinstance(baseline, list),
        "after_create_is_list": isinstance(after_create, list),
        "after_delete_is_list": isinstance(after_delete, list),
        "ids": ids,
        "worktrees": worktrees,
        "distinct": distinct,
        "grown": [w for w in distinct if w not in baseline_wts],
        "clone_session_posts": clone_posts,
        "deleted_real": deleted_real,
        "deleted_still_registered": deleted_still,
    }


def problems(doc: dict) -> tuple[list[str], dict]:
    bad: list[str] = []
    d = derive(doc)

    meta = doc.get("meta") or {}
    if meta.get("opencodeVersion") != "1.18.18":
        bad.append("meta-version")
    if meta.get("sourceCommit") != "2cba7e227d":
        bad.append("meta-source-commit")
    src = doc.get("source") or {}
    if not (src.get("ui") and src.get("server")):
        bad.append("source-citations-missing")

    if d["project_get_count"] < 3:
        bad.append(f"http-project-gets:{d['project_get_count']}")
        return bad, d
    for i, st in enumerate(d["statuses"]):
        if st != 200:
            bad.append(f"http-project-status[{i}]={st}")
    if not (d["baseline_is_list"] and d["after_create_is_list"] and d["after_delete_is_list"]):
        bad.append("http-project-top-level-not-array")
        return bad, d

    if len([i for i in d["ids"] if i]) != len(d["ids"]):
        bad.append("project-id-empty")
    if len(set(d["ids"])) != len(d["ids"]):
        bad.append("project-id-duplicate")
    for wt in d["worktrees"]:
        if not wt.startswith("/"):
            bad.append(f"worktree-not-absolute:{wt}")
    if len(set(d["worktrees"])) != len(d["worktrees"]):
        bad.append("worktree-duplicate")
    if len(d["distinct"]) < MIN_DISTINCT_WORKTREES:
        bad.append(f"distinct-worktrees:{len(d['distinct'])}")
    if len(d["grown"]) < MIN_DISTINCT_WORKTREES:
        bad.append("registry-growth-not-proven")
    if d["clone_session_posts"] < 2:
        bad.append(f"clone-session-posts:{d['clone_session_posts']}")
    if d["deleted_still_registered"] is None:
        bad.append("delete-observation-derivation-failed")

    # NON-AUTHORITATIVE copies must agree with the derived result.
    ac = doc.get("afterCreate") or {}
    if ac.get("count") is not None and ac.get("count") != len(d["worktrees"]):
        bad.append("summary-mismatch:afterCreate.count")
    if list(ac.get("worktrees") or []) != d["worktrees"]:
        bad.append("summary-mismatch:afterCreate.worktrees")
    ad = doc.get("afterDelete") or {}
    if ad.get("deletedGitWorktreeStillRegistered") is not None and ad.get(
        "deletedGitWorktreeStillRegistered"
    ) != d["deleted_still_registered"]:
        bad.append("summary-mismatch:afterDelete.deletedGitWorktreeStillRegistered")
    if list(ad.get("worktrees") or []) != _worktrees(_project_responses(doc.get("http") or [])[-1].get("response") if d["project_get_count"] >= 3 else None):
        bad.append("summary-mismatch:afterDelete.worktrees")

    if not SANITIZED.is_file():
        bad.append("sanitized-missing")
    return bad, d


def self_test() -> int:
    doc = load()
    bad, _ = problems(doc)
    if bad:
        print("self-test FAIL original", bad[:12], file=sys.stderr)
        return 1
    failures: list[str] = []

    def expect(mut, label: str) -> None:
        found, _ = problems(mut(copy.deepcopy(doc)))
        ok = bool(found)
        print(f"  {label}: {found[:4]} {'OK' if ok else 'FAIL'}")
        if not ok:
            failures.append(label)

    def break_status(d):
        d["http"][0]["status"] = 500
        return d

    def break_top_level(d):
        for e in d["http"]:
            if e.get("path") == "/project":
                e["response"] = {"data": e["response"]}
                return d
        return d

    def break_project_id(d):
        for e in d["http"]:
            if e.get("path") == "/project" and isinstance(e.get("response"), list):
                for item in e["response"]:
                    if isinstance(item, dict) and item.get("vcs"):
                        item["id"] = ""
                        return d
        return d

    def break_worktree_isolation(d):
        for e in d["http"]:
            if e.get("path") == "/project" and isinstance(e.get("response"), list):
                rows = [i for i in e["response"] if isinstance(i, dict) and i.get("vcs")]
                if len(rows) >= 2:
                    rows[1]["worktree"] = rows[0]["worktree"]
                    return d
        return d

    def break_deleted_row(d):
        # Drop the deleted worktree's row from the FINAL raw response — the
        # derived deleted-still-registered flips; the stale copy must mismatch.
        last = [e for e in d["http"] if e.get("path") == "/project"][-1]
        wt = os.path.realpath(d["afterDelete"]["deletedDirectory"])
        last["response"] = [i for i in last["response"] if i.get("worktree") != wt]
        return d

    def break_copy_only(d):
        # Tamper ONLY the summary copy — derived result must stay, mismatch must FAIL.
        d["afterCreate"]["count"] = 999
        return d

    expect(break_status, "http-status-corrupted")
    expect(break_top_level, "top-level-shape-corrupted")
    expect(break_project_id, "project-id-corrupted")
    expect(break_worktree_isolation, "worktree-isolation-corrupted")
    expect(break_deleted_row, "deleted-row-removed-from-raw")
    expect(break_copy_only, "summary-copy-tampered")
    if failures:
        print("self-test FAIL", failures, file=sys.stderr)
        return 1
    print("self-test PASS")
    return 0


def main() -> int:
    if "--self-test" in sys.argv:
        return self_test()
    if not RAW.exists():
        print("missing raw sample", RAW, file=sys.stderr)
        return 1
    doc = load()
    bad, d = problems(doc)
    report = {
        "raw": str(RAW),
        "derived": {
            "projectGets": d["project_get_count"],
            "statuses": d["statuses"],
            "worktrees": d["worktrees"],
            "distinctNonGlobal": len(d["distinct"]),
            "grownFromBaseline": len(d["grown"]),
            "deletedGitWorktreeStillRegistered": d["deleted_still_registered"],
        },
        "problems": bad,
    }
    print(json.dumps(report, indent=2, ensure_ascii=False))
    if bad:
        print("workspace.project sample FAIL", bad[:16], file=sys.stderr)
        return 1
    print(
        f"workspace.project sample ok (derived from http[]): gets={d['project_get_count']} "
        f"distinctNonGlobal={len(d['distinct'])} grown={len(d['grown'])} "
        f"deletedStillRegistered={d['deleted_still_registered']}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
