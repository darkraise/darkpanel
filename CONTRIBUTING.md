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
| `make schemas` | regenerates the action and resource schema snapshots in `schemas/` |
| `make vuln` | govulncheck v1.9.0 on every Go package |
| `make check` | `lint`, `test`, `build`, `verify-build` and `vuln` |
| `make clean` | removes `bin/` and the built UI |

`make build` is the one build command. CI runs these same targets.

## Run a development server

`--dev` keeps everything under your own user: the databases and keys in
`$XDG_STATE_HOME/darkpanel-dev/` (`~/.local/state/darkpanel-dev/` by default)
and the controller socket in `$XDG_RUNTIME_DIR/darkpanel-dev/`. In `--dev` the
socket also accepts your own uid, so the break-glass commands need no `sudo`.

```sh
make web    # once, so the binary embeds the UI
go run ./cmd/darkpanel serve --dev --listen loopback
```

In another terminal, create the first admin through the socket and print the
web root:

```sh
go run ./cmd/darkpanel user create --username admin --dev
go run ./cmd/darkpanel controller web-ca --dev
```

Then create an API token for the CLI as the README's "Use the CLI with a
token" shows. A second `serve` on the same paths
fails while the first runs; stop the first with Ctrl-C. Delete
`~/.local/state/darkpanel-dev/` to start again from nothing.

## Schemas

Every action and resource has a committed schema snapshot under `schemas/`.
After changing an action's or resource's input or output type, run
`make schemas` and commit the result; `go test ./internal/modules/` fails while
a snapshot is missing or out of date. `schemas/released/<tag>/` will hold the
snapshots of released versions, which a change may only extend (ADR 0002).

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
