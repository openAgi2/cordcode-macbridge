#!/usr/bin/env python3
"""p0-samples-tests：样本包自动校验（可复跑）。

1. 全部 fixture JSON 可解析；
2. 脱敏扫描（无真实用户 home 路径 / API key 形态 / 非 /var/folders 临时路径之外的个人路径）；
3. §12 各组最小覆盖清单逐项断言（对照 dumps/<group>/meta.json 与 raw.jsonl）；
4. 关键 wire 断言：与 §7.3 分歧裁决相关的字段物理存在性。
"""
import json
import pathlib
import re
import sys

HERE = pathlib.Path(__file__).resolve().parent
DUMPS = HERE / "dumps"
failures = []


def check(name, cond, detail=""):
    print(f"[{'PASS' if cond else 'FAIL'}] {name}" + (f" — {detail}" if detail else ""))
    if not cond:
        failures.append(name)


# 1. 全部 fixture 可解析
n = 0
for p in DUMPS.rglob("*.json*"):
    if p.name == "README.md":
        continue
    if p.suffix == ".jsonl":
        for line in p.read_text().splitlines():
            if line.strip():
                json.loads(line)
                n += 1
    else:
        json.loads(p.read_text())
        n += 1
check("all-fixtures-parse", True, f"{n} JSON docs/lines")

# 2. 脱敏扫描
bad = []
for p in DUMPS.rglob("*"):
    if not p.is_file() or p.name == "README.md":
        continue
    t = p.read_text(errors="ignore")
    if "/Users/" in t:
        bad.append(f"{p.name}: /Users/")
    if re.search(r"sk-[A-Za-z0-9]{20}", t):
        bad.append(f"{p.name}: api-key-shape")
    if "eyJhbGciOi" in t:
        bad.append(f"{p.name}: jwt-shape")
check("sanitization-scan", not bad, "; ".join(bad) if bad else "clean")

# 3. 各组存在 + 最小覆盖
GROUPS = {
    "initialize": ["initialize_ok"],
    "lifecycle": ["daemon_version_absent", "ws_healthz", "daemon_start"],
    "catalog": ["turn_completed", "delta_count", "thread_read_turns", "failed_turn_status",
                "steer_result", "interrupt", "cursor_pagination", "archived_list"],
    "interaction": ["command_approval_params_keys", "availableDecisions_present",
                    "file_approval_keys", "permission_approval_keys", "ask1_questions"],
    "models-config": ["model_list_count", "model_fields", "config_model_provider",
                      "config_has_model_providers"],
    "ownership": ["second_resume", "readonly_during_writer", "unsubscribe_status"],
    "reconnect": ["read_after_reconnect_turns", "resume_after_reconnect"],
}
for g, keys in GROUPS.items():
    meta = DUMPS / g / "meta.json"
    ok = meta.exists()
    if ok:
        e = json.loads(meta.read_text())["entries"]
        missing = [k for k in keys if k not in e]
        ok = not missing
        check(f"group-{g}", ok, f"missing={missing}" if missing else f"{len(e)} entries")
    else:
        check(f"group-{g}", False, "meta.json missing")

# 4. 关键 wire 断言（分歧裁决证据）
cat = json.loads((DUMPS / "catalog" / "raw.jsonl").read_text().splitlines()[0] and
                 json.dumps({})) if False else None
cat_lines = [json.loads(l) for l in (DUMPS / "catalog" / "raw.jsonl").read_text().splitlines()]
ia_lines = [json.loads(l) for l in (DUMPS / "interaction" / "raw.jsonl").read_text().splitlines()]
own_lines = [json.loads(l) for l in (DUMPS / "ownership" / "raw.jsonl").read_text().splitlines()]


def find(lines, **kw):
    for m in lines:
        msg = m.get("msg", {})
        if m.get("dir") != "server":
            continue
        if msg.get("method") == kw.get("method") and (kw.get("pred") is None or kw["pred"](msg)):
            return msg
    return None


# 4.1 command approval 携带 availableDecisions（未开 experimentalApi 的连接）
appr = find(ia_lines, method="item/commandExecution/requestApproval")
check("wire-availableDecisions-present",
      appr is not None and "availableDecisions" in appr["params"],
      "§7.3 分歧裁决：字段物理到达")
# 4.2 permission approval 无 availableDecisions + RequestPermissionProfile
papr = find(ia_lines, method="item/permissions/requestApproval")
check("wire-permission-approval-shape",
      papr is not None and "availableDecisions" not in papr["params"]
      and "permissions" in papr["params"] and "scope" not in papr["params"])
# 4.3 file approval 无 availableDecisions
fapr = find(ia_lines, method="item/fileChange/requestApproval")
check("wire-file-approval-no-availableDecisions",
      fapr is not None and "availableDecisions" not in fapr["params"])
# 4.4 requestUserInput 批结构 + resolved
ask = find(ia_lines, method="item/tool/requestUserInput")
check("wire-requestUserInput-batch",
      ask is not None and isinstance(ask["params"].get("questions"), list)
      and ask["params"]["questions"][0].get("id") == "confirm_path"
      and "isBlocking" in ask["params"])
res = find(ia_lines, method="serverRequest/resolved")
check("wire-serverRequest-resolved", res is not None)
# 4.5 ownership -32600 保留原文
se = json.loads((DUMPS / "ownership" / "meta.json").read_text())["entries"]
check("wire-ownership-minus32600",
      isinstance(se.get("second_resume"), dict) and se["second_resume"].get("code") == -32600
      and "active writer" in se["second_resume"].get("message", ""))
# 4.6 turn 生命周期：started → delta ≥2 → completed
started = find(cat_lines, method="turn/started")
deltas = [m for m in cat_lines if m.get("msg", {}).get("method") == "item/agentMessage/delta"]
completed = find(cat_lines, method="turn/completed")
check("wire-turn-lifecycle",
      started is not None and len(deltas) >= 2 and completed is not None,
      f"deltas={len(deltas)}")
# 4.7 completed 带 itemsView 三档之一
iv = (completed or {}).get("params", {}).get("turn", {}).get("itemsView")
check("wire-turn-completed-itemsView", iv in ("full", "summary", "notLoaded"), str(iv))
# 4.8 config/read additional 为空（存在性裁决）
mc = json.loads((DUMPS / "models-config" / "meta.json").read_text())["entries"]
check("wire-config-additional-empty",
      mc.get("config_has_model_providers") is False and mc.get("config_additional_keys") == [],
      "§7.3 additional.model_providers 不存在（样本裁决）")
# 4.9 interrupt 终态 interrupted
cm = json.loads((DUMPS / "catalog" / "meta.json").read_text())["entries"]
check("wire-interrupt-terminal", cm.get("interrupt_turn_status") == "interrupted")
check("wire-failed-terminal", cm.get("failed_turn_status") == "failed")

print()
if failures:
    print(f"FAILED {len(failures)}: {failures}")
    sys.exit(1)
print("ALL PASS")
