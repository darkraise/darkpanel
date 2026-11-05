#!/usr/bin/env bash
# shellcheck disable=SC2016 # the fixture files hold literal backticks
# Tests for scripts/fuzz.sh. Each case builds its own fixture tree under a
# temporary directory and runs the script from there, so no case depends on
# the repository's real inventory.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
runner="$here/fuzz.sh"
pass=0; failures=0
work=$(mktemp -d)
trap 'cd / && rm -rf "$work"' EXIT

if [ ! -f "$runner" ]; then echo "FAIL: $runner does not exist"; exit 1; fi

fz_note() { echo "FAIL $1: $2"; failures=$((failures + 1)); }

# fz_inventory <dir> <row>...: writes <dir>/parsers.md; a row is
# "<package> <target> <tags>" and becomes one table row.
fz_inventory() {
  local dir=$1 row pkg target tags; shift
  {
    printf '# Parsers\n\nIntroduction.\n\n## Fuzzed parsers\n\n'
    printf '| Parser | Input | Package | Target | Tags |\n|---|---|---|---|---|\n'
    for row in "$@"; do
      read -r pkg target tags <<<"$row"
      printf '| `p.%s` | input | `%s` | `%s` | %s |\n' "$target" "$pkg" "$target" "$tags"
    done
    printf '\n## Not fuzzed\n\n| Parser | Input | Reason |\n|---|---|---|\n| `q.Read` | file | not a parser |\n'
  } > "$dir/parsers.md"
}

# fz_package <dir> <package dir> <target>...: writes a test file holding one
# fuzz target per name whose body accepts every input.
fz_package() {
  local dir=$1 pkgdir=$2 target; shift 2
  mkdir -p "$dir/$pkgdir"
  {
    printf 'package %s\n\nimport "testing"\n' "$(basename "$pkgdir")"
    for target in "$@"; do
      printf '\nfunc %s(f *testing.F) {\n\tf.Add([]byte("x"))\n\tf.Fuzz(func(t *testing.T, b []byte) {})\n}\n' "$target"
    done
  } > "$dir/$pkgdir/x_test.go"
}

# fz_tree <name>: makes a fixture directory with a go.mod and prints it.
fz_tree() {
  local dir="$work/$1"
  mkdir -p "$dir"
  printf 'module example.com/fz\n\ngo 1.20\n' > "$dir/go.mod"
  echo "$dir"
}

# fz_fakego <dir> [reads]: puts a fake go command in <dir>/bin that logs its
# arguments to <dir>/go.log and fails when they name FuzzBad; with reads it
# first reads all of its standard input.
fz_fakego() {
  mkdir -p "$1/bin"
  {
    printf '#!/usr/bin/env bash\n'
    if [ "${2:-}" = reads ]; then printf 'cat >/dev/null\n'; fi
    printf 'echo "$*" >> "%s/go.log"\ncase "$*" in *FuzzBad*) exit 1;; esac\n' "$1"
  } > "$1/bin/go"
  chmod +x "$1/bin/go"
}

# fz_failing_find <dir>: puts a find command in <dir>/bin that fails when it
# is asked to walk one of the roots the orphan scan walks, and otherwise runs
# the real find.
fz_failing_find() {
  local real; real=$(command -v find)
  mkdir -p "$1/bin"
  printf '#!/usr/bin/env bash\ncase "$1" in cmd | internal | release | test) echo "find: walk failed" >&2; exit 1;; esac\nexec %s "$@"\n' "$real" > "$1/bin/find"
  chmod +x "$1/bin/find"
}

# fz_expect <name> <want exit> <output substring or -> <command...>: runs the
# command from the current directory and checks its exit status and output.
fz_expect() {
  local name=$1 want=$2 needle=$3 out rc; shift 3
  out=$("$@" 2>&1); rc=$?
  if [ "$rc" -ne "$want" ]; then fz_note "$name" "exit $rc, want $want; output: $out"; return; fi
  if [ "$needle" != "-" ] && [[ "$out" != *"$needle"* ]]; then fz_note "$name" "output lacks '$needle': $out"; return; fi
  pass=$((pass + 1))
}

# fz_equal <name> <got> <want>
fz_equal() {
  if [ "$2" != "$3" ]; then fz_note "$1" "got [$2], want [$3]"; else pass=$((pass + 1)); fi
}

fz_check_ok() {
  local d; d=$(fz_tree check_ok)
  fz_inventory "$d" "./internal/alpha FuzzAlpha -" "./internal/alpha FuzzAlpha -" "./internal/beta FuzzParse harness" "./internal/beta FuzzParseRestore -"
  fz_package "$d" internal/alpha FuzzAlpha
  fz_package "$d" internal/beta FuzzParse FuzzParseRestore
  cd "$d" || return
  fz_expect check_ok 0 "fuzz: inventory ok (3 targets)" bash "$runner" --check --inventory parsers.md
}

fz_check_missing_target() {
  local d; d=$(fz_tree check_missing)
  fz_inventory "$d" "./internal/alpha FuzzAlpha -" "./internal/beta FuzzParse -" "./internal/gamma FuzzGone -" "./internal/delta FuzzSub -"
  fz_package "$d" internal/alpha FuzzAlpha
  # FuzzParseRestore does not satisfy a row for FuzzParse.
  fz_package "$d" internal/beta FuzzParseRestore
  # A target in a subdirectory does not satisfy a row for the parent package.
  fz_package "$d" internal/delta/sub FuzzSub
  cd "$d" || return
  fz_expect missing_prefix_only 1 "fuzz: ./internal/beta: no target FuzzParse" bash "$runner" --check --inventory parsers.md
  fz_expect missing_no_package 1 "fuzz: ./internal/gamma: no target FuzzGone" bash "$runner" --check --inventory parsers.md
  fz_expect missing_subdirectory 1 "fuzz: ./internal/delta: no target FuzzSub" bash "$runner" --check --inventory parsers.md
  fz_equal missing_lines "$(bash "$runner" --check --inventory parsers.md 2>&1 | grep -c '^fuzz: .*no target ')" 3
}

fz_check_orphan_target() {
  local d; d=$(fz_tree check_orphan)
  fz_inventory "$d" "./internal/alpha FuzzParse -"
  fz_package "$d" internal/alpha FuzzParse
  # The same name in another package is an orphan: a row names a target and its package.
  fz_package "$d" internal/beta FuzzParse
  fz_package "$d" internal/gamma FuzzOrphan
  fz_package "$d" release/tool FuzzRelease
  fz_package "$d" test/harness FuzzHarness
  fz_package "$d" cmd/tool FuzzCmd
  # Outside cmd, internal, release and test a fuzz target is not the check's business.
  fz_package "$d" web/other FuzzIgnored
  cd "$d" || return
  fz_expect orphan_other_package 1 "fuzz: ./internal/beta: FuzzParse is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_expect orphan_internal 1 "fuzz: ./internal/gamma: FuzzOrphan is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_expect orphan_release 1 "fuzz: ./release/tool: FuzzRelease is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_expect orphan_test 1 "fuzz: ./test/harness: FuzzHarness is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_expect orphan_cmd 1 "fuzz: ./cmd/tool: FuzzCmd is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_equal orphan_lines "$(bash "$runner" --check --inventory parsers.md 2>&1 | grep -c '^fuzz: ')" 5
  fz_equal orphan_outside_roots "$(bash "$runner" --check --inventory parsers.md 2>&1 | grep -c 'FuzzIgnored')" 0
}

fz_check_scan_failure() {
  local d; d=$(fz_tree check_scan)
  fz_failing_find "$d"
  fz_inventory "$d" "./internal/alpha FuzzAlpha -"
  fz_package "$d" internal/alpha FuzzAlpha
  cd "$d" || return
  fz_expect scan_failure 1 "fuzz: the scan for fuzz targets missing from the inventory failed" env PATH="$d/bin:$PATH" bash "$runner" --check --inventory parsers.md
  fz_equal scan_failure_not_ok "$(env PATH="$d/bin:$PATH" bash "$runner" --check --inventory parsers.md 2>&1 | grep -c 'inventory ok')" 0
}

fz_check_parameter_names() {
  local d; d=$(fz_tree check_params)
  fz_inventory "$d" "./internal/alpha FuzzNamedT -"
  mkdir -p "$d/internal/alpha" "$d/internal/beta"
  printf 'package alpha\n\nimport "testing"\n\nfunc FuzzNamedT(t *testing.F) {\n\tt.Fuzz(func(*testing.T, []byte) {})\n}\n' > "$d/internal/alpha/x_test.go"
  printf 'package beta\n\nimport "testing"\n\nfunc FuzzOther(ff *testing.F) {\n\tff.Fuzz(func(*testing.T, []byte) {})\n}\n' > "$d/internal/beta/x_test.go"
  cd "$d" || return
  fz_expect params_orphan 1 "fuzz: ./internal/beta: FuzzOther is not in the inventory" bash "$runner" --check --inventory parsers.md
  fz_equal params_row_found "$(bash "$runner" --check --inventory parsers.md 2>&1 | grep -c 'no target')" 0
}

fz_check_crlf_inventory() {
  local d; d=$(fz_tree check_crlf)
  fz_inventory "$d" "./internal/alpha FuzzAlpha -"
  fz_package "$d" internal/alpha FuzzAlpha
  awk '{ printf "%s\r\n", $0 }' "$d/parsers.md" > "$d/crlf.md"
  cd "$d" || return
  fz_expect crlf_check 0 "fuzz: inventory ok (1 targets)" bash "$runner" --check --inventory crlf.md
  fz_equal crlf_list "$(bash "$runner" --list --inventory crlf.md)" "./internal/alpha FuzzAlpha -"
}

fz_list_and_shard() {
  local d; d=$(fz_tree list)
  fz_inventory "$d" "./a FuzzA -" "./a FuzzA -" "./b FuzzB harness" "./c FuzzC -" "./d FuzzD -" "./e FuzzE harness,extra"
  cd "$d" || return
  fz_equal list_all "$(bash "$runner" --list --inventory parsers.md)" "$(printf './a FuzzA -\n./b FuzzB harness\n./c FuzzC -\n./d FuzzD -\n./e FuzzE harness,extra')"
  fz_equal list_shard_2_of_3 "$(bash "$runner" --list --shard 2/3 --inventory parsers.md)" "$(printf './b FuzzB harness\n./e FuzzE harness,extra')"
  fz_equal list_shard_1_of_3 "$(bash "$runner" --list --shard 1/3 --inventory parsers.md)" "$(printf './a FuzzA -\n./d FuzzD -')"
  fz_equal list_shard_3_of_3 "$(bash "$runner" --list --shard 3/3 --inventory parsers.md)" "./c FuzzC -"
  fz_equal list_shard_1_of_1 "$(bash "$runner" --list --shard 1/1 --inventory parsers.md | wc -l | tr -d ' ')" 5
  fz_equal list_more_shards_than_targets "$(bash "$runner" --list --shard 7/9 --inventory parsers.md | wc -l | tr -d ' ')" 0
  # --list reads only the inventory: no package exists in this fixture.
  fz_expect list_exit 0 - bash "$runner" --list --inventory parsers.md
}

fz_usage() {
  local d; d=$(fz_tree usage)
  fz_inventory "$d" "./a FuzzA -"
  printf '# Parsers\n\n## Not fuzzed\n\n' > "$d/nosection.md"
  printf '# Parsers\n\n## Fuzzed parsers\n\n| Parser | Input | Package | Target | Tags |\n|---|---|---|---|---|\n| `p.A` | input | `./a` |\n' > "$d/short.md"
  printf '# Parsers\n\n## Fuzzed parsers\n\n| Parser | Input | Package | Target | Tags |\n|---|---|---|---|---|\n| `p.A` | input | `./a` |  | - |\n' > "$d/notarget.md"
  printf '# Parsers\n\n## Fuzzed parsers\n\n| Parser | Input | Package | Target | Tags |\n|---|---|---|---|---|\n| `p.A` | input | `./a` | `FuzzA` |  |\n' > "$d/notags.md"
  cd "$d" || return
  fz_expect usage_unknown_option 2 "fuzz: usage:" bash "$runner" --nope --inventory parsers.md
  fz_expect usage_shard_zero 2 "fuzz: usage:" bash "$runner" --list --shard 0/3 --inventory parsers.md
  fz_expect usage_shard_above_count 2 "fuzz: usage:" bash "$runner" --list --shard 4/3 --inventory parsers.md
  fz_expect usage_shard_no_slash 2 "fuzz: usage:" bash "$runner" --list --shard 2 --inventory parsers.md
  fz_expect usage_shard_not_numbers 2 "fuzz: usage:" bash "$runner" --list --shard a/b --inventory parsers.md
  fz_expect usage_shard_missing_value 2 "fuzz: usage:" bash "$runner" --list --inventory parsers.md --shard
  fz_expect usage_time_word 2 "fuzz: usage:" bash "$runner" --list --time soon --inventory parsers.md
  fz_expect usage_time_no_unit 2 "fuzz: usage:" bash "$runner" --list --time 10 --inventory parsers.md
  fz_expect usage_time_valid 0 - bash "$runner" --list --time 1m30s --inventory parsers.md
  fz_expect usage_inventory_missing 2 "fuzz: usage:" bash "$runner" --list --inventory absent.md
  fz_expect usage_inventory_no_section 2 "fuzz: usage:" bash "$runner" --list --inventory nosection.md
  fz_expect usage_inventory_short_row 2 "fuzz: usage:" bash "$runner" --list --inventory short.md
  fz_expect usage_inventory_empty_target 2 "inventory row has an empty Package, Target or Tags cell" bash "$runner" --list --inventory notarget.md
  fz_expect usage_inventory_empty_tags 2 "inventory row has an empty Package, Target or Tags cell" bash "$runner" --list --inventory notags.md
}

fz_run_arguments_and_continuation() {
  local d out rc; d=$(fz_tree run_args)
  fz_fakego "$d"
  fz_inventory "$d" "./internal/a FuzzA -" "./internal/bad FuzzBad -" "./internal/t FuzzTagged harness" "./internal/c FuzzC -"
  fz_package "$d" internal/a FuzzA
  fz_package "$d" internal/bad FuzzBad
  fz_package "$d" internal/t FuzzTagged
  fz_package "$d" internal/c FuzzC
  cd "$d" || return
  out=$(PATH="$d/bin:$PATH" bash "$runner" --time 7s --inventory parsers.md 2>&1); rc=$?
  fz_equal run_exit "$rc" 1
  fz_equal run_go_calls "$(cat go.log)" "$(printf '%s\n' \
    'test -run ^$ -fuzz ^FuzzA$ -fuzztime 7s ./internal/a' \
    'test -run ^$ -fuzz ^FuzzBad$ -fuzztime 7s ./internal/bad' \
    'test -run ^$ -fuzz ^FuzzTagged$ -fuzztime 7s -tags harness ./internal/t' \
    'test -run ^$ -fuzz ^FuzzC$ -fuzztime 7s ./internal/c')"
  if [[ "$out" == *"fuzz: FAIL ./internal/bad FuzzBad"* ]]; then pass=$((pass + 1)); else fz_note run_fail_line "output lacks the FAIL line: $out"; fi
  fz_equal run_fail_lines "$(grep -c '^fuzz: FAIL ' <<<"$out")" 1
  rm go.log
  out=$(PATH="$d/bin:$PATH" bash "$runner" --shard 2/2 --inventory parsers.md 2>&1); rc=$?
  fz_equal run_shard_exit "$rc" 1
  fz_equal run_shard_calls "$(cat go.log)" "$(printf '%s\n' \
    'test -run ^$ -fuzz ^FuzzBad$ -fuzztime 30s ./internal/bad' \
    'test -run ^$ -fuzz ^FuzzC$ -fuzztime 30s ./internal/c')"
}

fz_run_go_reading_stdin() {
  local d; d=$(fz_tree run_stdin)
  fz_fakego "$d" reads
  fz_inventory "$d" "./internal/a FuzzA -" "./internal/b FuzzB -" "./internal/c FuzzC -"
  fz_package "$d" internal/a FuzzA
  fz_package "$d" internal/b FuzzB
  fz_package "$d" internal/c FuzzC
  cd "$d" || return
  fz_expect run_stdin 0 - env PATH="$d/bin:$PATH" bash "$runner" --time 1s --inventory parsers.md
  fz_equal run_stdin_calls "$(wc -l < go.log | tr -d ' ')" 3
}

fz_run_checks_first() {
  local d; d=$(fz_tree run_check)
  fz_fakego "$d"
  fz_inventory "$d" "./internal/a FuzzA -" "./internal/b FuzzGone -"
  fz_package "$d" internal/a FuzzA
  cd "$d" || return
  fz_expect run_check_fails 1 "fuzz: ./internal/b: no target FuzzGone" env PATH="$d/bin:$PATH" bash "$runner" --time 1s --inventory parsers.md
  fz_equal run_check_ran_nothing "$(cat go.log 2>/dev/null)" ""
}

fz_run_real_fuzz_target() {
  local d; d=$(fz_tree run_real)
  fz_inventory "$d" "./internal/ok FuzzOk -"
  fz_package "$d" internal/ok FuzzOk
  cd "$d" || return
  fz_expect run_real 0 "elapsed" env GOTOOLCHAIN=local bash "$runner" --time 1s --inventory parsers.md
}

for t in fz_check_ok fz_check_missing_target fz_check_orphan_target fz_check_scan_failure fz_check_parameter_names \
  fz_check_crlf_inventory fz_list_and_shard fz_usage fz_run_arguments_and_continuation fz_run_go_reading_stdin \
  fz_run_checks_first fz_run_real_fuzz_target; do
  cd "$work" || exit 1
  "$t"
done

echo "fuzz tests: $pass passed, $failures failed"
[ "$failures" -eq 0 ]
