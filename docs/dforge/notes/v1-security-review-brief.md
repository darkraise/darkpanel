# Darkpanel v1 security review — shared brief

You are one of two independent security reviewers of the Darkpanel repository at
`/root/repositories/darkpanel`, branch `main`, commit `dc91aa2`. Darkpanel is a
self-hosted server control panel: a Go controller (web UI, REST API, SQLite) manages
root-run Go agents over mutual TLS; modules manage UFW, Docker, Tailscale, a tailnet,
a DERP relay, Nginx Proxy Manager (NPM), Cloudflare Tunnel and exposure of the panel
itself. This review is requirement R73 of the v1 release: "a security review of
authentication, the agent protocol, command execution and secret handling, with no
open critical or high findings". Every critical and high finding blocks the release.

## Read first

1. `docs/security/threat-model.md` — the written threat model. Its `## Areas` are the
   review scope. Its `## Known residual risks` are seven open design questions.
2. `docs/security/parsers.md` — the parser inventory and fuzz targets.
3. As needed: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (the design; use
   `rg` on it, it is 6,000 lines) and `docs/runbook.md`.

## What to do

1. **Hunt for vulnerabilities** in each area of the threat model: authentication
   (`internal/controller/auth`, `internal/controller/api`), the agent protocol
   (`internal/transport`, `internal/controller/fleet`, `internal/agent`), command
   execution (`internal/platform`, especially the exec rules and file provider),
   Compose execution (`internal/modules/docker`), secret handling and delivery
   (`internal/core/secret`, reveal paths, logs masking, `internal/controller/backup`),
   the firewall (`internal/modules/firewall`), cloud credentials
   (`internal/controller/cloud`, `internal/modules/tailnet`, `internal/modules/cloudflare`),
   exposure (`internal/controller/listen`, `internal/modules/panel`, `internal/modules/npm`),
   the DERP host (`internal/modules/derp`), and the update path (`internal/update`,
   `cmd/darkpanel`). The web UI (`web/src`) matters where it handles secrets or could
   be a script-injection sink.
2. **Check the mitigation claims.** Each bullet under `#### Mitigations` states what the
   code does. A claim the code does not hold is a finding, graded by what the gap
   allows.
3. **Rule on the seven Known residual risks.** For each, give a recommendation: fix
   (and the concrete fix) or accept (and why the risk is acceptable for v1), with a
   proposed severity if it were left unfixed.

Prior findings live in `docs/dforge/registers/2026-10-21-v1-completion.md`. Before
reporting a finding, `rg` that register for the file or symbol; if a row already
records it, cite the row number and say whether its recorded state or severity is
wrong. Do not spend effort re-reporting rows marked `done`.

Out of scope: everything the threat model's `## Scope` lists as not covered (a root
attacker on a host, physical access, a malicious admin, the security of Tailscale,
Cloudflare, Let's Encrypt and Docker themselves).

## Severity

- **critical** — an unauthenticated network attacker gains code execution, an
  authenticated session, or sealed secrets; or any path to root on a managed host
  for someone who is not an admin.
- **high** — an authenticated lower scope (`read`, `operate`) reaches an `admin`
  effect or a secret; a CSRF or script-injection path to a state change; an
  unprivileged local user redirects a root write or read; a secret reaches a log,
  audit row, journal, error or process list; the lockout guard or the automatic
  revert can be bypassed without the explicit override; a signature or checksum check
  on an update or artifact can be skipped.
- **medium** — a real weakness that needs an unlikely precondition, or whose impact is
  limited to availability or to information that is not a secret.
- **low** — defence in depth, hardening, or a claim in the threat model that is
  inaccurate without being exploitable.

## Rules

- Read-only. Do not edit, create or delete any file in the repository. Do not run
  builds, tests or network commands. Reading files and `rg`/`git log`/`git show` is
  fine.
- Report only what you have read in the code. Mark each finding **confirmed** (you
  traced the path in the code) or **plausible** (a gap you could not fully trace),
  and give the file and line.
- Report every finding you have evidence for, at every severity. Do not filter.

## Output format

Write one Markdown document with exactly these sections:

```
# Security review — <your reviewer name>

## Findings

### F1: <one-line title>
- Severity: critical | high | medium | low
- Confidence: confirmed | plausible
- Area: <threat-model area>
- Location: `path/to/file.go:123` (and others)
- Prior row: <register row number, or none>
- Description: <what is wrong>
- Failure scenario: <concrete actor, inputs and steps, and what they gain>
- Fix: <the concrete change>

### F2: ...

## Mitigation claims not held
- <area> — "<claim, quoted>" — <what the code does instead> — finding F<n> or "no impact"

## Known residual risks
- `Origin`: fix | accept — <reason; the concrete fix> — severity if unfixed: <level>
- `RFC 1918`: ...
- `forward host`: ...
- `port 81`: ...
- `port 3000`: ...
- `digest`: ...
- `openpgp`: ...

## Coverage
- <areas and packages read in depth; anything not reached>
```
