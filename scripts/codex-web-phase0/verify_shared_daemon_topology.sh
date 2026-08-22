#!/bin/bash
set -euo pipefail

codex_home="${CODEX_HOME:-$HOME/.codex}"
desktop_app="${CODEX_DESKTOP_APP:-/Applications/ChatGPT.app}"
standalone="$codex_home/packages/standalone/current/codex"
desktop_cli="$desktop_app/Contents/Resources/codex"
socket_path="$codex_home/app-server-control/app-server-control.sock"
desktop_pid="${CODEX_DESKTOP_PID:-}"
runtime_pid="${CORDCODE_RUNTIME_PID:-}"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

test -x "$standalone" || fail "official standalone missing: $standalone"
test -x "$desktop_cli" || fail "Desktop CLI missing: $desktop_cli"
test -S "$socket_path" || fail "official daemon control socket missing: $socket_path"

standalone_version="$($standalone --version)"
desktop_version="$($desktop_cli --version)"
test "$standalone_version" = "$desktop_version" || fail "version mismatch: standalone=$standalone_version desktop=$desktop_version"

attach_env="$(launchctl getenv CODEX_APP_SERVER_USE_LOCAL_DAEMON || true)"
test "$attach_env" = "1" || fail "launchd attach environment is not 1"

daemon_json="$(CODEX_HOME="$codex_home" "$standalone" app-server daemon version)"
echo "$daemon_json" | rg -q '"status":"running"' || fail "daemon is not running: $daemon_json"

daemon_pid="$(lsof -t "$socket_path" | head -1)"
test -n "$daemon_pid" || fail "no daemon listener owns $socket_path"

managed_count="$(ps -axo command= | awk 'index($0, "app-server --listen ws://" "127.0.0.1:") {count++} END {print count+0}')"
test "$managed_count" = "0" || fail "managed-loopback product process still exists"

daemon_objects="$(lsof -n -P -a -p "$daemon_pid" -U | awk -v socket="$socket_path" '$0 ~ socket {print $6}')"
test -n "$daemon_objects" || fail "daemon socket objects unavailable"

matching_peer_count() {
  local pid="$1"
  local peers
  peers="$(lsof -n -P -a -p "$pid" -U | awk '$NF ~ /^->0x/ {sub(/^->/, "", $NF); print $NF}')"
  awk 'NR==FNR {objects[$1]=1; next} objects[$1] {count++} END {print count+0}' \
    <(printf '%s\n' "$daemon_objects") <(printf '%s\n' "$peers")
}

if test -n "$desktop_pid"; then
  ps -p "$desktop_pid" -o command= | rg -q "$desktop_app/Contents/MacOS/ChatGPT" || fail "PID $desktop_pid is not the requested Desktop app"
  private_children="$(ps -axo ppid=,command= | awk -v parent="$desktop_pid" '$1 == parent && index($0, "/Contents/Resources/codex") && index($0, "app-server") {count++} END {print count+0}')"
  test "$private_children" = "0" || fail "Desktop PID $desktop_pid still owns a private app-server"
  desktop_peers="$(matching_peer_count "$desktop_pid")"
  test "$desktop_peers" -ge 1 || fail "Desktop PID $desktop_pid has no peer on the official daemon"
  echo "desktop_shared_peers=$desktop_peers"
fi

if test -n "$runtime_pid"; then
  ps -p "$runtime_pid" -o command= | rg -q '/CordCodeLink.app/Contents/Resources/cordcode-bridge-runtime' || fail "PID $runtime_pid is not installed CordCode runtime"
  runtime_peers="$(matching_peer_count "$runtime_pid")"
  test "$runtime_peers" -ge 2 || fail "CordCode runtime PID $runtime_pid has fewer than two daemon peers"
  echo "cordcode_shared_peers=$runtime_peers"
fi

echo "standalone_version=$standalone_version"
echo "desktop_version=$desktop_version"
echo "daemon_pid=$daemon_pid"
echo "managed_loopback_count=0"
echo "topology_gate=PASS"
