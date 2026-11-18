#!/usr/bin/env bash
# Freezes and checks the released schema snapshots (ADR 0002).
#
#   snapshots.sh copy <tag>   copies to schemas/released/<tag>/ only the current
#                             snapshots no earlier tag of the same major holds
#   snapshots.sh check <tag>  fails unless every current snapshot is held
#                             byte-identical by a tag of the same major, and
#                             released/<tag>/ holds only current snapshots
#
# A snapshot is schemas/<action>/v<n>.json. Nothing else under schemas/ (the
# Go package's embed.go, its tests and testdata, released/) is frozen, so
# released trees hold only snapshots. Each snapshot is stored once per major:
# released/<tag>/ holds only the snapshots new or changed in that tag, and a
# tag with nothing new gets no directory. A tag directory is a directory
# directly under released/ named like v<major>.<rest>; its same-major
# directories are those named v<major>.* (v1. matches v1.0.0-rc.3, not
# v10.0.0). Released snapshots are immutable: copy refuses an existing tag
# directory, and dedupes only against committed content, so it refuses
# uncommitted changes under released/. SCHEMAS_DIR overrides the schemas
# directory.
set -euo pipefail

tag_re='^v[0-9]+\..+$'

usage() {
  echo "usage: snapshots.sh copy|check <tag>" >&2
  exit 2
}

if [ "$#" -ne 2 ]; then
  usage
fi
mode=$1
tag=$2
case $mode in
copy | check) ;;
*) usage ;;
esac
if ! [[ $tag =~ $tag_re ]]; then
  usage
fi
major=${tag#v}
major=${major%%.*}
root=${SCHEMAS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/schemas}
dest="$root/released/$tag"

# snapshots: the current snapshot paths relative to $root, in C order.
snapshots() {
  (cd "$root" && find . -mindepth 2 -maxdepth 2 -type f -name 'v*.json' ! -path './released/*' -printf '%P\n' | LC_ALL=C sort)
}

# same_major: the names of the same-major tag directories under $root/released.
same_major=()
for d in "$root"/released/v"$major".*/; do
  [ -d "$d" ] || continue
  d=${d%/}
  d=${d##*/}
  if [[ $d =~ $tag_re ]]; then
    same_major+=("$d")
  fi
done

# held PATH: whether a same-major tag directory holds a file at PATH
# byte-identical to the current snapshot $root/PATH.
held() {
  local d
  for d in "${same_major[@]}"; do
    if cmp -s "$root/$1" "$root/released/$d/$1"; then
      return 0
    fi
  done
  return 1
}

current=$(snapshots)

case $mode in
copy)
  if [ -e "$dest" ]; then
    echo "snapshots: $dest exists; released snapshots are immutable" >&2
    exit 1
  fi
  if ! git -C "$root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "snapshots: $root is not inside a git work tree; copy dedupes against committed snapshots" >&2
    exit 1
  fi
  if ! status=$(git -C "$root" status --porcelain --untracked-files=all -- released); then
    echo "snapshots: git status failed in $root" >&2
    exit 1
  fi
  if [ -n "$status" ]; then
    echo "snapshots: $root/released has uncommitted changes; commit or discard them first:" >&2
    printf '%s\n' "$status" >&2
    exit 1
  fi
  copied=0
  while IFS= read -r f; do
    if [ -z "$f" ] || held "$f"; then
      continue
    fi
    mkdir -p "$dest/$(dirname "$f")"
    cp "$root/$f" "$dest/$f"
    copied=$((copied + 1))
  done <<<"$current"
  if [ "$copied" -eq 0 ]; then
    echo "snapshots: no new snapshots for $tag"
  fi
  ;;
check)
  failed=0
  while IFS= read -r f; do
    if [ -n "$f" ] && ! held "$f"; then
      echo "snapshots: $f has no byte-identical copy in a v$major.* tag directory; run make release-prep VERSION=${tag#v} and commit it" >&2
      failed=1
    fi
  done <<<"$current"
  if [ -d "$dest" ]; then
    stored=$(cd "$dest" && find . -type f -printf '%P\n' | LC_ALL=C sort)
    while IFS= read -r f; do
      if [ -z "$f" ]; then
        continue
      fi
      if ! grep -qxF -- "$f" <<<"$current"; then
        echo "snapshots: $dest/$f is not a current snapshot" >&2
        failed=1
      elif ! cmp -s "$root/$f" "$dest/$f"; then
        echo "snapshots: $dest/$f differs from the current snapshot" >&2
        failed=1
      fi
    done <<<"$stored"
  fi
  exit "$failed"
  ;;
esac
