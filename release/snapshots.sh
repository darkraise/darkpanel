#!/usr/bin/env bash
# Freezes and checks the released schema snapshots (ADR 0002).
#
#   snapshots.sh copy <tag>   copies the current snapshots to schemas/released/<tag>/
#   snapshots.sh check <tag>  fails unless schemas/released/<tag>/ holds exactly them
#
# A snapshot is schemas/<action>/v<n>.json. Nothing else under schemas/ (the
# Go package's embed.go, its tests and testdata, released/) is frozen, so
# released trees hold only snapshots. Released snapshots are immutable:
# copy refuses an existing tag directory. SCHEMAS_DIR overrides the schemas
# directory.
set -euo pipefail

if [ "$#" -ne 2 ] || { [ "$1" != copy ] && [ "$1" != check ]; }; then
  echo "usage: snapshots.sh copy|check <tag>" >&2
  exit 2
fi
mode=$1
tag=$2
root=${SCHEMAS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/schemas}
dest="$root/released/$tag"

# snapshots: the current snapshot paths relative to $root, in C order.
snapshots() {
  (cd "$root" && find . -mindepth 2 -maxdepth 2 -type f -name 'v*.json' ! -path './released/*' -printf '%P\n' | LC_ALL=C sort)
}

case $mode in
copy)
  if [ -e "$dest" ]; then
    echo "snapshots: $dest exists; released snapshots are immutable" >&2
    exit 1
  fi
  mkdir -p "$dest"
  snapshots | while IFS= read -r f; do
    mkdir -p "$dest/$(dirname "$f")"
    cp "$root/$f" "$dest/$f"
  done
  ;;
check)
  if [ ! -d "$dest" ]; then
    echo "snapshots: $dest is missing; run make release-prep VERSION=${tag#v} and commit it" >&2
    exit 1
  fi
  if ! diff <(snapshots) <(cd "$dest" && find . -type f -printf '%P\n' | LC_ALL=C sort) >&2; then
    echo "snapshots: $dest does not hold exactly the current snapshots" >&2
    exit 1
  fi
  while IFS= read -r f; do
    if ! cmp -s "$root/$f" "$dest/$f"; then
      echo "snapshots: $dest/$f differs from the current snapshot" >&2
      exit 1
    fi
  done < <(snapshots)
  ;;
esac
