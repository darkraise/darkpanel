#!/usr/bin/env bash
# Tests for release/snapshots.sh. Run: bash release/snapshots.test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/snapshots.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0

# expect NAME STATUS COMMAND...: runs COMMAND and checks its exit status.
expect() {
  local name=$1 want=$2 got=0
  shift 2
  "$@" >"$work/out" 2>&1 || got=$?
  if [ "$got" -eq "$want" ]; then
    pass=$((pass + 1))
  else
    echo "FAIL $name: exit $got, want $want" >&2
    cat "$work/out" >&2
    fail=$((fail + 1))
  fi
}

export SCHEMAS_DIR="$work/schemas"
mkdir -p "$SCHEMAS_DIR/core.host.list" "$SCHEMAS_DIR/hostinfo.summary" "$SCHEMAS_DIR/released/v0.1.0/core.host.list"
echo '{"input":1}' >"$SCHEMAS_DIR/core.host.list/v1.json"
echo '{"input":2}' >"$SCHEMAS_DIR/hostinfo.summary/v1.json"
# Files of the Go package schemas that are not snapshots and are never frozen.
mkdir -p "$SCHEMAS_DIR/testdata"
echo 'package schemas' >"$SCHEMAS_DIR/embed.go"
echo 'package schemas' >"$SCHEMAS_DIR/x_test.go"
echo '{"fixture":true}' >"$SCHEMAS_DIR/testdata/a.json"
echo 'Frozen snapshots of released tags.' >"$SCHEMAS_DIR/released/README.md"
echo '{"input":0}' >"$SCHEMAS_DIR/released/v0.1.0/core.host.list/v1.json"

expect usage 2 bash "$script" freeze v1.0.0
expect usage_count 2 bash "$script" copy
expect copy 0 bash "$script" copy v1.0.0
listing=$({ cd "$SCHEMAS_DIR/released/v1.0.0" && find . -type f; } 2>/dev/null | LC_ALL=C sort | tr '\n' ' ') || true
expect copy_file_set 0 test "$listing" = "./core.host.list/v1.json ./hostinfo.summary/v1.json "
expect copy_content 0 cmp "$SCHEMAS_DIR/core.host.list/v1.json" "$SCHEMAS_DIR/released/v1.0.0/core.host.list/v1.json"
expect copy_keeps_older_release 0 test "$(cat "$SCHEMAS_DIR/released/v0.1.0/core.host.list/v1.json")" = '{"input":0}'
expect copy_refuses_existing 1 bash "$script" copy v1.0.0
expect check_equal 0 bash "$script" check v1.0.0
expect check_missing_tag 1 bash "$script" check v9.9.9

echo '{"input":3}' >"$SCHEMAS_DIR/hostinfo.summary/v1.json"
expect check_changed 1 bash "$script" check v1.0.0
echo '{"input":2}' >"$SCHEMAS_DIR/hostinfo.summary/v1.json"

mkdir -p "$SCHEMAS_DIR/core.user.list"
echo '{}' >"$SCHEMAS_DIR/core.user.list/v1.json"
expect check_extra_current 1 bash "$script" check v1.0.0
rm -r "$SCHEMAS_DIR/core.user.list"

mkdir -p "$SCHEMAS_DIR/released/v1.0.0"
echo 'stray' >"$SCHEMAS_DIR/released/v1.0.0/notes.txt"
expect check_extra_released 1 bash "$script" check v1.0.0
rm -f "$SCHEMAS_DIR/released/v1.0.0/notes.txt"

rm -f "$SCHEMAS_DIR/released/v1.0.0/hostinfo.summary/v1.json"
expect check_missing_released 1 bash "$script" check v1.0.0

echo "snapshots tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
