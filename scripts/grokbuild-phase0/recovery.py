#!/usr/bin/env python3
"""P7 决定性实验：idle set plan → 持久化 → 关闭并 reap → 新 actor load → 恢复 effective mode。

零模型（无 user prompt）。隔离 GROK_HOME。判定依据：
- 新 actor 的 current_mode_update 通知 currentModeId
- 新 actor 重新 load 后 plan_mode.json 的 state
- 对比 1.0.16 源码 from_snapshot 的 Pending→Inactive 语义是否在 1.0.13 同样成立。
"""
import json, os, select, shutil, subprocess, sys, time

BIN = os.path.expanduser("~/.grok/bin/grok")

def spawn(home, cwd):
    env = dict(os.environ)
    env["GROK_HOME"] = home
    os.makedirs(home, exist_ok=True)
    auth = os.path.expanduser("~/.grok/auth.json")
    if os.path.isfile(auth) and not os.path.exists(os.path.join(home, "auth.json")):
        shutil.copy2(auth, os.path.join(home, "auth.json"))
    return subprocess.Popen([BIN, "agent", "--no-leader", "stdio"],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, cwd=cwd, env=env)

def call(proc, counter, method, params, timeout=20):
    counter[0] += 1
    rid = counter[0]
    proc.stdin.write((json.dumps({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}) + "\n").encode())
    proc.stdin.flush()
    deadline = time.time() + timeout
    while time.time() < deadline:
        r, _, _ = select.select([proc.stdout], [], [], 0.2)
        if not r:
            continue
        line = proc.stdout.readline()
        if not line:
            return {"eof": True}
        try:
            m = json.loads(line)
        except Exception:
            continue
        if m.get("id") == rid:
            return m
    return {"timeout": True}

def collect(proc, seconds=3.0):
    out = []
    deadline = time.time() + seconds
    while time.time() < deadline:
        r, _, _ = select.select([proc.stdout], [], [], 0.2)
        if not r:
            continue
        line = proc.stdout.readline()
        if not line:
            break
        try:
            m = json.loads(line)
        except Exception:
            continue
        if "id" not in m:
            out.append(m)
    return out

def setup(home, cwd):
    """启动新 actor 并 init/auth/new，返回 (proc, counter, sid)。"""
    proc = spawn(home, cwd)
    counter = [0]
    init = call(proc, counter, "initialize", {
        "protocolVersion": 1,
        "clientCapabilities": {"session": {"configOptions": {}}},
        "clientInfo": {"name": "cordcode-phase0-probe", "title": "Phase0 Probe", "version": "0.1"},
    })
    ams = (init.get("result") or {}).get("authMethods") or []
    if ams:
        auth = call(proc, counter, "authenticate", {"methodId": ams[0]["id"]}, timeout=30)
    else:
        auth = {"error": "no authMethods"}
    new = call(proc, counter, "session/new", {"cwd": cwd, "mcpServers": []}, timeout=30)
    sid = (new.get("result") or {}).get("sessionId", "")
    return proc, counter, sid

def main():
    home = sys.argv[1] if len(sys.argv) > 1 else "/tmp/grokbuild-probe-home-p7"
    cwd = sys.argv[2] if len(sys.argv) > 2 else os.path.expanduser("~")
    out = sys.argv[3] if len(sys.argv) > 3 else "/tmp/grokbuild-probe-out-p7"
    os.makedirs(out, exist_ok=True)
    shutil.rmtree(home, ignore_errors=True)

    # --- 阶段 1：idle set plan → 持久化确认 ---
    proc, counter, sid = setup(home, cwd)
    print("phase1 sid:", sid)
    setm = call(proc, counter, "session/set_mode", {"sessionId": sid, "modeId": "plan"}, timeout=20)
    with open(os.path.join(out, "10-set-mode.json"), "w", encoding="utf-8") as f:
        json.dump({"sid": sid, "response": setm}, f, ensure_ascii=False, indent=2)
    notifs1 = collect(proc, 3.0)
    with open(os.path.join(out, "11-notifs-after-set.jsonl"), "w", encoding="utf-8") as f:
        for n in notifs1:
            f.write(json.dumps(n, ensure_ascii=False) + "\n")
    # 等持久化
    sdir = os.path.join(home, "sessions", "%2F" + cwd.replace("/", "%2F"), sid) if False else None
    import urllib.parse
    sdir = os.path.join(home, "sessions", urllib.parse.quote(cwd, safe=""), sid)
    for _ in range(20):
        pm = os.path.join(sdir, "plan_mode.json")
        if os.path.exists(pm):
            break
        time.sleep(0.25)
    pm_before = None
    if os.path.exists(os.path.join(sdir, "plan_mode.json")):
        pm_before = json.load(open(os.path.join(sdir, "plan_mode.json"), encoding="utf-8"))
    with open(os.path.join(out, "12-plan-mode-before-close.json"), "w", encoding="utf-8") as f:
        json.dump({"sid": sid, "sdir": sdir, "plan_mode": pm_before}, f, ensure_ascii=False, indent=2)
    print("phase1 plan_mode.json:", pm_before)

    # --- 关闭并 reap ---
    proc.stdin.close()
    try:
        proc.wait(timeout=15)
    except subprocess.TimeoutExpired:
        proc.kill()
        proc.wait()
    print("phase1 exited:", proc.returncode)

    # --- 阶段 2：新 actor load 同一 session ---
    proc2, counter2, _ = setup(home, cwd)
    load = call(proc2, counter2, "session/load", {"sessionId": sid, "cwd": cwd, "mcpServers": []}, timeout=30)
    with open(os.path.join(out, "13-load-response.json"), "w", encoding="utf-8") as f:
        json.dump({"sid": sid, "response": load}, f, ensure_ascii=False, indent=2)
    notifs2 = collect(proc2, 5.0)
    with open(os.path.join(out, "14-notifs-after-load.jsonl"), "w", encoding="utf-8") as f:
        for n in notifs2:
            f.write(json.dumps(n, ensure_ascii=False) + "\n")
    cmus = [n for n in notifs2 if isinstance(n.get("params"), dict) and isinstance(n.get("params", {}).get("update"), dict) and n["params"]["update"].get("sessionUpdate") == "current_mode_update"]
    print("phase2 CMU count:", len(cmus))
    for c in cmus:
        print("  currentModeId:", c["params"]["update"].get("currentModeId"))
    # 重读 plan_mode.json
    pm_after = None
    if os.path.exists(os.path.join(sdir, "plan_mode.json")):
        pm_after = json.load(open(os.path.join(sdir, "plan_mode.json"), encoding="utf-8"))
    with open(os.path.join(out, "15-plan-mode-after-load.json"), "w", encoding="utf-8") as f:
        json.dump({"sid": sid, "plan_mode": pm_after, "cmu": [c["params"]["update"] for c in cmus]}, f, ensure_ascii=False, indent=2)
    print("phase2 plan_mode.json:", pm_after)
    proc2.stdin.close()
    try:
        proc2.wait(timeout=10)
    except subprocess.TimeoutExpired:
        proc2.kill()
        proc2.wait()
    print("DONE")

if __name__ == "__main__":
    main()
