#!/usr/bin/env bash
# Tests for scripts/check-adrs.sh. Each case builds its own ADR directory.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
checker="$here/check-adrs.sh"
pass=0; failures=0

# adr <file> [section to leave empty] [order: normal|swapped] [title]
adr() {
  local f=$1 empty=${2:-} order=${3:-normal} title=${4:-"# ADR 0001: Example"} h
  local heads=("## Status" "## Context" "## Decision" "## Alternatives considered" "## Consequences")
  [ "$order" = swapped ] && heads=("## Status" "## Decision" "## Context" "## Alternatives considered" "## Consequences")
  { printf '%s\n\n**Requirements:** R1, R2\n' "$title"
    for h in "${heads[@]}"; do
      printf '\n%s\n\n' "$h"
      if [ "$h" = "$empty" ]; then continue; fi
      if [ "$h" = "## Status" ]; then echo Proposed; else echo Text.; fi
    done; } > "$f"
}

expect() { # expect <name> <want exit> <stderr substring or -> <command...>
  local name=$1 want=$2 needle=$3; shift 3
  local err rc
  err=$("$@" 2>&1 >/dev/null); rc=$?
  if [ "$rc" -ne "$want" ]; then
    echo "FAIL $name: exit $rc, want $want; stderr: $err"; failures=$((failures+1)); return
  fi
  if [ "$needle" != "-" ] && [[ "$err" != *"$needle"* ]]; then
    echo "FAIL $name: stderr lacks '$needle': $err"; failures=$((failures+1)); return
  fi
  pass=$((pass+1))
}

d=$(mktemp -d); adr "$d/0001-example.md"
printf '# Index\n\n- [ADR 0001: Example](0001-example.md)\n' > "$d/README.md"
expect valid_set 0 - env ADR_DIR="$d" bash "$checker"

d=$(mktemp -d)
expect no_such_file 1 "no such file" env ADR_DIR="$d" bash "$checker" "$d/0009-absent.md"

d=$(mktemp -d); adr "$d/0001-example.md"
awk '$0!="## Consequences"' "$d/0001-example.md" > "$d/x" && mv "$d/x" "$d/0001-example.md"
expect missing_heading 1 "missing heading '## Consequences'" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md"
awk '!/^\*\*Requirements:/' "$d/0001-example.md" > "$d/x" && mv "$d/x" "$d/0001-example.md"
expect missing_requirements 1 "missing **Requirements:** line" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md"
awk '{ if ($0=="Proposed") print "Draft"; else print }' "$d/0001-example.md" > "$d/x" && mv "$d/x" "$d/0001-example.md"
expect bad_status 1 "status must be" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md" "" normal "# Example"
expect missing_title 1 "missing title line" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md" "" swapped
expect out_of_order 1 "out of order" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md" "## Decision"
expect empty_section 1 "empty section '## Decision'" env ADR_DIR="$d" bash "$checker" "$d/0001-example.md"

d=$(mktemp -d); adr "$d/0001-example.md"
printf '# Index\n' > "$d/README.md"
expect index_missing_link 1 "does not link 0001-example.md" env ADR_DIR="$d" bash "$checker"

d=$(mktemp -d); adr "$d/0001-example.md"
expect index_missing_file 1 "README.md: missing" env ADR_DIR="$d" bash "$checker"

d=$(mktemp -d); adr "$d/0001-example.md"
printf -- '- [ADR 0001: Example](0001-example.md)\n- [ADR 0002: Later](0002-later.md)\n' > "$d/README.md"
expect index_ignores_missing_without_complete 0 - env ADR_DIR="$d" bash "$checker"
expect complete_reports_missing 1 "links missing file 0002-later.md" env ADR_DIR="$d" bash "$checker" --complete

d=$(mktemp -d); adr "$d/0001-example.md"
printf -- '- [ADR 0001: Example](0001-example.md)\n\n## Traceability\n\n| Requirement | ADRs |\n|---|---|\n| R1, R2 | 0001 (Decision) |\n' > "$d/README.md"
expect trace_ok 0 - env ADR_DIR="$d" bash "$checker" --complete
printf '| R7 | 0001 (Decision) |\n' >> "$d/README.md"
expect trace_pair_missing 1 "maps R7 to ADR 0001" env ADR_DIR="$d" bash "$checker" --complete
printf '| R2 | 0004 |\n' >> "$d/README.md"
expect trace_missing_adr 1 "names missing ADR 0004" env ADR_DIR="$d" bash "$checker" --complete

echo "check-adrs tests: $pass passed, $failures failed"
[ "$failures" -eq 0 ]
