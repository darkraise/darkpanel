# v1 completion register

**Source:** owner request 2026-10-21 "let resolve them all, one spec cover all items and issues, then one plan and keep executing until we can release the final first version"; M3–M8 roadmap epics (R37–R81); ADR 0005 deferred controller backup and restore
**Covers:** docs/superpowers/specs/2026-10-21-v1-completion-design.md

| # | Item | Assigned | Acceptance | State | Note |
|---|---|---|---|---|---|
| 1 | R37 View and change UFW status, default policies, logging and rules (allow, deny, limit, reject; in and out; ports, protocols, sources, destinations, interfaces, comments) for IPv4 and IPv6. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 2 | R38 Rules created by Darkpanel are tagged so they are distinguishable from manual rules; manual rules are shown and never silently removed. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 3 | R39 Every firewall change shows a preview diff before it is applied. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 4 | R40 A change that would block the current SSH connection, the panel's access path or the agent's link to the controller is refused unless explicitly overridden. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 5 | R41 An applied change reverts automatically unless confirmed within a configurable timeout (default 60 seconds); the revert runs on the host, so it also happens when the client disconnects. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 6 | R42 Firewall snapshots can be listed, compared and restored. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 7 | R43 Darkpanel detects Docker's firewall backend (iptables or nftables) and the iptables variant (legacy or nft), enables the Docker integration only on supported combinations, and shows the reason otherwise. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 8 | R44 Darkpanel installs and removes a ufw-docker-style rule block (`DOCKER-USER` jumping into UFW's forward chain) in `after.rules` and `after6.rules` as a marked block, with its own implementation. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 9 | R45 External access to a container port is allowed or denied by container (name or Compose service) and port, stored as UFW route rules, and re-applied when the container is recreated with a new address. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 10 | R46 Container-to-container traffic, outbound traffic from containers and configured private ranges (LAN, Tailscale) keep working with the integration enabled. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 11 | R47 A health check detects when the integration block is missing or not loaded (for example after a UFW or Docker upgrade) and offers a repair. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 12 | R48 One view lists every listening port and Docker-published port, IPv4 and IPv6, with its computed reachability from the internet, the tailnet and localhost, and the rule responsible for each verdict. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 13 | R49 The exposure computation is checked against the external scanner in the integration harness on every supported OS. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 14 | R50 List and inspect containers; start, stop, restart and remove them; stream logs; show resource usage. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 15 | R51 List, pull and remove images; prune with a preview of what will be removed. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 16 | R52 List, create and remove networks and volumes, refusing to remove ones in use. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 17 | R53 List Compose projects found on the host and run up, down, pull and restart on them. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 18 | R54 Docker is accessed through the Engine API socket, never by parsing `docker` CLI output. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 19 | R55 Container views show the container's exposure and offer allow and deny actions through the firewall Docker integration. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 20 | R56 Show the node's Tailscale status, addresses, peers with connection type (direct, DERP, peer relay), and current DERP region and latency. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 21 | R57 Change node settings (up and down, advertised routes, exit node use and advertisement, accept routes, shields-up, Tailscale SSH) and install or update tailscaled on the host. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 22 | R58 Connect a tailnet with a scoped OAuth client; list devices, approve routes and exit nodes, authorize, expire and delete devices, create auth keys. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 23 | R59 View and edit the tailnet policy file with server-side validation and a diff before saving, preserving its comments. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 24 | R60 A newly installed agent can join the tailnet in the same step (generated auth key plus `tailscale up`). | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 25 | R61 Deploy a custom DERP server on a host with a choice of certificate mode (Let's Encrypt for a hostname, or an IP certificate), opening 80/tcp, 443/tcp and 3478/udp through the firewall module. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 26 | R62 Client verification is on by default with fail-open disabled. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 27 | R63 Add or remove the DERP region in the tailnet policy `derpMap` (region IDs 900–999), with an option to omit Tailscale's default regions. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 28 | R64 Probe DERP and STUN reachability, show which nodes use the region, and update the derper version. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 29 | R65 Deploy Nginx Proxy Manager as a Darkpanel-owned container on a chosen host at a pinned version, with its admin port bound to localhost and the tailnet only, and generated credentials stored encrypted. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 30 | R66 Manage proxy hosts, access lists and Let's Encrypt certificates through the Nginx Proxy Manager API. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 31 | R67 An "expose container" flow picks a container and port, creates the proxy host and certificate, and opens 80 and 443 through the firewall. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 32 | R68 Nginx Proxy Manager upgrades back up its data volume first and roll back on failure. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 33 | R69 Connect a Cloudflare account with a scoped API token. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 34 | R70 Create and delete remotely managed tunnels, deploy `cloudflared` on a host as a container with the tunnel token, and show connector status. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 35 | R71 Manage public hostnames (ingress rules) and their DNS records; expose a container through a tunnel without opening inbound ports. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 36 | R72 Expose the Darkpanel UI through Nginx Proxy Manager or a tunnel with guardrails (trusted-proxy configuration, TOTP enforced, visible warning) and revert the exposure cleanly. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 37 | R73 A written threat model and security review of authentication, the agent protocol, command execution and secret handling, with no open critical or high findings. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 38 | R74 Fuzz tests cover every parser of host tool output and config files. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 39 | R75 Automated upgrade matrix: upgrade from each prior release, rollback on a broken release, agent one minor behind the controller, on all four operating systems. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 40 | R76 The footprint budget in S6 is measured and met. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 41 | R77 Core flows meet WCAG 2.1 AA (keyboard use, contrast, labels). | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 42 | R78 User documentation covers install, every module and lockout recovery; a module-developer guide explains how to add a module. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 43 | R79 An API reference is generated from the action schemas. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 44 | R81 A v1.0 release candidate runs on the owner's production VPS plus at least one agent for 14 days with no manual repair (S7) before v1.0 is declared, with a runbook for lockout, failed update and lost controller. | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 45 | Core: cross-module calls as composite operations with durable step logs (spec 1.3) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 46 | Core: agent background tasks (spec 1.4) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 47 | Core: agent streams relayed as server-sent events and CLI follow (spec 1.6) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 48 | Core: guard context passed to agent plans (spec 1.5) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 49 | Core: host clients - exec allowlist, Docker Engine API, Tailscale local API, socket table, netfilter reader, restore writes, module-owned Docker objects (spec 1.8) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 50 | Core: cloud HTTP client, sealed account credentials and in-process fakes for Tailscale, Cloudflare and NPM (spec 1.9) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 51 | Core: trusted proxies and runtime listeners (spec 1.10) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 52 | Core: module skeletons, registry lines, error codes, dependencies and the web extension points and ChangeDialog (spec 1.12, 11) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 53 | ADR 0014 records the core extensions and closes the DERP and cloudflared deferred decisions (spec Contracts) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 54 | darkpanel controller backup and restore with an argon2id passphrase-derived key (ADR 0005, spec 10) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 55 | Live demonstrations with owner credentials: tailnet, DERP region traffic, NPM certificate, tunnel and DNS, panel exposed and reverted (spec 12 item 2) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 56 | Core: undo handlers for compensable actions (spec 1.2) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 57 | Core: per-action availability (spec 1.7) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 58 | Core: uninstall module options (spec 1.11) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 59 | Core: CLI confirmation semantics kept and per-guard overrides (spec 1.13) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 60 | Tailscale web certificate and MagicDNS name in the web root certificate (ADR 0010, spec 4.1) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 61 | Owner decision D2: NPM admin UI exposed over HTTPS on an owner-chosen hostname by explicit action, with an audited password reveal (spec 6, 1.14) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 62 | Core: agent-side module enablement route PUT /agent/v1/modules/{id}/enabled (spec 1.4) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 63 | Core: effects journal and undo route POST /agent/v1/changes/{id}/undo with change repair (spec 1.2) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 64 | Core: managed-dir owned-path kind and restore entries (spec 1.8) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 65 | Core: asynchronous apply for composites and long-running actions, read-only plan lookup (spec 1.3) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 66 | Core: module recovery handlers (spec 1.3b) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 67 | Core: recovery identities, post-state checks and the undo state machine (spec 1.2) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 68 | Core: agent availability route and unredacted internal reads (spec 1.7) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 69 | Restore quarantine after a disaster restore (spec 10) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 70 | Enrollment bootstrap bundle dpb2 carrying the tailnet join key (spec 4.2) | docs/superpowers/specs/2026-10-21-v1-completion-design.md | - | open | - |
| 71 | Owner confirmation P1: S3 wording for docker-proxy bindings governed by UFW INPUT rules (spec Owner decisions) | - | - | open | - |
| 72 | Owner confirmation P2: Docker routed gateway modes and custom-named public bridges unsupported for the integration (spec Owner decisions) | - | - | open | - |
