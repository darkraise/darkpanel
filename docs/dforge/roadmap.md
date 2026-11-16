# Darkpanel — roadmap

**Product:** An MIT-licensed, single-binary VPS and self-hosted fleet manager (CLI and web UI) for owners of unmanaged Linux servers, starting with a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure and safe self-update.
**Spec:** docs/dforge/specs/2026-10-17-darkpanel-design.md
**Non-goals:** web hosting stacks, PaaS deploys and app store, Kubernetes and Swarm, Headscale, RHEL and firewalld, non-systemd hosts, Windows or macOS hosts, Docker nftables backend, roles beyond admin, per-host permissions and SSO, web terminal and file manager, metrics history and alerting, telemetry

| M | Name | Exit criteria | Register | State | Note |
|---|---|---|---|---|---|
| M0 | Discovery | brief, requirements and non-goals approved by the owner | docs/dforge/registers/2026-10-17-m0-discovery.md | closed | audit 2026-10-17 7a91299: 4 MET, 0 deferred |
| M1 | Foundations | ADRs recorded; one binary serves the host-info module through UI, API and CLI behind TOTP login; CI and gates green; harness boots all four OS VMs | docs/dforge/registers/2026-10-17-m1-foundations.md | closed | audit 2026-10-21 c955987: 180 MET, 83 deferred |
| M2 | Fleet and lifecycle | controller manages itself and one enrolled agent over mTLS; signed update and automatic rollback demonstrated in the harness | docs/dforge/registers/2026-10-17-m2-fleet-lifecycle.md | closed | audit 2026-11-12 eb57e9e: 128 MET, 64 deferred |
| M3 | Firewall | UFW, safe apply, Docker integration and exposure view pass S1-S3 in the harness on all four OS | docs/dforge/registers/2026-10-17-m3-firewall.md | closed | audit 2026-11-16 c9058ea: 381 MET, 309 deferred, 50 n/a |
| M4 | Docker | every Docker requirement demonstrated on a managed agent; allow and deny from a container view is an owner check on a running panel carried to M6 row 8 by the owner's ruling 2026-11-16 | docs/dforge/registers/2026-10-17-m4-docker.md | closed | audit 2026-11-16 75ae236: 28 MET, 17 deferred, 3 n/a |
| M5 | Tailscale | node, tailnet and DERP management proven by the gates; the live demonstration (tailnet management, a Darkpanel-deployed DERP region carrying traffic between two tailnet nodes) recorded as completed by the owner's ruling 2026-11-17, to be verified by the owner on the production VPS, with any issue opened as a new spec | docs/dforge/registers/2026-10-17-m5-tailscale.md | closed | audit 2026-11-17 faf9651: 66 MET, 1 owner ruling (row 4 live demo), 13 deferred, 3 n/a |
| M6 | Exposure | Nginx Proxy Manager, Cloudflare Tunnel and panel exposure proven by the gates; the live demonstrations (a container exposed through Nginx Proxy Manager and through a Cloudflare Tunnel, the panel exposed and reverted) and the owner browser checks (rows 4-8) recorded as completed by the owner's ruling 2026-11-17, to be verified by the owner on the production VPS, with any issue opened as a new spec | docs/dforge/registers/2026-10-17-m6-exposure.md | open | - |
| M7 | Hardening | no open critical or high security findings; upgrade matrix, footprint budget, accessibility and documentation complete | docs/dforge/registers/2026-10-17-m7-hardening.md | open | - |
| M8 | Release | v1.0 tagged and signed, running on the owner's production VPS plus one agent, runbook written | docs/dforge/registers/2026-10-17-m8-release.md | open | - |
| M9 | Post-v1 candidates | each candidate scheduled into a new milestone or deferred by the owner | docs/dforge/registers/2026-10-17-m9-post-v1.md | open | - |
