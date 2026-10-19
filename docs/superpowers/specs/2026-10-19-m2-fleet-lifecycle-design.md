# M2 epics: controller-agent fleet, install and uninstall, self-update, release pipeline

**Date:** 2026-10-19
**Status:** design reviewed by Fable 2026-10-19 (rework; 18 findings accepted
by the owner); spec panel round 1 by Fable and Codex gpt-6-astra 2026-10-19
(both rework; every finding folded in, see the review log at the end)
**Product spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Requirements:** R22–R36, R80; M1 register rows 22, 23, 24, 49, 92
**Execution strategy:** parallel — one foundation part fixes every shared
type, interface, dependency, error code, path, data package and migration
owner first; then the authority, transport, agent API, install, update and
release parts and the web pages share no file and run together, followed by
the controller fleet, host actions, agent daemon and rollout parts, and a
final assembly part followed by the harness (owner standing preference:
favour parallel to reach v1 sooner).

## Goal

Meet the M2 exit criteria: the controller manages itself and one enrolled
agent over mTLS; a signed update and an automatic rollback are demonstrated in
the harness. Also deliver R80 (signed releases, changelog, SBOM, versioning
policy), pulled forward from M8, so self-update and the install script consume
real release artifacts.

## Owner decisions (2026-10-19)

- One spec covers the three M2 epics (Controller-agent fleet, Install, service
  and uninstall, Self-update) and the five M1 rows deferred into M2.
- All of R80 moves from M8 into this spec. M8 keeps R81 only.
- `install.sh` verifies the minisign signature with OpenSSL 3 alone, against
  public keys embedded in the script; it downloads nothing but the release
  files.
- `hashicorp/yamux` (MPL-2.0, weak file-level copyleft) is accepted as a
  dependency, used unmodified; ADR 0003's frozen yamux framing stays.
- Execution is parallel subagent waves.
- Every Fable design-review finding and every spec-panel finding of round 1 is
  accepted.

## Versions

Checked against the Go module proxy on 2026-10-19. Pins are exact; the
foundation plan re-checks them and records any change in its Assumptions.

| Package | Version | Licence | Use |
|---|---|---|---|
| `github.com/hashicorp/yamux` | v0.1.2 | MPL-2.0 | stream multiplexing on the agent connection (ADR 0003) |
| `aead.dev/minisign` | v0.3.0 | MIT | release signature verification (ADR 0007) |
| `github.com/godbus/dbus/v5` | v5.2.4 | BSD-2-Clause | systemd D-Bus: transient units, timers, unit control |

Release tooling, pinned in `release.yml` by version and checksum at plan time:
`git-cliff` (changelog), `cyclonedx-gomod` (SBOM), the `minisign` command built
from `aead.dev/minisign/cmd/minisign` at the pinned version.

## Design

JSON fields are camelCase (ADR index, shared contracts). Every new table is
`STRICT` and stores times as the fixed-width RFC 3339 UTC text M1 uses.

### 1. Processes, users, paths and settings (R26, M1 row 24)

- `darkpanel serve` is the controller only. It runs as the system user
  `darkpanel` (ADR 0005) and no longer opens `agent.db` or the agent key.
- `darkpanel agent run` is the agent daemon, as root. On the controller's host
  it dials `127.0.0.1:7443` like any remote agent, so every host goes through
  the same transport and handler (R26).
- `serve --dev` keeps development paths and allows a non-root user, as in M1.
  The M1 in-process agent stays behind a separate flag, `--embedded-agent`,
  valid only with `--dev`, so development and the existing end-to-end tests
  keep working. `darkpanel agent run --dev` runs the real daemon against the
  development layout. Production units never pass either flag.
- **Settings are flags**, as M1's `--listen` is; there is still no
  configuration file. `serve` gains:
  - `--agent-listen <addr:port>` (repeatable): extra agent-listener binds,
    **added** to the always-bound `127.0.0.1:7443` and the Tailscale address
    (section 4). Unlike the web `--listen`, it never replaces loopback,
    because the controller's own agent dials it.
  - `--agent-advertise <host:port>`: the address enrollment tokens carry when
    the request names none. No default.

  `darkpanel system install --role controller` takes the same two flags and
  writes them into the drop-in
  `/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf`, recorded
  in the ownership manifest.
- `internal/paths` gains, with production values:

| Field | Production | Owner and mode |
|---|---|---|
| `AgentSocket` | `/run/darkpanel-agent/agent.sock` | root, socket 0600, directory 0700 |
| `AgentTLSDir` | `/etc/darkpanel/agent/` | root, 0700 |
| `UpdateDir` | `/var/lib/darkpanel/update/` (transaction records, the update lock) | root, 0700 |
| `BinaryDir` | `/usr/local/lib/darkpanel/` (`darkpanel`, `darkpanel.prev`, `darkpanel.failed`, `darkpanel.new-<tx>`) | root, 0755, binaries 0755 |
| `BinaryLink` | `/usr/local/bin/darkpanel` → `BinaryDir/darkpanel` | root |

  The M1 fields keep their values. `Under(root)` places every new path under
  `root`.

### 2. Agent authority and certificates (R22, R23, ADR 0003)

- A second `core_ca` row with purpose `agent`: an Ed25519 root, 10-year
  lifetime, key sealed with the controller key (ADR 0005). It is separate from
  the web authority (ADR 0010); `listen/authority.go` already selects rows by
  purpose.
- The controller id is a ULID stored in `core_meta` under `controller_id`,
  created on first start.
- The controller's agent-listener certificate: Ed25519, one year, EKU
  `serverAuth` only, URI SAN `darkpanel://controller/<controller id>`, renewed
  from the authority after two thirds of its life. The listener presents the
  chain `[leaf, root]`. Agents pin the SAN, not the certificate.
- Agent certificates: Ed25519 keys generated on the agent, one year, EKU
  `clientAuth` only, URI SAN `darkpanel://host/<host id>`, no DNS or IP SANs.
  The controller checks every CSR's signature (`CheckSignature`) and requires
  an Ed25519 public key; it ignores every identity field in the CSR.
- **Credential generations** (agent). `AgentTLSDir` holds generation
  directories `gen-<n>/` (each with `agent.key`, `agent.crt`, `ca.crt`,
  `controller.json` = `{"address": "<host:port>", "san": "<controller SAN>"}`)
  and a symlink `current` → `gen-<n>`. A new generation is written completely
  (each file fsynced, then the directory), then `current` is replaced
  atomically (`symlink` to `current.tmp`, `rename` over `current`, fsync
  `AgentTLSDir`). The previous generation is kept until the first successful
  handshake with the new one, then deleted. Every reader resolves `current`
  once and reads all four files from that one directory, so a crash at any
  point leaves a complete old or a complete new generation.
- Table `core_agent_certs (serial TEXT PRIMARY KEY, host_id TEXT NOT NULL
  REFERENCES core_hosts (id) ON DELETE CASCADE, not_before, not_after,
  revoked_at, created_at)`. Serials are 128-bit random.
- **Renewal.** After two thirds of its life the agent sends a CSR over its
  live session (`POST /controller/v1/renew`, section 4). The controller issues
  a new certificate for the session's host id and keeps both serials valid.
  The agent writes the result as a new generation. The controller revokes the
  previous serial at the first handshake with the new one, or at the previous
  certificate's expiry, whichever comes first.
- **Revocation** is by serial, checked at every handshake, and closes the
  host's live session immediately.

### 3. Enrollment (R23)

- **Actions** (core module, controller side):

| Action | Class | Surface | Input | Output |
|---|---|---|---|---|
| `core.host.create/v1` | compensable, compensated by `core.host.delete` | network | `name` (pattern `^[a-z0-9][a-z0-9.-]{0,62}$`), `controllerAddress` (optional `host:port`) | `EnrollmentGrant` |
| `core.host.token/v1` | irreversible | network | `hostId`, `controllerAddress` (optional) | `EnrollmentGrant` |
| `core.host.enroll-local/v1` | irreversible | controller socket (break-glass) | none | `EnrollmentGrant` for the local host, address `127.0.0.1:7443` |
| `core.host.revoke/v1` | irreversible, typed confirmation | network | `hostId` | `Host` |
| `core.host.delete/v1` | irreversible, typed confirmation | network | `hostId` | `Host` |

  `EnrollmentGrant` is `{"host": Host, "token": "<token>", "expiresAt":
  "<time>"}` with `token` marked `secret:"true"`, shown once. A request
  without `controllerAddress` uses `--agent-advertise`; with neither it fails
  with `request.invalid_input` naming `controllerAddress`.
  `core.host.token` is refused for an enrolled, unrevoked host and replaces
  any earlier token of that host. `core.host.enroll-local` is allowed in every
  state of the local host: when the local host is enrolled it first revokes
  its serials, so `system install` can always repair the local agent.
  `core.host.delete` refuses the local host and an enrolled host that is not
  revoked.
- **Token.** `dpe1.` followed by base64url (no padding) of the JSON object
  `{"a": "<host:port>", "f": "<hex SHA-256 of the agent root DER>", "s":
  "darkpanel://controller/<id>", "h": "<host id>", "t": "<128-bit secret,
  base64url>"}`. Table `core_enroll_tokens (host_id TEXT PRIMARY KEY
  REFERENCES core_hosts (id) ON DELETE CASCADE, hash BLOB NOT NULL,
  expires_at, consumed_at, created_at)` stores the SHA-256 of the secret (a
  128-bit random secret needs no slow hash). Tokens last one hour.
- **Consumption** is one controller write transaction: select the row for
  `h`; compare the stored hash with the presented secret's hash in constant
  time; require `consumed_at IS NULL`, `expires_at > now` and the host in
  state `pending` or `revoked`; `UPDATE core_enroll_tokens SET consumed_at = ?
  WHERE host_id = ? AND hash = ? AND consumed_at IS NULL` checking one row
  changed; insert the certificate row; set the host `enrolled`. A concurrent
  `core.host.token` or `core.host.revoke` serialises on the same database
  writer, so a replaced token can never consume its replacement. Every failure
  answers the single code `enroll.token_invalid`. Enrollment requests are rate
  limited per source address (10 per minute) and globally (60 per minute) with
  `enroll.rate_limited`. Tests cover concurrent replacement against
  enrollment and revocation against enrollment.
- **Token input** (ADR 0006: secrets are never flags or arguments).
  `darkpanel agent enroll` reads the token from a hidden prompt on the
  terminal, or from `--token-file <path>` (a root-owned file, mode 0600 or
  stricter, else refused). `install.sh` takes `--token-file` and otherwise
  prompts on `/dev/tty`, and passes the token to `darkpanel` through a pipe on
  standard input, never as an argument. The web "Add host" dialog shows the
  token and the command
  `curl -fsSL <install.sh URL> -o install.sh && sudo sh install.sh --role agent`,
  which prompts for it.
- **Flow.** `darkpanel agent enroll` (root) dials `a` with ALPN
  `darkpanel-enroll/1` and no client certificate. It requires the presented
  chain's root to have SHA-256 `f`, verifies the leaf against that root alone,
  and requires the URI SAN `s` and EKU `serverAuth`, before sending anything.
  It posts `{"token": "...", "csr": "<PEM>"}` to `POST /enroll/v1`. The
  controller answers `{"certificate": "<PEM>", "ca": "<PEM>"}`; the agent
  requires the returned `ca`'s SHA-256 to equal `f` and the certificate to
  carry `darkpanel://host/<h>` and chain to it, writes a new credential
  generation, and exits; the service then dials with ALPN
  `darkpanel-agent/1`.
- Host states: `pending` (created, never enrolled), `enrolled`, `revoked`.
  A revoked host re-enrolls with a new token.

### 4. Transport (R22, R24, R25, R27, ADR 0003)

Package `internal/transport` owns TLS policies, peer verification, the yamux
session and the heartbeat. It knows nothing about actions.

- **Agent listener.** Port 7443 on `127.0.0.1` always, plus the Tailscale
  interface by the same rule and watcher as the web listener (ADR 0006,
  `internal/controller/listen`), plus every `--agent-listen` address. It never
  binds all interfaces by default.
- **Controller TLS.** TLS 1.3 only. `GetConfigForClient` selects the policy by
  the client's offered ALPN; a client offering both or neither protocol is
  refused:
  - `darkpanel-agent/1`: `ClientAuth: RequireAnyClientCert` and a
    `VerifyPeerCertificate` that verifies the chain against the agent root
    alone (no system roots), requires EKU `clientAuth`, exactly one URI SAN of
    the form `darkpanel://host/<host id>` naming a host in state `enrolled`,
    and an unrevoked, unexpired serial from `core_agent_certs`. The verified
    host id is attached to the connection; nothing else identifies the peer.
    The host lookup is a callback, so the transport has no database access.
  - `darkpanel-enroll/1`: no client certificate; the connection serves only
    `POST /enroll/v1` and closes after one request.
- **Agent TLS.** TLS 1.3 only, ALPN `darkpanel-agent/1`, its certificate as
  client certificate. Because Go's hostname check does not match URI SANs, the
  agent sets `InsecureSkipVerify` and does the whole check in
  `VerifyConnection`: `x509.Verify` against `ca.crt` alone with EKU
  `serverAuth`, then the pinned controller SAN. A test proves a certificate
  from another authority, a missing SAN, a different SAN and a
  `clientAuth`-only certificate are each refused.
- **Session.** yamux over the TLS connection, the agent as yamux client.
  `AcceptBacklog` 64, `StreamOpenTimeout` 10 s, `StreamCloseTimeout` 30 s,
  yamux keep-alive off. The controller opens streams to the agent and sends
  HTTP/1.1 requests to the agent API (section 5); the agent opens streams to
  the controller for `POST /controller/v1/renew` only. At most 32 concurrent
  controller-opened and 4 agent-opened streams per session; a request beyond
  the limit waits for a slot until its context ends.
- **Heartbeat.** The controller pings each session every 15 s with a 10 s
  timeout. Any successful ping or completed request updates the host's
  `last_seen_at` (written at most once per 15 s). Three consecutive failed
  pings (45 s) close the session and the host is offline. The agent redials
  with exponential backoff from 1 s to 60 s with full jitter.
- **Session registry** (`internal/controller/fleet`): one live session per
  host; a new authenticated session for a host replaces the old one, which is
  closed. It implements `dispatch.ClientSource` (section 6): a host without a
  live session answers `host.offline` immediately (R27). Online state is the
  registry's, never stored; every host is offline when the controller starts.

### 5. Agent API, socket and offline operation (R24, R28, R31, M1 rows 22, 23, 92)

- **Agent API** (`internal/agent/api`): one `http.Handler` over the agent
  runtime, served on transport streams and on the agent socket, and one client
  implementing `core.AgentClient` over any dialer (a yamux session or the Unix
  socket). Routes, all under `/agent/v1/`; request and response bodies are the
  JSON of the `core` types the method takes and returns:

| Route | Body | `core.AgentClient` method |
|---|---|---|
| `GET hello` | — → `AgentHello` | `Hello` (frozen for protocol 1) |
| `POST actions/{name}/{version}/read` | input → output | `Read` |
| `GET resources/{module}/{resource}` | — → output | `Resource` |
| `POST actions/{name}/{version}/plan` | input → `PlanView` | `Plan` |
| `POST plans/{id}/apply` | `ApplyOptions` → `ChangeView` | `Apply` |
| `POST changes/{id}/confirm`, `POST changes/{id}/revert` | — → `ChangeView` | `Confirm`, `Revert` |
| `GET changes/{id}`, `GET changes?` (the `ChangeFilter` fields as query parameters) | — → `ChangeView`, `[]ChangeView` | `Change`, `Changes` |
| `GET modules/{id}/preflight` | — → `ModuleStatus` | `Preflight` |
| `GET journal?after=&limit=`, `POST journal/ack` | — → `[]JournalEntry`; `{"seq": n}` | `Journal`, `Ack` |

  Frozen for protocol version 1 (ADR 0003): `hello`, the plan, apply and read
  routes of `core.update.apply/1`, `core.update.rollback/1` and
  `core.update.status/1`, the yamux framing, and the headers below. ADR 0012
  records this table as the concrete form of ADR 0003's endpoint list.
- **Headers.** The controller sends `Darkpanel-Principal` (the JSON of
  `core.Principal`) on every request, and on `GET hello` also
  `Darkpanel-Protocol`, `Darkpanel-Version` and `Darkpanel-Controller-Id`, so
  the hello runs both ways. The agent accepts `Darkpanel-Principal` only on
  transport streams, whose peer is the authenticated controller; on the
  socket the principal is always `local:root@<host id>`. Bodies are limited to
  4 MiB; errors are RFC 9457 problem details that the client maps back to
  `errcode` codes.
- **Hello** gains, beside M1's fields: `arch`; `journalEpoch`; and
  `schemaHashes`, a map from `"<name>@<version>"` to `{"input": "<hex>",
  "output": "<hex>"}` for every agent-side action version.
- **Schema hashes** (row 22, ADR 0002). Snapshots keep M1's layout:
  `schemas/<action>/v<n>.json` for the current tree and
  `schemas/released/<tag>/<action>/v<n>.json` for released ones. A hash is the
  hex SHA-256 of the snapshot's `input` (or `output`) member re-encoded with
  Go's `encoding/json` `Marshal` after decoding it into `any` (sorted keys, no
  whitespace); the foundation adds test vectors for two committed snapshots.
  The binary embeds `schemas/` through a new package `schemas`
  (`schemas/embed.go`, `//go:embed` of everything under it except `embed.go`),
  so a hash maps to a schema. The dispatcher's rule per agent action:
  - hash equal to the controller's: dispatch as is;
  - a hash found in an embedded released snapshot of that action version:
    validate the input against that older schema with unknown properties
    refused, and answer `request.invalid_input` naming the first field the
    agent's schema lacks; otherwise dispatch;
  - an unknown hash: the action version is unavailable on that host with the
    reason `agent schema unknown`.
- **Version window** (R24, R25). An agent is supported when its protocol is 1,
  its major equals the controller's, and its minor equals the controller's or
  is one lower. An agent outside the window, older or newer (a newer agent
  exists only after a forced controller rollback), is shown as `supported:
  false` and receives only `core.update.*` actions; every other action
  answers `host.version_unsupported`.
- **Journal epoch** (row 92). The agent stores a ULID in its `core_meta` under
  `journal_epoch`, created with agent.db and replaced only when agent.db or
  its journal table is recreated; re-enrollment keeps it, so unacknowledged
  entries are not ingested twice. The controller's `core_journal_cursor` and
  `core_audit` gain an `epoch` column; the unique index on `core_audit`
  becomes `(host_id, epoch, seq)`. When Hello reports an epoch different from
  the cursor's, the ingester starts that host's cursor at `(epoch, 0)`. No
  release has shipped, so this migration has no previous binary to keep
  compatible with. Each host's ingestion runs under a 30 s deadline (row 92's
  second item).
- **Offline hosts in list paths.** The audit ingester and `dispatch.Changes`
  skip a host whose client answers `host.offline`, logging at debug level.
- **Agent socket** (row 23, R31). `darkpanel agent run` serves the agent API
  on `AgentSocket`, accepting only peers whose `SO_PEERCRED` uid is 0. It
  serves agent-side actions only, bypasses per-host module enablement (the
  controller dispatcher's rule) and still runs each module's preflight.
  Before enrollment the socket answers `hello` and read actions with actor
  `local:root@unenrolled` and refuses mutations with `agent.not_enrolled`.
- **CLI.** A global flag `--local` sends a generated agent-side command to the
  agent socket instead of the controller API, and refuses controller-side
  actions.
- **Controller unreachable** (R28). The agent keeps its database, last applied
  state, revert timers and socket; nothing in the agent waits on the
  controller except the dial loop.

### 6. Dispatch seam

`dispatch.Hosts` stays the `core.HostDirectory` and keeps recording module
state from Hello. Its client map becomes an interface, owned by the
foundation:

```go
// ClientSource yields the current agent client of a host.
type ClientSource interface {
    // Client returns the host's client and a generation that changes whenever
    // the host's session is replaced; Hosts re-runs Hello when it changes.
    // A host with no live session answers errcode.HostOffline.
    Client(ctx context.Context, hostID string) (core.AgentClient, uint64, error)
}
```

The foundation converts M1's `Add` map into `dispatch.StaticClients`, used by
`--embedded-agent` and tests; the fleet session registry is the production
implementation. `dispatch.Config.Hosts` and `server/services.go` change once,
in the foundation.

### 7. Install, service and uninstall (R29, R30, M1 rows 24, 49)

- **`install.sh`** (repository root, generated from `install.sh.in` by
  `scripts/gen-install-sh`, both owned by the foundation together with
  `make install-sh`; also attached to each release). POSIX `sh`, `set -eu`,
  checked by `shellcheck` in gate 2. It:
  1. requires root; reads `/etc/os-release` and accepts only `ubuntu` 22.04
     and 24.04 and `debian` 12 and 13, else exits 1 with
     `darkpanel supports Ubuntu 22.04 and 24.04 and Debian 12 and 13; this
     host is <PRETTY_NAME>`; maps `uname -m` `x86_64`→`amd64`,
     `aarch64`→`arm64`, else refuses;
  2. resolves the version (`--version`, else the newest `stable` entry of
     `releases.json`, section 9) and downloads into a `mktemp -d` directory,
     with `curl`, else `wget`: `darkpanel_<version>_linux_<arch>.tar.gz`,
     `checksums.txt` and every `checksums.txt.<keyid>.minisig` whose key id is
     embedded, from `--base-url` (default
     `https://github.com/Darkraise/darkpanel/releases/download/v<version>`;
     `releases.json` from the latest release's assets);
  3. verifies one signature with OpenSSL 3 only: algorithm `ED` (minisign
     hashed mode) and the embedded key id; `openssl dgst -blake2b512 -binary
     checksums.txt`; the 64-byte signature over that digest with `openssl
     pkeyutl -verify -pubin -inkey <key.pem> -rawin`; the global signature
     over `signature || trusted comment` the same way; the trusted comment
     exactly `version=<version> channel=<stable|beta> purpose=checksums` with
     the version being installed. Any one valid signature from an embedded key
     suffices. Byte slicing uses `base64 -d`, `head -c`, `tail -c` and `od`;
  4. checks the tarball's line of `checksums.txt` with `sha256sum -c`,
     extracts, copies the binary to `BinaryDir/darkpanel.new-install` (fsync),
     renames it over `BinaryDir/darkpanel`, creates `BinaryLink`, and runs
     `darkpanel system install --role <controller|agent>`, piping the token to
     its standard input for the agent role.

  `--base-url` exists for mirrors and the harness: the signature, key id and
  trusted-comment checks make the source irrelevant to trust.
- **`darkpanel system install --role controller|agent`** (root, package
  `internal/install`) is idempotent through checkpoints it re-derives from the
  host on every run, never from a progress file:
  1. directories with the owners and modes of section 1; the user `darkpanel`
     for the controller role (`useradd --system --no-create-home --shell
     /usr/sbin/nologin darkpanel`, skipped when it exists);
  2. role keys, generated only when absent;
  3. agent.db opened and migrated by the installer itself, and every
     core-owned path recorded in the ownership manifest (re-recording is a
     no-op);
  4. units and the drop-in written when their content differs,
     `systemctl daemon-reload`, services enabled;
  5. controller role: start the controller and wait for `GET /healthz` on the
     controller socket (60 s);
  6. enrollment, skipped when `AgentTLSDir/current` exists and verifies
     against its own `ca.crt`. Controller role: `core.host.enroll-local` over
     the controller socket, then the enrollment flow with that token, which
     also repairs a local host whose credentials were lost. Agent role: the
     token from standard input (the installer refuses to run without one when
     enrollment is needed); a consumed token without saved credentials is
     reported with `this host's token was used but no credentials were saved;
     revoke the host and create a new token`;
  7. start the agent; the controller role prints how to create the first admin
     over the controller socket.

  The exec allowlist (ADR 0009) gains `/usr/sbin/useradd`,
  `/usr/sbin/userdel` and `/usr/bin/systemctl`. Tests rerun the installer
  after each checkpoint and after a completed install.
- **Units**, embedded templates in `internal/install/units/`:
  - `darkpanel-controller.service`: `User=darkpanel`, `ExecStart=
    /usr/local/lib/darkpanel/darkpanel serve`,
    `RuntimeDirectory=darkpanel-controller`, `Restart=on-failure`,
    `RestartSec=2`, `StartLimitIntervalSec=60`, `StartLimitBurst=5`,
    `OnFailure=darkpanel-rollback.service`, `NoNewPrivileges=yes`,
    `ProtectSystem=strict`, `ReadWritePaths=/var/lib/darkpanel/controller`,
    `PrivateTmp=yes`.
  - `darkpanel-agent.service`: root, `ExecStart=… agent run`,
    `RuntimeDirectory=darkpanel-agent`, the same restart, start-limit and
    `OnFailure` lines.
  - `darkpanel-rollback.service` and `darkpanel-update-recover.service`:
    `Type=oneshot`, `ExecStart=/usr/local/lib/darkpanel/darkpanel.prev update
    recover`, `ConditionPathExists=/usr/local/lib/darkpanel/darkpanel.prev`;
    the recover unit adds `Before=darkpanel-controller.service
    darkpanel-agent.service` and `WantedBy=multi-user.target` and runs on
    every boot (ADR 0007; ADR 0012 records `update recover` as the name of
    ADR 0007's `update rollback --pending`).

  Five starts two seconds apart exhaust the start limit in well under 30 s,
  inside S5's two minutes.
- **Ownership manifest.** The agent migration widens `core_owned_files.kind`
  from `file`, `block` to `file`, `block`, `dir`, `user`, `unit` (a SQLite
  table rebuild). The core declares its owned paths like a module's
  `OwnedPaths`: the four units and the drop-in, `BinaryDir`, `BinaryLink`, the
  key files, the TLS, update, agent and controller directories, and the user
  `darkpanel`.
- **Duplicate ownership** (row 49). Registry validation refuses two modules,
  or a module and the core, declaring the same path or the same marked block;
  startup stops with the clashing ids.
- **`darkpanel uninstall [--keep-data] [--yes]`** (root). It first reads the
  whole manifest into memory and prints the plan. Order: stop and disable the
  controller and agent units; take the agent directory's lock (refusing while
  an agent runs); run each module's optional uninstall step
  (`core.Uninstaller`, agent half: `Uninstall(ctx, env, keepData bool)
  error`; the M3 firewall module uses it to offer keep or revert); remove
  marked blocks; remove files; remove units and the drop-in and
  `daemon-reload`; remove the binary and link. Without `--keep-data` it then
  removes `/var/lib/darkpanel`, `/etc/darkpanel` (keys, TLS generations) and
  the user, and closes and deletes agent.db last. With `--keep-data` it keeps
  `/var/lib/darkpanel` (databases, backups, update records), `/etc/darkpanel`
  (role keys, which the databases need, and agent credentials) and the user
  `darkpanel` (the owner of the kept controller files), so a reinstall finds
  everything. Each step prints one line; a failed step stops with the
  remaining steps listed. Tests cover `--keep-data` followed by a reinstall
  that reads the kept databases.

### 8. Self-update (R32–R36, ADR 0007)

- **Library** (`internal/update`): release index, download, verification,
  staging, self-test, the transaction record, swap, health check, rollback and
  stepping. Verification uses `aead.dev/minisign` against the embedded trusted
  keys (any one valid signature suffices).
- **Release index.** Every release publishes `releases.json` (section 9):
  `{"formatVersion": 1, "releases": [{"version": "1.3.0", "channel":
  "stable"}, …]}` listing every published version with its signed channel.
  The updater reads it from the newest release's assets, at most once an hour
  per process (cached), and never calls the GitHub API, so rate limits do not
  apply. The index only selects versions; trust comes from each version's own
  signature. The release base URL is embedded; only a `harnesskeys` build
  overrides it.
- **Channels.** A host subscribes to `stable` (only entries whose channel is
  `stable`) or `beta` (every entry). Ordering is semantic-version precedence
  (`1.3.0-beta.1 < 1.3.0`). Verification checks the trusted comment's
  `channel` against **the selected entry's** channel, so a beta host taking a
  stable release accepts its `stable` comment. A host on `beta` is never moved
  back to a lower stable release automatically.
- **Agent actions** (core module, agent side, types in `internal/core`,
  handlers in `internal/update/actions.go`):

| Action | Class | Input | Output |
|---|---|---|---|
| `core.update.apply/v1` | compensable, compensated by `core.update.rollback` | `version` (semantic version pattern), `channel` (`stable`, `beta`), `force` (bool) | `UpdateTransaction` |
| `core.update.rollback/v1` | irreversible | `force` (bool) | `UpdateTransaction` |
| `core.update.status/v1` | read | none | `{"transactions": [UpdateTransaction]}`, newest first, at most 20 |

  `UpdateTransaction` is the record of the next item without its internal
  fields: `id`, `parentId`, `fromVersion`, `toVersion`, `targetVersion`,
  `state`, `error` (code and message, or null), `startedAt`, `updatedAt`.
  The input carries no URL or key: the agent builds every download URL from
  its embedded base and enforces the downgrade rule (refused unless `force`; a
  forced downgrade goes back at most one minor) and the one-minor stepping
  rule itself, so a compromised controller cannot direct a root agent
  elsewhere.
- **Transaction record** `UpdateDir/<transaction id>.json` (ADR 0004 record
  format rule): `formatVersion` (1), the `UpdateTransaction` fields,
  `changeId` (the initiating change), `deadline`, `prevSha256`,
  `newSha256`. Every mutation of any record, and every rename in
  `BinaryDir`, happens while holding an exclusive `flock` on
  `UpdateDir/lock`; a writer re-reads the record under the lock before acting
  on its state. Records are written to a temporary file, fsynced and renamed.
- **Reservation** (Fable spec finding 1, Codex 1). The lifecycle gains
  `Env.Reserve(ctx, key, owner string) error`: inside the change's apply
  transaction it inserts `core_locks (key, owner)` after checking, in the same
  transaction, that no lock is held and no revertible change is unconfirmed;
  the lock survives the change and is released by its owner. A held `*` holds
  every key, and taking `*` requires that no key is held.
- **Apply returns before the restart.** `core.update.apply`'s Apply:
  re-checks the version rules; reserves `*` owned by a new transaction id;
  writes the record `pending` with `changeId`; starts the transient root unit
  `darkpanel-update-<tx>.service` through D-Bus `StartTransientUnit` with
  property `OnFailure=darkpanel-rollback.service`, running the current binary
  as `darkpanel update run <tx>`; returns. The runner's first step waits (up
  to 60 s) until `changeId` is `completed` in agent.db, so no restart can
  interrupt the initiating change; if it never completes, the runner records
  `failed` and releases the reservation. Tests cover a runner faster than
  Verify and a mutation attempted between Plan and Apply.
- **The runner** (`update run`) moves the record through `pending`,
  `downloading`, `staged`, `swapped` (with a deadline), `rolling-back`, then
  `committed`, `rolled-back` or `failed` (any failure before the swap). Under
  the update lock where it touches the record or `BinaryDir`:
  1. download into `UpdateDir`, verify, extract, copy the new binary to
     `BinaryDir/darkpanel.new-<tx>` (same filesystem as `darkpanel`), fsync,
     record `newSha256`; run `darkpanel.new-<tx> selftest`; record `staged`;
  2. copy the current binary to `darkpanel.prev.tmp`, fsync, rename over
     `darkpanel.prev`, fsync `BinaryDir`, record `prevSha256`;
  3. start the transient timer `darkpanel-update-deadline-<tx>.timer`
     (`OnActiveSec=90s`, running `darkpanel.prev update recover --deadline
     <tx>`), then record `swapped` with `deadline` = now + 90 s, then rename
     `darkpanel.new-<tx>` over `darkpanel`, fsync `BinaryDir`;
  4. restart `darkpanel-controller` when present, then `darkpanel-agent`;
  5. health check until the deadline: the controller's version and schema
     version from `GET /healthz` on its socket; the agent's version from
     `hello` on its socket and, when enrolled, a connected controller session;
     on the controller's host both must pass;
  6. on a pass, under the lock and only if the record is still `swapped`:
     record `committed`, stop the deadline timer, release the reservation.
     On a failure: roll back (next item).

  The deadline is 90 s after the swap, so swap, restart, check and rollback
  fit S5's two minutes. Because the timer is independent of the runner, a
  killed runner still rolls back on time.
- **Rollback** has one implementation, entered by the runner on a failed
  check, by `update recover --deadline <tx>` from the timer, by `update
  recover` from `darkpanel-rollback.service` (`OnFailure=` of either service
  or of the runner unit) and from `darkpanel-update-recover.service` at boot.
  Under the update lock it claims the transition `swapped → rolling-back`;
  only the claimant continues, every other caller sees a non-`swapped` state
  and does nothing. The claimant checks `darkpanel.prev` against
  `prevSha256` (a mismatch records `failed` with `update.prev_mismatch` and
  leaves the binaries alone), renames `darkpanel` to `darkpanel.failed` and
  `darkpanel.prev` to `darkpanel`, fsyncs, records `rolled-back`, releases the
  reservation, stops the deadline timer and restarts the services. A record in
  `rolling-back` found by a later `recover` (a crash mid-rollback) is
  finished from whichever rename is missing. Tests race two rollbacks, kill
  the runner after the swap, and interrupt at every filesystem step.
- **Lock release on every terminal path.** `committed`, `failed`,
  `rolled-back` each release `core_locks WHERE owner = <tx>`; at agent start a
  sweep deletes locks whose owner is a transaction id with no record or a
  terminal record. A test asserts `core_locks` is empty after each path.
- **Manual rollback** (R31). `darkpanel update rollback` is the generated
  command of `core.update.rollback`. With `--local` and no agent socket
  answering, it runs the rollback directly as root: it re-executes
  `darkpanel.prev update rollback --local --direct` when `darkpanel.prev`
  exists and the current process is not already `--direct` (no re-exec loop),
  which goes back one step from a committed record with the apply guards. On
  the controller's host it reads `core_hosts.agent_version` from
  `controller.db` directly and refuses while an enrolled agent reports a
  newer version, unless `--force`. Without `darkpanel.prev` it fails with
  `update.nothing_to_roll_back`. The CLI gains the two hooks this needs,
  owned by the foundation: hand-written leaf commands attached inside a
  generated group (`update run`, `update recover`), and a per-action local
  fallback used when `--local` finds no agent socket.
- **Stepping.** From 1.2 to 1.5 the runner runs one transaction per minor (the
  newest patch of 1.3, then 1.4, then 1.5), every record carrying the same
  `targetVersion`. After a commit the runner starts the next step's transient
  unit with the newly installed binary, with `parentId` set. A failure leaves
  the host on the last committed minor.
- **Channels and schedule** (R35). Per-host policy on the controller in
  `core_host_update (host_id TEXT PRIMARY KEY REFERENCES core_hosts (id) ON
  DELETE CASCADE, channel TEXT NOT NULL, days TEXT, time_utc TEXT,
  last_window TEXT)`, set by `core.host.update-policy/v1` (compensable,
  compensated by itself with the previous policy; input `hostId`, `channel`,
  `schedule`: null, or `{"days": ["mon", …], "time": "HH:MM"}` in UTC; output
  `UpdatePolicy` = the same fields). A window opens at `time` on each listed
  day and lasts two hours. A scheduler (`internal/controller/rollout`) checks
  once a minute: for a host inside an open window whose `last_window` is not
  this window, when the index has a newer version in its channel, it starts a
  one-host rollout as actor `system:update-schedule` and records
  `last_window`. Missed windows are not caught up. An agent host whose target
  is newer than the controller's version is skipped with a logged reason until
  the controller runs that version.
- **Fleet rollout** (R36). Controller-side actions, defined in
  `internal/controller/rollout/actions.go`:

| Action | Class | Input | Output |
|---|---|---|---|
| `core.update.rollout/v1` | compensable, compensated by `core.update.rollout-cancel` | `hosts` (host ids), `version`, `channel` | `Rollout` |
| `core.update.rollout-cancel/v1` | irreversible | `rolloutId` | `Rollout` |
| `core.update.rollouts/v1` | read | `limit` (optional, default 20) | `{"rollouts": [Rollout]}` |

  `Rollout` is `{id, version, channel, state ("running", "succeeded",
  "failed", "cancelled"), error, createdAt, updatedAt, hosts: [RolloutHost]}`;
  `RolloutHost` is `{hostId, position, state ("waiting", "updating",
  "committed", "failed", "skipped"), planId, transactionId, error}`.
  Tables `core_rollouts (id, version, channel, state, error, created_at,
  updated_at)` with a partial unique index on `state` where `state =
  'running'`, so the database itself refuses a second running rollout
  (`rollout.in_progress`), and `core_rollout_hosts (rollout_id, host_id,
  position, state, plan_id, transaction_id, error)`. The action's Apply
  inserts both (own host first when listed; refused when the target is newer
  than the controller's version and the own host is not listed) and returns.
- **Rollout driver** (`internal/controller/rollout`, actor `system:rollout`),
  started with the controller, drives each running rollout from its rows, one
  host at a time:
  1. if the host row has no `plan_id`: plan `core.update.apply` on the host
     and store `plan_id` before applying;
  2. apply that `plan_id`; after a lost response or a controller restart it
     reads the change of that plan on the agent (`Changes` filtered by plan)
     instead of applying again, and stores `transaction_id`;
  3. poll `core.update.status` every 5 s, treating `host.offline` and
     transport errors as "not yet", until the record chain of
     `transaction_id` reaches `committed` with `toVersion` equal to the
     rollout's version (next host), any record in it reaches `rolled-back` or
     `failed` (the host and the rollout become `failed`; the driver stops), or
     10 minutes pass (`failed`).

  A controller restart during its own host's update resumes from the rows.
  `rollout-cancel` marks the rollout `cancelled`; the host being updated
  finishes its own transaction. Tests cover concurrent rollout submission,
  stop at first failure, resume after restart, and a stepped update that
  advances only on the target version.

### 9. Release pipeline (R80, R32)

- **Release preparation.** `make release-prep VERSION=x.y.z` copies every
  snapshot under `schemas/` except `schemas/released/` and `embed.go` to
  `schemas/released/vx.y.z/` and adds a changelog section; the owner commits
  it and pushes a signed tag `vx.y.z`. Snapshots under `schemas/released/` are
  immutable (ADR 0002).
- **`.github/workflows/release.yml`** on tags `v*`, third-party actions pinned
  by commit SHA:
  1. `verify`: `git verify-tag` against `release/allowed_signers` (the owner's
     SSH signing key); `schemas/released/<tag>/` exists and equals the current
     snapshots (the same file set `release-prep` copies); the tag is valid
     semantic versioning.
  2. `build`: clean checkout, pinned Go toolchain, `-trimpath`, linux/amd64 and
     linux/arm64 tarballs (binary, `LICENSE`, `README.md`), `checksums.txt`, a
     CycloneDX SBOM (`darkpanel_<version>.cdx.json`), release notes from
     `git-cliff` (`cliff.toml`, conventional commits), `releases.json` (the
     previous release's index plus this version, channel `beta` when the
     version has a prerelease part, else `stable`); GitHub artifact
     attestations for every file.
  3. `sign`, in the protected environment `release` (owner approval required):
     `gh attestation verify` for every artifact against this repository and
     the tag's commit; `darkpanel version --json` of the amd64 binary reports
     `testBuild: false` and `trustedKeys` equal to the production key ids;
     sign `checksums.txt` with every key in the environment, each producing
     `checksums.txt.<keyid hex>.minisig` with trusted comment
     `version=<version> channel=<stable|beta> purpose=checksums`.
  4. `publish`: a GitHub Release (marked prerelease for a prerelease version)
     with the tarballs, `checksums.txt`, every `.minisig`, the SBOM,
     `releases.json` and `install.sh`.
- **Trusted keys.** `internal/update/trustedkeys` is a data-only package owned
  by the foundation: the production key list (ADR 0007: dual signing for at
  least two minor releases across a rotation; verifiers accept any one valid
  signature from a trusted key). A file behind the build tag `harnesskeys`
  replaces the list with the harness test key and overrides the release base
  URL; `darkpanel version --json` gains `trustedKeys` and `testBuild`.
  `install.sh`'s embedded keys are generated from the same list by
  `scripts/gen-install-sh`; a unit test fails when they differ.
- **Policy.** `docs/versioning.md`: semantic versioning, the compatibility
  rules of ADR 0002 and ADR 0007 in user terms, channels, the agent support
  window, the stepping rule, and that a database backup is taken before every
  update's migrations and is never restored automatically (R34).
- **Makefile.** `VERSION` and `COMMIT` fall back to `dev` and `unknown` outside
  a git checkout (tooling register row 9).
- **Owner prerequisites**, done once by the owner, never by an agent: generate
  the minisign key pair; create the `release` environment with the owner as
  required reviewer and store the secret key and its password there; add the
  public key to `trustedkeys`; add the tag-signing public key to
  `release/allowed_signers`. Until then the workflow's `verify` and `sign`
  jobs fail closed.

### 10. Web host management (R27)

- The hosts page lists each host with online state, last seen, agent and
  protocol version, OS and architecture, `supported`, state and enabled
  modules.
- An "Add host" dialog takes the name and controller address and shows the
  token once with a copy button and the install command of section 3.
- The host page adds revoke and delete (typed confirmation), a new enrollment
  token, the update policy, "update now" (a one-host rollout) and the
  transaction history from `core.update.status`; a rollouts page starts a
  rollout and shows per-host progress.
- The pages use the generic action endpoints and the shapes in the
  Contracts section; the assembly part's schema snapshot test catches any
  drift.

### 11. Error codes and actors

New `errcode` codes: `host.offline`, `host.version_unsupported`,
`agent.not_enrolled`, `enroll.token_invalid`, `enroll.rate_limited`,
`update.in_progress`, `update.unconfirmed_changes`,
`update.signature_invalid`, `update.downgrade_refused`, `update.not_found`,
`update.prev_mismatch`, `update.nothing_to_roll_back`, `rollout.in_progress`.
New actors: `system:rollout`, `system:update-schedule`,
`local:root@unenrolled`. New reserved top-level CLI names: `agent`, `system`,
`uninstall`.

### 12. ADR 0012

ADR 0012 "M2 contract and layout additions" records: the install script's
verification method (closing the index's deferred decision); the agent route
table and headers of section 5; the schema-hash definition and dispatch rule;
the journal epoch; `Env.Reserve` and the lock key `*`; the update action
shapes, the apply-returns-before-restart rule, the update lock and the
rollback claim; `update recover` as ADR 0007's `update rollback --pending`;
per-key signature files and `releases.json`; the new actors and reserved CLI
names; the accepted MPL-2.0 dependency; and the layout additions
`internal/transport/`, `internal/agent/api/`, `internal/agent/daemon/`,
`internal/agent/socket/`, `internal/controller/fleet/`,
`internal/controller/rollout/`, `internal/install/`, `internal/update/`,
`internal/update/trustedkeys/`, `internal/platform/systemd.go`,
`schemas/embed.go`. It is Proposed when written and Accepted when this
milestone closes, as ADRs 0010 and 0011 were.

### 13. Integration harness (M2 exit criteria)

- **Topology.** M2 tests boot three VMs per OS job, after the M1 tests' VMs
  shut down: controller (`198.18.0.10`), agent (`198.18.0.11`) and scanner
  (`198.18.0.20`) on the existing "internet" multicast link, plus a second
  multicast link standing in for the tailnet with the controller at
  `10.77.0.10/24` and the agent at `10.77.0.11/24`. The controller is
  installed with `--agent-listen 10.77.0.10:7443`, so it listens on that
  address and on `127.0.0.1:7443`; UFW allows 7443/tcp on the private link
  only. From the scanner, both hosts' internet addresses still show exactly
  `22/tcp` open, IPv4 and IPv6.
- **Builds.** The harness build job builds with `-tags harnesskeys` signed
  test releases with distinct versions inside one minor above the current
  one, `N` (for example `0.2.0`): `N+1.0` good, `N+1.1` with
  `harnessbroken` (passes `selftest`, fails its health report), `N+1.2` with
  `harnesscrash` (passes `selftest`, exits at start under `serve` and
  `agent run`), `N+1.3` good, used for the reboot scenario, and `N+1.4` with
  `harnessmigration` (adds one additive agent and controller migration). A
  test minisign key signs them; the scanner serves the files and a
  `releases.json` over plain HTTP on `198.18.0.20:8000`, the base URL of the
  `harnesskeys` build and of `install.sh --base-url`, with the test key
  substituted (`make install-sh KEYS=harness`).
- **Budget.** Each VM gets 2 GiB of memory. The job timeout rises to 120
  minutes and `go test` to 100 minutes; both are inferences, measured on the
  first run and recorded in `docs/superpowers/gates.md`.
- **Scenarios**, per OS, each a named subtest, with the version each host runs
  afterwards:
  1. install `N`: `install.sh --role controller` on the controller,
     `install.sh --role agent --token-file` on the agent; the harness creates
     the host through the authenticated API (an admin user created over the
     controller socket and an API token). Both run `N`;
  2. self-management: `hostinfo.summary` of the controller's own host and of
     the agent host through the API, both over mTLS; the host list shows both
     online with versions;
  3. offline: stop the agent service; the host is offline within 60 s and an
     action fails with `host.offline`; restart; it returns;
  4. revoke: `core.host.revoke` closes the live session and the agent's
     redials are refused; `core.host.token` and `agent enroll` re-enroll it;
  5. update good: rollout to `N+1.0`; between the controller's and the agent's
     update, `hostinfo.summary` on the agent host (one minor behind) works
     (S5); both `committed`, each within two minutes; `core_locks` empty.
     Both run `N+1.0`;
  6. health failure: rollout to `N+1.1` rolls the controller's host back
     automatically within two minutes, `darkpanel.failed` exists,
     `core_locks` empty, the rollout is `failed` and the agent was not
     touched. Both run `N+1.0`;
  7. crash loop: rollout to `N+1.2` rolls back through `OnFailure=`. Both run
     `N+1.0`;
  8. reboot in window: rollout to `N+1.3`, reboot the controller host during
     the health window; the record ends `rolled-back`. Both run `N+1.0`;
  9. runner killed: during a rollout to `N+1.3`, kill the runner after
     `swapped`; the deadline timer rolls back within two minutes;
  10. migration and rollback: rollout to `N+1.4` with a health failure
      injected after migrations; the backup exists, `N+1.0` runs on the
      migrated databases, rows written by `N+1.4` before the rollback remain;
  11. break-glass: with the controller stopped, `darkpanel --local
      hostinfo summary` works on the agent; with both services stopped,
      `darkpanel update rollback --local` on the agent returns it from `N+1.0`
      to `N`;
  12. uninstall: on the agent, nothing listed in the manifest remains, the
      units are gone; on the controller, `uninstall --keep-data` then a
      reinstall of `N` finds the kept databases.
- `sqlite3` joins the provisioned packages so scenarios can read
  `core_locks`.

## Contracts

Cross-part contracts: the paths and flags of section 1; the token format,
`EnrollmentGrant` and token consumption of section 3; the route table and
headers of section 5; `ClientSource` (section 6); the action tables of
sections 3 and 8; the transaction record and update lock (section 8);
`releases.json` and the signature file names (section 9); the error codes,
actors and reserved names (section 11); and these shapes and columns:

- `Host` (output of `core.host.list`, `core.host.revoke`, `core.host.delete`;
  M1's fields plus additions): `{id, name, isLocal, state ("pending",
  "enrolled", "revoked"), online, lastSeenAt (or null), agentVersion,
  protocolVersion, os, arch, supported, modules}`; the four version and
  platform fields are null until the first Hello.
- `core_hosts` gains `state TEXT NOT NULL DEFAULT 'enrolled'`,
  `enrolled_at`, `last_seen_at`, `agent_version`, `protocol_version`, `os`,
  `arch` (all nullable except `state`; the M1 local host row becomes
  `enrolled`).
- `UpdateTransaction`, `UpdatePolicy`, `Rollout`, `RolloutHost`: section 8.
- Migrations: controller `0005_fleet.sql` (P1: `core_hosts` columns,
  `core_agent_certs`, `core_enroll_tokens`), `0006_journal_epoch.sql` (P9),
  `0007_update.sql` (P11: `core_host_update`, `core_rollouts`,
  `core_rollout_hosts`); agent `0005_ownership_kinds.sql` (P4),
  `0006_journal_epoch.sql` (P10).

The foundation creates the Go types for every action input and output above
(in `internal/core/fleet.go`), the error codes, actors and reserved names, the
paths, `ClientSource` and `StaticClients`, the Hello fields, `Env.Reserve`
and the lock key `*`, the `core.Uninstaller` interface, the duplicate-
ownership rule, the CLI hooks of section 8, `internal/update/trustedkeys`,
`schemas/embed.go` and the `install.sh` generator.

## Parallel decomposition

| Part | Wave | Owns | Consumes |
|---|---|---|---|
| P0 Foundation | 0 | `go.mod`, `go.sum`, `internal/deps/`, `internal/core/*.go` (fleet types, Hello fields, `Uninstaller`), `internal/core/lifecycle/` (`Env.Reserve`, key `*`), `internal/core/errcode/`, `internal/paths/`, `internal/registry/` and the duplicate-ownership rule, `internal/controller/dispatch/` (`ClientSource`, `StaticClients`), `internal/controller/server/services.go`, `internal/cli/` (the two hooks, reserved names), `internal/platform/systemd.go` (D-Bus: transient services and timers, start, stop, restart, enable, disable, reload, state) and the exec allowlist entries, `internal/update/trustedkeys/`, `schemas/embed.go`, `install.sh.in`, `scripts/gen-install-sh`, `docs/adr/0012-*.md`, `docs/adr/README.md` | this spec |
| P1 Agent authority | 1 | `internal/controller/fleet/ca*.go`, `certs*.go`, `tokens*.go`; `migrations/controller/0005_fleet.sql` | P0 |
| P2 Transport | 1 | `internal/transport/` | P0 |
| P3 Agent API | 1 | `internal/agent/api/` | P0 |
| P4 Install | 1 | `internal/install/` (system install, units, uninstall), `internal/platform/ownership.go`, `migrations/agent/0005_ownership_kinds.sql`, the generated `install.sh` and its fixture tests | P0 (`trustedkeys`, generator) |
| P5 Update library | 1 | `internal/update/` except `actions.go` and `trustedkeys/` | P0 |
| P6 Release workflow | 1 (may start beside P0) | `.github/workflows/release.yml`, `.github/workflows/ci.yml` (shellcheck step), `cliff.toml`, `release/`, `docs/versioning.md`, `Makefile` (release, release-prep, install-sh targets, VERSION fallback) | the generator path and `trustedkeys` path fixed by this spec |
| P7 Web hosts | 1 | `web/src/pages/hosts*.tsx`, `host*.tsx`, `rollouts*.tsx`, `web/src/api/types.ts`, `web/src/app.tsx` (the rollouts route), their tests | the Contracts shapes |
| P8 Fleet service | 2 | `internal/controller/fleet/` except P1's files (listener wiring, session registry, enrollment and renewal endpoints, revocation), `internal/controller/listen/` | P1, P2, P3 |
| P9 Host actions and dispatch | 2 | `internal/controller/coreactions/` (the `core.host.*` actions including `update-policy`), `internal/controller/dispatch/` (version window, schema negotiation, offline skip), `internal/controller/audit/` (epoch, ingestion deadline), `migrations/controller/0006_journal_epoch.sql` | P0, P3 |
| P10 Agent daemon | 2 | `internal/agent/daemon/` (dial loop, enrollment client, renewal, credential generations), `internal/agent/socket/`, `internal/agent/runtime/` (epoch, lock sweep), `migrations/agent/0006_journal_epoch.sql` | P2, P3, P5 |
| P11 Update actions and rollout | 2 | `internal/update/actions.go`, `internal/controller/rollout/` (rollout actions, driver, scheduler), `migrations/controller/0007_update.sql` | P5, P0 |
| P12 Assembly | 3 | `internal/controller/server/`, `internal/controller/api/` (health detail fields), `cmd/darkpanel/` (`serve` flags, `agent`, `system install`, `uninstall`, `selftest`, `update run`, `update recover`, the `harnessbroken`, `harnesscrash` and `harnessmigration` build-tag files), `internal/cli/` (`--local`, the update local fallback), `internal/modules/modules.go` and registry wiring, `schemas/` snapshots, end-to-end tests over a loopback transport, `README.md`, `CONTRIBUTING.md` | everything |
| P13 Harness | 3, after P12 | `test/harness/`, `.github/workflows/harness.yml`, `docs/superpowers/gates.md` | P12, P6's Makefile targets |

Migration paths are under `internal/core/store/`.

- Wave 0 runs alone: it owns the dependency manifest and every shared
  interface. P6 shares nothing with P0 and may start beside it.
- Wave 1 runs P1–P7 together; wave 2 runs P8–P11 together; wave 3 runs P12,
  then P13. A part consumes only P0's interfaces and earlier waves' merged
  code; where it needs a sibling's behaviour it codes against the P0 interface
  with a fake.
- Files a later part takes over (`internal/controller/dispatch/` and
  `internal/cli/` from P0 to P9 and P12, `internal/controller/fleet/` from P1
  to P8, `Makefile` from P6 to P13) have one owner per wave.
- Risk parts, each its own task with a risk-level review: P0's lifecycle
  change, P1, P2, P4, P5, P8, P10, P11.
- Test isolation: as M1 — `t.TempDir()` databases, keys and TLS material, port
  `0` on `127.0.0.1`, socket paths from `os.MkdirTemp("", "dp")`, the
  `coretest` fake clock; D-Bus, systemd and `flock` paths are behind
  interfaces or temporary directories in unit tests and exercised for real
  only in the harness. Harness changes are verified before merge by pushing
  the work branch and dispatching `harness.yml`.

## Plans

1. Foundation: P0.
2. Security libraries: P1, P2, P5.
3. Agent API and install: P3, P4.
4. Release workflow and web pages: P6, P7 (P6 may start beside plan 1; P7
   after plan 1).
5. Services: P8, P9, P10, P11.
6. Assembly and harness: P12, P13.

Plans 2, 3 and 4 run in parallel sessions after plan 1.

## Verification

- Gates 1–7 green on the branch and on `main`; gate 7 includes the M2
  scenarios on all four operating systems.
- Unit tests required beside the code:
  - enrollment: token reuse, expiry and wrong secret all answering
    `enroll.token_invalid`; concurrent token replacement and revocation
    against enrollment; a CSR naming another host id or with a bad signature;
    a returned `ca` with the wrong fingerprint;
  - transport: each TLS refusal case of section 4; a revoked serial refused
    and its live session closed;
  - credentials: renewal and enrollment interrupted between every write and
    the `current` switch, then restarted;
  - dispatch: schema negotiation with a fake previous-minor Hello; the version
    window including a newer agent; journal epoch reset; offline hosts skipped;
  - update: one-minor stepping, forced downgrade limits, dual-signed key
    rotation (old-only, new-only and overlapping trust lists against the
    published file layout), a replayed older `checksums.txt`, beta-to-stable
    promotion; the runner waiting for its change; two rollbacks racing; the
    runner killed after the swap; interruption at every filesystem step;
    `core_locks` empty after every terminal path; migration backup taken
    during `update run`;
  - rollout: concurrent submission refused; stop at the first failure; resume
    after a restart with a stored `planId`; a stepped update advancing only on
    the target version; schedule windows, `lastWindow` and the skip rule;
  - install: reruns after each checkpoint; duplicate ownership refused;
    uninstall order; `--keep-data` then reinstall; `install.sh` keys equal the
    trusted keys; `install.sh` against a local fixture server with the test
    key, including a bad signature, a wrong key id, a mismatched
    trusted-comment version and a checksum mismatch;
  - sockets: peer-credential refusal on the agent socket; `--local` refusing
    controller-side actions; the update local fallback.

## Rejected alternatives

- Running the local agent in-process in production: rejected; ADR 0005 runs
  the controller unprivileged and the agent as root, and R26 wants one code
  path.
- Modelling an update as a change that completes after the restart: rejected;
  startup recovery marks it interrupted.
- Sending only schema hashes and marking any mismatch unavailable: rejected;
  every additive field would break R25.
- Storing online state in `core_hosts`: rejected; a controller crash would
  leave hosts online.
- A pinned minisign binary or a required `minisign` package for
  `install.sh`: rejected by the owner in favour of OpenSSL alone.
- `xtaci/smux` (MIT) instead of yamux: rejected by the owner; ADR 0003 freezes
  yamux framing.
- Cron syntax for update schedules: rejected; a weekly two-hour window in UTC
  covers v1.
- A pull request opened by the release job to freeze schemas: rejected; the
  tagged commit must already contain its snapshot.
- Renaming the update actions to avoid the CLI clash with `darkpanel update
  rollback`: rejected; ADR 0003 freezes `core.update.apply` and
  `core.update.rollback` for protocol 1. The CLI hooks of section 8 solve it.
- The GitHub API as the release source: rejected; unauthenticated rate limits
  and no harness equivalent. `releases.json` serves both.
- A configuration file for the agent listener and advertise address:
  rejected; flags in a unit drop-in follow M1's `--listen`.

## Out of scope (recorded as register rows)

- Controller backup and restore (ADR 0005): M8 Production rollout.
- The firewall module's uninstall step: M3, through `core.Uninstaller`.
- The web certificate through `tailscale cert`: M5 (M1 row 21).

## Review log

- Spec panel round 1, 2026-10-19. Fable: rework; executability 11, coherence
  13, coverage 15, assumptions 13; 24 findings. Codex gpt-6-astra: rework;
  executability 10, coherence 12, coverage 15, assumptions 10; 19 findings.
  Every finding accepted and folded in: the update lock and rollback claim;
  the deadline timer and runner `OnFailure`; staging on the binary's
  filesystem with `prevSha256`; `Env.Reserve` and the runner's wait for its
  change; the durable rollout reservation, stored `planId` and target-version
  completion; token consumption in one transaction; credential generations;
  installer checkpoints; `--keep-data` contents; tokens never as arguments;
  action surfaces (`core.host.enroll-local`); per-key signatures; the
  signed-channel check; the existing schema layout and hash bytes; complete
  shapes, camelCase and the missing actions; settings as flags with loopback
  always bound; harness versions, fixtures and new scenarios; the CLI hooks;
  `releases.json`; trusted keys and the generator in the foundation; web and
  CI ownership; `/healthz` reuse; the ingestion deadline; the epoch kept
  across re-enrollment; the verification list.
