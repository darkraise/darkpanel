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

Every change passes `make check`. On a fresh clone, run
`pnpm -C web install --frozen-lockfile` first: `make check` runs the web lint
and tests before `make build` installs the web dependencies.

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
