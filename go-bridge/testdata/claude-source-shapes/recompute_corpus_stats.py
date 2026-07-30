#!/usr/bin/env python3
# recompute_corpus_stats.py — IR-6 live-~/.claude corpus recomputation (NON-CI; owner-local).
#
# Recomputes the H3/H4 corpus statistics that the synthetic fixtures in this directory
# were derived from. ~/.claude writes continuously, so EVERY output line is bound to the
# run timestamp below — these are shape evidence, not permanent constants. The fixtures +
# Go tests (not this script) are the CI oracle.
#
# This script fixes the two bugs in the old inline README snippet (round-10 F9):
#   BUG 1 (attachment misclassified): attachment is a TOP-LEVEL `type` field (28,800 rows
#       observed), NOT a content block kind. The old script read message.content block
#       kinds to classify type, so every attachment-replay group fell into "other".
#       Fix: classify by the row's top-level `type` first, only falling back to content
#       block kinds for user/assistant message rows.
#   BUG 2 (H4 used future placement): the old script built byuuid[u] in a full first pass,
#       so a duplicate UUID's entry was overwritten by its LAST occurrence, then the whole
#       file was traversed against that final-state map — parent-chain ancestry could use
#       a future/wrong placement. Fix: build the uuid→{role,parent} map INCREMENTALLY in
#       the same pass that resolves each row, so a row only ever sees PRIOR occurrences
#       (parents always precede children in file order).
#
# version: 1
import json, glob, os, collections, sys, datetime

TS = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
files = glob.glob(os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True)


def top_level_kind(rec):
    # BUG-1 fix: top-level type wins; only user/assistant fall through to content blocks.
    t = rec.get("type")
    if t in ("attachment", "last-prompt", "queue-operation"):
        return t
    msg = rec.get("message") if isinstance(rec.get("message"), dict) else None
    if msg:
        c = msg.get("content")
        if isinstance(c, list):
            kinds = {b.get("type") for b in c if isinstance(b, dict)}
            if "tool_use" in kinds or "tool_result" in kinds:
                return "tool"
            if "server_tool_use" in kinds:
                return "server_tool"
            if "image" in kinds:
                return "image"
            if "text" in kinds:
                return "text"
        elif isinstance(c, str):
            return "text"
    return "other"


# (a) same-file top-level UUID reuse groups (H3 replay phenomenon), by top-level kind.
uuid_groups = collections.Counter()
uuid_type = collections.Counter()
for fp in files:
    byuuid = collections.defaultdict(list)
    try:
        for line in open(fp, errors="replace"):
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except Exception:
                continue
            u = rec.get("uuid")
            if isinstance(u, str) and u:
                byuuid[u].append(rec)
    except Exception:
        continue
    for u, recs in byuuid.items():
        if len(recs) > 1 and len({json.dumps(r, sort_keys=True) for r in recs}) > 1:
            uuid_groups[os.path.basename(fp)] += 1
            uuid_type[top_level_kind(recs[0])] += 1

# (b) file-order currentTurn != parent-chain nearest-user owner (H4).
# BUG-2 fix: incremental byuuid so ancestry only sees prior occurrences.
total = 0
mismatch = 0
for fp in files:
    byuuid = {}  # uuid -> {"role":..., "parent":...}, built incrementally
    fo_user = None  # file-order currentTurnID (advanced by non-empty user text rows)
    try:
        for line in open(fp, errors="replace"):
            line = line.strip()
            if not line:
                continue
            try:
                rec = json.loads(line)
            except Exception:
                continue
            u = rec.get("uuid")
            role = rec.get("type")
            parent = rec.get("parentUuid")
            # index THIS row before resolving children, but AFTER resolving this row's own chain.
            msg = rec.get("message") if isinstance(rec.get("message"), dict) else None
            ut = None
            if msg:
                c = msg.get("content")
                if isinstance(c, list):
                    ts = [b.get("text", "") for b in c if isinstance(b, dict) and b.get("type") == "text"]
                    if ts:
                        ut = "".join(ts)
            # advance file-order currentTurn on a non-empty user text row
            if role == "user" and ut and ut.strip():
                fo_user = u
            # resolve parent-chain nearest-user owner for user/assistant rows (incremental map)
            if role in ("user", "assistant"):
                puid = parent
                chain = None
                seen = set()
                while puid and puid not in seen:
                    seen.add(puid)
                    pr = byuuid.get(puid)
                    if not pr:
                        break
                    if pr["role"] == "user":
                        chain = puid
                        break
                    puid = pr["parent"]
                if chain is not None:
                    total += 1
                    if fo_user != chain:
                        mismatch += 1
            # now index this row for subsequent rows (incremental — only prior rows visible)
            if isinstance(u, str) and u:
                byuuid[u] = {"role": role, "parent": parent}
    except Exception:
        continue

print(f"# recompute_corpus_stats.py v1")
print(f"# samplingTime: {TS}  (output is bound to this instant; ~/.claude shifts continuously)")
print(f"# files scanned: {len(files)}")
print(f"H3 same-file top-level UUID reuse groups: {sum(uuid_groups.values())} "
      f"(across {len(uuid_groups)} files) by top-level kind: {dict(uuid_type)}")
print(f"H4 resolvable assistant rows: {total}; file-order != parent-chain-nearest-user: {mismatch}")
print(f"# NOTE: H4 mismatch count depends on which user rows advance file-order currentTurnID")
print(f"# (here: non-empty user text rows). The fixture + Go test is the authoritative oracle,")
print(f"# not this live total.")
