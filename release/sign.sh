#!/usr/bin/env bash
# Signs checksums.txt with every minisign key in the environment, then
# refuses the release unless the release binary is not a test build and
# trusts exactly those keys (spec section 9, ADR 0007). The sign job of
# release.yml runs it in the owner-approved release environment.
#
#   sign.sh <version> <stable|beta> <release dir> <version.json>
#
# Keys: MINISIGN_KEY_1 with MINISIGN_PASSWORD_1, and MINISIGN_KEY_2 with
# MINISIGN_PASSWORD_2 during a key rotation; each key is the text of a
# minisign secret key file. Signatures are written as
# checksums.txt.<key id>.minisig. MINISIGN names the minisign command.
set -euo pipefail
umask 077

if [ "$#" -ne 4 ]; then
  echo "usage: sign.sh <version> <stable|beta> <release dir> <version.json>" >&2
  exit 2
fi
version=$1
channel=$2
dir=$3
info=$4
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

(cd "$root" && go build -o "$work/releasetool" ./release/releasetool)
"$work/releasetool" check-version "$version"
case $channel in
stable | beta) ;;
*)
  echo "sign: channel $channel is not stable or beta" >&2
  exit 1
  ;;
esac

comment="version=$version channel=$channel purpose=checksums"
# Signatures stay in $work until every key has signed and the build check
# passed, so a failure never leaves a partial set in the release directory.
mkdir "$work/sigs"
ids=()
for slot in 1 2; do
  key_var=MINISIGN_KEY_$slot
  password_var=MINISIGN_PASSWORD_$slot
  [ -n "${!key_var:-}" ] || continue
  printf '%s\n' "${!key_var}" >"$work/key"
  # minisign reads the password as one line when standard input is not a terminal.
  printf '%s\n' "${!password_var:-}" |
    "${MINISIGN:-minisign}" -S -s "$work/key" -m "$dir/checksums.txt" -t "$comment" -x "$work/sig" >/dev/null
  rm -f "$work/key"
  id=$("$work/releasetool" keyid "$work/sig")
  mv "$work/sig" "$work/sigs/checksums.txt.$id.minisig"
  ids+=("$id")
done

if [ "${#ids[@]}" -eq 0 ]; then
  echo "sign: no signing key in the environment; the owner sets MINISIGN_KEY_1 in the release environment" >&2
  exit 1
fi
"$work/releasetool" check-build "$(IFS=,; echo "${ids[*]}")" <"$info"
mv "$work"/sigs/*.minisig "$dir/"
echo "sign: checksums.txt signed by ${ids[*]}"
