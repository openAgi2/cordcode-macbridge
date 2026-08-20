#!/usr/bin/env python3
"""Validate captured sanitized samples. Missing files are reported, not invented."""

from __future__ import annotations

import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent.parent
SAMPLES = HERE / "samples"

REQUIRED_META = ("scenario", "opencodeVersion", "sourceCommit")
SCENARIOS = {
    "A1": "a1-first-healthy-text.sanitized.json",
    "A2": "a2-follow-up.sanitized.json",
    "A3": "a3-provider-error.sanitized.json",
    "A4": "a4-abort.sanitized.json",
    "A5": "a5-sse-reconnect.sanitized.json",
    "A6": "a6-permission.sanitized.json",
    "A7": "a7-question.sanitized.json",
    "A8": "a8-todos.sanitized.json",
    "A9": "a9-prompt-parts.sanitized.json",
    "A10": "a10-session-listing.sanitized.json",
}


def main() -> int:
    missing = []
    bad = []
    captured = []
    for sid, name in SCENARIOS.items():
        path = SAMPLES / name
        if not path.exists():
            missing.append(sid)
            continue
        doc = json.loads(path.read_text(encoding="utf-8"))
        meta = doc.get("meta") or {}
        for key in REQUIRED_META:
            if not meta.get(key):
                bad.append(f"{sid}.meta.{key}")
        if meta.get("opencodeVersion") != "1.18.18":
            bad.append(f"{sid}.version")
        if meta.get("sourceCommit") != "2cba7e227d":
            bad.append(f"{sid}.commit")
        if "http" not in doc:
            bad.append(f"{sid}.http")
        dumped = json.dumps(doc)
        if "gatea-pass" in dumped or "OPENCODE_SERVER_PASSWORD" in dumped:
            bad.append(f"{sid}.secret-leak")
        if "/Users/jacklee" in dumped:
            bad.append(f"{sid}.abs-path")
        captured.append(sid)
    print(f"captured={captured}")
    print(f"missing={missing}")
    if bad:
        print("shape errors:", bad, file=sys.stderr)
        return 1
    # Missing samples are a Gate A capture gap, not a check script failure
    # when running before capture. Exit 0 with a report; capture-tests uses
    # --require-all after captures exist.
    if "--require-all" in sys.argv and missing:
        print("required samples missing", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
