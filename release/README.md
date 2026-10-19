# Releases

`.github/workflows/release.yml` publishes a release when a signed tag
`v<version>` is pushed. Policy for users: [docs/versioning.md](../docs/versioning.md).

## One-time owner setup

Done once by the owner, never by automation. Until all four are done, the
workflow stops at its `verify` or `sign` job.

1. Generate the minisign key pair with a password:
   `minisign -G -p darkpanel.pub -s darkpanel.key`.
2. Create the GitHub environment `release` with the owner as required
   reviewer, and store in it the secrets `MINISIGN_KEY_1` (the text of
   `darkpanel.key`) and `MINISIGN_PASSWORD_1`.
3. Add the public key to `internal/update/trustedkeys`.
4. Add the tag-signing SSH public key to `release/allowed_signers` on `main`.

## Cutting a release

1. `make release-prep VERSION=x.y.z` freezes the action schemas under
   `schemas/released/vx.y.z/` and rewrites `CHANGELOG.md` (needs git-cliff).
2. Commit both on `main`, then push a signed tag:
   `git tag -s vx.y.z -m vx.y.z && git push origin main vx.y.z`.
3. Approve the `sign` job when GitHub asks.

The workflow:

- `verify` checks the tag is `v` plus a semantic version, its SSH signature
  against `release/allowed_signers` on `main`, and that
  `schemas/released/<tag>/` equals the current schemas.
- `build` runs `make release` (tarballs, SBOM, `checksums.txt`), checks the
  committed `install.sh` is current, writes the release notes with git-cliff,
  and attests every file.
- `sign` (environment `release`) verifies the attestations against this
  repository and the tag's commit, checks the amd64 binary is not a test build
  and trusts exactly the signing keys, and signs `checksums.txt` with each key
  as `checksums.txt.<key id>.minisig`.
- `publish` creates the GitHub Release (a prerelease for a beta version) and
  regenerates `releases.json` on the rolling `index` release.

Runs share the concurrency group `release`. GitHub cancels an older pending
run when a newer one queues behind a running one; re-run a cancelled release
from the Actions page.

If `publish` fails after the GitHub Release is published, re-run the failed
jobs: `publish` keeps the published release and its files unchanged and only
regenerates `releases.json`. An interrupted upload can leave a draft release;
delete that draft by hand before the re-run.

## Rotating the signing key

Generate the new key, store it as `MINISIGN_KEY_2` and `MINISIGN_PASSWORD_2`,
and add its public key to `internal/update/trustedkeys`, keeping the old one.
Releases are then signed by both keys. After at least two minor releases,
move the new key into the `_1` secrets, delete the `_2` secrets, and remove
the old public key from `internal/update/trustedkeys` in the same release.
