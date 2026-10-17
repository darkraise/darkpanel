# ADR 0002: Module contract and compatibility

**Requirements:** R1, R5, R6, R7, R9, R24

## Status

Proposed

## Context

New features must ship as modules without breaking hosts that are already in
production. Agents of the previous minor version stay connected (R25), so the
controller and an older agent routinely run different code. Review finding 15
showed the risk: an "additive" field can be silently ignored by an older agent
while the action version stays the same, which is dangerous when the field is a
safety setting. The module contract and the compatibility rules must make that
impossible. Action names, classes, schemas and version skew are defined in the
shared contracts in the index (`docs/adr/README.md`) and are not restated here.

## Decision

- A module implements one Go interface returning a manifest: id, version,
  actions, resources, settings schema, preflight checks, owned paths and
  marked blocks (ADR 0009), uninstall step, and migrations per role
  (controller, agent). It may supply a controller part, an agent part, or both;
  the UI part is a route bundle registered in `web/src/modules/registry.ts`.
- Modules are compiled in and listed once in `internal/modules`; no runtime
  plugins in v1.
- An action declares name, version, typed input and output, class (shared
  contracts), where it runs (controller or agent), whether it is break-glass
  (a controller-side action that the controller's local socket also serves,
  ADR 0006), and the lock keys it takes (ADR 0004).
- A module never changes another module's resources directly; it calls the
  owning module's actions (for example DERP opens ports through
  `firewall.rule.add`), so locking, snapshots and audit stay with the owner.
- Enablement and preflight results are stored per host in
  `core_module_state`. A disabled module, or one whose migration or preflight
  fails, is marked unavailable with the reason; the rest of the system starts.
- Compatibility rules, enforced by a test against the schema snapshots of every
  released tag, which the release job copies to `schemas/released/<tag>/`
  (immutable once released):
  - Allowed in a published version: new optional input fields, new output
    fields, new output enum values (clients must accept unknown values).
  - Forbidden: removing or renaming a field, changing a type, a default or a
    constraint, adding a required input field, removing an enum value, or
    changing an action's meaning or class.
  - A breaking change is a new version; the old version stays until the next
    major release.
- Agents advertise, per action version, the hash of the schema they implement.
  The controller refuses to send an input field the agent's schema does not
  contain, so an older agent can never silently ignore a newer safety field.

## Alternatives considered

- Runtime plugins (Go plugins, subprocess plugins, WebAssembly): rejected for
  v1. Go plugins require identical toolchains, subprocess plugins add a second
  protocol, and none is needed while all modules live in this repository.
- Protobuf instead of JSON Schema: rejected, because it adds a second schema
  system beside the JSON API.
- Comparing schemas only against the current commit: rejected, because a change
  could be made and reverted between releases. The baseline is therefore the
  snapshot of every released tag.

## Consequences

- Adding a module touches the core in one registration line.
- Every action needs a committed schema snapshot; the release job freezes them
  under `schemas/released/<tag>/`.
- The compatibility test runs in CI from M1.
- The controller must keep per-agent schema hashes from `hello` (ADR 0003).
- Cross-module effects go through the owning module's actions; M3, M5 and M6
  rely on this.
