#!/usr/bin/env python3
# Session-less catalog process probe: initialize -> authenticate -> _x.ai/commands/list {cwd}
# NO session/new, NO session/load, NO prompt — zero model calls. Isolated GROK_HOME.
import json, os, shutil, subprocess, sys, tempfile, time

HOME = tempfile.mkdtemp(prefix="grokbuild-cmdlist-probe-")
AUTH = os.path.expanduser("~/.grok/auth.json")
if os.path.isfile(AUTH):
    shutil.copy2(AUTH, os.path.join(HOME, "auth.json"))

CWD = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/Projects/Chat")
proc = subprocess.Popen(
    ["grok", "agent", "--no-leader", "stdio"],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
    cwd=CWD, env={**os.environ, "GROK_HOME": HOME}, text=True, bufsize=1)

counter = [0]
def call(method, params, timeout=30):
    counter[0] += 1
    req = {"jsonrpc": "2.0", "id": counter[0], "method": method, "params": params}
    t0 = time.monotonic()
    proc.stdin.write(json.dumps(req) + "\n"); proc.stdin.flush()
    while True:
        if time.monotonic() - t0 > timeout:
            return {"timeout": True}
        line = proc.stdout.readline()
        if not line:
            return {"eof": True}
        try:
            msg = json.loads(line)
        except json.JSONDecodeError:
            continue
        if msg.get("id") == counter[0]:
            msg["_elapsed_ms"] = round((time.monotonic() - t0) * 1000)
            return msg

out = {}
init = call("initialize", {"protocolVersion": 1, "clientInfo": {"name": "cordcode-probe", "title": "probe", "version": "1.0"}})
out["initialize"] = {"elapsed_ms": init.get("_elapsed_ms"), "authMethods": bool(init.get("result", {}).get("authMethods")),
                     "sessionList": bool(init.get("result", {}).get("agentCapabilities", {}).get("sessionCapabilities", {}).get("list", {}).get("enabled"))}
if init.get("result", {}).get("authMethods"):
    am = init["result"]["authMethods"][0]
    auth = call("authenticate", {"methodId": am["id"]})
    out["authenticate"] = {"elapsed_ms": auth.get("_elapsed_ms"), "error": bool(auth.get("error"))}
t0 = time.monotonic()
lst = call("_x.ai/commands/list", {"cwd": CWD})
res = lst.get("result") or {}
cmds = res.get("commands", [])
out["commands_list"] = {
    "elapsed_ms": lst.get("_elapsed_ms"), "count": len(cmds),
    "has_compact": any(c.get("name") == "compact" for c in cmds),
    "has_goal": any(c.get("name") == "goal" for c in cmds),
    "error": lst.get("error"),
}
samp = [c for c in cmds if c.get("name") in ("compact", "goal")]
# credentials never logged: only command descriptors
with open("/tmp/grokbuild-cmdlist-probe-out.json", "w") as f:
    json.dump({"cwd": CWD, "out": out, "admitted_samples": samp}, f, ensure_ascii=False, indent=1)
print(json.dumps(out, ensure_ascii=False))
proc.stdin.close()
try:
    proc.wait(timeout=5)
except subprocess.TimeoutExpired:
    proc.kill()
shutil.rmtree(HOME, ignore_errors=True)
