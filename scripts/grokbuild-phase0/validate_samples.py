#!/usr/bin/env python3
"""Phase 0a 归档样本形状校验（可重跑；p0a-tests 证据）。

对 scripts/grokbuild-phase0/samples/ 的归档样本做结构断言：
- P1 initialize：agentCapabilities/authMethods/_meta.agentVersion
- P2 ACU：availableCommands 元素形状（name/description/input）
- P3 commands/list：commands 数组与会话 ACU 子集关系
- P5 CMU：current_mode_update 形状；plan_mode.json Pending 落盘
- P7 恢复：load 后无 CMU、plan_mode.json 保持 Pending、system_prompt 无 plan 注入、prompt_context 恒 extend
- P6 真实 turn（p6-turns.json）：四 turn 终态/usage/hostTurn 反馈/取消形状 + 脱敏完备性

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

print("P6 real-turn lifecycle (p6-turns.json)")
p6 = load("p6-turns.json")
turns = p6["turns"]


def stop_reason(turn_name):
    r = turns[turn_name]["response"]
    assert "result" in r and "error" not in r, f"{turn_name}: response must be a result"
    return r["result"].get("stopReason")


def updates(turn_name, kind):
    for n in turns[turn_name]["stream"]:
        u = (n.get("params") or {}).get("update") or {}
        if u.get("sessionUpdate") == kind:
            yield u


def ext_notifs(turn_name, method):
    return [n for n in turns[turn_name]["stream"] if n.get("method") == method]


# A/B: host-turn slash 反馈 —— 零模型、end_turn settle、正文经 agent_message_chunk
for name, marker in (("A-hooks-list", "Loaded hooks"), ("B-hooks-add-invalid", "Hook path must be absolute.")):
    check(stop_reason(name) == "end_turn", f"{name}: stopReason == end_turn")
    meta = turns[name]["response"]["result"].get("_meta", {})
    check(meta.get("totalTokens") == 0, f"{name}: totalTokens == 0 (zero-model proof)")
    bodies = [u for u in updates(name, "agent_message_chunk")]
    check(any(marker in (b.get("content", {}) or {}).get("text", "") for b in bodies),
          f"{name}: agent_message_chunk carries feedback body containing {marker!r}")
    check(all(b.get("_meta", {}).get("hostTurn") is True for b in bodies if marker in b.get("content", {}).get("text", "")),
          f"{name}: feedback chunk _meta.hostTurn == true")
    check(len(list(updates(name, "turn_completed"))) == 1 and
          list(updates(name, "turn_completed"))[0].get("stop_reason") == "end_turn",
          f"{name}: exactly one turn_completed(stop_reason=end_turn)")

# C: 真实模型 turn —— end_turn + modelCalls=1 + usage 三处
check(stop_reason("C-real-prompt") == "end_turn", "C: stopReason == end_turn")
cmeta = turns["C-real-prompt"]["response"]["result"]["_meta"]
check(cmeta.get("usage", {}).get("modelCalls") == 1, "C: _meta.usage.modelCalls == 1")
check(cmeta.get("modelId", "").startswith("grok-"), "C: _meta.modelId grok-*")
check(cmeta.get("totalTokens", 0) > 0, "C: _meta.totalTokens > 0")
c_tc = list(updates("C-real-prompt", "turn_completed"))
check(len(c_tc) == 1 and c_tc[0].get("stop_reason") == "end_turn", "C: turn_completed stop_reason=end_turn")
# usage 通知轨：ext TurnCompleted.usage.totals（camelCase）
c_ext = ext_notifs("C-real-prompt", "_x.ai/session_notification")
turn_completed_ext = [n for n in c_ext
                      if (n.get("params", {}).get("update", {}) or {}).get("sessionUpdate") == "turn_completed"
                      or "turn_completed" in json.dumps(n.get("params", {}))]
check(any("usage" in json.dumps(n.get("params", {})) and "modelCalls" in json.dumps(n.get("params", {}))
          for n in c_ext), "C: ext notification carries usage with modelCalls")
# response_completed：anthropic 风格 snake_case usage
rc = list(updates("C-real-prompt", "response_completed"))
check(len(rc) == 1, "C: exactly one response_completed update")
rc_usage = json.dumps(rc[0])
check("cache_read_input_tokens" in rc_usage or "input_tokens" in rc_usage,
      "C: response_completed usage uses snake_case keys")
check(len(list(updates("C-real-prompt", "user_message_chunk"))) >= 1, "C: user echo present")
check(len(list(updates("C-real-prompt", "agent_thought_chunk"))) >= 1, "C: thought chunks present")
pc_notifs = ext_notifs("C-real-prompt", "_x.ai/session/prompt_complete")
check(len(pc_notifs) == 1 and pc_notifs[0].get("params", {}).get("stopReason") == "end_turn",
      "C: prompt_complete stopReason == end_turn")

# D: 取消 —— cancelled + MidTurnAbort 三处一致；cancel 是 notification（无 id 响应帧）
d = turns["D-cancel"]
check(stop_reason("D-cancel") == "cancelled", "D: stopReason == cancelled")
check(d["response"]["result"]["_meta"].get("cancellationCategory") == "MidTurnAbort",
      "D: response _meta.cancellationCategory == MidTurnAbort")
d_tc = list(updates("D-cancel", "turn_completed"))
check(len(d_tc) == 1 and d_tc[0].get("stop_reason") == "cancelled" and d_tc[0].get("elapsed_ms") > 0,
      "D: turn_completed stop_reason=cancelled with elapsed_ms")
d_tc_meta = None
for n in d["stream"]:
    u = (n.get("params") or {}).get("update") or {}
    if u.get("sessionUpdate") == "turn_completed":
        d_tc_meta = (n.get("params") or {}).get("_meta", {})
check(d_tc_meta and d_tc_meta.get("cancellationCategory") == "MidTurnAbort",
      "D: turn_completed _meta.cancellationCategory == MidTurnAbort")
d_pc = ext_notifs("D-cancel", "_x.ai/session/prompt_complete")
check(len(d_pc) == 1 and d_pc[0].get("params", {}).get("stopReason") == "cancelled"
      and d_pc[0].get("params", {}).get("cancellationCategory") == "MidTurnAbort"
      and d_pc[0].get("params", {}).get("agentResult") is None,
      "D: prompt_complete cancelled + MidTurnAbort + agentResult null")
d_ids = [n.get("id") for n in d["stream"] if n.get("id") is not None]
check(d_ids == [d["response"].get("id")], "D: no separate cancel response frame (cancel is a notification)")

# ext 三轨存在性
check(len(ext_notifs("D-cancel", "_x.ai/sessions/changed")) >= 1, "ext _x.ai/sessions/changed present")
check(len(ext_notifs("A-hooks-list", "_x.ai/queue/changed")) >= 1, "ext _x.ai/queue/changed present")

# 脱敏完备性：归档样本不得含凭证/未脱敏 UUID
import re
p6_raw = open(os.path.join(S, "p6-turns.json"), encoding="utf-8").read()
for secret, label in (("meta4agi", "email"), ("teamId", "teamId"), ("agentInstanceId", "agentInstanceId"),
                      ("xai-", "token prefix")):
    check(secret not in p6_raw, f"redaction: no {label} in archived sample")
leftover_uuids = re.findall(r"\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b", p6_raw, re.I)
check(not leftover_uuids, "redaction: no raw UUIDs left (all REDACT_UUID)")

print()
if failures:
    print(f"FAILED: {len(failures)} assertion(s)")
    sys.exit(1)
print("ALL SAMPLE-SHAPE ASSERTIONS PASSED")
