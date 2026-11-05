# Create the first admin

A new install has no default account and no setup page. You create the first
admin on the controller's host, through the controller socket. The socket
accepts only root on that host, so you need `sudo` there.

## Create the admin

Run this on the controller's host:

```sh
sudo darkpanel user create --username admin
```

The command does these things in order:

1. It asks for the password twice. The password must have 12 to 128
   characters.
2. It shows the plan and asks `Apply? [y/N]`. Answer `y`, or give `--yes` to
   skip the question.
3. It prints a QR code, an `otpauth` URI and the TOTP secret. Scan the QR code
   with an authenticator app, or enter the secret by hand.
4. It asks for a code from the authenticator app.
5. It activates the user and prints `User admin is active and can log in.`

The TOTP secret is shown once. If you lose it before you finish, start the
enrollment again as described below.

## If the activation did not finish

If you close the terminal, type a wrong code or lose the connection after the
user was created, the user stays pending and cannot sign in. Finish the
activation with a code from the authenticator app:

```sh
sudo darkpanel user activate --username admin
```

When the first command used `--socket` or `--dev`, the hint that the command
prints carries the same option. Give it to `user activate` as well.

## Sign in

Open the web interface as the install page describes. Enter your username,
your password and the six-digit code that your authenticator app shows for
Darkpanel. The code changes every 30 seconds.

## If you lose the authenticator

Run this on the controller's host to give the user a new TOTP secret:

```sh
sudo darkpanel user reset-totp --username admin
```

It ends the user's sessions, then runs the same flow as above: a new QR code
and secret, a code, and the activation. Until you finish, the user cannot sign
in. For a lost password, or for a locked account, see
[Recover from a lockout](lockout-recovery.md).

## Next: an API token for the CLI

The CLI has no login command. It authenticates with an API token. Create one
on the Tokens page of the web interface after you sign in. Choose a name, a
scope and, if you want, a number of days:

- The scope is `read`, `operate` or `admin`, from the least to the most a token
  can do.
- A token expires after 90 days unless you set a different time, from 1 to 365
  days, when you create it.
- The token is shown once. Copy it before you leave the page.

Give the token and the controller's address to the CLI in environment
variables:

```sh
export DARKPANEL_URL=http://127.0.0.1:8080 DARKPANEL_TOKEN=<token>
darkpanel hostinfo summary get
darkpanel audit list
```

You can also put `"token"` and `"url"` in `~/.config/darkpanel/cli.json`, which
must have mode 0600. The CLI sends a token only to the `"url"` of the same
file, or to `http://127.0.0.1:8080` when the file names none. It refuses to
send a token over plain `http` to any address that is not loopback.

Revoke a token you no longer need with `darkpanel token revoke --id <id>`. The
[API reference](../api/README.md) describes the same actions for scripts, and
[Administer Darkpanel](administration.md) lists every command and its scope.
