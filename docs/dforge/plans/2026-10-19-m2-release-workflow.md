# M2 Release Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build part P6 of the M2 spec: a tag-triggered release workflow that verifies the signed tag and the frozen schema snapshots, builds reproducible tarballs, an SBOM, release notes and attestations, signs `checksums.txt` with minisign in the owner-approved `release` environment, publishes the GitHub Release and regenerates `releases.json`; plus the Makefile release targets, `cliff.toml`, the CI shellcheck job and `docs/versioning.md` (R80). Plan 4's other part, P7 (web host pages), gets its own plan after plan 1 (P0) merges.

**Architecture:** Every piece of logic that can be tested locally lives outside the workflow YAML: a stdlib-only Go command `release/releasetool` (tag and version checks, the `releases.json` index, minisign key ids, the release binary's trust check) and three bash scripts under `release/` (schema snapshots, packaging, signing), each with its own test. The Makefile and `.github/workflows/release.yml` only call them.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`, standard library only), bash, GNU tar and coreutils, shellcheck, GitHub Actions, `gh`, git-cliff 2.14.2, cyclonedx-gomod 1.12.0, minisign from `aead.dev/minisign/cmd/minisign` v0.3.0, actionlint 1.7.12 (verification only).

**Spec:** docs/dforge/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves

**Plan review:** 2026-10-19 — dr-superpowers:judge-opus — executability 17 / coherence 18 / coverage 18 / assumptions 16 (round 1)

**Parallelism:** waves — 1-3 | 4 | 5-6 | 7

**Integration check:** `go test -count=1 ./release/... && make lint-sh test-release && GOTOOLCHAIN=local go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml .github/workflows/release.yml`

**Worktree setup:** none

## Global Constraints

- Build only with the toolchain `go.mod` pins: `GOTOOLCHAIN=local` (ADR 0001). The Makefile exports it already.
- This plan never edits `go.mod` or `go.sum` (plan 1 owns them in the same wave): `release/releasetool` imports the standard library only.
- Owner prerequisites are never tasks (spec section 9): no task generates a minisign or SSH key, creates the GitHub `release` environment, or stores a secret. Until the owner does, the workflow's `verify` and `sign` jobs fail closed.
- Third-party GitHub Actions are pinned by full commit SHA with the version in a trailing comment (ADR 0007); release tools are pinned by version and checksum. Use exactly the pins in Contracts.
- Release version strings are semantic versions without a leading `v` (`1.3.0`, `1.3.0-beta.1`); tags are `v` plus that version. A version with a prerelease part is channel `beta`, every other one `stable`.
- Every shell script starts with `#!/usr/bin/env bash` and `set -euo pipefail`, and passes `shellcheck` (0.9 or newer). When `command -v shellcheck` finds none on the development host, install it with `apt-get install -y shellcheck` (Debian package 0.10.0).
- Shell tests use only `mktemp -d` directories they delete on exit; Go tests use `t.TempDir()`. No test binds a port or writes outside its temporary directory, so tasks of one wave never meet at run time.
- Comments follow the repository style: full sentences, explaining why. New comments name no plan, register row or review.
- Lint: `golangci-lint run --allow-serial-runners` and `go vet ./...` stay clean (tasks of one wave may lint at the same time; golangci-lint holds a shared lock).
- Commits: `<type>(<scope>): <subject>` with type one of `feat`, `fix`, `docs`, `style`, `refactor`, `test`, `chore`, `perf`; subject at most 50 characters, imperative, no period, no attribution lines.

## Contracts

**Release files** (published under `<base>/v<version>/`, `<base>` = `https://github.com/Darkraise/darkpanel/releases/download`):

- `darkpanel_<version>_linux_amd64.tar.gz`, `darkpanel_<version>_linux_arm64.tar.gz`: gzip tar archives holding exactly three entries at the archive root, in this order: `darkpanel` (mode 0755), `LICENSE`, `README.md` (mode 0644); owner and group `0/0`, mtime the commit time, reproducible.
- `darkpanel_<version>.cdx.json`: CycloneDX 1.6 JSON SBOM of `cmd/darkpanel` for linux/amd64.
- `checksums.txt`: `sha256sum` text lines `<64 hex>  <file name>`, covering the two tarballs and the SBOM and nothing else, sorted by file name in the C locale. `package.sh` runs before `install.sh` is copied into the release directory, so it lists every file present then except `checksums.txt` and `*.minisig`.
- `checksums.txt.<key id>.minisig`: one minisign signature per signing key, hashed mode (algorithm bytes `ED`), trusted comment exactly `version=<version> channel=<stable|beta> purpose=checksums`. `<key id>` is the key id as exactly 16 upper-case hex digits, zero-padded (`fmt.Sprintf("%016X", id)`, where `id` is the little-endian `uint64` of signature bytes 2 to 9).
- `install.sh`: the committed, generated script, attached unchanged; not listed in `checksums.txt`.
- `releases.json` on the rolling prerelease `index` (`<base>/index/releases.json`): `{"formatVersion": 1, "releases": [{"version": "<version>", "channel": "stable|beta"}, …]}`, every published non-draft `v<semver>` release, newest first by semantic-version precedence, written by `json.Encoder` with two-space indentation and a trailing newline. Each release object has exactly two members, `version` then `channel`, in that order: `install.sh` has no JSON parser and reads them positionally (whitespace is free).

**`release/releasetool`** (Go `package main`, run as `go run ./release/releasetool <command>`; exit 0 on success, 1 with `releasetool: <error>` on standard error otherwise):

- `check-tag <tag>`: accepts `v<semver>`; prints `version=<version>\nchannel=<stable|beta>\n` (the `$GITHUB_OUTPUT` format).
- `check-version <version>`: accepts a semantic version without `v` and without build metadata; prints nothing.
- `index`: reads the JSON array of `gh release list --json tagName,isDraft` on standard input, writes `releases.json` on standard output.
- `keyid <file.minisig>`: prints the signature's key id (16 upper-case hex digits) and a newline; refuses an algorithm other than `ED`.
- `check-build <id>[,<id>…]`: reads `darkpanel version --json` on standard input; succeeds only when `testBuild` is present and `false` and `trustedKeys` equals the given ids as a set.

**Scripts** (each prints `usage: …` and exits 2 on a wrong argument count):

- `release/snapshots.sh copy <tag>`: copies the current snapshots — the files matching `*/v*.json` exactly one directory below `schemas/` (`schemas/<action>/v<n>.json`), outside `schemas/released/` — to `schemas/released/<tag>/`, keeping relative paths; any other file (`embed.go`, `*_test.go`, `testdata/`, `released/README.md`) is never copied. Exits 1 when the destination exists. `release/snapshots.sh check <tag>`: exits 1 unless `schemas/released/<tag>/` holds exactly the same relative paths, nothing else, each byte-equal (`cmp`) to its current snapshot. `SCHEMAS_DIR` overrides the schemas directory.
- `release/package.sh <version> <bin dir> <out dir>`: reads `<bin dir>/darkpanel-linux-amd64` and `-arm64`, writes both tarballs into `<out dir>`, then writes `<out dir>/checksums.txt`. `SOURCE_DATE_EPOCH` overrides the mtime (default: the last commit time).
- `release/sign.sh <version> <stable|beta> <release dir> <version.json>`: signs `<release dir>/checksums.txt` with each key present in `MINISIGN_KEY_1`/`MINISIGN_PASSWORD_1` and `MINISIGN_KEY_2`/`MINISIGN_PASSWORD_2` (each key is the text of a minisign secret key file), then runs `check-build` with those ids, and only then moves the `checksums.txt.<key id>.minisig` files into `<release dir>`; exits 1, leaving no `.minisig`, when no key is set, any signing fails, or the check fails. `MINISIGN` names the minisign command (default `minisign`).

**Makefile targets:** `make release VERSION=x.y.z` (writes `bin/release/`); `make release-prep VERSION=x.y.z`; `make install-sh [KEYS=harness]` (runs `go run [-tags harnesskeys] ./scripts/gen-install-sh -out install.sh`, the generator plan 1 creates); `make lint-sh`; `make test-release`. `VERSION` and `COMMIT` fall back to `dev` and `unknown` outside a git checkout.

**GitHub `release` environment secrets** (created by the owner): `MINISIGN_KEY_1`, `MINISIGN_PASSWORD_1`, and during a key rotation `MINISIGN_KEY_2`, `MINISIGN_PASSWORD_2`.

**Pins:**

| What | Pin |
|---|---|
| `actions/checkout` | `3d3c42e5aac5ba805825da76410c181273ba90b1` (v7.0.1) |
| `actions/setup-go` | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` (v7.0.0) |
| `pnpm/action-setup` | `ea17c68df8912ef543352723c149a84f56e3d413` (v6.1.0) |
| `actions/setup-node` | `949feb2413d6458794dcd2491c4babbbce0c15c1` (v7.1.0) |
| `actions/upload-artifact` | `cf430e030ddbb5b0abf93d22962f4752f3646cd9` (v7.0.2) |
| `actions/download-artifact` | `9000827ccba6bdab643e8b6fd33ac0654aef8333` (v8.0.2) |
| `actions/attest-build-provenance` | `4d101475d8b20a2381f78447822ac1eab6504dd8` (v4.2.2) |
| git-cliff | `git-cliff-2.14.2-x86_64-unknown-linux-gnu.tar.gz`, sha256 `24f397c733add5390fdceee3a2088588ab0d5f944ce00d34cb7029b888cf2db4` |
| cyclonedx-gomod | `cyclonedx-gomod_1.12.0_linux_amd64.tar.gz`, sha256 `004b9f5cc595b797fb5423e2ae4c97bcf0f18c712ed2faee1640b09e5efd6d15` |
| minisign | `go install aead.dev/minisign/cmd/minisign@v0.3.0`, module hash `h1:8Xafzy5PEVZqYDNP60yJHARlW1eOQtsKNp/Ph2c0vRA=` |
| actionlint | `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12` |

## Assumptions (evidence)

- Action SHAs: `gh api repos/<owner>/<repo>/commits/<tag> --jq .sha` on 2026-10-19 for download-artifact v8.0.2, attest-build-provenance v4.2.2 and upload-artifact v7.0.2; the other four are the pins `.github/workflows/ci.yml` already uses. Each is the latest release on 2026-10-19 (`gh api repos/<r>/releases/latest`).
- git-cliff v2.14.2 (2026-09-18) and cyclonedx-gomod v1.12.0 (2026-08-22) are the latest releases; their tarball sha256 values come from the GitHub release asset digests and were recomputed with `sha256sum` on downloaded copies on 2026-10-19. The git-cliff tarball holds `git-cliff-2.14.2/git-cliff`; the cyclonedx-gomod tarball holds `cyclonedx-gomod` at its root (`tar tzf`, same day).
- `cyclonedx-gomod app -json -licenses -main cmd/darkpanel -output <file> .` with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` ran on this repository on 2026-10-19: CycloneDX 1.6, 22 components, main component `github.com/darkraise/darkpanel`, and `go.mod`/`go.sum` unchanged. One SBOM covers both architectures because the module graph does not depend on GOARCH (both builds are pure Go, `CGO_ENABLED=0`).
- git-cliff 2.14.2 accepts `--tag`, `--unreleased`, `--current`, `--strip`, `-o` and `-c` (`git-cliff --help`, 2026-10-19). The `cliff.toml` of Task 6 rendered this repository's history with `--unreleased --tag v0.1.0` on 2026-10-19 (grouped Features and Fixes; merge and non-conventional commits skipped).
- minisign v0.3.0 (`aead.dev/minisign`): the CLI's `signFiles` reads a password from standard input as one newline-terminated line when standard input is not a terminal (cmd/minisign/minisign.go `readPassword`, lines 525-537, and `signFiles` line 222), and signs through `minisign.NewReader` and `Reader.SignWithComments`, which signs the BLAKE2b-512 hash with algorithm `HashEdDSA` = `0x4445` = bytes `ED` (minisign.go lines 33, 116-118, 164-168). Key ids are `binary.LittleEndian.Uint64` of signature bytes 2 to 9 (public.go line 100). The CLI's own untrusted comment prints the id without zero padding (minisign.go line 99), which is why `releasetool keyid` derives the file name from the signature bytes, not from minisign's output. Source read from the module proxy zip on 2026-10-19; module hash from `sum.golang.org/lookup/aead.dev/minisign@v0.3.0`.
- `gh attestation verify` supports `--repo`, `--signer-workflow`, `--source-ref`, `--source-digest` and `--deny-self-hosted-runners` (cli/cli `pkg/cmd/attestation/verify/verify.go`, lines 249-268, on 2026-10-19; gh v2.102.0 is current). The `ubuntu-24.04` runner image (version 20261012.334.1) ships GitHub CLI 2.102.0 (actions/runner-images `images/ubuntu/Ubuntu2404-Readme.md`, 2026-10-19). The development host's gh 2.46.0 has no `attestation` command, so the sign job's verification is exercised only on GitHub.
- The repository is public (`gh repo view --json visibility`, 2026-10-19), so artifact attestations are available on the free plan. No environment exists yet (`gh api repos/darkraise/darkpanel/environments`, 2026-10-19).
- GitHub's `ubuntu-24.04` runner image ships shellcheck 0.9.0 and jq 1.7 (actions/runner-images `images/ubuntu/Ubuntu2404-Readme.md`, 2026-10-19), so the CI job installs nothing.
- `actionlint` 1.7.12 is clean on the current workflows (`go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`, 2026-10-19, run without shellcheck installed). After Task 6 installs shellcheck it also checks `run:` blocks, so the plan's checks name only the two workflows this plan writes.
- GNU tar 1.35 keeps command-line operand order under `--sort=name` and writes `0/0` with `--owner=0 --group=0 --numeric-owner` (checked on 2026-10-19). GNU Make 4.4.1 supports `$(or …)`.
- The snapshot set is `schemas/<action>/v<n>.json` only, narrower than the spec's "every snapshot under `schemas/` except `schemas/released/` and `embed.go`": plan 1 makes `schemas/` a Go package holding `embed.go`, `embed_test.go` and `released/README.md`, and freezing a `_test.go` file into `schemas/released/<tag>/` would add Go files to the released tree. Every snapshot has that shape: `ls schemas/*/` on 2026-10-19 lists only `v<n>.json` files.
- `make release` and `release` (the directory) share a name; the target is `.PHONY`, so the directory never satisfies it.
- The tag-signature check reads `release/allowed_signers` from `origin/main`, not from the tagged commit: a forged tag could carry its own signer in the tagged tree. This reads the spec's "`git verify-tag` against `release/allowed_signers`" as the file on `main`.
- The workflow-level `concurrency: release` group keeps one run in progress and one pending; GitHub cancels an older pending run when a third arrives. Tags are pushed one at a time by the owner, so this is accepted; `release/README.md` says to re-run a cancelled release.
- `darkpanel version --json` gains `trustedKeys` (a list of 16-hex key ids) and `testBuild` in plan 6 (P12, `cmd/darkpanel`); until then `check-build` fails on the missing `testBuild` field, which keeps `sign` closed. `scripts/gen-install-sh` comes from plan 1 (P0) and the committed `install.sh` from plan 3 (P4); until both exist `make install-sh` and the build job's `install.sh` check fail, which keeps a release closed.
- Register: this plan discharges row 16 (R80). Its acceptance ("release.yml green on a signed test tag") needs the owner prerequisites and plans 1, 3 and 6 merged, so the row ends at `verify` when this plan finishes. Row 11 (R32) and row 13 (R34) stay with the update library and services plans; `docs/versioning.md` (Task 5) also states R34's backup rule that row 13's acceptance names.
- Executor lane: the codex gate printed `lane=false reason=plugin-not-enabled` on 2026-10-19, so no task carries an Executor line.
- Unverified — Task 7 cannot verify it: the workflow's end-to-end behaviour on GitHub (verify-tag, attestation, signing, publication). The first signed test tag after the owner prerequisites verifies it; that run is row 16's acceptance.

## Task index

1. releasetool command
2. Schema snapshot script
3. Release packaging script
4. Signing script
5. Versioning policy
6. Makefile targets, cliff.toml and CI job
7. Release workflow

---

### Task 1: releasetool command

**Files:**
- Create: `release/releasetool/main.go`
- Create: `release/releasetool/versions.go`
- Test: `release/releasetool/releasetool_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: the `release/releasetool` commands `check-tag`, `check-version`, `index`, `keyid`, `check-build`, and the `releases.json` format (Contracts).

**Items:** 16

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `release/releasetool/releasetool_test.go`:

```go
package main

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersion(t *testing.T) {
	for _, s := range []string{"0.1.0", "1.2.3", "1.3.0-beta.1", "1.0.0-rc.1.2", "1.0.0-x-y.7"} {
		if _, err := parseVersion(s); err != nil {
			t.Errorf("parseVersion(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range []string{"", "1.2", "v1.2.3", "01.2.3", "1.2.3-", "1.2.3-01", "1.2.3+build.1", "1.2.3-beta..1"} {
		if _, err := parseVersion(s); err == nil {
			t.Errorf("parseVersion(%q) = nil, want an error", s)
		}
	}
}

func TestCompareOrder(t *testing.T) {
	// Ascending precedence: the semver.org section 11 example, then minor and
	// major steps where string order and numeric order disagree.
	order := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
		"1.3.0-beta.1", "1.3.0", "1.10.0", "2.0.0",
	}
	vs := make([]version, len(order))
	for i, s := range order {
		v, err := parseVersion(s)
		if err != nil {
			t.Fatal(err)
		}
		vs[i] = v
	}
	for i := range vs {
		for j := range vs {
			if got, want := compare(vs[i], vs[j]), cmp.Compare(i, j); got != want {
				t.Errorf("compare(%s, %s) = %d, want %d", order[i], order[j], got, want)
			}
		}
	}
}

func TestRunCheckTag(t *testing.T) {
	tests := []struct {
		tag  string
		want string
		ok   bool
	}{
		{"v1.3.0", "version=1.3.0\nchannel=stable\n", true},
		{"v1.3.0-beta.1", "version=1.3.0-beta.1\nchannel=beta\n", true},
		{"1.3.0", "", false},
		{"v1.3", "", false},
		{"index", "", false},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		err := run([]string{"check-tag", tt.tag}, strings.NewReader(""), &out)
		if (err == nil) != tt.ok || out.String() != tt.want {
			t.Errorf("check-tag %q = %q, %v; want %q, ok %v", tt.tag, out.String(), err, tt.want, tt.ok)
		}
	}
}

func TestRunCheckVersion(t *testing.T) {
	if err := run([]string{"check-version", "1.2.3"}, strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Errorf("check-version 1.2.3 = %v, want nil", err)
	}
	for _, s := range []string{"v1.2.3", "dev", "1.2"} {
		if err := run([]string{"check-version", s}, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Errorf("check-version %q = nil, want an error", s)
		}
	}
}

func TestRunIndex(t *testing.T) {
	// gh release list order (newest publication first): the rolling index
	// release, a draft, and betas published after the latest stable.
	list := `[
  {"tagName": "index", "isDraft": false},
  {"tagName": "v1.3.0-beta.2", "isDraft": false},
  {"tagName": "v1.4.0", "isDraft": true},
  {"tagName": "v1.3.0-beta.1", "isDraft": false},
  {"tagName": "v1.2.10", "isDraft": false},
  {"tagName": "v1.2.1", "isDraft": false},
  {"tagName": "v1.2.0", "isDraft": false}
]`
	want := `{
  "formatVersion": 1,
  "releases": [
    {
      "version": "1.3.0-beta.2",
      "channel": "beta"
    },
    {
      "version": "1.3.0-beta.1",
      "channel": "beta"
    },
    {
      "version": "1.2.10",
      "channel": "stable"
    },
    {
      "version": "1.2.1",
      "channel": "stable"
    },
    {
      "version": "1.2.0",
      "channel": "stable"
    }
  ]
}
`
	var out bytes.Buffer
	if err := run([]string{"index"}, strings.NewReader(list), &out); err != nil {
		t.Fatalf("index: %v", err)
	}
	// The exact comparison also fixes the member order install.sh relies on:
	// version, then channel, and nothing else in a release object.
	if out.String() != want {
		t.Errorf("index =\n%s\nwant\n%s", out.String(), want)
	}

	out.Reset()
	if err := run([]string{"index"}, strings.NewReader("[]"), &out); err != nil {
		t.Fatalf("index of no releases: %v", err)
	}
	if want := "{\n  \"formatVersion\": 1,\n  \"releases\": []\n}\n"; out.String() != want {
		t.Errorf("index of no releases = %q, want %q", out.String(), want)
	}

	if err := run([]string{"index"}, strings.NewReader("not json"), &bytes.Buffer{}); err == nil {
		t.Error("index of invalid JSON = nil, want an error")
	}
}

// minisig returns a minisign signature file with the given algorithm bytes
// and key id; the signatures themselves are zero.
func minisig(alg string, id uint64) []byte {
	raw := make([]byte, 74)
	copy(raw, alg)
	binary.LittleEndian.PutUint64(raw[2:10], id)
	return []byte("untrusted comment: signature from minisign secret key\n" +
		base64.StdEncoding.EncodeToString(raw) + "\n" +
		"trusted comment: version=1.2.3 channel=stable purpose=checksums\n" +
		base64.StdEncoding.EncodeToString(make([]byte, 64)) + "\n")
}

func TestKeyID(t *testing.T) {
	tests := []struct {
		name string
		sig  []byte
		want string
	}{
		{"hashed", minisig("ED", 0x0123456789ABCDEF), "0123456789ABCDEF"},
		// minisign prints this id without its leading zeros; file names pad it.
		{"zero padded", minisig("ED", 0xABCDEF), "0000000000ABCDEF"},
	}
	for _, tt := range tests {
		if got, err := keyID(tt.sig); err != nil || got != tt.want {
			t.Errorf("%s: keyID = %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}
	for name, sig := range map[string][]byte{
		"legacy algorithm": minisig("Ed", 1),
		"no comment line":  []byte("not a signature\n"),
		"short signature":  []byte("untrusted comment: x\nAAAA\n"),
		"not base64":       []byte("untrusted comment: x\n!!!!\n"),
	} {
		if _, err := keyID(sig); err == nil {
			t.Errorf("%s: keyID = nil error, want an error", name)
		}
	}
}

func TestRunKeyID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checksums.txt.minisig")
	if err := os.WriteFile(path, minisig("ED", 0x0123456789ABCDEF), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run([]string{"keyid", path}, strings.NewReader(""), &out); err != nil {
		t.Fatalf("keyid: %v", err)
	}
	if out.String() != "0123456789ABCDEF\n" {
		t.Errorf("keyid = %q, want %q", out.String(), "0123456789ABCDEF\n")
	}
	if err := run([]string{"keyid", filepath.Join(t.TempDir(), "missing")}, strings.NewReader(""), &out); err == nil {
		t.Error("keyid of a missing file = nil, want an error")
	}
}

func TestCheckBuild(t *testing.T) {
	const a, b = "A000000000000000", "B000000000000000"
	tests := []struct {
		name string
		info string
		keys []string
		ok   bool
	}{
		{"one key", `{"testBuild":false,"trustedKeys":["A000000000000000"]}`, []string{a}, true},
		{"rotation in any order", `{"testBuild":false,"trustedKeys":["B000000000000000","A000000000000000"]}`, []string{a, b}, true},
		{"test build", `{"testBuild":true,"trustedKeys":["A000000000000000"]}`, []string{a}, false},
		{"no testBuild field", `{"trustedKeys":["A000000000000000"]}`, []string{a}, false},
		{"binary trusts an extra key", `{"testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`, []string{a}, false},
		{"signing key not trusted", `{"testBuild":false,"trustedKeys":["A000000000000000"]}`, []string{a, b}, false},
		{"no signing keys", `{"testBuild":false,"trustedKeys":[]}`, nil, false},
		{"not JSON", `darkpanel dev`, []string{a}, false},
	}
	for _, tt := range tests {
		if err := checkBuild([]byte(tt.info), tt.keys); (err == nil) != tt.ok {
			t.Errorf("%s: checkBuild = %v, want ok %v", tt.name, err, tt.ok)
		}
	}
}

func TestRunCheckBuild(t *testing.T) {
	info := `{"testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`
	if err := run([]string{"check-build", "A000000000000000,B000000000000000"}, strings.NewReader(info), &bytes.Buffer{}); err != nil {
		t.Errorf("check-build of both keys = %v, want nil", err)
	}
	if err := run([]string{"check-build", ""}, strings.NewReader(info), &bytes.Buffer{}); err == nil {
		t.Error("check-build of no keys = nil, want an error")
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"publish"}, {"check-tag"}, {"index", "extra"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("run(%q) = %v, want the usage error", args, err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./release/releasetool/`
Expected: FAIL — the build reports `undefined: parseVersion` (and the other functions).

- [ ] **Step 3: Write the implementation**

Create `release/releasetool/versions.go`:

```go
package main

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// semverPattern is the semver.org 2.0.0 grammar without build metadata,
// which has no place in a tag or a release file name.
var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?$`)

type version struct {
	text       string
	core       [3]uint64
	prerelease []string
}

func parseVersion(s string) (version, error) {
	m := semverPattern.FindStringSubmatch(s)
	if m == nil {
		return version{}, fmt.Errorf("%q is not a semantic version x.y.z or x.y.z-prerelease", s)
	}
	v := version{text: s}
	for i := range 3 {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return version{}, fmt.Errorf("%q: %w", s, err)
		}
		v.core[i] = n
	}
	if m[4] != "" {
		v.prerelease = strings.Split(m[4], ".")
	}
	return v, nil
}

// parseTag accepts a release tag, v<semantic version>.
func parseTag(tag string) (version, error) {
	s, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return version{}, fmt.Errorf("tag %q does not start with v", tag)
	}
	return parseVersion(s)
}

func (v version) channel() string {
	if len(v.prerelease) > 0 {
		return "beta"
	}
	return "stable"
}

// compare orders versions by semantic-version precedence (semver.org
// section 11): a prerelease sorts before its release.
func compare(a, b version) int {
	for i := range 3 {
		if c := cmp.Compare(a.core[i], b.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.prerelease) == 0 && len(b.prerelease) == 0:
		return 0
	case len(a.prerelease) == 0:
		return 1
	case len(b.prerelease) == 0:
		return -1
	}
	for i := 0; i < len(a.prerelease) && i < len(b.prerelease); i++ {
		if c := compareIdentifier(a.prerelease[i], b.prerelease[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.prerelease), len(b.prerelease))
}

// compareIdentifier compares numeric identifiers numerically and sorts them
// before alphanumeric ones, which compare in ASCII order.
func compareIdentifier(a, b string) int {
	an, aErr := strconv.ParseUint(a, 10, 64)
	bn, bErr := strconv.ParseUint(b, 10, 64)
	switch {
	case aErr == nil && bErr == nil:
		return cmp.Compare(an, bn)
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// ghRelease is one entry of `gh release list --json tagName,isDraft`.
type ghRelease struct {
	TagName string `json:"tagName"`
	IsDraft bool   `json:"isDraft"`
}

// indexEntry's field order is a contract: install.sh has no JSON parser and
// reads version, then channel, from each release object.
type indexEntry struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
}

// releaseIndex is releases.json. It only selects versions; the updater and
// install.sh take trust from each version's own signatures.
type releaseIndex struct {
	FormatVersion int          `json:"formatVersion"`
	Releases      []indexEntry `json:"releases"`
}

// buildIndex lists every published v<semver> release, newest first. Drafts
// and other tags, such as the rolling index release, are skipped.
func buildIndex(list []ghRelease) releaseIndex {
	var versions []version
	for _, r := range list {
		if r.IsDraft {
			continue
		}
		v, err := parseTag(r.TagName)
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}
	slices.SortFunc(versions, func(a, b version) int { return compare(b, a) })
	idx := releaseIndex{FormatVersion: 1, Releases: []indexEntry{}}
	for _, v := range versions {
		idx.Releases = append(idx.Releases, indexEntry{Version: v.text, Channel: v.channel()})
	}
	return idx
}
```

Create `release/releasetool/main.go`:

```go
// Command releasetool holds the release workflow's logic that can be tested
// locally: tag and version checks, the releases.json index, minisign key ids,
// and the check that a release binary is not a test build and trusts exactly
// the keys that sign it.
package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

const usage = `usage: releasetool check-tag <tag>
       releasetool check-version <version>
       releasetool index < release-list.json > releases.json
       releasetool keyid <file.minisig>
       releasetool check-build <key id>[,<key id>] < version.json`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "releasetool:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	cmd, rest := args[0], args[1:]
	switch {
	case cmd == "check-tag" && len(rest) == 1:
		v, err := parseTag(rest[0])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "version=%s\nchannel=%s\n", v.text, v.channel())
		return err
	case cmd == "check-version" && len(rest) == 1:
		_, err := parseVersion(rest[0])
		return err
	case cmd == "index" && len(rest) == 0:
		var list []ghRelease
		if err := json.NewDecoder(stdin).Decode(&list); err != nil {
			return fmt.Errorf("release list: %w", err)
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(buildIndex(list))
	case cmd == "keyid" && len(rest) == 1:
		sig, err := os.ReadFile(rest[0]) //nolint:gosec // G304: the signature file named on the command line
		if err != nil {
			return err
		}
		id, err := keyID(sig)
		if err != nil {
			return fmt.Errorf("%s: %w", rest[0], err)
		}
		_, err = fmt.Fprintln(stdout, id)
		return err
	case cmd == "check-build" && len(rest) == 1:
		info, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		keys := strings.FieldsFunc(rest[0], func(r rune) bool { return r == ',' })
		return checkBuild(info, keys)
	}
	return errors.New(usage)
}

// keyID returns the key id of a minisign signature file as 16 upper-case hex
// digits, the form the published file names use
// (checksums.txt.<key id>.minisig). It requires the hashed algorithm "ED",
// the only one install.sh and the updater accept.
func keyID(sig []byte) (string, error) {
	lines := strings.Split(string(sig), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "untrusted comment: ") {
		return "", errors.New("not a minisign signature file")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return "", fmt.Errorf("signature line: %w", err)
	}
	if len(raw) != 74 {
		return "", fmt.Errorf("signature line is %d bytes, want 74", len(raw))
	}
	if string(raw[:2]) != "ED" {
		return "", fmt.Errorf("signature algorithm %q, want the hashed algorithm ED", raw[:2])
	}
	return fmt.Sprintf("%016X", binary.LittleEndian.Uint64(raw[2:10])), nil
}

// buildInfo is the part of `darkpanel version --json` the release checks.
type buildInfo struct {
	TestBuild   *bool    `json:"testBuild"`
	TrustedKeys []string `json:"trustedKeys"`
}

// checkBuild refuses a binary built for tests, and one whose trusted keys
// differ from the keys signing the release: hosts running it could not
// verify the next release, or would trust a key the release does not use.
func checkBuild(versionJSON []byte, signing []string) error {
	var b buildInfo
	if err := json.Unmarshal(versionJSON, &b); err != nil {
		return fmt.Errorf("version --json: %w", err)
	}
	if b.TestBuild == nil {
		return errors.New("version --json has no testBuild field")
	}
	if *b.TestBuild {
		return errors.New("the release binary is a test build")
	}
	if len(signing) == 0 {
		return errors.New("no signing key ids")
	}
	got := slices.Sorted(slices.Values(b.TrustedKeys))
	want := slices.Sorted(slices.Values(signing))
	if !slices.Equal(got, want) {
		return fmt.Errorf("the binary trusts %v but the release is signed by %v", got, want)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 ./release/releasetool/ && go vet ./release/... && golangci-lint run --allow-serial-runners ./release/...`
Expected: `ok  	github.com/darkraise/darkpanel/release/releasetool`, then no vet output and `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add release/releasetool
git commit -m "feat(release): add the releasetool command"
```

### Task 2: Schema snapshot script

**Files:**
- Create: `release/snapshots.sh`
- Test: `release/snapshots.test.sh`

**Interfaces:**
- Consumes: nothing.
- Produces: `release/snapshots.sh copy|check <tag>` (Contracts, Scripts).

**Items:** 16

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `release/snapshots.test.sh`:

```bash
#!/usr/bin/env bash
# Tests for release/snapshots.sh. Run: bash release/snapshots.test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/snapshots.sh"
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

export SCHEMAS_DIR="$work/schemas"
mkdir -p "$SCHEMAS_DIR/core.host.list" "$SCHEMAS_DIR/hostinfo.summary" "$SCHEMAS_DIR/released/v0.1.0/core.host.list"
echo '{"input":1}' >"$SCHEMAS_DIR/core.host.list/v1.json"
echo '{"input":2}' >"$SCHEMAS_DIR/hostinfo.summary/v1.json"
# Files of the Go package schemas that are not snapshots and are never frozen.
mkdir -p "$SCHEMAS_DIR/testdata"
echo 'package schemas' >"$SCHEMAS_DIR/embed.go"
echo 'package schemas' >"$SCHEMAS_DIR/x_test.go"
echo '{"fixture":true}' >"$SCHEMAS_DIR/testdata/a.json"
echo 'Frozen snapshots of released tags.' >"$SCHEMAS_DIR/released/README.md"
echo '{"input":0}' >"$SCHEMAS_DIR/released/v0.1.0/core.host.list/v1.json"

expect usage 2 bash "$script" freeze v1.0.0
expect usage_count 2 bash "$script" copy
expect copy 0 bash "$script" copy v1.0.0
listing=$({ cd "$SCHEMAS_DIR/released/v1.0.0" && find . -type f; } 2>/dev/null | LC_ALL=C sort | tr '\n' ' ') || true
expect copy_file_set 0 test "$listing" = "./core.host.list/v1.json ./hostinfo.summary/v1.json "
expect copy_content 0 cmp "$SCHEMAS_DIR/core.host.list/v1.json" "$SCHEMAS_DIR/released/v1.0.0/core.host.list/v1.json"
expect copy_keeps_older_release 0 test "$(cat "$SCHEMAS_DIR/released/v0.1.0/core.host.list/v1.json")" = '{"input":0}'
expect copy_refuses_existing 1 bash "$script" copy v1.0.0
expect check_equal 0 bash "$script" check v1.0.0
expect check_missing_tag 1 bash "$script" check v9.9.9

echo '{"input":3}' >"$SCHEMAS_DIR/hostinfo.summary/v1.json"
expect check_changed 1 bash "$script" check v1.0.0
echo '{"input":2}' >"$SCHEMAS_DIR/hostinfo.summary/v1.json"

mkdir -p "$SCHEMAS_DIR/core.user.list"
echo '{}' >"$SCHEMAS_DIR/core.user.list/v1.json"
expect check_extra_current 1 bash "$script" check v1.0.0
rm -r "$SCHEMAS_DIR/core.user.list"

mkdir -p "$SCHEMAS_DIR/released/v1.0.0"
echo 'stray' >"$SCHEMAS_DIR/released/v1.0.0/notes.txt"
expect check_extra_released 1 bash "$script" check v1.0.0
rm -f "$SCHEMAS_DIR/released/v1.0.0/notes.txt"

rm -f "$SCHEMAS_DIR/released/v1.0.0/hostinfo.summary/v1.json"
expect check_missing_released 1 bash "$script" check v1.0.0

echo "snapshots tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash release/snapshots.test.sh`
Expected: FAIL lines (the script does not exist, so `bash` exits 127) and a final line `snapshots tests: N passed, M failed` with M greater than 0, exit 1.

- [ ] **Step 3: Write the implementation**

Create `release/snapshots.sh`:

```bash
#!/usr/bin/env bash
# Freezes and checks the released schema snapshots (ADR 0002).
#
#   snapshots.sh copy <tag>   copies the current snapshots to schemas/released/<tag>/
#   snapshots.sh check <tag>  fails unless schemas/released/<tag>/ holds exactly them
#
# A snapshot is schemas/<action>/v<n>.json. Nothing else under schemas/ (the
# Go package's embed.go, its tests and testdata, released/) is frozen, so
# released trees hold only snapshots. Released snapshots are immutable:
# copy refuses an existing tag directory. SCHEMAS_DIR overrides the schemas
# directory.
set -euo pipefail

if [ "$#" -ne 2 ] || { [ "$1" != copy ] && [ "$1" != check ]; }; then
  echo "usage: snapshots.sh copy|check <tag>" >&2
  exit 2
fi
mode=$1
tag=$2
root=${SCHEMAS_DIR:-$(cd "$(dirname "$0")/.." && pwd)/schemas}
dest="$root/released/$tag"

# snapshots: the current snapshot paths relative to $root, in C order.
snapshots() {
  (cd "$root" && find . -mindepth 2 -maxdepth 2 -type f -name 'v*.json' ! -path './released/*' -printf '%P\n' | LC_ALL=C sort)
}

case $mode in
copy)
  if [ -e "$dest" ]; then
    echo "snapshots: $dest exists; released snapshots are immutable" >&2
    exit 1
  fi
  mkdir -p "$dest"
  snapshots | while IFS= read -r f; do
    mkdir -p "$dest/$(dirname "$f")"
    cp "$root/$f" "$dest/$f"
  done
  ;;
check)
  if [ ! -d "$dest" ]; then
    echo "snapshots: $dest is missing; run make release-prep VERSION=${tag#v} and commit it" >&2
    exit 1
  fi
  if ! diff <(snapshots) <(cd "$dest" && find . -type f -printf '%P\n' | LC_ALL=C sort) >&2; then
    echo "snapshots: $dest does not hold exactly the current snapshots" >&2
    exit 1
  fi
  while IFS= read -r f; do
    if ! cmp -s "$root/$f" "$dest/$f"; then
      echo "snapshots: $dest/$f differs from the current snapshot" >&2
      exit 1
    fi
  done < <(snapshots)
  ;;
esac
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash release/snapshots.test.sh && shellcheck release/snapshots.sh release/snapshots.test.sh`
Expected: `snapshots tests: 13 passed, 0 failed` and no shellcheck output.

- [ ] **Step 5: Commit**

```bash
git add release/snapshots.sh release/snapshots.test.sh
git commit -m "feat(release): freeze and check schema snapshots"
```

### Task 3: Release packaging script

**Files:**
- Create: `release/package.sh`
- Test: `release/package.test.sh`

**Interfaces:**
- Consumes: nothing.
- Produces: `release/package.sh <version> <bin dir> <out dir>`, the tarball layout and `checksums.txt` format (Contracts, Release files and Scripts).

**Items:** 16

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `release/package.test.sh`:

```bash
#!/usr/bin/env bash
# Tests for release/package.sh. Run: bash release/package.test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/package.sh"
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

verify_checksums() { (cd "$1" && sha256sum --check --strict --quiet checksums.txt); }

export SOURCE_DATE_EPOCH=1700000000
mkdir -p "$work/bin" "$work/out1" "$work/out2" "$work/x"
printf 'amd64 binary' >"$work/bin/darkpanel-linux-amd64"
printf 'arm64 binary' >"$work/bin/darkpanel-linux-arm64"
for out in out1 out2; do
  printf '{"bomFormat":"CycloneDX"}' >"$work/$out/darkpanel_1.2.3.cdx.json"
done
amd64="$work/out1/darkpanel_1.2.3_linux_amd64.tar.gz"
names="darkpanel_1.2.3.cdx.json darkpanel_1.2.3_linux_amd64.tar.gz darkpanel_1.2.3_linux_arm64.tar.gz "

expect usage 2 bash "$script" 1.2.3 "$work/bin"
expect package 0 bash "$script" 1.2.3 "$work/bin" "$work/out1"
expect entries 0 test "$(tar -tzf "$amd64" 2>/dev/null | tr '\n' ' ')" = "darkpanel LICENSE README.md "
tar -xzf "$amd64" -C "$work/x" 2>/dev/null || true
expect binary_content 0 test "$(cat "$work/x/darkpanel" 2>/dev/null)" = "amd64 binary"
expect binary_mode 0 test "$(stat -c %a "$work/x/darkpanel" 2>/dev/null)" = 755
expect owner_root 0 test "$(tar -tvzf "$amd64" 2>/dev/null | awk '{print $2}' | sort -u)" = "0/0"
expect arm64_tarball 0 test -f "$work/out1/darkpanel_1.2.3_linux_arm64.tar.gz"
expect checksums_names 0 test "$(awk '{print $2}' "$work/out1/checksums.txt" 2>/dev/null | tr '\n' ' ')" = "$names"
expect checksums_verify 0 verify_checksums "$work/out1"
expect rerun 0 bash "$script" 1.2.3 "$work/bin" "$work/out1"
expect rerun_excludes_checksums 0 test "$(awk '{print $2}' "$work/out1/checksums.txt" 2>/dev/null | tr '\n' ' ')" = "$names"
expect package_again 0 bash "$script" 1.2.3 "$work/bin" "$work/out2"
expect reproducible 0 cmp "$amd64" "$work/out2/darkpanel_1.2.3_linux_amd64.tar.gz"
expect missing_binary 1 bash "$script" 1.2.3 "$work/out2" "$work/out2"

echo "package tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash release/package.test.sh`
Expected: FAIL lines (the script does not exist) and a final line `package tests: N passed, M failed` with M greater than 0, exit 1.

- [ ] **Step 3: Write the implementation**

Create `release/package.sh`:

```bash
#!/usr/bin/env bash
# Packages the release tarballs and writes checksums.txt over every file in
# the output directory (spec section 9).
#
#   package.sh <version> <bin dir> <out dir>
#
# <bin dir> holds darkpanel-linux-amd64 and darkpanel-linux-arm64 (make
# build). The tarballs are reproducible: entries in a fixed order, owner 0/0,
# fixed modes, the commit time (or SOURCE_DATE_EPOCH) as mtime, and gzip
# without a name or timestamp, so a rebuild of the tag gives the same bytes.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: package.sh <version> <bin dir> <out dir>" >&2
  exit 2
fi
version=$1
bindir=$2
outdir=$3
root=$(cd "$(dirname "$0")/.." && pwd)
mtime=${SOURCE_DATE_EPOCH:-$(git -C "$root" log -1 --format=%ct)}

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
mkdir -p "$outdir"

for arch in amd64 arm64; do
  dir="$stage/$arch"
  mkdir -p "$dir"
  install -m 0755 "$bindir/darkpanel-linux-$arch" "$dir/darkpanel"
  install -m 0644 "$root/LICENSE" "$root/README.md" "$dir/"
  tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$mtime" \
    -C "$dir" -cf - darkpanel LICENSE README.md |
    gzip -n -9 >"$outdir/darkpanel_${version}_linux_${arch}.tar.gz"
done

cd "$outdir"
files=()
while IFS= read -r f; do
  files+=("$f")
done < <(find . -maxdepth 1 -type f ! -name checksums.txt ! -name '*.minisig' -printf '%f\n' | LC_ALL=C sort)
sha256sum -- "${files[@]}" >checksums.txt
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash release/package.test.sh && shellcheck release/package.sh release/package.test.sh`
Expected: `package tests: 14 passed, 0 failed` and no shellcheck output.

- [ ] **Step 5: Commit**

```bash
git add release/package.sh release/package.test.sh
git commit -m "feat(release): package tarballs and checksums"
```

### Task 4: Signing script

**Files:**
- Create: `release/sign.sh`
- Test: `release/sign.test.sh`

**Interfaces:**
- Consumes: `release/releasetool` `check-version`, `keyid` and `check-build` (Contracts, Task 1).
- Produces: `release/sign.sh <version> <stable|beta> <release dir> <version.json>` and the signature file names (Contracts).

**Items:** 16

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

- [ ] **Step 1: Write the failing test**

Create `release/sign.test.sh`:

```bash
#!/usr/bin/env bash
# Tests for release/sign.sh against a stub minisign. Run: bash release/sign.test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/sign.sh"
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

# The stub writes a hashed-mode signature file whose key id is the KEYID=
# line of the key file, stored little-endian as minisign does, and logs the
# signed file, the trusted comment and the password line it read. A key file
# with a FAIL line makes it fail.
cat >"$work/minisign" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
while [ "$#" -gt 0 ]; do
  case $1 in
  -s) key=$2; shift 2 ;;
  -m) msg=$2; shift 2 ;;
  -t) comment=$2; shift 2 ;;
  -x) out=$2; shift 2 ;;
  *) shift ;;
  esac
done
IFS= read -r password
if grep -qx FAIL "$key"; then
  echo "stub: refusing key" >&2
  exit 1
fi
id=$(sed -n 's/^KEYID=//p' "$key")
le=$(printf '%s' "$id" | sed 's/../&\n/g' | tac | tr -d '\n' | sed 's/../\\x&/g')
{
  echo "untrusted comment: stub signature"
  { printf 'ED'; printf '%b' "$le"; head -c 64 /dev/zero; } | base64 -w0
  echo
  echo "trusted comment: $comment"
  head -c 64 /dev/zero | base64 -w0
  echo
} >"$out"
echo "$msg|$comment|$password" >>"$STUB_LOG"
STUB
chmod +x "$work/minisign"

export MINISIGN="$work/minisign" STUB_LOG="$work/log"
unset MINISIGN_KEY_1 MINISIGN_PASSWORD_1 MINISIGN_KEY_2 MINISIGN_PASSWORD_2
key1="KEYID=0123456789ABCDEF"
key2="KEYID=00000000000000FF"

# trusts ID...: writes a version.json for a production build trusting ID...
trusts() {
  local ids
  ids=$(printf '"%s",' "$@")
  printf '{"version":"1.2.3","testBuild":false,"trustedKeys":[%s]}\n' "${ids%,}" >"$work/version.json"
}

# fresh: an empty release directory holding only checksums.txt, and no log.
fresh() {
  rm -rf "$work/rel" "$work/log"
  mkdir -p "$work/rel"
  echo "0000  darkpanel_1.2.3_linux_amd64.tar.gz" >"$work/rel/checksums.txt"
}

sigs() { (cd "$work/rel" && find . -name '*.minisig' | LC_ALL=C sort | tr '\n' ' '); }

fresh
trusts 0123456789ABCDEF
expect one_key 0 env MINISIGN_KEY_1="$key1" MINISIGN_PASSWORD_1=pw1 bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect one_key_file 0 test "$(sigs)" = "./checksums.txt.0123456789ABCDEF.minisig "
expect one_key_log 0 test "$(cat "$work/log" 2>/dev/null)" = "$work/rel/checksums.txt|version=1.2.3 channel=stable purpose=checksums|pw1"

fresh
trusts 0123456789ABCDEF 00000000000000FF
expect two_keys 0 env MINISIGN_KEY_1="$key1" MINISIGN_PASSWORD_1=pw1 MINISIGN_KEY_2="$key2" MINISIGN_PASSWORD_2=pw2 \
  bash "$script" 1.3.0-beta.1 beta "$work/rel" "$work/version.json"
expect two_keys_files 0 test "$(sigs)" = "./checksums.txt.00000000000000FF.minisig ./checksums.txt.0123456789ABCDEF.minisig "
expect two_keys_log 0 grep -qx ".*|version=1.3.0-beta.1 channel=beta purpose=checksums|pw2" "$work/log"

fresh
trusts 0123456789ABCDEF
expect no_key 1 bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect no_key_no_files 0 test -z "$(sigs)"

fresh
trusts 0123456789ABCDEF
expect trust_mismatch 1 env MINISIGN_KEY_1="$key1" MINISIGN_KEY_2="$key2" bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect trust_mismatch_no_files 0 test -z "$(sigs)"

fresh
trusts 0123456789ABCDEF 00000000000000FF
expect second_key_fails 1 env MINISIGN_KEY_1="$key1" MINISIGN_KEY_2="$key2"$'\nFAIL' bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect second_key_fails_no_files 0 test -z "$(sigs)"

fresh
printf '{"testBuild":true,"trustedKeys":["0123456789ABCDEF"]}\n' >"$work/version.json"
expect test_build 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect test_build_no_files 0 test -z "$(sigs)"

fresh
trusts 0123456789ABCDEF
expect usage 2 bash "$script" 1.2.3 stable "$work/rel"
expect bad_version 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2 stable "$work/rel" "$work/version.json"
expect bad_channel 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2.3 nightly "$work/rel" "$work/version.json"
expect bad_args_no_files 0 test -z "$(sigs)"

echo "sign tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bash release/sign.test.sh`
Expected: FAIL lines (the script does not exist) and a final line `sign tests: N passed, M failed` with M greater than 0, exit 1.

- [ ] **Step 3: Write the implementation**

Create `release/sign.sh`:

```bash
#!/usr/bin/env bash
# Signs checksums.txt with every minisign key in the environment, then
# refuses the release unless the release binary is not a test build and
# trusts exactly those keys (spec section 9, ADR 0007). The sign job of
# release.yml runs it in the owner-approved release environment.
#
#   sign.sh <version> <stable|beta> <release dir> <version.json>
#
# Keys: MINISIGN_KEY_1 with MINISIGN_PASSWORD_1, and MINISIGN_KEY_2 with
# MINISIGN_PASSWORD_2 during a key rotation; each key is the text of a
# minisign secret key file. Signatures are written as
# checksums.txt.<key id>.minisig. MINISIGN names the minisign command.
set -euo pipefail
umask 077

if [ "$#" -ne 4 ]; then
  echo "usage: sign.sh <version> <stable|beta> <release dir> <version.json>" >&2
  exit 2
fi
version=$1
channel=$2
dir=$3
info=$4
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

(cd "$root" && go build -o "$work/releasetool" ./release/releasetool)
"$work/releasetool" check-version "$version"
case $channel in
stable | beta) ;;
*)
  echo "sign: channel $channel is not stable or beta" >&2
  exit 1
  ;;
esac

comment="version=$version channel=$channel purpose=checksums"
# Signatures stay in $work until every key has signed and the build check
# passed, so a failure never leaves a partial set in the release directory.
mkdir "$work/sigs"
ids=()
for slot in 1 2; do
  key_var=MINISIGN_KEY_$slot
  password_var=MINISIGN_PASSWORD_$slot
  [ -n "${!key_var:-}" ] || continue
  printf '%s\n' "${!key_var}" >"$work/key"
  # minisign reads the password as one line when standard input is not a terminal.
  printf '%s\n' "${!password_var:-}" |
    "${MINISIGN:-minisign}" -S -s "$work/key" -m "$dir/checksums.txt" -t "$comment" -x "$work/sig" >/dev/null
  rm -f "$work/key"
  id=$("$work/releasetool" keyid "$work/sig")
  mv "$work/sig" "$work/sigs/checksums.txt.$id.minisig"
  ids+=("$id")
done

if [ "${#ids[@]}" -eq 0 ]; then
  echo "sign: no signing key in the environment; the owner sets MINISIGN_KEY_1 in the release environment" >&2
  exit 1
fi
"$work/releasetool" check-build "$(IFS=,; echo "${ids[*]}")" <"$info"
mv "$work"/sigs/*.minisig "$dir/"
echo "sign: checksums.txt signed by ${ids[*]}"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bash release/sign.test.sh && shellcheck release/sign.sh release/sign.test.sh`
Expected: `sign tests: 18 passed, 0 failed` and no shellcheck output.

- [ ] **Step 5: Commit**

```bash
git add release/sign.sh release/sign.test.sh
git commit -m "feat(release): sign checksums with every release key"
```

### Task 5: Versioning policy

**Files:**
- Create: `docs/versioning.md`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing another task reads.

**Items:** 16

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 0 = 0

- [ ] **Step 1: Write the check**

There is no code to test. The check is that the file exists and states each rule the spec requires (section 9 Policy):

Run: `for s in 'Semantic Versioning' 'additive' 'previous minor' 'stable' 'beta' 'one minor' 'backup' 'never restored automatically' 'two minor releases'; do grep -qF "$s" docs/versioning.md || echo "missing: $s"; done`
Expected now: `grep` reports the file is missing and every `missing:` line prints.

- [ ] **Step 2: Write the document**

Create `docs/versioning.md`:

```markdown
# Versioning and compatibility

Darkpanel follows [Semantic Versioning 2.0.0](https://semver.org/). A release
is tagged `v<version>`, for example `v1.3.0` or `v1.3.0-beta.1`.

- **Major** releases may remove an action version or change behaviour
  incompatibly.
- **Minor** releases add features, actions and action versions, and may add
  database migrations.
- **Patch** releases fix bugs.

## Channels

A release with a prerelease part (`1.3.0-beta.1`) is published on the `beta`
channel; every other release is on `stable`. Each host follows one channel,
set in its update policy:

- `stable` hosts take only stable releases.
- `beta` hosts take every release, ordered by semantic-version precedence
  (`1.3.0-beta.1` comes before `1.3.0`), so a beta host moves on to the
  stable release when it ships. A beta host is never moved back to a lower
  stable release automatically.

## Releases and signatures

Each release publishes, under
`https://github.com/Darkraise/darkpanel/releases/download/v<version>/`:

- `darkpanel_<version>_linux_amd64.tar.gz` and
  `darkpanel_<version>_linux_arm64.tar.gz` (the binary, `LICENSE`,
  `README.md`);
- `checksums.txt`, the SHA-256 of every file above and of the SBOM;
- `checksums.txt.<key id>.minisig`, a minisign signature of `checksums.txt`
  for each release key. Its trusted comment names the version, the channel
  and the file's purpose, so an older signed file cannot be replayed as a
  newer release;
- `darkpanel_<version>.cdx.json`, a CycloneDX software bill of materials;
- `install.sh`.

`install.sh` and the updater accept a release when any one signature from a
key built into them verifies. The list of releases,
`https://github.com/Darkraise/darkpanel/releases/download/index/releases.json`,
only names versions and channels; trust always comes from the signatures.

When the release key changes, releases are signed with both the old and the
new key for at least two minor releases. A host that skipped that whole
window updates through the last dual-signed release first.

## Action compatibility

Every action has a name and a version, such as `core.host.list/v1`. Once
released, an action version changes only in additive ways:

- allowed: new optional input fields, new output fields, new output enum
  values (clients accept values they do not know);
- not allowed: removing or renaming a field, changing a type, a default or a
  constraint, adding a required input field, removing an enum value, or
  changing what the action does or its class.

A breaking change ships as a new action version; the old version stays until
the next major release. Each release freezes its action schemas under
`schemas/released/v<version>/`, and the build checks the current schemas
against every released one.

## Controller and agent versions

The controller supports agents of its own minor version and of the previous
minor version, with the same major version and agent protocol 1. An agent
outside that window is shown as unsupported and accepts only update actions
until it is updated. The controller is updated first, on its own host, and
then updates agents up to its own version, one host at a time, stopping at
the first failure.

## Updates

- An update moves at most one minor version at a time. From 1.2 to 1.5 a
  host installs the newest patch of 1.3, then of 1.4, then 1.5, and each
  step must pass its health check before the next starts. A failure leaves
  the host on the last minor version that passed.
- A downgrade is refused unless forced, and a forced downgrade goes back at
  most one minor version.
- An update is refused while a change on the host still waits for
  confirmation.
- A new binary that fails its health check, or does not pass it within the
  deadline, is rolled back automatically to the previous binary within two
  minutes.

## Databases

Each service migrates its own database when it starts. Before applying a
migration it takes a backup of the database in its own directory
(`backups/<from>-<to>-<timestamp>/`, the last five kept).

Migrations follow expand, then contract: a release's migrations leave the
database usable by the previous minor version's binary, and destructive
changes ship only in a later release, once nothing reads the old shape. A
rollback therefore runs the previous binary on the migrated database without
restoring data. The backup is for disaster recovery only and is never
restored automatically.
```

- [ ] **Step 3: Run the check**

Run: `for s in 'Semantic Versioning' 'additive' 'previous minor' 'stable' 'beta' 'one minor' 'backup' 'never restored automatically' 'two minor releases'; do grep -qF "$s" docs/versioning.md || echo "missing: $s"; done`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add docs/versioning.md
git commit -m "docs(versioning): add the versioning policy"
```

### Task 6: Makefile targets, cliff.toml and CI job

**Files:**
- Modify: `Makefile` (variables at lines 5-6 and after line 10, `.PHONY` at line 17, `lint` at line 47, `test` at line 59, new targets)
- Create: `cliff.toml`
- Modify: `.github/workflows/ci.yml` (a new job after `adr`)

**Interfaces:**
- Consumes: `release/releasetool check-version` (Task 1), `release/snapshots.sh` (Task 2), `release/package.sh` (Task 3), the three test scripts (Tasks 2-4), and plan 1's generator `scripts/gen-install-sh` (Contracts, Makefile targets).
- Produces: the Makefile targets `release`, `release-prep`, `install-sh`, `lint-sh`, `test-release` and the `VERSION`/`COMMIT` fallback (Contracts).

**Items:** 16

**Depends on:** Task 1, Task 2, Task 3, Task 4

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing checks**

Install shellcheck if needed (`command -v shellcheck || apt-get install -y shellcheck`), then run each check and note that it fails:

```bash
d=$(mktemp -d)
make -f "$PWD/Makefile" -C "$d" --no-print-directory --eval 'print-version: ; @echo $(VERSION) $(COMMIT)' print-version
rmdir "$d"
make -n release VERSION=1.2.3 | grep -F 'release/package.sh 1.2.3 bin bin/release'
make lint-sh test-release
```

Expected now: the first prints ` ` (both empty, plus a `git` error on standard error), the second finds nothing (`No rule to make target 'release'`), the third fails with `No rule to make target 'lint-sh'`.

- [ ] **Step 2: Edit the Makefile**

Replace lines 5-6:

```make
VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse --short HEAD)
```

with:

```make
# Outside a git checkout (a source tarball) git fails and both fall back.
VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null),dev)
COMMIT ?= $(or $(shell git rev-parse --short HEAD 2>/dev/null),unknown)
```

After the line `GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0`, add:

```make
RELEASE_DIR := bin/release
CYCLONEDX ?= cyclonedx-gomod
GIT_CLIFF ?= git-cliff
# install-sh embeds the production trusted keys; KEYS=harness embeds the
# harness test key and base URL instead.
KEYS ?= production
```

Replace the `.PHONY` line with:

```make
# release is phony on purpose: the release/ directory would otherwise satisfy it.
.PHONY: web-deps web build verify-build lint-go lint-web lint-sh lint test-go test-web test-adr test-release test schemas vuln check clean release-check release release-prep install-sh
```

Replace `lint: lint-go lint-web` with:

```make
lint-sh:
	shellcheck release/*.sh $(wildcard install.sh)

lint: lint-go lint-web lint-sh
```

Replace `test: test-go test-web test-adr` with:

```make
test-release:
	bash release/snapshots.test.sh
	bash release/package.test.sh
	bash release/sign.test.sh

test: test-go test-web test-adr test-release
```

At the end of the file, after the `clean` target, add:

```make
release-check:
	go run ./release/releasetool check-version $(VERSION)

# make release VERSION=x.y.z writes the tarballs, the SBOM and checksums.txt
# to bin/release. One SBOM serves both architectures: the module graph is the
# same for every GOARCH of this pure-Go build.
release: release-check verify-build
	rm -rf $(RELEASE_DIR)
	mkdir -p $(RELEASE_DIR)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(CYCLONEDX) app -json -licenses -main cmd/darkpanel \
		-output $(RELEASE_DIR)/darkpanel_$(VERSION).cdx.json .
	bash release/package.sh $(VERSION) bin $(RELEASE_DIR)

# make release-prep VERSION=x.y.z freezes the schema snapshots and rewrites
# CHANGELOG.md; the owner commits both, then pushes a signed tag vx.y.z.
release-prep: release-check
	bash release/snapshots.sh copy v$(VERSION)
	$(GIT_CLIFF) --tag v$(VERSION) -o CHANGELOG.md

install-sh:
	go run $(if $(filter harness,$(KEYS)),-tags harnesskeys,) ./scripts/gen-install-sh -out install.sh
```

- [ ] **Step 3: Create cliff.toml**

```toml
# git-cliff configuration: release notes and CHANGELOG.md from conventional
# commits (<type>(<scope>): <subject>). Test, style, chore, CI and build
# commits are left out; merge commits are not conventional and are skipped.

[changelog]
header = """
# Changelog

All notable changes to Darkpanel. Versions follow docs/versioning.md.

"""
body = """
{% if version %}\
## {{ version | trim_start_matches(pat="v") }} - {{ timestamp | date(format="%Y-%m-%d") }}
{% else %}\
## Unreleased
{% endif %}\
{% for group, commits in commits | group_by(attribute="group") %}
### {{ group | striptags | trim }}
{% for commit in commits %}
- {% if commit.scope %}**{{ commit.scope }}:** {% endif %}\
{% if commit.breaking %}[breaking] {% endif %}\
{{ commit.message | split(pat="\n") | first | trim }}\
{% endfor %}
{% endfor %}
"""
trim = true

[git]
conventional_commits = true
filter_unconventional = true
split_commits = false
protect_breaking_commits = true
filter_commits = true
tag_pattern = "^v[0-9]+\\.[0-9]+\\.[0-9]+(-[0-9A-Za-z.-]+)?$"
topo_order = false
sort_commits = "oldest"
commit_parsers = [
    { message = "^feat", group = "<!-- 0 -->Features" },
    { message = "^fix", group = "<!-- 1 -->Fixes" },
    { message = "^perf", group = "<!-- 2 -->Performance" },
    { message = "^refactor", group = "<!-- 3 -->Refactoring" },
    { message = "^docs", group = "<!-- 4 -->Documentation" },
    { message = "^(test|style|chore|ci|build)", skip = true },
]
```

- [ ] **Step 4: Add the CI job**

In `.github/workflows/ci.yml`, insert after the `adr` job (after its `- run: make test-adr` line and before `  go:`):

```yaml
  release-scripts:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      # lint-sh checks install.sh only once it exists.
      - run: make lint-sh test-release
```

- [ ] **Step 5: Run the checks**

```bash
d=$(mktemp -d)
make -f "$PWD/Makefile" -C "$d" --no-print-directory --eval 'print-version: ; @echo $(VERSION) $(COMMIT)' print-version
rmdir "$d"
```
Expected: `dev unknown`.

```bash
make --no-print-directory --eval 'print-version: ; @echo $(VERSION) $(COMMIT)' print-version
```
Expected: the `git describe` output and the short commit, as before the change.

```bash
make -n release VERSION=1.2.3 | grep -E 'check-version 1.2.3|cyclonedx-gomod app -json -licenses -main cmd/darkpanel|release/package.sh 1.2.3 bin bin/release'
make -n release-prep VERSION=1.2.3 | grep -E 'snapshots.sh copy v1.2.3|git-cliff --tag v1.2.3 -o CHANGELOG.md'
make -n install-sh | grep -F 'go run  ./scripts/gen-install-sh -out install.sh'
make -n install-sh KEYS=harness | grep -F 'go run -tags harnesskeys ./scripts/gen-install-sh -out install.sh'
make release-check VERSION=v1.2.3; echo "exit $?"
```
Expected: three lines, two lines, one line, one line, then `releasetool: "v1.2.3" is not a semantic version …` and a non-zero exit.

```bash
make lint-sh test-release
```
Expected: no shellcheck output, then `snapshots tests: 13 passed, 0 failed`, `package tests: 14 passed, 0 failed`, `sign tests: 18 passed, 0 failed`.

End-to-end release build with the pinned tools (takes a few minutes; it runs `verify-build`):

```bash
t=$(mktemp -d)
curl -fsSL -o "$t/cdx.tgz" https://github.com/CycloneDX/cyclonedx-gomod/releases/download/v1.12.0/cyclonedx-gomod_1.12.0_linux_amd64.tar.gz
curl -fsSL -o "$t/cliff.tgz" https://github.com/orhun/git-cliff/releases/download/v2.14.2/git-cliff-2.14.2-x86_64-unknown-linux-gnu.tar.gz
printf '%s  %s\n' 004b9f5cc595b797fb5423e2ae4c97bcf0f18c712ed2faee1640b09e5efd6d15 "$t/cdx.tgz" 24f397c733add5390fdceee3a2088588ab0d5f944ce00d34cb7029b888cf2db4 "$t/cliff.tgz" | sha256sum -c
tar -xzf "$t/cdx.tgz" -C "$t" cyclonedx-gomod
tar -xzf "$t/cliff.tgz" -C "$t" --strip-components=1 git-cliff-2.14.2/git-cliff
timeout 900 make release VERSION=0.0.0-test.1 CYCLONEDX="$t/cyclonedx-gomod"
ls bin/release
(cd bin/release && sha256sum --check --strict checksums.txt)
"$t/git-cliff" --unreleased --tag v0.0.1 | head -8
rm -rf "$t" bin/release
```
Expected: `sha256sum -c` prints two `OK` lines; `make release` ends without error; `ls` lists `checksums.txt`, `darkpanel_0.0.0-test.1.cdx.json`, `darkpanel_0.0.0-test.1_linux_amd64.tar.gz`, `darkpanel_0.0.0-test.1_linux_arm64.tar.gz`; the checksum check prints three `OK` lines; git-cliff prints `# Changelog`, a blank line, the policy sentence, a blank line, `## 0.0.1 - <today>` and a `### Features` group.

```bash
GOTOOLCHAIN=local go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml
```
Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add Makefile cliff.toml .github/workflows/ci.yml
git commit -m "chore(release): add release targets and CI checks"
```

### Task 7: Release workflow

**Files:**
- Create: `.github/workflows/release.yml`
- Create: `release/allowed_signers`
- Create: `release/README.md`

**Interfaces:**
- Consumes: `release/releasetool` `check-tag` and `index` (Task 1), `release/snapshots.sh check` (Task 2), the release files `release/package.sh` writes (Task 3), `release/sign.sh` (Task 4), `make release` and `make install-sh` (Task 6), the pins and secret names (Contracts).
- Produces: the published release files and `releases.json` (Contracts, Release files).

**Items:** 16

**Depends on:** Task 1, Task 2, Task 3, Task 4, Task 6

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing checks**

```bash
test -f .github/workflows/release.yml && GOTOOLCHAIN=local go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/release.yml
```
Expected now: exit 1 (the file does not exist).

- [ ] **Step 2: Create release/allowed_signers**

```text
# SSH keys allowed to sign release tags, in ssh-keygen ALLOWED SIGNERS
# format. The verify job of release.yml reads this file from main and runs
# git verify-tag against it. The owner adds one line per tag-signing key:
#
#   <email> namespaces="git" ssh-ed25519 AAAA...
#
# While no key line exists, every release stops at the verify job.
```

- [ ] **Step 3: Create .github/workflows/release.yml**

```yaml
name: Release

on:
  push:
    tags: ["v*"]

permissions:
  contents: read

# One publication at a time, so a regenerated releases.json never misses a
# release published beside it.
concurrency:
  group: release
  cancel-in-progress: false

env:
  GOTOOLCHAIN: local

jobs:
  verify:
    runs-on: ubuntu-24.04
    outputs:
      version: ${{ steps.tag.outputs.version }}
      channel: ${{ steps.tag.outputs.channel }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - id: tag
        run: go run ./release/releasetool check-tag "$GITHUB_REF_NAME" >>"$GITHUB_OUTPUT"
      - name: Verify the tag signature
        run: |
          # Fetch the annotated tag object itself; a checkout may hold only the commit.
          git fetch --force --no-tags origin "refs/tags/$GITHUB_REF_NAME:refs/tags/$GITHUB_REF_NAME"
          test "$(git cat-file -t "$GITHUB_REF_NAME")" = tag
          # Signers come from main, never from the tagged tree, which a forged tag could carry.
          git show origin/main:release/allowed_signers >"$RUNNER_TEMP/allowed_signers"
          git -c gpg.ssh.allowedSignersFile="$RUNNER_TEMP/allowed_signers" verify-tag "$GITHUB_REF_NAME"
      - run: bash release/snapshots.sh check "$GITHUB_REF_NAME"

  build:
    needs: verify
    runs-on: ubuntu-24.04
    permissions:
      contents: read
      id-token: write
      attestations: write
    env:
      VERSION: ${{ needs.verify.outputs.version }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - uses: pnpm/action-setup@ea17c68df8912ef543352723c149a84f56e3d413 # v6.1.0
        with:
          package_json_file: web/package.json
      - uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0
        with:
          node-version: 24
          cache: pnpm
          cache-dependency-path: web/pnpm-lock.yaml
      - name: Install the release tools
        run: |
          tools="$RUNNER_TEMP/tools"
          mkdir -p "$tools"
          curl -fsSL -o "$tools/cliff.tgz" https://github.com/orhun/git-cliff/releases/download/v2.14.2/git-cliff-2.14.2-x86_64-unknown-linux-gnu.tar.gz
          curl -fsSL -o "$tools/cdx.tgz" https://github.com/CycloneDX/cyclonedx-gomod/releases/download/v1.12.0/cyclonedx-gomod_1.12.0_linux_amd64.tar.gz
          printf '%s  %s\n' \
            24f397c733add5390fdceee3a2088588ab0d5f944ce00d34cb7029b888cf2db4 "$tools/cliff.tgz" \
            004b9f5cc595b797fb5423e2ae4c97bcf0f18c712ed2faee1640b09e5efd6d15 "$tools/cdx.tgz" | sha256sum --check --strict
          tar -xzf "$tools/cliff.tgz" -C "$tools" --strip-components=1 git-cliff-2.14.2/git-cliff
          tar -xzf "$tools/cdx.tgz" -C "$tools" cyclonedx-gomod
          echo "$tools" >>"$GITHUB_PATH"
      - run: make release VERSION="$VERSION"
      - name: Check install.sh is committed and current
        run: |
          git ls-files --error-unmatch install.sh
          make install-sh
          git diff --exit-code -- install.sh
          cp install.sh bin/release/
      - run: git-cliff --current --strip all -o bin/release-notes.md
      - uses: actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2
        with:
          subject-path: bin/release/*
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: release-files
          path: |
            bin/release/
            bin/release-notes.md
          if-no-files-found: error

  sign:
    needs: [verify, build]
    runs-on: ubuntu-24.04
    environment: release
    permissions:
      contents: read
      attestations: read
    env:
      VERSION: ${{ needs.verify.outputs.version }}
      CHANNEL: ${{ needs.verify.outputs.channel }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: release-files
          path: dist
      - name: Verify the build attestations
        env:
          GH_TOKEN: ${{ github.token }}
        run: |
          for f in dist/release/*; do
            gh attestation verify "$f" --repo "$GITHUB_REPOSITORY" \
              --signer-workflow "$GITHUB_REPOSITORY/.github/workflows/release.yml" \
              --source-ref "$GITHUB_REF" --source-digest "$GITHUB_SHA" \
              --deny-self-hosted-runners
          done
      - name: Read the release binary's build information
        run: |
          tar -xzf "dist/release/darkpanel_${VERSION}_linux_amd64.tar.gz" -C "$RUNNER_TEMP" darkpanel
          "$RUNNER_TEMP/darkpanel" version --json >"$RUNNER_TEMP/version.json"
      - run: go install aead.dev/minisign/cmd/minisign@v0.3.0
      - name: Sign checksums.txt
        env:
          MINISIGN_KEY_1: ${{ secrets.MINISIGN_KEY_1 }}
          MINISIGN_PASSWORD_1: ${{ secrets.MINISIGN_PASSWORD_1 }}
          MINISIGN_KEY_2: ${{ secrets.MINISIGN_KEY_2 }}
          MINISIGN_PASSWORD_2: ${{ secrets.MINISIGN_PASSWORD_2 }}
        run: |
          MINISIGN="$(go env GOPATH)/bin/minisign" \
            bash release/sign.sh "$VERSION" "$CHANNEL" dist/release "$RUNNER_TEMP/version.json"
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: release-signatures
          path: dist/release/checksums.txt.*.minisig
          if-no-files-found: error

  publish:
    needs: [verify, sign]
    runs-on: ubuntu-24.04
    permissions:
      contents: write
    env:
      VERSION: ${{ needs.verify.outputs.version }}
      CHANNEL: ${{ needs.verify.outputs.channel }}
      GH_TOKEN: ${{ github.token }}
      GH_REPO: ${{ github.repository }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: release-files
          path: dist
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: release-signatures
          path: dist/release
      - name: Publish the release
        run: |
          prerelease=()
          if [ "$CHANNEL" = beta ]; then prerelease=(--prerelease); fi
          gh release create "$GITHUB_REF_NAME" dist/release/* --verify-tag \
            --title "Darkpanel $VERSION" --notes-file dist/release-notes.md "${prerelease[@]}"
      - name: Regenerate releases.json
        run: |
          # Rebuilt from every published release, never appended to an older index.
          gh release list --exclude-drafts --limit 1000 --json tagName,isDraft >"$RUNNER_TEMP/list.json"
          go run ./release/releasetool index <"$RUNNER_TEMP/list.json" >"$RUNNER_TEMP/releases.json"
          if ! gh release view index >/dev/null 2>&1; then
            gh release create index --prerelease --target "$GITHUB_SHA" --title "Release index" \
              --notes "releases.json lists every published version and its channel. It selects versions only; trust comes from each release's signatures."
          fi
          gh release upload index "$RUNNER_TEMP/releases.json" --clobber
```

- [ ] **Step 4: Create release/README.md**

```markdown
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

## Rotating the signing key

Generate the new key, store it as `MINISIGN_KEY_2` and `MINISIGN_PASSWORD_2`,
and add its public key to `internal/update/trustedkeys`, keeping the old one.
Releases are then signed by both keys. After at least two minor releases,
move the new key into the `_1` secrets, delete the `_2` secrets, and remove
the old public key from `internal/update/trustedkeys` in the same release.
```

- [ ] **Step 5: Run the checks**

```bash
GOTOOLCHAIN=local go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/release.yml .github/workflows/ci.yml
```
Expected: no output, exit 0.

```bash
grep -E '^\s*-? *uses: ' .github/workflows/release.yml | grep -vE '@[0-9a-f]{40} # v[0-9]'
```
Expected: no output (every action is pinned by commit SHA with its version).

```bash
grep -c 'sha256sum --check --strict' .github/workflows/release.yml
grep -F 'aead.dev/minisign/cmd/minisign@v0.3.0' .github/workflows/release.yml
```
Expected: `1`, then the `go install` line.

- [ ] **Step 6: Commit**

```bash
git add .github/workflows/release.yml release/allowed_signers release/README.md
git commit -m "chore(release): add the signed release workflow"
```
