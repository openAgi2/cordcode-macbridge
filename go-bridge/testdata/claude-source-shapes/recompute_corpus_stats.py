#!/usr/bin/env python3
"""Recompute aggregate H3/H4 Claude source-shape evidence without printing content.

The live ~/.claude corpus changes continuously. This script therefore reports a real UTC
samplingTime for every run and treats the checked-in fixtures, not live totals, as the stable test
oracle. H4 is computed twice:

1. a one-pass incremental graph, matching the mapper's streaming view;
2. an indexed nearest-prior-occurrence graph, implemented independently.

The command fails if the two H4 strategies disagree.
"""

from __future__ import annotations

import argparse
import bisect
import collections
import datetime
import glob
import json
import os
import sys
from dataclasses import dataclass
from typing import Any, Iterable

SCRIPT_VERSION = 2


def message(record: dict[str, Any]) -> dict[str, Any] | None:
    value = record.get("message")
    return value if isinstance(value, dict) else None


def content_blocks(record: dict[str, Any]) -> list[dict[str, Any]]:
    msg = message(record)
    if not msg:
        return []
    content = msg.get("content")
    if isinstance(content, str):
        return [{"type": "text", "text": content}]
    if isinstance(content, list):
        return [block for block in content if isinstance(block, dict)]
    return []


def mapper_role(record: dict[str, Any]) -> str:
    msg = message(record)
    role = msg.get("role") if msg else None
    return role if isinstance(role, str) and role else str(record.get("type") or "")


def mapper_identity(record: dict[str, Any]) -> str | None:
    msg = message(record)
    message_id = msg.get("id") if msg else None
    if isinstance(message_id, str) and message_id.strip():
        return message_id.strip()
    uuid = record.get("uuid")
    return uuid.strip() if isinstance(uuid, str) and uuid.strip() else None


def text_content(record: dict[str, Any]) -> str:
    return "".join(
        str(block.get("text") or "")
        for block in content_blocks(record)
        if block.get("type") == "text"
    )


def is_internal_compact(record: dict[str, Any]) -> bool:
    return bool(record.get("isCompactSummary") or record.get("isVisibleInTranscriptOnly"))


def is_resume_meta(record: dict[str, Any]) -> bool:
    return (
        record.get("type") == "user"
        and record.get("isMeta") is True
        and any(
            block.get("type") == "text"
            and str(block.get("text") or "").strip() == "Continue from where you left off."
            for block in content_blocks(record)
        )
    )


def is_resume_no_response(record: dict[str, Any]) -> bool:
    return (
        record.get("type") == "assistant"
        and any(
            block.get("type") == "text"
            and str(block.get("text") or "").strip() == "No response requested."
            for block in content_blocks(record)
        )
    )


def is_interrupt(record: dict[str, Any]) -> bool:
    return (
        record.get("type") == "user"
        and any(
            block.get("type") == "text"
            and str(block.get("text") or "").strip().startswith("[Request interrupted by user")
            for block in content_blocks(record)
        )
    )


def is_mapper_turn_start(record: dict[str, Any]) -> bool:
    return (
        record.get("type") in ("user", "assistant")
        and message(record) is not None
        and not is_internal_compact(record)
        and not is_resume_meta(record)
        and mapper_role(record) == "user"
        and not is_interrupt(record)
        and bool(text_content(record).strip())
        and mapper_identity(record) is not None
    )


def top_level_kind(record: dict[str, Any]) -> str:
    row_type = record.get("type")
    if row_type in ("attachment", "last-prompt", "queue-operation"):
        return str(row_type)
    kinds = {block.get("type") for block in content_blocks(record)}
    if "tool_use" in kinds or "tool_result" in kinds:
        return "tool"
    if "server_tool_use" in kinds:
        return "server_tool"
    if "image" in kinds:
        return "image"
    if "text" in kinds:
        return "text"
    return "other"


def load_records(path: str) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    try:
        with open(path, encoding="utf-8", errors="replace") as handle:
            for line in handle:
                try:
                    value = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if isinstance(value, dict):
                    records.append(value)
    except OSError:
        return []
    return records


def nearest_user_incremental(
    record: dict[str, Any],
    prior: dict[str, dict[str, Any]],
) -> str | None:
    parent = record.get("parentUuid")
    if not isinstance(parent, str) or not parent:
        return None
    ancestor = prior.get(parent)
    return ancestor.get("turnOwner") if ancestor else None


@dataclass(frozen=True)
class H4Stats:
    assistant_rows: int = 0
    mismatches: int = 0


def h4_streaming(records: list[dict[str, Any]]) -> H4Stats:
    prior: dict[str, dict[str, Any]] = {}
    current_turn: str | None = None
    skip_resume_no_response = False
    total = 0
    mismatch = 0
    for record in records:
        chain_owner = nearest_user_incremental(record, prior)
        row_type = record.get("type")
        admitted = False
        if is_internal_compact(record) or (row_type == "system" and record.get("subtype") == "compact_boundary"):
            pass
        elif message(record) is None:
            pass
        elif is_resume_meta(record):
            skip_resume_no_response = True
        elif skip_resume_no_response and is_resume_no_response(record):
            skip_resume_no_response = False
        else:
            if skip_resume_no_response:
                skip_resume_no_response = False
            admitted = row_type in ("user", "assistant")

        role = mapper_role(record)
        if admitted and role == "user" and not is_interrupt(record) and text_content(record).strip():
            current_turn = mapper_identity(record)
        elif admitted and role == "assistant" and chain_owner is not None:
            total += 1
            if current_turn != chain_owner:
                mismatch += 1

        uuid = record.get("uuid")
        if isinstance(uuid, str) and uuid:
            prior[uuid] = {
                "turnOwner": mapper_identity(record) if is_mapper_turn_start(record) else chain_owner,
            }
    return H4Stats(total, mismatch)


def h4_indexed(records: list[dict[str, Any]]) -> H4Stats:
    """Independent H4 implementation using nearest prior UUID occurrence indexes."""
    positions: dict[str, list[int]] = collections.defaultdict(list)
    for index, record in enumerate(records):
        uuid = record.get("uuid")
        if isinstance(uuid, str) and uuid:
            positions[uuid].append(index)

    def prior_record(uuid: str, before: int) -> tuple[int, dict[str, Any]] | None:
        indexes = positions.get(uuid, [])
        slot = bisect.bisect_left(indexes, before) - 1
        return (indexes[slot], records[indexes[slot]]) if slot >= 0 else None

    def owner(record: dict[str, Any], before: int) -> str | None:
        parent = record.get("parentUuid")
        visited: set[str] = set()
        cursor = before
        while isinstance(parent, str) and parent and parent not in visited:
            visited.add(parent)
            found = prior_record(parent, cursor)
            if found is None:
                return None
            ancestor_index, ancestor = found
            if is_mapper_turn_start(ancestor):
                return mapper_identity(ancestor)
            cursor = ancestor_index
            parent = ancestor.get("parentUuid")
        return None

    current_turn: str | None = None
    suppress_next = False
    total = 0
    mismatch = 0
    for index, record in enumerate(records):
        row_type = record.get("type")
        ignored = is_internal_compact(record) or message(record) is None
        if row_type == "system" and record.get("subtype") == "compact_boundary":
            ignored = True
        if not ignored and is_resume_meta(record):
            suppress_next = True
            ignored = True
        elif not ignored and suppress_next and is_resume_no_response(record):
            suppress_next = False
            ignored = True
        elif not ignored and suppress_next:
            suppress_next = False
        if ignored or row_type not in ("user", "assistant"):
            continue
        role = mapper_role(record)
        if role == "user":
            if not is_interrupt(record) and text_content(record).strip():
                current_turn = mapper_identity(record)
            continue
        graph_owner = owner(record, index)
        if role == "assistant" and graph_owner is not None:
            total += 1
            mismatch += int(current_turn != graph_owner)
    return H4Stats(total, mismatch)


def h3_stats(files: Iterable[str]) -> tuple[int, int, dict[str, int], int]:
    groups = 0
    files_with_groups = 0
    by_kind: collections.Counter[str] = collections.Counter()
    physical_occurrences = 0
    for path in files:
        by_uuid: dict[str, list[dict[str, Any]]] = collections.defaultdict(list)
        for record in load_records(path):
            uuid = record.get("uuid")
            if isinstance(uuid, str) and uuid:
                by_uuid[uuid].append(record)
        file_groups = 0
        for occurrences in by_uuid.values():
            canonical = {json.dumps(record, sort_keys=True, separators=(",", ":")) for record in occurrences}
            if len(occurrences) > 1 and len(canonical) > 1:
                groups += 1
                file_groups += 1
                physical_occurrences += len(occurrences)
                by_kind[top_level_kind(occurrences[0])] += 1
        files_with_groups += int(file_groups > 0)
    return groups, files_with_groups, dict(sorted(by_kind.items())), physical_occurrences


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--root",
        default=os.path.expanduser("~/.claude/projects"),
        help="Root containing JSONL files (default: ~/.claude/projects)",
    )
    parser.add_argument("--expect-h4", help="Expected assistantRows,mismatches for a fixed fixture root")
    args = parser.parse_args()
    files = sorted(glob.glob(os.path.join(os.path.expanduser(args.root), "**", "*.jsonl"), recursive=True))
    aggregate_stream = H4Stats()
    aggregate_indexed = H4Stats()
    for path in files:
        records = load_records(path)
        stream = h4_streaming(records)
        indexed = h4_indexed(records)
        aggregate_stream = H4Stats(
            aggregate_stream.assistant_rows + stream.assistant_rows,
            aggregate_stream.mismatches + stream.mismatches,
        )
        aggregate_indexed = H4Stats(
            aggregate_indexed.assistant_rows + indexed.assistant_rows,
            aggregate_indexed.mismatches + indexed.mismatches,
        )
    if aggregate_stream != aggregate_indexed:
        print(
            f"FAIL: H4 strategies disagree streaming={aggregate_stream} indexed={aggregate_indexed}",
            file=sys.stderr,
        )
        return 1
    if args.expect_h4:
        expected = H4Stats(*(int(value) for value in args.expect_h4.split(",", maxsplit=1)))
        if aggregate_stream != expected:
            print(f"FAIL: fixed H4 expectation {expected}, got {aggregate_stream}", file=sys.stderr)
            return 1

    groups, grouped_files, kinds, occurrences = h3_stats(files)
    timestamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    print(f"# recompute_corpus_stats.py v{SCRIPT_VERSION}")
    print(f"# samplingTime: {timestamp}")
    print(f"# filesScanned: {len(files)}")
    print(
        "H3 logicalRecordReuseGroups: "
        f"{groups}; physicalOccurrencesInGroups: {occurrences}; filesWithGroups: {grouped_files}; "
        f"groupsByTopLevelKind: {json.dumps(kinds, sort_keys=True)}"
    )
    print(
        "H4 resolvableAssistantRows: "
        f"{aggregate_stream.assistant_rows}; fileOrderOwnerMismatchRows: {aggregate_stream.mismatches}; "
        "crossCheck: streaming==indexed"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
