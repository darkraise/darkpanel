# Darkpanel — roadmap

**Product:** An MIT-licensed, single-binary VPS and self-hosted fleet manager (CLI and web UI) for owners of unmanaged Linux servers, starting with a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure and safe self-update.
**Spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Non-goals:** web hosting stacks, PaaS deploys and app store, Kubernetes and Swarm, Headscale, RHEL and firewalld, non-systemd hosts, Docker nftables backend, roles beyond admin and SSO, web terminal and file manager, metrics history and alerting, telemetry

| M | Name | Exit criteria | Register | State | Note |
|---|---|---|---|---|---|
| M0 | Discovery | brief, requirements and non-goals approved by the owner | docs/superpowers/registers/2026-10-17-m0-discovery.md | open | - |
| M1 | Foundations | ADRs recorded; one binary serves the host-info module through UI, API and CLI behind TOTP login; CI and gates green; harness boots all four OS VMs | docs/superpowers/registers/2026-10-17-m1-foundations.md | open | - |
| M2 | Fleet and lifecycle | controller manages itself and one enrolled agent over mTLS; signed update and automatic rollback demonstrated in the harness | docs/superpowers/registers/2026-10-17-m2-fleet-lifecycle.md | open | - |
| M3 | Firewall | UFW, safe apply, Docker integration and exposure view pass S1-S3 in the harness on all four OS | docs/superpowers/registers/2026-10-17-m3-firewall.md | open | - |
| M4 | Docker | every Docker requirement demonstrated on a managed agent, including allow and deny from a container view | docs/superpowers/registers/2026-10-17-m4-docker.md | open | - |
| M5 | Tailscale | node and tailnet management demonstrated; a Darkpanel-deployed DERP region carries traffic between two tailnet nodes | docs/superpowers/registers/2026-10-17-m5-tailscale.md | open | - |
| M6 | Exposure | a container exposed through Nginx Proxy Manager and through a Cloudflare Tunnel, and the panel exposed and reverted, all demonstrated | docs/superpowers/registers/2026-10-17-m6-exposure.md | open | - |
| M7 | Hardening | no open critical or high security findings; upgrade matrix, footprint budget, accessibility and documentation complete | docs/superpowers/registers/2026-10-17-m7-hardening.md | open | - |
| M8 | Release | v1.0 tagged and signed, running on the owner's production VPS plus one agent, runbook written | docs/superpowers/registers/2026-10-17-m8-release.md | open | - |
| M9 | Post-v1 candidates | each candidate scheduled into a new milestone or deferred by the owner | docs/superpowers/registers/2026-10-17-m9-post-v1.md | open | - |
