# M2 epics: controller-agent fleet, install and uninstall, self-update, release pipeline

**Date:** 2026-10-19
**Status:** design reviewed by Fable 2026-10-19 (verdict rework; all 18
findings accepted by the owner and folded in); awaiting the Fable and Codex
spec panel
**Product spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Requirements:** R22–R36, R80; M1 register rows 22, 23, 24, 49, 92
**Execution strategy:** parallel — one foundation part fixes every shared
type, interface, dependency, error code, path and migration owner first; then
the authority, transport, agent API, install, update and release parts and the
web pages share no file and run together, followed by the controller fleet,
host actions, agent daemon and rollout parts, and a final assembly part
followed by the harness (owner standing preference: favour parallel to reach
v1 sooner).

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
- Every Fable design-review finding (2026-10-19) is accepted.

## Versions

Checked against the Go module proxy on 2026-10-19. Pins are exact; the
foundation plan re-checks them and records any change in its Assumptions.

| Package | Version | Licence | Use |
|---|---|---|---|
| `github.com/hashicorp/yamux` | v0.1.2 | MPL-2.0 | stream multiplexing on the agent connection (ADR 0003) |
| `aead.dev/minisign` | v0.3.0 | MIT | release signature verification (ADR 0007) |
| `github.com/godbus/dbus/v5` | v5.2.4 | BSD-2-Clause | systemd D-Bus: transient units, unit control |

Release tooling, pinned in `release.yml` by version and checksum at plan time:
`git-cliff` (changelog), `cyclonedx-gomod` (SBOM), the `minisign` command built
from `aead.dev/minisign/cmd/minisign` at the pinned version.

## Design

### 1. Processes, users and paths (R26, M1 row 24)

- `darkpanel serve` is the controller only. It runs as the system user
  `darkpanel` (ADR 0005) and no longer opens `agent.db` or the agent key.
- `darkpanel agent run` is the agent daemon, as root. On the controller's host
  it dials `127.0.0.1:7443` like any remote agent, so every host goes through
  the same transport and handler (R26).
- `serve --dev` keeps development paths and allows a non-root user, as in M1.
  The M1 in-process agent stays behind a separate flag, `--embedded-agent`,
  valid only with `--dev`, so development and the existing end-to-end tests
  keep working. `darkpanel agent run --dev` runs the real daemon against the
  development layout, so the real transport can be exercised locally.
  Production units never pass either flag.
- `internal/paths` gains, with production values:

| Field | Production | Owner and mode |
|---|---|---|
| `AgentSocket` | `/run/darkpanel-agent/agent.sock` | root, socket 0600, directory 0700 |
| `AgentTLSDir` | `/etc/darkpanel/agent/` (holds `agent.key`, `agent.crt`, `ca.crt`, `controller.json`) | root, 0700, files 0600 |
| `UpdateDir` | `/var/lib/darkpanel/update/` (transaction records, staged binaries) | root, 0700 |
| `BinaryDir` | `/usr/local/lib/darkpanel/` (`darkpanel`, `darkpanel.prev`, `darkpanel.failed`) | root, 0755, binaries 0755 |
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
  from the authority after two thirds of its life. Agents pin the SAN, not the
  certificate.
- Agent certificates: Ed25519 keys generated on the agent, one year, EKU
  `clientAuth` only, URI SAN `darkpanel://host/<host id>`, no DNS or IP SANs.
- Table `core_agent_certs (serial TEXT PRIMARY KEY, host_id, not_before,
  not_after, revoked_at, created_at)`. Serials are 128-bit random.
- **Renewal.** After two thirds of its life the agent sends a CSR over its
  live session (`POST /controller/v1/renew`, section 4). The controller issues
  a new certificate for the session's host id, ignoring any identity in the
  CSR, and keeps both serials valid. The agent writes key and certificate
  atomically (temporary file, fsync, rename, fsync the directory) before using
  them. The controller revokes the previous serial at the first handshake with
  the new one, or at the previous certificate's expiry, whichever comes first.
- **Revocation** is by serial, checked at every handshake, and closes the
  host's live session immediately.

### 3. Enrollment (R23)

- **Actions** (core module, controller side, so REST, CLI and audit come from
  the generic code):

| Action | Class | Input | Output |
|---|---|---|---|
| `core.host.create/v1` | compensable, compensated by `core.host.delete` | `name` (pattern `^[a-z0-9][a-z0-9.-]{0,62}$`), `controller_address` (optional `host:port`; default the `agent.advertise` setting) | `host`, `token` (`secret:"true"`, shown once) |
| `core.host.token/v1` | irreversible | `host_id`, `controller_address` (optional) | `token` (`secret:"true"`) |
| `core.host.revoke/v1` | irreversible, typed confirmation | `host_id` | `host` |
| `core.host.delete/v1` | irreversible, typed confirmation | `host_id` | `host` |

  `core.host.token` is break-glass (served on the controller socket), refused
  for an enrolled, unrevoked host, and invalidates any earlier token of that
  host. `core.host.delete` refuses the local host and an enrolled host that is
  not revoked. The `agent.advertise` setting has no default; a token request
  without an address and without the setting fails with
  `request.invalid_input` naming `controller_address`.
- **Token.** `dpe1.` followed by base64url (no padding) of the JSON object
  `{"a": "<host:port>", "f": "<hex SHA-256 of the agent root DER>", "s":
  "darkpanel://controller/<id>", "h": "<host id>", "t": "<128-bit secret,
  base64url>"}`. The table `core_enroll_tokens (host_id PRIMARY KEY, hash,
  expires_at, consumed_at, created_at)` stores the SHA-256 of the secret (a
  128-bit random secret needs no slow hash). Tokens last one hour. The
  controller compares hashes in constant time and consumes atomically with
  `UPDATE … SET consumed_at = ? WHERE host_id = ? AND consumed_at IS NULL AND
  expires_at > ?` checking one row changed. Every failure (unknown, expired,
  consumed, wrong secret) answers the single code `enroll.token_invalid`.
  Enrollment attempts are rate limited per source address (10 per minute) and
  globally (60 per minute) with `enroll.rate_limited`.
- **Flow.** `darkpanel agent enroll <token>` (root) dials `a` with ALPN
  `darkpanel-enroll/1` and no client certificate. It requires the server's
  chain to end in a root whose SHA-256 is `f`, verifies the leaf against that
  root alone, and requires the URI SAN `s` and EKU `serverAuth`, before sending
  anything. It then posts `{token, csr}` to `POST /enroll/v1`. The controller
  verifies and consumes the token, issues a certificate for the bound host id
  `h`, and answers `{certificate, ca}`. The agent writes `agent.key`,
  `agent.crt`, `ca.crt` and `controller.json` (`{address, san}`) atomically,
  replaces its journal epoch (section 5), and exits; the service then dials
  with ALPN `darkpanel-agent/1`.
- **The controller's own agent** enrolls through the same flow. `darkpanel
  system install --role controller` (section 7) calls `core.host.token` for the
  local host over the controller socket as root, with address
  `127.0.0.1:7443`, then runs the enrollment. No token is copied by hand.
- Host states: `pending` (created, never enrolled), `enrolled`, `revoked`.
  A revoked host re-enrolls with a new token from `core.host.token`.

### 4. Transport (R22, R24, R25, R27, ADR 0003)

Package `internal/transport` owns TLS configuration, peer verification, the
yamux session and the heartbeat. It knows nothing about actions.

- **Agent listener.** Default port 7443. It binds 127.0.0.1 and the Tailscale
  interface by default, by the same rule and watcher as the web listener
  (ADR 0006, `internal/controller/listen`); any other bind address is explicit
  configuration (`agent.listen`), and it never binds all interfaces by
  default.
- **Controller TLS.** TLS 1.3 only. `GetConfigForClient` selects the policy
  by the client's offered ALPN; a client offering both or neither protocol is
  refused:
  - `darkpanel-agent/1`: `ClientAuth: RequireAnyClientCert` and a
    `VerifyPeerCertificate` that verifies the chain against the agent root
    alone (no system roots), requires EKU `clientAuth`, exactly one URI SAN of
    the form `darkpanel://host/<host id>` naming a host in state `enrolled`,
    and an unrevoked, unexpired serial from `core_agent_certs`. The verified
    host id is attached to the connection; nothing else identifies the peer.
  - `darkpanel-enroll/1`: no client certificate; the connection serves only
    `POST /enroll/v1` and closes after one request.
- **Agent TLS.** TLS 1.3 only, ALPN `darkpanel-agent/1`, its certificate as
  client certificate. `RootCAs` holds only `ca.crt`. Because Go's hostname
  check does not match URI SANs, the agent sets `InsecureSkipVerify` and does
  the whole check in `VerifyConnection`: `x509.Verify` against `ca.crt` alone
  with EKU `serverAuth`, then the pinned controller SAN from
  `controller.json`. A test proves a certificate from another authority, a
  missing SAN, a different SAN and a `clientAuth`-only certificate are each
  refused.
- **Session.** yamux over the TLS connection, the agent as yamux client.
  `AcceptBacklog` 64, `StreamOpenTimeout` 10 s, `StreamCloseTimeout` 30 s,
  yamux keep-alive off (the heartbeat below replaces it). The controller opens
  streams to the agent and sends HTTP/1.1 requests to the agent API
  (section 5); the agent opens streams to the controller for
  `POST /controller/v1/renew` only. At most 32 concurrent controller-opened
  and 4 agent-opened streams per session; a request beyond the limit waits
  for a slot until its context ends.
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
  socket). Routes, all under `/agent/v1/`:

| Route | `core.AgentClient` method |
|---|---|
| `GET hello` | `Hello` (frozen for protocol 1) |
| `POST actions/{name}/{version}/read` | `Read` |
| `GET resources/{module}/{resource}` | `Resource` |
| `POST actions/{name}/{version}/plan` | `Plan` |
| `POST plans/{id}/apply` | `Apply` |
| `POST changes/{id}/confirm`, `POST changes/{id}/revert` | `Confirm`, `Revert` |
| `GET changes/{id}`, `GET changes` | `Change`, `Changes` |
| `GET modules/{id}/preflight` | `Preflight` |
| `GET journal?after=&limit=`, `POST journal/ack` | `Journal`, `Ack` |

  The routes `hello`, `actions/core.update.apply/1/*`,
  `actions/core.update.rollback/1/*` and `actions/core.update.status/1/read`
  and the yamux framing are frozen for protocol version 1 (ADR 0003). ADR 0012
  records this table as the concrete form of ADR 0003's endpoint list.
- **Principal.** The controller sends the caller in the header
  `Darkpanel-Principal` (the JSON of `core.Principal`). The agent accepts it
  only on transport streams, whose peer is the authenticated controller; on
  the socket the principal is always `local:root@<host id>`. Bodies are
  limited to 4 MiB; errors are RFC 9457 problem details that the client maps
  back to `errcode` codes.
- **Hello** gains, beside M1's fields: `arch`; `journal_epoch`; and per action
  version `schema_hashes` (`{"<name>@<version>": {"input": "<hex>", "output":
  "<hex>"}}`). The controller sends its own protocol, binary version and
  controller id in request headers of `GET hello`, so the exchange runs both
  ways.
- **Schema hashes** (row 22, ADR 0002). A hash is the hex SHA-256 of the
  canonical schema snapshot bytes that `schemagen` writes for that action
  version's `input` or `output`. The binary embeds `schemas/` (current and
  `released/**`) through a new package `schemas` (`schemas/embed.go`), so a
  hash maps to a schema. The dispatcher's rule per agent action:
  - hash equal to the controller's: dispatch as is;
  - a hash the controller knows from an embedded released snapshot: validate
    the input against that older schema with unknown properties refused, and
    answer `request.invalid_input` naming the first field the agent's schema
    lacks; otherwise dispatch;
  - an unknown hash: the action version is unavailable on that host with the
    reason `agent schema unknown`.
- **Version window** (R24, R25). An agent is supported when its protocol is 1,
  its major equals the controller's, and its minor equals the controller's or
  is one lower. An agent outside the window, older or newer (a newer agent
  exists only after a forced controller rollback), is shown with state
  `unsupported` and receives only `core.update.*` actions; every other action
  answers `host.version_unsupported`.
- **Journal epoch** (row 92). The agent stores a ULID in its `core_meta` under
  `journal_epoch`, created with agent.db and replaced at every enrollment and
  whenever the journal table is recreated. The controller's
  `core_journal_cursor` and `core_audit` gain an `epoch` column; the unique
  index on `core_audit` becomes `(host_id, epoch, seq)`. When Hello reports an
  epoch different from the cursor's, the ingester starts that host's cursor at
  `(epoch, 0)`. No release has shipped, so this migration has no previous
  binary to keep compatible with.
- **Offline hosts in list paths.** The audit ingester and
  `dispatch.Changes` skip a host whose client answers `host.offline`, logging
  at debug level, instead of failing the tick or the list.
- **Agent socket** (row 23, R31). `darkpanel agent run` serves the agent API
  on `AgentSocket`, accepting only peers whose `SO_PEERCRED` uid is 0. It
  serves agent-side actions only, bypasses per-host module enablement (that is
  the controller dispatcher's rule) and still runs each module's preflight.
  Before enrollment the host has no id: the socket answers `hello` and read
  actions with actor `local:root@unenrolled` and refuses mutations with
  `agent.not_enrolled`.
- **CLI.** A global flag `--local` sends a generated agent-side command to the
  agent socket instead of the controller API, and refuses controller-side
  actions. Break-glass paths: agent actions over the agent socket with the
  controller stopped; `darkpanel user reset-totp` over the controller socket
  with the agent stopped; `darkpanel update rollback` with neither service
  running (section 8).
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

- **`install.sh`** (repository root, also attached to each release). POSIX
  `sh`, `set -eu`, checked by `shellcheck` in gate 2. It:
  1. requires root; reads `/etc/os-release` and accepts only `ubuntu` 22.04
     and 24.04 and `debian` 12 and 13, else exits 1 with
     `darkpanel supports Ubuntu 22.04 and 24.04 and Debian 12 and 13; this
     host is <PRETTY_NAME>`; maps `uname -m` `x86_64`→`amd64`,
     `aarch64`→`arm64`, else refuses;
  2. resolves the version (`--version`, else the latest stable release from
     the GitHub API) and downloads into a `mktemp -d` directory, with `curl`,
     else `wget`: `darkpanel_<version>_linux_<arch>.tar.gz`, `checksums.txt`,
     `checksums.txt.minisig`, from `--base-url` (default
     `https://github.com/Darkraise/darkpanel/releases/download/v<version>`);
  3. verifies the signature with OpenSSL 3 only. It requires the signature
     algorithm `ED` (minisign hashed mode) and a key id equal to one of the
     embedded keys; computes `openssl dgst -blake2b512 -binary checksums.txt`;
     verifies the 64-byte signature over that digest with `openssl pkeyutl
     -verify -pubin -inkey <key.pem> -rawin`; verifies the global signature
     over `signature || trusted comment` the same way; and requires the trusted
     comment to be exactly `version=<version> channel=<stable|beta>
     purpose=checksums` with the version it is installing. Byte slicing uses
     `base64 -d`, `head -c`, `tail -c` and `od` from coreutils;
  4. checks the tarball's line of `checksums.txt` with `sha256sum -c`,
     extracts, installs the binary to `BinaryDir/darkpanel` (0755) and the
     link `BinaryLink`, and runs `darkpanel system install --role
     <controller|agent> [--token <token>]`.

  The embedded public keys (PEM of the Ed25519 key, with its 8-byte minisign
  key id) are generated from the same list as the binary's trusted keys
  (section 9); a unit test fails when they differ. `--base-url` exists for
  mirrors and the harness: the signature, key id and trusted-comment checks
  make the source irrelevant to trust, and they never trust TLS alone.
- **`darkpanel system install --role controller|agent`** (root, idempotent,
  package `internal/install`). For both roles it creates directories with the
  owners and modes of section 1, generates the role keys when absent, opens
  and migrates agent.db itself (the agent has not run yet), records every
  core-owned path in the ownership manifest, writes the units, runs
  `systemctl daemon-reload` and enables the services. The controller role also
  creates the system user `darkpanel` (`useradd --system --no-create-home
  --shell /usr/sbin/nologin darkpanel`), starts the controller, waits for its
  health on the controller socket (60 s), enrolls the local agent (section 3)
  and starts the agent, then prints how to create the first admin over the
  controller socket. The agent role enrolls with `--token`, then starts the
  agent. The exec allowlist (ADR 0009) gains `/usr/sbin/useradd`,
  `/usr/sbin/userdel` and `/usr/bin/systemctl`.
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
  - `darkpanel-rollback.service`: `Type=oneshot`, `ExecStart=
    /usr/local/lib/darkpanel/darkpanel.prev update rollback --pending`,
    `ConditionPathExists=/usr/local/lib/darkpanel/darkpanel.prev`.
  - `darkpanel-update-recover.service`: the same command, `Type=oneshot`,
    `Before=darkpanel-controller.service darkpanel-agent.service`,
    `WantedBy=multi-user.target`, run on every boot (ADR 0007).

  Five starts two seconds apart exhaust the start limit in well under 30 s,
  inside S5's two minutes.
- **Ownership manifest.** The agent migration widens `core_owned_files.kind`
  from `file`, `block` to `file`, `block`, `dir`, `user`, `unit` (a SQLite
  table rebuild). The core declares its owned paths like a module's
  `OwnedPaths`: the four units, `BinaryDir`, `BinaryLink`, the key files, the
  TLS, update, agent and controller directories, and the user `darkpanel`.
- **Duplicate ownership** (row 49). Registry validation refuses two modules,
  or a module and the core, declaring the same path or the same marked block;
  startup stops with the clashing ids.
- **`darkpanel uninstall [--keep-data] [--yes]`** (root). Order: stop and
  disable the controller and agent units; take the agent directory's lock
  (refusing while an agent runs); read the whole manifest; run each module's
  optional uninstall step (`core.Uninstaller`, agent half: `Uninstall(ctx,
  env, keepData bool) error`; the M3 firewall module uses it to offer keep or
  revert); remove marked blocks, then files, then units and
  `daemon-reload`, then directories (data directories kept with
  `--keep-data`), the binary and link, the user; delete agent.db last. Each
  step prints one line; a failed step stops with the remaining steps listed.

### 8. Self-update (R32–R36, ADR 0007)

- **Library** (`internal/update`): release listing, download, verification,
  staging, self-test, the transaction record, swap, health check, rollback and
  stepping. Verification uses `aead.dev/minisign` against the embedded trusted
  keys and checks the trusted comment's version, channel and purpose against
  what the agent requested, and the tarball name against its own arch.
- **Release source.** GitHub Releases of `github.com/Darkraise/darkpanel`.
  Channel `stable` lists releases without a prerelease part; `beta` lists all.
  Ordering is semantic-version precedence (`1.3.0-beta.1 < 1.3.0`). A host on
  `beta` is never moved back to a lower stable release automatically. The
  release base URL is embedded; only a `harnesskeys` build overrides it
  (section 9).
- **Agent actions** (core module, agent side, defined in `internal/update`):

| Action | Class | Input | Output |
|---|---|---|---|
| `core.update.apply/v1` | compensable, compensated by `core.update.rollback` | `version` (semantic version pattern), `channel` (`stable`, `beta`), `force` (bool) | `transaction_id`, `from`, `to`, `state` |
| `core.update.rollback/v1` | irreversible | `force` (bool) | `transaction_id`, `state` |
| `core.update.status/v1` | read | none | the last 20 transaction records |

  The input carries no URL or key: the agent builds every download URL from
  its embedded base and enforces the downgrade rule (refused unless `force`; a
  forced downgrade goes back at most one minor) and the one-minor stepping
  rule itself, so a compromised controller cannot direct a root agent
  elsewhere.
- **Apply returns before the restart** (Fable finding 1). `core.update.apply`'s
  Apply only checks the guards (no unconfirmed revertible change, no
  transaction in progress, version and channel allowed), writes the
  transaction record in state `pending`, and starts the transient root unit
  `darkpanel-update-<transaction id>.service` through D-Bus
  `StartTransientUnit`, running the current binary as `darkpanel update run
  <transaction id>`. Verify checks the unit was started or the record has left
  `pending`. Completion is observed through `core.update.status`, never
  through the change, so the agent's restart never interrupts a change in
  `applying`.
- **The runner** (`update run`) moves the record through `pending`,
  `downloading`, `staged`, `swapped` (with a deadline), then `committed`, or
  `rolled-back`, or `failed` (any failure before the swap). Steps follow
  ADR 0007: take the lock key `*` owned by the transaction id; download and
  verify; run `<staged binary> selftest`; record `swapped`, keep the current
  binary as `darkpanel.prev`, rename the staged binary over `darkpanel`;
  restart `darkpanel-controller` when present, then `darkpanel-agent`; health
  check. The health deadline is 90 s after the swap, so swap, restart, check
  and rollback fit S5's two minutes. Health: the controller reports the new
  version and schema version on its socket (`GET /local/v1/health`); the agent
  reports the new version on its socket and, when enrolled, a connected
  controller session; on the controller's host both must pass.
- **Lock key `*`.** The lifecycle treats a held `*` as holding every key, and
  taking `*` requires that no key is held. Every terminal path deletes
  `core_locks WHERE owner = <transaction id>` from agent.db (Fable finding 2):
  `committed`; `failed` before the swap; `rolled-back` from the health check or
  deadline; `rollback --pending` from the rollback or boot-recovery unit; a
  manual rollback; and a runner killed before the swap, which the next
  `update run`, `rollback --pending` or agent start finds. At agent start a
  sweep deletes locks whose owner is a transaction id with no record or a
  record in a terminal state. A test asserts `core_locks` is empty after each
  path.
- **Rollback** of a `swapped` record runs at most once: it renames
  `darkpanel.prev` back, keeps the failed binary as `darkpanel.failed`,
  records `rolled-back`, releases the locks and restarts the services.
  `committed` and `rolled-back` records make the automatic path a no-op.
  `darkpanel update rollback` (root, no service needed) re-executes
  `darkpanel.prev` when it exists, so the binary that wrote the record reads
  it; after a commit it goes back one step with the apply guards. On the
  controller's host it is refused while an enrolled agent reports a newer
  version, unless `--force`.
- **Stepping.** From 1.2 to 1.5 the runner runs one transaction per minor (the
  latest patch of 1.3, then 1.4, then 1.5). After a commit the runner starts
  the next step's transient unit with the newly installed binary; each record
  names its parent. A failure leaves the host on the last committed minor.
- **Channels and schedule** (R35). Per-host policy on the controller in
  `core_host_update (host_id, channel, days, time_utc)`, set by
  `core.host.update-policy/v1` (compensable; input `host_id`, `channel`,
  `schedule`: null, or `{days: [mon…sun], time: "HH:MM"}` in UTC). A
  controller scheduler checks once a minute; inside a host's window, when a
  newer release exists in its channel, it starts a one-host rollout as actor
  `system:update-schedule`. The controller's own host goes first: an agent
  host never receives a version newer than the controller's.
- **Fleet rollout** (R36). `core.update.rollout/v1` (controller side,
  compensable, compensated by `core.update.rollout-cancel/v1`; input `hosts`,
  `version`, `channel`) takes the controller lock key `core.rollout`, so two
  rollouts never overlap; its Apply writes `core_rollouts` and
  `core_rollout_hosts` rows (own host first when listed; refused when the
  target is newer than the controller's version and the own host is not
  listed) and returns. A rollout driver (`internal/controller/rollout`), actor
  `system:rollout`, sends `core.update.apply` to one host at a time and polls
  `core.update.status` every 5 s until `committed` (next host), `rolled-back`
  or `failed` (the rollout stops as `failed`), or 10 minutes pass (stops as
  `failed`). When the controller restarts during its own host's update, the
  driver resumes from the stored rows. `core.update.rollouts/v1` (read) lists
  rollouts with per-host progress.

### 9. Release pipeline (R80, R32)

- **Release preparation.** `make release-prep VERSION=x.y.z` copies
  `schemas/current/` to `schemas/released/vx.y.z/` and adds a changelog
  section; the owner commits it and pushes a signed tag `vx.y.z`. Snapshots
  under `schemas/released/` are immutable (ADR 0002).
- **`.github/workflows/release.yml`** on tags `v*`, third-party actions pinned
  by commit SHA:
  1. `verify`: `git verify-tag` against `release/allowed_signers` (the owner's
     SSH signing key); `schemas/released/<tag>/` exists and equals
     `schemas/current/`; the version in the tag is valid semver.
  2. `build`: clean checkout, pinned Go toolchain, `-trimpath`, linux/amd64 and
     linux/arm64 tarballs (binary, `LICENSE`, `README.md`), `checksums.txt`, a
     CycloneDX SBOM (`darkpanel_<version>.cdx.json`), release notes from
     `git-cliff` (`cliff.toml`, conventional commits); GitHub artifact
     attestations for every file.
  3. `sign`, in the protected environment `release` (owner approval required):
     `gh attestation verify` for every artifact against this repository and
     the tag's commit; `darkpanel version --json` of the amd64 binary reports
     `test_build: false` and `trusted_keys` equal to the production key ids;
     sign `checksums.txt` with trusted comment `version=<version>
     channel=<stable|beta> purpose=checksums`.
  4. `publish`: a GitHub Release (marked prerelease when the version has a
     prerelease part) with the tarballs, `checksums.txt`,
     `checksums.txt.minisig`, the SBOM and `install.sh`.
- **Trusted keys.** `internal/update/trustedkeys` holds the production key
  list (ADR 0007: dual signing for at least two minor releases across a
  rotation). A file behind the build tag `harnesskeys` replaces the list with
  the harness test key and overrides the release base URL; `darkpanel version
  --json` gains `trusted_keys` and `test_build`. `install.sh`'s embedded keys
  are generated from the same list (`make install-sh`).
- **Policy.** `docs/versioning.md`: semantic versioning, the compatibility
  rules of ADR 0002 and ADR 0007 in user terms, channels, the agent support
  window and the stepping rule.
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
  protocol version, OS and architecture, supported or `unsupported`, and
  enabled modules.
- An "Add host" dialog takes the name and controller address and shows the
  token once with a copy button and the install command
  `curl -fsSL <install.sh URL> | sudo sh -s -- --role agent --token <token>`.
- The host page adds revoke and delete (typed confirmation), new enrollment
  token, update policy, "update now" and the transaction history from
  `core.update.status`; a rollouts page starts a rollout and shows per-host
  progress.
- The pages use the generic action endpoints and the JSON shapes in this
  spec's action tables; the assembly part's schema snapshot test catches any
  drift.

### 11. Error codes and actors

New `errcode` codes: `host.offline`, `host.version_unsupported`,
`agent.not_enrolled`, `enroll.token_invalid`, `enroll.rate_limited`,
`update.in_progress`, `update.unconfirmed_changes`,
`update.signature_invalid`, `update.downgrade_refused`, `update.not_found`,
`rollout.in_progress`. New actors: `system:rollout`,
`system:update-schedule`, `local:root@unenrolled`.

### 12. ADR 0012

ADR 0012 "M2 contract and layout additions" records: the install script's
verification method (closing the index's deferred decision); the agent route
table of section 5; the `Darkpanel-Principal` header; the schema-hash
definition and dispatch rule; the journal epoch; the lock key `*`; the update
action shapes and the apply-returns-before-restart rule; the new actors; the
accepted MPL-2.0 dependency; and the layout additions `internal/transport/`,
`internal/agent/api/`, `internal/agent/daemon/`, `internal/agent/socket/`,
`internal/controller/fleet/`, `internal/controller/rollout/`,
`internal/install/`, `internal/update/`, `internal/platform/systemd`,
`schemas/embed.go`. It is Proposed when written and Accepted when this
milestone closes, as ADRs 0010 and 0011 were.

### 13. Integration harness (M2 exit criteria)

- **Topology.** M2 tests boot three VMs per OS job, after the M1 tests' VMs
  shut down: controller (`198.18.0.10`), agent (`198.18.0.11`) and scanner
  (`198.18.0.20`) on the existing "internet" multicast link, plus a second
  multicast link standing in for the tailnet with the controller at
  `10.77.0.10/24` and the agent at `10.77.0.11/24`. The controller binds its
  agent listener to `10.77.0.10:7443` by explicit configuration and UFW allows
  7443/tcp on that link only. From the scanner, both hosts' internet
  addresses still show exactly `22/tcp` open, IPv4 and IPv6.
- **Builds.** The harness build job builds with `-tags harnesskeys` three
  signed test releases: the current version `N`, `N+1` (next minor, good), and
  two broken `N+1` variants from build tags: `harnessbroken` fails its health
  report, `harnesscrash` exits at start. A test minisign key signs them; the
  scanner serves them over plain HTTP on `198.18.0.20:8000`, the base URL of
  the `harnesskeys` build and of `install.sh --base-url`, with the test key
  substituted (`make install-sh KEYS=harness`).
- **Budget.** Each VM gets 2 GiB of memory. The job timeout rises to 120
  minutes and `go test` to 100 minutes; both are inferences, measured on the
  first run and recorded in `docs/superpowers/gates.md`.
- **Scenarios**, per OS, each a named subtest:
  1. install: `install.sh --role controller` on the controller,
     `install.sh --role agent --token` on the agent (token from
     `darkpanel host create` over the controller socket);
  2. self-management: `hostinfo.summary` of the controller's own host and of
     the agent host through the API, both over mTLS; the host list shows both
     online with versions;
  3. offline: stop the agent service; the host is offline within 60 s and an
     action fails with `host.offline`; restart; it returns;
  4. revoke: `core.host.revoke` closes the live session and the agent's
     redials are refused; `core.host.token` and `agent enroll` re-enroll it;
  5. update good: rollout to `N+1` updates the controller's host, then the
     agent; both `committed`, each within two minutes; `core_locks` empty;
  6. health failure: `N+1` broken rolls back automatically within two
     minutes; `darkpanel.failed` exists; `core_locks` empty;
  7. crash loop: `N+1` crash rolls back through `OnFailure=`;
  8. reboot in window: rebooting during the health window ends `rolled-back`;
  9. break-glass: with the controller stopped, `darkpanel --local
     hostinfo summary` works on the agent; with both stopped,
     `darkpanel update rollback` works;
  10. uninstall: on the agent, nothing listed in the manifest remains, the
      units and user are gone.
- `sqlite3` joins the provisioned packages so scenarios can read
  `core_locks`.

## Contracts

The tables in sections 3, 5, 8 and 11, the `ClientSource` interface in
section 6, the paths table in section 1 and the token format in section 3 are
the cross-part contracts. The foundation creates the Go types for every action
input and output listed (in `internal/core/fleet.go`), the error codes, the
paths, `ClientSource` and `StaticClients`, the Hello fields, the lock key `*`
semantics, the `core.Uninstaller` interface and the duplicate-ownership rule.

## Parallel decomposition

| Part | Wave | Owns | Consumes |
|---|---|---|---|
| P0 Foundation | 0 | `go.mod`, `go.sum`, `internal/deps/`, `internal/core/*.go` (fleet types, Hello fields, `Uninstaller`, lock key `*` in `internal/core/lifecycle/`), `internal/core/errcode/`, `internal/paths/`, `internal/registry/` and the duplicate-ownership rule, `internal/controller/dispatch/` (`ClientSource`, `StaticClients`), `internal/controller/server/services.go`, `internal/platform/systemd.go` (D-Bus provider) and the exec allowlist entries, `schemas/embed.go`, `docs/adr/0012-*.md`, `docs/adr/README.md` | this spec |
| P1 Agent authority | 1 | `internal/controller/fleet/ca*.go`, `certs*.go`, `tokens*.go`; `migrations/controller/0005_fleet.sql` (host columns, `core_agent_certs`, `core_enroll_tokens`) | P0 |
| P2 Transport | 1 | `internal/transport/` (TLS policies and verifiers taking host-lookup callbacks, yamux session, heartbeat) | P0 |
| P3 Agent API | 1 | `internal/agent/api/` (handler and `core.AgentClient` client over any dialer) | P0 |
| P4 Install | 1 | `internal/install/` (system install, units, uninstall), `install.sh`, `internal/platform/ownership.go`, `migrations/agent/0005_ownership_kinds.sql` | P0 |
| P5 Update library | 1 | `internal/update/` except `actions.go`, `internal/update/trustedkeys/` | P0 |
| P6 Release workflow | 1 | `.github/workflows/release.yml`, `cliff.toml`, `release/`, `docs/versioning.md`, `Makefile` (release, release-prep, install-sh targets, VERSION fallback) | spec only |
| P7 Web hosts | 1 | `web/src/pages/hosts*.tsx`, `host*.tsx`, `rollouts*.tsx` and their tests | section 10 shapes |
| P8 Fleet service | 2 | `internal/controller/fleet/` except P1's files (listener wiring, session registry, enrollment and renewal endpoints, revocation), `internal/controller/listen/` | P1, P2, P3 |
| P9 Host actions and dispatch | 2 | `internal/controller/coreactions/`, `internal/controller/dispatch/` (version window, schema negotiation, offline skip), `internal/controller/audit/` (epoch), `migrations/controller/0006_journal_epoch.sql` | P0, P3 |
| P10 Agent daemon | 2 | `internal/agent/daemon/`, `internal/agent/socket/`, `internal/agent/runtime/` (epoch, lock sweep), `migrations/agent/0006_journal_epoch.sql` | P2, P3, P5 |
| P11 Update actions and rollout | 2 | `internal/update/actions.go`, `internal/controller/rollout/`, `migrations/controller/0007_update.sql` (`core_host_update`, `core_rollouts`, `core_rollout_hosts`) | P5, P0 |
| P12 Assembly | 3 | `internal/controller/server/`, `internal/controller/api/` (`GET /local/v1/health` on the controller socket), `cmd/darkpanel/` (including `selftest`, `update run` and `update rollback`, and the `harnessbroken` and `harnesscrash` build-tag files), `internal/cli/` (`--local`), `internal/modules/modules.go` and registry wiring, `schemas/` snapshots, end-to-end tests over a loopback transport, `README.md`, `CONTRIBUTING.md` | everything |
| P13 Harness | 3, after P12 | `test/harness/`, `.github/workflows/harness.yml`, `docs/superpowers/gates.md` | P12, P6's Makefile targets |

Migration paths are under `internal/core/store/`.

- Wave 0 runs alone: it owns the dependency manifest and every shared
  interface. P6 shares nothing with P0 and may start beside it.
- Wave 1 runs P1–P7 together; wave 2 runs P8–P11 together; wave 3 runs P12,
  then P13. A part consumes only P0's interfaces and earlier waves' merged
  code; where it needs a sibling's behaviour it codes against the P0 interface
  with a fake.
- Files a later part takes over (`internal/controller/dispatch/` from P0 to
  P9, `internal/controller/fleet/` from P1 to P8, `Makefile` from P6 to P13)
  have one owner per wave.
- Risk parts, each its own task with a risk-level review: P0's lifecycle lock
  change, P1, P2, P4, P5, P8, P10, P11.
- Test isolation: as M1 — `t.TempDir()` databases, keys and TLS material, port
  `0` on `127.0.0.1`, socket paths from `os.MkdirTemp("", "dp")`, the
  `coretest` fake clock; D-Bus and systemd are behind interfaces faked in unit
  tests and exercised for real only in the harness. Harness changes are
  verified before merge by pushing the work branch and dispatching
  `harness.yml`.

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
- Unit tests required beside the code: token reuse, expiry and wrong secret
  all answering `enroll.token_invalid`; a CSR naming another host id; a
  revoked serial refused and its live session closed; renewal interrupted
  before the agent saved the new certificate; each TLS refusal case of
  section 4; schema negotiation with a fake previous-minor Hello; the version
  window including a newer agent; journal epoch reset; one-minor stepping and
  forced downgrade limits; dual-signed key rotation; a replayed older
  `checksums.txt`; `core_locks` empty after every update terminal path;
  duplicate ownership refused; `install.sh` keys equal the trusted keys;
  `install.sh` against a local fixture server with the test key, including a
  bad signature, a wrong key id, a mismatched trusted-comment version and a
  checksum mismatch.

## Rejected alternatives

- Running the local agent in-process in production: rejected; ADR 0005 runs
  the controller unprivileged and the agent as root, and R26 wants one code
  path.
- Modelling an update as a change that completes after the restart: rejected
  (Fable finding 1); startup recovery marks it interrupted.
- Sending only schema hashes and marking any mismatch unavailable: rejected
  (Fable finding 4); every additive field would break R25.
- Storing online state in `core_hosts`: rejected; a controller crash would
  leave hosts online.
- A pinned minisign binary or a required `minisign` package for
  `install.sh`: rejected by the owner in favour of OpenSSL alone.
- `xtaci/smux` (MIT) instead of yamux: rejected by the owner; ADR 0003 freezes
  yamux framing.
- Cron syntax for update schedules: rejected; a weekly day-and-time window in
  UTC covers v1.
- A pull request opened by the release job to freeze schemas: rejected; the
  tagged commit must already contain its snapshot.

## Out of scope (recorded as register rows)

- Controller backup and restore (ADR 0005): M8 Production rollout.
- The firewall module's uninstall step: M3, through `core.Uninstaller`.
- The web certificate through `tailscale cert`: M5 (M1 row 21).
