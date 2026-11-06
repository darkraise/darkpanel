# Threat model

This page is the written threat model of Darkpanel. It states what the product protects, who can act against it, where trust changes hands, and, for each area, the threats, the mitigations that exist and the risk that remains. It is the base of a security review and of the decision which remaining risk to accept.

## Scope

- Covered: the controller, the agents it manages, the modules shipped in v1 (firewall, Docker, Tailscale, tailnet, DERP, Nginx Proxy Manager, Cloudflare and panel exposure), the CLI, the web UI, backup and restore, and the update path.
- Not covered: a compromised root account on a host (the agent runs as root, so a root attacker already holds the host), physical access, and the security of Tailscale, Cloudflare, Let's Encrypt and Docker themselves.
- Not covered: a malicious administrator. An admin can do everything an admin may do; the model limits what a lower scope, a stolen credential or a mistake can do.

## Assets

- The controller database `controller.db`: hosts, the audit log, sealed secrets, the sealed private keys of both certificate authorities and quarantine state. Loss of confidentiality exposes sealed values to anyone who also holds the key file; loss of integrity corrupts the fleet's record; loss of availability stops management until a restore.
- The key files `controller.key` and `agent.key`, which seal every secret column. Disclosure opens every sealed value; loss makes the sealed data unreadable unless a backup exists.
- The agent's identity, its key and certificate in the agent's TLS directory. Theft lets an attacker act as that host; loss forces the host to enroll again.
- Sessions, API tokens, password hashes and TOTP secrets. Theft gives the access of the user at the user's scope; only hashes of sessions and tokens are stored.
- Cloud credentials: the Tailscale API key and auth keys, the Cloudflare API token and tunnel tokens. Theft gives the reach of the account token; loss stops the module until it is configured again.
- The Nginx Proxy Manager admin password, its Let's Encrypt private keys and its database. Theft allows changes to every proxy host and impersonation of the proxied sites.
- Container environment values. They often hold application secrets, so disclosure reaches the applications behind them.
- Backup files and their passphrases. A backup holds every sealed value, so a stolen backup with a weak passphrase is a full disclosure.
- The reachability of every host, that is, what the firewall allows. A wrong change cuts the operator off or opens a port to the internet.
- The trusted release keys in the binary and the signed release and derper artifacts. Tampering would run attacker code as root on every host that updates.
- The DERP node key. Theft lets an attacker impersonate the relay.

## Actors

- An unauthenticated network attacker. Reaches only the listeners: the web listeners, the enrollment listener and any exposure the operator created.
- An authenticated user at each scope: `read` sees state, `operate` changes state through declared actions, `admin` also manages users, tokens, secrets and Compose projects.
- A holder of a stolen session cookie or API token. Has the scope of that credential until it expires or is revoked.
- Hostile web content in an admin's browser, through cross-site requests or script injection. Can only act with what the browser sends automatically.
- The operator, whose mistake, such as a firewall change, can lock them out.
- An unprivileged local user on a managed host. Can plant files and links in places a root process later reads or writes.
- A container workload on a managed Docker host, including one on a network joined through `npm.network.attach`. Reaches what the network reaches, which includes Nginx Proxy Manager on a joined network.
- A compromised agent host. Holds that host's key and can act as that host only.
- A compromised controller. Can send any declared action to every enrolled agent.
- A compromised third party: Nginx Proxy Manager's API, a cloud API or an upstream release. Controls the answers Darkpanel reads from it.

## Trust boundaries

### Browser and controller

- HTTPS listeners on loopback and the Tailscale interface by default, with plain HTTP only on 127.0.0.1. The certificate comes from `tailscale cert` or the internal web root (ECDSA P-256).
- Cookie sessions (Secure on HTTPS, HttpOnly, SameSite=Strict) plus a CSRF token, and scoped, expiring API tokens. One middleware denies every route that is not on the public list, whatever the method. A strict Content-Security-Policy and the other headers are set by `internal/controller/api/middleware.go`.

### Proxy and controller

- The runtime listeners of a panel exposure accept HTTPS only, on a private bridge address, from one allowed peer address (the proxy).
- One client-address header is read (`X-Darkpanel-Client` for Nginx Proxy Manager, `Cf-Connecting-IP` for a tunnel) and every other forwarded header is ignored. A request whose forwarded protocol is not https is refused, an unsafe request whose Origin is present must match the public host, and revocation is durable.

### Controller and agent

- The agent dials out and opens no port. The link is mutual TLS 1.3 over a yamux session. ALPN `darkpanel-agent/1` requires a client certificate and `darkpanel-enroll/1` serves only enrollment.
- The controller uses its own Ed25519 authority. Controller and agent identities are URI SANs.

### Agent and host

- The agent is root. It runs allowlisted commands with fixed environments, opens files with `openat2` under root-owned directories, and talks to systemd over D-Bus, to the Docker Engine API and to the Tailscale local API.
- The agent socket is root-only and is checked with `SO_PEERCRED`.

### Controller and cloud APIs

- Outbound HTTPS to the Tailscale and Cloudflare APIs with sealed credentials and 15-second timeouts. Only reads and deletes are retried, and a write whose outcome is uncertain has an explicit procedure.
- Let's Encrypt is reached only through Nginx Proxy Manager on the agent's host.

## Areas

### Authentication

#### Threats

- Password guessing and credential stuffing.
- A stolen password, and a replayed or brute-forced TOTP code.
- Session theft, and cross-site request forgery against a cookie session.
- A route that forgets its check, and a lower scope reaching an admin action.
- An attacker erasing their traces.

#### Mitigations

- Users have argon2id password hashes, and only a bounded number of hashes run at a time. TOTP (RFC 6238) is required to sign in and a code is never accepted twice (`internal/controller/auth`).
- A login rate limit by user name and by source address applies a lock, and a flood of new keys cannot clear a running lock (`internal/controller/auth`).
- Sessions have an idle and an absolute lifetime and only their hash is stored. API tokens carry a `read`, `operate` or `admin` scope and an expiry, and only their hash is stored (`internal/controller/auth`).
- One authentication middleware on every listener applies to every method and denies unless the route is public. Cookies are SameSite=Strict and HttpOnly, a CSRF token guards unsafe methods, and runtime listeners check the Origin (`internal/controller/api`).
- Break-glass user commands run only on the controller socket, which is root-only through `SO_PEERCRED` (`internal/controller/api`).
- Every action leaves an audit entry (`internal/controller/audit`), including the rows the system services (the Cloudflare hostname driver and the tailnet key sweep) write, which the audit table accepts because its phase check lists the phases those services use (`internal/core/store/migrations/controller`).

#### Residual risk

- The rate limiter's counters live in memory, so a restart clears them.
- TOTP uses SHA-1 because authenticator apps implement it.
- The web listeners accept TLS 1.2 for browsers; the agent transport requires TLS 1.3.
- An admin whose session and CSRF token are both stolen acts as that admin until the session ends. The open question about the Origin header is listed in Known residual risks.

### Agent protocol

#### Threats

- An agent impersonating the controller, and a stolen agent key or certificate.
- Enrollment token theft, guessing or reuse, and a CSR that claims another host's identity.
- A stale or revoked agent staying connected.
- A host offline for a long time losing its identity.
- A downgrade to an unsupported protocol version.

#### Mitigations

- The two roles use separate authorities and certificates. Every agent pins the controller SAN and the controller requires a host SAN of every agent (`internal/transport`).
- The link is TLS 1.3 with a client certificate and ALPN `darkpanel-agent/1`; an enrollment listener on `darkpanel-enroll/1` serves only enrollment (`internal/transport`).
- An enrollment token holds 128 random bits, is stored hashed, is valid for one hour, is compared in constant time, is consumed atomically, is rate limited and is bound to one host id. The identity the CSR claims is ignored (`internal/controller/fleet`).
- Certificates last one year and are renewed over the live session. Revocation by serial is checked at every handshake and closes the live session (`internal/transport`, `internal/controller/fleet`, `internal/controller/coreactions`, `internal/agent/daemon`).
- The agent listener binds loopback and the Tailscale interface by default, never all interfaces (`internal/controller/listen/agent.go`). The agent dials out to the controller (`internal/agent/daemon/daemon.go`).
- A frozen hello and update path lets an agent outside the version window still be updated (`internal/controller/dispatch`, `internal/controller/coreactions`, `internal/agent/api`).
- A restored controller issues enrollment tokens only in quarantine and only for existing hosts (`internal/controller/backup`).

#### Residual risk

- A stolen agent key acts as that host until it is revoked.
- A compromised controller can send any declared action to every enrolled agent. The declared schemas and the allowlist on the agent limit its power but do not remove it.
- A host offline for longer than a certificate's life must enroll again.

### Command execution

#### Threats

- Argument or option injection, and a binary found through `PATH`.
- An inherited environment.
- A symlink planted by an unprivileged process to redirect a root write.
- A marked block corrupted by hand.
- A parser fed hostile tool output.

#### Mitigations

- The exec provider runs only allowlisted absolute binary paths with a fixed minimal environment. It never uses a shell and runs only argument vectors that a rule for that path accepts, mostly exact vectors; where a tool has no `--`, as ufw, the input schema patterns refuse a value that starts with `-` (`internal/platform`).
- Input schemas constrain every value that reaches a program (`internal/modules`, `internal/core/lifecycle`).
- The file provider writes only declared paths and marked blocks. It opens the parent with `openat2` and `RESOLVE_NO_SYMLINKS`, requires a root-owned parent that others cannot write, writes through a temporary file and a rename, and refuses duplicated, nested or unterminated markers (`internal/platform`).
- APIs are preferred to tools: the Docker Engine API, the Tailscale local API and systemd over D-Bus. An ownership manifest drives uninstall, Agent modules run programs and write host files only through `internal/platform`; the update path (`internal/update`, `cmd/darkpanel/update.go`) additionally runs the staged or previous Darkpanel binary itself.
- Every parser of tool output and untrusted input is listed in `docs/security/parsers.md` with a fuzz target that `scripts/fuzz.sh` runs.

#### Residual risk

- The agent is root, so a flaw in an allowlisted tool is a root flaw.
- The file provider defends against an unprivileged process, not against root.
- Package scripts run as root when a module installs or pins a package.

### Compose execution

#### Threats

- A Compose file that runs a host program.
- A remote `include` or `extends` reaching Git or an OCI registry.
- A build that starts a host plugin.
- A project that touches Darkpanel's own objects.
- Hostile Compose output.

#### Mitigations

- Compose files are operator-authored and trusted like any root-run configuration. Darkpanel never writes them, running them is an `admin` action, and the plan says it runs the project's file as root (`internal/modules/docker`).
- Validation requires absolute paths owned by root or the directory's owner with no symlink. A service with a `provider` key, or any key outside the documented set, is refused (`internal/modules/docker/compose_check.go`, `internal/modules/docker/compose/validate.go`).
- Both remote loaders are switched off with `COMPOSE_EXPERIMENTAL_GIT_REMOTE=false` and the OCI equivalent, `up` always runs with `--no-build`, and nothing is inherited from the agent's environment: it holds the runner's fixed base, the Darkpanel-owned `DOCKER_CONFIG` and `HOME`, and the two remote-loader switches (`internal/platform/exec_rules.go`, `internal/modules/docker/compose_run.go`, `internal/modules/docker/compose`).
- A project named `darkpanel*`, or one that touches an object labelled `darkpanel.module`, is refused with `docker.owned` (`internal/modules/docker/compose_check.go`).
- The resolved configuration is read as strict JSON and fuzzed (`internal/modules/docker/compose`).

#### Residual risk

- A Compose file can still mount host paths and start privileged containers, which is root-equivalent by design.
- The key allowlist follows the Compose version installed on the host.
- Private registry pulls through Compose are unsupported.

### Secret handling and delivery

#### Threats

- A secret in a log, a plan, a journal, an audit row, an error, a process list or shell history.
- The key file leaking.
- A secret shown to a user who should not see it.
- A stolen or tampered backup, and a plaintext copy left on disk.
- A secret surviving in a removed component.

#### Mitigations

- Secret columns are sealed with XChaCha20-Poly1305 under a per-role key file (mode 0600), with the table, column and row id as associated data. The `secret` tag drives sealing and redaction (`internal/core/secret`).
- The CLI takes secrets only from a prompt or standard input, never a flag (`internal/cli`).
- A reveal action returns one stored secret once, needs `admin`, writes its audit entry before it answers and sets `Cache-Control: no-store`. It is never journaled, cached, relayed through server-sent events or stored in a change, and has no `--local` form (`internal/core/lifecycle/reveal.go`, `internal/controller/api/reveal.go`).
- The logs of containers a Darkpanel module owns, the Cloudflare connector and the Nginx Proxy Manager container, are masked for the secrets Darkpanel placed in them. The logs of other containers pass unchanged (`internal/modules/docker/logsecrets.go`, `internal/modules/cloudflare`, `internal/modules/npm`).
- A backup is one archive, framed and sealed with a key derived from the operator's passphrase by argon2id and authenticated to its end so truncation is detected (`internal/controller/backup`). The passphrase is read from the terminal or a root-owned 0600 file (`internal/cli`).
- A restore stages with mode 0600 and gives owner and mode to each staged file through one descriptor that refuses links. A backup keeps its plaintext copy of the database in a private directory that the controller sweeps at startup, and reads the key file with a bound, so a key that grew is refused (`internal/controller/backup`).
- The CLI builds the backup request from the passphrase bytes (`internal/cli`).
- Removing an Nginx Proxy Manager instance deletes its upgrade archives, which hold Let's Encrypt private keys and the NPM database (`internal/modules/npm`).

#### Residual risk

- The key files are readable by the service user and root, so anyone who reads them reads every sealed value.
- A passphrase typed at a terminal passes through a Go string in the terminal reader before it is copied.
- The strength of a backup passphrase is the operator's.
- Losing `controller.key` without a backup loses the sealed data.
- A secret that an application writes to its own container's logs is not masked.

### Firewall

#### Threats

- A change that cuts the operator's SSH session, the panel's path or the agent's link (a lockout).
- A change that cannot be reverted.
- A torn read of a half-applied state.
- Docker publishing a port around UFW, and a rule the operator did not mean to open.
- The guard itself being wrong.

#### Mitigations

- A lockout guard predicts the effect of a change on the protected flows and refuses it unless explicitly overridden (`internal/modules/firewall/predict`, `internal/modules/firewall/eval`, `internal/modules/firewall/guard`).
- A change stays pending until confirmed and the host reverts it by itself after the confirmation timeout (60 seconds by default), so the revert happens even when the client disconnects (`internal/core/lifecycle`, `internal/modules/firewall`).
- A snapshot is taken before every change, and a break-glass rollback and restore through the agent socket work without the controller (`internal/cli`, `internal/modules/firewall`).
- The module holds one read-write lock per instance. Every path that changes UFW's files, the loaded rules or the Docker block takes it for writing, and every status read and plan preparation takes it for reading, so a plan or a status read never sees a state between a file write and the reload (`internal/modules/firewall`).
- The Docker integration is a marked block with its own implementation that pins a container's allowance to its published binding (`internal/modules/firewall/dockerfw`).
- The exposure view computes reachability per port and the harness checks it against an external scanner. The parsers of UFW files and ruleset output are fuzzed (`internal/modules/firewall`, `internal/modules/firewall/exposure`, `test/harness`).

#### Residual risk

- A break-glass process outside the agent is not covered by the lock, because it runs when the agent cannot be reached.
- An overridden guard can still cut the operator off, and the revert timer is the only net.
- Docker network modes the integration cannot protect are reported as unprotected.
- Manual rules are shown and never removed.

### Cloud credentials

#### Threats

- A Tailscale or Cloudflare credential leaking, and a leaked auth key.
- A write sent twice, or lost after it committed.
- Credentials deleted while cloud objects still depend on them.
- An over-privileged token.

#### Mitigations

- Credentials are stored sealed in each module's own table and are write-only through the API, except through secret delivery (`internal/controller/cloud`).
- One HTTP client with 15-second timeouts retries only reads and deletes (`internal/controller/cloud`).
- Every write has a procedure for an uncertain outcome: the effect becomes unresolved and the change `needs_repair`, and nothing settles it automatically or re-sends it blindly (`internal/controller/cloud`, `internal/core/lifecycle`).
- A check of pending cleanup runs before any path deletes a credentials row (`internal/modules/cloudflare`, `internal/modules/tailnet`).
- Every auth key Darkpanel creates is recorded first and swept by description (`internal/modules/tailnet`).
- Connect probes read capabilities and report write capabilities as unverified until a write succeeds. The cloud fakes test every fault (`internal/controller/cloud`).

#### Residual risk

- Cloudflare's configuration `PUT` can overwrite a concurrent write, and nothing detects it.
- An account token holds all the permissions Darkpanel asked for, so its theft is as bad as the module's reach.
- A Tailscale key whose create timed out may exist until the sweep removes it.

### Exposure

#### Threats

- The panel reachable by the wrong party through a proxy.
- Spoofed client-address or protocol headers.
- Cross-site requests through an exposure.
- Nginx Proxy Manager's admin UI exposed by mistake or unchecked.
- A proxy host that points at the panel or at NPM itself.
- A compromised NPM exhausting an agent.
- A revoked exposure coming back.

#### Mitigations

- Runtime listeners accept only HTTPS from one allowed peer, read exactly one client header set by a proxy Darkpanel configured (`X-Darkpanel-Client` or `Cf-Connecting-IP`) and ignore `X-Forwarded-For` and `X-Real-IP`. They refuse a request whose forwarded protocol is not https, and refuse an unsafe request whose Origin is not the exposure's public host, compared with the exposure's public host, which is stored in lower case without a trailing dot (`internal/controller/listen`, `internal/controller/api`).
- Login over an exposure requires TOTP and every page shows a banner. Revocation is a durable `revoked` state that is checked on every request and stream and that nothing reverses; `sudo darkpanel panel listener revoke` is the break-glass (`internal/modules/panel`, `internal/controller/api`, `internal/controller/listen`, `web/src/modules/panel/banner.tsx`).
- NPM's admin port 81 is published on 127.0.0.1 only, and its HTTPS exposure is an opt-in action. The banner warns about it on every page, and, because it reads the list again, also about each host whose status could not be read (`unreadable`) and about a failed read (`web/src/modules/npm/banner.tsx`, `internal/modules/npm`).
- Proxy hosts that forward to NPM's own port 81 or to the panel gateway are refused as reserved (`internal/modules/npm/proxy`).
- The agent's NPM client limits the size of every answer and drains refusals (`internal/modules/npm/npmapi`).
- The public check honours the sources a Docker binding denies (`internal/modules/npm`).
- Each runtime listener keeps its own TLS session tickets, so a session resumed on one listener is not accepted by another (`internal/controller/listen`).

#### Residual risk

- The open questions about Origin, forward hosts and NPM's ports are listed in Known residual risks.
- A container on a network joined through the NPM attach action shares that network with NPM.
- Let's Encrypt's duplicate-certificate limit can refuse a repeated add.

### DERP host

#### Threats

- A tampered derper binary, and a downgrade to an older artifact.
- A derper that relays for strangers.
- A version mismatch that makes client verification fail open or closed wrongly.
- A service account that Darkpanel did not create being removed.
- The probe used against internal targets.

#### Mitigations

- Derper is built by the release workflow from upstream at a pinned version with a pinned module environment, and is listed in a manifest of per-architecture SHA-256 digests that is verified through the release's signature. The manifest parser refuses duplicate members (`internal/modules/derp/artifact`, `release/releasetool`).
- The agent installs derper as a systemd service under the system user `darkpanel-derp` with `NoNewPrivileges=yes` and only `CAP_NET_BIND_SERVICE`. It records that it created the user only after `useradd` succeeded, so uninstall never removes an account somebody else made (`internal/modules/derp`).
- Derper runs with `-verify-clients`, which checks every client's node key against the host's own `tailscaled` and refuses the client when `tailscaled` fails (`internal/modules/derp`).
- The host's `tailscaled` is pinned and held at the derper version, and a pin that fails rolls the package back on a context of its own, even when the forward step's context has been cancelled (`internal/modules/tailscale`, `internal/modules/tailscale/package_pin.go`).
- Derper's node key file lives in `/var/lib/darkpanel/derp`, a directory owned by `darkpanel-derp` that is created and kept at mode 0700 (`internal/modules/derp/server/server.go`, `internal/platform/files_servicedir.go`). Darkpanel does not set the mode of the key file itself, which derper writes. The probe uses a throwaway node key (`internal/platform/tslocal/derp.go`). The probe accepts only an IP literal or a lower-case DNS name of at least two labels made of letters, digits and hyphens, and refuses loopback, unspecified, multicast and link-local addresses, including among the addresses a name resolves to (`internal/modules/derp/node_probe.go`).

#### Residual risk

- Holding `tailscaled` at derper's version delays its security updates on that host.
- The probe's target ranges and the error text of a failed probe are an open question listed in Known residual risks.
- Derper itself answers unauthenticated STUN and HTTPS probes from the internet.

## Known residual risks

These are the security questions that remain open. None is described as accepted.

- `Origin`: a request without an Origin header is accepted for a cookie session on an unsafe method through a runtime listener, so only the CSRF token guards it. Whether to require Origin for sessions is undecided.
- `RFC 1918`: the DERP node probe accepts RFC 1918 and CGNAT targets and returns raw TLS error text that can name internal certificate names. Whether to refuse those ranges or to record the decision is undecided.
- `forward host`: the reserved-target check of a proxy host resolves no names, so a forward host that resolves to loopback, to NPM's admin port or to the panel gateway passes. Whether to resolve it as NPM does is undecided.
- `port 81`: NPM serves its admin login on port 81 inside its container, so any container on a network joined through the attach action can reach it, and only the host side is bound to loopback. Whether to restrict it inside the container or to accept it is undecided.
- `port 3000`: the guard covers NPM's port 81 only, while NPM's backend API listens on port 3000, and whether it binds loopback only in the pinned image is unverified.
- `digest`: the label digest in Docker network and volume effect documents is an unsalted SHA-256, and plan diffs outside the effect document show raw label values. Whether to salt the digest or to redact the diffs is undecided.
- `openpgp`: the `openpgp` package of `golang.org/x/crypto` carries an advisory (GO-2026-5932) with no tagged fix, and Darkpanel does not call it. The open choice is to keep the current pin until a tagged release carries the fix, or to remove the package from the module graph.
