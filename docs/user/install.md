# Install Darkpanel

Darkpanel has two roles. The controller runs the web interface, the API and the
database, and one controller manages many hosts. An agent runs on every managed
host, including the controller's own, and carries out the controller's
changes. You install the controller first, then add the other hosts.

## Before you start

You need:

- A host running Ubuntu 22.04 or 24.04, or Debian 12 or 13, on x86_64 or
  aarch64. `install.sh` stops on any other system.
- Root access. Run the installer with `sudo`.
- These tools on the host: `openssl`, `sha256sum`, `tar`, `base64`, `od`, and
  either `curl` or `wget`. `install.sh` names the first one it cannot find.

## Install the controller

Download `install.sh` from the latest release and run it on the controller's
host:

```sh
curl -fsSL https://github.com/Darkraise/darkpanel/releases/latest/download/install.sh -o install.sh
sudo sh install.sh --role controller --agent-advertise <address agents reach>:7443
```

`--agent-advertise` is the address that enrollment tokens tell new agents to
connect to. Use a name or an IP address that every host you add can reach.

`install.sh` accepts these options:

- `--role controller` or `--role agent` is required. It chooses what to install
  on this host.
- `--version X.Y.Z` installs that version. Without it, `install.sh` reads the
  release index and installs the newest stable release.
- `--base-url URL` downloads from a mirror instead of the release site. The URL
  must start with `https://` or `http://`. A mirror needs no trust of its own,
  because `install.sh` installs a release only when a key it carries signed
  that release's `checksums.txt`.
- `--agent-listen <ip>:<port>` adds an address of this host for the agent
  listener. Repeat it for more than one address.
- `--agent-advertise <host>:<port>` sets the address enrollment tokens carry
  when a request names none.
- `--token-file <token-file>` reads the enrollment token or bootstrap bundle of
  an agent from a file. See the section on adding a host.

`--agent-listen` and `--agent-advertise` are for the controller role only, and
`--token-file` is for the agent role only. `install.sh` refuses a command line
that mixes them.

## What the installer does

`install.sh` does these steps in order, and stops at the first one that fails:

1. It downloads the release and the signature files for `checksums.txt`.
2. It verifies a minisign signature of `checksums.txt` with OpenSSL against the
   release keys embedded in `install.sh`. The signature must name exactly the
   version being installed.
3. It checks the tarball against the SHA-256 in `checksums.txt`.
4. It installs the binary under `/usr/local/lib/darkpanel/` and points
   `/usr/local/bin/darkpanel` at it.
5. It runs `darkpanel system install` for the role.

`darkpanel system install` creates the `darkpanel` user, the data and key
directories and the systemd units. For the controller role it then starts
`darkpanel-controller.service`, enrolls the host's own agent and starts
`darkpanel-agent.service`. The controller runs as `darkpanel` and the agent
runs as root. You can run `install.sh` or `sudo darkpanel system install` again
at any time: it finishes or repairs an install.

The installed host looks like this:

- The controller keeps its database under `/var/lib/darkpanel/controller/` and
  the agent keeps its database under `/var/lib/darkpanel/agent/`.
- The role keys and the agent's credentials are under `/etc/darkpanel/`.
- The agent listener is always bound on `127.0.0.1:7443` and on port 7443 of
  each Tailscale address of the host. Each `--agent-listen` adds one address.
  The options are written to
  `/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf`.
- The web interface is served over HTTP on `127.0.0.1:8080` and `[::1]:8080`,
  and over HTTPS on port 8443 of each Tailscale address.

## Check install.sh before you run it

`install.sh` is listed in the release's signed `checksums.txt`, like the
tarballs. To check it before you run it as root, verify the signature with
[minisign](https://jedisct1.github.io/minisign/), using a release public key and
its key id from `internal/update/trustedkeys/production.go` in the source, and
then the checksum:

```sh
base=https://github.com/Darkraise/darkpanel/releases/latest/download
curl -fsSLO "$base/checksums.txt"
curl -fsSLO "$base/checksums.txt.<key id>.minisig"
minisign -V -P <public key> -m checksums.txt -x checksums.txt.<key id>.minisig
sha256sum --check --ignore-missing checksums.txt
```

The trusted comment that minisign prints names the version and the channel.
Check that they are the release you meant to install: an older release's
`checksums.txt` is signed too, and it verifies together with that release's
older `install.sh`.

The release workflow also attests every file, so
`gh attestation verify install.sh --repo darkraise/darkpanel` is a second check.

## Add a host

Create the host on the Hosts page of the web interface with "Add host", or with
the CLI:

```sh
darkpanel host create --name <name>
```

The answer shows an enrollment token once. The token lasts one hour and works
once. On the new host, download `install.sh` as above and run:

```sh
sudo sh install.sh --role agent
```

`install.sh` asks for the token on the terminal without showing it. To give it
the token from a file instead, write the token to a file that root owns with
mode 0600, and pass its path:

```sh
sudo sh install.sh --role agent --token-file <token-file>
```

The agent connects out to the controller and opens no listening port.

To have a host join your tailnet before it enrolls, create it with
`darkpanel host create --name <name> --tailnet-join --tags <tag>`. The answer
is a bootstrap bundle in place of a plain token, and `--token-file` accepts a
bundle as well. The module pages for `tailnet` and `tailscale` explain the
tailnet side.

If a host is revoked, or its credentials are lost, enroll it again:

1. Run `darkpanel host revoke` for a host that is still enrolled. This closes
   its session and refuses its certificate.
2. Run `darkpanel host token` to issue the revoked host a new token.
3. On the host, run `sudo darkpanel agent enroll`. It reads the token from
   `--token-file`, from a hidden prompt on the terminal, or from standard
   input. A running agent uses the new credentials from its next connection
   attempt and needs no restart.

If saved agent credentials already exist on a host, `install.sh --role agent`
installs without asking for a token. Rerun it with `--token-file` when the
credentials were revoked.

## Reach the web interface

The loopback listener is only reachable from the controller's host. From
another machine, forward the port over SSH and open `http://127.0.0.1:8080` in
your browser:

```sh
ssh -L 8080:127.0.0.1:8080 <host>
```

On a host with Tailscale, the HTTPS listener on port 8443 of its Tailscale
address presents a certificate from the controller's own web root. To trust it,
print the web root and its SHA-256 fingerprint on the controller's host:

```sh
sudo darkpanel controller web-ca
```

Import the PEM block into your browser's or your system's trusted roots. To
make the CLI verify the HTTPS listener against it, put the fingerprint in
`~/.config/darkpanel/cli.json`, which must have mode 0600:

```json
{"url": "https://100.64.0.1:8443", "caFingerprint": "<fingerprint>"}
```

To replace these web listeners, add `--listen` to the `darkpanel serve` command
of `darkpanel-controller.service` in a systemd drop-in. You can repeat it, and
each value is `loopback`, `tailscale`, `https://<ip>:<port>` or
`http://<loopback ip>:<port>`.

## Next step

You cannot sign in yet, because a new install has no account. Continue with
[Create the first admin](first-admin.md).
