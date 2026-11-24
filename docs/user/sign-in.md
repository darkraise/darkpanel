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
user. Pressing it signs them in.

Turn it on in Settings, then Sign-in, or with
`darkpanel auth tailscale set --enabled`. Set each user's tailnet login with
Edit sign-in links on the Users page.

It works only on the Tailscale listener, and only for a device owned by a
user: a tagged device, a device shared into the tailnet, a request through a
proxy and any Darkpanel host's own address never sign in. A host is known by
the tailnet addresses its agent reports when it connects, so a host whose
Tailscale module is off is not recognised. If Darkpanel is not listening on the
tailnet, the Sign-in page says so.

## Require the code too

Each method has "Require Darkpanel code too". With it on, the user also enters
the code from their authenticator app after the provider or the tailnet match,
and the session starts only after the code.

## Sign-in outcomes

| The login page says | Why |
|---|---|
| No Darkpanel user is linked to this account. Ask an admin to add you. | No user has that email or tailnet login. |
| This sign-in method is off. Use your username, password and code. | An admin turned the method off. |
| The sign-in was refused. Try again, or use your username, password and code. | The provider or the tailnet check failed, the user is disabled, or the provider account is linked to another user. An invited user must enroll first when the method requires the code. |

Every sign-in, refused or not, is in the audit log with its method.
