#!/usr/bin/env bash
# Tests for release/snapshots.sh. Run: bash release/snapshots.test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/snapshots.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=owner GIT_AUTHOR_EMAIL=owner@example.invalid
export GIT_COMMITTER_NAME=owner GIT_COMMITTER_EMAIL=owner@example.invalid

# expect NAME STATUS COMMAND...: runs COMMAND and checks its exit status; its
# output stays in $work/out until the next expect.
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

# said NAME TEXT: checks that the last expect's output contains TEXT.
said() {
  if grep -qF -- "$2" "$work/out"; then
    pass=$((pass + 1))
  else
    echo "FAIL $1: output lacks $2" >&2
    cat "$work/out" >&2
    fail=$((fail + 1))
  fi
}

# files DIR: the files under DIR, relative and sorted, each followed by a space.
files() {
  (cd "$1" && find . -type f -printf '%P\n') | LC_ALL=C sort | tr '\n' ' '
}

# sums DIR...: a sorted checksum listing of the files under the tag directories.
sums() {
  (cd "$R" && find "$@" -type f -exec sha256sum {} + | LC_ALL=C sort)
}

# commit MESSAGE [TAG]: commits everything in the fixture, and tags it.
commit() {
  git -C "$repo" add -A
  git -C "$repo" commit -qm "$1"
  if [ "$#" -eq 2 ]; then
    git -C "$repo" tag -a "$2" -m "$2"
  fi
}

repo="$work/repo"
export SCHEMAS_DIR="$repo/schemas"
S=$SCHEMAS_DIR
R=$S/released
mkdir -p "$S/core.host.list" "$S/hostinfo.summary" "$S/testdata" \
  "$R/v0.1.0/core.host.list" "$R/v0.1.0/hostinfo.summary"
git -C "$repo" init -q
echo '{"input":1}' >"$S/core.host.list/v1.json"
echo '{"input":2}' >"$S/hostinfo.summary/v1.json"
# Files of the Go package schemas that are not snapshots and are never frozen.
echo 'package schemas' >"$S/embed.go"
echo 'package schemas' >"$S/x_test.go"
echo '{"fixture":true}' >"$S/testdata/a.json"
# Not tag directories: every rule ignores them.
echo 'Frozen snapshots of released tags.' >"$R/README.md"
: >"$R/.gitkeep"
# Major 0: one snapshot differs from current, one equals it.
echo '{"input":0}' >"$R/v0.1.0/core.host.list/v1.json"
echo '{"input":2}' >"$R/v0.1.0/hostinfo.summary/v1.json"
commit base v0.1.0

expect usage 2 bash "$script" freeze v1.0.0
expect usage_count 2 bash "$script" copy
expect copy_malformed_tag 2 bash "$script" copy 1.0.0
expect check_malformed_tag 2 bash "$script" check vX.0

# The first release of major 1 holds the full set, whatever major 0 holds.
before=$(sums v0.1.0)
expect copy_new_major 0 bash "$script" copy v1.0.0
expect copy_new_major_set 0 test "$(files "$R/v1.0.0")" = "core.host.list/v1.json hostinfo.summary/v1.json "
expect copy_content 0 cmp "$S/core.host.list/v1.json" "$R/v1.0.0/core.host.list/v1.json"
expect copy_keeps_other_tags 0 test "$(sums v0.1.0)" = "$before"
commit v1.0.0 v1.0.0
expect check_full 0 bash "$script" check v1.0.0
expect copy_refuses_existing 1 bash "$script" copy v1.0.0

# Nothing new within the major: no directory, nothing for git to commit.
expect copy_nothing_new 0 bash "$script" copy v1.0.1
said copy_nothing_new_says "no new snapshots for v1.0.1"
expect copy_nothing_new_no_dir 1 test -e "$R/v1.0.1"
expect copy_nothing_new_clean 0 test -z "$(git -C "$repo" status --porcelain --untracked-files=all -- schemas/released)"
expect check_without_dir 0 bash "$script" check v1.0.1

# One changed snapshot: only that path is written.
echo '{"input":3}' >"$S/hostinfo.summary/v1.json"
commit change
expect check_changed_not_copied 1 bash "$script" check v1.0.2
said check_changed_names_file "hostinfo.summary/v1.json"
said check_changed_hint "run make release-prep VERSION=1.0.2 and commit it"
before=$(sums v0.1.0 v1.0.0)
expect copy_changed 0 bash "$script" copy v1.0.2
expect copy_changed_status 0 test "$(git -C "$repo" status --porcelain --untracked-files=all -- schemas/released)" = "?? schemas/released/v1.0.2/hostinfo.summary/v1.json"
expect copy_changed_keeps_other_tags 0 test "$(sums v0.1.0 v1.0.0)" = "$before"
expect check_changed 0 bash "$script" check v1.0.2
commit v1.0.2 v1.0.2

# A tag directory may hold only current snapshots, unchanged.
echo 'stray' >"$R/v1.0.2/notes.txt"
expect check_stray 1 bash "$script" check v1.0.2
said check_stray_names_file "notes.txt"
rm "$R/v1.0.2/notes.txt"
echo '{"input":9}' >"$R/v1.0.2/hostinfo.summary/v1.json"
expect check_stale 1 bash "$script" check v1.0.2
said check_stale_names_file "hostinfo.summary/v1.json"
git -C "$repo" checkout -q -- schemas/released

# copy dedupes only against committed content.
echo '{}' >"$R/v1.0.0/extra.json"
expect copy_refuses_untracked 1 bash "$script" copy v1.0.3
rm "$R/v1.0.0/extra.json"
echo ' ' >>"$R/v1.0.0/core.host.list/v1.json"
expect copy_refuses_modified 1 bash "$script" copy v1.0.3
git -C "$repo" checkout -q -- schemas/released
rm "$R/v0.1.0/core.host.list/v1.json"
expect copy_refuses_deleted 1 bash "$script" copy v1.0.3
git -C "$repo" checkout -q -- schemas/released
echo '{}' >"$R/v0.1.0/staged.json"
git -C "$repo" add schemas/released/v0.1.0/staged.json
expect copy_refuses_staged 1 bash "$script" copy v1.0.3
git -C "$repo" rm -qf schemas/released/v0.1.0/staged.json
expect copy_refusals_wrote_nothing 1 test -e "$R/v1.0.3"
mkdir -p "$work/nogit"
cp -r "$S" "$work/nogit/"
expect copy_outside_git 1 env SCHEMAS_DIR="$work/nogit/schemas" GIT_CEILING_DIRECTORIES="$work" bash "$script" copy v1.0.3
expect copy_outside_git_wrote_nothing 1 test -e "$work/nogit/schemas/released/v1.0.3"

# v10 is another major than v1: it gets the full set, and v1.x never counts it.
mkdir -p "$S/core.user.list"
echo '{}' >"$S/core.user.list/v1.json"
commit new-action
expect copy_v10 0 bash "$script" copy v10.0.0
expect copy_v10_set 0 test "$(files "$R/v10.0.0")" = "core.host.list/v1.json core.user.list/v1.json hostinfo.summary/v1.json "
commit v10.0.0 v10.0.0
expect check_v1_ignores_v10 1 bash "$script" check v1.0.3
said check_v1_ignores_v10_names_file "core.user.list/v1.json"

# retained: no released content may disappear from the working tree.
expect retained_usage 2 bash "$script" retained
expect retained_unchanged 0 bash "$script" retained v10.0.0
expect retained_older_tag 0 bash "$script" retained v1.0.2
cp -r "$R/v1.0.0" "$R/v1.0.9"
commit duplicate
dup=$(git -C "$repo" rev-parse HEAD)
rm -r "$R/v1.0.9"
expect retained_duplicate_removed 0 bash "$script" retained "$dup"
commit drop-duplicate
# {"input":0} lives only in v0.1.0/core.host.list/v1.json.
rm "$R/v0.1.0/core.host.list/v1.json"
expect retained_last_copy_deleted 1 bash "$script" retained v1.0.2
said retained_deleted_names_file "v0.1.0/core.host.list/v1.json"
git -C "$repo" checkout -q -- schemas/released
echo '{"input":5}' >"$R/v0.1.0/core.host.list/v1.json"
expect retained_last_copy_edited 1 bash "$script" retained v1.0.2
said retained_edited_names_file "v0.1.0/core.host.list/v1.json"
git -C "$repo" checkout -q -- schemas/released
expect retained_empty_ref 1 bash "$script" retained ""
expect retained_unknown_ref 1 bash "$script" retained no-such-ref
empty=$(git -C "$repo" commit-tree "$(git -C "$repo" mktree </dev/null)" -m empty)
expect retained_empty_baseline 1 bash "$script" retained "$empty"
# Pins the git behaviour the call sites rely on (Task 5 pins the lines
# themselves): a tagged HEAD resolves to the previous tag, never itself.
git -C "$repo" tag -a v10.0.1 -m v10.0.1
expect callsite_tag_on_head 0 test "$(git -C "$repo" describe --tags --abbrev=0 --match 'v*' "$(git -C "$repo" rev-parse HEAD)")" = v10.0.1
expect callsite_previous_tag 0 test "$(git -C "$repo" describe --tags --abbrev=0 --match 'v*' "$(git -C "$repo" rev-parse HEAD)^")" = v10.0.0

echo "snapshots tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
