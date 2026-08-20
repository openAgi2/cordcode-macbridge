#!/bin/bash
# Isolated OpenCode 1.18.18 serve + local mock. Never touches owner managed :4096.
set -euo pipefail

HARNESS_DIR="$(cd "$(dirname "$0")" && pwd)"
OPENCODE_BIN="${OPENCODE_BIN:-/opt/homebrew/bin/opencode}"
ROOT="${OCW_GATEA_ROOT:-/tmp/ocw-gate-a-20260820}"
SERVE_PORT="${OCW_SERVE_PORT:-4398}"
MOCK_PORT="${OCW_MOCK_PORT:-4399}"
USER_NAME="${OCW_SERVE_USER:-gatea}"
PASS_WORD="${OCW_SERVE_PASS:-gatea-pass}"

if [[ ! -x "$OPENCODE_BIN" ]]; then
  echo "missing opencode binary: $OPENCODE_BIN" >&2
  exit 1
fi
if lsof -nP -iTCP:4096 -sTCP:LISTEN >/dev/null 2>&1; then
  echo "note: owner managed serve is listening on 4096; this harness will not talk to it" >&2
fi
if lsof -nP -iTCP:"$SERVE_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "port $SERVE_PORT already in use" >&2
  exit 1
fi
if lsof -nP -iTCP:"$MOCK_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "port $MOCK_PORT already in use" >&2
  exit 1
fi

ver="$("$OPENCODE_BIN" --version | tail -n1 | tr -d '[:space:]')"
if [[ "$ver" != "1.18.18" ]]; then
  echo "expected opencode 1.18.18, got $ver" >&2
  exit 1
fi

rm -rf "$ROOT"
mkdir -p "$ROOT"/{home,xdg/{config,data,cache,state},workspace,outside,logs,run}
printf 'outside-file-for-a6\n' > "$ROOT/outside/secret.txt"
printf 'workspace-readme\n' > "$ROOT/workspace/README.md"
cp "$HARNESS_DIR/opencode.json" "$ROOT/workspace/opencode.json"
python3 - <<PY
import json, pathlib, os
p = pathlib.Path("$ROOT/workspace/opencode.json")
cfg = json.loads(p.read_text())
cfg["provider"]["localmock"]["options"]["baseURL"] = "http://127.0.0.1:${MOCK_PORT}/v1"
p.write_text(json.dumps(cfg, indent=2) + "\n")
PY

export HOME="$ROOT/home"
export OPENCODE_TEST_HOME="$ROOT/home"
export XDG_CONFIG_HOME="$ROOT/xdg/config"
export XDG_DATA_HOME="$ROOT/xdg/data"
export XDG_CACHE_HOME="$ROOT/xdg/cache"
export XDG_STATE_HOME="$ROOT/xdg/state"
export OPENCODE_SERVER_USERNAME="$USER_NAME"
export OPENCODE_SERVER_PASSWORD="$PASS_WORD"
# Drop inherited provider credentials so the isolated serve cannot bill.
# Do not copy host ~/.config/opencode or auth.json into this tree.
unset OPENAI_API_KEY ANTHROPIC_API_KEY AZURE_OPENAI_API_KEY GROQ_API_KEY
unset OPENROUTER_API_KEY XAI_API_KEY DEEPSEEK_API_KEY GOOGLE_GENERATIVE_AI_API_KEY
unset GOOGLE_API_KEY GEMINI_API_KEY TOGETHER_API_KEY MISTRAL_API_KEY
unset OPENCODE_API_KEY GITHUB_TOKEN COPILOT_GITHUB_TOKEN GH_TOKEN
unset ZHIPU_API_KEY DASHSCOPE_API_KEY MOONSHOT_API_KEY MINIMAX_API_KEY
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN
unset VERTEX_PROJECT GOOGLE_APPLICATION_CREDENTIALS
export HTTP_PROXY="" HTTPS_PROXY="" ALL_PROXY="" NO_PROXY="*"
export OCW_MOCK_PORT="$MOCK_PORT"
export OCW_OUTSIDE_FILE="$ROOT/outside/secret.txt"
export OCW_MOCK_LOG="$ROOT/logs/mock.jsonl"
# Record owner managed listener so health check can prove we did not take it over.
lsof -nP -iTCP:4096 -sTCP:LISTEN > "$ROOT/run/managed-4096-before.txt" 2>/dev/null || true

cleanup_on_fail() {
  echo "start_sandbox failed; recycling 4398/4399" >&2
  OCW_GATEA_ROOT="$ROOT" "$HARNESS_DIR/stop_sandbox.sh" || true
}
trap cleanup_on_fail ERR

python3 "$HARNESS_DIR/mock_provider.py" >"$ROOT/logs/mock.stdout" 2>"$ROOT/logs/mock.stderr" &
echo $! > "$ROOT/run/mock.pid"

# Bind serve to isolated HOME/XDG so ~/.config/opencode credentials cannot leak.
# cwd is the sandbox worktree so directory-scoped routes match official Web.
(
  cd "$ROOT/workspace"
  exec "$OPENCODE_BIN" serve --pure --hostname 127.0.0.1 --port "$SERVE_PORT" --print-logs
) >"$ROOT/logs/serve.stdout" 2>"$ROOT/logs/serve.stderr" &
echo $! > "$ROOT/run/serve.pid"

ok=0
for i in $(seq 1 60); do
  if curl -sf -u "$USER_NAME:$PASS_WORD" --max-time 1 "http://127.0.0.1:${SERVE_PORT}/global/health" >/tmp/ocw-gate-a-health.json 2>/dev/null; then
    ok=1
    break
  fi
  sleep 0.5
done
if [[ "$ok" != 1 ]]; then
  echo "serve did not become healthy within 30s" >&2
  tail -n 80 "$ROOT/logs/serve.stderr" >&2 || true
  exit 1
fi
trap - ERR

{
  echo "ROOT=$ROOT"
  echo "SERVE=http://127.0.0.1:${SERVE_PORT}"
  echo "MOCK=http://127.0.0.1:${MOCK_PORT}"
  echo "USER=$USER_NAME"
  echo "WORKSPACE=$ROOT/workspace"
  echo "HEALTH=$(cat /tmp/ocw-gate-a-health.json)"
} | tee "$ROOT/run/env.txt"

# Refuse if we accidentally bound 4096.
if [[ "$(lsof -nP -iTCP:4096 -sTCP:LISTEN -F p 2>/dev/null | head -1 || true)" == "p$$" ]]; then
  echo "refusing to own port 4096" >&2
  exit 1
fi
