#!/usr/bin/env python3
"""Join content-free Claude source and Kernel trace rows by private physical identity."""

import argparse
import json
from pathlib import Path


def key(row):
    return (
        row.get("backendID"),
        row.get("sessionPrefix"),
        row.get("segmentStableKey"),
        row.get("segmentGeneration"),
        row.get("byteStart"),
        row.get("byteEnd"),
        row.get("uuid"),
    )


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("trace_jsonl")
    args = parser.parse_args()
    source = {}
    kernel = {}
    for raw in Path(args.trace_jsonl).read_text(encoding="utf-8").splitlines():
        if not raw.strip():
            continue
        row = json.loads(raw)
        if row.get("msg") != "go-bridge: claude_source_trace":
            continue
        if row.get("phase") == "kernel":
            kernel[key(row)] = row
        elif row.get("phase") in ("hydrate", "live"):
            source[key(row)] = row
    print("phase\tingestDomain\tbyteStart\tbyteEnd\tuuid\ttransition\tsourceStateRev\tprojectionTurnId\tprojectionPartId")
    joined = 0
    for identity in sorted(source):
        source_row = source[identity]
        kernel_row = kernel.get(identity)
        if kernel_row is None:
            continue
        joined += 1
        print(
            "\t".join(
                str(value)
                for value in (
                    source_row.get("phase", ""),
                    source_row.get("ingestDomain", ""),
                    source_row.get("byteStart", ""),
                    source_row.get("byteEnd", ""),
                    source_row.get("uuid", ""),
                    kernel_row.get("transition", ""),
                    kernel_row.get("sourceStateRev", ""),
                    kernel_row.get("projectionTurnId", ""),
                    kernel_row.get("projectionPartId", ""),
                )
            )
        )
    print(f"joined={joined} source={len(source)} kernel={len(kernel)}")
    if joined == 0:
        raise SystemExit("no source/kernel trace rows joined")


if __name__ == "__main__":
    main()
