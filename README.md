# Darkpanel

Darkpanel is an MIT-licensed, single-binary VPS and self-hosted fleet manager,
with a CLI and a web UI, for owners of unmanaged Linux servers. It starts with
a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure
and safe self-update.

## Status

Pre-release, in milestone M1 (Foundations). `darkpanel serve` runs the
controller and the local agent, serves the REST API with TOTP login and the
host-information module, and the CLI drives it. The web UI's pages are not
built yet, and nothing installs the service; do not use it on a server you
care about.

## Prerequisites

- Go 1.27.2
- Node 24
- pnpm 11.17.0
- GNU make

## Build

```sh
make build
```

This builds the web UI, then two static Linux binaries with the UI embedded:

- `bin/darkpanel-linux-amd64`
- `bin/darkpanel-linux-arm64`

`bin/darkpanel-linux-amd64 version` prints the version the binary was built
from. The commands below call it `darkpanel`; copy the binary for your
machine's architecture onto your `PATH` under that name:

```sh
sudo install -m 0755 bin/darkpanel-linux-amd64 /usr/local/bin/darkpanel
```

## Run

In M1 the service runs as root:

```sh
sudo darkpanel serve
```

It keeps its databases under `/var/lib/darkpanel/`, its keys under
`/etc/darkpanel/` and its controller socket under `/run/darkpanel-controller/`,
and serves HTTP on `127.0.0.1:8080` and `[::1]:8080` and HTTPS on port 8443 of
each Tailscale address. `--listen` replaces those listeners and may be
repeated: `loopback`, `tailscale`, `https://<ip>:<port>` or
`http://<loopback ip>:<port>`. Reach the loopback listener from another
machine through SSH port forwarding: `ssh -L 8080:127.0.0.1:8080 <host>`.

### The first admin

There is no default account and no setup page. Create the first admin on the
host itself, through the controller socket:

```sh
sudo darkpanel user create --username admin
```

It asks for the password twice, shows a QR code and the TOTP secret for an
authenticator app, and asks for a code to activate the user. The secret is not
shown again; `sudo darkpanel user reset-totp --username admin` issues a new
one.

### Trust the web root

HTTPS listeners present a certificate from the controller's own web root.
Print it and its SHA-256 fingerprint:

```sh
sudo darkpanel controller web-ca
```

Import the PEM block into your browser's or system's trusted roots. To let the
CLI verify the HTTPS listeners against it, put the fingerprint in
`~/.config/darkpanel/cli.json`, which must have mode 0600:

```json
{"url": "https://100.64.0.1:8443", "caFingerprint": "<fingerprint>"}
```

### Use the CLI with a token

The CLI has no login command. It authenticates with an API token, which you
create once with your session: on the web UI's Tokens page when the UI is
built, or through the API. With the API, log in, then plan and apply
`core.token.create`, passing the `csrfToken` of the login answer:

```sh
URL=http://127.0.0.1:8080
curl -c session -H 'Content-Type: application/json' \
  -d '{"username": "admin", "password": "<password>", "code": "<code>"}' $URL/api/v1/auth/login
curl -b session -H 'Content-Type: application/json' -H 'X-CSRF-Token: <csrfToken>' \
  -d '{"input": {"name": "laptop", "scope": "admin"}}' $URL/api/v1/actions/core.token.create/v1/plan
curl -b session -H 'Content-Type: application/json' -H 'X-CSRF-Token: <csrfToken>' \
  -d '{"planId": "<planId>"}' $URL/api/v1/actions/core.token.create/v1/apply
rm session
```

The apply answer's `output.token` is shown only this once. Give it to the CLI
in `DARKPANEL_TOKEN` or as `"token"` in `cli.json`:

```sh
export DARKPANEL_URL=http://127.0.0.1:8080 DARKPANEL_TOKEN=<token>
darkpanel hostinfo summary get
darkpanel audit list
darkpanel --help
```

A token's scope is `read`, `operate` or `admin`, and it expires after 90 days
unless `expiresInDays` says otherwise; `darkpanel token revoke --id <id>`
revokes one.

[CONTRIBUTING.md](CONTRIBUTING.md) explains the make targets and how to run a
development server. [SECURITY.md](SECURITY.md) explains how to report a
vulnerability.

## License

MIT; see [LICENSE](LICENSE).
