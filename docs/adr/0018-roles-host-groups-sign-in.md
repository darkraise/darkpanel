# ADR 0018: Roles, host groups and sign-in methods

**Requirements:** none

## Status

Accepted

## Context

Until M10 every Darkpanel user was an admin, and a token's scope (read,
operate, admin) applied to every host. Teams need members who see and act on
some hosts only. Host data reaches the API through controller-side reads as
well as through per-host routes, and those reads were written for callers who
could see the whole fleet: any of them could hand a member another host's
name, address or state.

## Decision

- **Roles and grants.** A user is an `admin` or a `member`. A member's grants
  give `viewer` (read) or `operator` (operate) on a host group or on `*`, every
  host including later ones. A member's scope on the controller's own host is
  capped at read. Access is resolved at each request, so a grant change applies
  to the next request, and a token never exceeds its user.
- **Not found, never forbidden.** A host outside a member's grants answers
  `request.not_found`, the answer for an unknown host, so host ids do not leak.
- **Every controller-side read declares its host data class** in
  `core.ActionSpec.HostData`. A read without a class is served only to fleet
  principals, so a new read fails closed. Internal reads are exempt. A read
  whose required scope is above read needs that scope on every host.
- **Users join by invitation.** An admin invites a user with its role and
  grants; the user sets a password and an authenticator code through a
  one-time enrollment link valid 72 hours. A disabled user is refused by every
  sign-in method, its sessions and its tokens, and the last active admin
  cannot be demoted, disabled or deleted.
- **OIDC links, never provisions.** One provider is configured. A first
  sign-in links a verified email an admin set on an existing user and pins the
  provider's issuer and subject; later sign-ins match the pin. No sign-in
  creates a user.
- **Tailscale identity on the tailnet listener only.** The controller host's
  agent reads the peer's whois; tagged and shared-in nodes, and any node or
  address of a Darkpanel host, are refused, and the tailnet user id is pinned
  at the first sign-in.
- **Provider trust by default.** OIDC and Tailscale identity trust the
  provider's or the tailnet's own sign-in; each method can require the
  Darkpanel code on top.

### Host data classes

`fleet` reads need fleet access (an admin user, at any token scope). `none`,
`input` and `filtered` reads need fleet access or read on at least one host.
An `input` read also needs read on each host its `HostPointers` name. A
`filtered` read drops every row tied to a host outside the caller's grants,
and every row tied to no host unless the caller has fleet access.

| Action | Class | Reason |
|---|---|---|
| `cloudflare.account.get` | fleet | The connected account is fleet configuration. |
| `cloudflare.hostname.list` | filtered | Hostnames are tied to no host, so only fleet callers see them. |
| `cloudflare.tunnel.list` | filtered | Tunnels are tied to no host, so only fleet callers see them. |
| `auth.settings.get` | fleet | The panel's sign-in settings. |
| `core.audit.list` | filtered | Entries of hosts outside the grants, and host-less entries, are dropped. |
| `core.group.list` | fleet | Host groups and their members span the fleet. |
| `core.host.list` | filtered | Lists only the hosts inside the grants. |
| `core.module.list` | input (`/hostId`) | Lists the modules of the one host it names. |
| `core.token.list` | none | The caller's own tokens. |
| `core.update.rollouts` | filtered | Hosts outside the grants are dropped; a rollout left with none is hidden. |
| `core.user.directory` | fleet | Every user of the panel, with grants and sign-in links. |
| `core.user.list` | fleet | Every user of the panel. |
| `derp.server.list` | filtered | Servers on hosts outside the grants are dropped. |
| `derp.server.probe` | input (`/hostId`, `/agents`) | Probes one host's server; probe sources and nodes are cut to the grants. |
| `metrics.alert.list` | filtered | Alerts of hosts outside the grants are dropped. |
| `metrics.alert.list-rules` | fleet | Alert rules target every host or a group. |
| `metrics.history.get` | input (`/hostId`) | Reads the metrics of the one host it names. |
| `notify.channel.list` | fleet | Channels and their routing are fleet configuration. |
| `notify.channel.test` | fleet | Sends a test through a fleet-wide channel. |
| `npm.instance.list` | filtered | Instances and unreadable hosts outside the grants are dropped. |
| `panel.exposure.status` | fleet | The exposure of the controller's own interface. |
| `tailnet.account.get` | fleet | The connected tailnet account. |
| `tailnet.authkey.list` | fleet | Auth keys enroll any host. |
| `tailnet.device.list` | filtered | Devices are tied to no host, so only fleet callers see them. |
| `tailnet.policy.get` | fleet | The tailnet policy governs every host. |

`internal/registry/hostdata_test.go` fails for an unclassified read, and
`internal/controller/api/leak_test.go` reads every class as a member to check
that no other host's id, name or address comes back.

## Alternatives considered

- **Filtering in the API layer.** One generic filter over read outputs cannot
  know which fields name a host and would miss nested rows.
- **Per-host scopes on tokens only.** Tokens alone cannot give a person signed
  in through the browser less than the whole fleet.
- **Creating users at the first OIDC sign-in.** The provider would decide who
  gets access; linking only users an admin added keeps that decision in
  Darkpanel.
- **Requiring the Darkpanel code for every method.** It would make OIDC and
  Tailscale identity no faster than a password; each method offers it as an
  option instead.

## Consequences

- Every new controller-side read must choose a class; the registry test names
  the ones that do not.
- Reads that join host-less provider data (tailnet devices, Cloudflare
  tunnels) show members nothing until those rows are tied to hosts.
- Filtering after a fetch can thin a page; the change list already refetches
  to fill one.
