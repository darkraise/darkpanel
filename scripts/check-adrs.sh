#!/usr/bin/env bash
# Structural check for architecture decision records. Uses awk, not grep:
# grep is ugrep on some hosts and GNU grep on others.
set -euo pipefail

adr_dir=${ADR_DIR:-docs/adr}
complete=0
if [ "${1:-}" = "--complete" ]; then complete=1; shift; fi
errors=0
fail() { echo "check-adrs: $1" >&2; errors=$((errors + 1)); }

check_adr() {
  local f=$1 msg
  if [ ! -f "$f" ]; then fail "$f: no such file"; return; fi
  while IFS= read -r msg; do fail "$f: $msg"; done < <(awk -v q="'" '
    BEGIN { split("## Status|## Context|## Decision|## Alternatives considered|## Consequences", req, "|") }
    NR == 1 && $0 !~ /^# ADR [0-9][0-9][0-9][0-9]: [^ ]/ { print "missing title line " q "# ADR NNNN: <title>" q }
    /^\*\*Requirements:\*\* (R[0-9]|none)/ { reqline = 1 }
    /^## / { cur = $0; pos[cur] = ++n; filled[cur] = 0; next }
    cur != "" && NF { filled[cur]++; if (cur == "## Status" && status == "") status = $0 }
    END {
      if (!reqline) print "missing **Requirements:** line"
      last = 0
      for (i = 1; i <= 5; i++) {
        h = req[i]
        if (!(h in pos)) { print "missing heading " q h q; continue }
        if (pos[h] < last) print "heading " q h q " out of order"
        last = pos[h]
        if (!filled[h]) print "empty section " q h q
      }
      if (status != "Proposed" && status != "Accepted" && status !~ /^Superseded by ADR [0-9][0-9][0-9][0-9]/)
        print "status must be Proposed, Accepted or " q "Superseded by ADR NNNN" q ", got " q status q
    }' "$f")
}

# Prints "R<n> <NNNN>" for every requirement/ADR pair in README's Traceability table.
trace_pairs() {
  awk '
    /^## / { t = ($0 == "## Traceability"); next }
    t && /^\| *R[0-9]/ {
      split($0, cell, "|")
      nr = split(cell[2], rs, /[^A-Za-z0-9]+/)
      na = split(cell[3], as, /[^0-9]+/)
      for (i = 1; i <= nr; i++) if (rs[i] ~ /^R[0-9]+$/)
        for (j = 1; j <= na; j++) if (as[j] ~ /^[0-9][0-9][0-9][0-9]$/) print rs[i], as[j]
    }' "$1"
}

lists_requirement() {
  awk -v r="$2" '/^\*\*Requirements:\*\*/ { n = split($0, a, /[ ,]+/); for (i = 1; i <= n; i++) if (a[i] == r) f = 1 } END { exit !f }' "$1"
}

if [ $# -gt 0 ]; then
  files=("$@")
else
  shopt -s nullglob
  files=("$adr_dir"/[0-9][0-9][0-9][0-9]-*.md)
fi
for f in "${files[@]}"; do check_adr "$f"; done

if [ $# -eq 0 ]; then
  index="$adr_dir/README.md"
  if [ ! -f "$index" ]; then
    fail "$index: missing"
  else
    for f in "${files[@]}"; do
      b=$(basename "$f")
      awk -v l="($b)" 'index($0, l) { found = 1 } END { exit !found }' "$index" \
        || fail "$index: does not link $b"
    done
    if [ "$complete" -eq 1 ]; then
      while read -r l; do
        [ -f "$adr_dir/$l" ] || fail "$index: links missing file $l"
      done < <(awk '{ while (match($0, /\([0-9][0-9][0-9][0-9]-[a-z0-9-]+\.md\)/)) { print substr($0, RSTART + 1, RLENGTH - 2); $0 = substr($0, RSTART + RLENGTH) } }' "$index" | sort -u)
      while read -r r n; do
        adr=("$adr_dir"/"$n"-*.md)
        if [ ${#adr[@]} -eq 0 ] || [ ! -f "${adr[0]}" ]; then fail "$index: traceability names missing ADR $n"; continue; fi
        lists_requirement "${adr[0]}" "$r" || fail "$index: traceability maps $r to ADR $n, but its **Requirements:** line lacks $r"
      done < <(trace_pairs "$index")
    fi
  fi
fi

[ "$errors" -eq 0 ]
