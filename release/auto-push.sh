#!/usr/bin/env bash
# Commits a prepared release as the release bot, signs its tag and pushes both.
#
#   BOT_KEY=<private key file> auto-push.sh <version> <tip sha>
#
# Runs in a checkout at <tip> whose index holds what release-prep changed.
# The tag is verified with the checks release.yml's verify job runs, against
# the signers on origin's main, before anything is pushed; main and the tag
# are then pushed atomically, so neither lands without the other. When the
# push is refused because main moved past <tip>, the newer push releases
# instead and this run ends quietly. The caller supplies the SSH transport
# (GIT_SSH_COMMAND); BOT_KEY only signs.
set -euo pipefail

if [ "$#" -ne 2 ] || [ -z "${BOT_KEY:-}" ] || [ ! -r "$BOT_KEY" ]; then
  echo "usage: BOT_KEY=<private key file> auto-push.sh <version> <tip sha>" >&2
  exit 2
fi
version=$1
tip=$2
tag="v$version"

head=$(git rev-parse HEAD)
if [ "$head" != "$tip" ]; then
  echo "auto-push: HEAD is $head, not $tip" >&2
  exit 1
fi

export GIT_AUTHOR_NAME=darkpanel-release-bot GIT_COMMITTER_NAME=darkpanel-release-bot
export GIT_AUTHOR_EMAIL=release-bot@darkpanel.invalid GIT_COMMITTER_EMAIL=release-bot@darkpanel.invalid
git commit -q -m "chore(release): prepare $version"
git -c gpg.format=ssh -c user.signingkey="$BOT_KEY" tag -s "$tag" -m "$tag"

signers=$(mktemp)
trap 'rm -f "$signers"' EXIT
fail() {
  echo "auto-push: $tag does not verify: $1" >&2
  exit 1
}
# Full ref names only, as release.yml checks them: a short name could resolve
# to a branch or another ref.
[ "$(git cat-file -t "refs/tags/$tag")" = tag ] || fail "not an annotated tag"
[ "$(git for-each-ref --format='%(tag)' "refs/tags/$tag")" = "$tag" ] || fail "the tag object names another tag"
[ "$(git rev-parse "refs/tags/$tag^{commit}")" = "$(git rev-parse HEAD)" ] || fail "it does not point at the release commit"
git fetch -q origin main
git show refs/remotes/origin/main:release/allowed_signers >"$signers"
git -c gpg.ssh.allowedSignersFile="$signers" verify-tag "refs/tags/$tag" || fail "the signature is not from a signer on origin's main"

if ! out=$(git push --atomic origin HEAD:refs/heads/main "refs/tags/$tag" 2>&1); then
  if git fetch -q origin main && [ "$(git rev-parse refs/remotes/origin/main)" != "$tip" ]; then
    echo "::notice::main moved past $tip; the newer push releases."
    exit 0
  fi
  echo "$out" >&2
  exit 1
fi
echo "$out"
