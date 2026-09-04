#!/usr/bin/env python3
"""Phase 0.5 hooks probe (design §6 Phase 0.5, M4/S2/S3/R2-S7).

Spawns `claude` with the production inner args PLUS `--settings` inline HTTP
hooks pointing at a local token-authed receiver (token in the URL path
segment — the live-machine sample shows HTTP hooks carry no headers field).

Scenario:
  1. receiver up on 127.0.0.1:<port>, path /hook/<token>
  2. spawn with --settings {"hooks":{SessionStart,Stop,UserPromptSubmit,
     ConfigChange,SessionEnd → http hook}}
  3. send BARE initialize (no hooks field) — R2-S7 comparison: settings-layer
     HTTP hooks must still fire (hooks_applied semantics only covers SDK
     callback hooks)
  4. one real turn (SessionStart/Stop/UserPromptSubmit POSTs)
  5. mid-session write W/.claude/settings.json (project layer) to trigger
     ConfigChange
  6. stdin close (SessionEnd)

Every POST (headers + body) is archived to dumps/hooks-posts.jsonl; the
stream to dumps/hooks.jsonl. PostModelSwitch is NOT probed (needs ≥2.1.251;
this machine's PATH CLI is 2.1.234 = documented-absent).
"""

import json
import secrets
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

from control_plane_probe import Probe, req, DUMPS_DIR  # noqa: E402

POSTS_PATH = DUMPS_DIR / "hooks-posts.jsonl"


class Receiver(BaseHTTPRequestHandler):
    token: str = ""
    posts: list = []

    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length).decode("utf-8", "replace") if length else ""
        rec = {
            "t": time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()) + "Z",
            "path": self.path,
            "auth_ok": self.path == f"/hook/{Receiver.token}",
            "headers": {k: v for k, v in self.headers.items()},
            "body": body,
        }
        Receiver.posts.append(rec)
        with open(POSTS_PATH, "a") as f:
            f.write(json.dumps(rec, ensure_ascii=False) + "\n")
        if not rec["auth_ok"]:
            self.send_response(404)
        else:
            self.send_response(200)
        self.send_header("Content-Length", "0")
        self.end_headers()

    def log_message(self, *a):  # silence default stderr logging
        pass


HOOK_EVENTS = ["SessionStart", "Stop", "UserPromptSubmit", "ConfigChange", "SessionEnd"]


def main() -> None:
    DUMPS_DIR.mkdir(parents=True, exist_ok=True)
    POSTS_PATH.write_text("")  # fresh archive per run

    token = secrets.token_urlsafe(16)
    Receiver.token = token
    srv = ThreadingHTTPServer(("127.0.0.1", 0), Receiver)
    port = srv.server_address[1]
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    url = f"http://127.0.0.1:{port}/hook/{token}"

    hooks_settings = {
        "hooks": {
            **{ev: [{"hooks": [{"type": "http", "url": url, "timeout": 10}]}]
               for ev in HOOK_EVENTS if ev != "SessionStart"},
            # explicit matcher: discriminate "no matcher" vs layer-timing theories
            "SessionStart": [{"matcher": "startup",
                              "hooks": [{"type": "http", "url": url, "timeout": 10}]}],
        }
    }
    settings_arg = json.dumps(hooks_settings, separators=(",", ":"))

    # project-layer settings must exist BEFORE spawn to be in the cascade
    projdir = DUMPS_DIR / "workdir-hooks" / ".claude"
    projdir.mkdir(parents=True, exist_ok=True)
    (projdir / "settings.json").write_text('{"model": "sonnet"}\n')

    p = Probe("hooks")
    p.start(extra_args=["--settings", settings_arg])
    p.meta(hook_url=url.replace(token, "***TOKEN***"), events=HOOK_EVENTS,
           note="--settings inline JSON; token in URL path segment")

    # R2-S7: bare initialize (no hooks field) — settings hooks must survive it
    p.send(req("req_h1", {"subtype": "initialize"}))
    p.expect_response("req_h1", "initialize_bare")

    p.send({"type": "user", "message": {"role": "user", "content": "Reply with exactly one word: pong"}})
    end = time.monotonic() + 90
    while time.monotonic() < end:
        line = p.read_line(2.0)
        if line is None:
            if p.proc.poll() is not None:
                break
            continue
        p.record("in", line)
        try:
            obj = json.loads(line)
        except json.JSONDecodeError:
            continue
        if obj.get("type") == "result":
            p.meta(note="turn result frame seen")
            break

    # ConfigChange: write project-layer settings mid-session
    proj = Path(f"dumps/workdir-hooks/.claude")
    # workdir is relative to cwd when the probe runs from scripts/claudecode-phase0;
    # resolve against the probe's own dumps dir to be safe
    proj = DUMPS_DIR / "workdir-hooks" / ".claude"
    proj.mkdir(parents=True, exist_ok=True)
    (proj / "settings.json").write_text('{"model": "sonnet"}\n')
    time.sleep(3)
    (proj / "settings.json").write_text('{"model": "haiku"}\n')
    time.sleep(3)

    p.finish()
    srv.shutdown()

    events_seen = []
    for rec in Receiver.posts:
        try:
            events_seen.append(json.loads(rec["body"]).get("hook_event_name"))
        except Exception:
            events_seen.append("<non-json>")
    p.meta(posts_total=len(Receiver.posts), events_seen=events_seen,
           auth_all_ok=all(r["auth_ok"] for r in Receiver.posts))
    p.flush()
    print("posts:", len(Receiver.posts), "events:", events_seen)


if __name__ == "__main__":
    main()
