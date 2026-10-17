# Darkpanel

Darkpanel is an MIT-licensed, single-binary VPS and self-hosted fleet manager,
with a CLI and a web UI, for owners of unmanaged Linux servers. It starts with
a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure
and safe self-update.

## Status

Pre-release, in milestone M1 (Foundations). The repository, build and CI exist;
the binary has only a `version` command and is not ready to run on a server.

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
from.

[CONTRIBUTING.md](CONTRIBUTING.md) lists the other make targets.
[SECURITY.md](SECURITY.md) explains how to report a vulnerability.

## License

MIT; see [LICENSE](LICENSE).
