#!/usr/bin/env python3
"""Phase 0.1 control-plane probe for the Claude Code backend (design 2026-09-04 §6 Phase 0.1).

Spawns `claude` with the EXACT production inner args (baseClaudeInnerArgs,
agent/claudecode/session.go:108):

    --output-format stream-json --input-format stream-json
    --permission-prompt-tool stdio --include-partial-messages --verbose

No -p. stdin stays open for the whole scenario (SDK: control methods only
work with streaming input; one-shot -p closes stdin).

Env model mirrors production newClaudeSession():
    FilterEnvToAllowlist(os.Environ(), agentEnvRuntimeAllowlist)   # 10 base vars
    + provider env (captured from a live production claude/runtime process)
    + CLAUDE_CODE_ENTRYPOINT=claude-desktop-3p

Every stdin/stdout line is archived to dumps/<name>.jsonl with direction and
timestamp. Secret values (ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN) are
redacted IN MEMORY before anything is written to disk.

Verdicts are recorded per request: success | error | unknown-subtype |
no-response (the three legal probe conclusions of the design).

Usage:
    ./control_plane_probe.py main        # init -> list_models -> set_model x2
                                        # -> set_permission_mode x2 -> interrupt
                                        # -> rename_session
    ./control_plane_probe.py bare-list   # list_models BEFORE initialize (M1:
                                        # "must initialize first?" control)
    ./control_plane_probe.py bypass      # initialize -> set_permission_mode
                                        # bypassPermissions (recorded separately,
                                        # R2-S6)
"""

import json
import os
import re
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
DUMPS_DIR = SCRIPT_DIR / "dumps"
ENV_MIRROR = SCRIPT_DIR / "runtime-env.mirror"

BASE_ALLOWLIST = [
    "PATH", "HOME", "USER", "LOGNAME",
    "LANG", "LC_ALL", "LC_CTYPE", "LC_MESSAGES",
    "TMPDIR", "SHELL",
]

EXPECT_TIMEOUT = 30.0        # per control_response
SPAWN_HARD_LIMIT = 240.0     # per scenario watchdog


def now_iso() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="milliseconds")


class Probe:
    def __init__(self, name: str):
        self.name = name
        self.dump_path = DUMPS_DIR / f"{name}.jsonl"
        self.summary_path = DUMPS_DIR / f"{name}-summary.json"
        self.secrets: list[str] = []
        self.verdicts: dict[str, dict] = {}
        self.records: list[dict] = []

    # ---- recording -------------------------------------------------------

    def _redact(self, text: str) -> str:
        for s in self.secrets:
            if s:
                text = text.replace(s, "***REDACTED***")
        # generic key-shaped fallbacks
        text = re.sub(r"sk-ant-[A-Za-z0-9_\-]{8,}", "***REDACTED***", text)
        return text

    def record(self, dirn: str, line: str) -> None:
        rec = {"t": now_iso(), "dir": dirn, "line": self._redact(line)}
        self.records.append(rec)

    def meta(self, **kv) -> None:
        rec = {"t": now_iso(), "dir": "meta", **{k: self._redact(str(v)) if isinstance(v, str) else v for k, v in kv.items()}}
        self.records.append(rec)

    def flush(self) -> None:
        with open(self.dump_path, "w") as f:
            for rec in self.records:
                f.write(json.dumps(rec, ensure_ascii=False) + "\n")
        with open(self.summary_path, "w") as f:
            json.dump({"scenario": self.name, "verdicts": self.verdicts}, f, ensure_ascii=False, indent=2)

    # ---- env -------------------------------------------------------------

    def build_env(self) -> list[str]:
        env = {k: v for k, v in os.environ.items() if k in BASE_ALLOWLIST}
        provider = {}
        if ENV_MIRROR.exists():
            for ln in ENV_MIRROR.read_text().splitlines():
                if "=" in ln:
                    k, _, v = ln.partition("=")
                    provider[k] = v
                    if k in ("ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"):
                        self.secrets.append(v)
        env.update(provider)
        # p0-6 matrix combos override specific process-env vars (last-wins,
        # matching how a supervisor would inject them before spawn)
        for k, v in getattr(self, "env_overrides", {}).items():
            env[k] = v
        # production runtimeEnvLocked() always merges this last
        env["CLAUDE_CODE_ENTRYPOINT"] = "claude-desktop-3p"
        return [f"{k}={v}" for k, v in env.items()]

    # ---- process driving --------------------------------------------------

    def start(self, extra_args: list[str] | None = None) -> None:
        args = [
            "claude",
            "--output-format", "stream-json",
            "--input-format", "stream-json",
            "--permission-prompt-tool", "stdio",
            "--include-partial-messages",
            "--verbose",
        ]
        if extra_args:
            args += extra_args
        workdir = DUMPS_DIR / f"workdir-{self.name}"
        workdir.mkdir(parents=True, exist_ok=True)
        self.meta(spawn_bin="claude", spawn_args=" ".join(args), workdir=str(workdir),
                  note="verbatim baseClaudeInnerArgs(session.go:108), no -p, stdin held open")
        self.proc = subprocess.Popen(
            args, cwd=str(workdir), env=dict(kv.split("=", 1) for kv in self.build_env()),
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, bufsize=1,
        )
        self.deadline = time.monotonic() + SPAWN_HARD_LIMIT
        self.started_at = time.time()

    def send(self, obj: dict) -> None:
        line = json.dumps(obj, ensure_ascii=False)
        self.record("out", line)
        assert self.proc.stdin is not None
        self.proc.stdin.write(line + "\n")
        self.proc.stdin.flush()

    def read_line(self, timeout: float) -> str | None:
        # line-buffered read with a deadline; None on timeout/EOF
        import select
        end = time.monotonic() + timeout
        fd = self.proc.stdout.fileno()
        while True:
            remain = end - time.monotonic()
            if remain <= 0:
                return None
            r, _, _ = select.select([fd], [], [], min(remain, 0.5))
            if not r:
                if self.proc.poll() is not None:
                    return None
                continue
            line = self.proc.stdout.readline()
            if line == "":
                return None
            return line.rstrip("\n")

    def expect_response(self, request_id: str, label: str, timeout: float = EXPECT_TIMEOUT) -> dict:
        """Wait for the control_response echoing request_id; classify verdict."""
        self.meta(waiting_for=request_id, label=label, timeout=timeout)
        while time.monotonic() < self.deadline:
            line = self.read_line(min(timeout, self.deadline - time.monotonic()))
            if line is None:
                break
            self.record("in", line)
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue
            if obj.get("type") != "control_response":
                continue
            resp = obj.get("response") or {}
            if resp.get("request_id") != request_id:
                self.meta(note=f"control_response with foreign request_id seen: {resp.get('request_id')}")
                continue
            subtype = resp.get("subtype")
            err = str(resp.get("error") or resp.get("message") or "")
            if subtype == "success":
                verdict = "success"
            elif "unknown" in err.lower() or "unsupported" in err.lower() or "not support" in err.lower():
                verdict = "unknown-subtype"
            else:
                verdict = "error"
            self.verdicts[label] = {
                "request_id": request_id, "verdict": verdict,
                "response_subtype": subtype,
            }
            self.meta(verdict=label + ":" + verdict)
            return obj
        self.verdicts[label] = {"request_id": request_id, "verdict": "no-response"}
        self.meta(verdict=label + ":no-response")
        return {}

    def wait_init_message(self, timeout: float = 30.0) -> dict | None:
        init = None
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            line = self.read_line(min(1.0, end - time.monotonic()))
            if line is None:
                break
            self.record("in", line)
            try:
                obj = json.loads(line)
            except json.JSONDecodeError:
                continue
            if obj.get("type") == "system" and obj.get("subtype") == "init":
                init = obj
                self.meta(note="system/init captured")
                break
        return init

    def finish(self) -> None:
        try:
            if self.proc.stdin and not self.proc.stdin.closed:
                self.proc.stdin.close()
        except BrokenPipeError:
            pass
        try:
            self.proc.wait(timeout=15)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait()
            self.meta(note="had to SIGKILL after stdin EOF")
        stderr = (self.proc.stderr.read() or "").strip()
        if stderr:
            for ln in stderr.splitlines()[-30:]:
                self.record("err", ln)
        self.meta(exit_code=self.proc.returncode,
                  wall_seconds=round(time.time() - self.started_at, 1))
        self.flush()


def req(rid: str, inner: dict) -> dict:
    return {"type": "control_request", "request_id": rid, "request": inner}


def scenario_main() -> None:
    p = Probe("main")
    p.start()
    init = p.wait_init_message()
    models_default = None
    if init:
        # probe-side peek only (NOT a production parser): pick a model value
        # from the init message if present, for the set_model target.
        for m in (init.get("models") or []):
            if isinstance(m, dict) and m.get("value"):
                models_default = m["value"]
                break
        p.meta(init_models_seen=[m.get("value") for m in (init.get("models") or [])][:20] if isinstance(init.get("models"), list) else "absent-from-init-message")
    p.send(req("req_1", {"subtype": "initialize"}))
    resp = p.expect_response("req_1", "initialize")
    init_models = None
    if resp:
        r = resp.get("response") or {}
        init_models = r.get("models") if isinstance(r.get("models"), list) else None
    target = None
    if init_models:
        for m in init_models:
            if isinstance(m, dict) and m.get("value"):
                target = m["value"]
                break
    p.meta(set_model_target=target or models_default or "default")
    p.send(req("req_2", {"subtype": "list_models"}))
    p.expect_response("req_2", "list_models")
    p.send(req("req_3", {"subtype": "set_model", "model": target or models_default or "default"}))
    p.expect_response("req_3", "set_model")
    p.send(req("req_4", {"subtype": "set_model", "model": "default"}))
    p.expect_response("req_4", "set_model_reset_default")
    p.send(req("req_5", {"subtype": "set_permission_mode", "mode": "acceptEdits"}))
    p.expect_response("req_5", "set_permission_mode_acceptEdits")
    p.send(req("req_6", {"subtype": "set_permission_mode", "mode": "default"}))
    p.expect_response("req_6", "set_permission_mode_default")
    p.send(req("req_7", {"subtype": "interrupt", "cancel_queued": True}))
    p.expect_response("req_7", "interrupt_cancel_queued")
    p.send(req("req_8", {"subtype": "rename_session", "title": "cordcode-phase0-probe"}))
    p.expect_response("req_8", "rename_session")
    p.finish()


def scenario_bare_list() -> None:
    p = Probe("bare-list")
    p.start()
    p.wait_init_message()
    # M1 control: list_models FIRST, before any initialize
    p.send(req("req_b1", {"subtype": "list_models"}))
    p.expect_response("req_b1", "list_models_before_initialize")
    p.send(req("req_b2", {"subtype": "initialize"}))
    p.expect_response("req_b2", "initialize_after_bare_list")
    p.send(req("req_b3", {"subtype": "list_models"}))
    p.expect_response("req_b3", "list_models_after_initialize")
    p.finish()


def scenario_bypass() -> None:
    p = Probe("bypass")
    p.start()
    p.wait_init_message()
    p.send(req("req_c1", {"subtype": "initialize"}))
    p.expect_response("req_c1", "initialize")
    # R2-S6: bypassPermissions needs allowDangerouslySkipPermissions; the local
    # auto-approve path never goes through the CLI, so no history to infer from.
    p.send(req("req_c2", {"subtype": "set_permission_mode", "mode": "bypassPermissions"}))
    p.expect_response("req_c2", "set_permission_mode_bypassPermissions")
    p.finish()


def scenario_turn() -> None:
    """One real model turn to capture system/init (capabilities[]) and
    assistant message.model — neither is emitted before the first user input
    on 2.1.234 (observed in the `main` dump)."""
    p = Probe("turn")
    p.start()
    # drain startup hook frames
    end = time.monotonic() + 8
    while time.monotonic() < end:
        line = p.read_line(1.0)
        if line is None:
            break
        p.record("in", line)
    p.send(req("req_t1", {"subtype": "initialize"}))
    p.expect_response("req_t1", "initialize")
    p.send({"type": "user", "message": {"role": "user", "content": "Reply with exactly one word: pong"}})
    # read until the turn's result frame (or deadline)
    end = time.monotonic() + 120
    saw_init = None
    assistant_models = []
    while time.monotonic() < end:
        line = p.read_line(min(2.0, end - time.monotonic()))
        if line is None:
            if p.proc.poll() is not None:
                break
            continue
        p.record("in", line)
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        t, st = obj.get("type"), obj.get("subtype")
        if t == "system" and st == "init":
            saw_init = obj
            p.meta(note="system/init captured during turn")
        if t == "assistant":
            m = (obj.get("message") or {})
            if m.get("model"):
                assistant_models.append(m.get("model"))
        if t == "result":
            p.meta(result_subtype=st, result_model=obj.get("model"),
                   assistant_models_seen=sorted(set(assistant_models)))
            break
    if saw_init:
        p.meta(init_capabilities=saw_init.get("capabilities"),
               init_keys=sorted(saw_init.keys()))
    else:
        p.meta(note="system/init NOT seen even during turn")
    p.finish()


SCENARIOS = {
    "main": scenario_main,
    "bare-list": scenario_bare_list,
    "bypass": scenario_bypass,
    "turn": scenario_turn,
}

if __name__ == "__main__":
    if len(sys.argv) != 2 or sys.argv[1] not in SCENARIOS:
        print(__doc__)
        sys.exit(2)
    DUMPS_DIR.mkdir(parents=True, exist_ok=True)
    SCENARIOS[sys.argv[1]]()
    print("done")
