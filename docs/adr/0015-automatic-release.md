# ADR 0015: Automatic release

**Requirements:** R32

## Status

Proposed

## Context

A release needed two steps from the owner: pushing a signed tag `v<version>`
and approving the `sign` job of `release.yml` in the environment `release`.
On 2026-11-17 the owner asked for a release on every shipping push to `main`,
with no approval step.

## Decision

- **The automatic path.** `.github/workflows/auto-release.yml` runs when CI
  or Harness completes on `main`, or by hand. Once CI from the push and
  Harness (from the push, the nightly or a manual run) have both passed on
  the tip of `main`, and a commit since the current release ships (`feat`,
  `fix`, `perf`, `refactor`, `revert`, or any breaking commit), its
  `prepare` job, which holds no secret, picks the next version, runs
  `make release-prep` and the Go tests, and checks that the change touches
  only what release-prep writes. Its `push` job checks the patch's paths
  again with git and the shell alone, before the key is written, then commits
  `chore(release): prepare <version>` as `darkpanel-release-bot`, signs the
  tag `v<version>` with the bot's SSH key, verifies it with the checks of
  `release.yml`'s `verify` job and pushes `main` and the tag atomically. The
  tag runs `release.yml`, whose `sign` job no longer waits for approval. A
  pre-release line moves only its last numeric identifier; the owner leaves
  it by tagging a stable release by hand. This supersedes ADR 0007's
  approval-gated signing environment.
- **Security before.** Two barriers guarded the minisign secrets: only the
  owner could create a `v*` tag, and the owner approved `sign`. Push access
  to `main` reached neither. The tag-signature check in `verify` is no
  barrier, because it runs from the tagged commit's own `release.yml`.
- **Security after.** Push access to `main` is enough to obtain a signed
  release: `main` has no protection, a shipping push releases with no
  further credential, and `auto-release.yml` on `main` could be edited to
  disclose the bot key. The tag ruleset's `DeployKey` bypass carries no key
  id, so any deploy key with write access can push a branch with a modified
  `release.yml`, tag it `v*`, and have `sign` sign anything, which hosts with
  an update window install as root within a week. CI and the harness are a
  quality gate, not a security barrier, since both are defined by the pushed
  tree.
- **Exposure limits.** The bot key lives only in the environment
  `auto-release`, limited to the branch `main`, is used only in the `push`
  job, which runs no build tool on a fresh runner, and never reaches
  `release.yml`. Actions stay pinned by commit SHA. `sign` still verifies
  the build attestations against the tag's commit.
- **Revocation.** Delete the deploy key and its `release/allowed_signers`
  line. When a release was signed after a leak, rotate the minisign key,
  since hosts trust every signature the old key made.

## Alternatives considered

- One rewritten workflow that releases from CI completion with an unsigned
  tag created by GitHub: it drops the tag-signature check and reworks
  `release.yml`.
- Removing the approval only, and tagging with a local `make release-next`:
  it does not release on push.

## Consequences

- Every release adds a bot commit to `main`.
- A shipping push waits one CI and harness cycle before it releases.
- Every release freezes the action schemas under `schemas/released/`.
- After v1.0.0 every shipping push is a stable release, so compatibility
  windows counted in minor releases shrink to a few `feat` pushes.
