#!/usr/bin/env python3
"""Grok Build 1.0.13 零模型 ACP stdio 探针（Phase 0 证据采集）。

隔离 GROK_HOME（-h 指定，默认 /tmp/grokbuild-probe-home-<pid>），不触碰
~/.grok 用户会话。仅发送协议 RPC，不发送任何 user prompt（零模型）。

用法:
  probe.py --home <dir> --cwd <dir> --out <dir> [--stage all|init|new|list|mode]

阶段:
  init   初始化 + 保存 initialize 原始响应
  new    创建空会话 + 收集 handshake 通知（ACU 形状）
  list   catalog 进程发 _x.ai/commands/list {cwd}（P3）
  mode   set_mode plan → 收集 CMU → 检查持久化 → 关闭 reap → 重载（P7）
  不传 --stage 则依次执行全部并把原始样本写入 --out。
"""
import argparse, json, os, shutil, subprocess, sys, tempfile, time, signal

BIN = os.path.expanduser("~/.grok/bin/grok")

def rpc_id(counter):
    counter[0] += 1
    return counter[0]

def call_proc(proc, stdin_f, counter, method, params, timeout=15):
    rid = rpc_id(counter)
    req = {"jsonrpc": "2.0", "id": rid, "method": method, "params": params}
    stdin_f.write((json.dumps(req) + "\n").encode())
    stdin_f.flush()
    deadline = time.time() + timeout
    while time.time() < deadline:
        line = proc.stdout.readline()
        if not line:
            break
        try:
            msg = json.loads(line)
        except Exception:
            continue
        if msg.get("id") == rid:
            return msg
    return {"timeout": True, "method": method}

def collect_notifications(proc, seconds=2.0):
    """收集 stdout 上无 id 的 notification（不阻塞超过 seconds）。"""
    out = []
    deadline = time.time() + seconds
    while time.time() < deadline:
        try:
            proc.stdout._buffer  # noqa
        except Exception:
            pass
        import select
        r, _, _ = select.select([proc.stdout], [], [], 0.2)
        if not r:
            continue
        line = proc.stdout.readline()
        if not line:
            break
        try:
            msg = json.loads(line)
        except Exception:
            continue
        if "id" not in msg:
            out.append(msg)
    return out

def spawn(home, cwd, extra_env=None):
    env = dict(os.environ)
    env["GROK_HOME"] = home
    if extra_env:
        env.update(extra_env)
    # 认证：隔离 home 复制本机 auth.json（凭证不进证据；仅本机取证用）
    auth_src = os.path.expanduser("~/.grok/auth.json")
    if os.path.isfile(auth_src):
        os.makedirs(home, exist_ok=True)
        dst = os.path.join(home, "auth.json")
        if not os.path.exists(dst):
            shutil.copy2(auth_src, dst)
    proc = subprocess.Popen(
        [BIN, "agent", "--no-leader", "stdio"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        cwd=cwd, env=env,
    )
    return proc

def initialize(proc, stdin_f, counter, out_path):
    msg = call_proc(proc, stdin_f, counter, "initialize", {
        "protocolVersion": 1,
        "clientCapabilities": {"session": {"configOptions": {}}},
        "clientInfo": {"name": "cordcode-phase0-probe", "title": "Phase0 Probe", "version": "0.1"},
    })
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump({"request": {"method": "initialize", "params": {
            "protocolVersion": 1,
            "clientCapabilities": {"session": {"configOptions": {}}},
            "clientInfo": {"name": "cordcode-phase0-probe", "title": "Phase0 Probe", "version": "0.1"}}},
            "response": msg}, f, ensure_ascii=False, indent=2)
    return msg

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--home", default=None)
    ap.add_argument("--cwd", default=os.path.expanduser("~"))
    ap.add_argument("--out", default=None)
    ap.add_argument("--stage", default=None)
    args = ap.parse_args()

    home = args.home or f"/tmp/grokbuild-probe-home-{os.getpid()}"
    outdir = args.out or f"/tmp/grokbuild-probe-out-{os.getpid()}"
    os.makedirs(outdir, exist_ok=True)
    # 隔离 home：确保是全新目录（测试会话），非用户 home
    if not os.path.isdir(home):
        os.makedirs(home, exist_ok=True)
    with open(os.path.join(outdir, "meta.json"), "w", encoding="utf-8") as f:
        json.dump({"bin": BIN, "home": home, "cwd": args.cwd, "stage": args.stage}, f, ensure_ascii=False, indent=2)

    stage = args.stage
    counter = [0]

    if stage in (None, "init", "new", "list", "mode"):
        proc = spawn(home, args.cwd)
        stdin_f = proc.stdin
        try:
            init = initialize(proc, stdin_f, counter, os.path.join(outdir, "01-initialize.json"))
            print("initialize:", json.dumps({k: init.get(k) for k in ("result", "error", "timeout")}, ensure_ascii=False)[:500])
            if stage == "init":
                return
            # authenticate（initialize 返回 authMethods 时）
            if isinstance(init.get("result"), dict) and init["result"].get("authMethods"):
                am = init["result"]["authMethods"][0]
                authmsg = call_proc(proc, stdin_f, counter, "authenticate", {"methodId": am["id"]}, timeout=30)
                with open(os.path.join(outdir, "02-auth.json"), "w", encoding="utf-8") as f:
                    json.dump({"request": {"method": "authenticate", "params": {"methodId": am["id"]}},
                               "response": authmsg}, f, ensure_ascii=False, indent=2)
                print("authenticate:", json.dumps({k: authmsg.get(k) for k in ("result", "error", "timeout")}, ensure_ascii=False)[:300])
            # session/new
            newmsg = call_proc(proc, stdin_f, counter, "session/new", {
                "cwd": args.cwd, "mcpServers": [],
                "_meta": {"modelState": None} if False else None,
            }, timeout=20)
            with open(os.path.join(outdir, "02-session-new.json"), "w", encoding="utf-8") as f:
                json.dump({"request": {"method": "session/new", "params": {"cwd": args.cwd, "mcpServers": []}},
                           "response": newmsg}, f, ensure_ascii=False, indent=2)
            print("session/new:", json.dumps({k: newmsg.get(k) for k in ("result", "error", "timeout")}, ensure_ascii=False)[:500])
            sid = ""
            if isinstance(newmsg.get("result"), dict):
                sid = newmsg["result"].get("sessionId", "")
            # handshake notifications (ACU)
            notifs = collect_notifications(proc, 2.5)
            with open(os.path.join(outdir, "03-handshake-notifications.jsonl"), "w", encoding="utf-8") as f:
                for n in notifs:
                    f.write(json.dumps(n, ensure_ascii=False) + "\n")
            print("handshake notifs:", len(notifs), [n.get("method", "?") for n in notifs[:8]])
            if stage == "new":
                return
            # P3 commands/list on same proc? 目录 catalog 是单例进程；此处先在同一进程验证 method 存在性
            listmsg = call_proc(proc, stdin_f, counter, "_x.ai/commands/list", {"cwd": args.cwd}, timeout=30)
            with open(os.path.join(outdir, "04-commands-list.json"), "w", encoding="utf-8") as f:
                json.dump({"request": {"method": "_x.ai/commands/list", "params": {"cwd": args.cwd}},
                           "response": listmsg}, f, ensure_ascii=False, indent=2)
            print("commands/list:", json.dumps({k: listmsg.get(k) for k in ("result", "error", "timeout")}, ensure_ascii=False)[:300])
            if stage == "list":
                return
            # P7: set_mode plan
            modestart = call_proc(proc, stdin_f, counter, "session/set_mode", {"sessionId": sid, "modeId": "plan"}, timeout=20)
            with open(os.path.join(outdir, "05-set-mode-plan.json"), "w", encoding="utf-8") as f:
                json.dump({"request": {"method": "session/set_mode", "params": {"modeId": "plan"}},
                           "response": modestart}, f, ensure_ascii=False, indent=2)
            print("set_mode plan:", json.dumps({k: modestart.get(k) for k in ("result", "error", "timeout")}, ensure_ascii=False)[:300])
            notifs2 = collect_notifications(proc, 3.0)
            with open(os.path.join(outdir, "06-mode-notifications.jsonl"), "w", encoding="utf-8") as f:
                for n in notifs2:
                    f.write(json.dumps(n, ensure_ascii=False) + "\n")
            print("mode notifs:", len(notifs2), [n.get("method", "?") for n in notifs2[:8]])
            # 等持久化：轮询 home 下 session 目录 plan 状态文件
            time.sleep(1.0)
            snap_files = []
            for root, dirs, files in os.walk(home):
                for fn in files:
                    if "plan" in fn.lower() or "mode" in fn.lower() or fn.endswith(".jsonl"):
                        snap_files.append(os.path.join(root, fn))
            print("persisted files:", snap_files[:10])
            with open(os.path.join(outdir, "07-persisted-files.txt"), "w", encoding="utf-8") as f:
                f.write("\n".join(snap_files) + "\n")
            if stage == "mode":
                return
        finally:
            try:
                stdin_f.close()
            except Exception:
                pass
            try:
                proc.wait(timeout=10)
            except Exception:
                proc.kill()

if __name__ == "__main__":
    main()
