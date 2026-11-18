#!/usr/bin/env bash
# Acceptance tests of the snapshot-dedupe spec for release/snapshots.sh.
# Run: bash release/snapshot-dedupe.acceptance.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/snapshots.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=qa GIT_AUTHOR_EMAIL=qa@example.invalid
export GIT_COMMITTER_NAME=qa GIT_COMMITTER_EMAIL=qa@example.invalid

# check NAME WANT GOT: counts WANT equal to GOT as a pass.
check() {
  if [ "$2" = "$3" ]; then
    pass=$((pass + 1))
  else
    echo "FAIL $1: got [$3], want [$2]" >&2
    fail=$((fail + 1))
  fi
}

# run_exit CMD...: prints the exit status of CMD; its output stays in
# $work/out.
run_exit() {
  local got=0
  "$@" >"$work/out" 2>&1 || got=$?
  echo "$got"
}

# said NAME TEXT: counts a pass when the last run's output contains TEXT.
said() {
  if grep -qF -- "$2" "$work/out"; then
    pass=$((pass + 1))
  else
    echo "FAIL $1: output lacks [$2]: $(cat "$work/out")" >&2
    fail=$((fail + 1))
  fi
}

# files DIR: the files under DIR, relative and sorted, space separated.
files() {
  if [ -d "$1" ]; then
    (cd "$1" && find . -type f -printf '%P\n') | LC_ALL=C sort | tr '\n' ' '
  fi
}

# tree_sums: a checksum listing of every file under the fixture's released/.
tree_sums() {
  (cd "$R" && find . -type f -exec sha256sum {} + | LC_ALL=C sort)
}

# put TAG PATH CONTENT: writes a released file.
put() {
  mkdir -p "$R/$1/$(dirname "$2")"
  printf '%s\n' "$3" >"$R/$1/$2"
}

# snap PATH CONTENT: writes a current snapshot.
snap() {
  mkdir -p "$S/$(dirname "$1")"
  printf '%s\n' "$2" >"$S/$1"
}

# commit MESSAGE [TAG]: commits everything in the fixture, and tags it.
commit() {
  git -C "$repo" add -A
  git -C "$repo" commit -qm "$1"
  if [ "$#" -eq 2 ]; then
    git -C "$repo" tag -a "$2" -m "$2"
  fi
}

# status: what git reports for released/, one path per line.
status() {
  git -C "$S" status --porcelain --untracked-files=all -- released
}

# fixture NAME: a fresh committed git repository.
#   current snapshots: core.host.list/v1 {"l":1}, core.host.get/v1 {"g":1},
#                      hostinfo.summary/v1 {"h":1}
#   released/v1.0.0-beta.1: list {"l":0}, get, summary
#   released/v1.0.0-rc.1:   list {"l":1}
#   released/v0.9.0:        get, summary
#   released/README.md and .gitkeep: not tag directories
fixture() {
  repo="$work/$1"
  export SCHEMAS_DIR="$repo/schemas"
  S=$SCHEMAS_DIR
  R=$S/released
  mkdir -p "$S" "$R"
  git -C "$repo" init -q
  snap core.host.list/v1.json '{"l":1}'
  snap core.host.get/v1.json '{"g":1}'
  snap hostinfo.summary/v1.json '{"h":1}'
  echo 'Released snapshots.' >"$R/README.md"
  : >"$R/.gitkeep"
  put v1.0.0-beta.1 core.host.list/v1.json '{"l":0}'
  put v1.0.0-beta.1 core.host.get/v1.json '{"g":1}'
  put v1.0.0-beta.1 hostinfo.summary/v1.json '{"h":1}'
  put v1.0.0-rc.1 core.host.list/v1.json '{"l":1}'
  put v0.9.0 core.host.get/v1.json '{"g":1}'
  put v0.9.0 hostinfo.summary/v1.json '{"h":1}'
  commit base
}

# --- A1 ---------------------------------------------------------------------

fixture a1_changed
snap hostinfo.summary/v1.json '{"h":2}'
snap core.host.new/v1.json '{"n":1}'
commit change
check "A1: copy of changed and new snapshots exits 0" 0 "$(run_exit bash "$script" copy v1.1.0)"
check "A1: copy writes exactly the changed and new snapshots" "core.host.new/v1.json hostinfo.summary/v1.json " "$(files "$R/v1.1.0")"
check "A1: copy writes the current bytes" '{"h":2}' "$(cat "$R/v1.1.0/hostinfo.summary/v1.json")"

fixture a1_older_same_major
snap core.host.list/v1.json '{"l":0}'
commit list-back-to-beta
check "A1: a match in an older same-major directory is enough" 0 "$(run_exit bash "$script" copy v1.1.0)"
check "A1: nothing is written for a snapshot an older same-major tag holds" "" "$(files "$R/v1.1.0")"

fixture a1_nothing_new
check "A1: copy with nothing new exits 0" 0 "$(run_exit bash "$script" copy v1.1.0)"
check "A1: copy with nothing new creates no directory" no "$([ -e "$R/v1.1.0" ] && echo yes || echo no)"
check "A1: copy with nothing new leaves released/ clean" "" "$(status)"

fixture a1_new_major
check "A1: the first tag of a new major exits 0" 0 "$(run_exit bash "$script" copy v2.0.0)"
check "A1: the first tag of a new major gets every current snapshot" "core.host.get/v1.json core.host.list/v1.json hostinfo.summary/v1.json " "$(files "$R/v2.0.0")"

fixture a1_v10_is_not_v1
put v10.0.0 hostinfo.summary/v1.json '{"h":2}'
snap hostinfo.summary/v1.json '{"h":2}'
commit v10-holds-summary
check "A1: copy for v1.x ignores v10.x copies" 0 "$(run_exit bash "$script" copy v1.1.0)"
check "A1: copy for v1.x writes what only v10.x holds" "hostinfo.summary/v1.json " "$(files "$R/v1.1.0")"
commit v1.1.0
check "A1: copy for v10.x ignores v1.x copies" 0 "$(run_exit bash "$script" copy v10.1.0)"
check "A1: copy for v10.x writes what v1.x holds but v10.x does not" "core.host.get/v1.json core.host.list/v1.json " "$(files "$R/v10.1.0")"

# --- A2 ---------------------------------------------------------------------

fixture a2_existing
mkdir "$R/v1.1.0"
before=$(tree_sums)
check "A2: copy refuses an existing tag directory" 1 "$(run_exit bash "$script" copy v1.1.0)"
check "A2: refusal of an existing directory changes no file" "$before" "$(tree_sums)"

fixture a2_untracked
snap hostinfo.summary/v1.json '{"h":2}'
commit change
echo '{}' >"$R/v1.0.0-rc.1/extra.json"
before=$(tree_sums)
check "A2: copy refuses an untracked file under released/" 1 "$(run_exit bash "$script" copy v1.1.0)"
said "A2: untracked refusal says why" "snapshots:"
check "A2: untracked refusal writes no directory" no "$([ -e "$R/v1.1.0" ] && echo yes || echo no)"
check "A2: untracked refusal changes no file" "$before" "$(tree_sums)"

fixture a2_modified
snap hostinfo.summary/v1.json '{"h":2}'
commit change
echo '{"l":99}' >"$R/v1.0.0-rc.1/core.host.list/v1.json"
before=$(tree_sums)
check "A2: copy refuses a modified file under released/" 1 "$(run_exit bash "$script" copy v1.1.0)"
check "A2: modified refusal writes no directory" no "$([ -e "$R/v1.1.0" ] && echo yes || echo no)"
check "A2: modified refusal changes no file" "$before" "$(tree_sums)"

fixture a2_deleted
snap hostinfo.summary/v1.json '{"h":2}'
commit change
rm "$R/v1.0.0-beta.1/core.host.list/v1.json"
before=$(tree_sums)
check "A2: copy refuses a deleted file under released/" 1 "$(run_exit bash "$script" copy v1.1.0)"
check "A2: deleted refusal writes no directory" no "$([ -e "$R/v1.1.0" ] && echo yes || echo no)"
check "A2: deleted refusal changes no file" "$before" "$(tree_sums)"

fixture a2_staged
snap hostinfo.summary/v1.json '{"h":2}'
commit change
echo '{}' >"$R/v0.9.0/staged.json"
git -C "$repo" add schemas/released/v0.9.0/staged.json
before=$(tree_sums)
check "A2: copy refuses a staged file under released/" 1 "$(run_exit bash "$script" copy v1.1.0)"
check "A2: staged refusal writes no directory" no "$([ -e "$R/v1.1.0" ] && echo yes || echo no)"
check "A2: staged refusal changes no file" "$before" "$(tree_sums)"

fixture a2_staged_deleted
snap hostinfo.summary/v1.json '{"h":2}'
commit change
git -C "$repo" rm -q schemas/released/v0.9.0/hostinfo.summary/v1.json
check "A2: copy refuses a staged deletion under released/" 1 "$(run_exit bash "$script" copy v1.1.0)"

fixture a2_dirty_outside
echo 'notes' >"$repo/CHANGELOG.md"
snap hostinfo.summary/v1.json '{"h":2}'
check "A2: uncommitted changes outside released/ do not refuse" 0 "$(run_exit bash "$script" copy v1.1.0)"

fixture a2_outside_git
mkdir -p "$work/nogit"
cp -r "$S" "$work/nogit/schemas"
check "A2: copy refuses a schemas directory outside a git work tree" 1 "$(run_exit env SCHEMAS_DIR="$work/nogit/schemas" GIT_CEILING_DIRECTORIES="$work" bash "$script" copy v1.1.0)"
said "A2: outside-git refusal says why" "snapshots:"
check "A2: outside-git refusal writes no directory" no "$([ -e "$work/nogit/schemas/released/v1.1.0" ] && echo yes || echo no)"

fixture a2_malformed
before=$(tree_sums)
for bad in 1.0.0 vx.1.0 v.1 release-1.0 v1 ''; do
  check "A2: copy of malformed tag [$bad] exits 2" 2 "$(run_exit bash "$script" copy "$bad")"
done
check "A2: malformed tags change no file" "$before" "$(tree_sums)"

fixture a2_other_tags_untouched
snap hostinfo.summary/v1.json '{"h":2}'
commit change
before=$(tree_sums)
check "A2: a successful copy exits 0" 0 "$(run_exit bash "$script" copy v1.1.0)"
check "A2: a successful copy leaves every other tag directory as it was" "$before" "$(cd "$R" && find . -type f ! -path './v1.1.0/*' -exec sha256sum {} + | LC_ALL=C sort)"

# --- A3 ---------------------------------------------------------------------

fixture a3_without_dir
check "A3: check passes without released/<tag>/" 0 "$(run_exit bash "$script" check v1.1.0)"

fixture a3_with_dir
snap hostinfo.summary/v1.json '{"h":2}'
commit change
bash "$script" copy v1.1.0 >/dev/null 2>&1
commit v1.1.0 v1.1.0
check "A3: check passes with released/<tag>/ holding the changed snapshot" 0 "$(run_exit bash "$script" check v1.1.0)"

fixture a3_older_match
snap core.host.list/v1.json '{"l":0}'
commit list-back-to-beta
check "A3: a copy in any same-major directory is enough" 0 "$(run_exit bash "$script" check v1.1.0)"

fixture a3_dir_with_duplicate
put v1.1.0 core.host.get/v1.json '{"g":1}'
commit duplicate
check "A3: check passes for a directory holding only unchanged current snapshots" 0 "$(run_exit bash "$script" check v1.1.0)"

# --- A4 ---------------------------------------------------------------------

fixture a4_changed
snap hostinfo.summary/v1.json '{"h":2}'
commit change
check "A4: check fails for a changed snapshot nobody copied" 1 "$(run_exit bash "$script" check v1.1.0)"
said "A4: check names the changed snapshot" "hostinfo.summary/v1.json"

fixture a4_new_snapshot
snap core.host.new/v1.json '{"n":1}'
commit new-action
check "A4: check fails for a new snapshot nobody copied" 1 "$(run_exit bash "$script" check v1.1.0)"
said "A4: check names the new snapshot" "core.host.new/v1.json"

fixture a4_other_major_only
snap core.host.new/v1.json '{"n":1}'
put v0.9.0 core.host.new/v1.json '{"n":1}'
put v10.0.0 core.host.new/v1.json '{"n":1}'
commit other-majors
check "A4: copies in other majors do not count" 1 "$(run_exit bash "$script" check v1.1.0)"
said "A4: check names the snapshot only other majors hold" "core.host.new/v1.json"

fixture a4_stray
put v1.1.0 notes.txt 'stray'
commit stray
check "A4: check fails for a stray file in released/<tag>/" 1 "$(run_exit bash "$script" check v1.1.0)"
said "A4: check names the stray file" "notes.txt"

fixture a4_not_current_snapshot
put v1.1.0 core.host.gone/v1.json '{"x":1}'
commit gone
check "A4: check fails for a snapshot that is no longer current" 1 "$(run_exit bash "$script" check v1.1.0)"
said "A4: check names the file that is not current" "core.host.gone/v1.json"

fixture a4_differs
put v1.1.0 hostinfo.summary/v1.json '{"h":9}'
commit differs
check "A4: check fails for a tag file that differs from current" 1 "$(run_exit bash "$script" check v1.1.0)"
said "A4: check names the differing file" "hostinfo.summary/v1.json"

# --- A7 (shell half) --------------------------------------------------------

fixture a7_clean
bash "$script" copy v1.1.0 >/dev/null 2>&1
check "A7: copy with nothing new leaves git status empty" "" "$(git -C "$S" status --porcelain --untracked-files=all -- released)"

fixture a7_one_changed
snap hostinfo.summary/v1.json '{"h":2}'
commit change
bash "$script" copy v1.1.0 >/dev/null 2>&1
check "A7: copy with one changed snapshot lists only that path" "?? schemas/released/v1.1.0/hostinfo.summary/v1.json" "$(git -C "$S" status --porcelain --untracked-files=all -- released)"

# --- A11 --------------------------------------------------------------------

# The fixture's history: v1.0.0 holds base content, then a duplicate copy is
# committed, then removed again.
fixture a11
git -C "$repo" tag -a v1.0.0 -m v1.0.0
put v1.0.1 core.host.list/v1.json '{"l":1}'
put v1.0.1 hostinfo.summary/v1.json '{"h":1}'
commit duplicate-copies v1.0.1
rm -r "$R/v1.0.1"
commit remove-duplicates
check "A11: retained passes when only duplicates were removed" 0 "$(run_exit bash "$script" retained v1.0.1)"
check "A11: retained passes for the older baseline too" 0 "$(run_exit bash "$script" retained v1.0.0)"

# The only copy of {"l":0} is released/v1.0.0-beta.1/core.host.list/v1.json.
rm "$R/v1.0.0-beta.1/core.host.list/v1.json"
check "A11: retained fails when the last copy of a snapshot is deleted" 1 "$(run_exit bash "$script" retained v1.0.0)"
said "A11: retained names the lost file" "core.host.list/v1.json"
git -C "$repo" checkout -q -- schemas/released

echo '{"l":5}' >"$R/v1.0.0-beta.1/core.host.list/v1.json"
check "A11: retained fails when the last copy of a snapshot is edited" 1 "$(run_exit bash "$script" retained v1.0.0)"
said "A11: retained names the edited file" "core.host.list/v1.json"
git -C "$repo" checkout -q -- schemas/released

check "A11: retained fails for an empty reference" 1 "$(run_exit bash "$script" retained '')"
check "A11: retained fails for an unresolvable reference" 1 "$(run_exit bash "$script" retained no-such-ref-anywhere)"
empty=$(git -C "$repo" commit-tree "$(git -C "$repo" mktree </dev/null)" -m empty)
check "A11: retained fails for a reference without a baseline" 1 "$(run_exit bash "$script" retained "$empty")"
readme_id=$(git -C "$repo" hash-object -w "$R/README.md")
only_docs_tree=$(GIT_INDEX_FILE="$work/docs-index" git -C "$repo" update-index --add --cacheinfo "100644,$readme_id,schemas/released/README.md" && GIT_INDEX_FILE="$work/docs-index" git -C "$repo" write-tree)
only_docs=$(git -C "$repo" commit-tree "$only_docs_tree" -m docs-only)
check "A11: retained fails for a reference whose tree has only released/README.md" 1 "$(run_exit bash "$script" retained "$only_docs")"

# With HEAD tagged, the call-site rule compares with the previous tag, so a
# release commit that dropped the last copy of a snapshot is caught.
fixture a11_callsite
git -C "$repo" tag -a v1.0.0 -m v1.0.0
rm "$R/v1.0.0-beta.1/core.host.list/v1.json"
commit drop-last-copy v1.0.1
ref=$(git -C "$repo" describe --tags --abbrev=0 --match 'v*' "$(git -C "$repo" rev-parse HEAD)^")
check "A11: the call-site rule picks the previous tag for a tagged HEAD" v1.0.0 "$ref"
check "A11: retained with the previous tag catches the loss in the tagged commit" 1 "$(run_exit bash "$script" retained "$ref")"
said "A11: the call-site loss names the file" "core.host.list/v1.json"
check "A11: retained with HEAD's own tag would pass (so the rule matters)" 0 "$(run_exit bash "$script" retained v1.0.1)"

echo "snapshot-dedupe acceptance (shell): $pass passed, $fail failed"
[ "$fail" -eq 0 ]
