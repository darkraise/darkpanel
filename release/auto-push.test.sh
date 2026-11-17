#!/usr/bin/env bash
# Tests for release/auto-push.sh. Run: bash release/auto-push.test.sh
# shellcheck disable=SC2016 # bash -c scripts take their arguments as $1
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/auto-push.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=owner GIT_AUTHOR_EMAIL=owner@example.invalid
export GIT_COMMITTER_NAME=owner GIT_COMMITTER_EMAIL=owner@example.invalid

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

# check NAME COMMAND...: counts COMMAND's success as a pass.
check() {
  local name=$1
  shift
  if "$@" >/dev/null 2>&1; then
    pass=$((pass + 1))
  else
    echo "FAIL $name" >&2
    fail=$((fail + 1))
  fi
}

ssh-keygen -q -t ed25519 -N '' -C release-bot@darkpanel.invalid -f "$work/bot"
ssh-keygen -q -t ed25519 -N '' -f "$work/other"

# setup NAME SIGNER_PUB: a bare remote whose main lets SIGNER_PUB sign tags,
# and a clone in $work/NAME/clone whose index holds a prepared change.
# Prints the clone's HEAD.
setup() {
  local dir="$work/$1" pub=$2
  git init -q --bare -b main "$dir/remote.git"
  git init -q -b main "$dir/seed"
  mkdir -p "$dir/seed/release"
  printf 'release-bot@darkpanel.invalid namespaces="git" %s\n' "$(cut -d' ' -f1,2 "$pub")" >"$dir/seed/release/allowed_signers"
  echo '# Changelog' >"$dir/seed/CHANGELOG.md"
  git -C "$dir/seed" add -A
  git -C "$dir/seed" commit -q -m 'feat: first'
  git -C "$dir/seed" push -q "$dir/remote.git" main
  git clone -q "$dir/remote.git" "$dir/clone"
  echo '## 1.0.0-rc.3 - 2026-11-18' >>"$dir/clone/CHANGELOG.md"
  git -C "$dir/clone" add -A
  git -C "$dir/clone" rev-parse HEAD
}

no_tag() { ! git -C "$1" rev-parse -q --verify refs/tags/v1.0.0-rc.3; }

tip=$(setup ok "$work/bot.pub")
expect ok 0 env -C "$work/ok/clone" BOT_KEY="$work/bot" bash "$script" 1.0.0-rc.3 "$tip"
check ok_subject test "$(git -C "$work/ok/remote.git" log -1 --format=%s main)" = 'chore(release): prepare 1.0.0-rc.3'
check ok_committer test "$(git -C "$work/ok/remote.git" log -1 --format=%ce main)" = release-bot@darkpanel.invalid
check ok_parent test "$(git -C "$work/ok/remote.git" rev-parse main^)" = "$tip"
check ok_tag test "$(git -C "$work/ok/remote.git" rev-parse 'v1.0.0-rc.3^{commit}')" = "$(git -C "$work/ok/remote.git" rev-parse main)"
check ok_signed git -C "$work/ok/remote.git" -c gpg.ssh.allowedSignersFile="$work/ok/seed/release/allowed_signers" verify-tag v1.0.0-rc.3

tip=$(setup moved "$work/bot.pub")
git clone -q "$work/moved/remote.git" "$work/moved/other"
git -C "$work/moved/other" commit -q --allow-empty -m 'fix: newer'
git -C "$work/moved/other" push -q origin main
expect moved 0 env -C "$work/moved/clone" BOT_KEY="$work/bot" bash "$script" 1.0.0-rc.3 "$tip"
check moved_notice grep -q '^::notice::' "$work/out"
check moved_no_tag no_tag "$work/moved/remote.git"

tip=$(setup taken "$work/bot.pub")
git clone -q "$work/taken/remote.git" "$work/taken/other"
git -C "$work/taken/other" commit -q --allow-empty -m 'fix: newer'
git -C "$work/taken/other" tag v1.0.0-rc.3
git -C "$work/taken/other" push -q origin main v1.0.0-rc.3
expect taken 1 env -C "$work/taken/clone" BOT_KEY="$work/bot" bash "$script" 1.0.0-rc.3 "$tip"
check taken_no_notice bash -c '! grep -q "::notice::" "$1"' _ "$work/out"

tip=$(setup rejected "$work/bot.pub")
mkdir -p "$work/rejected/remote.git/hooks"
printf '#!/bin/sh\necho "denied by policy" >&2\nexit 1\n' >"$work/rejected/remote.git/hooks/pre-receive"
chmod +x "$work/rejected/remote.git/hooks/pre-receive"
expect rejected 1 env -C "$work/rejected/clone" BOT_KEY="$work/bot" bash "$script" 1.0.0-rc.3 "$tip"
check rejected_error grep -q 'denied by policy' "$work/out"
check rejected_no_notice bash -c '! grep -q "::notice::" "$1"' _ "$work/out"
check rejected_no_tag no_tag "$work/rejected/remote.git"

tip=$(setup unknown "$work/other.pub")
expect unknown_signer 1 env -C "$work/unknown/clone" BOT_KEY="$work/bot" bash "$script" 1.0.0-rc.3 "$tip"
check unknown_no_push test "$(git -C "$work/unknown/remote.git" rev-parse main)" = "$tip"
check unknown_no_tag no_tag "$work/unknown/remote.git"

tip=$(setup head "$work/bot.pub")
expect head_mismatch 1 env -C "$work/head/clone" BOT_KEY="$work/bot" bash "$script" 1.0.0-rc.3 0000000000000000000000000000000000000000
check head_no_push test "$(git -C "$work/head/remote.git" rev-parse main)" = "$tip"

expect usage 2 bash "$script" 1.0.0-rc.3
expect no_key 2 env -u BOT_KEY bash "$script" 1.0.0-rc.3 "$tip"

echo "auto-push: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
