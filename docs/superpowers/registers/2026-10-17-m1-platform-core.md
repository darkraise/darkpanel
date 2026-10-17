# M1 platform core register

**Source:** M1 roadmap epics "Module framework" (R5-R12), "Walking skeleton" (R13, R14), "Identity, sessions and audit" (R15-R20), "Integration test harness" (R21); tooling register rows 8, 10, 11, 12
**Covers:** docs/superpowers/specs/2026-10-17-m1-platform-core-design.md

| # | Item | Assigned | Acceptance | State | Note |
|---|---|---|---|---|---|
| 1 | R5 a module declares id, version, actions, read resources, settings, migrations and preflight through one Go interface; adding a module touches the core in one registration line | docs/superpowers/plans/2026-10-17-m1-platform-foundation.md | hostinfo registered by one line in internal/modules/modules.go; registry validation tests pass | planned | - |
| 2 | R6 every action has a stable versioned name and JSON Schemas; published versions change only additively | - | schema snapshot test and compatibility fixture tests pass | open | - |
| 3 | R7 every registered action is exposed through REST, CLI and audit with no module-specific glue | - | CLI parity test passes; end-to-end test reads hostinfo through API and CLI | open | - |
| 4 | R8 per-module tables and forward-only migrations, backup before applying | docs/superpowers/plans/2026-10-17-m1-platform-foundation.md | store tests: ordering, checksum refusal, backup taken only when pending, module failure isolated | planned | - |
| 5 | R9 modules enabled or disabled per host; a failing module never blocks others | - | core.module enable/disable and preflight tests; unavailable module answers module.unavailable | open | - |
| 6 | R10 change lifecycle: plan, apply, verify, confirm-or-revert; snapshots; compensable and irreversible classes; typed confirmation | - | lifecycle fixture-module tests for every class, recovery and races pass | open | - |
| 7 | R11 writes only manifest-listed files and marked blocks; argument vectors, never a shell | - | platform exec and files tests, including symlinked parent and invalid markers | open | - |
| 8 | R12 third-party credentials encrypted at rest with a per-role 0600 key and never returned by the API | - | secret package tests; secret output returned once and redacted in audit | open | - |
| 9 | R13 darkpanel serve with embedded UI, /healthz, and host-information module end to end in UI, API and CLI | - | end-to-end test and manual Playwright check pass | open | - |
| 10 | R14 the web UI registers module pages through a route registry | - | registry-driven tabs test; hostinfo added by one registry line | open | - |
| 11 | R15 argon2id passwords, mandatory TOTP, first admin by CLI on the host, no default credentials or setup page | - | auth tests; end-to-end test creates the first admin through the controller socket | open | - |
| 12 | R16 secure cookies, CSRF, rate-limited login, deny-by-default middleware for every method and route | - | end-to-end test checks every non-public route and method refuses without a session; CSRF and rate-limit tests | open | - |
| 13 | R17 CLI and automation authenticate with scoped, expiring API tokens | - | end-to-end test runs the CLI with a token; scope tests | open | - |
| 14 | R18 every mutating action writes an audit entry viewable in UI and CLI | - | end-to-end test finds login and token creation in darkpanel audit list | open | - |
| 15 | R19 user records carry a role field; only admin in v1 | - | users table and auth tests | open | - |
| 16 | R20 web UI listens on 127.0.0.1 and the Tailscale interface by default; other binds need explicit configuration | - | listener tests; harness scan finds only 22/tcp open | open | - |
| 17 | R21 harness boots Ubuntu 22.04, 24.04, Debian 12, 13 VMs, installs the build, tests real UFW, Docker and tailscaled, scans from an external VM | - | harness.yml green on main for all four OS (gate 7) | open | - |
| 18 | Tooling rows 8, 10, 11, 12: web-deps target and verify-build prerequisite; import.meta.dirname; pre-paint script file with dark fallback; type-check vite and vitest configs | - | make check passes on a fresh clone; no Vite __dirname warning; tsc covers both configs | n/a | split into row 27 (tooling row 8, foundation plan) and row 28 (tooling rows 10-12, web plan) |
| 19 | ADR 0010 web certificate authority (ECDSA P-256 root for web certificates) | docs/superpowers/plans/2026-10-17-m1-platform-foundation.md | check-adrs --complete passes; Status Accepted when this epic closes | planned | - |
| 20 | Server-sent events /api/v1/events (ADR 0006) | - | - | deferred | M3 Firewall safe apply (revert countdowns) |
| 21 | Web certificate from tailscale cert through the agent (ADR 0006) | - | - | deferred | M5 Tailscale node management |
| 22 | Schema-hash capability negotiation in hello (ADR 0002, R24) | - | - | deferred | M2 Controller-agent fleet |
| 23 | Agent socket and agent break-glass commands (R31) | - | - | deferred | M2 Install, service and uninstall |
| 24 | Separate darkpanel and root service users and directories | - | - | deferred | M2 Install, service and uninstall |
| 25 | User management and self-service password change in the UI | - | - | deferred | M9 roles candidate; break-glass commands cover M1 |
| 26 | Guard overrides on apply (R40) | - | - | deferred | M3 Firewall safe apply |
| 27 | Tooling row 8: a web-deps target that runs pnpm -C web install --frozen-lockfile, prerequisite of web, lint-web and test-web; verify-build depends on build | docs/superpowers/plans/2026-10-17-m1-platform-foundation.md | make check passes on a fresh clone; make verify-build alone builds first | planned | - |
| 28 | Tooling rows 10, 11, 12: vite.config.ts uses import.meta.dirname; pre-paint script in web/public/theme-init.js with a dark fallback; tsconfig.node.json and a typecheck script covering the vite and vitest configs | - | no Vite __dirname warning; tsc covers both configs; the pre-paint script loads with a plain script src | open | - |
| 29 | ADR 0011 actors, reserved CLI names and package layout | docs/superpowers/plans/2026-10-17-m1-platform-foundation.md | check-adrs --complete passes; Status Accepted when this epic closes | planned | - |
