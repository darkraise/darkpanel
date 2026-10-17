# M1 Repository, Tooling, CI and Gates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the documentation-only repository into a buildable project: one Go module whose `make build` produces static `linux/amd64` and `linux/arm64` binaries with the web UI embedded, CI that lints, type-checks, tests and builds on every push, the legal and contribution files, and the project's gates.

**Architecture:** A Go module with a cobra CLI (`cmd/darkpanel`) and a `web` package that embeds the Vite build in `web/dist`. A `Makefile` is the single entry point for building, linting, testing and vulnerability scanning; the CI workflow and the gates manifest call only its targets. The web app is the `create-darkraise-ui` 6.9.6 template with exact pins, a placeholder page, ESLint and Vitest.

**Tech Stack:** Go 1.27.2, cobra v1.10.2, golangci-lint v2.15.0, govulncheck v1.9.0, GNU make; Node 24, pnpm 11.17.0, Vite 8, React 19, darkraise-ui 6.9.6, TypeScript 6.0.3, ESLint 10, Vitest 5; GitHub Actions.

**Spec:** docs/superpowers/specs/2026-10-17-m1-repository-tooling-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves.

**Plan review:** 2026-10-17 — dr-superpowers:judge-opus — executability 16 / coherence 15 / coverage 16 / assumptions 15 (round 1)

**Parallelism:** waves — 1 | 2 | 3-4 | 5 | 6-9 | 10

**Integration check:** `if [ -f web/eslint.config.js ]; then timeout 900 make check; elif [ -f web/package.json ]; then timeout 900 make lint-go test-go test-adr build verify-build vuln; elif [ -f Makefile ]; then timeout 600 make lint-go test-go test-adr vuln; else GOTOOLCHAIN=local timeout 300 go vet ./... && GOTOOLCHAIN=local timeout 300 go test ./... && bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete; fi`

**Worktree setup:** `if [ -f web/package.json ]; then timeout 300 pnpm -C web install --frozen-lockfile; fi`

## Global Constraints

- The spec is the source of truth, including its "Owner decisions" and "Plan-time rulings" sections. Where this plan gives a file's complete content, write exactly that content.
- Pins are exact (no `^`, `~` or ranges) in `go.mod` and `web/package.json`. Versions, verbatim from the spec: Go 1.27.2 (`go 1.27`, `toolchain go1.27.2`); golangci-lint v2.15.0; govulncheck `golang.org/x/vuln` v1.9.0; `github.com/spf13/cobra` v1.10.2; Node 24; pnpm 11.17.0; darkraise-ui 6.9.6 (its i18next, recharts and react-i18next peers are optional and not installed); react and react-dom 19.3.1; vite 8.3.4; @vitejs/plugin-react-swc 4.3.3; tailwindcss and @tailwindcss/vite 4.3.3; typescript 6.0.3; eslint 10.13.0 with @eslint/js 10.0.1; typescript-eslint 8.71.2; eslint-plugin-react-hooks 7.1.1; eslint-plugin-react-refresh 0.5.7; globals 17.13.0; vitest 5.0.5; jsdom 30.1.3; @testing-library/react 16.3.3; @testing-library/dom 10.4.2; @types/react and @types/react-dom 19.3.0; @types/node 26.6.5.
- Every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001). The `Makefile` exports it; a `go` command typed outside `make` is prefixed with `GOTOOLCHAIN=local`.
- The embedded UI loads nothing from the internet: no font, icon or script URL outside the app.
- A task edits only the files in its own `**Files:**` block, plus files its tools regenerate as named in its steps (`go.sum`, `web/pnpm-lock.yaml`).
- In a waves run every task executes in its own git worktree created by the controller and commits only there; the controller merges a wave's worktrees before starting the next wave. No two tasks share a git index. The exception is Task 10, which pushes the plan branch: it runs in the controller's checkout of that branch, after every earlier wave has merged.
- `**Items:**` numbers are row numbers of the register `docs/superpowers/registers/2026-10-17-m1-repository-tooling.md`.
- Tests use no ports and no fixed paths outside the worktree; scratch files go in directories made with `mktemp -d`. The Go build cache and the pnpm store are shared between worktrees; both are safe for concurrent use.
- Every network, build and test command carries an explicit `timeout`.
- English only. Commits use `<type>(<scope>): <subject>` with type one of feat, fix, docs, style, refactor, test, chore, perf; the subject is imperative, at most 50 characters, no trailing period; no AI attribution of any kind.
- `Makefile` recipe lines begin with a tab character, never spaces.

## Contracts

**Go module and packages**

- Module path `github.com/darkraise/darkpanel`; `go.mod` carries `go 1.27` and `toolchain go1.27.2`.
- Package `web` (`web/embed.go`) exports `var Dist embed.FS`, declared with `//go:embed all:dist`. Paths inside it start with `dist/`; the built UI's entry is `dist/index.html`. `web/dist/.gitkeep` is committed so the package compiles without a web build.
- `DARKPANEL_REQUIRE_UI=1 go test ./web/` fails unless `Dist` holds `dist/index.html`; without the variable that test skips.

**CLI** (`cmd/darkpanel/main.go`, package `main`)

- `var version = "dev"` and `var commit = "unknown"`, set by `-ldflags "-X main.version=<v> -X main.commit=<c>"`.
- `darkpanel version` prints `darkpanel <version> (commit <commit>, <go version>, <GOOS>/<GOARCH>)` and a newline, for example `darkpanel dev (commit unknown, go1.27.2, linux/amd64)`.
- `darkpanel version --json` prints one JSON object and a newline: `{"version":"…","commit":"…","go":"…","os":"…","arch":"…","ui":<bool>}`. `ui` is `true` when `web.Dist` holds `dist/index.html`.

**Build outputs**

- `bin/darkpanel-linux-amd64` and `bin/darkpanel-linux-arm64`.
- UI marker: the string `<title>Darkpanel</title>`. Task 5 puts it in `web/index.html`; Vite copies it into `web/dist/index.html`; the `verify-build` target (Task 3) requires it in each binary.

**Make targets** (Task 3; every later task and CI call these names)

| Target | Runs |
|---|---|
| `web` | `pnpm -C web install --frozen-lockfile` then `pnpm -C web build` |
| `build` | `web`, then for `amd64` and `arm64`: `CGO_ENABLED=0 GOOS=linux GOARCH=<arch> go build -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o bin/darkpanel-linux-<arch> ./cmd/darkpanel` |
| `verify-build` | `file` reports both binaries as statically linked ELF for the right architecture; each binary contains the UI marker; `DARKPANEL_REQUIRE_UI=1 go test -count=1 ./web/` passes |
| `lint-go` | `golangci-lint run` and `go vet ./...` |
| `lint-web` | `pnpm -C web lint` and `pnpm -C web typecheck` |
| `lint` | `lint-go lint-web` |
| `test-go` | `go test ./...` |
| `test-web` | `pnpm -C web test` |
| `test-adr` | `bash scripts/check-adrs.test.sh` and `bash scripts/check-adrs.sh --complete` |
| `test` | `test-go test-web test-adr` |
| `vuln` | `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...` |
| `check` | `lint test build verify-build vuln` |
| `clean` | removes `bin/` and everything in `web/dist/` except `.gitkeep` |

`VERSION` defaults to `git describe --tags --always --dirty`, `COMMIT` to `git rev-parse --short HEAD`.

**Web package** (Task 5)

- `web/package.json`: name `darkpanel-web`, `packageManager` `pnpm@11.17.0`, scripts `dev`, `build`, `typecheck`, `lint`, `test`.
- `web/pnpm-lock.yaml` and `web/pnpm-workspace.yaml` are committed.

**CI actions** (Task 9; resolved with `gh api` on 2026-10-17)

| Action | Version | Commit SHA |
|---|---|---|
| `actions/checkout` | v7.0.1 | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| `actions/setup-go` | v7.0.0 | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| `pnpm/action-setup` | v6.1.0 | `ea17c68df8912ef543352723c149a84f56e3d413` |
| `actions/setup-node` | v7.1.0 | `949feb2413d6458794dcd2491c4babbbce0c15c1` |
| `golangci/golangci-lint-action` | v9.3.0 | `ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a` |
| `actions/upload-artifact` | v7.0.2 | `cf430e030ddbb5b0abf93d22962f4752f3646cd9` |

CI jobs: `adr`, `go`, `web`, `build`; artifact `darkpanel-binaries`, kept 7 days.

**Gates** (Task 8, `docs/superpowers/gates.md`): 1 ADR records, 2 Lint, 3 Tests, 4 Build, 5 Vulnerabilities, 6 CI green.

## Assumptions (evidence)

- Go 1.27.2's `go1.27.2.linux-amd64.tar.gz` has SHA-256 `ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5` — `curl -s 'https://go.dev/dl/?mode=json&include=all' | jq …`, 2026-10-17. golangci-lint v2.15.0 publishes `golangci-lint-2.15.0-linux-amd64.tar.gz` and `golangci-lint-2.15.0-checksums.txt` — `gh api repos/golangci/golangci-lint/releases/tags/v2.15.0`, 2026-10-17. Both archives were downloaded to a scratch directory and passed their checksums the same day.
- Go and golangci-lint are not installed on the host (`go: command not found`, 2026-10-17); Node 24.18.0, pnpm 11.17.0, GNU Make 4.4.1, `file`, `jq` and `gh` (logged in as `darkraise`) are. `grep` on the host is ugrep 7.8.4, which accepts `-qaF`.
- The whole plan was prototyped in a scratch directory on 2026-10-17 with the code below: `make check` exited 0 (golangci-lint `0 issues.`, Go and Vitest tests pass, `check-adrs tests: 21 passed, 0 failed`, `No vulnerabilities found.`), and `file` reported `ELF 64-bit LSB executable, x86-64, version 1 (SYSV), statically linked` and `ELF 64-bit LSB executable, ARM aarch64, version 1 (SYSV), statically linked`.
- The Go linker drops an `embed.FS` no reachable code reads: with a blank import of `web`, or `var _ = web.Dist`, the binary did not contain `<title>Darkpanel</title>`; with `uiEmbedded(web.Dist)` called from `currentVersion` it did, and `make verify-build` exited 2 on a binary built without the UI (prototype, 2026-10-17). This is why the spec now has the `ui` key.
- `npx -y create-darkraise-ui@6.9.6 web -y --no-install --layout sidebar --shell-style classic --accent blue --surface-color slate --density compact` writes the eight spec-listed files plus `package.json` and `.gitignore`, then runs `git init` and commits inside the generated directory; `--no-install` skips `package-lock.json` (2026-10-17). Task 5 therefore generates into a `mktemp -d` directory and copies the eight files.
- The generated files that later steps edit or rely on contain, verbatim (generator output, 2026-10-17): `web/src/main.tsx` has `import { App } from "./app"` (a named import, which Task 6's rewritten `app.tsx` keeps exporting); `web/src/theme.config.ts` has the line `    mode: "system",` inside `defaults` and no other `mode:` line; `web/index.html` has `    <title>web</title>`, the `/vite.svg` icon link and the six Google Fonts lines Task 5 deletes.
- `scripts/check-adrs.sh` and `scripts/check-adrs.test.sh` are in the repository (`rg --files`, 2026-10-17) and use only bash builtins, awk, `sort` and `mktemp` (the word `grep` appears only in comments; `rg -o` over both scripts, 2026-10-17), all present on the `ubuntu-24.04` runner.
- The generated `SidebarLayout` throws `useRouterAdapter must be used inside a <RouterAdapterProvider>`; `darkraise-ui/router` exports `RouterAdapterProvider` and `useOptionalRouterAdapter`, which returns the kit's fallback adapter outside a provider (`dist/router/index.d.ts` in darkraise-ui 6.9.6; prototype test, 2026-10-17).
- pnpm 11.17.0 fails an install with `ERR_PNPM_IGNORED_BUILDS` for `@swc/core` unless `allowBuilds` decides it, and applies a one-day minimum release age: eslint 10.13.0 was published 2026-10-16T14:24:10Z, so before 2026-10-17T14:24Z pnpm appends `minimumReleaseAgeExclude: [eslint@10.13.0]` to `pnpm-workspace.yaml` and continues (prototype, 2026-10-17). After that time pnpm appends nothing. Either file is correct.
- `web/node_modules` contains a Go file (`.pnpm/flatted@3.4.4/node_modules/flatted/golang/pkg/flatted/flatted.go`). Go's `./...` skips directories whose names begin with a dot, and pnpm keeps every real package under `.pnpm`, so `go test`, `go vet`, golangci-lint and govulncheck never see it (prototype `make check`, 2026-10-17).
- `actions/setup-go` v7 uses the `toolchain` directive of `go.mod` when present (its README at v7.0.0). `golangci/golangci-lint-action` v9.3.0 has `install-only`; `pnpm/action-setup` v6.1.0 has `package_json_file`; `actions/setup-node` v7.1.0 has `cache` and `cache-dependency-path` (their `action.yml` files at those tags, 2026-10-17). The draft workflow passed actionlint v1.7.12 the same day.
- The runner image `ubuntu-24.04` provides `file` and GNU make — unverified; Task 10 verifies it (the CI `build` job runs `verify-build`).
- `github.com/darkraise/darkpanel` is public and empty (`gh repo view … --json isEmpty,visibility`: `true`, `PUBLIC`, 2026-10-17), and private vulnerability reporting is off (`gh api repos/darkraise/darkpanel/private-vulnerability-reporting`: `{"enabled":false}`). Task 10 turns it on, because `SECURITY.md` sends reporters there.
- Execution normally runs on a feature branch. Task 10 pushes the branch it runs on and checks CI for that commit; finishing the branch merges it into `main`, pushes `main`, and re-checks gate 6 there (spec §6).
- Wave 6-9 holds four tasks. They are trivially independent: four disjoint file sets, three of them documentation or configuration with no test that runs at the same time as another. A wider wave runs in parts.

## Task index

1. Host toolchain and web embed package
2. Version command
3. Makefile and Go lint configuration
4. Git ignore and editor configuration
5. Web package and build
6. Placeholder page, ESLint and Vitest
7. Repository documents
8. Gates manifest
9. CI workflow
10. Full verification, push and CI green

---

### Task 1: Host toolchain and web embed package

**Files:**
- Create: `go.mod`
- Create: `web/embed.go`
- Create: `web/dist/.gitkeep` (empty)
- Test: `web/embed_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: the Go module and `web.Dist` (Contracts, "Go module and packages"); Go 1.27.2 and golangci-lint v2.15.0 on the host `PATH` for every later task.

**Items:** 1, 6

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Confirm the install targets are free**

Run: `test ! -e /usr/local/go && test ! -e /usr/local/bin/go && test ! -e /usr/local/bin/golangci-lint && echo free`
Expected: `free`. If anything exists, run Step 4's check: when it already prints the pinned versions and paths (a retry after an earlier install), skip to Step 5. Otherwise stop and report BLOCKED with what you found; do not delete it.

- [ ] **Step 2: Install Go 1.27.2 system-wide**

Run as one command; the subshell stops at the first failure.

```bash
dl=$(mktemp -d)
(
  set -e
  cd "$dl"
  timeout 300 curl -fsSLO https://go.dev/dl/go1.27.2.linux-amd64.tar.gz
  echo "ecbadb99091a3f46e31f5f934b068b1864eafa7995211b39eaddf76996045fe5  go1.27.2.linux-amd64.tar.gz" | sha256sum -c
  tar -C /usr/local -xzf go1.27.2.linux-amd64.tar.gz
  ln -s /usr/local/go/bin/go /usr/local/bin/go
  ln -s /usr/local/go/bin/gofmt /usr/local/bin/gofmt
)
rm -rf "$dl"
```

Expected: `go1.27.2.linux-amd64.tar.gz: OK` and exit 0. Stop on any other output.

- [ ] **Step 3: Install golangci-lint v2.15.0 system-wide**

```bash
dl=$(mktemp -d)
(
  set -e
  cd "$dl"
  timeout 300 curl -fsSLO https://github.com/golangci/golangci-lint/releases/download/v2.15.0/golangci-lint-2.15.0-linux-amd64.tar.gz
  timeout 60 curl -fsSLO https://github.com/golangci/golangci-lint/releases/download/v2.15.0/golangci-lint-2.15.0-checksums.txt
  sha256sum --ignore-missing -c golangci-lint-2.15.0-checksums.txt
  tar -xzf golangci-lint-2.15.0-linux-amd64.tar.gz
  install -m 0755 golangci-lint-2.15.0-linux-amd64/golangci-lint /usr/local/bin/golangci-lint
)
rm -rf "$dl"
```

Expected: `golangci-lint-2.15.0-linux-amd64.tar.gz: OK` and exit 0.

- [ ] **Step 4: Verify the toolchain**

Run: `command -v go golangci-lint && go version && golangci-lint version`
Expected: `/usr/local/bin/go`, `/usr/local/bin/golangci-lint`, `go version go1.27.2 linux/amd64`, and a line starting `golangci-lint has version 2.15.0`.

- [ ] **Step 5: Write the failing test**

`web/embed_test.go`:

```go
package web

import (
	"io/fs"
	"os"
	"testing"
)

func TestDistContainsIndex(t *testing.T) {
	if os.Getenv("DARKPANEL_REQUIRE_UI") != "1" {
		t.Skip("set DARKPANEL_REQUIRE_UI=1 to require a built web UI")
	}
	if _, err := fs.Stat(Dist, "dist/index.html"); err != nil {
		t.Fatalf("embedded UI lacks dist/index.html: %v", err)
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `GOTOOLCHAIN=local timeout 120 go test ./web/`
Expected: FAIL — `go: go.mod file not found in current directory or any parent directory` (or `undefined: Dist` once `go.mod` exists).

- [ ] **Step 7: Write the module and the package**

`go.mod`:

```
module github.com/darkraise/darkpanel

go 1.27

toolchain go1.27.2
```

`web/embed.go`:

```go
// Package web embeds the built web UI.
package web

import "embed"

// Dist holds the Vite build output, rooted at dist/.
//
//go:embed all:dist
var Dist embed.FS
```

Create the empty placeholder: `mkdir -p web/dist && : > web/dist/.gitkeep`

- [ ] **Step 8: Run the test both ways**

Run: `GOTOOLCHAIN=local timeout 120 go test -count=1 -v ./web/`
Expected: PASS with `--- SKIP: TestDistContainsIndex`.

Run: `DARKPANEL_REQUIRE_UI=1 GOTOOLCHAIN=local timeout 120 go test -count=1 ./web/`
Expected: FAIL with `embedded UI lacks dist/index.html`.

Run: `printf '<html></html>' > web/dist/index.html && DARKPANEL_REQUIRE_UI=1 GOTOOLCHAIN=local timeout 120 go test -count=1 ./web/; rm web/dist/index.html`
Expected: `ok  	github.com/darkraise/darkpanel/web`. Then `ls -A web/dist` prints only `.gitkeep`.

- [ ] **Step 9: Vet and format**

Run: `GOTOOLCHAIN=local timeout 120 go vet ./... && test -z "$(gofmt -l web)" && echo clean`
Expected: `clean`.

- [ ] **Step 10: Commit**

```bash
git add go.mod web/embed.go web/embed_test.go web/dist/.gitkeep
git commit -m "feat(web): add embedded UI package"
```

### Task 2: Version command

**Files:**
- Modify: `go.mod` (via `go get` and `go mod tidy`; also creates `go.sum`)
- Create: `cmd/darkpanel/main.go`
- Test: `cmd/darkpanel/version_test.go`

**Interfaces:**
- Consumes: `web.Dist` (Contracts, "Go module and packages").
- Produces: the `darkpanel` CLI, `main.version`, `main.commit` and the `version` output formats (Contracts, "CLI").

**Items:** 1

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

`cmd/darkpanel/version_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"testing"
	"testing/fstest"
)

func runRoot(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("darkpanel %v: %v", args, err)
	}
	return out.String()
}

func TestVersionText(t *testing.T) {
	got := runRoot(t, "version")
	want := "darkpanel dev (commit unknown, " + runtime.Version() + ", " + runtime.GOOS + "/" + runtime.GOARCH + ")\n"
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestVersionJSON(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal([]byte(runRoot(t, "version", "--json")), &got); err != nil {
		t.Fatalf("version --json is not a JSON object: %v", err)
	}
	want := map[string]string{
		"version": "dev",
		"commit":  "unknown",
		"go":      runtime.Version(),
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("version --json %q = %v, want %q", key, got[key], value)
		}
	}
	// The value depends on whether the web build has run, so only its type is fixed.
	if _, ok := got["ui"].(bool); !ok {
		t.Errorf("version --json \"ui\" = %v, want a boolean", got["ui"])
	}
	if len(got) != len(want)+1 {
		t.Errorf("version --json has keys %v, want exactly version, commit, go, os, arch, ui", got)
	}
}

func TestUIEmbedded(t *testing.T) {
	tests := []struct {
		name string
		dist fstest.MapFS
		want bool
	}{
		{"built", fstest.MapFS{"dist/index.html": {Data: []byte("<html></html>")}, "dist/.gitkeep": {}}, true},
		{"placeholder only", fstest.MapFS{"dist/.gitkeep": {}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := uiEmbedded(tt.dist); got != tt.want {
				t.Errorf("uiEmbedded() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local timeout 120 go test ./cmd/darkpanel/`
Expected: FAIL — `undefined: newRootCmd` and `undefined: uiEmbedded`.

- [ ] **Step 3: Write the command**

`cmd/darkpanel/main.go`:

```go
// Command darkpanel is the Darkpanel controller, agent and CLI.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/web"
)

// Set by the build with -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = "unknown"
)

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	UI      bool   `json:"ui"`
}

func currentVersion() versionInfo {
	return versionInfo{
		Version: version,
		Commit:  commit,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		// Reading web.Dist here is what keeps the UI in the binary: the linker
		// drops an embed.FS that no reachable code reads.
		UI: uiEmbedded(web.Dist),
	}
}

func uiEmbedded(dist fs.FS) bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "darkpanel",
		Short:         "Single-binary fleet manager for unmanaged Linux servers",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit, Go version and platform",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeVersion(cmd.OutOrStdout(), currentVersion(), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the version as a JSON object")
	return cmd
}

func writeVersion(w io.Writer, v versionInfo, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(v)
	}
	_, err := fmt.Fprintf(w, "darkpanel %s (commit %s, %s, %s/%s)\n", v.Version, v.Commit, v.Go, v.OS, v.Arch)
	return err
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "darkpanel:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Add cobra and tidy the module**

Run: `GOTOOLCHAIN=local timeout 300 go get github.com/spf13/cobra@v1.10.2 && GOTOOLCHAIN=local timeout 300 go mod tidy && cat go.mod`
Expected `go.mod`, exactly:

```
module github.com/darkraise/darkpanel

go 1.27

toolchain go1.27.2

require github.com/spf13/cobra v1.10.2

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local timeout 120 go test -count=1 ./...`
Expected: `ok  	github.com/darkraise/darkpanel/cmd/darkpanel` and `ok  	github.com/darkraise/darkpanel/web`.

- [ ] **Step 6: Run the binary**

Run: `out=$(mktemp -d) && GOTOOLCHAIN=local timeout 120 go build -o "$out/darkpanel" ./cmd/darkpanel && "$out/darkpanel" version && "$out/darkpanel" version --json; rm -rf "$out"`
Expected: `darkpanel dev (commit unknown, go1.27.2, linux/amd64)` and `{"version":"dev","commit":"unknown","go":"go1.27.2","os":"linux","arch":"amd64","ui":false}`.

- [ ] **Step 7: Lint**

Run: `timeout 300 golangci-lint run && GOTOOLCHAIN=local timeout 120 go vet ./...`
Expected: `0 issues.` and no `go vet` output. golangci-lint runs here without `.golangci.yml` (Task 3 adds it); the standard linters it uses by default must already be clean.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum cmd/darkpanel/main.go cmd/darkpanel/version_test.go
git commit -m "feat(cli): add version command"
```

### Task 3: Makefile and Go lint configuration

**Files:**
- Create: `Makefile`
- Create: `.golangci.yml`

**Interfaces:**
- Consumes: the Go module, `./cmd/darkpanel`, `./web/` and `DARKPANEL_REQUIRE_UI` (Contracts, "Go module and packages", "CLI"); `scripts/check-adrs.test.sh` and `scripts/check-adrs.sh` (already in the repository).
- Produces: every make target (Contracts, "Make targets"), the binary paths and the UI-marker check (Contracts, "Build outputs").

**Items:** 1, 2

**Depends on:** Task 1, Task 2

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Run the targets to verify they fail**

Run: `timeout 60 make lint-go test-go test-adr vuln`
Expected: FAIL — `make: *** No rule to make target 'lint-go'.  Stop.`

- [ ] **Step 2: Write the Makefile**

`Makefile` (every recipe line starts with one tab character):

```make
SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := build

VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse --short HEAD)
ARCHES := amd64 arm64
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
# A string from web/index.html; finding it in a binary proves the UI is embedded.
UI_MARKER := <title>Darkpanel</title>

# ADR 0001: build only with the toolchain go.mod pins, never a downloaded one.
export GOTOOLCHAIN := local

.PHONY: web build verify-build lint-go lint-web lint test-go test-web test-adr test vuln check clean

web:
	pnpm -C web install --frozen-lockfile
	pnpm -C web build

build: web
	for arch in $(ARCHES); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o bin/darkpanel-linux-$$arch ./cmd/darkpanel; \
	done

verify-build:
	file bin/darkpanel-linux-amd64 | grep -F 'ELF 64-bit LSB executable, x86-64' | grep -qF 'statically linked'
	file bin/darkpanel-linux-arm64 | grep -F 'ELF 64-bit LSB executable, ARM aarch64' | grep -qF 'statically linked'
	for arch in $(ARCHES); do grep -qaF '$(UI_MARKER)' bin/darkpanel-linux-$$arch; done
	DARKPANEL_REQUIRE_UI=1 go test -count=1 ./web/

lint-go:
	golangci-lint run
	go vet ./...

lint-web:
	pnpm -C web lint
	pnpm -C web typecheck

lint: lint-go lint-web

test-go:
	go test ./...

test-web:
	pnpm -C web test

test-adr:
	bash scripts/check-adrs.test.sh
	bash scripts/check-adrs.sh --complete

test: test-go test-web test-adr

vuln:
	go run $(GOVULNCHECK) ./...

check: lint test build verify-build vuln

clean:
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete
```

- [ ] **Step 3: Write the lint configuration**

`.golangci.yml`:

```yaml
version: "2"

linters:
  default: standard
  enable:
    - errorlint
    - gosec
    - misspell
    - revive

formatters:
  enable:
    - gofmt
    - goimports
```

- [ ] **Step 4: Run the Go, ADR and vulnerability targets**

Run: `timeout 600 make lint-go test-go test-adr vuln`
Expected: exit 0, with `0 issues.`, `ok  	github.com/darkraise/darkpanel/cmd/darkpanel`, `check-adrs tests: 21 passed, 0 failed` (the count may differ; `0 failed` is what matters) and `No vulnerabilities found.`. A `missing separator` error means a recipe line starts with spaces.

- [ ] **Step 5: Dry-run the web-dependent targets**

The web app does not exist yet (Task 5), so only print these targets' commands.

Run: `timeout 60 make -n build verify-build clean`
Expected: the `pnpm -C web …` lines, a `for arch in amd64 arm64` loop whose `-ldflags` carries `-X main.version=<git describe output> -X main.commit=<short sha>`, the three `verify-build` checks with `<title>Darkpanel</title>`, and the two `clean` commands.

- [ ] **Step 6: Commit**

```bash
git add Makefile .golangci.yml
git commit -m "chore(build): add Makefile and Go lint config"
```

### Task 4: Git ignore and editor configuration

**Files:**
- Create: `.gitignore`
- Create: `.editorconfig`

**Interfaces:**
- Consumes: the `web/dist/.gitkeep` path (Contracts, "Go module and packages").
- Produces: ignore rules that keep `bin/`, `web/node_modules/`, the built UI and `coverage/` out of git while `web/dist/.gitkeep` stays tracked.

**Items:** 1

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the ignore check to verify it fails**

```bash
git check-ignore -q bin/darkpanel-linux-amd64 && git check-ignore -q web/node_modules/x \
  && git check-ignore -q web/dist/index.html && git check-ignore -q coverage/x \
  && ! git check-ignore -q web/dist/.gitkeep && echo ignore-ok
```

Expected: no `ignore-ok` (the first `check-ignore` exits 1).

- [ ] **Step 2: Write the files**

`.gitignore`:

```
bin/
web/node_modules/
web/dist/*
!web/dist/.gitkeep
coverage/
```

`.editorconfig`:

```
root = true

[*]
charset = utf-8
end_of_line = lf
insert_final_newline = true
indent_style = space
indent_size = 2

[{*.go,Makefile}]
indent_style = tab
```

- [ ] **Step 3: Run the ignore check to verify it passes**

Run the command from Step 1.
Expected: `ignore-ok`.

- [ ] **Step 4: Commit**

```bash
git add .gitignore .editorconfig
git commit -m "chore(repo): add gitignore and editorconfig"
```

### Task 5: Web package and build

**Files:**
- Create: `web/package.json`
- Create: `web/pnpm-workspace.yaml`
- Create: `web/pnpm-lock.yaml` (generated by `pnpm install`)
- Create (copied from the generator): `web/index.html`, `web/vite.config.ts`, `web/tsconfig.json`, `web/src/main.tsx`, `web/src/app.tsx`, `web/src/env.d.ts`, `web/src/theme.config.ts`, `web/src/styles/globals.css`
- Test: `make build verify-build` (Task 3's targets)

**Interfaces:**
- Consumes: the `web`, `build` and `verify-build` targets and the UI marker (Contracts, "Make targets", "Build outputs"); `web.Dist` and the `version --json` output (Contracts, "Go module and packages", "CLI"); the ignore rules from Task 4.
- Produces: `web/package.json` with its scripts, `web/pnpm-lock.yaml` and `web/pnpm-workspace.yaml` (Contracts, "Web package"); `<title>Darkpanel</title>` in `web/index.html`.

**Items:** 1

**Depends on:** Task 1, Task 2, Task 3, Task 4

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Run the build to verify it fails**

Run: `timeout 300 make build verify-build`
Expected: FAIL at `pnpm -C web install --frozen-lockfile`, because `web/package.json` does not exist.

- [ ] **Step 2: Generate the template and copy its source files**

The generator runs `git init` in its output, so it runs in a scratch directory.

```bash
gen=$(mktemp -d)
(cd "$gen" && timeout 300 npx -y create-darkraise-ui@6.9.6 web -y --no-install \
  --layout sidebar --shell-style classic --accent blue --surface-color slate --density compact)
for f in index.html vite.config.ts tsconfig.json src/main.tsx src/app.tsx src/env.d.ts \
         src/theme.config.ts src/styles/globals.css; do
  mkdir -p "web/$(dirname "$f")"
  cp "$gen/web/$f" "web/$f"
done
rm -rf "$gen"
```

Expected: the generator ends with `Success! Created web at …`. Do not copy its `package.json`, `.gitignore` or `.git`.

- [ ] **Step 3: Write the package manifest and the pnpm settings**

`web/package.json`:

```json
{
  "name": "darkpanel-web",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "packageManager": "pnpm@11.17.0",
  "engines": {
    "node": ">=24"
  },
  "scripts": {
    "dev": "vite",
    "build": "tsc --noEmit && vite build && touch dist/.gitkeep",
    "typecheck": "tsc --noEmit",
    "lint": "eslint .",
    "test": "vitest run"
  },
  "dependencies": {
    "darkraise-ui": "6.9.6",
    "react": "19.3.1",
    "react-dom": "19.3.1"
  },
  "devDependencies": {
    "@eslint/js": "10.0.1",
    "@tailwindcss/vite": "4.3.3",
    "@testing-library/dom": "10.4.2",
    "@testing-library/react": "16.3.3",
    "@types/node": "26.6.5",
    "@types/react": "19.3.0",
    "@types/react-dom": "19.3.0",
    "@vitejs/plugin-react-swc": "4.3.3",
    "eslint": "10.13.0",
    "eslint-plugin-react-hooks": "7.1.1",
    "eslint-plugin-react-refresh": "0.5.7",
    "globals": "17.13.0",
    "jsdom": "30.1.3",
    "tailwindcss": "4.3.3",
    "typescript": "6.0.3",
    "typescript-eslint": "8.71.2",
    "vite": "8.3.4",
    "vitest": "5.0.5"
  }
}
```

`web/pnpm-workspace.yaml`:

```yaml
allowBuilds:
  '@swc/core': false
```

- [ ] **Step 4: Make the page load nothing from the internet and set its title**

In `web/index.html`, delete this line:

```html
    <link rel="icon" type="image/svg+xml" href="/vite.svg" />
```

Replace `    <title>web</title>` with `    <title>Darkpanel</title>`.

Delete these six lines:

```html
    <link rel="preconnect" href="https://fonts.googleapis.com" />
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
    <link
      href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500&display=swap"
      rel="stylesheet"
    />
```

Leave the rest of the file, including the inline theme-restoring `<script>`, unchanged. Check: `grep -c 'https://' web/index.html` prints `0` and `grep -cF '<title>Darkpanel</title>' web/index.html` prints `1`.

- [ ] **Step 5: Install the dependencies**

Run: `timeout 300 pnpm -C web install`
Expected: exit 0, `web/pnpm-lock.yaml` created, and no `ERR_PNPM_IGNORED_BUILDS`. If pnpm prints `Added 1 entry to minimumReleaseAgeExclude in pnpm-workspace.yaml`, keep the entry it wrote (`eslint@10.13.0`, published less than a day before 2026-10-17T14:24Z); the file then holds exactly `allowBuilds` plus that `minimumReleaseAgeExclude` list.

- [ ] **Step 6: Run the build to verify it passes**

Run: `timeout 600 make build verify-build`
Expected: exit 0; Vite prints `dist/index.html` and two `dist/assets/` files; `ok  	github.com/darkraise/darkpanel/web`.

Run: `timeout 10 bin/darkpanel-linux-amd64 version --json`
Expected: an object whose `"ui"` is `true` and whose `"version"` equals the output of `git describe --tags --always --dirty`.

- [ ] **Step 7: Check what git sees**

Run: `git status --short --untracked-files=all web`
Expected: exactly the eleven files in this task's Files block, all untracked (`??`); nothing under `web/node_modules/` or `web/dist/`.

- [ ] **Step 8: Commit**

```bash
git add web/package.json web/pnpm-workspace.yaml web/pnpm-lock.yaml web/index.html \
  web/vite.config.ts web/tsconfig.json web/src/main.tsx web/src/app.tsx web/src/env.d.ts \
  web/src/theme.config.ts web/src/styles/globals.css
git commit -m "feat(web): scaffold web app from darkraise-ui"
```

### Task 6: Placeholder page, ESLint and Vitest

**Files:**
- Create: `web/eslint.config.js`
- Create: `web/vitest.config.ts`
- Create: `web/src/test/setup.ts`
- Modify: `web/src/app.tsx`
- Modify: `web/src/theme.config.ts`
- Test: `web/src/app.test.tsx`

**Interfaces:**
- Consumes: `web/package.json` scripts `lint`, `typecheck`, `test` and the `lint-web`, `test-web`, `build`, `verify-build` targets (Contracts, "Web package", "Make targets").
- Produces: nothing later tasks name.

**Items:** 1

**Depends on:** Task 3, Task 5

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 0 = 3

- [ ] **Step 1: Write the test and its harness**

`web/vitest.config.ts`:

```ts
import { defineConfig } from "vitest/config"
import react from "@vitejs/plugin-react-swc"

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
  },
})
```

`web/src/test/setup.ts`:

```ts
// jsdom has no matchMedia, and the darkraise-ui theme provider reads it on mount.
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string): MediaQueryList => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
})
```

`web/src/app.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { expect, test } from "vitest"
import { App } from "./app"

test("renders the Darkpanel heading", () => {
  render(<App />)
  expect(screen.getByRole("heading", { name: "Darkpanel" })).toBeDefined()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 180 pnpm -C web test`
Expected: FAIL — `Error: useRouterAdapter must be used inside a <RouterAdapterProvider>`.

- [ ] **Step 3: Write the page and the theme mode**

`web/src/app.tsx` (replace the whole file):

```tsx
import type { ReactNode } from "react"
import { ThemeProvider } from "darkraise-ui/theme"
import { SidebarLayout } from "darkraise-ui/layout"
import {
  RouterAdapterProvider,
  useOptionalRouterAdapter,
} from "darkraise-ui/router"
import { themeConfig } from "./theme.config"

// SidebarLayout requires a router adapter. Until the app has a router, provide
// the kit's fallback adapter, which useOptionalRouterAdapter returns outside a
// provider and which navigates with window.location.
function FallbackRouter({ children }: { children: ReactNode }) {
  const adapter = useOptionalRouterAdapter()
  return <RouterAdapterProvider value={adapter}>{children}</RouterAdapterProvider>
}

export function App() {
  return (
    <ThemeProvider config={themeConfig}>
      <FallbackRouter>
        <SidebarLayout nav={[]} showThemeSwitcher={true}>
          <div className="flex flex-col items-center justify-center gap-4 py-16">
            <h1 className="text-4xl font-medium">Darkpanel</h1>
            <p className="text-muted-foreground">
              The walking skeleton replaces this page.
            </p>
          </div>
        </SidebarLayout>
      </FallbackRouter>
    </ThemeProvider>
  )
}
```

In `web/src/theme.config.ts`, replace the line `    mode: "system",` (inside `defaults`) with `    mode: "dark",`. Change nothing else in that file.

- [ ] **Step 4: Run the test to verify it passes**

Run: `timeout 180 pnpm -C web test`
Expected: `Test Files  1 passed (1)` and `Tests  1 passed (1)`.

- [ ] **Step 5: Run the lint target to verify it fails**

Run: `timeout 180 make lint-web`
Expected: FAIL — ESLint reports that it could not find an `eslint.config` file.

- [ ] **Step 6: Write the ESLint configuration**

`web/eslint.config.js`:

```js
import js from "@eslint/js"
import globals from "globals"
import reactHooks from "eslint-plugin-react-hooks"
import reactRefresh from "eslint-plugin-react-refresh"
import tseslint from "typescript-eslint"

export default tseslint.config(
  { ignores: ["dist"] },
  {
    files: ["**/*.{ts,tsx,js}"],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
    ],
    languageOptions: {
      globals: globals.browser,
    },
    plugins: {
      "react-refresh": reactRefresh,
    },
    rules: {
      "react-refresh/only-export-components": [
        "warn",
        { allowConstantExport: true },
      ],
    },
  },
)
```

- [ ] **Step 7: Run the web targets and the build**

Run: `timeout 900 make lint-web test-web build verify-build`
Expected: exit 0; `eslint .` and `tsc --noEmit` print no findings; Vitest `Tests  1 passed (1)`; `ok  	github.com/darkraise/darkpanel/web`.

- [ ] **Step 8: Commit**

```bash
git add web/eslint.config.js web/vitest.config.ts web/src/test/setup.ts web/src/app.test.tsx \
  web/src/app.tsx web/src/theme.config.ts
git commit -m "feat(web): add placeholder page, lint and tests"
```

### Task 7: Repository documents

**Files:**
- Create: `LICENSE`
- Create: `README.md`
- Create: `CONTRIBUTING.md`
- Create: `SECURITY.md`

**Interfaces:**
- Consumes: the make target names and binary paths (Contracts, "Make targets", "Build outputs").
- Produces: nothing later tasks name.

**Items:** 2, 4

**Depends on:** Task 3

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 0 = 3

- [ ] **Step 1: Run the content check to verify it fails**

```bash
grep -qF 'Copyright (c) 2026 Darkraise' LICENSE \
  && grep -qF 'make build' README.md && grep -qF 'bin/darkpanel-linux-arm64' README.md \
  && grep -qF 'make check' CONTRIBUTING.md && grep -qF 'docs/adr/' CONTRIBUTING.md \
  && grep -qF 'private vulnerability reporting' SECURITY.md && grep -qF '7 days' SECURITY.md \
  && echo docs-ok
```

Expected: no `docs-ok` (the files do not exist).

- [ ] **Step 2: Write `LICENSE`**

```
MIT License

Copyright (c) 2026 Darkraise

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

- [ ] **Step 3: Write `README.md`**

````markdown
# Darkpanel

Darkpanel is an MIT-licensed, single-binary VPS and self-hosted fleet manager,
with a CLI and a web UI, for owners of unmanaged Linux servers. It starts with
a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure
and safe self-update.

## Status

Pre-release, in milestone M1 (Foundations). The repository, build and CI exist;
the binary has only a `version` command and is not ready to run on a server.

## Prerequisites

- Go 1.27.2
- Node 24
- pnpm 11.17.0
- GNU make

## Build

```sh
make build
```

This builds the web UI, then two static Linux binaries with the UI embedded:

- `bin/darkpanel-linux-amd64`
- `bin/darkpanel-linux-arm64`

`bin/darkpanel-linux-amd64 version` prints the version the binary was built
from.

[CONTRIBUTING.md](CONTRIBUTING.md) lists the other make targets.
[SECURITY.md](SECURITY.md) explains how to report a vulnerability.

## License

MIT; see [LICENSE](LICENSE).
````

- [ ] **Step 4: Write `CONTRIBUTING.md`**

```markdown
# Contributing to Darkpanel

## Prerequisites

- Go 1.27.2. The build sets `GOTOOLCHAIN=local`, so it never downloads another
  Go toolchain.
- golangci-lint v2.15.0
- Node 24 and pnpm 11.17.0
- GNU make

## Make targets

| Target | What it runs |
|---|---|
| `make web` | installs the web dependencies from the lockfile and builds the UI into `web/dist/` |
| `make build` | `make web`, then static `linux/amd64` and `linux/arm64` binaries in `bin/` |
| `make verify-build` | checks that both binaries are static ELF for the right architecture and embed the UI |
| `make lint-go` | `golangci-lint run` and `go vet ./...` |
| `make lint-web` | ESLint and the TypeScript type check |
| `make lint` | `lint-go` and `lint-web` |
| `make test-go` | `go test ./...` |
| `make test-web` | the Vitest suite |
| `make test-adr` | the ADR checker's tests and the ADR structural check |
| `make test` | `test-go`, `test-web` and `test-adr` |
| `make vuln` | govulncheck v1.9.0 on every Go package |
| `make check` | `lint`, `test`, `build`, `verify-build` and `vuln` |
| `make clean` | removes `bin/` and the built UI |

`make build` is the one build command. CI runs these same targets.

## Before you open a pull request

Every change passes `make check`.

## Commit messages

Commit messages use `<type>(<scope>): <subject>`:

- `<type>` is one of `feat`, `fix`, `docs`, `style`, `refactor`, `test`,
  `chore`, `perf`;
- `<subject>` is imperative, at most 50 characters, with no trailing period.

## Decisions

Architecture decisions are recorded as ADRs in [docs/adr/](docs/adr/). An
accepted ADR changes only through a new ADR that supersedes it.

## Language

Code, comments, documentation, commit messages and error messages are written
in English.
```

- [ ] **Step 5: Write `SECURITY.md`**

```markdown
# Security policy

## Reporting a vulnerability

Report vulnerabilities through GitHub private vulnerability reporting on
[darkraise/darkpanel](https://github.com/darkraise/darkpanel/security/advisories/new).
Never report a vulnerability in a public issue.

A report should include:

- the affected version and commit (`darkpanel version` prints both);
- the host operating system and how Darkpanel was installed;
- the steps to reproduce it, and what an attacker gains;
- any proof of concept, logs or suggested fix.

You can expect an acknowledgement within 7 days.

## Supported versions

No version is supported before v1.0. From v1.0 on, the latest minor release
receives security fixes.
```

- [ ] **Step 6: Run the content check to verify it passes**

Run the command from Step 1.
Expected: `docs-ok`.

- [ ] **Step 7: Commit**

```bash
git add LICENSE README.md CONTRIBUTING.md SECURITY.md
git commit -m "docs(repo): add license, readme and policies"
```

### Task 8: Gates manifest

**Files:**
- Create: `docs/superpowers/gates.md`

**Interfaces:**
- Consumes: the make target names (Contracts, "Make targets").
- Produces: the six gates (Contracts, "Gates").

**Items:** 5

**Depends on:** Task 3

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 0 = 1

- [ ] **Step 1: Write the format check and run it to verify it fails**

Save nothing; run:

```bash
awk '
  /^## [0-9]+\. / { n++; split($2, a, "."); if (a[1] != n) { print "gate " n " is numbered " a[1]; bad = 1 } }
  /^Command: / { f[n, "c"] = 1 } /^Green: / { f[n, "g"] = 1 }
  /^Evidence: / { f[n, "e"] = 1 } /^Why: / { f[n, "w"] = 1 }
  END {
    for (i = 1; i <= n; i++) if (!f[i, "c"] || !f[i, "g"] || !f[i, "e"] || !f[i, "w"]) { print "gate " i " lacks Command, Green, Evidence or Why"; bad = 1 }
    if (n != 6) { print n " gates, want 6"; bad = 1 }
    if (!bad) print "gates-format-ok"
    exit bad
  }' docs/superpowers/gates.md
```

Expected: FAIL — awk exits non-zero because the file does not exist (its exact message depends on the awk implementation).

- [ ] **Step 2: Write `docs/superpowers/gates.md`**

```markdown
# Gates

What proves a change to Darkpanel works, in the order it must run. The
dr-superpowers running-gates skill reads this file. Every gate's command must
also exit 0.

## 1. ADR records
Command: `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete`
Green: the `check-adrs tests:` line reports `0 failed`, and no `check-adrs:` error line follows it
Evidence: output
Applies: always
Why: the ADRs are the decision record every later epic builds on; the checker catches a missing section or an untraced requirement

## 2. Lint
Command: `make lint`
Green: golangci-lint prints `0 issues.`, and ESLint and `tsc --noEmit` print no findings
Evidence: output
Applies: always
Why: CI runs the same lint targets; a finding caught here does not cost a CI round

## 3. Tests
Command: `make test`
Green: every Go package line starts with `ok`, the Vitest `Tests` line has no `failed`, and the ADR checker reports `0 failed`
Evidence: output
Applies: always
Why: the unit tests for the CLI, the embedded UI package, the web app and the ADR checker

## 4. Build
Command: `make build verify-build`
Green: `ok  	github.com/darkraise/darkpanel/web` after the `file` and UI-marker checks
Evidence: output
Applies: always
Why: the Go linker drops an embed.FS that no code reads, so a build can succeed with no UI in it; verify-build checks each binary for the UI

## 5. Vulnerabilities
Command: `make vuln`
Green: `No vulnerabilities found.`
Evidence: output
Applies: always
Why: govulncheck reports known vulnerabilities in the code the binaries actually reach

## 6. CI green
Command: `timeout 60 gh run list --branch "$(git branch --show-current)" --commit "$(git rev-parse HEAD)" --json conclusion --jq '.[0].conclusion'`
Green: `success`
Evidence: output
Applies: once the branch is pushed to origin
Why: only a clean runner shows that the build does not depend on the development host; the CI run on main is M1's evidence for "CI and gates green"
```

- [ ] **Step 3: Run the format check to verify it passes**

Run the command from Step 1.
Expected: `gates-format-ok`.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/gates.md
git commit -m "docs(gates): declare project gates"
```

### Task 9: CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: the make target names (Contracts, "Make targets"); `web/package.json` and `web/pnpm-lock.yaml` (Contracts, "Web package"); the action pins (Contracts, "CI actions").
- Produces: the `CI` workflow with jobs `adr`, `go`, `web`, `build` and the artifact `darkpanel-binaries` (Contracts, "CI actions").

**Items:** 3

**Depends on:** Task 3, Task 5

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 1 = 2

- [ ] **Step 1: Run the workflow lint to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml`
Expected: FAIL — actionlint reports that `.github/workflows/ci.yml` does not exist.

- [ ] **Step 2: Write `.github/workflows/ci.yml`**

```yaml
name: CI

on:
  push:
  pull_request:

permissions:
  contents: read

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

env:
  GOTOOLCHAIN: local

jobs:
  adr:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - run: make test-adr

  go:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - uses: golangci/golangci-lint-action@ba0d7d2ec06a0ea1cb5fa41b2e4a3ab91d21278a # v9.3.0
        with:
          version: v2.15.0
          install-only: true
      - run: make lint-go test-go vuln

  web:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: pnpm/action-setup@ea17c68df8912ef543352723c149a84f56e3d413 # v6.1.0
        with:
          package_json_file: web/package.json
      - uses: actions/setup-node@949feb2413d6458794dcd2491c4babbbce0c15c1 # v7.1.0
        with:
          node-version: 24
          cache: pnpm
          cache-dependency-path: web/pnpm-lock.yaml
      - run: pnpm -C web install --frozen-lockfile
      - run: make lint-web test-web

  build:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
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
      - run: make build verify-build
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: darkpanel-binaries
          path: bin/darkpanel-linux-*
          retention-days: 7
          if-no-files-found: error
```

- [ ] **Step 3: Run the workflow lint and the pin check**

Run: `GOTOOLCHAIN=local timeout 300 go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml && echo actionlint-ok`
Expected: `actionlint-ok` and no findings.

Run: `awk '/uses: / && $0 !~ /@[0-9a-f]{40} # v[0-9]/ { print "unpinned: " $0; bad = 1 } END { if (!bad) print "pins-ok"; exit bad }' .github/workflows/ci.yml`
Expected: `pins-ok`.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "chore(ci): add CI workflow"
```

### Task 10: Full verification, push and CI green

**Files:**
- None. This task changes no file and makes no commit.

**Interfaces:**
- Consumes: everything above; the gate 6 command (Contracts, "Gates").
- Produces: the pushed branch and its successful CI run.

**Items:** 1, 3, 5

**Depends on:** Task 6, Task 7, Task 8, Task 9

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 2 = 4

Run this task in the controller's checkout of the plan branch, never in a task worktree (Global Constraints). If any step below fails, stop and report BLOCKED with the failing command and its output (for a CI failure, the last 100 lines of `gh run view "$run" --log-failed`). Do not edit files in this task; the controller routes the fix.

- [ ] **Step 1: Run the full check from a clean build**

Run: `timeout 60 make clean && timeout 1200 make check`
Expected: exit 0, including `0 issues.`, Vitest `Tests  1 passed (1)`, `0 failed` from the ADR checker, `ok  	github.com/darkraise/darkpanel/web` and `No vulnerabilities found.`.

- [ ] **Step 2: Check the binaries**

Run: `file bin/darkpanel-linux-amd64 bin/darkpanel-linux-arm64`
Expected: `ELF 64-bit LSB executable, x86-64, … statically linked` and `ELF 64-bit LSB executable, ARM aarch64, … statically linked`.

Run: `grep -caF '<title>Darkpanel</title>' bin/darkpanel-linux-amd64 bin/darkpanel-linux-arm64`
Expected: a count of at least 1 for each file.

Run: `test "$(timeout 10 bin/darkpanel-linux-amd64 version --json | jq -r '.version + " " + (.ui | tostring)')" = "$(git describe --tags --always --dirty) true" && echo version-ok`
Expected: `version-ok`.

- [ ] **Step 3: Check the work tree is clean**

Run: `git status --short`
Expected: no output, or only the untracked `.superpowers/` directory, which belongs to the owner and must not be added.

- [ ] **Step 4: Turn on private vulnerability reporting**

`SECURITY.md` sends reporters to GitHub private vulnerability reporting, which is off on the repository.

Run: `timeout 60 gh api -X PUT repos/darkraise/darkpanel/private-vulnerability-reporting && timeout 60 gh api repos/darkraise/darkpanel/private-vulnerability-reporting`
Expected: `{"enabled":true}`.

- [ ] **Step 5: Push the branch**

Everything pushed is public (spec, Owner decisions).

Run: `branch=$(git branch --show-current) && test -n "$branch" && echo "$branch"`
Expected: the plan branch's name. An empty result means a detached HEAD: stop and report BLOCKED.

Run: `timeout 300 git push -u origin "$(git branch --show-current)"`
Expected: the branch is created on `origin`.

- [ ] **Step 6: Wait for the CI run and its result**

```bash
sha=$(git rev-parse HEAD)
timeout 180 bash -c 'until [ -n "$(gh run list --workflow ci.yml --commit "$0" --json databaseId --jq ".[0].databaseId")" ]; do sleep 10; done' "$sha"
run=$(timeout 60 gh run list --workflow ci.yml --commit "$sha" --json databaseId --jq '.[0].databaseId')
timeout 1800 gh run watch "$run" --exit-status
timeout 60 gh run view "$run" --json jobs --jq '.jobs[] | "\(.name) \(.conclusion)"'
timeout 60 gh api "repos/darkraise/darkpanel/actions/runs/$run/artifacts" --jq '.artifacts[].name'
```

Expected: `gh run watch` exits 0; the jobs print `adr success`, `go success`, `web success`, `build success`; the artifact list prints `darkpanel-binaries`.

- [ ] **Step 7: Run gate 6**

Run: `timeout 60 gh run list --branch "$(git branch --show-current)" --commit "$(git rev-parse HEAD)" --json conclusion --jq '.[0].conclusion'`
Expected: `success`.

- [ ] **Step 8: Hand `main` to finishing**

When the branch is `main`, this run is the M1 evidence and the task is done. When it is not, record in the task's report: "`main` not yet pushed: finishing the branch merges into `main`, pushes it (`git push origin main`), waits for that commit's CI run and re-runs gate 6 on `main` (spec §6)." Do not merge or push `main` in this task.
