#!/usr/bin/env python3
"""Fail closed when the Phase 6 observation pack drifts from the design gates."""

from pathlib import Path
import re


ROOT = Path(__file__).resolve().parents[2]
DESIGN = ROOT / "docs/2026-08-21-codex-web-backend-design.md"
MATRIX = ROOT / "docs/2026-08-22-codex-web-owner-device-acceptance.md"
OBSERVATION = ROOT / "docs/2026-08-22-codex-web-phase6-observation.md"
METRICS = ROOT / "docs/2026-08-21-codex-web-ab-frame-metrics.md"
TOPOLOGY = ROOT / "scripts/codex-web-phase0/dumps/gate-desktop-attach/README.md"


def numbered_rows(text: str, heading: str) -> list[list[str]]:
    section = text.split(heading, 1)[1]
    rows: list[list[str]] = []
    for line in section.splitlines():
        if rows and line.startswith("## "):
            break
        if re.match(r"^\|\s*\d+\s*\|", line):
            rows.append([cell.strip() for cell in line.strip().strip("|").split("|")])
    return rows


design = DESIGN.read_text()
matrix = MATRIX.read_text()
observation = OBSERVATION.read_text()
metrics = METRICS.read_text()
topology = TOPOLOGY.read_text()

matrix_rows = numbered_rows(matrix, "## 14 行矩阵")
assert len(matrix_rows) == 14, f"owner matrix must contain 14 rows, got {len(matrix_rows)}"
assert [row[0] for row in matrix_rows] == [str(i) for i in range(1, 15)]
assert all(row[4] in {"NOT RUN", "PASS", "FAIL", "BLOCKED"} for row in matrix_rows)
assert all(matrix_rows[index - 1][4] == "PASS" for index in (6, 7, 8)), "T1 rows 6-8 must remain PASS"

retirement_rows = numbered_rows(observation, "## 3. §15 退役门槛账本")
assert len(retirement_rows) == 12, f"retirement ledger must contain 12 rows, got {len(retirement_rows)}"
assert "状态：`PREPARED`" in observation
assert "禁止退役旧入口" in observation
assert "PENDING_OWNER_DECISION" in observation

for term in (
    "send → turn/started",
    "send → 首 delta",
    "每轮 delta 数",
    "完成延迟",
):
    assert term in metrics, f"missing A/B metric: {term}"

for term in (
    "v2.0 T1",
    "managed-loopback",
    "topology_gate=PASS",
):
    assert term in topology, f"missing topology proof term: {term}"

for term in (
    "Phase 6：并行观察与退役裁决",
    "旧源码保留一个发布观察窗口",
    "memory 能力对照",
):
    assert term in design, f"missing design retirement contract: {term}"

print("PASS: Phase 6 observation pack preserves 14 owner rows, 12 retirement gates, T1 proof, and fail-closed retirement")
