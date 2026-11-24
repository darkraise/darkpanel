# Users and roles

Darkpanel has two roles. An admin can do everything on every host and change
the panel's own settings. A member sees and acts only on the hosts its grants
name. This page explains how to give someone access; the commands themselves
are listed in [Administer Darkpanel](administration.md#users).

## Roles and grants

A member's grants each give one of two roles on a target:

- `viewer` lets the member read the hosts: their pages, changes, audit
  entries, rollouts, alerts and metrics.
- `operator` adds every change a host's modules offer, such as firewall rules,
  packages or containers. A few actions stay admin-only on every host, such as
  revealing a stored secret.

The target is a host group, or `*` for every host, including hosts enrolled
later. When several grants cover a host, the highest role applies. On the
controller's own host a member is never more than a viewer, because that host
holds the panel's database and keys.

A host that no grant covers does not exist for the member: Darkpanel answers
"not found", as it does for a host id that is not there, so a member cannot
learn other hosts' names or ids. Fleet-wide pages, such as Network and the
admin settings, are hidden from members.

Grant changes apply to the member's next request, and an open live view ends
within 30 seconds once its host is no longer visible.

## Host groups

A host group is a named set of hosts. Grants, alert rules and notification
channels target groups, so you name a set once and use it everywhere. A host
can sit in several groups. Create groups on the Host groups page under
Settings, or with `darkpanel group create --name <name>`. A group that a grant,
an alert rule or a channel still names cannot be deleted; the refusal lists
each one.

## Invite a user

1. Open Settings, then Users, and press Invite user.
2. Enter a username and choose Member or Admin. For a member, tick the groups
   it may use and a role for each, or choose a role on all hosts.
3. Optionally enter the email an OIDC sign-in should match and the tailnet
   login a Tailscale sign-in should match ([Sign-in methods](sign-in.md)).
4. Review the plan, type the username to confirm, and apply it. Darkpanel
   shows the enrollment link once, with the date it expires, 72 hours later.
   Copy it and send it to the user.

The user opens the link, sets a password, scans the authenticator QR code and
enters a code, and is then signed in. A used or expired link says "This link
has expired. Ask an admin for a new one." Use New enrollment link on the user's
row menu for a new one; the old link stops working.

From the CLI, `darkpanel user invite --username <name> --role member` invites
a user, with its grants in the `--input` JSON:

```json
{"username": "noor", "role": "member", "grants": [{"role": "operator", "target": "<group id>"}]}
```

## Change a user's access

The row menu on the Users page changes a user's role, access and sign-in
links, and disables or enables the user. A disabled user cannot sign in by
any method, and its sessions and API tokens stop working until it is enabled
again. Darkpanel refuses to demote, disable or delete the last active admin.

## API tokens of a member

A member creates its own API tokens on the API tokens page, with the `read` or
`operate` scope. A token never does more than its user: a member's `operate`
token can read, but not change, a host the member only views, and it loses
access as soon as its user does.
