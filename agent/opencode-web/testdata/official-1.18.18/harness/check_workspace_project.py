#!/usr/bin/env python3
"""Gate the official 1.18.18 /project registry sample (directive-003 Phase 0).

Derives every claim from the RAW HTTP payloads only — the sanitized summary is
never a truth source. Fails when the top-level shape, project identity, or
worktree/directory isolation is not independently provable from raw.
"""

from __future__ import annotations

import copy
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
RAW = ROOT / "agent/opencode-web/testdata/official-1.18.18/samples/wp-workspace-project.raw.json"
SANITIZED = ROOT / "agent/opencode-web/testdata/official-1.18.18/samples/wp-workspace-project.sanitized.json"

MIN_DISTINCT_WORKTREES = 2


def load() -> dict:
    return json.loads(RAW.read_text(encoding="utf-8"))


def _entries(raw: dict, phase: str):
    payloads = raw.get("rawPayloads") or {}
    value = payloads.get(phase)
    return value if isinstance(value, list) else None


def problems(doc: dict, require_sanitized: bool = True) -> list[str]:
    bad: list[str] = []
    meta = doc.get("meta") or {}
    if meta.get("opencodeVersion") != "1.18.18":
        bad.append("meta-version")
    if meta.get("sourceCommit") != "2cba7e227d":
        bad.append("meta-source-commit")
    if meta.get("captureStatus") != "captured":
        bad.append("meta-not-captured")

    for phase in ("baseline", "afterCreate", "afterDelete"):
        entries = _entries(doc, phase)
        if entries is None:
            bad.append(f"raw-missing:{phase}")
    if bad:
        return bad

    # Top-level shape: every phase must be a bare JSON array.
    for phase in ("baseline", "afterCreate", "afterDelete"):
        entries = _entries(doc, phase)
        if not isinstance(entries, list):
            bad.append(f"top-level-not-array:{phase}")

    # Identity: non-global rows need non-empty, unique ids; worktree absolute
    # and unique (directory isolation).
    created = _entries(doc, "afterCreate")
    ids: list[str] = []
    worktrees: list[str] = []
    for item in created:
        if not isinstance(item, dict):
            bad.append("entry-not-object")
            continue
        pid = item.get("id")
        if not isinstance(pid, str) or not pid:
            bad.append("project-id-empty")
            continue
        if pid in ids:
            bad.append(f"project-id-duplicate:{pid}")
        ids.append(pid)
        wt = item.get("worktree")
        if not isinstance(wt, str) or not wt:
            bad.append(f"worktree-empty:{pid}")
            continue
        if not wt.startswith("/"):
            bad.append(f"worktree-not-absolute:{wt}")
        if wt in worktrees:
            bad.append(f"worktree-duplicate:{wt}")
        worktrees.append(wt)

    distinct = [w for w in worktrees if w not in ("/", "")]
    if len(distinct) < MIN_DISTINCT_WORKTREES:
        bad.append(f"distinct-worktrees:{len(distinct)}")

    # Field presence derived from raw rows only: id/worktree/time/sandboxes
    # observed; vcs present on git rows (absent on the global pseudo-project).
    git_rows = [i for i in created if isinstance(i, dict) and i.get("vcs")]
    if not git_rows:
        bad.append("no-vcs-rows")
    for item in created:
        if not isinstance(item, dict):
            continue
        for field in ("time", "sandboxes"):
            if field not in item:
                bad.append(f"field-missing:{field}:{item.get('id')}")

    # Growth: the two git worktrees appear in afterCreate but not baseline.
    baseline_wts = {
        str(i.get("worktree") or "") for i in _entries(doc, "baseline") if isinstance(i, dict)
    }
    grown = [w for w in distinct if w not in baseline_wts]
    if len(grown) < MIN_DISTINCT_WORKTREES:
        bad.append(f"registry-growth-not-proven:{sorted(baseline_wts)}")

    # Delete observation must be present and derived from raw: the deleted
    # harness git worktree either stays registered (server registry truth) or
    # is dropped — both are honest observations, but the observation itself
    # must exist and must be consistent with the raw afterDelete payload.
    after_delete = doc.get("afterDelete") or {}
    deleted_dir = after_delete.get("deletedDirectory")
    if not deleted_dir:
        bad.append("delete-observation-missing")
    else:
        import os

        real_deleted = os.path.realpath(deleted_dir)
        delete_wts = {
            str(i.get("worktree") or "") for i in _entries(doc, "afterDelete") if isinstance(i, dict)
        }
        observed_still = after_delete.get("deletedGitWorktreeStillRegistered")
        derived_still = real_deleted in delete_wts
        if observed_still is not derived_still:
            bad.append(f"delete-observation-inconsistent:stated={observed_still} derived={derived_still}")

    if require_sanitized and not SANITIZED.is_file():
        bad.append("sanitized-missing")
    return bad


def self_test() -> int:
    doc = load()
    orig = problems(doc)
    if orig:
        print("self-test FAIL original", orig[:12], file=sys.stderr)
        return 1
    failures: list[str] = []

    def expect(mut, label: str) -> None:
        found = problems(mut(copy.deepcopy(doc)))
        ok = bool(found)
        print(f"  {label}: {found[:4]} {'OK' if ok else 'FAIL'}")
        if not ok:
            failures.append(label)

    def corrupt_top_level(d):
        d["rawPayloads"]["afterCreate"] = {"data": d["rawPayloads"]["afterCreate"]}
        return d

    def corrupt_project_id(d):
        rows = d["rawPayloads"]["afterCreate"]
        for item in rows:
            if isinstance(item, dict) and item.get("vcs") == "git":
                item["id"] = ""
                break
        return d

    def corrupt_worktree_isolation(d):
        rows = d["rawPayloads"]["afterCreate"]
        wts = [i for i in rows if isinstance(i, dict) and i.get("vcs") == "git"]
        if len(wts) >= 2:
            wts[1]["worktree"] = wts[0]["worktree"]
        return d

    def corrupt_delete_observation(d):
        d["afterDelete"]["deletedGitWorktreeStillRegistered"] = not d["afterDelete"][
            "deletedGitWorktreeStillRegistered"
        ]
        return d

    expect(corrupt_top_level, "top-level-shape-corrupted")
    expect(corrupt_project_id, "project-id-corrupted")
    expect(corrupt_worktree_isolation, "worktree-isolation-corrupted")
    expect(corrupt_delete_observation, "delete-observation-flipped")
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
    bad = problems(doc)
    created = _entries(doc, "afterCreate") or []
    report = {
        "raw": str(RAW),
        "entries": len(created),
        "worktrees": [i.get("worktree") for i in created if isinstance(i, dict)],
        "distinctNonGlobal": len([i for i in created if isinstance(i, dict) and i.get("worktree") not in ("/", "", None)]),
        "deletedGitWorktreeStillRegistered": (doc.get("afterDelete") or {}).get(
            "deletedGitWorktreeStillRegistered"
        ),
        "problems": bad,
    }
    print(json.dumps(report, indent=2, ensure_ascii=False))
    if bad:
        print("workspace.project sample FAIL", bad[:16], file=sys.stderr)
        return 1
    print(
        f"workspace.project sample ok: entries={report['entries']} "
        f"distinctNonGlobal={report['distinctNonGlobal']} "
        f"deletedStillRegistered={report['deletedGitWorktreeStillRegistered']}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
