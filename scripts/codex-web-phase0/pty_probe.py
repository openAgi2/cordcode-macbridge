#!/usr/bin/env python3
"""Gate 侦察：PTY 下启动官方 TUI，观察启动屏幕与日志文件落点。"""
import os
import pty
import pathlib
import select
import subprocess
import sys
import tempfile
import time

CODEX = "/Applications/ChatGPT.app/Contents/Resources/codex"


def make_home(port):
    home = pathlib.Path(tempfile.mkdtemp(prefix="gate-recon-home-"))
    ws = pathlib.Path(tempfile.mkdtemp(prefix="gate-recon-ws-"))
    (home / "config.toml").write_text(f'''model = "mock-model"
model_provider = "mockpi"

[model_providers.mockpi]
name = "Mock Provider"
base_url = "http://127.0.0.1:{port}/v1"
wire_api = "responses"

[projects."{ws}"]
trust_level = "trusted"
''')
    return home, ws


def run_tui(home, ws, seconds=6, extra_args=None, send=None):
    pid, fd = pty.fork()
    if pid > 0:
        import fcntl, struct, termios
        fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 120, 0, 0))
    if pid == 0:
        os.environ["CODEX_HOME"] = str(home)
        os.environ["TERM"] = "xterm-256color"
        os.chdir(str(ws))
        os.execv(CODEX, [CODEX] + (extra_args or []))
    out = b""
    deadline = time.time() + seconds
    sent = False
    while time.time() < deadline:
        r, _, _ = select.select([fd], [], [], 0.5)
        if r:
            try:
                chunk = os.read(fd, 65536)
                if not chunk:
                    break
                out += chunk
            except OSError:
                break
        if send and not sent and time.time() > deadline - seconds + 3:
            os.write(fd, send)
            sent = True
    try:
        os.kill(pid, 15)
    except ProcessLookupError:
        pass
    try:
        os.waitpid(pid, 0)
    except ChildProcessError:
        pass
    os.close(fd)
    return out


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8901
    home, ws = make_home(port)
    print("home:", home, "ws:", ws)
    out = run_tui(home, ws, seconds=20)
    text = out.decode("utf-8", "replace")
    # 去掉 ANSI 转义便于阅读
    import re
    clean = re.sub(r"\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07]*\x07|\x1b[()][A-B0]|\r", "", text)
    print("=== TUI 输出（清理 ANSI，前 1500 字） ===")
    print(clean[-2500:])
    print("=== home 目录 ===")
    for p in sorted(home.rglob("*")):
        print(" ", p.relative_to(home), p.stat().st_size if p.is_file() else "")
    logdir = home / "log"
    if logdir.exists():
        for f in sorted(logdir.glob("*")):
            print(f"--- {f.name} tail ---")
            print(f.read_text(errors="replace")[-1200:])


if __name__ == "__main__":
    main()
