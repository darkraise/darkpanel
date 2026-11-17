# Darkpanel

[![CI](https://github.com/Darkraise/darkpanel/actions/workflows/ci.yml/badge.svg)](https://github.com/Darkraise/darkpanel/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Darkraise/darkpanel?include_prereleases)](https://github.com/Darkraise/darkpanel/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Darkpanel is a single-binary fleet manager for owners of unmanaged Linux
servers. One controller, with a CLI and a web UI, manages the firewall, Docker,
Tailscale and the services you expose on each host, and every host updates
itself from signed releases with automatic rollback.

## Features

- **One binary, two roles.** `darkpanel serve` runs the controller; `darkpanel
  agent run` runs the agent on every host. Agents connect out to the controller
  over mutual TLS and open no listening port of their own.
- **Safe changes.** Every change shows a plan before it applies, lands in an
  audit log, and where it can, comes with an undo. A firewall change reverts by
  itself unless you confirm it, and is refused when it would cut your own
  access.
- **Modules.** [hostinfo](docs/user/modules/hostinfo.md), a Docker-aware UFW
  [firewall](docs/user/modules/firewall.md), [Docker](docs/user/modules/docker.md),
  [Tailscale](docs/user/modules/tailscale.md) and its
  [tailnet](docs/user/modules/tailnet.md), [DERP](docs/user/modules/derp.md)
  relays, and exposure through [Nginx Proxy Manager](docs/user/modules/npm.md)
  or [Cloudflare tunnels](docs/user/modules/cloudflare.md).
- **Signed updates.** A host updates one at a time, the controller's own host
  first, from releases whose minisign signature it verifies. Each update takes a
  backup, keeps the previous binary, and rolls back within two minutes when the
  new version fails its health check.
- **Private by default.** There is no default account. The web interface
  listens on loopback and on Tailscale addresses only, and sign-in needs a
  password and a TOTP code.
- **Open interfaces.** Every action is a CLI command and a REST route, with an
  [API reference](docs/api/README.md) and an OpenAPI description.

Supported hosts: Ubuntu 22.04, 24.04 and 26.04, and Debian 12 and 13, on amd64
and arm64.

## Status

Darkpanel is pre-1.0. The newest build is a release candidate, published as a
pre-release on the [Releases](https://github.com/Darkraise/darkpanel/releases)
page; it is the build the install command below installs. Use it on servers you
can rebuild, and expect changes before v1.0. See [CHANGELOG.md](CHANGELOG.md)
for what changed and [SECURITY.md](SECURITY.md) for the support policy.

## Quick start

On the server that will run the controller:

```sh
curl -fsSL https://github.com/Darkraise/darkpanel/releases/download/v1.0.0-rc.3/install.sh | sudo sh
```

The script asks whether the server is the controller or an agent, which version
to install and how agents reach the controller, shows the equivalent command
line, and installs once you confirm. It verifies the release's signature and
checksum before it installs anything. To read it first, see [Check install.sh
before you run it](docs/user/install.md#check-installsh-before-you-run-it).

Then create the first admin on that server. It asks for a password and shows a
QR code for an authenticator app:

```sh
sudo darkpanel user create --username admin
```

The web interface listens on the server's loopback address. From your own
machine, forward the port and open `http://127.0.0.1:8080`:

```sh
ssh -L 8080:127.0.0.1:8080 <host>
```

Add each other server from the Hosts page, or with `darkpanel host create
--name <name>`, and run the same install command there as an agent with the
enrollment token it shows.

## Documentation

| Read | To |
|---|---|
| [User guide](docs/user/README.md) | install, administer and uninstall Darkpanel, and use each module |
| [Runbook](docs/runbook.md) | recover from a lockout, a failed update or a lost controller |
| [API reference](docs/api/README.md) | script against the REST API |
| [Versioning and compatibility](docs/versioning.md) | understand versions, channels and update rules |
| [Threat model](docs/security/threat-model.md) | see what Darkpanel defends against |
| [Architecture decisions](docs/adr/README.md) | learn why it is built this way |
| [Module guide](docs/dev/module-guide.md) | write a module |

## Build from source

You need Go 1.27.4, Node 24, pnpm 11.17.0 and GNU make.

```sh
make build
```

This builds the web UI, then static Linux binaries with the UI embedded:
`bin/darkpanel-linux-amd64` and `bin/darkpanel-linux-arm64`.
`bin/darkpanel-linux-amd64 version` prints the version a binary was built from.

## Contributing

Contributions are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) explains the make
targets, how to run a development server and what a pull request needs,
including updating this README and the docs when a change alters what users
see or do. Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).
Report a vulnerability as [SECURITY.md](SECURITY.md) describes, not in a public
issue.

## License

MIT; see [LICENSE](LICENSE).
