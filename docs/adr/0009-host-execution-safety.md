# ADR 0009: Host execution safety

**Requirements:** R11, R30, R54

## Status

Proposed

## Context

The agent runs as root (ADR 0001), so anything it executes or writes on the
host has full privilege. Review finding 11 showed that argument vectors alone
are not enough: option injection, PATH lookup and abuse of the inherited
environment remain possible.

File ownership was also not enforced, so nothing guaranteed that the files a
module wrote could later be identified and removed.

Coolify's January 2026 advisories showed the same class of failure in
practice: user-supplied strings reached shell commands that ran as root.

See the shared contracts in the index (`docs/adr/README.md`) for names,
schemas and errors.

## Decision

- **Commands.** The exec provider runs only binaries from an allowlist of
  absolute paths, with a fixed minimal environment, a timeout from the caller's
  context, and argument vectors only; values that could be read as options are
  separated with `--` where the tool supports it. Input schemas constrain
  values with patterns (ports, names, CIDRs, comments) before they reach a
  provider.
- **Files.** The file provider writes only paths and marked blocks declared in
  the module manifest. It opens the parent directory with
  `openat2(RESOLVE_NO_SYMLINKS)`, which fails if any path component is a
  symlink, and requires that directory to be owned by root and not writable by
  others. It then creates a temporary file and renames it over the target
  relative to that directory descriptor, preserving the target's mode and
  owner. A file with duplicated, nested or unterminated `BEGIN/END DARKPANEL`
  markers is refused, never repaired by guessing. Ownership is recorded in the
  ownership manifest that `darkpanel uninstall` reverses (R30). Declared paths
  are fixed, so this defends against a symlink planted by an unprivileged
  process, not against root. `openat2` needs Linux 5.6; every supported host
  ships 5.15 or later.
- Prefer APIs over tools: Docker Engine API, Tailscale local API, systemd over
  D-Bus.

## Alternatives considered

- Running commands through `sh -c`: rejected. Shell parsing of user-supplied
  values is the injection class described in the context.
- Parsing command-line tool output where an API exists (`docker ps`): rejected.
  Output formats change between versions, and R54 requires the Docker Engine
  API.
- Unrestricted file writes from module code: rejected. Uninstall could not
  reverse them, and a symlink could redirect a root write.
- `O_NOFOLLOW` alone: rejected. It refuses a symlink only as the last path
  component and follows a symlinked parent directory.

## Consequences

- `internal/platform` is the only package that runs programs or writes host
  files.
- Every module declares its owned paths and marked blocks in its manifest
  (ADR 0002).
- The exec provider's allowlist of absolute binary paths lives in
  `internal/platform`.
- Input schemas carry patterns for every value passed to a program.
- The ownership manifest drives `darkpanel uninstall`.
