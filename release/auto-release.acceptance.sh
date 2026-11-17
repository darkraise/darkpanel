#!/usr/bin/env bash
# Acceptance tests of the automatic release spec for release/auto-push.sh.
# Run: bash release/auto-release.acceptance.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/auto-push.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=owner GIT_AUTHOR_EMAIL=owner@example.invalid
export GIT_COMMITTER_NAME=owner GIT_COMMITTER_EMAIL=owner@example.invalid

# ok NAME COMMAND...: counts COMMAND's success as a pass.
ok() {
  local name=$1
  shift
  if "$@" >/dev/null 2>&1; then
    pass=$((pass + 1))
  else
    echo "FAIL $name" >&2
    fail=$((fail + 1))
  fi
}

ssh-keygen -q -t ed25519 -N '' -C bot -f "$work/bot"

# A bare remote whose main lets the bot key sign tags, and a clone of it whose
# index holds a prepared change. Prints the clone's HEAD.
make_repo() {
  local dir="$work/$1"
  git init -q --bare -b main "$dir/remote.git"
  git init -q -b main "$dir/seed"
  mkdir -p "$dir/seed/release"
  printf '* namespaces="git" %s\n' "$(cut -d' ' -f1,2 "$work/bot.pub")" >"$dir/seed/release/allowed_signers"
  echo '# Changelog' >"$dir/seed/CHANGELOG.md"
  git -C "$dir/seed" add -A
  git -C "$dir/seed" commit -q -m 'feat: first'
  git -C "$dir/seed" push -q "$dir/remote.git" main
  git clone -q "$dir/remote.git" "$dir/clone"
  echo '## 1.0.0-rc.3 - 2026-11-18' >>"$dir/clone/CHANGELOG.md"
  git -C "$dir/clone" add -A
  git -C "$dir/clone" rev-parse HEAD
}

# push_script NAME TIP: runs auto-push.sh in NAME's clone; sets status and
# leaves its output in $work/out.
push_script() {
  status=0
  env -C "$work/$1/clone" BOT_KEY="$work/bot" timeout 60 bash "$script" 1.0.0-rc.3 "$2" >"$work/out" 2>&1 || status=$?
}

remote_tags() { git -C "$work/$1/remote.git" tag --list; }

# Control: a plain push lands main and the tag, so the other cases fail for
# their own reason.
tip=$(make_repo control)
push_script control "$tip"
ok "A9: control push exits 0" test "$status" -eq 0
ok "A9: control push creates the tag" test "$(remote_tags control)" = v1.0.0-rc.3
ok "A9: control push advances main" test "$(git -C "$work/control/remote.git" rev-parse main)" != "$tip"

# A9: a push rejected by a pre-receive hook exits non-zero with git's error.
tip=$(make_repo hook)
mkdir -p "$work/hook/remote.git/hooks"
printf '#!/bin/sh\necho "denied by policy" >&2\nexit 1\n' >"$work/hook/remote.git/hooks/pre-receive"
chmod +x "$work/hook/remote.git/hooks/pre-receive"
push_script hook "$tip"
ok "A9: a pre-receive rejection exits non-zero" test "$status" -ne 0
ok "A9: a pre-receive rejection prints git's error" grep -Eq 'rejected|declined' "$work/out"
ok "A9: a pre-receive rejection prints the hook's message" grep -q 'denied by policy' "$work/out"
ok "A9: a pre-receive rejection creates no tag" test -z "$(remote_tags hook)"
ok "A9: a pre-receive rejection leaves main alone" test "$(git -C "$work/hook/remote.git" rev-parse main)" = "$tip"

# A9: a push rejected because main moved exits 0 with a notice and no tag.
tip=$(make_repo moved)
git clone -q "$work/moved/remote.git" "$work/moved/other"
git -C "$work/moved/other" commit -q --allow-empty -m 'fix: a newer push'
git -C "$work/moved/other" push -q origin main
newer=$(git -C "$work/moved/other" rev-parse HEAD)
push_script moved "$tip"
ok "A9: a moved main exits 0" test "$status" -eq 0
ok "A9: a moved main prints a notice" grep -qi 'notice' "$work/out"
ok "A9: a moved main creates no tag on the remote" test -z "$(remote_tags moved)"
ok "A9: a moved main stays at the newer push" test "$(git -C "$work/moved/remote.git" rev-parse main)" = "$newer"

echo "auto-release acceptance: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
