#!/usr/bin/env python3
"""Phase 0 §12 样本采集器（官方二进制真实 wire 样本）。

边界：
- 与 app-server 的全部帧均来自真实官方二进制 codex-cli 0.149.0-alpha.4（stdio transport）。
- 模型上游为本地 mock Responses provider（mock_provider.py），只控制脚本，不伪造 app-server 行为。
- 隔离 CODEX_HOME + 合成 workspace；内容全部为 MOCK:* 合成标记；输出前统一脱敏路径。

用法：python3 collect_samples.py [--only group1,group2]
产物：scripts/codex-web-phase0/dumps/<group>/{raw.jsonl,meta.json}
"""
import argparse
import json
import os
import pathlib
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request

HERE = pathlib.Path(__file__).resolve().parent
DUMPS = HERE / "dumps"
CODEX = "/Applications/ChatGPT.app/Contents/Resources/codex"
CLI_VERSION = "codex-cli 0.149.0-alpha.4"

SANITIZE = []  # (pattern, replacement) 运行时填充
SANITIZE_HOME = ""


class JsonRpcStdio:
    """newline-delimited JSON-RPC client for `codex app-server` stdio transport."""

    def __init__(self, codex_home, workspace, log_path, experimental=False):
        self.proc = None
        self.next_id = 1
        self.pending = {}
        self.notifications = []
        self.server_requests = []
        self.log_f = open(log_path, "a", encoding="utf-8")
        self.experimental = experimental
        env = dict(os.environ)
        env["CODEX_HOME"] = str(codex_home)
        env.pop("OPENAI_API_KEY", None)
        self.proc = subprocess.Popen(
            [CODEX, "app-server"],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            cwd=str(workspace), env=env, text=True, bufsize=1,
        )
        threading.Thread(target=self._read_loop, daemon=True).start()
        threading.Thread(target=self._drain_stderr, daemon=True).start()

    def _drain_stderr(self):
        try:
            for line in self.proc.stderr:
                self._log({"dir": "stderr", "line": line.rstrip()[:500]})
        except Exception:
            pass

    def _log(self, obj):
        obj["ts"] = round(time.time(), 3)
        self.log_f.write(json.dumps(obj, ensure_ascii=False) + "\n")
        self.log_f.flush()

    def _read_loop(self):
        try:
            for line in self.proc.stdout:
                line = line.strip()
                if not line:
                    continue
                try:
                    msg = json.loads(line)
                except Exception:
                    self._log({"dir": "server", "raw": line[:400]})
                    continue
                self._log({"dir": "server", "msg": msg})
                if "id" in msg and ("result" in msg or "error" in msg):
                    fut = self.pending.get(msg["id"])
                    if fut:
                        fut["msg"] = msg
                        fut["event"].set()
                elif "method" in msg and "id" in msg:
                    self.server_requests.append(msg)
                elif "method" in msg:
                    self.notifications.append(msg)
        except Exception:
            pass

    def send(self, obj):
        self._log({"dir": "client", "msg": obj})
        self.proc.stdin.write(json.dumps(obj, ensure_ascii=False) + "\n")
        self.proc.stdin.flush()

    def request(self, method, params=None, timeout=90):
        rid = self.next_id
        self.next_id += 1
        fut = {"event": threading.Event(), "msg": None}
        self.pending[rid] = fut
        req = {"jsonrpc": "2.0", "id": rid, "method": method}
        if params is not None:
            req["params"] = params
        self.send(req)
        if not fut["event"].wait(timeout):
            raise TimeoutError(f"request {method} timed out")
        del self.pending[rid]
        return fut["msg"]

    def notify(self, method, params=None):
        n = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            n["params"] = params
        self.send(n)

    def respond(self, req_id, result):
        self.send({"jsonrpc": "2.0", "id": req_id, "result": result})

    def wait_notification(self, method, timeout=90, pred=None):
        deadline = time.time() + timeout
        idx = 0
        while time.time() < deadline:
            while idx < len(self.notifications):
                n = self.notifications[idx]
                idx += 1
                if n.get("method") == method and (pred is None or pred(n)):
                    return n
            time.sleep(0.05)
        return None

    def wait_server_request(self, method, timeout=90, only_new=True):
        """only_new=True 时只接受本次调用之后新到达的 server request（幂等高水位扫描）。"""
        deadline = time.time() + timeout
        idx = len(self.server_requests) if only_new else 0
        while time.time() < deadline:
            while idx < len(self.server_requests):
                r = self.server_requests[idx]
                idx += 1
                if r.get("method") == method:
                    return r
            time.sleep(0.05)
        return None

    def initialize(self, name="codex-web-phase0", version="0.0.1"):
        caps = {"fs": {"readTextFile": False, "writeTextFile": False}}
        if self.experimental:
            caps["experimentalApi"] = True
        res = self.request("initialize", {"clientInfo": {"name": name, "version": version, "title": "phase0 harness"}, "capabilities": caps})
        self.notify("initialized")
        return res

    def close(self):
        try:
            self.proc.stdin.close()
        except Exception:
            pass
        try:
            self.proc.wait(timeout=10)
        except Exception:
            self.proc.kill()
        self.log_f.close()


def sanitize_text(s):
    for pat, rep in SANITIZE:
        s = s.replace(pat, rep)
    return s


def sanitize_obj(obj):
    return json.loads(sanitize_text(json.dumps(obj, ensure_ascii=False)))


def dump(group, raw_path):
    out = DUMPS / group
    out.mkdir(parents=True, exist_ok=True)
    with open(out / "raw.jsonl", "w", encoding="utf-8") as f:
        for line in open(raw_path, encoding="utf-8"):
            try:
                f.write(json.dumps(sanitize_obj(json.loads(line)), ensure_ascii=False) + "\n")
            except Exception:
                continue
    return out


def write_meta(group, entries):
    out = DUMPS / group
    meta = {
        "cli_version": CLI_VERSION,
        "transport": "stdio (newline-delimited JSON-RPC)",
        "collected_at": time.strftime("%Y-%m-%dT%H:%M:%S%z"),
        "model_upstream": "local mock Responses provider (mock_provider.py) — 只控制上游脚本；app-server 全部行为为真实官方二进制",
        "sanitization": "隔离 CODEX_HOME/workspace 路径替换为 $CODEX_HOME/$WORKSPACE；内容全为 MOCK:* 合成文本",
        "entries": entries,
    }
    meta = sanitize_obj(meta)
    (out / "meta.json").write_text(json.dumps(meta, ensure_ascii=False, indent=2))


def start_provider():
    proc = subprocess.Popen([sys.executable, str(HERE / "mock_provider.py")],
                            stdout=subprocess.PIPE, text=True)
    port = int(proc.stdout.readline().strip())
    for _ in range(50):
        try:
            urllib.request.urlopen(f"http://127.0.0.1:{port}/healthz", timeout=1)
            return proc, port
        except Exception:
            time.sleep(0.1)
    raise RuntimeError("mock provider failed to start")


def make_home(provider_port):
    home = pathlib.Path(tempfile.mkdtemp(prefix="codexweb-p0-home-"))
    ws = pathlib.Path(tempfile.mkdtemp(prefix="codexweb-p0-ws-"))
    (ws / "note.txt").write_text("phase0 synthetic workspace\n")
    cfg = f'''# phase0 isolated config
model = "mock-model"
model_provider = "mockpi"

[model_providers.mockpi]
name = "Mock Provider"
base_url = "http://127.0.0.1:{provider_port}/v1"
wire_api = "responses"

[features]
request_permissions_tool = true

[projects."{ws}"]
trust_level = "trusted"
'''
    (home / "config.toml").write_text(cfg)
    return home, ws


def turn_thread_id(n):
    p = n.get("params", {})
    return p.get("threadId") or p.get("turn", {}).get("threadId")


def wait_turn_completed(rpc, thread_id, turn_id=None, timeout=120):
    """turn_id 精确匹配（官方 turn/start 响应返回 turn.id；同 thread 多 turn 必须区分）。"""
    if turn_id:
        return rpc.wait_notification("turn/completed", timeout=timeout,
                                     pred=lambda n: (n.get("params", {}).get("turn") or {}).get("id") == turn_id)
    return rpc.wait_notification("turn/completed", timeout=timeout,
                                 pred=lambda n: turn_thread_id(n) == thread_id)


def last_turn_started(rpc, thread_id, timeout=30):
    """取该 thread 最新一条 turn/started 的 turn id（从末尾向前找最新匹配）。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        for n in reversed(rpc.notifications):
            if n.get("method") == "turn/started" and turn_thread_id(n) == thread_id:
                return (n.get("params", {}).get("turn") or {}).get("id")
        time.sleep(0.05)
    return None


def text_input(text):
    return [{"type": "text", "text": text}]


# ---------------- 采样流程 ----------------

def collect_initialize(home, ws):
    raw = str(DUMPS / "_tmp_initialize.jsonl")
    entries = {}
    rpc = JsonRpcStdio(home, ws, raw, experimental=False)
    try:
        res = rpc.initialize()
        entries["initialize_ok"] = "result" in res
        entries["server_info"] = {k: res.get("result", {}).get(k) for k in ("userAgent", "codexHome", "platformFamily", "platformOs")}
        rpc2 = JsonRpcStdio(home, ws, raw, experimental=True)
        try:
            res2 = rpc2.initialize()
            entries["experimental_init_ok"] = "result" in res2
        finally:
            rpc2.close()
        return entries
    finally:
        rpc.close()
        dump("initialize", raw)
        write_meta("initialize", entries | {"covers": "initialize 请求/响应（含 capabilities 实验开关两组）+ initialized 通知"})
        os.remove(raw)


def collect_catalog_and_turns(home, ws):
    raw = str(DUMPS / "_tmp_ct.jsonl")
    rpc = JsonRpcStdio(home, ws, raw)
    entries = {}
    try:
        rpc.initialize()
        empty = rpc.request("thread/list", {"limit": 50})
        entries["thread_list_empty"] = len(empty.get("result", {}).get("data") or [])
        t1 = rpc.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi"})
        tid1 = t1["result"]["thread"]["id"]
        entries["thread_start_effective"] = {k: t1["result"]["thread"].get(k) for k in ("model", "modelProvider", "cwd", "status")}
        tr = rpc.request("turn/start", {"threadId": tid1, "input": text_input("MOCK:STREAM catalog first turn")})
        turn1 = tr["result"]["turn"]["id"]
        entries["turn_start_response_status"] = tr["result"]["turn"].get("status")
        done = wait_turn_completed(rpc, tid1, turn1)
        entries["turn_completed"] = bool(done)
        deltas = [n for n in rpc.notifications if n.get("method") == "item/agentMessage/delta" and turn_thread_id(n) == tid1]
        entries["delta_count"] = len(deltas)
        entries["turn_lifecycle_methods"] = sorted({n["method"] for n in rpc.notifications if turn_thread_id(n) == tid1})
        extra_ids = []
        for i in range(3):
            tx = rpc.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi"})
            xid = tx["result"]["thread"]["id"]
            trx = rpc.request("turn/start", {"threadId": xid, "input": text_input(f"MOCK:STREAM filler turn {i}")})
            wait_turn_completed(rpc, xid, trx["result"]["turn"]["id"])
            extra_ids.append(xid)
        lst = rpc.request("thread/list", {"limit": 50})
        entries["thread_list_all"] = len(lst["result"].get("data") or [])
        page1 = rpc.request("thread/list", {"limit": 1})
        cursor = page1["result"].get("nextCursor")
        entries["cursor_pagination"] = cursor or "none"
        if cursor:
            page2 = rpc.request("thread/list", {"limit": 1, "cursor": cursor})
            entries["cursor_page2_count"] = len(page2["result"].get("data") or [])
        # 归档一个有内容的 thread
        rpc.request("thread/archive", {"threadId": extra_ids[0]})
        lst2 = rpc.request("thread/list", {"limit": 50, "archived": True})
        entries["archived_list"] = len(lst2["result"].get("data") or [])
        rd = rpc.request("thread/read", {"threadId": tid1, "includeTurns": True})
        entries["thread_read_turns"] = len(rd["result"].get("thread", {}).get("turns") or [])
        rpc.request("thread/name/set", {"threadId": tid1, "name": "phase0-mock-thread"})
        trr = rpc.request("turn/start", {"threadId": tid1, "input": text_input("MOCK:REASON history reasoning sample")})
        wait_turn_completed(rpc, tid1, trr["result"]["turn"]["id"])
        rd2 = rpc.request("thread/read", {"threadId": tid1, "includeTurns": True})
        turns = rd2["result"].get("thread", {}).get("turns") or []
        entries["turns_after_reason"] = len(turns)
        entries["turn2_itemsView"] = turns[-1].get("itemsView") if turns else None
        # 失败 turn
        tf = rpc.request("turn/start", {"threadId": tid1, "input": text_input("MOCK:FAIL failure sample")})
        turnf = tf["result"]["turn"]["id"]
        done3 = wait_turn_completed(rpc, tid1, turnf, timeout=60)
        entries["failed_turn_status"] = (done3 or {}).get("params", {}).get("turn", {}).get("status") or "NO_COMPLETED"
        # steer + interrupt 需要活跃 turn：快速发起后立即 steer
        ts = rpc.request("turn/start", {"threadId": tid1, "input": text_input("MOCK:STREAM steer target turn")})
        active_turn = ts["result"]["turn"]["id"]
        entries["steer_same_turn_id_queue_semantics"] = (active_turn == turnf)
        st = rpc.request("turn/steer", {"threadId": tid1, "expectedTurnId": active_turn, "input": text_input("MOCK:STREAM steer injected")})
        entries["steer_result"] = st.get("error") or st.get("result", "ok")
        wait_turn_completed(rpc, tid1, active_turn, timeout=120)
        # stale turnId 的 steer（用旧 turn id）→ 官方拒绝样本
        st2 = rpc.request("turn/steer", {"threadId": tid1, "expectedTurnId": turn1, "input": text_input("MOCK:STREAM stale steer")})
        entries["steer_stale_error"] = st2.get("error")
        # interrupt
        ti = rpc.request("turn/start", {"threadId": tid1, "input": text_input("MOCK:SLOW interrupt target")})
        at2 = ti["result"]["turn"]["id"]
        # turn/start 响应先于 active-turn 注册（真实窗口，本轮实测同毫秒 interrupt 报 no active turn）；
        # 等该 turn 首个 delta 到达再 interrupt。
        rpc.wait_notification("item/agentMessage/delta", timeout=30,
                              pred=lambda n: (n.get("params", {}).get("turnId") == at2))
        iv = rpc.request("turn/interrupt", {"threadId": tid1, "turnId": at2})
        entries["interrupt"] = "result" in iv or iv.get("error")
        c = wait_turn_completed(rpc, tid1, at2, timeout=120)
        entries["interrupt_turn_status"] = (c or {}).get("params", {}).get("turn", {}).get("status")
        return entries
    finally:
        rpc.close()
        dump("catalog", raw)
        write_meta("catalog", entries | {"covers": "空/多 thread 列表、archive 列表、cursor 分页、thread/read(includeTurns)、rename、turn 生命周期(started/deltas/completed/failed)、reasoning turn、steer、interrupt"})
        os.remove(raw)


def collect_interaction(home, ws):
    raw = str(DUMPS / "_tmp_interaction.jsonl")
    rpc = JsonRpcStdio(home, ws, raw)
    entries = {}
    try:
        rpc.initialize()
        t = rpc.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi",
                                         "approvalPolicy": "untrusted"})
        tid = t["result"]["thread"]["id"]
        ta = rpc.request("turn/start", {"threadId": tid, "input": text_input("MOCK:CMD:echo phase0-approval")})
        turn_a = ta["result"]["turn"]["id"]
        req = rpc.wait_server_request("item/commandExecution/requestApproval", timeout=90, only_new=True)
        if req:
            entries["command_approval_params_keys"] = sorted(req["params"].keys())
            entries["availableDecisions_present"] = "availableDecisions" in req["params"]
            entries["additionalPermissions_present"] = "additionalPermissions" in req["params"]
            rpc.respond(req["id"], {"decision": "accept"})
            c = wait_turn_completed(rpc, tid, turn_a, timeout=120)
            entries["turn_completed_after_accept"] = bool(c)
        else:
            entries["command_approval"] = "NOT_RECEIVED"
        # requestUserInput（单题）
        ta1 = rpc.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi",
                                           "config": {"features.default_mode_request_user_input": True}})
        tidq = ta1["result"]["thread"]["id"]
        tq1 = rpc.request("turn/start", {"threadId": tidq, "input": text_input("MOCK:ASK1 single question")})
        q1 = rpc.wait_server_request("item/tool/requestUserInput", timeout=90)
        if q1:
            entries["ask1_questions"] = len(q1["params"].get("questions") or [])
            entries["ask1_isBlocking"] = q1["params"].get("isBlocking")
            answers1 = {}
            for q in q1["params"]["questions"]:
                opts = q.get("options") or [{"label": "Yes"}]
                answers1[q["id"]] = {"answers": [opts[0]["label"]]}
            rpc.respond(q1["id"], {"answers": answers1})
            c = rpc.wait_notification("serverRequest/resolved", timeout=60)
            entries["ask1_resolved"] = bool(c)
            wait_turn_completed(rpc, tidq, tq1["result"]["turn"]["id"], timeout=120)
        # requestUserInput（多题）
        ta3 = rpc.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi",
                                           "config": {"features.default_mode_request_user_input": True}})
        tidq3 = ta3["result"]["thread"]["id"]
        tq3 = rpc.request("turn/start", {"threadId": tidq3, "input": text_input("MOCK:ASK3 multi question")})
        q3 = rpc.wait_server_request("item/tool/requestUserInput", timeout=90)
        if q3:
            entries["ask3_questions"] = len(q3["params"].get("questions") or [])
            answers3 = {}
            for q in q3["params"]["questions"]:
                if q.get("options"):
                    answers3[q["id"]] = {"answers": [q["options"][0]["label"]]}
                else:
                    answers3[q["id"]] = {"answers": ["free note answer"]}
            rpc.respond(q3["id"], {"answers": answers3})
            wait_turn_completed(rpc, tidq3, tq3["result"]["turn"]["id"], timeout=120)
            entries["ask3_turn_completed"] = True
        # fileChange approval（read-only sandbox + apply_patch）
        tfp = rpc.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi",
                                           "sandbox": "read-only", "approvalPolicy": "on-request"})
        tidf = tfp["result"]["thread"]["id"]
        tfp2 = rpc.request("turn/start", {"threadId": tidf, "input": text_input("MOCK:PATCH write file")})
        fq = rpc.wait_server_request("item/fileChange/requestApproval", timeout=60)
        if fq:
            entries["file_approval_keys"] = sorted(fq["params"].keys())
            rpc.respond(fq["id"], {"decision": "accept"})
            wait_turn_completed(rpc, tidf, tfp2["result"]["turn"]["id"], timeout=120)
        else:
            entries["file_approval"] = "NOT_RECEIVED"
        # permission approval（内置 request_permissions 工具）
        tnp = rpc.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi"})
        tidn = tnp["result"]["thread"]["id"]
        tnp2 = rpc.request("turn/start", {"threadId": tidn, "input": text_input("MOCK:PERM ask permissions")})
        pq = rpc.wait_server_request("item/permissions/requestApproval", timeout=60)
        if pq:
            entries["permission_approval_keys"] = sorted(pq["params"].keys())
            entries["permission_request_profile"] = json.dumps(pq["params"].get("permissions"))[:200]
            rpc.respond(pq["id"], {"permissions": {}, "scope": "session"})
            wait_turn_completed(rpc, tidn, tnp2["result"]["turn"]["id"], timeout=120)
        else:
            entries["permission_approval"] = "NOT_RECEIVED"
        t2 = rpc.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi",
                                          "approvalPolicy": "untrusted"})
        tid2 = t2["result"]["thread"]["id"]
        tb = rpc.request("turn/start", {"threadId": tid2, "input": text_input("MOCK:CMD:echo phase0-deny")})
        turn_b = tb["result"]["turn"]["id"]
        req2 = rpc.wait_server_request("item/commandExecution/requestApproval", timeout=90, only_new=True)
        if req2:
            rpc.respond(req2["id"], {"decision": "cancel"})
            c2 = wait_turn_completed(rpc, tid2, turn_b, timeout=120)
            entries["deny_branch_status"] = (c2 or {}).get("params", {}).get("turn", {}).get("status")
        return entries
    finally:
        rpc.close()
        dump("interaction", raw)
        write_meta("interaction", entries | {"covers": "command approval accept/deny（on-request）；availableDecisions 物理存在性 = §7.3 分歧裁决证据"})
        os.remove(raw)


def collect_models_config(home, ws):
    raw = str(DUMPS / "_tmp_models.jsonl")
    rpc = JsonRpcStdio(home, ws, raw)
    entries = {}
    try:
        rpc.initialize()
        ml = rpc.request("model/list", {})
        data = ml["result"].get("data") or []
        entries["model_list_count"] = len(data)
        if data:
            entries["model_fields"] = sorted(data[0].keys())
            entries["model_has_provider_field"] = any("provider" in k.lower() for k in data[0].keys())
        cr = rpc.request("config/read", {})
        cfg = cr["result"].get("config", {})
        entries["config_model_provider"] = cfg.get("model_provider")
        entries["config_additional_keys"] = sorted((cfg.get("additional") or {}).keys())
        has_mp = "model_providers" in (cfg.get("additional") or {})
        entries["config_has_model_providers"] = has_mp
        if has_mp:
            entries["model_providers_composition"] = sorted((cfg["additional"]["model_providers"] or {}).keys())
        pp = rpc.request("permissionProfile/list", {})
        entries["permission_profiles_ok"] = "result" in pp
        return entries
    finally:
        rpc.close()
        dump("models-config", raw)
        write_meta("models-config", entries | {"covers": "model/list（custom provider 目录）/ config/read typed model_provider + additional.model_providers 存在性与组成 / permissionProfile/list"})
        os.remove(raw)


def collect_ownership(home, ws):
    raw = str(DUMPS / "_tmp_ownership.jsonl")
    entries = {}
    rpc_a = JsonRpcStdio(home, ws, raw)
    try:
        rpc_a.initialize()
        t = rpc_a.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi"})
        tid = t["result"]["thread"]["id"]
        tw = rpc_a.request("turn/start", {"threadId": tid, "input": text_input("MOCK:STREAM ownership writer turn")})
        writer_turn = tw["result"]["turn"]["id"]
        rpc_b = JsonRpcStdio(home, ws, raw)
        try:
            rpc_b.initialize()
            res = rpc_b.request("thread/resume", {"threadId": tid})
            entries["second_resume"] = res.get("error") or "ok"
            rd = rpc_b.request("thread/read", {"threadId": tid})
            entries["readonly_during_writer"] = "result" in rd
            ar = rpc_b.request("thread/archive", {"threadId": tid})
            entries["archive_conflict"] = ar.get("error") or "ok"
        finally:
            rpc_b.close()
        un = rpc_a.request("thread/unsubscribe", {"threadId": tid})
        entries["unsubscribe_status"] = (un.get("result") or {}).get("status") or un.get("error")
        wait_turn_completed(rpc_a, tid, writer_turn, timeout=120)
        return entries
    finally:
        rpc_a.close()
        dump("ownership", raw)
        write_meta("ownership", entries | {"covers": "写者活跃时第二连接 resume/archive 冲突与只读可用性 / unsubscribe 返回语义"})
        os.remove(raw)


def collect_reconnect(home, ws):
    raw = str(DUMPS / "_tmp_reconnect.jsonl")
    entries = {}
    rpc = JsonRpcStdio(home, ws, raw)
    tid = None
    try:
        rpc.initialize()
        t = rpc.request("thread/start", {"cwd": str(ws), "model": "mock-model", "modelProvider": "mockpi"})
        tid = t["result"]["thread"]["id"]
        tr = rpc.request("turn/start", {"threadId": tid, "input": text_input("MOCK:STREAM reconnect first turn")})
        wait_turn_completed(rpc, tid, tr["result"]["turn"]["id"])
    finally:
        rpc.close()
    time.sleep(1)
    rpc2 = JsonRpcStdio(home, ws, raw)
    try:
        rpc2.initialize()
        rd = rpc2.request("thread/read", {"threadId": tid, "includeTurns": True})
        entries["read_after_reconnect_turns"] = len(rd["result"].get("thread", {}).get("turns") or [])
        res = rpc2.request("thread/resume", {"threadId": tid})
        entries["resume_after_reconnect"] = res.get("error") or "ok"
        return entries
    finally:
        rpc2.close()
        dump("reconnect", raw)
        write_meta("reconnect", entries | {"covers": "连接关闭 → 新连接 initialize → thread/read 冷校准 → resume"})
        os.remove(raw)


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def collect_lifecycle():
    raw = str(DUMPS / "_tmp_lifecycle.jsonl")
    entries = {}
    # daemon 子命令需要 installer 管理的 standalone 副本 + 短路径（Unix socket SUN_LEN 限制）
    home = pathlib.Path("/tmp/cw-p0-lifecycle-home")
    shutil.rmtree(home, ignore_errors=True)
    home.mkdir(parents=True)
    pkg = home / "packages" / "standalone" / "current"
    pkg.mkdir(parents=True)
    os.symlink(CODEX, pkg / "codex")
    env = dict(os.environ)
    env["CODEX_HOME"] = str(home)
    p = subprocess.run([CODEX, "app-server", "daemon", "version"], capture_output=True, text=True, env=env, timeout=30)
    entries["daemon_version_absent"] = {"stdout": p.stdout.strip()[:400], "stderr": p.stderr.strip()[:300], "rc": p.returncode}
    srv_port = free_port()
    ws_proc = subprocess.Popen([CODEX, "app-server", "--listen", f"ws://127.0.0.1:{srv_port}"],
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)
    try:
        entries["ws_healthz"] = None
        entries["ws_readyz"] = None
        for _ in range(60):
            time.sleep(0.5)
            try:
                entries["ws_healthz"] = urllib.request.urlopen(f"http://127.0.0.1:{srv_port}/healthz", timeout=2).status
                entries["ws_readyz"] = urllib.request.urlopen(f"http://127.0.0.1:{srv_port}/readyz", timeout=2).status
                break
            except Exception:
                continue
        entries["ws_endpoint"] = f"ws://127.0.0.1:{srv_port} (managed-loopback-ws)"
    finally:
        ws_proc.terminate()
        try:
            ws_proc.wait(timeout=10)
        except Exception:
            ws_proc.kill()
    p = subprocess.run([CODEX, "app-server", "daemon", "start"], capture_output=True, text=True, env=env, timeout=180)
    entries["daemon_start"] = {"stdout": p.stdout.strip()[:600], "stderr": p.stderr.strip()[:300], "rc": p.returncode}
    p = subprocess.run([CODEX, "app-server", "daemon", "version"], capture_output=True, text=True, env=env, timeout=30)
    entries["daemon_version_running"] = {"stdout": p.stdout.strip()[:600], "rc": p.returncode}
    p = subprocess.run([CODEX, "app-server", "daemon", "stop"], capture_output=True, text=True, env=env, timeout=60)
    entries["daemon_stop"] = {"rc": p.returncode, "stdout": p.stdout.strip()[:300]}
    with open(raw, "w", encoding="utf-8") as f:
        f.write(json.dumps({"entry": "lifecycle summary (subprocess captured)", "entries": sanitize_obj(entries)}, ensure_ascii=False) + "\n")
    dump("lifecycle", raw)
    shutil.rmtree(home, ignore_errors=True)
    write_meta("lifecycle", {k: "captured" for k in entries} | {"covers": "daemon absent/running/start/version/stop + managed WS healthz/readyz（短路径隔离 CODEX_HOME + standalone 符号链种子，daemon 用后即停）",
                 "prereq_facts": "daemon start 需要 $CODEX_HOME/packages/standalone/current/codex（installer 管理副本）；control socket 路径超 macOS SUN_LEN(104) 会报 'path must be shorter than SUN_LEN'（首轮长临时目录已实录该错误，见 git 历史 7e3d77e 前版 dumps）"})
    os.remove(raw)
    return entries


def main():
    global SANITIZE_HOME
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", default="")
    args = ap.parse_args()
    only = set(x for x in args.only.split(",") if x)
    DUMPS.mkdir(parents=True, exist_ok=True)

    provider_proc, port = start_provider()
    home, ws = make_home(port)
    SANITIZE_HOME = str(home)
    SANITIZE.extend([(str(home), "$CODEX_HOME"), (str(ws), "$WORKSPACE"),
                     ("/private" + str(home), "$CODEX_HOME"), ("/private" + str(ws), "$WORKSPACE")])
    try:
        if not only or "initialize" in only:
            print("== initialize ==", json.dumps(collect_initialize(home, ws), ensure_ascii=False)[:200])
        if not only or "lifecycle" in only:
            print("== lifecycle ==", json.dumps(collect_lifecycle(), ensure_ascii=False)[:200])
        if not only or "catalog" in only:
            print("== catalog/turns ==", json.dumps(collect_catalog_and_turns(home, ws), ensure_ascii=False)[:300])
        if not only or "interaction" in only:
            print("== interaction ==", json.dumps(collect_interaction(home, ws), ensure_ascii=False)[:300])
        if not only or "models" in only or "config" in only:
            print("== models/config ==", json.dumps(collect_models_config(home, ws), ensure_ascii=False)[:300])
        if not only or "ownership" in only:
            print("== ownership ==", json.dumps(collect_ownership(home, ws), ensure_ascii=False)[:300])
        if not only or "reconnect" in only:
            print("== reconnect ==", json.dumps(collect_reconnect(home, ws), ensure_ascii=False)[:200])
    finally:
        provider_proc.terminate()
        shutil.rmtree(home, ignore_errors=True)
        shutil.rmtree(ws, ignore_errors=True)
    print("DONE ->", DUMPS)


if __name__ == "__main__":
    main()
