# Darkpanel — product design

**Date:** 2026-10-17
**Status:** approved by the owner 2026-10-17
**License:** MIT

## 1. Problem

People running unmanaged VPSes and self-hosted servers manage them with a mix of
SSH sessions, hand-edited config files and single-purpose tools. Three problems
recur:

1. **Docker silently bypasses UFW.** A container published with `-p 8080:80` is
   reachable from the internet even when UFW denies 8080, because Docker inserts
   its own iptables rules ahead of UFW's. It is the most-asked firewall question
   in r/selfhosted. No existing panel enforces a fix; Dokploy, CapRover and
   Coolify only document it, and 1Panel closed its attempt unmerged (PR #12704,
   2026-05-10).
2. **Firewall changes are dangerous on remote hosts.** One wrong rule locks the
   owner out of SSH, and no panel offers preview, auto-revert or rollback.
3. **Existing panels are heavy, risky or untrusted.** Hosting panels (HestiaCP,
   CyberPanel, aaPanel) take over the whole host; PaaS tools (Coolify, Dokploy)
   impose their own proxy and deploy model; several have had mass-exploited
   vulnerabilities (CyberPanel CVE-2024-51567, 1Panel CVE-2025-54424, Coolify's
   eleven critical injection flaws in January 2026); licensing has drifted
   toward source-available (Portainer, Cosmos, Umbrel, Dockhand).

Tailscale users have a fourth gap: every Tailscale UI targets self-hosted
Headscale, and nothing manages custom DERP relay servers for hosted Tailscale.

## 2. Market summary

Research date 2026-10-17; star counts from the GitHub API on that date.

| Category | Examples | What they miss for this audience |
|---|---|---|
| Hosting panels | 1Panel (37k), HestiaCP, CyberPanel, aaPanel, CloudPanel, Webmin | Own the whole host; no Docker-aware firewall; history of critical CVEs |
| PaaS | Coolify (63k), Dokploy (38k), CapRover, Dokku, Easypanel | App deploy, not host management; leave Docker/UFW to the operator |
| Docker UIs | Portainer (39k), Dockge, Komodo, Arcane, Dockhand | Containers only; no firewall, no host networking |
| Home server OS/app stores | CasaOS, Umbrel, Runtipi, YunoHost, Cosmos | Home-lab appliances; CasaOS stalled since 2025-04 |
| General admin | Cockpit, Ajenti, Webmin | No Docker/UFW integration, no fleet model |
| Firewall tools | chaifeng/ufw-docker (6.8k, GPL-3.0, shell), udwall, NiftyWall | CLI scripts or replace UFW; no UI, no fleet |
| Tailscale UIs | Headplane, headscale-ui | Headscale only; nothing for DERP or hosted Tailscale |

**Gaps Darkpanel will own:** Docker-aware firewall enforcement with an exposure
view; safe firewall changes (preview, lockout guard, auto-revert, snapshots);
DERP lifecycle for hosted Tailscale; private-by-default access; full CLI
parity; verified self-update with rollback; a small MIT-licensed Go codebase
with no telemetry that touches only files it owns.

**Docker firewall landscape to design for:**

- Docker 28 hardened container networking (unpublished ports blocked,
  localhost-published ports protected via the `raw` table).
- Docker 29 added an experimental nftables firewall backend that creates no
  `DOCKER-USER` chain and does not support Swarm. Docker states nftables will
  become the default. A `DOCKER-USER`-only integration therefore has a limited
  lifespan: Darkpanel detects the backend and keeps the integration behind an
  interface so an nftables strategy can be added later.

## 3. Users and top tasks

**Primary user:** a technical owner of one to a few dozen Linux VPSes or home
servers running Docker workloads, often joined by Tailscale. Comfortable with
SSH, but wants safe, visible, repeatable management from one place.

Top tasks:

1. See what each host exposes to the internet, Docker included, and close what
   should not be open.
2. Open a port or expose a container without risking lockout.
3. Start, stop, update and read logs of containers and Compose projects.
4. Keep hosts joined to the tailnet, approve routes, and run a private DERP
   relay.
5. Publish a service through Nginx Proxy Manager or a Cloudflare Tunnel.
6. Keep Darkpanel itself updated across the fleet without breaking anything.

## 4. Success measures

| # | Measure | How it is checked |
|---|---|---|
| S1 | No firewall change can lock the owner out | Integration tests: every change that would cut the current SSH session, the panel path or the agent link is refused; an unconfirmed change reverts within its timeout, including when the client disconnects |
| S2 | Exposure view is truthful | In the integration harness, the computed exposure for each host equals an external port scan, IPv4 and IPv6, zero ports reported closed that are open |
| S3 | UFW governs Docker | A container's published port is unreachable from the external scanner unless a Darkpanel route rule allows it, on every supported OS |
| S4 | One UI for the fleet, CLI parity | A test enumerates the action registry and fails if any mutating action lacks a CLI command |
| S5 | Upgrades never break | Automated: upgrade from every prior v1.x release succeeds; a deliberately broken release rolls back automatically within 2 minutes; an agent one minor version behind works with the newest controller |
| S6 | Small footprint | Agent idle resident memory ≤ 40 MB; controller ≤ 150 MB managing 10 hosts (proposed budget, owner may adjust) |
| S7 | Production-proven | Runs the owner's production VPS plus at least one agent for 14 days with no manual repair |

## 5. Non-goals for v1

- Web hosting stacks: PHP runtimes, mail servers, DNS servers, FTP.
- PaaS features: git-push deploys, build pipelines, an app store.
- Kubernetes and Docker Swarm.
- Headscale (hosted Tailscale only; the control-plane client sits behind an
  interface so Headscale can be added).
- RHEL-family hosts, firewalld, non-systemd hosts, Windows or macOS hosts.
- Docker's nftables firewall backend: detected and reported as unsupported.
- Roles beyond admin, per-host permissions, SSO/OIDC.
- Web terminal and file manager.
- Metrics history, uptime monitoring and alerting.
- Telemetry of any kind, ever.

## 6. Constraints

- **Stack:** Go, single static binary (CGO off) containing CLI, controller,
  agent and the embedded web UI. Web UI in React 19 with the darkraise-ui kit
  (Tailwind 4). SQLite (pure-Go driver) for controller state.
- **Hosts:** Ubuntu 22.04 and 24.04, Debian 12 and 13; amd64 and arm64;
  systemd; UFW.
- **Topology:** multi-host from v1. Agents dial out to the controller over
  mutual TLS; agents open no listening port.
- **Access:** web UI private by default (127.0.0.1 and the Tailscale
  interface). Public exposure only through an explicit Nginx Proxy Manager or
  Cloudflare Tunnel setup managed by Darkpanel.
- **Tailscale:** hosted Tailscale (tailscale.com). Node features use the local
  Tailscale API; tailnet features use a scoped OAuth client.
- **Licensing:** MIT. No GPL code may be vendored; in particular the
  ufw-docker approach is reimplemented, not copied from chaifeng/ufw-docker
  (GPL-3.0).
- **Production safety:** v1 deploys to a real production VPS. Every interface
  that outlives a release (action names, API, agent protocol, database schema,
  files on disk) follows the compatibility rules in section 9.
- **Release target:** no fixed date; v1.0 ships when M1–M8 exit criteria pass.

## 7. Requirements

Each requirement belongs to exactly one epic (section 8).

### Architecture and tooling

- **R1** Before its first implementation, each of these decisions is recorded
  as an architecture decision record in `docs/adr/`: module contract,
  controller–agent protocol, change lifecycle, storage and migrations, API
  versioning, release signing.
- **R2** One Go module builds static `linux/amd64` and `linux/arm64` binaries
  with the web UI embedded, using one documented build command.
- **R3** CI runs Go and TypeScript lint, type checks, unit tests and both
  builds on every push; the repository carries `LICENSE` (MIT),
  `CONTRIBUTING.md` and `SECURITY.md`.
- **R4** `docs/superpowers/gates.md` declares the project's gates.

### Module framework

- **R5** A module declares its id, version, actions, read resources, settings,
  migrations and host preflight checks through one Go interface; adding a
  module touches the core in exactly one registration line.
- **R6** Every action has a stable versioned name (e.g.
  `firewall.rule.add/v1`) and JSON Schemas for input and output. A published
  action version only changes additively; a breaking change adds a new
  version, and the old one stays until the next major release.
- **R7** Every registered action is exposed through the REST API, the CLI
  (`darkpanel <module> <action>`) and the audit log with no module-specific
  glue.
- **R8** Each module owns its database tables under its own prefix with
  forward-only migrations; the core applies them in order and backs up the
  database before applying any.
- **R9** Modules are enabled or disabled per host; a disabled module or one
  failing preflight shows the reason and never blocks other modules.
- **R10** Mutating actions follow one change lifecycle: plan (dry-run diff),
  apply, verify, and optionally confirm-or-revert. Revertible actions store a
  snapshot sufficient to roll them back; compensable and irreversible actions
  are declared as such, and irreversible ones require typed confirmation.
- **R11** Darkpanel writes only files listed in its ownership manifest; edits
  inside system files are confined to marked `BEGIN/END DARKPANEL` blocks;
  external programs run with argument vectors, never through a shell.
- **R12** Third-party credentials (Tailscale, Cloudflare, Nginx Proxy Manager)
  are encrypted at rest with a per-role key file, mode 0600, readable only by
  the service that uses it (root for the agent, the `darkpanel` service user
  for the controller), and never returned by the API after being saved.

### Walking skeleton

- **R13** `darkpanel serve` runs the controller with the embedded web UI,
  `/healthz` (status for anyone; version and schema version for an
  authenticated caller), and a host-information module visible
  end to end in the UI, the API and the CLI.
- **R14** The web UI registers each module's pages through a route registry, so
  adding a module's pages touches no other module.

### Identity, sessions and audit

- **R15** Local users have argon2id-hashed passwords and mandatory TOTP. The
  first admin is created by CLI on the host; there are no default credentials
  and no unauthenticated setup page.
- **R16** Sessions use secure cookies and CSRF protection; login is rate
  limited; authentication middleware denies by default for every HTTP method
  and route.
- **R17** The CLI and automation authenticate with scoped, expiring API tokens.
- **R18** Every mutating action writes an audit entry (who, when, host, action,
  input with secrets redacted, result), viewable in the UI and CLI.
- **R19** User records carry a role field; only `admin` exists in v1.
- **R20** The web UI listens on 127.0.0.1 and the Tailscale interface by
  default; any other bind address requires explicit configuration.

### Integration test harness

- **R21** An automated harness boots disposable VMs for Ubuntu 22.04 and 24.04
  and Debian 12 and 13, installs the build, runs integration tests against real
  UFW, Docker and tailscaled, and scans the hosts from a separate external VM.

### Fleet

- **R22** `darkpanel agent` connects outbound to the controller over mutual
  TLS with full certificate chain and identity verification; agents open no
  listening port.
- **R23** Enrollment uses a single-use, expiring token generated on the
  controller; each agent receives its own certificate, which can be rotated and
  revoked.
- **R24** On connect, agent and controller exchange their versions and
  supported action versions; the controller dispatches only actions the agent
  supports and shows the rest as unavailable.
- **R25** The controller supports agents of the same and the previous minor
  version.
- **R26** The controller host manages itself through the same agent code path.
- **R27** The host list shows online state, last seen, versions and enabled
  modules; an action sent to an offline host fails immediately with a clear
  error.
- **R28** An agent keeps its host's last applied state and its local CLI
  working when the controller is unreachable.

### Install, service and uninstall

- **R29** A one-line install script verifies the release signature, installs
  the systemd unit for the chosen role (controller or agent), and refuses
  unsupported operating systems with a clear message.
- **R30** `darkpanel uninstall` removes everything in the ownership manifest and
  removes its marked blocks; each module contributes its own uninstall step
  (the firewall module, from M3, offers to keep or revert its rules).
- **R31** Break-glass commands run on the host through local sockets, without
  the web UI: agent actions through the agent socket (working without the
  controller), `darkpanel user reset-totp` through the controller's local
  socket, and `darkpanel update rollback` without either service
  (`darkpanel firewall rollback` arrives with R42 in M3).

### Self-update

- **R32** Releases publish binaries, a checksum file and a minisign signature;
  the updater refuses unsigned or mismatched artifacts and refuses downgrades
  unless forced.
- **R33** An update downloads, verifies, stages, self-tests the new binary,
  swaps it atomically while keeping the previous one, restarts through systemd,
  health-checks, and rolls back to the previous binary automatically if the
  health check fails within its time limit.
- **R34** Database migrations during an update run after a database backup that
  is kept for disaster recovery. Every release's migrations keep the schema
  usable by the previous minor release's binary (expand, then contract), so a
  rollback runs the previous binary on the migrated schema; the backup is not
  restored automatically.
- **R35** Hosts follow a release channel (stable or beta); updates run on
  demand or on an optional per-host schedule.
- **R36** The controller rolls an update out to selected agents one host at a
  time and stops at the first failure.

### Firewall: UFW

- **R37** View and change UFW status, default policies, logging and rules
  (allow, deny, limit, reject; in and out; ports, protocols, sources,
  destinations, interfaces, comments) for IPv4 and IPv6.
- **R38** Rules created by Darkpanel are tagged so they are distinguishable
  from manual rules; manual rules are shown and never silently removed.

### Firewall: safe apply

- **R39** Every firewall change shows a preview diff before it is applied.
- **R40** A change that would block the current SSH connection, the panel's
  access path or the agent's link to the controller is refused unless
  explicitly overridden.
- **R41** An applied change reverts automatically unless confirmed within a
  configurable timeout (default 60 seconds); the revert runs on the host, so it
  also happens when the client disconnects.
- **R42** Firewall snapshots can be listed, compared and restored.

### Firewall: Docker integration

- **R43** Darkpanel detects Docker's firewall backend (iptables or nftables)
  and the iptables variant (legacy or nft), enables the Docker integration only
  on supported combinations, and shows the reason otherwise.
- **R44** Darkpanel installs and removes a ufw-docker-style rule block
  (`DOCKER-USER` jumping into UFW's forward chain) in `after.rules` and
  `after6.rules` as a marked block, with its own implementation.
- **R45** External access to a container port is allowed or denied by
  container (name or Compose service) and port, stored as UFW route rules, and
  re-applied when the container is recreated with a new address.
- **R46** Container-to-container traffic, outbound traffic from containers and
  configured private ranges (LAN, Tailscale) keep working with the integration
  enabled.
- **R47** A health check detects when the integration block is missing or not
  loaded (for example after a UFW or Docker upgrade) and offers a repair.

### Firewall: exposure view

- **R48** One view lists every listening port and Docker-published port, IPv4
  and IPv6, with its computed reachability from the internet, the tailnet and
  localhost, and the rule responsible for each verdict.
- **R49** The exposure computation is checked against the external scanner in
  the integration harness on every supported OS.

### Docker

- **R50** List and inspect containers; start, stop, restart and remove them;
  stream logs; show resource usage.
- **R51** List, pull and remove images; prune with a preview of what will be
  removed.
- **R52** List, create and remove networks and volumes, refusing to remove ones
  in use.
- **R53** List Compose projects found on the host and run up, down, pull and
  restart on them.
- **R54** Docker is accessed through the Engine API socket, never by parsing
  `docker` CLI output.
- **R55** Container views show the container's exposure and offer allow and
  deny actions through the firewall Docker integration.

### Tailscale: node

- **R56** Show the node's Tailscale status, addresses, peers with connection
  type (direct, DERP, peer relay), and current DERP region and latency.
- **R57** Change node settings (up and down, advertised routes, exit node use
  and advertisement, accept routes, shields-up, Tailscale SSH) and install or
  update tailscaled on the host.

### Tailscale: tailnet

- **R58** Connect a tailnet with a scoped OAuth client; list devices, approve
  routes and exit nodes, authorize, expire and delete devices, create auth
  keys.
- **R59** View and edit the tailnet policy file with server-side validation and
  a diff before saving, preserving its comments.
- **R60** A newly installed agent can join the tailnet in the same step
  (generated auth key plus `tailscale up`).

### Tailscale: DERP

- **R61** Deploy a custom DERP server on a host with a choice of certificate
  mode (Let's Encrypt for a hostname, or an IP certificate), opening 80/tcp,
  443/tcp and 3478/udp through the firewall module.
- **R62** Client verification is on by default with fail-open disabled.
- **R63** Add or remove the DERP region in the tailnet policy `derpMap`
  (region IDs 900–999), with an option to omit Tailscale's default regions.
- **R64** Probe DERP and STUN reachability, show which nodes use the region,
  and update the derper version.

### Exposure: Nginx Proxy Manager

- **R65** Deploy Nginx Proxy Manager as a Darkpanel-owned container on a chosen
  host at a pinned version, with its admin port bound to localhost and the
  tailnet only, and generated credentials stored encrypted.
- **R66** Manage proxy hosts, access lists and Let's Encrypt certificates
  through the Nginx Proxy Manager API.
- **R67** An "expose container" flow picks a container and port, creates the
  proxy host and certificate, and opens 80 and 443 through the firewall.
- **R68** Nginx Proxy Manager upgrades back up its data volume first and roll
  back on failure.

### Exposure: Cloudflare Tunnel

- **R69** Connect a Cloudflare account with a scoped API token.
- **R70** Create and delete remotely managed tunnels, deploy `cloudflared` on a
  host as a container with the tunnel token, and show connector status.
- **R71** Manage public hostnames (ingress rules) and their DNS records; expose
  a container through a tunnel without opening inbound ports.

### Exposure: the panel itself

- **R72** Expose the Darkpanel UI through Nginx Proxy Manager or a tunnel with
  guardrails (trusted-proxy configuration, TOTP enforced, visible warning) and
  revert the exposure cleanly.

### Hardening

- **R73** A written threat model and security review of authentication, the
  agent protocol, command execution and secret handling, with no open critical
  or high findings.
- **R74** Fuzz tests cover every parser of host tool output and config files.
- **R75** Automated upgrade matrix: upgrade from each prior release, rollback
  on a broken release, agent one minor behind the controller, on all four
  operating systems.
- **R76** The footprint budget in S6 is measured and met.
- **R77** Core flows meet WCAG 2.1 AA (keyboard use, contrast, labels).
- **R78** User documentation covers install, every module and lockout
  recovery; a module-developer guide explains how to add a module.
- **R79** An API reference is generated from the action schemas.

### Release

- **R80** A tagged release produces signed artifacts, a changelog and an SBOM;
  the semantic-versioning and compatibility policy is documented.
- **R81** A v1.0 release candidate runs on the owner's production VPS plus at
  least one agent for 14 days with no manual repair (S7) before v1.0 is
  declared, with a runbook for lockout, failed update and lost controller.

## 8. Epics

| Epic | Milestone | Requirements |
|---|---|---|
| Product brief approved | M0 | sections 1–6 |
| Requirement list approved | M0 | section 7 |
| Non-goals approved | M0 | section 5 |
| Architecture decision records | M1 | R1 |
| Repository, tooling, CI and gates | M1 | R2, R3, R4 |
| Module framework | M1 | R5–R12 |
| Walking skeleton | M1 | R13, R14 |
| Identity, sessions and audit | M1 | R15–R20 |
| Integration test harness | M1 | R21 |
| Controller-agent fleet | M2 | R22–R28 |
| Install, service and uninstall | M2 | R29–R31 |
| Self-update | M2 | R32–R36 |
| UFW management | M3 | R37, R38 |
| Firewall safe apply | M3 | R39–R42 |
| Docker firewall integration | M3 | R43–R47 |
| Exposure view | M3 | R48, R49 |
| Docker management | M4 | R50–R55 |
| Tailscale node management | M5 | R56, R57 |
| Tailnet management | M5 | R58–R60 |
| DERP server management | M5 | R61–R64 |
| Nginx Proxy Manager | M6 | R65–R68 |
| Cloudflare Tunnel | M6 | R69–R71 |
| Panel exposure | M6 | R72 |
| Security review | M7 | R73, R74 |
| Upgrade and compatibility matrix | M7 | R75 |
| Performance budget | M7 | R76 |
| Accessibility | M7 | R77 |
| Documentation | M7 | R78, R79 |
| Release pipeline and versioning | M8 | R80 |
| Production rollout and runbook | M8 | R81 |

## 9. Milestones

| M | Name | Exit criteria |
|---|---|---|
| M0 | Discovery | Brief, requirements and non-goals approved by the owner |
| M1 | Foundations | ADRs recorded; one binary serves the host-info module through UI, API and CLI behind TOTP login; CI and gates green; harness boots all four OS VMs |
| M2 | Fleet and lifecycle | A controller manages itself and one enrolled agent over mTLS; signed update and automatic rollback demonstrated in the harness |
| M3 | Firewall | UFW, safe apply, Docker integration and exposure view pass S1–S3 in the harness on all four OS |
| M4 | Docker | Every Docker requirement demonstrated on a managed agent, including allow and deny from a container view |
| M5 | Tailscale | Node and tailnet management demonstrated; a Darkpanel-deployed DERP region carries traffic between two tailnet nodes |
| M6 | Exposure | A container exposed through Nginx Proxy Manager and through a Cloudflare Tunnel, and the panel exposed and reverted, all demonstrated |
| M7 | Hardening | No open critical or high security findings; upgrade matrix, footprint budget, accessibility and documentation complete |
| M8 | Release | v1.0 tagged and signed, running on the owner's production VPS plus one agent, runbook written |
| M9 | Post-v1 candidates | Each candidate either scheduled into a new milestone or deferred by the owner |

## 10. Architecture direction

### Process model

One binary, three roles:

```
             browser / darkpanel CLI (remote, API token)
                        │ HTTPS, /api/v1
        ┌───────────────▼────────────────┐
        │ darkpanel serve  (controller)  │  SQLite, users, audit,
        │  REST API · web UI · scheduler │  desired state, secrets
        └──────▲──────────────────▲──────┘
     mTLS, agent dials out        │ same code path (R26)
        ┌──────┴───────┐   ┌──────┴───────┐
        │ darkpanel    │   │ local agent  │
        │ agent (VPS2) │   │ (controller  │
        └──────┬───────┘   │  host)       │
               │           └──────────────┘
   ufw · Docker Engine API · tailscaled LocalAPI · systemd
```

- `darkpanel serve` is the controller: API, web UI, database, scheduler.
- `darkpanel agent` runs on every managed host (including the controller host)
  as root under systemd, holding the host-side half of each module.
- `darkpanel <module> <action>` is the CLI, generated from the action registry
  (R7). It talks to the controller API, or to the local agent socket for
  break-glass commands (R31).

### Module contract

A module is a Go package with two halves sharing one manifest:

- **Manifest:** id, version, actions (versioned name, input and output JSON
  Schema, mutating or read-only, needs confirm-or-revert), resources, settings,
  preflight checks, migrations.
- **Controller half:** persistence, orchestration across hosts, calls to cloud
  APIs (Tailscale, Cloudflare).
- **Agent half:** host executors that talk to local systems through providers
  (UFW, Docker Engine API, Tailscale LocalAPI, systemd D-Bus, file editor with
  marked blocks).
- **UI half:** a lazy-loaded route bundle registered in the web route registry.

The core supplies the change lifecycle (R10), audit (R18), secrets (R12),
migrations (R8), API and CLI exposure (R7) and capability negotiation (R24).
New features are new modules, new actions or new action versions; a published
action version only ever changes additively (R6).

### Agent half before M2

In M1 there is no remote agent yet. The controller runs the agent half
in-process through the same handler interface the local agent uses (R26), so
the host-information module works end to end in M1 and M2 only moves the agent
half behind the mTLS transport.

### Compatibility rules (what keeps production safe)

| Surface | Rule |
|---|---|
| Action names and schemas | Additive changes only within a version; new version for breaking changes; old version kept until next major |
| REST API | `/api/v1` frozen under the same additive rule; generated from action schemas |
| Agent protocol | Version and capability handshake; unknown fields ignored; controller supports agents N and N-1 minor |
| Database | Forward-only, per-module migrations; backup before migrate; expand-then-contract so the previous minor's binary runs on the new schema; no automatic restore on rollback |
| Files on disk | Only manifest-listed files and marked blocks; uninstall reverses them |
| Binary | Signed releases, atomic swap, health-checked restart, automatic rollback |

### Firewall integration strategy

The Docker integration sits behind a `DockerFirewallStrategy` interface. v1
ships one implementation: the `DOCKER-USER` chain hooked into UFW's forward
chain, written by Darkpanel. Detection (R43) selects a strategy or reports the
host as unsupported, so Docker's nftables backend can get its own strategy
later without changing the firewall module's actions.

### Open architecture questions (decided in M1 ADRs)

- Agent transport: gRPC over mTLS or WebSocket with JSON messages.
- Harness VM technology: Incus/LXD VMs, QEMU, or Proxmox VMs on the
  development host.
- How DERP and Cloudflare `cloudflared` run: container or systemd service.
- Release hosting and signing key custody.

## 11. Post-v1 candidates

Proposed modules after v1, held in the M9 register for the owner to schedule:

1. Host overview with metrics history and alerts.
2. systemd services and journal logs.
3. Package updates, unattended-upgrades and reboot-required tracking.
4. Users, SSH keys and SSH daemon hardening.
5. Intrusion prevention with fail2ban or CrowdSec.
6. Backups with restic (paths, Docker volumes, database dumps) to S3, B2 or
   SFTP, with restore.
7. Notifications (ntfy, Telegram, Discord, email, webhooks).
8. Compose stacks from Git and container image update checks.
9. Uptime and certificate expiry monitoring.
10. Cloudflare DNS management beyond tunnels.
11. WireGuard tunnels.
12. Headscale control plane support.
13. Roles, per-host permissions, OIDC single sign-on, Tailscale identity login.
14. Docker nftables firewall backend strategy.
15. Security baseline audit (SSH, kernel settings, open ports, outdated
    packages).
16. Web terminal and file manager.
17. Scheduled jobs (cron and systemd timers), swap and sysctl tuning.
18. Managed database containers (PostgreSQL, MySQL) with backups.
