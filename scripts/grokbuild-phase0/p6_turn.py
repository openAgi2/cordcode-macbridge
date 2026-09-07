#!/usr/bin/env python3
"""P6 真实 turn 取证探针（owner 2026-09-07 确认，P4P6-CONFIRM.md 最小集 1/2/4）。

隔离 GROK_HOME + 只读复制 auth.json；不触碰 ~/.grok 真实会话；不动 leader。

Turn 序列（源码依据 grok-build slash_exec.rs / turn.rs / cancel.rs）：
  A /hooks-list         —— 本地 builtin，零模型；期望 hostTurn user echo +
                           agent_message_chunk(hostTurn=true) + end_turn + 0 token
  B /hooks-add rel/path —— 本地失败（"Hook path must be absolute."），零模型
  C "Reply with exactly: ok" —— 1 次真实模型调用，完整流（echo/chunk/ext/usage）
  D 长 prompt + 立即 session/cancel —— cancelled 终态形状（≤1 次调用）

全部 stdout 行按序归档（请求/响应/通知交错保真），脱敏后落 samples/。
"""
import json, os, re, select, shutil, subprocess, sys, time

BIN = os.path.expanduser("~/.grok/bin/grok")
REDACT_PATTERNS = [
    (re.compile(r"meta4agi@gmail\.com"), "REDACT_EMAIL"),
    (re.compile(r"[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}"), "REDACT_UUID"),
]

class Child:
    def __init__(self, home, cwd):
        env = dict(os.environ)
        env["GROK_HOME"] = home
        auth_src = os.path.expanduser("~/.grok/auth.json")
        if os.path.isfile(auth_src):
            os.makedirs(home, exist_ok=True)
            dst = os.path.join(home, "auth.json")
            if not os.path.exists(dst):
                shutil.copy2(auth_src, dst)
        self.proc = subprocess.Popen(
            [BIN, "agent", "--no-leader", "stdio"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            cwd=cwd, env=env,
        )
        self.tape = []       # 按序全部行（dict）
        self.rid = 0

    def send(self, method, params, notification=False):
        frame = {"jsonrpc": "2.0", "method": method, "params": params}
        if not notification:
            self.rid += 1
            frame["id"] = self.rid
        self.proc.stdin.write((json.dumps(frame) + "\n").encode())
        self.proc.stdin.flush()
        return self.rid if not notification else None

    def _readline(self, timeout):
        r, _, _ = select.select([self.proc.stdout], [], [], timeout)
        if not r:
            return None
        line = self.proc.stdout.readline()
        if not line:
            return "EOF"
        try:
            return json.loads(line)
        except Exception:
            return None

    def wait_response(self, rid, timeout, also_collect=True):
        """等待 id==rid 的响应；沿途通知全部进 tape。返回响应或 {timeout:True}。"""
        deadline = time.time() + timeout
        while time.time() < deadline:
            msg = self._readline(min(1.0, deadline - time.time()))
            if msg == "EOF":
                return {"eof": True}
            if msg is None:
                continue
            self.tape.append(msg)
            if msg.get("id") == rid:
                return msg
        return {"timeout": True}

    def drain(self, seconds):
        deadline = time.time() + seconds
        while time.time() < deadline:
            msg = self._readline(0.3)
            if msg == "EOF":
                break
            if msg is not None:
                self.tape.append(msg)

    def close(self):
        try:
            self.proc.stdin.close()
        except Exception:
            pass
        try:
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()
            self.proc.wait()

def redact(obj):
    s = json.dumps(obj, ensure_ascii=False)
    for pat, repl in REDACT_PATTERNS:
        s = pat.sub(repl, s)
    return json.loads(s)

def turn(ch, label, text, timeout, cancel_after=None):
    """一个 turn：session/prompt →（可选立即 cancel 通知）→ 等响应 → drain 尾流。"""
    mark = len(ch.tape)
    rid = ch.send("session/prompt", {
        "sessionId": SID[0],
        "prompt": [{"type": "text", "text": text}],
    })
    if cancel_after is not None:
        time.sleep(cancel_after)
        ch.send("session/cancel", {"sessionId": SID[0]}, notification=True)
    resp = ch.wait_response(rid, timeout)
    ch.drain(1.5)
    return {"label": label, "request_text": text, "response": resp,
            "stream": ch.tape[mark:]}

def main():
    global SID
    home = sys.argv[1] if len(sys.argv) > 1 else "/tmp/grokbuild-probe-home-p6"
    cwd = sys.argv[2] if len(sys.argv) > 2 else os.path.expanduser("~")
    outdir = sys.argv[3] if len(sys.argv) > 3 else "/tmp/grokbuild-p6-out"
    os.makedirs(outdir, exist_ok=True)
    ch = Child(home, cwd)
    try:
        # 握手（与 phase0 探针同形状）
        rid = ch.send("initialize", {
            "protocolVersion": 1,
            "clientCapabilities": {"session": {"configOptions": {}}},
            "clientInfo": {"name": "cordcode-p6-probe", "title": "P6 Probe", "version": "0.1"},
        })
        init = ch.wait_response(rid, 30)
        assert isinstance(init.get("result"), dict) and init["result"].get("authMethods"), init
        rid = ch.send("authenticate", {"methodId": init["result"]["authMethods"][0]["id"]})
        auth = ch.wait_response(rid, 30)
        assert "error" not in auth, auth
        rid = ch.send("session/new", {"cwd": cwd, "mcpServers": []})
        new = ch.wait_response(rid, 30)
        SID[0] = new["result"]["sessionId"]
        ch.drain(2.0)
        print("handshake ok, session:", SID[0][:8], "…")

        samples = {}
        samples["A-hooks-list"] = turn(ch, "A", "/hooks-list", 30)
        print("A response stopReason:", samples["A-hooks-list"]["response"].get("result", {}).get("stopReason"))
        samples["B-hooks-add-invalid"] = turn(ch, "B", "/hooks-add relative/path", 30)
        print("B response stopReason:", samples["B-hooks-add-invalid"]["response"].get("result", {}).get("stopReason"))
        samples["C-real-prompt"] = turn(ch, "C", "Reply with exactly: ok", 90)
        print("C response:", json.dumps(samples["C-real-prompt"]["response"].get("result"), ensure_ascii=False)[:200])
        samples["D-cancel"] = turn(ch, "D", "Count slowly from 1 to 100, one number per line.", 60, cancel_after=2.0)
        print("D response stopReason:", samples["D-cancel"]["response"].get("result", {}).get("stopReason"))

        with open(os.path.join(outdir, "p6-raw-samples.json"), "w", encoding="utf-8") as f:
            json.dump({
                "meta": {"bin": BIN, "home": home, "cwd": cwd,
                         "grok_version": subprocess.run([BIN, "--version"], capture_output=True, text=True).stdout.strip(),
                         "captured_at": time.strftime("%Y-%m-%dT%H:%M:%S%z")},
                "handshake_tape": redact(ch.tape[:0]),  # 握手带凭证字段，不入档
                "turns": {k: redact(v) for k, v in samples.items()},
            }, f, ensure_ascii=False, indent=1)
        print("archived →", outdir)
    finally:
        ch.close()

SID = [""]

if __name__ == "__main__":
    main()
