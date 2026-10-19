#!/usr/bin/env bash
# Tests for release/package.sh. Run: bash release/package.test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/package.sh"
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

verify_checksums() { (cd "$1" && sha256sum --check --strict --quiet checksums.txt); }

export SOURCE_DATE_EPOCH=1700000000
mkdir -p "$work/bin" "$work/out1" "$work/out2" "$work/x"
printf 'amd64 binary' >"$work/bin/darkpanel-linux-amd64"
printf 'arm64 binary' >"$work/bin/darkpanel-linux-arm64"
for out in out1 out2; do
  printf '{"bomFormat":"CycloneDX"}' >"$work/$out/darkpanel_1.2.3.cdx.json"
done
amd64="$work/out1/darkpanel_1.2.3_linux_amd64.tar.gz"
names="darkpanel_1.2.3.cdx.json darkpanel_1.2.3_linux_amd64.tar.gz darkpanel_1.2.3_linux_arm64.tar.gz "

expect usage 2 bash "$script" 1.2.3 "$work/bin"
expect package 0 bash "$script" 1.2.3 "$work/bin" "$work/out1"
expect entries 0 test "$(tar -tzf "$amd64" 2>/dev/null | tr '\n' ' ')" = "darkpanel LICENSE README.md "
tar -xzf "$amd64" -C "$work/x" 2>/dev/null || true
expect binary_content 0 test "$(cat "$work/x/darkpanel" 2>/dev/null)" = "amd64 binary"
expect binary_mode 0 test "$(stat -c %a "$work/x/darkpanel" 2>/dev/null)" = 755
expect owner_root 0 test "$(tar -tvzf "$amd64" 2>/dev/null | awk '{print $2}' | sort -u)" = "0/0"
expect arm64_tarball 0 test -f "$work/out1/darkpanel_1.2.3_linux_arm64.tar.gz"
expect checksums_names 0 test "$(awk '{print $2}' "$work/out1/checksums.txt" 2>/dev/null | tr '\n' ' ')" = "$names"
expect checksums_verify 0 verify_checksums "$work/out1"
expect rerun 0 bash "$script" 1.2.3 "$work/bin" "$work/out1"
expect rerun_excludes_checksums 0 test "$(awk '{print $2}' "$work/out1/checksums.txt" 2>/dev/null | tr '\n' ' ')" = "$names"
expect package_again 0 bash "$script" 1.2.3 "$work/bin" "$work/out2"
expect reproducible 0 cmp "$amd64" "$work/out2/darkpanel_1.2.3_linux_amd64.tar.gz"
expect missing_binary 1 bash "$script" 1.2.3 "$work/out2" "$work/out2"

echo "package tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
