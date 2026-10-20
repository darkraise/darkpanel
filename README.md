# Darkpanel

Darkpanel is an MIT-licensed, single-binary VPS and self-hosted fleet manager,
with a CLI and a web UI, for owners of unmanaged Linux servers. It starts with
a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure
and safe self-update.

## Status

Pre-release, in milestone M2 (Fleet and lifecycle). `darkpanel serve` runs the
controller: the REST API with TOTP login, the web UI, the audit log and the
agent listener. `darkpanel agent run` runs the agent on every host, the
controller's own included, over mutual TLS. `install.sh` and
`darkpanel system install` install either role, and hosts update themselves
from signed releases with automatic rollback. No release has been published
yet; do not use it on a server you care about.

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

## Install

`install.sh` installs a release on Ubuntu 22.04 or 24.04 or Debian 12 or 13. It
verifies the release's minisign signature with OpenSSL against the keys it
embeds, checks the tarball's checksum, installs the binary under
`/usr/local/lib/darkpanel/` with `/usr/local/bin/darkpanel` pointing at it, and
runs `darkpanel system install` for the role. On the controller's host:

```sh
curl -fsSL https://github.com/Darkraise/darkpanel/releases/latest/download/install.sh -o install.sh
sudo sh install.sh --role controller --agent-advertise <address agents reach>:7443
```

`install.sh` itself is not in the signed `checksums.txt`; the release workflow
attests its build instead. Check that attestation with the GitHub CLI before
running the script as root:

```sh
gh attestation verify install.sh --repo darkraise/darkpanel
```

`system install` creates the `darkpanel` user and the directories, keys and
systemd units, starts `darkpanel-controller.service`, enrolls the host's own
agent and starts `darkpanel-agent.service`. Rerunning it finishes or repairs an
install. The controller runs as `darkpanel`, the agent as root. The agent
listener is always bound on `127.0.0.1:7443` and on port 7443 of each
Tailscale address; `--agent-listen <ip>:<port>` adds a bind, and
`--agent-advertise` is the address enrollment tokens carry when the request
names none. Both are written to
`/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf`.

The controller keeps its database under `/var/lib/darkpanel/controller/`, the
agent under `/var/lib/darkpanel/agent/`, the keys and the agent's credentials
under `/etc/darkpanel/`, and serves HTTP on `127.0.0.1:8080` and `[::1]:8080`
and HTTPS on port 8443 of each Tailscale address. `--listen` in a drop-in
replaces those web listeners and may be repeated: `loopback`, `tailscale`,
`https://<ip>:<port>` or `http://<loopback ip>:<port>`. Reach the loopback
listener from another machine through SSH port forwarding:
`ssh -L 8080:127.0.0.1:8080 <host>`.

### Add a host

Create the host on the web UI's Hosts page ("Add host"), or with
`darkpanel host create --name <name>`. The answer shows a one-hour, single-use
enrollment token once. On the new host:

```sh
curl -fsSL https://github.com/Darkraise/darkpanel/releases/latest/download/install.sh -o install.sh
sudo sh install.sh --role agent
```

It asks for the token on the terminal (or reads it from `--token-file`, a
root-owned file with mode 0600). The agent connects out to the controller; it
opens no listening port. `darkpanel host revoke` closes a host's session and
refuses its certificate; `darkpanel host token` issues a revoked host a new
token, and `sudo darkpanel agent enroll` on the host takes it.

### Updates

A host updates to a signed release with `darkpanel update rollout --hosts <id>
--version <version> --channel stable`, one host at a time, the controller's own host first,
stopping at the first failure; `darkpanel host update-policy` sets a host's
channel (`stable` or `beta`) and an optional weekly update window. Each update
takes a database backup before migrating, keeps the previous binary as
`darkpanel.prev`, and rolls back by itself when the new version is not healthy
within 80 seconds. [docs/versioning.md](docs/versioning.md) explains the
version and compatibility rules.

### Break-glass

On a host whose controller cannot be reached, `sudo darkpanel --local
<command>` sends an agent-side command to the local agent's socket instead,
for example `sudo darkpanel hostinfo summary get --local`. With the agent
stopped too, `sudo darkpanel update rollback --local --yes` puts the previous
binary back. If that rollback is interrupted, for example with Ctrl+C, the
services can stay stopped until the next boot; `sudo darkpanel update recover`
finishes a rollback that had started replacing the binary and restarts the
services, or ends one that had not, so it can be run again.

### Uninstall

`sudo darkpanel uninstall` stops and removes the services, units, binary,
users, data and keys listed in the host's ownership manifest, after printing
the plan. `--keep-data` keeps the databases, backups, keys and credentials, so
a later install finds them.

### The first admin

There is no default account and no setup page. Create the first admin on the
controller's host itself, through the controller socket:

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
in `DARKPANEL_TOKEN` or as `"token"` in `cli.json`. A token in `cli.json` is
sent only to the `"url"` in the same file (or to `http://127.0.0.1:8080` when
the file names none), and the CLI refuses to send any token over plain `http`
except to a loopback address:

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
