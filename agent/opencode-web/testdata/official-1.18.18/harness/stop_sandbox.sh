#!/bin/bash
set -euo pipefail
ROOT="${OCW_GATEA_ROOT:-/tmp/ocw-gate-a-20260820}"
for name in serve mock; do
  pidfile="$ROOT/run/${name}.pid"
  if [[ -f "$pidfile" ]]; then
    pid="$(cat "$pidfile")"
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      sleep 0.2
      kill -9 "$pid" 2>/dev/null || true
    fi
    rm -f "$pidfile"
  fi
done
# Do not kill owner managed opencode on 4096.
if lsof -nP -iTCP:4398 -sTCP:LISTEN >/dev/null 2>&1; then
  pids="$(lsof -nP -iTCP:4398 -sTCP:LISTEN -t || true)"
  if [[ -n "$pids" ]]; then
    kill $pids 2>/dev/null || true
  fi
fi
if lsof -nP -iTCP:4399 -sTCP:LISTEN >/dev/null 2>&1; then
  pids="$(lsof -nP -iTCP:4399 -sTCP:LISTEN -t || true)"
  if [[ -n "$pids" ]]; then
    kill $pids 2>/dev/null || true
  fi
fi
# Wait until harness ports are actually free. Never touch 4096.
for port in 4398 4399; do
  for _ in $(seq 1 20); do
    if ! lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "warn: port $port still listening after stop" >&2
  fi
done
echo "stopped sandbox (left $ROOT on disk for evidence)"
