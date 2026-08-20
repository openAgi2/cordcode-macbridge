#!/usr/bin/env python3
"""Fail if Gate A inventory rows lack required citations."""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[5]
INVENTORY = ROOT / "docs" / "2026-08-20-opencode-web-1.18.18-sample-inventory.md"
REQUIRED = {f"A{i}" for i in range(1, 11)}


def main() -> int:
    text = INVENTORY.read_text(encoding="utf-8")
    rows = []
    in_matrix = False
    for line in text.splitlines():
        if line.startswith("## 1. P0 scenario matrix"):
            in_matrix = True
            continue
        if in_matrix and line.startswith("## "):
            break
        if not in_matrix or not line.startswith("| A"):
            continue
        cells = [c.strip() for c in line.strip("|").split("|")]
        if len(cells) < 8:
            print(f"short row: {line}", file=sys.stderr)
            return 1
        rows.append(cells)
    found = {row[0] for row in rows}
    missing = REQUIRED - found
    if missing:
        print(f"missing scenarios: {sorted(missing)}", file=sys.stderr)
        return 1
    empty = []
    for row in rows:
        sid, _scenario, ui, server, cmd, sample, status, mapping = row[:8]
        for label, value in [
            ("ui", ui),
            ("server", server),
            ("cmd", cmd),
            ("sample", sample),
            ("status", status),
            ("mapping", mapping),
        ]:
            if not value or value in {"-", "TODO", "tbd"}:
                empty.append(f"{sid}.{label}")
        if "packages/app/" not in ui and "server-compat.ts" not in ui and "session-load.ts" not in ui:
            # allow combined citations
            if "server-compat.ts" not in ui and "session.tsx" not in ui and "session-" not in ui:
                empty.append(f"{sid}.ui-path")
        if "packages/" not in server and "httpapi" not in server and "schema" not in server:
            empty.append(f"{sid}.server-path")
        if not re.search(r"harness/capture\.py --scenario a\d+", cmd):
            empty.append(f"{sid}.cmd")
        if "samples/" not in sample:
            empty.append(f"{sid}.sample")
        if status not in {"pending", "captured", "blocked", "out-of-scope"}:
            empty.append(f"{sid}.status={status}")
    if empty:
        print("inventory gaps:", empty, file=sys.stderr)
        return 1
    print(f"inventory ok: {len(rows)} P0 rows")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
