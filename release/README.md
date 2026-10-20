# Releases

`.github/workflows/release.yml` publishes a release when a signed tag
`v<version>` is pushed. Policy for users: [docs/versioning.md](../docs/versioning.md).

## One-time owner setup

Done once by the owner, never by automation. Until all five are done, the
workflow stops at its `verify` or `sign` job; `build` also stops while
`install.sh` is not committed or is stale.

1. Generate the minisign key pair with a password:
   `minisign -G -p darkpanel.pub -s darkpanel.key`.
2. Create the GitHub environment `release` with the owner as required
   reviewer and its deployment branches and tags limited to tags matching
   `v*`, and store in it the secrets `MINISIGN_KEY_1` (the text of
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

## Cutting a release

1. `make release-prep VERSION=x.y.z` freezes the action schemas under
   `schemas/released/vx.y.z/` and rewrites `CHANGELOG.md` (needs git-cliff).
2. Commit both on `main`, then push a signed tag:
   `git tag -s vx.y.z -m vx.y.z && git push origin main vx.y.z`.
3. When GitHub asks you to approve the `sign` job, first check the pushed tag
   locally: `git fetch --force origin tag vx.y.z`, then
   `git -c gpg.ssh.allowedSignersFile=release/allowed_signers verify-tag vx.y.z`,
   and compare `git rev-parse vx.y.z^{commit}` with the run's commit.

The workflow:

- `verify` checks the tag is `v` plus a semantic version, its SSH signature
  against `release/allowed_signers` on `main`, that the tagged commit is on
  `main`, and that `schemas/released/<tag>/` equals the current schemas.
- `build` checks the committed `install.sh` is current, runs `make release`
  (tarballs, SBOM, `install.sh` and a `checksums.txt` over all of them),
  writes the release notes with git-cliff (a stable release's notes cover
  every commit since the previous stable release, a beta's since the
  previous tag), and attests every file.
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

## Rotating the signing key

Generate the new key, store it as `MINISIGN_KEY_2` and `MINISIGN_PASSWORD_2`,
and add its public key to `internal/update/trustedkeys`, keeping the old one.
Releases are then signed by both keys. After at least two minor releases,
move the new key into the `_1` secrets, delete the `_2` secrets, and remove
the old public key from `internal/update/trustedkeys` in the same release.
After each change to `internal/update/trustedkeys`, run `make install-sh`
and commit `install.sh`, or the `build` job fails.
