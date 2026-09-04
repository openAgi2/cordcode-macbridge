#!/usr/bin/env bash
# Captures the provider env of a live production-spawned claude process (best)
# or the cordcode-bridge-runtime process (fallback) for probe reuse.
# Writes runtime-env.mirror (0600, gitignored). Never prints values.
set -euo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
OUT="$DIR/runtime-env.mirror"

PICK=""
CLAUDE_PID="$(pgrep -f 'claude .*--input-format stream-json' | head -1 || true)"
RUNTIME_PID="$(pgrep -f 'cordcode-bridge-runtime' | head -1 || true)"
if [[ -n "$CLAUDE_PID" ]]; then
  PICK="$CLAUDE_PID"; SRC="live claude child (production spawn env, exact)"
elif [[ -n "$RUNTIME_PID" ]]; then
  PICK="$RUNTIME_PID"; SRC="cordcode-bridge-runtime process env (approximation; provider layer merged at spawn)"
else
  echo "no live claude or runtime process found" >&2
  exit 1
fi

# ps eww splits on spaces; env values containing spaces would be truncated.
# Current provider values (keys/URLs/model ids) contain none.
ps eww -o command= -p "$PICK" 2>/dev/null \
  | tr ' ' '\n' \
  | grep -E '^(ANTHROPIC_[A-Za-z0-9_]+|CLAUDE_CODE_[A-Za-z0-9_]+|CLAUDE_[A-Za-z0-9_]+|NO_PROXY|DISABLE_[A-Za-z0-9_]+)=' \
  | grep -v '^CLAUDE_CODE_ENTRYPOINT=' \
  > "$OUT" || true
chmod 600 "$OUT"

echo "source=$SRC pid=$PICK vars=$(wc -l < "$OUT" | tr -d ' ')"
