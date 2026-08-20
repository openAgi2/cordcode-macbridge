#!/usr/bin/env python3
"""Structural gate for the opencode-web canonical execution design.

This checker proves that an implementation agent can find every required
decision field in one document. It deliberately does not prove an external
payload shape; that remains the job of the raw-sample checkers.
"""

from __future__ import annotations

import argparse
from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[5]
DESIGN = ROOT / "docs/2026-08-20-opencode-web-source-first-convergence-plan.md"

DOSSIERS = {
    "6.1": "Runtime version and transport boundary",
    "6.2": "Session list, session detail, and project buckets",
    "6.3": "Load, reopen, and hydrate the message page",
    "6.4": "Create session and send first/follow-up messages",
    "6.5": "Live stream, terminal state, abort, reconnect, and external turns",
    "6.6": "Provider, model, agent, and selected variant",
    "6.7": "Permission Dock",
    "6.8": "Structured Question Dock",
    "6.9": "Todo Dock",
    "6.10": "Rename, archive, and delete sessions",
    "6.11": "Capability activation and explicit exclusions",
}

FIELDS = (
    "Status",
    "User-visible behavior",
    "Official UI source",
    "Server/schema source",
    "Same-version samples",
    "Verified transport shape",
    "Bridge and SSV2 mapping",
    "Error and unsupported behavior",
    "Owning tests",
    "Out of scope",
)

PENDING = {
    "E1": "6.4",
    "E2": "6.3",
    "E3": "6.5",
    "E4": "6.6",
    "E5": "6.6",
    "E6": "6.10",
    "E7": "6.10",
}


def sections(text: str) -> dict[str, str]:
    matches = list(re.finditer(r"^### (6\.\d+) [^\n]*$", text, re.MULTILINE))
    result: dict[str, str] = {}
    for idx, match in enumerate(matches):
        end = matches[idx + 1].start() if idx + 1 < len(matches) else len(text)
        result[match.group(1)] = text[match.start():end]
    return result


def check(text: str) -> list[str]:
    problems: list[str] = []
    if not text.startswith("# OpenCode Web source-first convergence design（canonical）"):
        problems.append("canonical title missing")
    if "single canonical design" not in text or "only implementation-design authority" not in text:
        problems.append("single-authority declaration missing")
    if "## 6. Gate C — implementation order" in text:
        problems.append("obsolete Gate C sketch still present")

    found = sections(text)
    if set(found) != set(DOSSIERS):
        problems.append(
            "dossier ids mismatch: expected="
            + ",".join(DOSSIERS)
            + " actual="
            + ",".join(found)
        )
    for dossier_id, title in DOSSIERS.items():
        body = found.get(dossier_id, "")
        if title not in body:
            problems.append(f"{dossier_id}: title mismatch")
        for field in FIELDS:
            if f"**{field}:**" not in body:
                problems.append(f"{dossier_id}: missing field {field}")

    for evidence_id, dossier_id in PENDING.items():
        queue_pattern = rf"\| {evidence_id} \|[^\n]+`PENDING-SAMPLE`"
        if not re.search(queue_pattern, text):
            problems.append(f"{evidence_id}: pending queue row missing")
        body = found.get(dossier_id, "")
        if evidence_id not in body or "PENDING-SAMPLE" not in body:
            problems.append(f"{evidence_id}: missing pending gate in dossier {dossier_id}")

    for sample_id in (f"A{i}" for i in range(1, 11)):
        if not re.search(rf"\b{sample_id}\b", text):
            problems.append(f"captured sample {sample_id} not referenced")

    required_phrases = (
        "does **not** choose bridge mappings",
        "Only then may an implementation directive authorize that translator",
        "HTTP 204 means admission only",
        "iOS `ProjectionStore` remains the only `messages[]` writer",
        "one source adapter normalizes direct SSE",
        "do not synthesize IDs",
        "UNKNOWN UNTIL E1",
        "UNKNOWN UNTIL E2",
        "UNKNOWN UNTIL E3",
        "UNKNOWN UNTIL E4/E5/E1",
        "UNKNOWN UNTIL E6/E7",
    )
    for phrase in required_phrases:
        if phrase not in text:
            problems.append(f"required invariant missing: {phrase}")
    return problems


def self_test(text: str) -> list[str]:
    mutations = {
        "remove-dossier-field": text.replace(
            "- **Owning tests:** `TestVerified118Only`",
            "- **Removed tests:** `TestVerified118Only`",
            1,
        ),
        "remove-dossier": re.sub(
            r"^### 6\.9 [\s\S]*?(?=^### 6\.10 )", "", text, count=1, flags=re.MULTILINE
        ),
        "restore-obsolete-sketch": text + "\n## 6. Gate C — implementation order\n",
        "drop-e1-queue-state": text.replace("| E1 | selected variant", "| EX | selected variant", 1),
        "drop-e2-unknown": text.replace("UNKNOWN UNTIL E2", "assume reasoning", 1),
        "drop-single-writer": text.replace(
            "iOS `ProjectionStore` remains the only `messages[]` writer",
            "iOS may also apply history",
            1,
        ),
        "drop-source-owner-boundary": text.replace(
            "does **not** choose bridge mappings", "may choose bridge mappings", 1
        ),
    }
    failures: list[str] = []
    for name, mutated in mutations.items():
        if not check(mutated):
            failures.append(name)
    return failures


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    text = DESIGN.read_text()
    problems = check(text)
    if problems:
        for problem in problems:
            print(f"FAIL: {problem}")
        return 1
    if args.self_test:
        failures = self_test(text)
        if failures:
            print("FAIL: self-test mutations escaped: " + ", ".join(failures))
            return 1
        print("self-test PASS")
        return 0
    print(
        "canonical design ok: dossiers=11 pending-evidence=7 "
        "single-authority=True productCodeFrozen=True"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
