#!/usr/bin/env python3
"""p0-callsite-index-tests：核对 call-site 索引引用的 file:line 在 pinned source 真实存在。

对 docs/2026-08-21-codex-web-phase0-callsite-index.md 中登记的关键锚点，验证目标行或 ±6 行
邻域内存在预期符号（源码小漂移容忍，符号名为准）。
"""
import pathlib
import subprocess
import sys

SRC = pathlib.Path("/Users/jacklee/Projects/codex/codex-rs")
IDX = pathlib.Path(__file__).resolve().parent.parent.parent / "docs/2026-08-21-codex-web-phase0-callsite-index.md"

failures = []

# (file, approx_line, expected_symbols_any)
ANCHORS = [
    ("cli/src/main.rs", 2588, ["agents_overview"]),
    ("cli/src/main.rs", 2596, ["codex_app_server_daemon::run", "app_server_daemon"]),
    ("tui/src/lib.rs", 275, ["enum AppServerTarget"]),
    ("tui/src/lib.rs", 436, ["maybe_probe_default_daemon_socket"]),
    ("tui/src/lib.rs", 851, ["fn app_server_target_for_launch"]),
    ("tui/src/lib.rs", 912, ["fn can_reuse_implicit_local_daemon"]),
    ("tui/src/startup_orchestration.rs", 135, ["reuse_implicit_local_daemon"]),
    ("tui/src/startup_orchestration.rs", 186, ["app_server_target_for_launch"]),
    ("tui/src/app/session_lifecycle.rs", 365, ["attach_live_thread_for_selection"]),
    ("tui/src/app/session_lifecycle.rs", 462, ["select_agent_thread"]),
    ("tui/src/app/session_lifecycle.rs", 617, ["handle_startup_thread_started"]),
    ("tui/src/app/session_lifecycle.rs", 796, ["replace_chat_widget_with_app_server_thread"]),
    ("tui/src/app/session_lifecycle.rs", 225, ["is_terminal_thread_read_error"]),
    ("tui/src/app/app_server_events.rs", 54, ["handle_app_server_event"]),
    ("app-server-client/src/lib.rs", 300, ["InProcessAppServerClient"]),
    ("app-server-client/src/lib.rs", 516, ["resolve_server_request"]),
    ("app-server-client/src/lib.rs", 574, ["next_event"]),
    ("app-server-client/src/remote.rs", 72, ["enum RemoteAppServerEndpoint"]),
    ("app-server-client/src/remote.rs", 166, ["async fn connect"]),
    ("app-server-client/src/remote.rs", 541, ["resolve_server_request"]),
    ("app-server/src/thread_state.rs", 533, ["try_ensure_connection_subscribed"]),
    ("app-server/src/thread_state.rs", 560, ["try_add_connection_to_thread"]),
    ("app-server/src/request_processors/thread_processor.rs", 960, ["thread_unsubscribe_response_inner"]),
    ("app-server/src/request_processors/thread_lifecycle.rs", 7, ["THREAD_UNLOADING_DELAY"]),
    ("app-server/src/error_code.rs", 3, ["INVALID_REQUEST_ERROR_CODE"]),
    ("app-server/src/request_processors/thread_processor.rs", 531, ["thread_resume"]),
    ("app-server/src/request_processors/thread_processor.rs", 3479, ["thread_resume_inner"]),
    ("app-server/src/bespoke_event_handling.rs", 714, ["available_decisions: Some"]),
    ("app-server/src/transport.rs", 175, ["filter_outgoing_message_for_connection"]),
    ("app-server/src/transport.rs", 189, ["strip_experimental_fields"]),
    ("app-server-protocol/src/protocol/v2/item.rs", 1512, ["strip_experimental_fields"]),
    ("app-server-transport/src/transport/websocket.rs", 149, ["/readyz"]),
    ("app-server-transport/src/transport/websocket.rs", 135, ["is_unauthenticated_non_loopback_listener"]),
    ("app-server-daemon/src/lib.rs", 191, ["pub async fn run"]),
    ("thread-store/src/local/writer_lock.rs", 39, ["fn acquire"]),
    ("thread-store/src/local/mod.rs", 302, ["acquire_writer_locks"]),
    ("thread-store/src/local/archive_thread.rs", 24, ["writer_lock_thread_ids"]),
]

TESTS = [
    "app-server/tests/suite/v2/initialize.rs",
    "app-server/tests/suite/v2/connection_handling_websocket.rs",
    "app-server/tests/suite/v2/connection_handling_websocket_unix.rs",
    "app-server/tests/suite/v2/session_end.rs",
    "app-server/tests/suite/v2/thread_list.rs",
    "app-server/tests/suite/v2/thread_loaded_list.rs",
    "app-server/tests/suite/v2/thread_read.rs",
    "app-server/tests/suite/v2/thread_resume.rs",
    "app-server/tests/suite/v2/thread_start.rs",
    "app-server/tests/suite/v2/thread_archive.rs",
    "app-server/tests/suite/v2/thread_unarchive.rs",
    "app-server/tests/suite/v2/thread_delete.rs",
    "app-server/tests/suite/v2/thread_fork.rs",
    "app-server/tests/suite/v2/thread_unsubscribe.rs",
    "app-server/tests/suite/v2/thread_name_websocket.rs",
    "app-server/tests/suite/v2/thread_status.rs",
    "app-server/tests/suite/v2/turn_start.rs",
    "app-server/tests/suite/v2/turn_steer.rs",
    "app-server/tests/suite/v2/turn_interrupt.rs",
    "app-server/tests/suite/v2/request_user_input.rs",
    "app-server/tests/suite/v2/mcp_server_elicitation.rs",
    "app-server/tests/suite/v2/experimental_api.rs",
    "app-server/tests/suite/v2/command_exec.rs",
    "app-server/tests/suite/v2/compaction.rs",
    "app-server/tests/suite/v2/account_thread_usage.rs",
    "app-server/tests/suite/v2/review.rs",
    "app-server/tests/suite/v2/config_rpc.rs",
]

n = 0
for rel, approx, symbols in ANCHORS:
    path = SRC / rel
    if not path.exists():
        failures.append(f"missing file {rel}")
        continue
    lines = path.read_text().splitlines()
    lo, hi = max(0, approx - 7), min(len(lines), approx + 7)
    window = "\n".join(lines[lo:hi])
    hit = any(sym in window for sym in symbols)
    n += 1
    status = "PASS" if hit else "FAIL"
    print(f"[{status}] {rel}:{approx} ~ {symbols[0]}")
    if not hit:
        failures.append(f"{rel}:{approx} {symbols}")

for rel in TESTS:
    ok = (SRC / rel).exists()
    n += 1
    print(f"[{'PASS' if ok else 'FAIL'}] exists {rel}")
    if not ok:
        failures.append(f"missing test {rel}")

# 索引文档本身存在且非空
ok = IDX.exists() and IDX.stat().st_size > 4000
print(f"[{'PASS' if ok else 'FAIL'}] index doc non-trivial")
n += 1
if not ok:
    failures.append("index doc")

print()
if failures:
    print(f"FAILED {len(failures)}: {failures}")
    sys.exit(1)
print(f"ALL PASS ({n} checks)")
