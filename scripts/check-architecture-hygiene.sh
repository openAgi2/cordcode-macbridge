#!/usr/bin/env bash
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IOS_ROOT="$(cd "$ROOT/../cordcode-ios" 2>/dev/null && pwd || true)"

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

print_section "Gate status"
printf 'Result: warning-only. Existing inventory is reported but does not fail this script.\n'
exit 0
