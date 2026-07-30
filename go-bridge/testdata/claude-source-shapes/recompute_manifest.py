#!/usr/bin/env python3
# recompute_manifest.py — IR-6 machine-readable manifest validator (CI oracle).
#
# Reads every *.jsonl fixture in this directory, recomputes each file's sha256 and
# per-record [byteStart,byteEnd) range from the raw bytes, and asserts they match
# manifest.json exactly. Exit code 0 = manifest in sync; 1 = drift (re-run generator
# or fix the fixture). This is the deterministic, CI-friendly counterpart to the Go
# test TestClaudeSourceShapeFixtures_LoadAllAndValidateManifest.
#
# version: 1   |   schema: claude-source-shapes/manifest@v1
import json, hashlib, glob, os, sys

HERE = os.path.dirname(os.path.abspath(__file__))


def recompute(path):
    data = open(path, "rb").read()
    records = []
    pos = 0
    for line in data.split(b"\n"):
        if not line:
            if pos < len(data):
                pos += 1
            continue
        start = pos
        end = pos + len(line) + 1  # include the trailing LF
        json.loads(line)  # raise on invalid JSON
        records.append({"byteStart": start, "byteEnd": end})
        pos = end
    return hashlib.sha256(data).hexdigest(), len(data), records


def main():
    manifest_path = os.path.join(HERE, "manifest.json")
    if not os.path.exists(manifest_path):
        print("FAIL: manifest.json missing", file=sys.stderr)
        return 1
    manifest = json.load(open(manifest_path))
    by_file = {f["file"]: f for f in manifest.get("fixtures", [])}
    failures = 0
    for fp in sorted(glob.glob(os.path.join(HERE, "*.jsonl"))):
        name = os.path.basename(fp)
        sha, nbytes, records = recompute(fp)
        entry = by_file.get(name)
        if entry is None:
            print(f"FAIL {name}: present on disk but missing from manifest.json", file=sys.stderr)
            failures += 1
            continue
        if entry["sha256"] != sha:
            print(f"FAIL {name}: sha256 drift manifest={entry['sha256'][:16]} actual={sha[:16]}", file=sys.stderr)
            failures += 1
        if entry["bytes"] != nbytes:
            print(f"FAIL {name}: byte-size drift manifest={entry['bytes']} actual={nbytes}", file=sys.stderr)
            failures += 1
        if entry["recordCount"] != len(records):
            print(f"FAIL {name}: record-count drift manifest={entry['recordCount']} actual={len(records)}", file=sys.stderr)
            failures += 1
        elif [{"byteStart": r["byteStart"], "byteEnd": r["byteEnd"]} for r in entry["records"]] != records:
            print(f"FAIL {name}: per-record byte-range drift", file=sys.stderr)
            failures += 1
    # also catch manifest entries whose file vanished
    for name in by_file:
        if not os.path.exists(os.path.join(HERE, name)):
            print(f"FAIL {name}: listed in manifest.json but file missing on disk", file=sys.stderr)
            failures += 1
    if failures:
        print(f"manifest validation: {failures} failure(s)", file=sys.stderr)
        return 1
    print(f"manifest validation: OK ({len(by_file)} fixtures in sync)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
