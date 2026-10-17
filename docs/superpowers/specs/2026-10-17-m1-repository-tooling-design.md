# M1 epic: repository, tooling, CI and gates

**Date:** 2026-10-17
**Status:** design approved by the owner 2026-10-17
**Product spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Requirements:** R2, R3, R4
**Execution strategy:** parallel — the Go foundation and the web scaffold each own a dependency manifest and run alone; the repository documents, the gates manifest and the CI workflow share no files and run together (owner ruling 2026-10-17: favour parallel to reach v1 sooner).

## Goal

Turn the documentation-only repository into a buildable project: one Go module
that produces static `linux/amd64` and `linux/arm64` binaries with the web UI
embedded through one documented command (`make build`), CI that lints,
type-checks, tests and builds on every push, the repository's legal and
contribution files, and the project's gates. The walking-skeleton epic builds
the real UI shell, `serve` and `/healthz` on top of this; none of that is here.

## Owner decisions (2026-10-17)

- The web app starts from the `create-darkraise-ui` template, not a bare Vite
  app.
- The single build entry is a `Makefile`; CI calls its targets, so local runs
  and CI run the same commands (approach A).
- The plan pushes `main` to `github.com/darkraise/darkpanel` (public, empty
  today) so GitHub Actions runs; everything pushed is public.
- Go and golangci-lint are installed system-wide on the development host.

## Versions

Checked against go.dev, the Go module proxy and npm on 2026-10-17. Pins are
exact (no ranges) in `go.mod` and `web/package.json`.

| Tool or package | Version | Note |
|---|---|---|
| Go | 1.27.2 | `go 1.27`, `toolchain go1.27.2`; `GOTOOLCHAIN=local` (ADR 0001) |
| golangci-lint | v2.15.0 | |
| govulncheck (`golang.org/x/vuln`) | v1.9.0 | run with `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0` |
| `github.com/spf13/cobra` | v1.10.2 | ADR 0001 |
| Node | 24 | development host has 24.18.0 |
| pnpm | 11.17.0 | `packageManager` field in `web/package.json` |
| darkraise-ui | 6.9.6 | its i18next, recharts and react-i18next peers are optional and not installed |
| react, react-dom | 19.3.1 | |
| vite | 8.3.4 | |
| @vitejs/plugin-react-swc | 4.3.3 | |
| tailwindcss, @tailwindcss/vite | 4.3.3 | |
| typescript | 6.0.3 | not 7.x: typescript-eslint 8.71.2 requires `>=4.8.4 <6.1.0` |
| eslint | 10.13.0 | with @eslint/js 10.0.1 |
| typescript-eslint | 8.71.2 | |
| eslint-plugin-react-hooks | 7.1.1 | |
| eslint-plugin-react-refresh | 0.5.7 | |
| globals | 17.13.0 | |
| vitest | 5.0.5 | |
| jsdom | 30.1.3 | |
| @testing-library/react | 16.3.3 | |
| @testing-library/dom | 10.4.2 | peer of @testing-library/react |
| @types/react, @types/react-dom | 19.3.0 | |
| @types/node | 26.6.5 | |

## Design

### 1. Toolchain and Go foundation

- **Development host.** Go 1.27.2 is installed to `/usr/local/go` from
  `go1.27.2.linux-amd64.tar.gz`, verified against the SHA-256 that
  `https://go.dev/dl/?mode=json` lists. golangci-lint v2.15.0 is installed to
  `/usr/local/bin` from its GitHub release archive, verified against the
  release's `checksums.txt`.
- **`go.mod`:** module `github.com/darkraise/darkpanel`, `go 1.27`,
  `toolchain go1.27.2`, one direct dependency: cobra v1.10.2.
- **`cmd/darkpanel/main.go`:** a cobra root command `darkpanel` and a
  `version` subcommand. `darkpanel version` prints
  `darkpanel <version> (commit <commit>, <go version>, <GOOS>/<GOARCH>)`;
  `darkpanel version --json` prints
  `{"version":…,"commit":…,"go":…,"os":…,"arch":…}`. `version` and `commit` are
  package `main` variables defaulting to `dev` and `unknown`, set by the build
  with `-ldflags "-X main.version=… -X main.commit=…"`.
- **`web/embed.go`:** package `web` declares `//go:embed all:dist` and exports
  `var Dist embed.FS`. `web/dist/.gitkeep` is committed so the package compiles
  without a web build; every other file under `web/dist/` is ignored by git.
  Vite empties `dist/` before building, so the web build script re-creates
  `dist/.gitkeep` afterwards.
- **`Makefile`** (GNU make; `VERSION` defaults to
  `git describe --tags --always --dirty`, `COMMIT` to `git rev-parse --short HEAD`):

  | Target | Runs |
  |---|---|
  | `web` | `pnpm -C web install --frozen-lockfile` then `pnpm -C web build` |
  | `build` | `web`, then for `amd64` and `arm64`: `CGO_ENABLED=0 GOOS=linux GOARCH=<arch> go build -trimpath -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o bin/darkpanel-linux-<arch> ./cmd/darkpanel` |
  | `verify-build` | `file` reports both binaries as statically linked ELF for the right architecture; `DARKPANEL_REQUIRE_UI=1 go test ./web/` passes |
  | `lint-go` | `golangci-lint run` and `go vet ./...` |
  | `lint-web` | `pnpm -C web lint` and `pnpm -C web typecheck` |
  | `lint` | `lint-go lint-web` |
  | `test-go` | `go test ./...` |
  | `test-web` | `pnpm -C web test` |
  | `test-adr` | `bash scripts/check-adrs.test.sh` and `bash scripts/check-adrs.sh --complete` |
  | `test` | `test-go test-web test-adr` |
  | `vuln` | govulncheck on `./...` |
  | `check` | `lint test build verify-build vuln` |
  | `clean` | removes `bin/` and everything in `web/dist/` except `.gitkeep` |

  `make build` is the one documented build command (R2). Before the web
  scaffold exists (wave 1), the `web`-dependent targets are present but are run
  only from wave 2 on.
- **`.golangci.yml`** (version 2 format): the `standard` linters plus `gosec`,
  `misspell`, `revive` and `errorlint`; formatters `gofmt` and `goimports`.
- **Root `.gitignore`:** `bin/`, `web/node_modules/`, `web/dist/*` with
  `!web/dist/.gitkeep`, `coverage/`. **`.editorconfig`:** UTF-8, LF, final
  newline; tabs for Go and the Makefile, two spaces elsewhere.
- **Tests:** `cmd/darkpanel/version_test.go` runs the root command with
  `version` and `version --json` and checks the default `dev` output and the
  JSON keys. `web/embed_test.go` checks that `Dist` contains `dist/index.html`
  when `DARKPANEL_REQUIRE_UI=1`, and skips otherwise. The race detector is not
  used yet: it needs CGO and there is no concurrent code; it arrives with the
  agent runtime.

### 2. Web scaffold

- `web/` holds the files `create-darkraise-ui` 6.9.6 generates with
  `--layout sidebar --shell-style classic --accent blue --surface-color slate
  --density compact` (`index.html`, `vite.config.ts`, `tsconfig.json`,
  `src/main.tsx`, `src/app.tsx`, `src/env.d.ts`, `src/theme.config.ts`,
  `src/styles/globals.css`), without the `.git` directory, `.gitignore` and
  `package-lock.json` the generator creates.
- Changes from the generated files:
  - `package.json`: name `darkpanel-web`, `packageManager: pnpm@11.17.0`,
    `engines.node: ">=24"`, exact pins from the Versions table, scripts
    `dev` (`vite`), `build` (`tsc --noEmit && vite build && touch dist/.gitkeep`),
    `typecheck` (`tsc --noEmit`), `lint` (`eslint .`), `test` (`vitest run`).
    `pnpm-lock.yaml` is committed.
  - `index.html`: title `Darkpanel`; the Google Fonts links and the
    `/vite.svg` icon link are removed, so the embedded UI loads nothing from
    the internet. Self-hosted fonts are the walking skeleton's job.
  - `src/theme.config.ts`: `mode: "dark"`; the other generated defaults stay.
    The owner's direction A names a zinc surface, which the template does not
    offer; the walking skeleton decides it.
  - `src/app.tsx`: the placeholder text reads `Darkpanel` and
    `The walking skeleton replaces this page.`
  - New `eslint.config.js` (flat config): `@eslint/js` recommended,
    `typescript-eslint` recommended, `react-hooks` recommended,
    `react-refresh` `only-export-components` as a warning, browser globals,
    `dist/` ignored.
  - New `vitest.config.ts`: environment `jsdom`, setup file
    `src/test/setup.ts` that provides `window.matchMedia` (jsdom lacks it and
    the theme provider reads it).
  - New `src/app.test.tsx`: renders `<App />` and finds the heading
    `Darkpanel`.

### 3. Repository documents

- `LICENSE`: the MIT licence text, `Copyright (c) 2026 Darkraise`.
- `README.md`: one paragraph on what Darkpanel is (from the product spec's
  roadmap line), its status (pre-release, M1), prerequisites (Go 1.27.2,
  Node 24, pnpm 11.17.0, GNU make) and `make build`, with the output paths.
- `CONTRIBUTING.md`: prerequisites; the make targets and what each runs; the
  commit convention `<type>(<scope>): <subject>` (types feat, fix, docs,
  style, refactor, test, chore, perf; subject at most 50 characters,
  imperative); that every change passes `make check`; that decisions go
  through ADRs in `docs/adr/` (accepted ADRs change only through a new one);
  English only.
- `SECURITY.md`: report vulnerabilities through GitHub private vulnerability
  reporting on `darkraise/darkpanel`, never in public issues; supported
  versions: none before v1.0, then the latest minor release; what a report
  should include; acknowledgement target of 7 days.

### 4. Gates manifest

`docs/superpowers/gates.md`, in the format of dr-superpowers running-gates:

1. ADR records — `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete`
2. Lint — `make lint`
3. Tests — `make test`
4. Build — `make build verify-build`
5. Vulnerabilities — `make vuln`
6. CI green — `gh run list --branch <branch> --commit <sha> --json conclusion`
   shows `success` for the CI workflow; Applies: once the branch is pushed.

Each gate carries `Green`, `Evidence` and `Why` fields.

### 5. CI

`.github/workflows/ci.yml`:

- Triggers: `push` (all branches) and `pull_request`. `permissions:
  contents: read`. Concurrency group `ci-${{ github.ref }}` with
  cancel-in-progress.
- Runner `ubuntu-24.04`. Every action pinned by full commit SHA with the
  version in a trailing comment; the plan resolves the SHAs with `gh api`.
  Actions: `actions/checkout`, `actions/setup-go` (`go-version-file: go.mod`,
  env `GOTOOLCHAIN=local`), `pnpm/action-setup` (reads `packageManager`),
  `actions/setup-node` (Node 24, pnpm cache), `golangci/golangci-lint-action`
  (version v2.15.0, used only to install the binary; the plan confirms the
  action's install-only input), `actions/upload-artifact`.
- Every job runs `make` targets only:
  - `adr`: `make test-adr`.
  - `go`: `make lint-go test-go vuln`.
  - `web`: `pnpm -C web install --frozen-lockfile`, then `make lint-web test-web`.
  - `build`: `make build verify-build`; uploads `bin/darkpanel-linux-*` as the
    artifact `darkpanel-binaries`, kept 7 days.

### 6. Push

After the last wave merges into `main`, `main` is pushed to `origin` and the CI
run for the pushed commit must conclude `success` (gate 6). That run is the
evidence for M1's exit criterion "CI and gates green".

## Parallel decomposition

| Part | Wave | Owns | Consumes |
|---|---|---|---|
| Go foundation | 1 | `go.mod`, `go.sum`, `cmd/darkpanel/`, `web/embed.go`, `web/embed_test.go`, `web/dist/.gitkeep`, `Makefile`, `.golangci.yml`, `.gitignore`, `.editorconfig`; host toolchain install | this spec |
| Web scaffold | 2 | every other file under `web/` | the `Makefile` targets and `.gitignore` from wave 1 |
| Repository documents | 3 | `LICENSE`, `README.md`, `CONTRIBUTING.md`, `SECURITY.md` | Makefile target names |
| Gates manifest | 3 | `docs/superpowers/gates.md` | Makefile target names |
| CI workflow | 3 | `.github/workflows/ci.yml` | Makefile target names, `web/package.json` |
| Push | after wave 3 | nothing new | everything |

Wave 1 and wave 2 each touch a dependency manifest, so each runs alone. The
three wave-3 parts share no file and consume only names fixed in waves 1 and 2.
Tests use no ports, databases or fixed paths outside each worktree; Vitest and
`go test` share nothing.

## Verification

- `make check` passes on the development host.
- `file bin/darkpanel-linux-amd64 bin/darkpanel-linux-arm64` reports two
  statically linked ELF executables (x86-64 and ARM aarch64), and both embed
  `web/dist/index.html`.
- `bin/darkpanel-linux-amd64 version` prints the `git describe` version.
- The CI run on the pushed `main` commit concludes `success` with all four
  jobs.
- `docs/superpowers/gates.md` follows the running-gates format and every gate's
  command passes.

## Rejected alternatives

- CI with inline commands and a Makefile for the build only: CI and local runs
  drift, and the gates would restate CI's steps.
- A bare Vite app without darkraise-ui: the walking skeleton would add the kit
  and its theme setup later anyway.
- TypeScript 7.0.2: typescript-eslint does not support it yet.
- Taskfile or shell scripts as the build entry: one more tool to install, or no
  dependency handling between targets.

## Out of scope

`darkpanel serve`, `/healthz`, the UI shell, routing, self-hosted fonts and the
final theme (walking skeleton, R13 and R14); release builds, signing and
attestations (M8, ADR 0007); the race detector (with the agent runtime);
Dependabot or other dependency automation.
