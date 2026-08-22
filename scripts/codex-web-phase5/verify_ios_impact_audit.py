#!/usr/bin/env python3
"""Verify the Phase 5 iOS impact audit against its frozen iOS HEAD."""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
IOS = ROOT.parent / "cordcode-ios"
DOC = ROOT / "docs/2026-08-22-codex-web-ios-impact-audit.md"
HEAD = "2cdb490f17ce98b36a03c6f3cf59c86e3257feda"


def git(*args: str) -> str:
    return subprocess.check_output(["git", "-C", str(IOS), *args], text=True)


def require(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def main() -> int:
    require(IOS.is_dir(), f"missing iOS repo: {IOS}")
    require(DOC.is_file(), f"missing audit doc: {DOC}")
    require(git("cat-file", "-t", HEAD).strip() == "commit", f"missing frozen commit {HEAD}")
    doc = DOC.read_text(encoding="utf-8")
    require(HEAD in doc, "audit does not name the frozen iOS HEAD")

    for index, title in enumerate(
        [
            "BackendKind",
            "wire kind",
            "backend discovery / switch",
            "server creation",
            "display / icon",
            "capability gate",
            "model mapping",
            "permission mapping",
            "agent mapping",
            "session / message cache scope",
            "stream / recovery special case",
            "protocol mirror / tests",
        ],
        start=1,
    ):
        require(f"### {index}. {title}" in doc, f"missing audit surface {index}: {title}")

    tracked = git("ls-tree", "-r", "--name-only", HEAD, "OpenCodeiOS/OpenCodeiOS").splitlines()
    codex_files: list[str] = []
    for path in tracked:
        if not path.endswith(".swift"):
            continue
        source = git("show", f"{HEAD}:{path}")
        if re.search(r"(?:\.codex\b|\"codex\"|case codex)", source):
            codex_files.append(path)
    require(codex_files, "no production codex references found at frozen HEAD")
    for path in codex_files:
        require(f"`{path}`" in doc, f"uncategorized production codex file: {path}")

    baseline_backend_models = git(
        "show", f"{HEAD}:OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift"
    )
    require("codexWeb" not in baseline_backend_models, "frozen pre-implementation HEAD already has codexWeb")

    required_paths = [
        "OpenCodeiOS/OpenCodeiOS/Services/Backend/BackendModels.swift",
        "OpenCodeiOS/OpenCodeiOS/Services/Backend/BridgeDiscoveryService.swift",
        "OpenCodeiOS/OpenCodeiOS/Services/Bridge/CCCodeBridgeBackendClient.swift",
        "OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel.swift",
        "OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+Generation.swift",
        "OpenCodeiOS/OpenCodeiOS/ViewModels/ChatViewModel+MessageSync.swift",
        "OpenCodeiOS/OpenCodeiOS/App/ChatUIKitContainerView.swift",
        "OpenCodeiOS/OpenCodeiOSTests/BridgeTransportTests.swift",
        "OpenCodeiOS/OpenCodeiOSTests/ChatViewModelSessionSyncV2Tests.swift",
        "OpenCodeiOS/OpenCodeiOSTests/ModelReasoningEffortWireMappingTests.swift",
        "docs/protocol/bridge-v1.md",
        "docs/protocol/unified-bridge-protocol.md",
    ]
    for path in required_paths:
        require(f"`{path}`" in doc, f"missing required audit path: {path}")

    for classification in ("must change", "verified generic", "intentionally codex-only", "N/A"):
        require(classification in doc, f"missing classification: {classification}")

    print(f"PASS: 12 surfaces; {len(codex_files)} production codex-reference files categorized; HEAD={HEAD[:12]}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (AssertionError, subprocess.CalledProcessError) as exc:
        print(f"FAIL: {exc}", file=sys.stderr)
        raise SystemExit(1)
