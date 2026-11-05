# Darkpanel user guide

Darkpanel is an MIT-licensed, single-binary VPS and self-hosted fleet manager,
with a CLI and a web UI, for owners of unmanaged Linux servers. It starts with
a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure
and safe self-update.

## Get started

- [Install Darkpanel](install.md) puts the controller on one host and adds
  agents on the others.
- [Create the first admin](first-admin.md) makes the account you sign in with.
- [Uninstall Darkpanel](uninstall.md) removes it from a host again.

## Day to day

- [Administer Darkpanel](administration.md) covers users, API tokens, hosts,
  modules, the audit log, updates, and backup and restore.

Each module has a page of its own:

- [hostinfo](modules/hostinfo.md) shows a host's operating system, CPU,
  memory, load and uptime.
- [firewall](modules/firewall.md) manages UFW rules, with a revert timer for
  every change and a Docker-aware container firewall.
- [docker](modules/docker.md) lists and controls containers, images, networks,
  volumes and Compose projects.
- [tailscale](modules/tailscale.md) installs Tailscale on a host and manages
  its node.
- [tailnet](modules/tailnet.md) manages the tailnet account: access policy,
  devices and auth keys.
- [derp](modules/derp.md) runs DERP relay servers for the tailnet.
- [npm](modules/npm.md) deploys Nginx Proxy Manager and exposes containers
  through it.
- [cloudflare](modules/cloudflare.md) manages Cloudflare tunnels and the
  hostnames they serve.
- [panel](modules/panel.md) exposes the Darkpanel web interface itself through
  Nginx Proxy Manager or a tunnel.

## When something goes wrong

- [Recover from a lockout](lockout-recovery.md) gets you back in after a lost
  password, a firewall change that cut you off, or an update that broke a host.

## Reference

- [API reference](../api/README.md) documents every route, action and resource.
- [Versioning and compatibility](../versioning.md) explains versions, channels,
  releases and the update rules.
- [Module guide](../dev/module-guide.md) explains how to write a module.
