# Releases

`.github/workflows/release.yml` publishes a release when a signed tag
`v<version>` is pushed. Policy for users: [docs/versioning.md](../docs/versioning.md).

## One-time owner setup

The steps below are the owner's; automatic releases need the further setup
in [Automatic releases](#automatic-releases). Until these are done, the
workflow stops at its `verify` or `sign` job; `build` also stops while
`install.sh` is not committed or is stale.

1. Generate the minisign key pair with a password:
   `minisign -G -p darkpanel.pub -s darkpanel.key`.
2. Create the GitHub environment `release` with its deployment branches and
   tags limited to tags matching `v*`, and store in it the secrets `MINISIGN_KEY_1` (the text of
   `darkpanel.key`) and `MINISIGN_PASSWORD_1`.
3. Add the public key to `internal/update/trustedkeys`, then run
   `make install-sh` and commit `install.sh`; `build` fails while the
   committed file is stale.
4. Add the tag-signing SSH public key to `release/allowed_signers` on `main`,
   and set up git to sign tags with it: `git config gpg.format ssh` and
   `git config user.signingkey <path to the SSH key>`.
5. Add a tag ruleset on `v*` that lets only the owner create, update or
   delete those tags. A `v*` tag runs the `release.yml` of its own commit, so
   anyone who can push one can remove the `verify` job.

## Automatic releases

`.github/workflows/auto-release.yml` releases `main` once CI and Harness
pass on its tip and a commit since the current release ships: a `feat`,
`fix`, `perf`, `refactor` or `revert` commit (git's own `Revert "…"` subject
included), or a breaking one (a `!` after the type, or a `BREAKING CHANGE:`
footer). `docs`, `test`, `chore`, `ci`, `style` and `build` commits release
nothing.

While the newest release is a pre-release, each automatic release is the
next one (`1.0.0-rc.2` → `1.0.0-rc.3`). The owner declares a stable release
by pushing a signed tag by hand; from then on a `fix` releases a patch, a
`feat` a minor version and a breaking commit a major version.

The workflow's `prepare` job holds no secret: it picks the version, runs
`make release-prep` and the Go tests, and checks that the change touches only
what release-prep writes. The `push` job alone holds `RELEASE_BOT_SSH_KEY`
and runs no build tool: it checks the patch's paths again with git and the
shell before the key exists, then commits `chore(release): prepare <version>` to
`main` as `darkpanel-release-bot`, signs the tag `v<version>` with the bot
key, verifies it as `release.yml` does, and pushes both atomically. The tag
then runs `release.yml`.

- The bot commits to `main`, so pull before your next push. Never
  force-push `main`.
- Adding any deploy key with write access grants `v*` tag creation, because
  the tag ruleset's bypass names deploy keys, not one key.
- A release adds snapshot files under `schemas/released/` only for the
  schemas it changed.

### Setup

In this order:

1. Generate an ed25519 key with no passphrase for the bot:
   `ssh-keygen -t ed25519 -N '' -C release-bot@darkpanel.invalid -f release-bot`.
2. Add `release-bot.pub` as a deploy key with write access, and add
   `DeployKey` as a bypass actor of the `release tags` ruleset.
3. Create the environment `auto-release` with its deployment branches
   limited to `main`, and store the private key in it as
   `RELEASE_BOT_SSH_KEY`, before the workflow lands on `main`.
4. Add the public key to `release/allowed_signers` on `main` with the
   principal `release-bot@darkpanel.invalid`.
5. Remove the required reviewer from the environment `release`.
6. Confirm every `v*` tag has a published release: the workflow refuses to
   release on top of a tag that never published.
7. Only then push `auto-release.yml` to `main`.

### Recovery

- A red `Auto release` run names its cause in the failing step.
- A failed or flaky CI or Harness run releases nothing until a re-run passes
  on the same commit; or dispatch `Auto release` by hand once both have
  passed.
- A `release.yml` failure blocks later automatic releases until that tag
  publishes. When re-running it cannot fix it, fix the cause on `main` and
  cut the next version by hand. The unpublished tag stays: deleting it is no
  recovery, because the release commit and its changelog entry are already
  on `main`, and so are its snapshots when it changed any.
- A leaked bot key: delete the deploy key and its `allowed_signers` line. If
  any release was signed after the leak, rotate the minisign key as
  [Rotating the signing key](#rotating-the-signing-key) describes, since
  hosts trust every signature the old key made.

## Cutting a release

1. `make release-prep VERSION=x.y.z` freezes the action schemas new in this
   release under `schemas/released/vx.y.z/` and rewrites `CHANGELOG.md`
   (needs git-cliff).
2. Commit both on `main`, then push a signed tag:
   `git tag -s vx.y.z -m vx.y.z && git push origin main vx.y.z`.
3. Hand releases still work alongside automatic ones, and `sign` runs
   without approval.

The workflow:

- `verify` checks the tag is `v` plus a semantic version, its SSH signature
  against `release/allowed_signers` on `main`, that the tagged commit is on
  `main`, that every current schema is released in this major
  (`snapshots.sh check`), and that no released snapshot was lost since the
  previous tag (`snapshots.sh retained`).
- `build` checks the committed `install.sh` is current, runs `make release`
  (tarballs, SBOM, `install.sh` and a `checksums.txt` over all of them),
  then builds `tailscale.com/cmd/derper` at the version in
  `release/derper.version` for amd64 and arm64 with `releasetool derper`,
  adds the two `derper-<version>-linux-<arch>` binaries and `derper.json` to
  the release directory and rewrites `checksums.txt` so that the signature
  covers them, writes the release notes with git-cliff (a stable release's
  notes cover every commit since the previous stable release, a beta's since
  the previous tag), and attests every file.
- `sign` (environment `release`) verifies the attestations against this
  repository and the tag's commit, checks every file against `checksums.txt`,
  checks the amd64 binary reports the tag's version, is not a test build and
  trusts exactly the signing keys, and signs `checksums.txt` with each key as
  `checksums.txt.<key id>.minisig`.
- `publish` creates the GitHub Release (a prerelease for a beta version; not
  marked Latest when a higher stable release is already published) and
  regenerates `releases.json` on the rolling `index` release.

Runs share the concurrency group `release`. GitHub cancels an older pending
run when a newer one queues behind a running one; re-run a cancelled release
from the Actions page.

If `publish` fails after the GitHub Release is published, re-run the failed
jobs: `publish` keeps the published release and its files unchanged and only
regenerates `releases.json`. An interrupted upload can leave a draft release;
delete that draft by hand before the re-run.

Leave GitHub's immutable releases setting off for this repository: every
`publish` replaces `releases.json` on the published `index` release with
`gh release upload --clobber`, and an immutable release's files can be
neither replaced nor deleted.

## The derper version

`release/derper.version` holds the derper release Darkpanel builds,
publishes and installs. A DERP host's tailscaled is pinned to the same
version, so a host receives a Tailscale update only when a Darkpanel release
bumps `release/derper.version`. Bumping it promptly after a Tailscale
security release is part of the release routine.

## Rotating the signing key

Generate the new key, store it as `MINISIGN_KEY_2` and `MINISIGN_PASSWORD_2`,
and add its public key to `internal/update/trustedkeys`, keeping the old one.
Releases are then signed by both keys. After at least two minor releases,
move the new key into the `_1` secrets, delete the `_2` secrets, and remove
the old public key from `internal/update/trustedkeys` in the same release.
After each change to `internal/update/trustedkeys`, run `make install-sh`
and commit `install.sh`, or the `build` job fails.
