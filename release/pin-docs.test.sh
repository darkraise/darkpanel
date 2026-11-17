#!/usr/bin/env bash
# Tests for release/pin-docs.sh. Run: bash release/pin-docs.test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/pin-docs.sh"
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

# same NAME FILE TEXT: checks FILE holds TEXT.
same() {
  if [ "$(cat "$2")" = "$3" ]; then
    pass=$((pass + 1))
  else
    echo "FAIL $1: $2 holds:" >&2
    cat "$2" >&2
    fail=$((fail + 1))
  fi
}

export DOCS_ROOT="$work/repo"
seed() {
  mkdir -p "$DOCS_ROOT/docs/user"
  printf '%s\n' "$1" >"$DOCS_ROOT/README.md"
  printf '%s\n' "$2" >"$DOCS_ROOT/docs/user/install.md"
}

readme='curl -fsSL https://github.com/Darkraise/darkpanel/releases/download/v1.0.0-rc.2/install.sh | sudo sh'
# shellcheck disable=SC2016 # the backticks are Markdown, not a command
install='- `--version X.Y.Z` installs that version.
curl -fsSL https://github.com/Darkraise/darkpanel/releases/latest/download/install.sh | sudo sh
curl -fsSL https://github.com/Darkraise/darkpanel/releases/download/v1.0.0-rc.2/install.sh | sudo sh
curl -fsSL https://github.com/Darkraise/darkpanel/releases/download/v1.0.0-rc.2/install.sh | sudo sh -s -- --role controller --version 1.0.0-rc.2 --agent-advertise <address agents reach>:7443'

seed "$readme" "$install"
expect prerelease 0 bash "$script" 1.0.0-rc.3
same prerelease_readme "$DOCS_ROOT/README.md" "${readme//1.0.0-rc.2/1.0.0-rc.3}"
same prerelease_install "$DOCS_ROOT/docs/user/install.md" "${install//1.0.0-rc.2/1.0.0-rc.3}"

seed "$readme" "$install"
expect stable 0 bash "$script" 1.0.0
same stable_readme "$DOCS_ROOT/README.md" "$readme"
same stable_install "$DOCS_ROOT/docs/user/install.md" "$install"

seed "$readme" "${install//1.0.0-rc.2/1.0.0-rc.1}"
expect mixed 1 bash "$script" 1.0.0-rc.3
same mixed_readme "$DOCS_ROOT/README.md" "$readme"
same mixed_install "$DOCS_ROOT/docs/user/install.md" "${install//1.0.0-rc.2/1.0.0-rc.1}"

nopin='curl -fsSL https://github.com/Darkraise/darkpanel/releases/latest/download/install.sh | sudo sh'
seed "$nopin" "$nopin"
expect nopin 0 bash "$script" 1.1.0-beta.1
same nopin_readme "$DOCS_ROOT/README.md" "$nopin"

expect usage 2 bash "$script"
expect not_a_version 2 bash "$script" 1.0

echo "pin-docs: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
