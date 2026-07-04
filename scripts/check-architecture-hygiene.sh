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

print_section "Net-growth gates (per-file baselines)"
BASELINE_FILE="$ROOT/scripts/hygiene-baseline.json"
GATE_RAN=0
GROWTH=0

# 遍历 hygiene-baseline.json 中每个 baseline 条目（除 _comment* 元数据键外），
# 对每个条目按 path / lines / funcs / forTesting 比对当前实测值，任一净增即记 growth。
# path 是相对 iOS repo root（或 MacBridge root，以能解析到文件为准）。
check_baseline_entry() {
  local key="$1"
  local relpath="$2"
  local base_lines="$3"
  local base_funcs="$4"
  local base_fortesting="$5"

  # relpath 以 ../cordcode-ios/ 开头时解析到 IOS_ROOT，否则解析到 ROOT（MacBridge）。
  local target=""
  case "$relpath" in
    ../cordcode-ios/*)
      if [ -z "$IOS_ROOT" ]; then return 0; fi
      target="$IOS_ROOT/${relpath#../cordcode-ios/}"
      ;;
    *)
      target="$ROOT/$relpath"
      ;;
  esac
  if [ ! -f "$target" ]; then
    printf '  %s: file not measurable (%s); skipped\n' "$key" "$relpath"
    return 0
  fi
  GATE_RAN=1
  local cur_lines cur_funcs cur_fortesting
  cur_lines="$(wc -l < "$target" | tr -d ' ')"
  cur_funcs="$(grep -wo 'func' "$target" | wc -l | tr -d ' ')"
  cur_fortesting="$(grep -o 'ForTesting' "$target" | wc -l | tr -d ' ')"

  printf '  %s (%s)\n' "$key" "$relpath"
  printf '    lines:      %s -> %s\n' "$base_lines" "$cur_lines"
  printf '    funcs:      %s -> %s\n' "$base_funcs" "$cur_funcs"
  if [ -n "$base_fortesting" ]; then
    printf '    forTesting: %s -> %s\n' "$base_fortesting" "$cur_fortesting"
  fi

  if [ "$cur_lines" -gt "$base_lines" ]; then
    printf '    ❌ lines net growth (+%s)\n' "$((cur_lines - base_lines))"
    GROWTH=1
  fi
  if [ "$cur_funcs" -gt "$base_funcs" ]; then
    printf '    ❌ funcs net growth (+%s)\n' "$((cur_funcs - base_funcs))"
    GROWTH=1
  fi
  if [ -n "$base_fortesting" ] && [ "$cur_fortesting" -gt "$base_fortesting" ]; then
    printf '    ❌ forTesting net growth (+%s)\n' "$((cur_fortesting - base_fortesting))"
    GROWTH=1
  fi
}

if [ ! -f "$BASELINE_FILE" ]; then
  printf 'Result: baseline file missing (%s); gate disabled.\n' "$BASELINE_FILE"
elif ! command -v python3 >/dev/null 2>&1; then
  # 无 python3 时回落到旧 BridgeProvider-only 逻辑（兼容环境）。
  BP_PATH="$IOS_ROOT/OpenCodeiOS/OpenCodeiOS/Services/Bridge/BridgeProvider.swift"
  if [ -n "$IOS_ROOT" ] && [ -f "$BP_PATH" ]; then
    GATE_RAN=1
    BP_LINES="$(wc -l < "$BP_PATH" | tr -d ' ')"
    BASE_LINES="$(grep '"lines"' "$BASELINE_FILE" | head -1 | grep -oE '[0-9]+' | head -1)"
    printf '  BridgeProvider (fallback, no python3): %s lines (baseline %s)\n' "$BP_LINES" "$BASE_LINES"
    [ "$BP_LINES" -gt "$BASE_LINES" ] && GROWTH=1
  fi
else
  # 用 python3 解析 JSON 并遍历所有 baseline 条目。
  while IFS=$'\t' read -r key relpath base_lines base_funcs base_fortesting; do
    check_baseline_entry "$key" "$relpath" "$base_lines" "$base_funcs" "$base_fortesting"
  done < <(python3 - "$BASELINE_FILE" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
for key, entry in data.items():
    if key.startswith("_comment"):
        continue
    if not isinstance(entry, dict) or "path" not in entry:
        continue
    print("\t".join([
        key,
        entry.get("path", ""),
        str(entry.get("lines", 0)),
        str(entry.get("funcs", 0)),
        str(entry.get("forTesting", "")) if "forTesting" in entry else "",
    ]))
PY
)
fi

print_section "Gate status"
if [ "$GATE_RAN" -eq 1 ] && [ "${CORDCODE_HYGIENE_STRICT:-0}" = "1" ]; then
  if [ "$GROWTH" -eq 1 ]; then
    printf 'Result: STRICT FAILED — net growth detected in one or more baseline files.\n'
    printf 'Fix: move new logic into a separate responsibility file (e.g. ChatTurnSyncPolicy/State),\n'
    printf 'or update scripts/hygiene-baseline.json with a documented justification in the same PR.\n'
    exit 1
  fi
  printf 'Result: STRICT passed — no net growth across all baseline files.\n'
  exit 0
fi
printf 'Result: warning-only (gate_ran=%s, strict=%s).\n' "$GATE_RAN" "${CORDCODE_HYGIENE_STRICT:-0}"
[ "$GROWTH" -eq 1 ] && printf '  ⚠ growth detected; set CORDCODE_HYGIENE_STRICT=1 to enforce.\n'
exit 0
