#!/usr/bin/env python3
"""Phase 0a 归档样本形状校验（可重跑；p0a-tests 证据）。

对 scripts/grokbuild-phase0/samples/ 的归档样本做结构断言：
- P1 initialize：agentCapabilities/authMethods/_meta.agentVersion
- P2 ACU：availableCommands 元素形状（name/description/input）
- P3 commands/list：commands 数组与会话 ACU 子集关系
- P5 CMU：current_mode_update 形状；plan_mode.json Pending 落盘
- P7 恢复：load 后无 CMU、plan_mode.json 保持 Pending、system_prompt 无 plan 注入、prompt_context 恒 extend

退出码 0 = 全部断言通过。
"""
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
S = os.path.join(HERE, "samples")

failures = []


def check(cond, label):
    if cond:
        print(f"  ok    {label}")
    else:
        print(f"  FAIL  {label}")
        failures.append(label)


def load(name):
    with open(os.path.join(S, name), encoding="utf-8") as f:
        return json.load(f)


def load_jsonl(name):
    out = []
    with open(os.path.join(S, name), encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                out.append(json.loads(line))
    return out


print("P1 initialize")
init = load("p1-initialize.json")["response"]["result"]
check(init.get("protocolVersion") == 1, "protocolVersion == 1")
ac = init.get("agentCapabilities", {})
check(ac.get("loadSession") is True, "agentCapabilities.loadSession true")
check(isinstance(init.get("authMethods"), list) and init["authMethods"], "authMethods non-empty")
check(init.get("_meta", {}).get("agentVersion") == "1.0.13", "_meta.agentVersion == 1.0.13")
init_acu = init.get("_meta", {}).get("availableCommands")
check(isinstance(init_acu, list) and len(init_acu) == 7,
      "_meta.availableCommands == 7 builtin commands (diagnostic snapshot only)")
check(all(c.get("input") is None or isinstance(c.get("input"), dict) for c in init_acu),
      "init availableCommands input null-or-object, no commandId")

print("P2 ACU shape")
notifs = load_jsonl("p2-handshake-notifications.jsonl")
acu = None
for n in notifs:
    u = (n.get("params") or {}).get("update") or {}
    if u.get("sessionUpdate") == "available_commands_update":
        acu = u
check(acu is not None, "handshake contains available_commands_update")
cmds = acu["availableCommands"]
check(all(("name" in c and "description" in c and ("input" in c)) for c in cmds),
      "every command has name/description/input")
check(all(c.get("input") is None or isinstance(c.get("input"), dict) for c in cmds),
      "input is null or object")
check(all("commandId" not in c for c in cmds), "no commandId field (official shape)")

print("P3 commands/list vs session ACU")
lst = load("p3-commands-list-catalog.json")["response"]["result"]["commands"]
lst_names = {c["name"] for c in lst}
full_acu = None
for n in load_jsonl("p7-14-notifs-after-load.jsonl"):
    u = (n.get("params") or {}).get("update") or {}
    if u.get("sessionUpdate") == "available_commands_update":
        full_acu = {c["name"] for c in u["availableCommands"]}
check(len(lst_names) == 34, f"catalog list count == 34 (got {len(lst_names)})")
check(full_acu is not None and len(full_acu) == 43,
      f"post-load session ACU == 43 (got {len(full_acu) if full_acu else 0})")
check(lst_names <= (full_acu or set()), "catalog list is SUBSET of session ACU (not equivalent)")

print("P5 CMU + persisted Pending")
mode_notifs = load_jsonl("p5-mode-notifications.jsonl")
cmus = [ (n.get("params") or {}).get("update") or {} for n in mode_notifs
         if (n.get("params") or {}).get("update", {}).get("sessionUpdate") == "current_mode_update" ]
check(len(cmus) >= 1 and cmus[0].get("currentModeId") == "plan", "CMU currentModeId == plan on stdout")
pm = load("p5-plan-mode-pending.json")
check(pm.get("state") == "Pending", "plan_mode.json persisted state == Pending")

print("P7 recovery semantics")
load_resp = load("p7-13-load-response.json")["response"]
check("error" not in load_resp or load_resp.get("error") is None, "session/load succeeds")
after = load_jsonl("p7-14-notifs-after-load.jsonl")
cmu_after = [n for n in after
             if (n.get("params") or {}).get("update", {}).get("sessionUpdate") == "current_mode_update"]
check(len(cmu_after) == 0, "NO current_mode_update after load-restore")
pm_after = load("p7-15-plan-mode-after-load.json")["plan_mode"]
check(pm_after.get("state") == "Pending", "plan_mode.json stays Pending after load (file not rewritten)")
sp = open(os.path.join(S, "p7-system-prompt-after-load.txt"), encoding="utf-8").read()
check("read-only software architect" not in sp, "restored system prompt has NO plan role injection")
pc = load("p7-prompt-context-after-load.json")
check(pc.get("prompt_mode") == "extend", "prompt_context.prompt_mode == extend (not a mode source)")

print()
if failures:
    print(f"FAILED: {len(failures)} assertion(s)")
    sys.exit(1)
print("ALL SAMPLE-SHAPE ASSERTIONS PASSED")
