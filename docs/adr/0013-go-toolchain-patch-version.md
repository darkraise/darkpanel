# ADR 0013: Go toolchain patch version

**Requirements:** R2

## Status

Proposed

## Context

ADR 0001 pins `toolchain go1.27.2` in `go.mod`. On 2026-10-21 govulncheck
(gate 5) reported GO-2026-6809 in `crypto/x509` on go1.27.2, reached from the
agent's enrollment check, which calls `x509.Certificate.Verify`. Go 1.27.3
fixes it. Accepted ADRs are not edited, and a new ADR for every Go security
release would add a record without adding a decision.

## Decision

- The `toolchain` line in `go.mod` is the single source of the Go patch
  version. It names go1.27.3 from 2026-10-21. ADR 0001 keeps the minor version
  (`go 1.27`), `GOTOOLCHAIN=local`, and the rule that CI and the development
  host build with exactly the pinned toolchain.
- A Go patch release inside 1.27 is adopted by changing the `toolchain` line,
  the README and CONTRIBUTING prerequisites, and the development host's
  installed Go together, without a new ADR. A move to another minor version
  needs a new ADR.

## Alternatives considered

- Editing ADR 0001 for each patch release: rejected. The ADR lifecycle forbids
  editing an Accepted ADR.
- A new ADR for each patch release: rejected. A patch release changes no
  decision, so the record would only repeat `go.mod`.
- Leaving go1.27.2 until the next planned upgrade: rejected. Gate 5 is red
  while called code is affected by a known vulnerability.

## Consequences

- `go.mod`, not ADR 0001, says which Go patch version builds Darkpanel.
- The development host must be upgraded in the same change as `go.mod`, or
  every local build fails under `GOTOOLCHAIN=local`.
- Specs written before this ADR that name go1.27.2 describe the version at the
  time they were written.
