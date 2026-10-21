# ADR 0001: Stack, layout and process model

**Requirements:** R2, R13, R14, R26

## Status

Accepted

## Context

One Go binary must hold the CLI, the controller, the agent and the embedded web
UI, and it must run on `linux/amd64` and `linux/arm64` (product spec
Constraints). The controller manages its own host through the same agent path
as every other host (R26). Reviewers flagged two points as undecided: which
user the controller runs as, and how the Go toolchain version is pinned.

The shared contracts (names, schemas, action classes, errors, actors, version
skew) are defined in the index (`docs/adr/README.md`) and are not restated
here.

Every later epic places files and code according to the decisions here, so the
stack, the process model and the directory layout need to be fixed before any
module work starts.

## Decision

- Go 1.27 with `go 1.27` and `toolchain go1.27.2` in `go.mod`; CI sets
  `GOTOOLCHAIN=local` on the exact version. CGO disabled; `-trimpath`; one
  binary for `linux/amd64` and `linux/arm64`.
- HTTP with the standard library router; CLI with `github.com/spf13/cobra`.
- Web UI: Vite, React 19, darkraise-ui 6.x, TanStack Query, built to `web/dist`
  and embedded with `go:embed`; pnpm.
- Process model: two systemd services from one binary. `darkpanel-controller`
  runs as an unprivileged `darkpanel` user (it never touches the host
  directly). `darkpanel-agent` runs as root on every managed host, including
  the controller's host, where it connects over loopback like any other agent.
  In M1, before the transport exists, the controller calls the agent handler
  in-process, and in development mode both roles use the invoking user's
  paths; the separate users and directories (ADR 0005) apply from the M2
  install.
- Layout:

```
cmd/darkpanel/            entry point, role dispatch (serve, agent, CLI, update)
internal/core/            registry, actions, lifecycle, audit, errcode, schema checks
internal/controller/      HTTP API, auth, database, fleet, scheduler, CA
internal/agent/           agent runtime, local socket, journal, revert timers
internal/transport/       TLS, enrollment, yamux
internal/platform/        providers: exec, files, systemd, OS detection (ADR 0009)
internal/update/          updater, transaction, recovery
internal/modules/<id>/    manifest, controller part, agent part, migrations
schemas/<action>/v<n>.json committed action schema snapshots
schemas/released/<tag>/   frozen snapshots per release (ADR 0002)
web/src/modules/<id>/     module UI pages
docs/adr/                 decision records
```

## Alternatives considered

- Controller running as root: rejected. It never needs to touch the host
  directly, and the CyberPanel CVE-2024-51567 compromise ran with full
  privileges.
- Agent inside the controller process on the controller host as the permanent
  design: rejected from M2 on. A controller crash or update would stop pending
  firewall reverts (owner ruling 2026-10-17). The in-process call in M1 is a
  temporary step before the transport exists, as the decision text says.
- A web framework router (chi, gin): rejected. The standard library router
  covers the routes in ADR 0006 with one fewer dependency.
- A `toolchain` directive alone: rejected. It allows newer toolchains, so
  builds would differ between machines.

## Consequences

- Each controller host runs two systemd units.
- The agent is the only long-running root service. Root CLI invocations reach
  it through its local socket (ADR 0006).
- Host-touching code lives only in agent parts, which reach the host through
  `internal/platform` (ADR 0009).
- Every later epic places its files according to the layout above.
- CI must set `GOTOOLCHAIN=local` on the exact toolchain version.
