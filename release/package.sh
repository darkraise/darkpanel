#!/usr/bin/env bash
# Packages the release tarballs and writes checksums.txt over every file in
# the output directory (spec section 9).
#
#   package.sh <version> <bin dir> <out dir>
#
# <bin dir> holds darkpanel-linux-amd64 and darkpanel-linux-arm64 (make
# build). The tarballs are reproducible: entries in a fixed order, owner 0/0,
# fixed modes, the commit time (or SOURCE_DATE_EPOCH) as mtime, and gzip
# without a name or timestamp, so a rebuild of the tag gives the same bytes.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: package.sh <version> <bin dir> <out dir>" >&2
  exit 2
fi
version=$1
bindir=$2
outdir=$3
root=$(cd "$(dirname "$0")/.." && pwd)
mtime=${SOURCE_DATE_EPOCH:-$(git -C "$root" log -1 --format=%ct)}

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$outdir"

for arch in amd64 arm64; do
  dir="$stage/$arch"
  mkdir -p "$dir"
  install -m 0755 "$bindir/darkpanel-linux-$arch" "$dir/darkpanel"
  install -m 0644 "$root/LICENSE" "$root/README.md" "$dir/"
  tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$mtime" \
    -C "$dir" -cf - darkpanel LICENSE README.md |
    gzip -n -9 >"$outdir/darkpanel_${version}_linux_${arch}.tar.gz"
done

cd "$outdir"
files=()
while IFS= read -r f; do
  files+=("$f")
done < <(find . -maxdepth 1 -type f ! -name checksums.txt ! -name '*.minisig' -printf '%f\n' | LC_ALL=C sort)
sha256sum -- "${files[@]}" >checksums.txt
