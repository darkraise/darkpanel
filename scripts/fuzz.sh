#!/usr/bin/env bash
# Runs the Go fuzz targets listed in the parser inventory and checks that the
# inventory and the fuzz targets in the repository agree. Uses awk and find,
# not grep: grep is ugrep on some hosts and GNU grep on others.
set -euo pipefail

synopsis='scripts/fuzz.sh [--check] [--list] [--time <go duration>] [--shard <k>/<n>] [--inventory <path>]'

usage() {
  echo "fuzz: usage: $1; $synopsis" >&2
  exit 2
}

mode=run
time=30s
shard=1/1
inventory=docs/security/parsers.md
while [ $# -gt 0 ]; do
  case $1 in
    --check) mode=check; shift ;;
    --list) mode=list; shift ;;
    --time | --shard | --inventory)
      [ $# -ge 2 ] || usage "missing value for $1"
      case $1 in
        --time) time=$2 ;;
        --shard) shard=$2 ;;
        --inventory) inventory=$2 ;;
      esac
      shift 2 ;;
    -*) usage "unknown option $1" ;;
    *) usage "unexpected argument $1" ;;
  esac
done

duration_re='^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$'
[[ $time =~ $duration_re ]] || usage "--time must be a Go duration such as 30s, got '$time'"

shard_re='^([0-9]{1,9})/([0-9]{1,9})$'
[[ $shard =~ $shard_re ]] || usage "--shard must be <k>/<n>, got '$shard'"
shard_k=$((10#${BASH_REMATCH[1]}))
shard_n=$((10#${BASH_REMATCH[2]}))
if [ "$shard_k" -lt 1 ] || [ "$shard_k" -gt "$shard_n" ]; then
  usage "--shard must satisfy 1 <= k <= n, got '$shard'"
fi

[ -f "$inventory" ] || usage "inventory '$inventory' is not a file"

# Prints one "<package> <target> <tags>" line per distinct triple of the
# '## Fuzzed parsers' table, or a single "ERR <reason>" line.
read_inventory() {
  awk -F'|' '
    in_section && /^## / { exit }
    /^## Fuzzed parsers[ \t]*$/ { in_section = 1; found = 1; next }
    in_section && /^\|/ {
      if (NF != 7) { err = "inventory row does not have five cells: " $0; exit }
      for (i = 2; i <= 6; i++) gsub(/^[ \t`]+|[ \t`]+$/, "", $i)
      if ($4 == "Package" || $4 ~ /^-+$/) next
      if ($4 == "" || $5 == "" || $6 == "") { err = "inventory row has an empty Package, Target or Tags cell: " $0; exit }
      key = $4 " " $5 " " $6
      if (!(key in seen)) { seen[key] = 1; rows[++count] = key }
    }
    END {
      if (err != "") print "ERR " err
      else if (!found) print "ERR inventory has no \"## Fuzzed parsers\" section"
      else for (i = 1; i <= count; i++) print rows[i]
    }' "$inventory"
}

triples=$(read_inventory)
case $triples in
  "ERR "*) usage "${triples#ERR }" ;;
esac

shard_triples=$(printf '%s\n' "$triples" | awk -v k="$shard_k" -v n="$shard_n" 'NF && (NR - 1) % n == k - 1')

if [ "$mode" = list ]; then
  [ -z "$shard_triples" ] || printf '%s\n' "$shard_triples"
  exit 0
fi

# Prints every problem to standard error, then "fuzz: inventory ok" when there
# is none. Returns 1 on a problem.
check_inventory() {
  local problems=0 pkg target tags dir found orphans root count
  local roots=()

  while IFS=' ' read -r pkg target tags; do
    [ -n "$pkg" ] || continue
    dir=${pkg#./}
    found=
    if [ -d "$dir" ]; then
      found=$(find "$dir" -maxdepth 1 -type f -name '*_test.go' -exec awk -v s="func $target(f *testing.F)" 'index($0, s) == 1 { print "y"; exit }' {} +)
    fi
    if [ -z "$found" ]; then
      echo "fuzz: $pkg: no target $target" >&2
      problems=1
    fi
  done <<<"$triples"

  for root in cmd internal release test; do
    if [ -d "$root" ]; then roots+=("$root"); fi
  done
  if [ ${#roots[@]} -gt 0 ]; then
    orphans=$({
      printf '%s\n' "$triples" | awk 'NF { print "I " $1 " " $2 }'
      find "${roots[@]}" -type f -name '*_test.go' -exec awk '
        /^func Fuzz[A-Za-z0-9_]*\(f \*testing\.F\)/ {
          name = $2; sub(/\(.*/, "", name)
          dir = FILENAME; sub(/\/[^\/]*$/, "", dir)
          print "./" dir " " name
        }' {} + | LC_ALL=C sort -u | awk '{ print "H " $1 " " $2 }'
    } | awk '
      $1 == "I" { inventory[$2 " " $3] = 1; next }
      $1 == "H" && !(($2 " " $3) in inventory) { print "fuzz: " $2 ": " $3 " is not in the inventory" }')
    if [ -n "$orphans" ]; then
      printf '%s\n' "$orphans" >&2
      problems=1
    fi
  fi

  [ "$problems" -eq 0 ] || return 1
  count=$(printf '%s\n' "$triples" | awk 'NF { n++ } END { print n + 0 }')
  echo "fuzz: inventory ok ($count targets)"
}

check_inventory || exit 1
if [ "$mode" = check ]; then exit 0; fi

failed=0
while IFS=' ' read -r pkg target tags; do
  [ -n "$pkg" ] || continue
  tag_args=()
  if [ "$tags" != "-" ]; then tag_args=(-tags "$tags"); fi
  if ! go test -run '^$' -fuzz "^$target\$" -fuzztime "$time" ${tag_args[@]+"${tag_args[@]}"} "$pkg"; then
    echo "fuzz: FAIL $pkg $target" >&2
    failed=1
  fi
done <<<"$shard_triples"
exit "$failed"
