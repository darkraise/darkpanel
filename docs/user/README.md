# Darkpanel user guide

Darkpanel is an MIT-licensed, single-binary VPS and self-hosted fleet manager,
with a CLI and a web UI, for owners of unmanaged Linux servers. It starts with
a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure
and safe self-update, and administers each host's services, users and SSH,
intrusion prevention, scheduled jobs and kernel tuning, with a root console for
admins.

## Get started

- [Install Darkpanel](install.md) puts the controller on one host and adds
  agents on the others.
- [Create the first admin](first-admin.md) makes the account you sign in with.
- [Uninstall Darkpanel](uninstall.md) removes it from a host again.

## Day to day

- [Administer Darkpanel](administration.md) covers users, API tokens, hosts,
  modules, the audit log, updates, and backup and restore.
- [Users and roles](users-and-roles.md) gives people access to some hosts or
  all of them, with host groups and enrollment links.
- [Sign-in methods](sign-in.md) adds an OIDC provider and Tailscale identity
  to the password and code.
- [Notifications](notifications.md) sends alerts, failed changes and failed
  updates to ntfy, Telegram, Discord, email or a webhook.
- [Metrics and alerts](metrics-and-alerts.md) charts each host's history and
  raises alerts from it.

Each module has a page of its own:

- [hostinfo](modules/hostinfo.md) shows a host's operating system, CPU,
  memory, load and uptime.
- [system](modules/system.md) updates, installs and removes packages, tracks
  reboots, switches automatic updates and installs Docker Engine.
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
- [services](modules/services.md) starts, stops and enables systemd units and
  reads and follows the journal.
- [users](modules/users.md) manages login accounts, their SSH keys and the
  SSH daemon's hardening.
- [fail2ban](modules/fail2ban.md) installs fail2ban and bans addresses that
  keep failing to sign in over SSH.
- [console](modules/console.md) opens a root terminal and a file manager on a
  host, for admins, after an authenticator code.
- [jobs](modules/jobs.md) runs commands on a schedule through systemd timers.
- [tuning](modules/tuning.md) sets kernel parameters and manages a swap file.

## When something goes wrong

- [Recover from a lockout](lockout-recovery.md) gets you back in after a lost
  password, a firewall change that cut you off, or an update that broke a host.
- [Follow the runbook](../runbook.md) lists the procedures for a lockout, a failed
  update and a lost controller, in the order you need them.

## Reference

- [API reference](../api/README.md) documents every route, action and resource.
- [Versioning and compatibility](../versioning.md) explains versions, channels,
  releases and the update rules.
- [Module guide](../dev/module-guide.md) explains how to write a module.
