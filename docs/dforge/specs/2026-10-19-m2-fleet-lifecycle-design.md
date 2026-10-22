# M2 epics: controller-agent fleet, install and uninstall, self-update, release pipeline

**Date:** 2026-10-19
**Status:** approved by the owner 2026-10-19, after a Fable design review
and three Fable and Codex gpt-6-astra spec panel rounds (see the review log
at the end)
**Product spec:** docs/dforge/specs/2026-10-17-darkpanel-design.md
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
  `darkpanel` (ADR 0005), refuses to run as root without `--dev` (it would
  create root-owned files the unit could not open), and no longer opens
  `agent.db` or the agent key or creates the agent's directories.
- `darkpanel agent run` is the agent daemon, as root. On the controller's host
  it dials `127.0.0.1:7443` like any remote agent, so every host goes through
  the same transport and handler (R26).
- `serve --dev` keeps development paths and allows a non-root user, as in M1.
  The M1 in-process agent stays behind a separate flag, `--embedded-agent`,
  valid only with `--dev` and refused with `--agent-listen` (it opens no agent
  listener), so development and the existing end-to-end tests keep working.
  `darkpanel agent run --dev` runs the real daemon against the development
  layout, with an updater that never manages systemd units, so its update
  actions fail. Production units never pass either flag.
- **Settings are flags**, as M1's `--listen` is; there is still no
  configuration file. `serve` gains:
  - `--agent-listen <addr:port>` (repeatable): extra agent-listener binds,
    **added** to the always-bound `127.0.0.1:7443` and the Tailscale address
    (section 4). Unlike the web `--listen`, it never replaces loopback,
    because the controller's own agent dials it, and it refuses an
    unspecified address.
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
| `BinaryDir` | `/usr/local/lib/darkpanel/` (`darkpanel`, `darkpanel.prev`, `darkpanel.recovery`, `darkpanel.failed`, `darkpanel.new-<tx>`) | root, 0755, binaries 0755 |
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
  point leaves a complete old or a complete new generation. Writers of a
  generation (`agent enroll` and the daemon's renewal) hold an exclusive
  `flock` on `AgentTLSDir/save.lock` from reading `current` to the final
  fsync; a renewal whose session generation is no longer current writes
  nothing.
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
| `core.host.token/v1` | irreversible, typed confirmation (the host name) | network | `hostId`, `controllerAddress` (optional) | `EnrollmentGrant` |
| `core.host.enroll-local/v1` | irreversible, typed confirmation (the host name) | controller socket (break-glass) | none | `EnrollmentGrant` for the local host, address `127.0.0.1:7443` |
| `core.host.revoke/v1` | irreversible, typed confirmation | network | `hostId` | `Host` |
| `core.host.delete/v1` | irreversible, typed confirmation | network | `hostId` | `Host` |

  `EnrollmentGrant` is `{"host": Host, "token": "<token>", "expiresAt":
  "<time>"}` with `token` marked `secret:"true"`, shown once. A request
  without `controllerAddress` uses `--agent-advertise`; with neither it fails
  with `request.invalid_input` naming `controllerAddress`.
  Host names are unique. `core.host.token` is refused for the local host and
  for an enrolled, unrevoked host, and replaces any earlier token of that
  host. `core.host.enroll-local` is allowed in every
  state of the local host: in one controller write transaction it revokes the
  local host's serials, sets its state to `revoked` (a fresh local host is
  `pending`), and replaces its token, so `system install` can always repair
  the local agent.
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
  A revoked host re-enrolls with a new token. The controller inserts its local
  host row as `pending` at first start (`ensureLocalHost`); the migration sets
  an existing M1 local row to `pending`.

### 4. Transport (R22, R24, R25, R27, ADR 0003)

Package `internal/transport` owns TLS policies, peer verification, the yamux
session and the heartbeat. It knows nothing about actions.

- **Agent listener.** Port 7443 on `127.0.0.1` always, plus the Tailscale
  interface by the same rule as the web listener and a watcher of its own
  (ADR 0006, `internal/controller/listen`), whatever the web `--listen` says,
  plus every `--agent-listen` address. It never binds all interfaces.
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
  socket). Routes, all under `/agent/v1/`. Bodies are explicit wire types in
  `internal/agent/wire` (owned by the foundation), never the untagged `core`
  structs: camelCase fields, times in RFC 3339 UTC, durations as integer
  seconds (`confirmTimeoutSeconds`), nullable fields as JSON `null`, with
  conversion functions to and from the `core` types. The table names each
  wire type by its `core` counterpart:

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
- **Hello** gains, beside M1's fields: `arch`; `journalEpoch`;
  `controllerConnected` (bool, null when not enrolled); and
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
  - an unknown or missing hash: the action version answers
    `module.unavailable` on that host with the reason `agent schema unknown`.
- **Version window** (R24, R25). An agent is supported when its protocol is 1,
  its major equals the controller's, and its minor equals the controller's or
  is one lower. An agent outside the window, older or newer (a newer agent
  exists only after a forced controller rollback), is shown as `supported:
  false` and receives only `core.update.*` actions; every other action
  answers `host.version_unsupported`.
- **Journal epoch** (row 92). The agent stores a ULID in its `core_meta` under
  `journal_epoch`, created by the runtime when it opens agent.db and finds
  none (a migration that rebuilds `core_journal` would delete such a row, so
  no agent migration creates it), and replaced only when agent.db or its
  journal table is recreated; re-enrollment keeps it, so unacknowledged
  entries are not ingested twice. The controller's `core_journal_cursor` and
  `core_audit` gain an `epoch` column; the unique index on `core_audit`
  becomes `(host_id, epoch, seq)`. When Hello reports an epoch different from
  the cursor's, the ingester starts that host's cursor at `(epoch, 0)`. No
  release has shipped, so this migration has no previous binary to keep
  compatible with. The ingester's in-memory acknowledgement map is keyed by
  `(host, epoch)`, and the audit insert's conflict target becomes `(host_id,
  epoch, seq)` with the index; the epoch-reset test covers both. Each host's
  ingestion runs under a 30 s deadline (row 92's second item).
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
  actions. `update` is already a reserved hand-written top-level name
  (`internal/core/registry.go`), so the CLI gains two hooks, owned by the
  foundation: a reserved hand-written group may adopt the generated commands
  of named actions (the `update` group adopts `core.update.apply`,
  `core.update.rollback`, `core.update.status`, beside its hand-written
  `run`, `recover` and the rollout commands), and a per-action local fallback
  runs when `--local` finds no agent socket (section 8). `--local` dials the
  socket once before reading or planning; only a missing socket or a refused
  connection runs the fallback, which asks `Apply? [y/N]` for a mutating
  action unless `--yes` and needs no confirmation phrase, because without the
  agent there is no plan. `--host` and `--url` are refused with `--local`.
  The controller sends the actor `system:controller` as `Darkpanel-Principal`
  on its own calls (hello, change reads, preflight, the journal).
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

The core module now has agent-side actions (`core.update.*`), which M1's
dispatcher and runtime refuse: `Hosts.hello` skips the core when seeding
`core_module_state`, `usable` answers `module.unavailable` without a row, and
the runtime excludes the core from its modules. Module id `core` is exempt
from enablement and availability in the dispatcher (P9) and is always loaded
by the runtime (P10). The foundation gives `coreactions.Deps` an `Extra
[]core.ActionDef` slot, so actions defined outside `coreactions` (the update
and rollout actions) join the one core manifest; assembly fills it.

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
     `<base>/index/releases.json`, section 8) and downloads into a `mktemp -d`
     directory, with `curl`, else `wget`, from `<base>/v<version>/`:
     `darkpanel_<version>_linux_<arch>.tar.gz`, `checksums.txt`, and for each
     embedded key id `checksums.txt.<keyid>.minisig`, where a missing file is
     skipped, not fatal. `<base>` is `--base-url`, default
     `https://github.com/Darkraise/darkpanel/releases/download`;
  3. verifies one signature with OpenSSL 3 only: algorithm `ED` (minisign
     hashed mode) and the embedded key id; `openssl dgst -blake2b512 -binary
     checksums.txt`; the 64-byte signature over that digest with `openssl
     pkeyutl -verify -pubin -inkey <key.pem> -rawin`; the global signature
     over `signature || trusted comment` the same way; the trusted comment
     exactly `version=<version> channel=<stable|beta> purpose=checksums` with
     the version being installed. Any one valid signature from an embedded key
     suffices; the script fails only when none verifies. Byte slicing uses `base64 -d`, `head -c`, `tail -c` and `od`;
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
  4. units and the drop-in written when their content differs, then
     `Reload` and `EnableUnitFiles` through the D-Bus provider;
  5. controller role: start the controller and wait for `GET /healthz` on the
     controller socket (60 s);
  6. enrollment, skipped when `darkpanel agent check` (one mTLS handshake with
     the saved credentials; exit 0 accepted, 1 refused, 2 unreachable, 3 the
     check could not run) exits 0. Controller role: on 1 or 2 (the controller
     is local and up), `core.host.enroll-local` over the controller socket,
     then the enrollment flow with that token, which also repairs a revoked or
     lost local agent. When the agent socket's `hello` reports
     `controllerConnected: true`, the credentials count as accepted and no
     handshake runs: a check is a session, and it would replace the running
     daemon's for a moment. Agent role: on 1, the token from standard input is
     required; on 2 with saved credentials it warns that the controller is
     unreachable and keeps them; without credentials it requires a token. A
     consumed token without saved credentials is reported with `this host's
     token was used but no credentials were saved; revoke the host and create
     a new token`;
  7. start the agent; the controller role prints how to create the first admin
     over the controller socket.

  The exec allowlist (ADR 0009) gains `/usr/sbin/useradd` and
  `/usr/sbin/userdel`; every systemd operation goes through D-Bus. Tests rerun the installer
  after each checkpoint and after a completed install.
- **Units**, embedded templates in `internal/install/units/`:
  - `darkpanel-controller.service`: `User=darkpanel`, `ExecStart=
    /usr/local/lib/darkpanel/darkpanel serve`,
    `RuntimeDirectory=darkpanel-controller`, `Restart=on-failure`,
    `RestartSec=2`, `StartLimitIntervalSec=60`, `StartLimitBurst=5`,
    `TimeoutStartSec=30s`, `TimeoutStopSec=10s`,
    `OnFailure=darkpanel-rollback.service`, `NoNewPrivileges=yes`,
    `ProtectSystem=strict`, `ReadWritePaths=/var/lib/darkpanel/controller`,
    `PrivateTmp=yes`.
  - `darkpanel-agent.service`: root, `ExecStart=… agent run`,
    `RuntimeDirectory=darkpanel-agent`, the same restart, start-limit,
    timeout and `OnFailure` lines.
  - `darkpanel-rollback.service` and `darkpanel-update-recover.service`:
    `Type=oneshot`, `ExecStart=/usr/local/lib/darkpanel/darkpanel.recovery
    update recover` (the boot unit adds `--boot`), `TimeoutStartSec=30s`,
    `ConditionPathExists=/usr/local/lib/darkpanel/darkpanel.recovery`;
    the recover unit adds `Before=darkpanel-controller.service
    darkpanel-agent.service` and `WantedBy=multi-user.target` and runs on
    every boot, and at boot `update recover` never restarts services
    (section 8). ADR 0012 records `update recover` and `darkpanel.recovery`
    as the form of ADR 0007's `darkpanel.prev update rollback --pending`.

  Five starts two seconds apart exhaust the start limit in well under 30 s,
  inside S5's two minutes.
- **Ownership manifest.** The agent migration widens `core_owned_files.kind`
  from `file`, `block` to `file`, `block`, `dir`, `user`, `unit` (a SQLite
  table rebuild that also rewrites the CHECK `(kind = 'file') = (block_id =
  '')` as `(kind = 'block') = (block_id <> '')`). `platform.Files` ignores
  entries of kinds `dir`, `user` and `unit`. The core declares its owned paths like a module's
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
  everything; with `--keep-data` the file step skips every core-owned entry
  under `/var/lib/darkpanel` and `/etc/darkpanel`. Each step prints one line; a failed step stops with the
  remaining steps listed. Tests cover `--keep-data` followed by a reinstall
  whose kept keys open the kept databases.

### 8. Self-update (R32–R36, ADR 0007)

- **Library** (`internal/update`): release index, download, verification,
  staging, self-test, the transaction record, swap, health check, rollback and
  stepping. Verification uses `aead.dev/minisign` against the embedded trusted
  keys; any one valid signature from a trusted key suffices, and a signature
  file that is missing or fails is one unsuccessful candidate, not an error.
- **Release index** (section 9). One channel-independent URL:
  `<base>/index/releases.json`, where `<base>` is
  `https://github.com/Darkraise/darkpanel/releases/download` (the rolling
  release `index` holds the file) and each version's files are at
  `<base>/v<version>/`. The file is `{"formatVersion": 1, "releases":
  [{"version": "1.3.0", "channel": "stable"}, …]}`, listing every published
  version with its signed channel. The updater reads it at most once an hour
  per process (cached) and never calls the GitHub API. The index only selects
  versions; trust comes from each version's own signature. `<base>` is
  embedded; only a `harnesskeys` build overrides it.
- **Channels.** A host subscribes to `stable` (only entries whose channel is
  `stable`) or `beta` (every entry). Ordering is semantic-version precedence
  (`1.3.0-beta.1 < 1.3.0`). Verification checks the trusted comment's
  `channel` against **the selected entry's** channel, so a beta host taking a
  stable release accepts its `stable` comment. A host on `beta` is never moved
  back to a lower stable release automatically.
- **Agent actions** (core module, agent side; types in `internal/core`,
  handlers in `internal/update/actions.go`, added to the core manifest through
  the `coreactions.Deps.Extra` slot, section 6):

| Action | Class | Input | Output |
|---|---|---|---|
| `core.update.apply/v1` | compensable, compensated by `core.update.rollback` | `version` (semantic version pattern), `channel` (`stable`, `beta`), `force` (bool) | `UpdateTransaction` |
| `core.update.rollback/v1` | irreversible | `force` (bool) | `UpdateTransaction` |
| `core.update.status/v1` | read | `operationId` or `changeId` (both optional, at most one) | `{"transactions": [UpdateTransaction]}`: every record of that operation (or of the operation that change started), or the newest 20 operations' records, newest first |

  `UpdateTransaction` is the record of the next item without its internal
  fields: `id`, `operationId`, `parentId`, `kind` (`update`, `rollback`),
  `fromVersion`, `toVersion`, `targetVersion`, `state`, `error` (`{code,
  message}` or null), `startedAt`, `updatedAt`. The inputs carry no URL or
  key: the agent builds every download URL from its embedded base and
  enforces the downgrade rule (refused unless `force`; a forced downgrade goes
  back at most one minor) and the one-minor stepping rule itself, so a
  compromised controller cannot direct a root agent elsewhere.
- **Transaction record** `UpdateDir/<transaction id>.json` (ADR 0004 record
  format rule): `formatVersion` (1), the `UpdateTransaction` fields,
  `changeId` (the initiating change, empty for a stepping child), `steps` (the
  ordered versions of the whole operation, written before the first swap),
  `deadline`, `prevSha256`, `newSha256`. An operation is one requested update
  or rollback; its id is its first transaction's id, and every step carries
  it. Every mutation of any record and every rename in `BinaryDir` happens
  while holding an exclusive `flock` on `UpdateDir/lock`; a writer re-reads
  the record under the lock before acting on its state. Records are written to
  a temporary file, fsynced, renamed, and `UpdateDir` is fsynced.
- **Reservation** (`internal/core/lifecycle`, owned by the foundation). A
  store-level function `lifecycle.Reserve(ctx, db, key, owner string) error`
  opens its own write transaction on agent.db (never nested in another write,
  `store.DB.Write` forbids that) and, in it, checks that no lock row exists
  and no revertible change is unconfirmed, then inserts `core_locks (key,
  owner)`; `lifecycle.Release(ctx, db, owner)` deletes the owner's rows.
  Reservation owners are written `op:<operation id>`, which no change id
  can equal, so every sweep below touches only `op:` rows and never a live
  change's lock.
  `takeLocks` refuses any key while a `*` row exists, so a change whose
  pre-check passed before the reservation still cannot take its lock after
  it. The registry refuses an agent-side revertible action that declares no
  lock key, so no revertible change can start, and stay unconfirmed, while
  an update holds `*`; a keyless compensable or irreversible change may. Handlers reach the function through `Env.DB`; the runner and `--direct`
  rollback call it directly. `Env` gains `ChangeID`, set by the engine for
  Apply and Verify (a plan has no change yet).
- **Apply returns before the restart.** `core.update.apply` declares no lock
  keys. Its Apply re-checks the version rules; calls `Reserve("*", <operation
  id>)` (a failure answers `update.in_progress` or
  `update.unconfirmed_changes`); writes the record `pending` with `changeId =
  Env.ChangeID`; starts the transient root unit `darkpanel-update-<tx>.service`
  through D-Bus `StartTransientUnit` with property
  `OnFailure=darkpanel-rollback.service`, running the current binary as
  `darkpanel update run <tx>`; and returns. If writing the record or starting
  the unit fails, Apply records `failed` (when the record exists), releases
  the reservation and returns the error. Verify: the record exists and is not
  `failed`. The runner's first step waits up to 60 s for `changeId` to be
  `completed` in agent.db, stops at once (recording `failed`, releasing) when
  the change is `failed` or `interrupted`, and skips the wait when `changeId`
  is empty. Tests cover a runner faster than Verify and a mutation attempted
  between Plan and Apply.
- **Rollback action.** `core.update.rollback` mirrors apply: guards (the
  newest operation is a committed update, `darkpanel.prev` is present and
  matches that update's `prevSha256`, the controller-host rule below),
  `Reserve`, a record with `kind: rollback` in `pending`, then the transient
  unit `darkpanel-rollback-<tx>.service` running the current binary as
  `darkpanel update rollback --direct --transaction <tx>`, and return. Its
  result is observed through `core.update.status`, never through the change,
  so the restart it causes never interrupts its own change. A `kind:
  rollback` record moves `pending → rolling-back → rolled-back | failed`: the
  runner hard-links `darkpanel.prev` as `darkpanel.recovery` (so a crash
  mid-rename is finished by `update recover` like any other), claims
  `rolling-back`, performs the same renames and restarts as an automatic
  rollback, and records `rolled-back`. There is no health window: a
  rollback is final, and the operator reinstalls if the previous binary is
  bad. The manual `--direct` path without an agent writes the same record.
  The record definitions belong to the update library (P5).
- **The runner** (`update run`) moves the record through `pending`,
  `downloading`, `staged`, `swapped` (with a deadline), `rolling-back`, then
  `committed`, `rolled-back` or `failed` (any failure before the swap). Under
  the update lock where it touches the record or `BinaryDir`:
  1. download into `UpdateDir`, verify, extract, copy the new binary to
     `BinaryDir/darkpanel.new-<tx>` (same filesystem as `darkpanel`), fsync,
     record `newSha256`; run `darkpanel.new-<tx> selftest`; record `staged`;
  2. copy the current binary to `darkpanel.prev.tmp`, fsync, rename over
     `darkpanel.prev`; hard-link `darkpanel.prev` as `darkpanel.recovery`
     (replacing any older one); fsync `BinaryDir`; record `prevSha256`;
  3. start the transient timer `darkpanel-update-deadline-<tx>.timer`
     (`OnActiveSec=80s`, `AccuracySec=1s`, running `darkpanel.recovery update
     recover --deadline <tx>`), then record `swapped` with `deadline` = now +
     80 s, then rename `darkpanel.new-<tx>` over `darkpanel`, fsync
     `BinaryDir`;
  4. restart `darkpanel-controller` when present, then `darkpanel-agent`;
  5. health check every 2 s until the deadline (rule below);
  6. on a pass, under the lock and only if the record is still `swapped`:
     record `committed`, stop the deadline timer; for a final step delete
     `darkpanel.recovery` and release the reservation; for an intermediate
     step keep the reservation and start the next step (Stepping). On a
     failure: roll back.
- **Health rule.** A refused connection, a timeout, or a reported version
  equal to `fromVersion` is "not yet" for either probe. Reaching the deadline
  is a failure.
  - Controller (when the controller unit exists): `GET /healthz` on its
    socket passes when `version` is `toVersion` and `status` is `ok`; a
    `toVersion` report with any other status is an immediate failure.
  - Agent: `hello` on its socket passes when `binaryVersion` is `toVersion`
    and, for an enrolled agent, `controllerConnected` is `true`; Hello has no
    status field, so the agent probe never fails early.

  The `harnessbroken` build reports `status: degraded` from the controller
  and never sets `controllerConnected`, so it fails at once on the
  controller's host and at the deadline on an agent-only host.
- **Time budget** (S5: rollback within two minutes). Deadline 80 s after the
  swap; timer accuracy 1 s; the recover command bounds its own work to 30 s
  (service stops use the units' `TimeoutStopSec=10s`), so the worst case is
  under 115 s. Both service units set `TimeoutStopSec=10s` and
  `TimeoutStartSec=30s`.
- **Taking the update lock.** Every recover path takes `UpdateDir/lock` with
  a 5 s timeout. On timeout it stops the operation's runner unit through
  D-Bus (`KillUnit` with `SIGKILL`, then `StopUnit`), which releases the
  `flock` with the process, and takes the lock again. A frozen runner
  therefore cannot block the deadline rollback.
- **Rollback** has one implementation, entered by the runner on a failed
  check, by `update recover --deadline <tx>` from the timer, by `update
  recover` from `darkpanel-rollback.service` (`OnFailure=` of either service
  or of a runner unit) and from `darkpanel-update-recover.service` at boot.
  All of them run `darkpanel.recovery`, which exists from step 2 until the
  operation is terminal. Under the update lock the caller claims `swapped →
  rolling-back`; only the claimant continues, every other caller sees a
  non-`swapped` state and does nothing. The claimant checks `darkpanel.prev`
  against `prevSha256` (a mismatch records `failed` with
  `update.prev_mismatch` and leaves the binaries alone), renames `darkpanel`
  to `darkpanel.failed` and `darkpanel.prev` to `darkpanel`, fsyncs, records
  `rolled-back`, releases the reservation, stops the deadline timer, deletes
  `darkpanel.recovery`, and restarts the services after `ResetFailedUnit`
  (a crash-looping unit is in `start-limit-hit` and refuses a plain start),
  except at boot: the boot
  unit is ordered before the services, so it only performs the renames and
  records, and the services start after it. A record in `rolling-back` found
  later (a crash mid-rollback) is finished from whichever rename is missing,
  using the file hashes to tell which binary is where. Tests race two
  rollbacks, freeze the runner after the swap, reboot after each rename, and
  interrupt at every filesystem step.
- **Pre-swap recovery.** At agent start and in every `update recover`, under
  the update lock: a record in `pending`, `downloading` or `staged` whose
  transient unit is not active becomes `failed` (`update.interrupted`), its
  `darkpanel.new-<tx>` is deleted and its reservation released. Next, stepping
  continuation runs (below). Only then, a sweep deletes `op:` rows of
  `core_locks` whose operation has no record or is finished. An operation is
  finished when any record is `failed` or `rolled-back`, or when its last
  record is `committed` with `toVersion` equal to `targetVersion` (for `kind:
  rollback`, when it is `rolled-back` or `failed`); a committed intermediate
  step is not finished. A test asserts `core_locks` is empty after every
  terminal path, and one crashes between an intermediate commit and the next
  step's record and checks the reservation survives until the target
  commits.
- **Manual rollback** (R31). `darkpanel update rollback` is the generated
  command of `core.update.rollback`, adopted by the reserved hand-written
  `update` group (section 5). With `--local` and no agent socket answering, it
  runs the rollback in-process as root (`--direct`): it re-executes
  `darkpanel.prev update rollback --direct` when `darkpanel.prev` exists and
  the current process is not already `--direct` (no re-exec loop). On the
  controller's host it reads `core_hosts.agent_version` from `controller.db`
  directly and refuses while an enrolled agent reports a newer version, unless
  `--force`. Without `darkpanel.prev` it fails with
  `update.nothing_to_roll_back`.
- **Stepping.** From 1.2 to 1.5 the operation's `steps` are the newest patch
  of 1.3, then 1.4, then 1.5, all with the same `targetVersion` and
  `operationId`. The reservation, owned by the operation id, is held from the
  first step to the last, so no other update or keyed change runs between
  steps. After an intermediate commit the runner writes the next step's record
  `pending` (with `parentId`) and only then starts its transient unit with the
  newly installed binary. At agent start and in `update recover`, a committed
  intermediate record whose next step has no record gets one, and the step is
  started. A failure leaves the host on the last committed minor and releases
  the reservation.
- **Channels and schedule** (R35). Per-host policy in `core_host_update
  (host_id TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
  channel TEXT NOT NULL, days TEXT, time_utc TEXT, last_window TEXT,
  last_rollout_id TEXT)`, created by P1's migration and set by
  `core.host.update-policy/v1` (compensable, compensated by itself with the
  previous policy; input `hostId`, `channel`, `schedule`: null, or `{"days":
  ["mon", …], "time": "HH:MM"}` in UTC; output `UpdatePolicy` = those
  fields). A window opens at `time` on each listed day and lasts two hours;
  its id is `<date>T<time>`. A scheduler (`internal/controller/rollout`)
  checks once a minute. For a host inside an open window whose `last_window`
  differs, when the index has a newer version in its channel and the host is
  online, it claims the window in one controller write transaction: it calls
  the package function `rollout.Create(tx, …)`, which the action's Apply also
  uses, to insert the one-host rollout rows, sets `last_window` and
  `last_rollout_id`, and records a `core.Audit` entry with actor
  `system:update-schedule`. Scheduled rollouts are not changes. When a
  rollout is already active (`rollout.in_progress`) or the host is offline,
  or, for a host other than the controller's own, the controller runs an
  older version than the target, it claims nothing and retries next minute
  inside the window. The controller's own host is never held back by that
  rule, so it updates first. A started rollout consumes the window whatever
  its outcome; missed windows are not caught up. Tests schedule the own host
  to a newer version, and an agent host before and after the controller
  moves.
- **Fleet rollout** (R36). Controller-side actions, defined in
  `internal/controller/rollout/actions.go` and added through
  `coreactions.Deps.Extra`:

| Action | Class | Input | Output |
|---|---|---|---|
| `core.update.rollout/v1` | compensable, compensated by `core.update.rollout-cancel` | `hosts` (host ids), `version`, `channel` | `Rollout` |
| `core.update.rollout-cancel/v1` | irreversible | `rolloutId`, `force` (bool) | `Rollout` |
| `core.update.rollouts/v1` | read | `limit` (optional, default 20) | `{"rollouts": [Rollout]}` |

  `Rollout` is `{id, version, channel, state, error, createdAt, updatedAt,
  hosts: [RolloutHost]}` with `state` one of `running`, `draining`
  (dispatching stopped, a host's operation still active), `succeeded`,
  `failed`, `cancelled`; `RolloutHost` is `{hostId, position, state, planId,
  operationId, error}` with `state` one of `waiting`, `updating`,
  `committed`, `failed`, `skipped` (a host the rollout never reached because
  it stopped or was cancelled). Tables `core_rollouts (id, version, channel,
  state, error, created_at, updated_at)` with a partial unique index on a
  constant expression where `state IN ('running', 'draining')`, so the
  database itself refuses a second active rollout (`rollout.in_progress`),
  and `core_rollout_hosts (rollout_id, host_id, position, state, plan_id,
  operation_id, error)`. The action's Apply inserts both (own host first when
  listed; refused when the target is newer than the controller's version and
  the own host is not listed) and returns.
- **Rollout driver** (`internal/controller/rollout`, actor `system:rollout`),
  started with the controller, drives each active rollout from its rows, one
  host at a time, and every transition is one controller write transaction:
  1. a host row without `plan_id`: plan `core.update.apply` on the host and
     store `plan_id` before applying;
  2. apply `plan_id`. After a lost response or a controller restart it applies
     the same `plan_id` again: the engine returns the existing change of a
     plan whatever its state, so it never acts twice
     (`internal/core/lifecycle/apply.go`). When the host row has no
     `operation_id` and the answer is `plan.expired` or `plan.not_found` (an
     expired plan is purged on the agent and dropped from the controller's
     plan index), the plan is cleared and planned again. A completed change's
     output carries the operation id, stored as `operation_id`. For a
     `failed` or `interrupted` change the driver asks `core.update.status`
     with that `changeId`: no record means the failure came before any
     handoff, so the host fails and the rollout becomes `failed` at once; a
     record means an operation exists, whose id is stored and followed as
     below;
  3. poll `core.update.status` with that `operationId` every 5 s, treating
     `host.offline` and transport errors as "not yet", until the operation's
     last record is `committed` with `toVersion` equal to the rollout's
     version (next host), any record is `rolled-back` or `failed` (the host
     fails), or 10 minutes pass.

  A failed host, or a 10-minute timeout, moves the rollout to `draining`; it
  becomes `failed` when the host's operation is terminal, so a second rollout
  cannot start while a host is still updating. `rollout-cancel` moves a
  `running` rollout to `draining` (or straight to `cancelled` when no host was
  sent its update), serialised with dispatch on the same database writer; the
  driver marks it `cancelled` once the active operation is terminal.
  `force: true` marks an active rollout `cancelled` at once, for a host that
  never comes back. `core_rollouts` also stores `outcome` (the end state of a
  draining rollout), `change_id`, `error_code` and `error_message`, and
  `core_rollout_hosts` stores `apply_sent_at` (from then on the host may be
  updating) and `followed_at` (the start of the ten-minute limit). The update
  actions of a process without an updater, the controller's, answer
  `unavailable`, which fails the host. A controller restart during its own host's update resumes
  from the rows. Tests cover concurrent submission, stop at the first failure,
  resume after a restart before and after the stored `planId` was applied, a
  restart longer than the plan lifetime (clock past `PlanTTL`, purge run), a
  failed apply with and without a record, cancellation while a host updates, and a stepped update that
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
     linux/arm64 tarballs (binary, `LICENSE`, `README.md`), a CycloneDX SBOM
     (`darkpanel_<version>.cdx.json`), the committed `install.sh`,
     `checksums.txt` over all of them, release notes from `git-cliff`
     (`cliff.toml`, conventional commits); GitHub artifact attestations for
     every file.
  3. `sign`, in the protected environment `release` (owner approval required):
     `gh attestation verify` for every artifact against this repository and
     the tag's commit; `darkpanel version --json` of the amd64 binary reports
     `testBuild: false` and `trustedKeys` equal to the production key ids;
     sign `checksums.txt` with every key in the environment, each producing
     `checksums.txt.<keyid hex>.minisig` with trusted comment
     `version=<version> channel=<stable|beta> purpose=checksums`.
  4. `publish`: a GitHub Release (marked prerelease for a prerelease version)
     with the tarballs, `checksums.txt`, every `.minisig`, the SBOM and
     `install.sh`; then it regenerates `releases.json` from the complete list
     of published `v*` releases (`gh release list`, channel `beta` for a
     prerelease version, else `stable`; never by appending to an older index)
     and uploads it with `--clobber` to the rolling prerelease `index`, created
     on first use. The workflow's `concurrency: release` group serialises
     publications, so no index loses a release.
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
`update.prev_mismatch`, `update.nothing_to_roll_back`, `update.interrupted`,
`rollout.in_progress`, `unavailable` (503: a service that is shutting down or
that this process does not run).
New actors: `system:rollout`, `system:update-schedule`,
`local:root@unenrolled`, `system:controller` (the controller's own agent API
calls). New reserved top-level CLI name: `system` (`agent`,
`update` and `uninstall` are reserved already).

### 12. ADR 0012

ADR 0012 "M2 contract and layout additions" records: the install script's
verification method (closing the index's deferred decision); the agent route
table and headers of section 5; the schema-hash definition and dispatch rule;
the journal epoch; `lifecycle.Reserve`, `Env.ChangeID` and the lock key `*`; the update action
shapes, the apply-returns-before-restart rule, the update lock and the
rollback claim; `update recover` and `darkpanel.recovery` as ADR 0007's
`darkpanel.prev update rollback --pending`; the health rule;
per-key signature files and `releases.json`; the new actors and reserved CLI
names; the accepted MPL-2.0 dependency; and the layout additions
`internal/transport/`, `internal/agent/api/`, `internal/agent/daemon/`,
`internal/agent/socket/`, `internal/controller/fleet/`,
`internal/controller/rollout/`, `internal/install/`, `internal/update/`,
`internal/update/trustedkeys/`, `internal/agent/wire/`,
`internal/platform/systemd.go`,
`schemas/embed.go`. It is Proposed when written and Accepted when this
milestone closes, the rule ADRs 0010 and 0011 follow for M1.

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
  test minisign key signs them; the scanner serves them over plain HTTP at
  `http://198.18.0.20:8000` in the production layout (`index/releases.json`,
  `v<version>/…`), the `<base>` of the `harnesskeys` build and of
  `install.sh --base-url`, with the test key substituted
  (`make install-sh KEYS=harness`).
- **Budget.** Each VM gets 2 GiB of memory. The job timeout rises to 120
  minutes and `go test` to 100 minutes; both are inferences, measured on the
  first run and recorded in `docs/dforge/gates.md`.
- **Scenarios**, per OS, each a named subtest, with the version each host runs
  afterwards:
  1. install `N`: `install.sh --version N --role controller` on the
     controller, `install.sh --version N --role agent --token-file` on the
     agent; the harness creates
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
  9. runner frozen: during a rollout to `N+1.3`, freeze the runner after
     `swapped` (`systemctl kill --signal=STOP`) so only the deadline timer
     can act; it rolls back within two minutes (the runner's `OnFailure`
     path has a unit test);
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

- `core.host.list` keeps its envelope `{"hosts": [Host]}` (`HostList`);
  `core.host.revoke` and `core.host.delete` return one `Host`. `Host` is M1's
  fields plus additions; `online` comes from the session registry through
  `Env.Agents` (a host answering `host.offline` is offline): `{id, name, isLocal, state ("pending",
  "enrolled", "revoked"), online, lastSeenAt (or null), agentVersion,
  protocolVersion, os, arch, supported, modules}`; the four version and
  platform fields are null until the first Hello.
- `core_hosts` gains `state TEXT NOT NULL DEFAULT 'pending'`,
  `enrolled_at`, `last_seen_at`, `agent_version`, `protocol_version`, `os`,
  `arch` (all nullable except `state`; an existing M1 local host row stays
  `pending`).
- `UpdateTransaction`, `UpdatePolicy`, `Rollout`, `RolloutHost`: section 8.
- Migrations: controller `0005_fleet.sql` (P1: `core_hosts` columns,
  `core_agent_certs`, `core_enroll_tokens`, `core_host_update`),
  `0006_journal_epoch.sql` (P9), `0007_rollout.sql` (P11: `core_rollouts`,
  `core_rollout_hosts`); agent `0005_ownership_kinds.sql` (P4); the agent's
  journal epoch is created by the runtime (section 5), not a migration.

The foundation creates the Go types for every action input and output above
(in `internal/core/fleet.go`), the error codes, actors and reserved names, the
paths, `ClientSource` and `StaticClients`, the Hello fields (including
`controllerConnected`), `Env.ChangeID`, `lifecycle.Reserve` and `Release` and
the `*` rule in `takeLocks`, the `core.Uninstaller` interface, the duplicate-
ownership rule, the CLI hooks of section 5, `coreactions.Deps.Extra`, the wire
types in `internal/agent/wire`, `internal/update/trustedkeys`,
`schemas/embed.go`, `install.sh.in` and `scripts/gen-install-sh`.

## Parallel decomposition

| Part | Wave | Owns | Consumes |
|---|---|---|---|
| P0 Foundation | 0 | `go.mod`, `go.sum`, `internal/deps/`, `internal/core/*.go` (fleet types, Hello fields, `Uninstaller`), `internal/core/lifecycle/` (`Env.ChangeID`, `Reserve`, `Release`, the `*` rule in `takeLocks`), `internal/agent/wire/`, `internal/controller/coreactions/module.go` (the `Deps.Extra` slot), `internal/core/errcode/`, `internal/paths/`, `internal/registry/` and the duplicate-ownership rule, `internal/controller/dispatch/` (`ClientSource`, `StaticClients`), `internal/controller/server/services.go`, `internal/cli/` (the two hooks, reserved names), `internal/platform/systemd.go` (D-Bus: transient services and timers, start, stop, restart, kill, reset-failed, enable, disable, reload, state) and the exec allowlist entries, `internal/update/trustedkeys/`, `schemas/embed.go`, `install.sh.in`, `scripts/gen-install-sh`, `docs/adr/0012-*.md`, `docs/adr/README.md` | this spec |
| P1 Agent authority | 1 | `internal/controller/fleet/ca*.go`, `certs*.go`, `tokens*.go`; `migrations/controller/0005_fleet.sql` | P0 |
| P2 Transport | 1 | `internal/transport/` | P0 |
| P3 Agent API | 1 | `internal/agent/api/` | P0 |
| P4 Install | 1 | `internal/install/` (system install, units, uninstall), `internal/platform/ownership.go`, `internal/platform/files.go` (ignore the new kinds), `migrations/agent/0005_ownership_kinds.sql`, the generated `install.sh` and its fixture tests | P0 (`trustedkeys`, generator) |
| P5 Update library | 1 | `internal/update/` except `actions.go` and `trustedkeys/` | P0 |
| P6 Release workflow | 1 (may start beside P0) | `.github/workflows/release.yml`, `.github/workflows/ci.yml` (a shellcheck step that skips when `install.sh` does not exist yet), `cliff.toml`, `release/`, `docs/versioning.md`, `Makefile` (every M2 target: release, release-prep, install-sh calling `scripts/gen-install-sh`, VERSION fallback; P0 runs the generator directly and touches no Makefile) | the generator and `trustedkeys` paths fixed by this spec |
| P7 Web hosts | 1 | `web/src/pages/hosts*.tsx`, `host*.tsx`, `rollouts*.tsx`, `web/src/api/types.ts`, `web/src/app.tsx` (the rollouts route), their tests | the Contracts shapes |
| P8 Fleet service | 2 | `internal/controller/fleet/` except P1's files (listener wiring, session registry, enrollment and renewal endpoints, revocation), `internal/controller/listen/` | P1, P2, P3 |
| P9 Host actions and dispatch | 2 | `internal/controller/coreactions/` (the `core.host.*` actions including `update-policy`), `internal/controller/dispatch/` (version window, schema negotiation, offline skip, the `core` exemption), `internal/controller/audit/` (epoch, ingestion deadline), `migrations/controller/0006_journal_epoch.sql` | P0, P3 |
| P10 Agent daemon | 2 | `internal/agent/daemon/` (dial loop, enrollment client, renewal, credential generations, `agent check`), `internal/agent/socket/`, `internal/agent/runtime/` (epoch, `controllerConnected`, the `core` module always loaded, pre-swap recovery and the lock sweep at start) | P2, P3, P5 |
| P11 Update actions and rollout | 2 | `internal/update/actions.go`, `internal/controller/rollout/` (rollout actions, driver, scheduler), `migrations/controller/0007_rollout.sql` | P5, P0, P1's `core_host_update` table |
| P12 Assembly | 3 | `internal/controller/server/`, `internal/controller/api/` (health detail fields), `cmd/darkpanel/` (`serve` flags, `agent`, `system install`, `uninstall`, `selftest`, `update run`, `update recover`, the `harnessbroken`, `harnesscrash` and `harnessmigration` build-tag files), `internal/cli/` (`--local`, the update local fallback), `internal/modules/modules.go`, registry wiring and filling `coreactions.Deps.Extra`, `schemas/` snapshots, end-to-end tests over a loopback transport, `README.md`, `CONTRIBUTING.md` | everything |
| P13 Harness | 3, after P12 | `test/harness/`, `.github/workflows/harness.yml`, `docs/dforge/gates.md` | P12, P6's Makefile targets |

Migration paths are under `internal/core/store/`.

- Wave 0 runs alone: it owns the dependency manifest and every shared
  interface. P6 shares nothing with P0 and may start beside it.
- Wave 1 runs P1–P7 together; wave 2 runs P8–P11 together; wave 3 runs P12,
  then P13. A part consumes only P0's interfaces and earlier waves' merged
  code; where it needs a sibling's behaviour it codes against the P0 interface
  with a fake.
- Files a later part takes over (`internal/controller/dispatch/` and
  `internal/cli/` from P0 to P9 and P12, `internal/controller/coreactions/`
  from P0 to P9, `internal/controller/fleet/` from P1 to P8, `Makefile` from
  P6 to P13) have one owner per wave.
- Risk parts, each its own task with a risk-level review: P0's lifecycle
  change, P1, P2, P4, P5, P8, P10, P11.
- Test isolation: as M1 — `t.TempDir()` databases, keys and TLS material, port
  `0` on `127.0.0.1`, socket paths from `os.MkdirTemp("", "dp")`, the
  `coretest` fake clock; D-Bus, systemd and `flock` paths are behind
  interfaces or temporary directories in unit tests and exercised for real
  only in the harness. Harness changes are verified before merge by pushing
  the work branch and dispatching `harness.yml`.
- Ownership as built: P9 committed the `schemas/core.host.*` snapshots and
  the host line of `internal/cli/names_test.go` (the snapshot test fails as
  soon as an action changes without its snapshot); P12 also changed
  `internal/core/registry.go` (the lock-key rule), `internal/core/errcode/`
  (`unavailable`), `internal/agent/daemon/` (the save lock, the harnessbroken
  hook), `internal/update/actions.go` (`unavailable` without an updater) and
  `internal/controller/fleet/server.go`; the `harnessmigration` files live in
  `internal/core/store/`, which owns the embedded migrations.

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
    runner frozen after the swap (timer path) and killed (`OnFailure`
    path); reboot after each rollback rename; pre-swap recovery of a dead
    runner; stepping continuation after a crash between commit and the next
    step; the rollback action's transient unit; the health rule's "not yet",
    pass and failure cases for each probe; a recover path killing a frozen
    runner that holds the update lock; `ResetFailedUnit` before restarting a
    crash-looped unit; the rollback record's states; the sweep sparing
    change-owned locks; interruption at every filesystem step;
    `core_locks` empty after every terminal path; the migration backup the
    restarted service takes before migrating;
  - rollout: concurrent submission refused; stop at the first failure;
    resume after a restart before and after the stored `planId` was applied;
    a restart longer than `PlanTTL` (`plan.not_found`); a failed apply with
    and without an update record; cancellation while a host updates (`draining`) and
    `force`; a stepped update advancing only on the target version; schedule
    windows, the atomic window claim, the own host scheduled ahead of the
    controller's version, and the retry and skip rules;
  - wire: every DTO round-trips through its `core` type; field names are
    camelCase;
  - release: `releases.json` regenerated from a release list including betas
    after the latest stable; `install.sh` and the updater with a trust list of
    two keys against single-signed and dual-signed releases;
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
- Spec panel round 2, 2026-10-19 (delta). Fable: approve with changes;
  executability 14, coherence 14, coverage 16, assumptions 15; 13 new
  findings. Codex gpt-6-astra: rework; executability 14, coherence 14,
  coverage 18, assumptions 14; 14 new findings. Every finding accepted and
  folded in: `darkpanel.recovery` as the recovery executable; pre-swap
  recovery; timer accuracy and the time budget; `lifecycle.Reserve` as its
  own transaction with the `*` rule in `takeLocks` and `Env.ChangeID`; plan
  re-apply for rollout reconciliation; the `draining` state and forced
  cancel; operation-scoped reservations and stepping continuation;
  `operationId` in `core.update.status`; the rolling `index` release
  regenerated on every publication; local host `pending` and atomic
  `enroll-local`; `agent check` for installer reuse; explicit wire types; the
  `HostList` envelope; the atomic schedule claim; Makefile and policy-table
  ownership; the baseline `--version N`; missing signature files as
  candidates; the rollback action's transient unit; the `core` module
  exemption and `Deps.Extra`; the health rule and `controllerConnected`;
  `--keep-data` file skipping; boot recovery without restarts; the frozen
  runner scenario; the epoch acknowledgement key and conflict target;
  Verify and the runner's wait; the ownership CHECK rewrite; D-Bus for every
  systemd operation; the `update` group adopting generated commands.
- Spec panel round 3, 2026-10-19 (delta, final). Fable: approve with
  changes; executability 16, coherence 15, coverage 17, assumptions 16; 5
  findings. Codex gpt-6-astra: rework; executability 16, coherence 15,
  coverage 18, assumptions 16; 4 new findings and 2 partly fixed. Every
  finding folded in: the scheduler's own-host exception and scheduled
  rollouts created by `rollout.Create` outside the lifecycle;
  `plan.not_found` re-planned like `plan.expired`; failed applies resolved by
  `changeId`; `op:` reservation owners and a sweep that runs after stepping
  continuation with a defined finished operation; the rollback record's
  state machine with no health window; split controller and agent health
  predicates; recover paths killing a frozen runner that holds the update
  lock; `ResetFailedUnit` before restarting a crash-looped unit;
  `Env.ChangeID` for Apply and Verify only.
