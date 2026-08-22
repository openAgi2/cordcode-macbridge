#!/usr/bin/env python3
"""Verify the Phase 5 owner matrix mirrors design §13.3 without claiming results."""

from pathlib import Path
import re


ROOT = Path(__file__).resolve().parents[2]
DESIGN = ROOT / "docs/2026-08-21-codex-web-backend-design.md"
MATRIX = ROOT / "docs/2026-08-22-codex-web-owner-device-acceptance.md"


def table_rows(text: str, start_heading: str) -> list[str]:
    section = text.split(start_heading, 1)[1]
    rows = []
    for line in section.splitlines():
        if line.startswith("## ") or line.startswith("### "):
            if rows:
                break
            continue
        if re.match(r"^\|\s*\d+\s*\|", line):
            rows.append(line)
    return rows


design_rows = table_rows(DESIGN.read_text(), "### 13.3 owner 真机验收矩阵")
matrix_rows = table_rows(MATRIX.read_text(), "## 14 行矩阵")

assert len(design_rows) == 14, f"design row count changed: {len(design_rows)}"
assert len(matrix_rows) == 14, f"owner matrix row count: {len(matrix_rows)}"

valid_results = {"NOT RUN", "PASS", "FAIL", "BLOCKED"}
for index, (design_row, matrix_row) in enumerate(zip(design_rows, matrix_rows), 1):
    assert design_row.startswith(f"| {index} |"), f"design row order broken at {index}"
    assert matrix_row.startswith(f"| {index} |"), f"matrix row order broken at {index}"
    cells = [cell.strip() for cell in matrix_row.strip().strip("|").split("|")]
    assert cells[4] in valid_results, f"row {index} has invalid result: {cells[4]}"

required_terms = [
    "Relay", "LocalDaemon", "Embedded", "custom provider", "requestUserInput",
    "ownership", "中断网络", "旧 Codex",
]
matrix_text = MATRIX.read_text()
for term in required_terms:
    assert term in matrix_text, f"missing matrix contract term: {term}"

print("PASS: owner matrix has 14 ordered rows, valid honest results, and required §13.3 contracts")
