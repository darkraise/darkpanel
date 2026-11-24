# ADR 0019: Remembered tailnet devices

**Requirements:** none

## Status

Proposed

## Context

ADR 0018 let Tailscale identity sign a user in from any node their tailnet
login owns, unless the method requires the Darkpanel code too. It refused the
nodes of Darkpanel hosts by the addresses and node ids their agents reported
when they connected. Two weaknesses followed. Root on any user-owned server in
the tailnet that is not a recognised host signs in as the server's owner with
no code. And the host table trusted what an agent said about itself, so a host
whose agent reported nothing, or reported something else, was not refused.

## Decision

- **A node is remembered by a code typed from it.** A Tailscale Continue as
  signs in without a code only from a node its user remembered: the first
  Continue as from a node asks for the user's TOTP code, and a right code
  remembers the node for that user (`core_user_tailnet_devices`, migration
  0019). With "Require the code every time" on, every Continue as asks the
  code, and each right code still remembers its node.
- **The code step is bound to its node.** The code-step ticket carries the
  node's stable id, name and addresses and the tailnet link it was issued
  for. The code step refuses a request from any other address, a user whose
  tailnet link changed since, and a node recorded as a host meanwhile, before
  it judges the code.
- **Tailscale sign-in never activates an invite.** A pending user is refused;
  the login page tells them to finish enrollment.
- **The host table holds only what the controller observed.** Each agent
  session's own tailnet source address is recorded; an agent's self-report is
  no longer read, and the stored node ids are dropped. A host that connects
  over a LAN or public address is not recorded, and the controller logs so at
  each connect.
- **Devices are forgotten** by the user, by an admin, and whenever the user's
  code is reset, an enrollment starts or their tailnet login changes.

This amends the "Tailscale identity on the tailnet listener only" and
"Provider trust by default" decisions of
[ADR 0018](0018-roles-host-groups-sign-in.md) for Tailscale identity; OIDC is
unchanged.

## Alternatives considered

- **Require the code on every Tailscale sign-in.** It closes the gap but makes
  Continue as no faster than a password, which ADR 0018 rejected.
- **Keep trusting agent self-reports, and add node ids from the Tailscale
  API.** A compromised or misconfigured host still decides what is recorded
  about it, and the API needs a credential the controller may not hold.
- **Remember a device in a browser cookie.** The secret would sit in the
  browser of the very server it should guard against, and could be copied.

## Consequences

- The first Continue as from each device asks the code once.
- Users that only Tailscale sign-in activated must finish enrollment after
  the upgrade; admins give them a new enrollment link.
- Hosts whose agents reach the controller over a LAN or public address are no
  longer refused as hosts until they enroll again against the controller's
  tailnet address; the threat model lists this and the other accepted risks.
- Four actions list and forget remembered devices, for the caller and for an
  admin (`core.tailnetdevice.list`, `core.tailnetdevice.forget`,
  `core.user.list-tailnet-devices`, `core.user.forget-tailnet-device`).
