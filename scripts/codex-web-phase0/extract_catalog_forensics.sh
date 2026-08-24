#!/bin/bash
# extract_catalog_forensics.sh —— v5 §3.4 冻结的离线提取入口。
#
# 只从 bridge 日志选择 msg="go-bridge: catalog_forensics" 的事件并验证 schema，
# 按 runId 写入 scripts/codex-web-phase0/dumps/catalog-forensics/<run-id>/
# （catalog-forensics.v1.jsonl + manifest.json）。提取器不得复制其他日志行。
#
# 用法：
#   extract_catalog_forensics.sh <go-bridge.log>
#   [OUT_DIR 覆盖输出根，默认 scripts/codex-web-phase0/dumps/catalog-forensics]
#
# 产出前执行脱敏自检：个人绝对路径 / hostname / installation·environment ID /
# workspace·title 模式任何命中都会导致脚本以非零退出，证据不得入 Git（§7.2）。

set -euo pipefail

LOG_FILE="${1:-}"
OUT_BASE="${2:-${OUT_DIR:-$(dirname "$0")/dumps/catalog-forensics}}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

usage() {
  echo "usage: $0 <go-bridge.log> [OUT_DIR]" >&2
  exit 2
}

[ -n "$LOG_FILE" ] && [ -f "$LOG_FILE" ] || usage

# 敏感模式（递归扫描用）：个人绝对路径（任意一层）、本机 hostname 残留、
# installation/environment ID。证据包内任何命中都导致非零退出。
SENSITIVE_PATTERNS='(/Users/[A-Za-z0-9_.-]+|\.local|env_e_[A-Za-z0-9]+|inst_e_[A-Za-z0-9]+)'

python3 - "$LOG_FILE" "$OUT_BASE" <<'PY'
import json
import os
import re
import sys
from collections import defaultdict

log_file, out_base = sys.argv[1], sys.argv[2]

SCHEMA_FIELDS = [
    "schemaVersion", "runId", "sampleId", "correlationId", "corpusKind",
    "triggerKind", "recordKind", "monotonicOffsetMs", "rowCount", "rawCount",
    "fingerprint", "catalogGenerationBefore", "catalogGenerationAfter",
    "rowKeyHmac", "fieldChangeMask", "index", "updatedAtDeltaMs",
    "observerError", "droppedCount",
]
RECORD_KINDS = {"sample_summary", "row_diff", "run_summary"}
CORPUS_KINDS = {"head", "authoritative"}
TRIGGER_KINDS = {"seed", "periodic_tick", "head_changed", "catalog_signal_coalesced"}
OBSERVER_ERRORS = {"none", "encode_failed", "limit_reached", "write_failed", "dropped"}
HEX64 = re.compile(r"^[0-9a-f]{64}$")
HEX24 = re.compile(r"^[0-9a-f]{24}$")
HEX32 = re.compile(r"^[0-9a-f]{32}$")

def parse_event(line: str):
    """从 TextHandler 行提取 msg 与 event 载荷。只认目标消息，其余行一律跳过。"""
    m = re.search(r'msg=(?:"go-bridge: catalog_forensics"|go-bridge: catalog_forensics)', line)
    if not m:
        return None
    em = re.search(r'\bevent=("(?:[^"\\]|\\.)*"|[^\s]+)', line[m.end():])
    if not em:
        return None
    raw = em.group(1)
    try:
        # TextHandler 把 JSON 载荷转义成带引号的字符串字面量，需先解一次转义再解析。
        payload = json.loads(json.loads(raw)) if raw.startswith('"') else json.loads(raw)
    except json.JSONDecodeError:
        raise SystemExit(f"event payload decode failed: {line[:200]}")
    if not isinstance(payload, dict):
        raise SystemExit(f"event payload not an object: {line[:200]}")
    return payload

def validate(event: dict, line_num: int):
    for f in SCHEMA_FIELDS:
        if f not in event:
            raise SystemExit(f"line {line_num}: missing field {f}")
    if event["schemaVersion"] != "catalog-forensics.v1":
        raise SystemExit(f"line {line_num}: schemaVersion={event['schemaVersion']}")
    if event["recordKind"] not in RECORD_KINDS:
        raise SystemExit(f"line {line_num}: recordKind={event['recordKind']}")
    if event["recordKind"] == "sample_summary":
        if event["corpusKind"] not in CORPUS_KINDS:
            raise SystemExit(f"line {line_num}: corpusKind={event['corpusKind']}")
        if event["triggerKind"] not in TRIGGER_KINDS:
            raise SystemExit(f"line {line_num}: triggerKind={event['triggerKind']}")
        # runId 是 16 随机字节(32-hex),sampleId 是 12 随机字节(24-hex)。
        if not HEX32.match(event["runId"]) or not HEX24.match(event["sampleId"]):
            raise SystemExit(f"line {line_num}: bad run/sample identity")
        if event["fingerprint"] is not None and not HEX64.match(event["fingerprint"]):
            raise SystemExit(f"line {line_num}: fingerprint not 64-hex")
        if event["correlationId"] is not None and not HEX24.match(event["correlationId"]):
            raise SystemExit(f"line {line_num}: correlationId not 24-hex")
    if event["recordKind"] == "row_diff":
        if not HEX64.match(event["rowKeyHmac"]):
            raise SystemExit(f"line {line_num}: rowKeyHmac not 64-hex")
        mask = event["fieldChangeMask"]
        if not isinstance(mask, int) or not (0 < mask <= 127):
            raise SystemExit(f"line {line_num}: fieldChangeMask={mask}")
    if event["observerError"] not in OBSERVER_ERRORS:
        raise SystemExit(f"line {line_num}: observerError={event['observerError']}")
    # 值级脱敏：身份字段按宽度白名单（runId=32-hex / sampleId·correlationId=24-hex /
    # rowKeyHmac=64-hex）；其余敏感字段必须为 null。
    identity_width = {"runId": HEX32, "sampleId": HEX24, "correlationId": HEX24, "rowKeyHmac": HEX64}
    for f in ("runId", "sampleId", "correlationId", "rowKeyHmac"):
        v = event[f]
        if v is None:
            continue
        if not identity_width[f].match(v):
            raise SystemExit(f"line {line_num}: non-hex identity value in {f}")

events = []
with open(log_file, encoding="utf-8", errors="replace") as fh:
    for num, line in enumerate(fh, 1):
        ev = parse_event(line)
        if ev is not None:
            validate(ev, num)
            events.append(ev)

if not events:
    raise SystemExit("no catalog_forensics events found in log")

by_run = defaultdict(list)
for ev in events:
    by_run[ev["runId"]].append(ev)

for run_id, run_events in by_run.items():
    out_dir = os.path.join(out_base, run_id)
    os.makedirs(out_dir, exist_ok=True)
    jsonl_path = os.path.join(out_dir, "catalog-forensics.v1.jsonl")
    with open(jsonl_path, "w", encoding="utf-8") as out:
        for ev in run_events:
            out.write(json.dumps(ev, ensure_ascii=False) + "\n")
    body = "".join(json.dumps(ev, ensure_ascii=False) + "\n" for ev in run_events)
    err_counts = defaultdict(int)
    for ev in run_events:
        if ev["recordKind"] == "run_summary":
            err_counts[ev["observerError"]] += 1
    manifest = {
        "schemaVersion": "catalog-forensics.v1",
        "runId": run_id,
        "eventCount": len(run_events),
        "jsonlBytes": len(body.encode("utf-8")),
        "sourceBasename": os.path.basename(log_file),
        "observerErrors": dict(err_counts) if err_counts else {"none_written": True},
        "extractedAt": __import__("datetime").datetime.now().isoformat(timespec="seconds"),
    }
    with open(os.path.join(out_dir, "manifest.json"), "w", encoding="utf-8") as out:
        json.dump(manifest, out, ensure_ascii=False, indent=1)
        out.write("\n")
    print(f"{out_dir}: {len(run_events)} events for run {run_id}")
PY

# 递归脱敏自检（§7.2）：证据包整体不得命中敏感模式。
echo "redaction scan: $OUT_BASE"
if grep -rEo "$SENSITIVE_PATTERNS" "$OUT_BASE" 2>/dev/null | head -5 | grep .; then
  echo "FATAL: sensitive content found in evidence pack (see above); do not commit." >&2
  exit 1
fi
echo "redaction scan: clean"
