#!/usr/bin/env bash
# Moves the release pinned in the install commands to a new pre-release.
#
#   pin-docs.sh <version>
#
# While no stable release exists, README.md and docs/user/install.md install
# a named release candidate (releases/download/v<x>/ and --version <x>)
# instead of releases/latest. For a pre-release <version>, every such pin
# moves to it; a stable version, or docs with no pin, are left alone. Both
# files must pin the same version, or nothing is written (exit 1). DOCS_ROOT
# overrides the repository root.
set -euo pipefail

if [ "$#" -ne 1 ] || ! [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "usage: pin-docs.sh <version>" >&2
  exit 2
fi
version=$1
[[ $version == *-* ]] || exit 0

root=${DOCS_ROOT:-$(cd "$(dirname "$0")/.." && pwd)}
files=("$root/README.md" "$root/docs/user/install.md")

# grep exits 1 when nothing matches, and 2 when a file cannot be read.
status=0
urls=$(grep -ohE 'releases/download/v[^/[:space:]]+/' "${files[@]}") || status=$?
[ "$status" -le 1 ] || exit 1
[ -n "$urls" ] || exit 0
pinned=$(sed -E 's#^releases/download/v(.*)/$#\1#' <<<"$urls" | LC_ALL=C sort -u)
if [ "$(printf '%s\n' "$pinned" | wc -l)" -ne 1 ]; then
  echo "pin-docs: README.md and docs/user/install.md pin different versions:" >&2
  echo "  ${pinned//$'\n'/$'\n'  }" >&2
  exit 1
fi

old=$(printf '%s' "$pinned" | sed 's/[.[\*^$/]/\\&/g')
sed -i -E \
  -e "s/releases\/download\/v$old\//releases\/download\/v$version\//g" \
  -e "s/--version $old([^0-9A-Za-z.-]|\$)/--version $version\1/g" \
  "${files[@]}"
if grep -nE "releases/download/v$old/|--version $old([^0-9A-Za-z.-]|\$)" "${files[@]}" >&2; then
  echo "pin-docs: the lines above still pin $pinned" >&2
  exit 1
fi
