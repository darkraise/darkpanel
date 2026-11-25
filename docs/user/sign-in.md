# Sign-in methods

Every user can sign in with a username, a password and a code from an
authenticator app. An admin can add two more ways in Settings, then Sign-in:
an OIDC identity provider and Tailscale identity. Neither one creates users:
it signs in a user an admin already added, as
[Users and roles](users-and-roles.md) explains.

## OIDC

With OIDC on, the login page shows a `Sign in with <name>` button below the
form. It sends the user to the provider and back.

Set it up:

1. Register Darkpanel at your provider as a web application. Its redirect URL
   is `<redirect base>/api/v1/auth/oidc/callback`, where the redirect base is
   the `https://` address users open Darkpanel at, one of its HTTPS listeners
   or a runtime listener's public host.
2. In Settings, then Sign-in, fill in the issuer URL, the client id, the
   client secret, the button name and the redirect base, and save. The
   secret is never shown again.
3. For each user who signs in this way, set the email the provider reports,
   with Edit sign-in links on the Users page.

Or from the CLI:

```sh
darkpanel auth oidc set --issuer https://id.example.com --client-id darkpanel \
  --display-name "Acme SSO" --redirect-base https://panel.example.com
```

The client secret is write-only, so give it in the `--input` JSON as
`clientSecret`.

The first sign-in links a user by the provider's verified email. Later
sign-ins match the provider's account id instead, so a changed or reassigned
email at the provider cannot take over the user. Changing a user's email in
Darkpanel ends that link. When no user matches, the login page says "No
Darkpanel user is linked to this account. Ask an admin to add you."

The button shows only on the redirect base's address. Turn off removes the
button and keeps the links.

## Tailscale identity

With Tailscale identity on, a user who opens Darkpanel at its tailnet address
sees `Continue as <user> (<login>, from <device>)` above the login form when
the device they browse from belongs to a tailnet login an admin set on that
user.

The first Continue as from a device asks for the code from the user's
authenticator app ("Enter your code once to remember laptop."). A right code
signs the user in and remembers that device, so the next Continue as from it
signs in without a code. A user who has not finished enrollment, or has no
code yet, sees "Continue as needs your authenticator code once. Finish account
setup or sign in below." instead of the button: Tailscale sign-in never
activates an invite.

Turn it on in Settings, then Sign-in, or with
`darkpanel auth tailscale set --enabled`. Set each user's tailnet login with
Edit sign-in links on the Users page.

It works only on the Tailscale listener, and only for a device owned by a
user: a tagged device, a device shared into the tailnet, a request through a
proxy and any Darkpanel host's own address never sign in. A host is known by
the tailnet address its agent connects from, which the controller observes
itself; a host whose agent connects over a LAN or public address is not known
(see Upgrading). A remembered device that later becomes a Darkpanel host stays
listed but no longer signs in. If Darkpanel is not listening on the tailnet,
the Sign-in page says so.

### Remembered devices

Each user sees their remembered devices in Settings, then Tailscale devices,
with when each was remembered and last used. Forget asks for the device's
stable id and makes its next Continue as ask for the code again; sessions it
opened stay signed in. An admin sees and forgets any user's devices with
Tailscale devices in that user's row menu on the Users page.

A user's devices are forgotten when an admin resets their code, when they
start an enrollment, and when their tailnet login changes. Deleting a user
removes them; disabling keeps them, and a disabled user cannot sign in.

## Require the code

OIDC has "Require Darkpanel code too": with it on, the user also enters the
code from their authenticator app after the provider, and the session starts
only after the code.

Tailscale identity has "Require the code every time": with it on, every
Continue as asks for the code, and no device signs in without one. Each right
code still remembers its device, so turning the switch off lets those devices
sign in without a code.

## Sign-in outcomes

| The login page says | Why |
|---|---|
| No Darkpanel user is linked to this account. Ask an admin to add you. | No user has that email or tailnet login. |
| This sign-in method is off. Use your username, password and code. | An admin turned the method off. |
| The sign-in was refused. Try again, or use your username, password and code. | The provider or the tailnet check failed, the user is disabled, the provider account is linked to another user, or the device is neither remembered nor allowed to ask for the code. An invited user must enroll first when OIDC requires the code; Tailscale never signs in an invited user. |

Every sign-in, refused or not, is in the audit log with its method.

## Upgrading

Two changes need an admin's attention when upgrading to a version with
remembered devices.

Users that only Tailscale sign-in ever activated have no password or code of
their own, so after the upgrade they cannot sign in until they finish
enrollment. Before upgrading, look on the Users page for active users whose
sign-in methods lack "Password and code", and after it give each a new link
with New enrollment link in their row menu (`core.user.renew-invite`). If every
admin is such a user, root on the controller's host recovers one over the
controller socket:

```sh
sudo darkpanel user reset-totp --username <name>
sudo darkpanel user activate --username <name>
sudo darkpanel user reset-password --username <name>
```

The first command shows the new secret; add it to an authenticator app, give
the second a code from it, and set a password with the third.

Hosts whose agents reach the controller over a LAN or public address are no
longer known as hosts, so their tailnet devices are not refused. The
controller logs `fleet: the host's agent session is not from a tailnet
address; it is not recorded` once per connect for each such host. To make a
host connect over the tailnet, set the controller's `--agent-advertise` to its
tailnet address ([Install Darkpanel](install.md)), then revoke the host and
enroll its agent again, which keeps the host and its settings:

```sh
darkpanel host revoke --host-id <id>
darkpanel host token --host-id <id>
sudo darkpanel agent enroll --token-file <token-file>
```

Run the last command as root on the host's machine, with the token the second
command printed. An enrolled agent keeps the controller address its enrollment
token gave it, so enrolling again is the only way to change it.

If you roll back to an older version and run it for a while, a code reset or a
tailnet-login change made meanwhile does not forget that user's devices. After
upgrading again, forget the devices of every user changed that way.

A sign-in under way while the controller upgrades, at the identity provider
or at its code step, fails when it comes back to Darkpanel. Start it again.

