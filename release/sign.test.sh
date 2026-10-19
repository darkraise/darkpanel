#!/usr/bin/env bash
# Tests for release/sign.sh against a stub minisign. Run: bash release/sign.test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/sign.sh"
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

# The stub writes a hashed-mode signature file whose key id is the KEYID=
# line of the key file, stored little-endian as minisign does, and logs the
# signed file, the trusted comment and the password line it read. A key file
# with a FAIL line makes it fail.
cat >"$work/minisign" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
while [ "$#" -gt 0 ]; do
  case $1 in
  -s) key=$2; shift 2 ;;
  -m) msg=$2; shift 2 ;;
  -t) comment=$2; shift 2 ;;
  -x) out=$2; shift 2 ;;
  *) shift ;;
  esac
done
IFS= read -r password
if grep -qx FAIL "$key"; then
  echo "stub: refusing key" >&2
  exit 1
fi
id=$(sed -n 's/^KEYID=//p' "$key")
le=$(printf '%s' "$id" | sed 's/../&\n/g' | tac | tr -d '\n' | sed 's/../\\x&/g')
{
  echo "untrusted comment: stub signature"
  { printf 'ED'; printf '%b' "$le"; head -c 64 /dev/zero; } | base64 -w0
  echo
  echo "trusted comment: $comment"
  head -c 64 /dev/zero | base64 -w0
  echo
} >"$out"
echo "$msg|$comment|$password" >>"$STUB_LOG"
STUB
chmod +x "$work/minisign"

export MINISIGN="$work/minisign" STUB_LOG="$work/log"
unset MINISIGN_KEY_1 MINISIGN_PASSWORD_1 MINISIGN_KEY_2 MINISIGN_PASSWORD_2
key1="KEYID=0123456789ABCDEF"
key2="KEYID=00000000000000FF"

# trusts ID...: writes a version.json for a production build trusting ID...
trusts() {
  local ids
  ids=$(printf '"%s",' "$@")
  printf '{"version":"1.2.3","testBuild":false,"trustedKeys":[%s]}\n' "${ids%,}" >"$work/version.json"
}

# fresh: an empty release directory holding only checksums.txt, and no log.
fresh() {
  rm -rf "$work/rel" "$work/log"
  mkdir -p "$work/rel"
  echo "0000  darkpanel_1.2.3_linux_amd64.tar.gz" >"$work/rel/checksums.txt"
}

sigs() { (cd "$work/rel" && find . -name '*.minisig' | LC_ALL=C sort | tr '\n' ' '); }

fresh
trusts 0123456789ABCDEF
expect one_key 0 env MINISIGN_KEY_1="$key1" MINISIGN_PASSWORD_1=pw1 bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect one_key_file 0 test "$(sigs)" = "./checksums.txt.0123456789ABCDEF.minisig "
expect one_key_log 0 test "$(cat "$work/log" 2>/dev/null)" = "$work/rel/checksums.txt|version=1.2.3 channel=stable purpose=checksums|pw1"

fresh
trusts 0123456789ABCDEF 00000000000000FF
expect two_keys 0 env MINISIGN_KEY_1="$key1" MINISIGN_PASSWORD_1=pw1 MINISIGN_KEY_2="$key2" MINISIGN_PASSWORD_2=pw2 \
  bash "$script" 1.3.0-beta.1 beta "$work/rel" "$work/version.json"
expect two_keys_files 0 test "$(sigs)" = "./checksums.txt.00000000000000FF.minisig ./checksums.txt.0123456789ABCDEF.minisig "
expect two_keys_log 0 grep -qx ".*|version=1.3.0-beta.1 channel=beta purpose=checksums|pw2" "$work/log"

fresh
trusts 0123456789ABCDEF
expect no_key 1 bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect no_key_no_files 0 test -z "$(sigs)"

fresh
trusts 0123456789ABCDEF
expect trust_mismatch 1 env MINISIGN_KEY_1="$key1" MINISIGN_KEY_2="$key2" bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect trust_mismatch_no_files 0 test -z "$(sigs)"

fresh
trusts 0123456789ABCDEF 00000000000000FF
expect second_key_fails 1 env MINISIGN_KEY_1="$key1" MINISIGN_KEY_2="$key2"$'\nFAIL' bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect second_key_fails_no_files 0 test -z "$(sigs)"

fresh
printf '{"testBuild":true,"trustedKeys":["0123456789ABCDEF"]}\n' >"$work/version.json"
expect test_build 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect test_build_no_files 0 test -z "$(sigs)"

fresh
trusts 0123456789ABCDEF
expect usage 2 bash "$script" 1.2.3 stable "$work/rel"
expect bad_version 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2 stable "$work/rel" "$work/version.json"
expect bad_channel 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2.3 nightly "$work/rel" "$work/version.json"
expect bad_args_no_files 0 test -z "$(sigs)"

echo "sign tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
