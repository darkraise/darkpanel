# ADR 0006: API, listeners, authentication and CLI

**Requirements:** R1, R7, R12, R15, R16, R17, R20, R28, R31, R72

## Status

Proposed

## Context

Darkpanel is private by default (R20), and every action available in the UI
must also be available in the CLI (S4). An operator must also keep break-glass
access on the host itself (R31, as amended on 2026-10-17).

Review findings 7, 17, 18, 22, 25, 28 and 32 shape this decision:

- CyberPanel CVE-2024-51567 was exploited through method-dependent
  authentication, so the boundary must not depend on the HTTP method.
- Break-glass commands did not fit the agent socket.
- The standard router rejects `v{n}:plan` style paths.
- Web TLS was undecided.
- The generated CLI lacked rules for input, secrets and confirmation.

## Decision

- **Listeners.** Web and API: 127.0.0.1 and the Tailscale interface by default,
  HTTPS with a `tailscale cert` certificate when the tailnet has HTTPS enabled,
  otherwise a certificate from the internal authority; plain HTTP only on
  127.0.0.1. Agent listener: ADR 0003, no web routes. Local sockets: below.
- **Authentication boundary.** One middleware wraps every route on every
  listener and denies unless the route is in an explicit public list (login,
  health). It applies to every HTTP method. Sessions use Secure, HttpOnly,
  SameSite=Strict cookies with CSRF tokens; API tokens are scoped
  (read, operate, admin) and expire. Behind Nginx Proxy Manager or a tunnel,
  forwarded headers are trusted only from configured proxy addresses.
- **REST** under `/api/v1`: hosts; resources; actions at
  `/api/v1/hosts/{host}/actions/{name}/{version}/plan` and `.../apply`,
  controller-side actions at `/api/v1/actions/{name}/{version}/plan` and
  `.../apply`; `/api/v1/changes/{id}/confirm` and `/revert`; audit; auth.
  (`{version}` is a whole path segment such as `v1`, which the standard router
  requires.)
- Live updates over server-sent events at `/api/v1/events`; OpenAPI 3.1 at
  `/api/v1/openapi.json`, generated from the registry; `/api/v1` is additive
  only (ADR 0002).
- **CLI**, generated from the registry: `darkpanel <module> <noun> <verb>`.
  Scalar inputs are flags; nested inputs come from `--input <file|->` as JSON;
  secret fields are prompted or read from stdin, never accepted as flags. Read
  actions print tables or `--json`. Mutating actions show the plan, then ask;
  `--yes` answers that question only. A revertible change is confirmed only by
  `--confirm` or by `darkpanel change confirm <id>`. Top-level names `serve`,
  `agent`, `update`, `user`, `controller`, `change`, `uninstall`, `version`
  are reserved.
- **Local access.** The agent serves its handler on `/run/darkpanel/agent.sock`
  (root, 0600) for every agent action, checked with `SO_PEERCRED` uid 0,
  with actor `local:root@<host id>`; entries are journaled and reach the audit
  log when the controller is reachable (R28). The controller serves
  `/run/darkpanel/controller.sock` (0600, `darkpanel`, root allowed via
  `SO_PEERCRED`) for controller break-glass commands such as
  `darkpanel user reset-totp`. `darkpanel update rollback` needs neither
  service (ADR 0007).

## Alternatives considered

- `v1:plan` style custom methods in paths: rejected; the standard library
  router needs whole-segment wildcards.
- WebSockets for live updates: rejected; server-sent events are one-way, which
  is all the UI needs, and work through proxies.
- Accepting secrets as CLI flags: rejected; they leak into shell history and
  process lists.
- File mode alone for socket access: rejected; `SO_PEERCRED` also identifies
  the actor for audit.

## Consequences

Every route on every listener sits behind the one authentication middleware,
so a new handler is denied unless it is added to the explicit public list. The
controller needs a certificate source (tailnet or internal authority). Trusted
proxy addresses are configuration that M6 panel exposure sets. Reserved
top-level names constrain module ids.
