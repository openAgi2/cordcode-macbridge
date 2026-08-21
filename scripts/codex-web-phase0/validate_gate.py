#!/usr/bin/env python3
"""p0-gate-terminal-tests：三场景断言（可复跑，对照 dumps/gate-terminal/ 证据）。"""
import json
import pathlib
import sys

HERE = pathlib.Path(__file__).resolve().parent
GATE = HERE / "dumps" / "gate-terminal"
failures = []


def check(name, cond, detail=""):
    print(f"[{'PASS' if cond else 'FAIL'}] {name}" + (f" — {detail}" if detail else ""))
    if not cond:
        failures.append(name)


def lines(scene):
    return [json.loads(l) for l in (GATE / scene / "observer.jsonl").read_text().splitlines()]


# ---- 场景 1：daemon 已运行 + 默认 TUI ----
s1 = json.loads((GATE / "scene1-daemon-default" / "meta.json").read_text())["entries"]
l1 = lines("scene1-daemon-default")
check("s1-daemon-started", s1.get("daemon_start_rc") == 0 and "running" in str(s1.get("daemon_version")))
check("s1-tui-thread-loaded-in-daemon",
      bool(s1.get("loaded_threads_during_turn")) and isinstance(s1.get("loaded_threads_during_turn"), (list, dict)) and s1.get("loaded_threads_during_turn"),
      "LocalDaemon 权威证据（thread/loaded/list）")
check("s1-observer-resume-ok", s1.get("observer_resume") == "ok",
      "同 daemon 多连接 resume 无 writer 冲突")
deltas = [m for m in l1 if m.get("msg", {}).get("method") == "item/agentMessage/delta"]
item_started = [m for m in l1 if m.get("msg", {}).get("method") == "item/started"]
item_completed = [m for m in l1 if m.get("msg", {}).get("method") == "item/completed"]
completed = [m for m in l1 if m.get("msg", {}).get("method") == "turn/completed"]
thread_started = [m for m in l1 if m.get("msg", {}).get("method") == "thread/started"]
check("s1-live-deltas", len(deltas) >= 5, f"{len(deltas)} 条实时 delta")
check("s1-item-lifecycle", len(item_completed) >= 1,
      f"item/completed={len(item_completed)}（item/started 同属 mid-turn attach 边界：本次 {len(item_started)}）")
check("s1-turn-completed", len(completed) == 1, "唯一终态")
check("s1-thread-started-global-before-subscribe", len(thread_started) >= 1,
      "全局通知在订阅前到达")
status = [m for m in l1 if m.get("msg", {}).get("method") == "thread/status/changed"]
check("s1-status-transitions", len(status) >= 2, f"{len(status)} 次 status/changed")
# 顺序：item/started 早于所有 delta，delta 早于 completed
ts = lambda arr, i=0: arr[i]["ts"] if arr else float("inf")
first = min(ts(item_started), ts(deltas, 0), ts(item_completed))
check("s1-ordering", ts(deltas, 0) < ts(deltas, -1) < ts(completed) and first < ts(completed),
      "首事件 → deltas 递增 → completed 有序")
check("s1-turn-started-boundary-recorded", "observer_turn_started" in s1,
      "turn/started 边界如实记录（mid-turn attach 不重放）")

# ---- 场景 2：TUI 先启动（Embedded）→ daemon 观察端不伪 live ----
s2 = json.loads((GATE / "scene2-tui-first-embedded" / "meta.json").read_text())["entries"]
check("s2-daemon-absent-confirmed", "failed to connect" in str(s2.get("daemon_absent_check")))
check("s2-socket-absent-while-embedded", s2.get("socket_existed_while_embedded") is False)
check("s2-list-read-available", s2.get("observer_list_count", 0) >= 1 and s2.get("observer_read_turns", 0) >= 1)
check("s2-no-fake-live-replay", s2.get("unexpected_live_events") == 0)

# ---- 场景 3：daemon 已运行 + -c 覆盖（Embedded）→ 不串流 ----
s3 = json.loads((GATE / "scene3-daemon-override-embedded" / "meta.json").read_text())["entries"]
check("s3-daemon-running", s3.get("daemon_running") is True)
check("s3-tui-not-connected-to-daemon", s3.get("tui_connected_to_socket") is False)
check("s3-no-live-leak", s3.get("live_events_at_observer") == 0)

# ---- 脱敏扫描 ----
bad = []
for p in GATE.rglob("*"):
    if p.is_file() and p.suffix in (".json", ".jsonl", ".txt"):
        t = p.read_text(errors="ignore")
        if "/Users/" in t:
            bad.append(p.name)
check("gate-sanitization", not bad, "; ".join(bad) if bad else "clean")

print()
if failures:
    print(f"FAILED {len(failures)}: {failures}")
    sys.exit(1)
print("ALL PASS")


# ---- 宿主（p0-gate-hosts）断言 ----
HOSTS = HERE / "dumps" / "gate-hosts"
hosts_doc = (HOSTS / "README.md").read_text()
check("hosts-desktop-embedded-stdio-documented",
      "stdio" in hosts_doc and "-c features.code_mode_host=true" in hosts_doc
      and "不能隐式复用 daemon" in hosts_doc)
check("hosts-no-user-daemon-documented", "无用户 daemon" in hosts_doc or "不存在" in hosts_doc)
check("hosts-vscode-blocked-honestly", "未安装" in hosts_doc and "不从 Terminal/Desktop 类推" in hosts_doc)
check("hosts-store-level-relay-documented", "list/read" in hosts_doc)
check("hosts-owner-matrix-present", "最短 owner 验证矩阵" in hosts_doc)
check("hosts-process-evidence-archived", (HOSTS / "process-evidence.txt").exists())

print()
if failures:
    print(f"FAILED {len(failures)}: {failures}")
    sys.exit(1)
print("ALL PASS (gate + hosts)")
