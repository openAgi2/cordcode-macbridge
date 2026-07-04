#!/usr/bin/env bash
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [ -n "${CORDCODE_IOS_ROOT:-}" ]; then
  IOS_ROOT="$(cd "$CORDCODE_IOS_ROOT" 2>/dev/null && pwd || true)"
else
  IOS_ROOT="$(cd "$ROOT/../cordcode-ios" 2>/dev/null && pwd || true)"
fi

count_rg() {
  local pattern="$1"
  shift
  rg --count-matches "$pattern" "$@" 2>/dev/null | awk -F: '{sum += $NF} END {print sum + 0}'
}

line_count_over() {
  local threshold="$1"
  shift
  find "$@" -type f 2>/dev/null | while IFS= read -r file; do
    case "$file" in
      */.git/*|*/build/*|*/dist/*|*/node_modules/*|*/DerivedData/*) continue ;;
    esac
    lines="$(wc -l < "$file" | tr -d ' ')"
    if [ "$lines" -gt "$threshold" ]; then
      printf '%s:%s\n' "$file" "$lines"
    fi
  done
}

print_section() {
  printf '\n== %s ==\n' "$1"
}

printf 'CordCode architecture hygiene check (warning-only)\n'
printf 'Project root: %s\n' "$ROOT"
if [ -n "$IOS_ROOT" ]; then
  printf 'iOS mirror root: %s\n' "$IOS_ROOT"
else
  printf 'iOS mirror root: not found; skipping adjacent iOS counts\n'
fi

print_section "Logging inventory"
printf 'Swift NSLog count: %s\n' "$(count_rg 'NSLog\s*\(' "$ROOT/MacBridge" ${IOS_ROOT:+"$IOS_ROOT/OpenCodeiOS"} --glob '*.swift')"
printf 'Swift print count: %s\n' "$(count_rg '\bprint\s*\(' "$ROOT/MacBridge" ${IOS_ROOT:+"$IOS_ROOT/OpenCodeiOS"} --glob '*.swift')"
printf 'Swift os.Logger/Logger count: %s\n' "$(count_rg '\b(os\.)?Logger\s*\(' "$ROOT/MacBridge" ${IOS_ROOT:+"$IOS_ROOT/OpenCodeiOS"} --glob '*.swift')"
printf 'Go fmt.Println/log.Printf count: %s\n' "$(count_rg '\b(fmt\.Println|log\.Printf)\s*\(' "$ROOT" --glob '*.go')"
printf 'Rule: new runtime logging should use the documented boundary in docs/engineering-constitution.md.\n'

print_section "Localization inventory"
printf 'Swift lines with CJK characters: %s\n' "$(rg -n '[\p{Han}]' "$ROOT/MacBridge" ${IOS_ROOT:+"$IOS_ROOT/OpenCodeiOS"} --glob '*.swift' 2>/dev/null | wc -l | tr -d ' ')"
printf 'Rule: new user-visible text should use localization, not hard-coded UI strings.\n'

print_section "Testing injection inventory"
printf 'ForTesting occurrences: %s\n' "$(count_rg 'ForTesting' "$ROOT/MacBridge" ${IOS_ROOT:+"$IOS_ROOT/OpenCodeiOS"} --glob '*.swift')"
printf 'Rule: prefer protocol/factory injection; new ForTesting hooks require a scoped explanation.\n'

print_section "Long file inventory"
printf 'Go files over 1000 lines:\n'
line_count_over 1000 "$ROOT" -name '*.go' | sed "s#^$ROOT/##" | sort || true
printf 'Swift files over 1000 lines:\n'
if [ -n "$IOS_ROOT" ]; then
  line_count_over 1000 "$ROOT/MacBridge" "$IOS_ROOT/OpenCodeiOS" -name '*.swift' | sed "s#^$ROOT/##; s#^$IOS_ROOT/#../cordcode-ios/#" | sort || true
else
  line_count_over 1000 "$ROOT/MacBridge" -name '*.swift' | sed "s#^$ROOT/##" | sort || true
fi
printf 'TS/TSX files over 600 lines:\n'
if [ -n "$IOS_ROOT" ]; then
  line_count_over 600 "$IOS_ROOT/message-web" "$IOS_ROOT/remote-web" \( -name '*.ts' -o -name '*.tsx' \) | sed "s#^$IOS_ROOT/#../cordcode-ios/#" | sort || true
else
  printf 'skipped\n'
fi

print_section "Protocol-change reminder"
printf 'Mac protocol docs: %s files\n' "$(find "$ROOT/docs/protocol" -type f 2>/dev/null | wc -l | tr -d ' ')"
if [ -n "$IOS_ROOT" ] && [ -d "$IOS_ROOT/docs/protocol" ]; then
  printf 'iOS protocol mirror: %s files\n' "$(find "$IOS_ROOT/docs/protocol" -type f 2>/dev/null | wc -l | tr -d ' ')"
else
  printf 'iOS protocol mirror: not found\n'
fi
printf 'Rule: protocol/capability/relay changes must update docs/protocol, iOS mirror/models, targeted tests, and living docs.\n'

print_section "BridgeProvider net-growth gate"
BASELINE_FILE="$ROOT/scripts/hygiene-baseline.json"
BP_PATH="$IOS_ROOT/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift"
GATE_RAN=0
GROWTH=0

if [ ! -f "$BASELINE_FILE" ]; then
  printf 'Result: baseline file missing (%s); gate disabled.\n' "$BASELINE_FILE"
elif [ -z "$IOS_ROOT" ] || [ ! -f "$BP_PATH" ]; then
  printf 'Result: BridgeProvider.swift not measurable (iOS repo not co-located); gate skipped.\n'
else
  GATE_RAN=1
  BP_LINES="$(wc -l < "$BP_PATH" | tr -d ' ')"
  BP_FUNCS="$(grep -wo 'func' "$BP_PATH" | wc -l | tr -d ' ')"
  BP_FORTESTING="$(grep -o 'ForTesting' "$BP_PATH" | wc -l | tr -d ' ')"
  BASE_LINES="$(grep '"lines"' "$BASELINE_FILE" | head -1 | grep -oE '[0-9]+' | head -1)"
  BASE_FUNCS="$(grep '"funcs"' "$BASELINE_FILE" | head -1 | grep -oE '[0-9]+' | head -1)"
  BASE_FORTESTING="$(grep '"forTesting"' "$BASELINE_FILE" | head -1 | grep -oE '[0-9]+' | head -1)"

  printf 'BridgeProvider.swift baseline -> current:\n'
  printf '  lines:      %s -> %s\n' "$BASE_LINES" "$BP_LINES"
  printf '  funcs:      %s -> %s\n' "$BASE_FUNCS" "$BP_FUNCS"
  printf '  forTesting: %s -> %s\n' "$BASE_FORTESTING" "$BP_FORTESTING"

  if [ "$BP_LINES" -gt "$BASE_LINES" ]; then
    printf '  ❌ lines net growth (+%s)\n' "$((BP_LINES - BASE_LINES))"
    GROWTH=1
  fi
  if [ "$BP_FUNCS" -gt "$BASE_FUNCS" ]; then
    printf '  ❌ funcs net growth (+%s)\n' "$((BP_FUNCS - BASE_FUNCS))"
    GROWTH=1
  fi
  if [ "$BP_FORTESTING" -gt "$BASE_FORTESTING" ]; then
    printf '  ❌ forTesting net growth (+%s)\n' "$((BP_FORTESTING - BASE_FORTESTING))"
    GROWTH=1
  fi
fi

print_section "Gate status"
if [ "$GATE_RAN" -eq 1 ] && [ "${CORDCODE_HYGIENE_STRICT:-0}" = "1" ]; then
  if [ "$GROWTH" -eq 1 ]; then
    printf 'Result: STRICT FAILED — BridgeProvider net growth detected.\n'
    printf 'Fix: move new logic into a separate responsibility file (round 3 extract-and-test),\n'
    printf 'or update scripts/hygiene-baseline.json with a documented justification in the same PR.\n'
    exit 1
  fi
  printf 'Result: STRICT passed — no BridgeProvider net growth.\n'
  exit 0
fi
printf 'Result: warning-only (gate_ran=%s, strict=%s).\n' "$GATE_RAN" "${CORDCODE_HYGIENE_STRICT:-0}"
[ "$GROWTH" -eq 1 ] && printf '  ⚠ growth detected; set CORDCODE_HYGIENE_STRICT=1 to enforce.\n'
exit 0
