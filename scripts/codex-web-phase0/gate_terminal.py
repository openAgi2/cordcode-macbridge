#!/usr/bin/env python3
"""Phase 0 核心 Gate：Terminal TUI 三场景受控实验（设计 §8.2 前三行）。

场景 1（核心，必须 PASS）：daemon 已运行 + 默认启动配置 TUI → TUI 选 LocalDaemon，
   观察连接（经官方 proxy 连 control socket）对同 thread 收到 turn/started、多 delta、
   turn/completed。
场景 2（隔离边界）：daemon 未运行，TUI 先启动（Embedded）完成 turn；之后 daemon+观察连接
   不得伪称收到该 live turn（无重放），但 list/read 可用。
场景 3（隔离边界）：daemon 已运行，TUI 带 `-c` 覆盖启动（Embedded）→ 观察连接不串入该
   turn 的 live 流。

证据：每场景目录含 observer.jsonl（观察连接全帧）、tui-screen.txt（PTY 输出尾部）、
process-evidence.txt（ps/lsof/daemon version）、meta.json。
TUI 消息用 MOCK:SLOW（~16s 流式），给观察连接足够的 attach 窗口。
"""
import json
import os
import pathlib
import pty
import re
import select
import socket
import shutil
import subprocess
import sys
import tempfile
import threading
import time

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from collect_samples import (CODEX, CLI_VERSION, JsonRpcStdio, start_provider,  # noqa: E402
                             sanitize_obj, wait_turn_completed)

DUMPS = HERE / "dumps" / "gate-terminal"
SANI = []


class TuiSession:
    def __init__(self, home, ws, extra_args=None):
        self.home, self.ws = home, ws
        self.raw = bytearray()
        pid, fd = pty.fork()
        if pid > 0:
            import fcntl, struct, termios
            fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
            self.pid, self.fd = pid, fd
        else:
            os.environ["CODEX_HOME"] = str(home)
            os.environ["TERM"] = "xterm-256color"
            os.chdir(str(ws))
            os.execv(CODEX, [CODEX] + (extra_args or []))

    def pump(self, seconds):
        """读 TUI 输出并自动应答终端能力查询（OSC 10/11 颜色、DA1），否则 TUI 会卡在 splash。"""
        deadline = time.time() + seconds
        answered = set()
        while time.time() < deadline:
            r, _, _ = select.select([self.fd], [], [], 0.3)
            if r:
                try:
                    chunk = os.read(self.fd, 65536)
                    if not chunk:
                        break
                    self.raw.extend(chunk)
                    tail = bytes(self.raw[-400:])
                    if b"\x1b]10;?" in tail and "fg" not in answered:
                        answered.add("fg")
                        os.write(self.fd, b"\x1b]10;rgb:cccc/cccc/cccc\x1b\\")
                    if b"\x1b]11;?" in tail and "bg" not in answered:
                        answered.add("bg")
                        os.write(self.fd, b"\x1b]11;rgb:0000/0000/0000\x1b\\")
                    if b"\x1b[c" in tail and "da" not in answered:
                        answered.add("da")
                        os.write(self.fd, b"\x1b[?62;c")
                    if b"trust the contents" in tail.lower() and "trust" not in answered:
                        answered.add("trust")
                        time.sleep(0.5)
                        os.write(self.fd, b"\r")
                except OSError:
                    break

    def send(self, text):
        os.write(self.fd, text.encode())

    def wait_composer(self, timeout=120):
        """等待 composer 出现（首启含插件/marketplace 同步，固定 sleep 不可靠）。"""
        deadline = time.time() + timeout
        while time.time() < deadline:
            if b"Ask Codex" in bytes(self.raw):
                return True
            self.pump(1)
        return False

    def screen(self):
        t = self.raw.decode("utf-8", "replace")
        return re.sub(r"\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][A-B0]|\r", "", t)

    def close(self):
        try:
            os.kill(self.pid, 15)
            time.sleep(0.5)
            os.kill(self.pid, 9)
        except ProcessLookupError:
            pass
        try:
            os.waitpid(self.pid, 0)
        except ChildProcessError:
            pass
        try:
            os.close(self.fd)
        except OSError:
            pass


class WsUdsObserver:
    """观察连接：WebSocket over Unix socket 直连 daemon control socket。

    协议事实（Phase 0 实测 + 源码 unix_socket.rs:64 accept_async）：
    control socket 是 WS 升级而非裸 newline JSON；每个 JSON-RPC 消息一个 WS text 帧。
    """

    def __init__(self, home, ws, log_path):
        import base64
        self.next_id = 1
        self.pending = {}
        self.notifications = []
        self.server_requests = []
        self.log_f = open(log_path, "a", encoding="utf-8")
        self.experimental = False
        sock_path = pathlib.Path(home) / "app-server-control" / "app-server-control.sock"
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(None)
        self.sock.connect(str(sock_path))
        key = base64.b64encode(os.urandom(16)).decode()
        hs = ("GET / HTTP/1.1\r\nHost: codex-daemon\r\nUpgrade: websocket\r\n"
              "Connection: Upgrade\r\nSec-WebSocket-Key: " + key + "\r\n"
              "Sec-WebSocket-Version: 13\r\n\r\n")
        self.sock.sendall(hs.encode())
        resp = b""
        while b"\r\n\r\n" not in resp:
            chunk = self.sock.recv(4096)
            if not chunk:
                raise RuntimeError("WS handshake closed")
            resp += chunk
        if b"101" not in resp.split(b"\r\n")[0]:
            raise RuntimeError("WS handshake failed: " + resp[:120].decode("latin1"))
        import threading
        self.alive = True
        self.thread_started_hook = None  # (thread_id) -> None，由 reader 线程在收到 thread/started 时调用
        threading.Thread(target=self._read_loop, daemon=True).start()

    def _log(self, obj):
        obj["ts"] = round(time.time(), 3)
        self.log_f.write(json.dumps(obj, ensure_ascii=False) + "\n")
        self.log_f.flush()

    def _recv_frame(self):
        def read_exact(n):
            buf = b""
            while len(buf) < n:
                chunk = self.sock.recv(n - len(buf))
                if not chunk:
                    raise ConnectionError("ws closed")
                buf += chunk
            return buf
        hdr = read_exact(2)
        fin = hdr[0] & 0x80
        opcode = hdr[0] & 0x0F
        masked = hdr[1] & 0x80
        length = hdr[1] & 0x7F
        if length == 126:
            length = int.from_bytes(read_exact(2), "big")
        elif length == 127:
            length = int.from_bytes(read_exact(8), "big")
        mask = read_exact(4) if masked else None
        payload = read_exact(length)
        if mask:
            payload = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
        return fin, opcode, payload

    def _send_frame(self, payload, opcode=1):
        import os as _os
        mask = _os.urandom(4)
        masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
        length = len(payload)
        if length < 126:
            hdr = bytes([0x80 | opcode, 0x80 | length])
        elif length < 65536:
            hdr = bytes([0x80 | opcode, 0x80 | 126]) + length.to_bytes(2, "big")
        else:
            hdr = bytes([0x80 | opcode, 0x80 | 127]) + length.to_bytes(8, "big")
        self.sock.sendall(hdr + mask + masked)

    def _read_loop(self):
        buf = b""
        cur_op = None
        try:
            while self.alive:
                fin, opcode, payload = self._recv_frame()
                if opcode == 9:  # ping → pong
                    self._send_frame(payload, opcode=10)
                    continue
                if opcode == 8:
                    break
                if opcode in (1, 2, 0):
                    if opcode in (1, 2):
                        cur_op = opcode
                        buf = payload
                    else:
                        buf += payload
                    if fin:
                        text = buf.decode("utf-8", "replace")
                        buf = b""
                        try:
                            msg = json.loads(text)
                        except Exception:
                            self._log({"dir": "server", "raw": text[:300]})
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
                            if msg["method"] == "thread/started" and self.thread_started_hook:
                                try:
                                    nid = (msg.get("params", {}).get("thread") or {}).get("id")
                                    if nid:
                                        self.thread_started_hook(nid)
                                except Exception:
                                    pass
        except Exception as e:
            self._log({"dir": "error", "err": str(e)[:200]})

    def send(self, obj):
        self._log({"dir": "client", "msg": obj})
        self._send_frame(json.dumps(obj, ensure_ascii=False).encode())

    def request(self, method, params=None, timeout=60):
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

    def wait_notification(self, method, timeout=60, pred=None):
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

    def wait_server_request(self, method, timeout=60, only_new=True):
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

    def initialize(self, name="codex-web-observer", version="0.0.1", experimental=False):
        caps = {"experimentalApi": True} if experimental else {}
        res = self.request("initialize", {"clientInfo": {"name": name, "version": version}, "capabilities": caps})
        self.notify("initialized")
        return res

    def close(self):
        self.alive = False
        try:
            self.sock.close()
        except Exception:
            pass
        self.log_f.close()


def make_home(provider_port, ws, seed_standalone=False, short=None):
    if short:
        home = pathlib.Path(short)
        shutil.rmtree(home, ignore_errors=True)
        home.mkdir(parents=True)
    else:
        home = pathlib.Path(tempfile.mkdtemp(prefix="gate-home-"))
    if seed_standalone:
        # daemon start 需要 installer 管理的 standalone 副本；以符号链接种子（官方 daemon 机制不变）
        pkg = home / "packages" / "standalone" / "current"
        pkg.mkdir(parents=True)
        os.symlink(CODEX, pkg / "codex")
    (home / "config.toml").write_text(f'''model = "mock-model"
model_provider = "mockpi"

[model_providers.mockpi]
name = "Mock Provider"
base_url = "http://127.0.0.1:{provider_port}/v1"
wire_api = "responses"

[projects."{ws}"]
trust_level = "trusted"

[projects."/private{ws}"]
trust_level = "trusted"
''')
    return home


def sh(cmd, env):
    return subprocess.run(cmd, shell=True, capture_output=True, text=True, env=env, timeout=30)


def dump_scene(scene, entries, observer_raw, tui_screen, proc_ev, meta_extra):
    out = DUMPS / scene
    out.mkdir(parents=True, exist_ok=True)
    with open(out / "observer.jsonl", "w", encoding="utf-8") as f:
        for line in open(observer_raw, encoding="utf-8"):
            try:
                f.write(json.dumps(sanitize_obj(json.loads(line)), ensure_ascii=False) + "\n")
            except Exception:
                continue
    (out / "tui-screen.txt").write_text(tui_screen[-8000:])
    (out / "process-evidence.txt").write_text(proc_ev)
    meta = {"cli_version": CLI_VERSION, "entries": entries} | meta_extra
    (out / "meta.json").write_text(json.dumps(sanitize_obj(meta), ensure_ascii=False, indent=2))


def find_new_thread(observer, before_ids, timeout=30):
    """权威信号优先：thread/loaded/list（daemon 进程驻留）；退回 thread/list（store）。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            loaded = observer.request("thread/loaded/list", {}, timeout=10)
            lt = (loaded.get("result") or {}).get("threadIds") or []
            newl = [i for i in lt if i not in before_ids]
            if newl:
                return newl[0]
        except Exception:
            pass
        res = observer.request("thread/list", {"limit": 50}, timeout=15)
        ids = [t.get("id") for t in (res.get("result", {}).get("data") or [])]
        new = [i for i in ids if i not in before_ids]
        if new:
            return new[0]
        time.sleep(0.05)
    return None


def scene1(provider_port):
    """daemon 已运行 + 默认 TUI。期望：TUI→LocalDaemon；观察连接收到完整生命周期。"""
    ws = pathlib.Path("/tmp/cw-gate-ws-s1"); shutil.rmtree(ws, ignore_errors=True); ws.mkdir()
    home = make_home(provider_port, ws, seed_standalone=True, short="/tmp/cw-gate-home-s1")
    env = dict(os.environ, CODEX_HOME=str(home))
    raw = str(DUMPS / "_s1_observer.jsonl")
    DUMPS.mkdir(parents=True, exist_ok=True)
    entries = {}
    # 1. daemon start
    p = subprocess.run([CODEX, "app-server", "daemon", "start"], capture_output=True, text=True, env=env, timeout=300)
    entries["daemon_start_rc"] = p.returncode
    v = subprocess.run([CODEX, "app-server", "daemon", "version"], capture_output=True, text=True, env=env, timeout=30)
    entries["daemon_version"] = (v.stdout or v.stderr).strip()[:300]
    # 2. 观察连接先 initialize（记录 baseline thread 列表）
    obs = WsUdsObserver(home, ws, raw)
    obs.initialize(name="codex-web-observer", experimental=True)
    base = obs.request("thread/list", {"limit": 50})
    before_ids = [t.get("id") for t in (base.get("result", {}).get("data") or [])]
    entries["observer_initialize"] = True
    # 3. TUI 默认启动 + 发消息
    tui = TuiSession(home, ws)
    ready = tui.wait_composer(150)
    entries["tui_composer_ready"] = ready
    sock = home / "app-server-control" / "app-server-control.sock"
    tui.send("MOCK:SLOW gate scene one message")
    time.sleep(0.6)
    tui.send("\r")
    # tid 来源优先用全局广播的 thread/started（比轮询 loaded/list 快约 2s，
    # 可在 turn/started 触发前完成 resume 订阅）
    tid_fast = None
    got_thread_started = threading.Event()
    def _on_thread_started(nid):
        nonlocal tid_fast
        if nid not in before_ids and not tid_fast:
            tid_fast = nid
            got_thread_started.set()
    obs.thread_started_hook = _on_thread_started
    got_thread_started.wait(10)
    # 4. 立即订阅（turn/started 只发订阅者且不重放；任何 subprocess 取证都会吃掉 0.3s+ 窗口）
    tid = tid_fast or find_new_thread(obs, before_ids)
    entries["new_thread_found"] = tid or "NONE"
    if tid:
        # 订阅优先：取证放 resume 之后（turn/started 只发订阅者且不重放）
        res = obs.request("thread/resume", {"threadId": tid, "excludeTurns": True}, timeout=30)
        entries["observer_resume"] = res.get("error") or "ok"
        # 订阅完成后再取证（turn 有 ~17s 窗口）
        lsof_during = sh(f"lsof '{sock}' 2>/dev/null", env)
        entries["lsof_during_turn"] = lsof_during.stdout.strip()[:400]
        try:
            loaded_during = obs.request("thread/loaded/list", {}, timeout=15)
            entries["loaded_threads_during_turn"] = (loaded_during.get("result") or {}).get("threadIds") or (loaded_during.get("result") or {})
        except Exception as ex:
            entries["loaded_threads_during_turn"] = str(ex)[:80]
        ps_tui = sh(f"ps -o pid,ppid,command -p {tui.pid}", env)
        children = sh(f"pgrep -lP {tui.pid}", env)
        proc_ev = f"=== daemon version ===\n{entries['daemon_version']}\n=== lsof control socket ===\n{lsof_during.stdout}\n=== TUI ps ===\n{ps_tui.stdout}\n=== TUI children ===\n{children.stdout}\n"
        entries["tui_connected_to_socket"] = bool(lsof_during.stdout.strip())
        # 等待该 thread 的 turn 完成（live 事件应持续到达）
        deadline = time.time() + 40
        while time.time() < deadline:
            deltas = [n for n in obs.notifications
                      if n.get("method") == "item/agentMessage/delta"
                      and n.get("params", {}).get("threadId") == tid]
            done = [n for n in obs.notifications
                    if n.get("method") == "turn/completed"
                    and (n.get("params", {}).get("threadId") == tid
                         or (n.get("params", {}).get("turn", {}) or {}).get("threadId") == tid)]
            if done and len(deltas) >= 5:
                break
            time.sleep(0.4)
        deltas = [n for n in obs.notifications if n.get("method") == "item/agentMessage/delta" and n.get("params", {}).get("threadId") == tid]
        started = [n for n in obs.notifications if n.get("method") == "turn/started" and n.get("params", {}).get("threadId") == tid]
        completed = [n for n in obs.notifications if n.get("method") == "turn/completed" and n.get("params", {}).get("threadId") == tid]
        entries["observer_turn_started"] = len(started)
        entries["observer_delta_count"] = len(deltas)
        deltas_before = len(deltas)
        entries["observer_turn_completed"] = len(completed)
    tui.pump(2)
    screen = tui.screen()
    tui.close()
    obs.close()
    dump_scene("scene1-daemon-default", entries, raw, screen, proc_ev,
               {"expectation": "TUI=LocalDaemon；观察端收到 started/多delta/completed",
                "verdict_input": {"tui_connected": entries.get("tui_connected_to_socket"),
                                  "observer_events": [entries.get("observer_turn_started"), entries.get("observer_delta_count"), entries.get("observer_turn_completed")]}})
    os.remove(raw)
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    return entries


def scene2(provider_port):
    """daemon 未运行，TUI 先启动（Embedded）→ 之后 daemon+观察连接不伪称 live。"""
    ws = pathlib.Path("/tmp/cw-gate-ws-s2"); shutil.rmtree(ws, ignore_errors=True); ws.mkdir()
    home = make_home(provider_port, ws, seed_standalone=True, short="/tmp/cw-gate-home-s2")
    env = dict(os.environ, CODEX_HOME=str(home))
    raw = str(DUMPS / "_s2_observer.jsonl")
    DUMPS.mkdir(parents=True, exist_ok=True)
    entries = {}
    v = subprocess.run([CODEX, "app-server", "daemon", "version"], capture_output=True, text=True, env=env, timeout=30)
    entries["daemon_absent_check"] = (v.stdout + v.stderr).strip()[:200]
    tui = TuiSession(home, ws)
    ready = tui.wait_composer(150)
    entries["tui_composer_ready"] = ready
    tui.send("MOCK:SLOW gate scene two embedded message")
    time.sleep(0.6)
    tui.send("\r")
    tui.pump(22)  # 让 Embedded turn 完成
    screen_mid = tui.screen()
    sock = home / "app-server-control" / "app-server-control.sock"
    entries["socket_existed_while_embedded"] = sock.exists()
    children = sh(f"pgrep -lP {tui.pid}", env)
    proc_ev = f"=== daemon absent ===\n{entries['daemon_absent_check']}\n=== control socket exists during TUI ===\n{sock.exists()}\n=== TUI children ===\n{children.stdout}\n"
    # TUI 保持运行，启动 daemon + 观察连接
    p = subprocess.run([CODEX, "app-server", "daemon", "start"], capture_output=True, text=True, env=env, timeout=300)
    entries["daemon_start_after"] = p.returncode
    obs = WsUdsObserver(home, ws, raw)
    obs.initialize(name="codex-web-observer")
    lst = obs.request("thread/list", {"limit": 50})
    data = lst.get("result", {}).get("data") or []
    entries["observer_list_count"] = len(data)
    live_methods = []
    if data:
        tid = data[0]["id"]
        rd = obs.request("thread/read", {"threadId": tid, "includeTurns": True}, timeout=30)
        entries["observer_read_turns"] = len((rd.get("result", {}).get("thread", {}) or {}).get("turns") or [])
        entries["read_thread_id"] = tid
        # 等待 8s：不应有任何该 thread 的 live turn 事件（Embedded turn 已完成，无重放）
        time.sleep(8)
        evs = [n for n in obs.notifications if n.get("method") in ("turn/started", "item/agentMessage/delta", "turn/completed")]
        entries["unexpected_live_events"] = len(evs)
        live_methods = sorted({n["method"] for n in evs})
        entries["unexpected_live_methods"] = live_methods
    tui.pump(1)
    screen = tui.screen()
    tui.close()
    obs.close()
    dump_scene("scene2-tui-first-embedded", entries, raw, screen, proc_ev,
               {"expectation": "TUI=Embedded（socket 不存在）；观察端无伪 live 事件，list/read 可用",
                "verdict_input": {"socket_existed": entries.get("socket_existed_while_embedded"),
                                  "unexpected_live": entries.get("unexpected_live_events")}})
    os.remove(raw)
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    return entries


def scene3(provider_port):
    """daemon 已运行，TUI 带 -c 覆盖（Embedded）→ 观察端不串入。"""
    ws = pathlib.Path("/tmp/cw-gate-ws-s3"); shutil.rmtree(ws, ignore_errors=True); ws.mkdir()
    home = make_home(provider_port, ws, seed_standalone=True, short="/tmp/cw-gate-home-s3")
    env = dict(os.environ, CODEX_HOME=str(home))
    raw = str(DUMPS / "_s3_observer.jsonl")
    DUMPS.mkdir(parents=True, exist_ok=True)
    entries = {}
    subprocess.run([CODEX, "app-server", "daemon", "start"], capture_output=True, text=True, env=env, timeout=300)
    v = subprocess.run([CODEX, "app-server", "daemon", "version"], capture_output=True, text=True, env=env, timeout=30)
    entries["daemon_running"] = bool(v.stdout.strip())
    obs = WsUdsObserver(home, ws, raw)
    obs.initialize(name="codex-web-observer", experimental=True)
    base = obs.request("thread/list", {"limit": 50})
    before_ids = [t.get("id") for t in (base.get("result", {}).get("data") or [])]
    tui = TuiSession(home, ws, extra_args=["-c", 'model_reasoning_effort="low"'])
    ready = tui.wait_composer(150)
    entries["tui_composer_ready"] = ready
    tui.send("MOCK:SLOW gate scene three override message")
    time.sleep(0.6)
    tui.send("\r")
    tui.pump(22)
    # 覆盖启动：TUI 不应连接 control socket
    sock = home / "app-server-control" / "app-server-control.sock"
    lsof = sh(f"lsof '{sock}' 2>/dev/null | grep -v 'app-server-daemon\\|proxy' | head -5", env)
    children = sh(f"pgrep -lP {tui.pid}", env)
    proc_ev = f"=== daemon version ===\n{v.stdout[:200]}\n=== lsof socket (non-daemon) ===\n{lsof.stdout}\n=== TUI children ===\n{children.stdout}\n"
    entries["tui_connected_to_socket"] = bool(lsof.stdout.strip())
    lst = obs.request("thread/list", {"limit": 50})
    ids = [t.get("id") for t in (lst.get("result", {}).get("data") or [])]
    new_threads = [i for i in ids if i not in before_ids]
    entries["new_threads_visible_to_observer"] = len(new_threads)
    # 观察端不应收到该 Embedded turn 的 live 事件
    evs = [n for n in obs.notifications if n.get("method") in ("turn/started", "item/agentMessage/delta", "turn/completed")]
    entries["live_events_at_observer"] = len(evs)
    screen = tui.screen()
    tui.close()
    obs.close()
    dump_scene("scene3-daemon-override-embedded", entries, raw, screen, proc_ev,
               {"expectation": "TUI=Embedded（-c 覆盖不复用 daemon）；观察端 0 live 事件",
                "verdict_input": {"tui_connected": entries.get("tui_connected_to_socket"),
                                  "live_events": entries.get("live_events_at_observer")}})
    os.remove(raw)
    subprocess.run([CODEX, "app-server", "daemon", "stop"], capture_output=True, text=True, env=env, timeout=60)
    shutil.rmtree(home, ignore_errors=True)
    shutil.rmtree(ws, ignore_errors=True)
    return entries


def main():
    only = sys.argv[1] if len(sys.argv) > 1 else "1,2,3"
    provider_proc, port = start_provider()
    import collect_samples
    SANI.extend([(f"/var/folders", "$TMPDIR")])
    try:
        if "1" in only:
            print("== scene1 ==", json.dumps(scene1(port), ensure_ascii=False)[:400])
        if "2" in only:
            print("== scene2 ==", json.dumps(scene2(port), ensure_ascii=False)[:400])
        if "3" in only:
            print("== scene3 ==", json.dumps(scene3(port), ensure_ascii=False)[:400])
    finally:
        provider_proc.terminate()
    print("DONE ->", DUMPS)


if __name__ == "__main__":
    main()
