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
- A login rate limit applies a lock by source address and a lock on the pair of user name and source, where the source is the client's IPv4 address or the /64 of its IPv6 address. Behind a runtime listener the source is the client address the listener parsed, not the text the audit log keeps. A flood of new keys cannot clear a running lock (`internal/controller/auth`, `internal/controller/api`).
- Sessions have an idle and an absolute lifetime and only their hash is stored. API tokens carry a `read`, `operate` or `admin` scope and an expiry, and only their hash is stored (`internal/controller/auth`).
- One authentication middleware on every listener applies to every method and denies unless the route is public. Cookies are SameSite=Strict and HttpOnly, a CSRF token guards unsafe methods, and runtime listeners check the Origin (`internal/controller/api`).
- Break-glass user commands run only on the controller socket, which is root-only through `SO_PEERCRED` (`internal/controller/api`).
- Every action leaves an audit entry (`internal/controller/audit`), including the rows the system services (the Cloudflare hostname driver and the tailnet key sweep) write, which the audit table accepts because its phase check lists the phases those services use (`internal/core/store/migrations/controller`).

#### Residual risk

- The rate limiter's counters live in memory, so a restart clears them.
- TOTP uses SHA-1 because authenticator apps implement it.
- The web listeners accept TLS 1.2 for browsers; the agent transport requires TLS 1.3.
- An admin whose session and CSRF token are both stolen acts as that admin until the session ends. The accepted risk of a request without an Origin header is listed in Known residual risks.
- A lock on the pair of user name and source gives each source its own attempts against a user name, so an attacker who uses many sources, many IPv6 /64 networks included, makes more guesses than one who uses few. A correct password is still not enough to sign in without a TOTP code.

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
- The global enrollment budget is shared by every source, so a peer that reaches the agent listener can use it up and delay enrollment. The accepted risk is listed in Known residual risks.

### Command execution

#### Threats

- Argument or option injection, and a binary found through `PATH`.
- An inherited environment.
- A symlink planted by an unprivileged process to redirect a root write.
- A marked block corrupted by hand.
- A parser fed hostile tool output.

#### Mitigations

- The exec provider runs only allowlisted absolute binary paths with a fixed minimal environment. It never uses a shell and runs only argument vectors that a rule for that path accepts, mostly exact vectors. ufw has no `--`, so a value that starts with `-` must never reach it as an argument: the input schema patterns of an address, a port and an interface refuse such a value, and a comment reaches ufw only after the tag prefix `darkpanel:` that every rule a client adds carries, because `withComment` drops the comment of a manual rule that starts with `-` (`internal/platform`, `internal/modules/firewall/model`).
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
- A failed `docker compose config` or run reports its exit status and the command to run on the host to see why, and no byte of Compose's stderr reaches an error, a change record or a log line, because Compose can quote values it resolved from env files (`internal/modules/docker`).

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
- The CLI takes a secret from a prompt, from standard input or from a JSON input file named with `--input`, and never from a flag; the file's path is on the command line and its content is not (`internal/cli`).
- A reveal action returns one stored secret once, needs `admin`, writes its audit entry before it answers and sets `Cache-Control: no-store`. It is never journaled, cached, relayed through server-sent events or stored in a change, and has no `--local` form (`internal/core/lifecycle/reveal.go`, `internal/controller/api/reveal.go`).
- The logs of containers a Darkpanel module owns, the Cloudflare connector and the Nginx Proxy Manager container, are masked for the secrets Darkpanel placed in them. The logs of other containers pass unchanged (`internal/modules/docker/logsecrets.go`, `internal/modules/cloudflare`, `internal/modules/npm`).
- A backup is one archive, framed and sealed with a key derived from the operator's passphrase by argon2id and authenticated to its end so truncation is detected (`internal/controller/backup`). The passphrase is read from the terminal or a root-owned 0600 file (`internal/cli`).
- A restore stages with mode 0600 and gives owner and mode to each staged file through one descriptor that refuses links. A backup keeps its plaintext copy of the database in a private directory that the controller sweeps at startup, opens the copy without following a link, sets its mode 0600 through that descriptor and streams from the same descriptor, and reads the key file with a bound, so a key that grew is refused (`internal/controller/backup`).
- The CLI builds the backup request from the passphrase bytes (`internal/cli`).
- Removing an Nginx Proxy Manager instance deletes its upgrade archives, which hold Let's Encrypt private keys and the NPM database (`internal/modules/npm`).
- An effect document holds a value it only compares as a keyed fingerprint, `hmac-sha256:` followed by the HMAC-SHA-256 of the value under a key derived from the role key, so nobody can check the fingerprint against a guess without that key. A value an undo restores goes into the effect's sealed secret instead. This covers the settings of Nginx Proxy Manager proxy hosts, custom locations included, and the labels of Docker networks and volumes (`internal/modules/npm`, `internal/modules/docker`, `internal/core/lifecycle`).
- Network and volume label values are secret inputs: a plan shows a label's key and whether it is added, changed or removed, and never its value (`internal/modules/docker`).
- Container inspection masks every element of a container's `Cmd`, because an image with an `Entrypoint` makes the first element an argument and not a program. An element of `Entrypoint` keeps the masking rule it had (`internal/modules/docker`).

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

- Darkpanel does not reload an active UFW for rule, logging, container or Docker block changes: UFW applies those commands live and Darkpanel loads its Docker chains atomically per family (`internal/modules/firewall/dockerfw/restore_input.go`).
- A lockout guard predicts the effect of a change on the protected flows and refuses it unless explicitly overridden. It checks the public leg of a panel exposure, the browser to NPM binding, from the exposure's own record, and a request through an NPM exposure without those bindings fails closed. It evaluates "any internet address" once for each cell of the public unicast space that the ruleset's prefixes and the host's routes cut. A flow to any internet address, the tunnel connector's or tailscaled's, holds only when every cell passes. A flow from any internet address, to the controller listener or to NPM's public binding, fails only on a cell the change itself blocks: a source the loaded rules already drop or reject does not refuse a later change, and the guard does not report it, while a source the guard cannot decide still refuses. A refusal names the first blocked cell. A flow to a wildcard bind is checked on every non-link-local address of its family on every up, non-loopback interface, and an NPM exposure recorded on `[::]:443` is checked on both families. The guard reads the policy rules: their prefixes cut the cells, and their protocol and port selectors are carried on route lookups (`internal/modules/firewall/predict`, `internal/modules/firewall/eval`, `internal/modules/firewall/guard`, `internal/controller/api`, `internal/modules/panel`).
- A change stays pending until confirmed and the host reverts it by itself after the confirmation timeout (60 seconds by default), so the revert happens even when the client disconnects (`internal/core/lifecycle`, `internal/modules/firewall`).
- A snapshot is taken before every change, and a break-glass rollback and restore through the agent socket work without the controller (`internal/cli`, `internal/modules/firewall`).
- The module holds one read-write lock per instance. Every path that changes UFW's files, the loaded rules or the Docker block takes it for writing, and every status read and plan preparation takes it for reading, so a plan or a status read never sees a state between a file write and the chain load or reload that follows it (`internal/modules/firewall`).
- The Docker integration is a marked block with its own implementation that pins a container's allowance to its published binding (`internal/modules/firewall/dockerfw`).
- The exposure view computes reachability per port and the harness checks it against an external scanner. The parsers of UFW files and ruleset output are fuzzed (`internal/modules/firewall`, `internal/modules/firewall/exposure`, `test/harness`).

#### Residual risk

- `ufw default` restarts an active UFW, and enabling, disabling and repairing UFW, a Docker repair whose differences reach outside Darkpanel's chains, and restoring a snapshot, restart it; the built-in policies are ACCEPT for about a second in that window.
- A break-glass process outside the agent is not covered by the lock, because it runs when the agent cannot be reached.
- An overridden guard can still cut the operator off, and the revert timer is the only net.
- Docker network modes the integration cannot protect are reported as unprotected.
- Manual rules are shown and never removed.
- The cells cover the public unicast space only. `0.0.0.0/8`, `10.0.0.0/8`, `100.64.0.0/10`, `127.0.0.0/8`, `169.254.0.0/16`, `172.16.0.0/12`, `192.0.0.0/24`, `192.0.2.0/24`, `192.168.0.0/16`, `198.18.0.0/15`, `198.51.100.0/24`, `203.0.113.0/24`, `224.0.0.0/4`, `240.0.0.0/4`, `2001:db8::/32` and all IPv6 outside `2000::/3` are left out, so an any-address flow is not protected from or to an address there. A protected flow with a concrete address, such as an SSH session from a private network, is checked whatever its range (`internal/modules/firewall/eval`).
- The guard does not know a reply's destination port, and its route lookups carry no TOS or user. While a policy rule selects by `dport`, `tos` or `uidrange`, or by a firewall mark while a `raw` or `mangle` rule may set one, a missing route no longer skips a source cell, so such a host refuses guarded changes until overridden. A forwarded or outbound flow whose source port is not known is looked up without one, so a policy rule selecting by `sport` does not match that lookup and the guard may evaluate the flow on another route. The guard also does not read `dscp` or flow label selectors, so where a policy rule selecting by `uidrange`, `tos`, `dscp` or flow label moves a protected flow to another route, the guard may evaluate that flow on the route the lookup gives and pass a change that blocks the route it really takes (`internal/modules/firewall/guard`).
- Inside a cell whose longest route is a blackhole, the guard takes any EINVAL from a route lookup as "no route", because the kernel answers a lookup there with EINVAL. An EINVAL there for another reason also skips the cell (`internal/modules/firewall/guard`).
- A check that would cost too much fails closed: more than 65,536 cells, or cells times rules above 10,000,000. About 2,200 bans that can match a protected flow's port, or a full BGP table, refuse guarded changes until overridden. The limit counts neither the addresses and interfaces each cell is probed on nor the comparison of each cell with the route table, so a check under it on a host with many addresses, interfaces or routes can hold the request far longer than the limit suggests (`internal/modules/firewall/guard`).
- A wildcard bind is checked on the host's bridge and tailnet addresses too. A hand-written allowance that names only the uplinks fails the guard of a revert that already had blocked effects, conflicts or a write error. That guard has no before-state, so a block the host already had also fails it, including a drop of port 443 on the IPv6 public leg of an NPM exposure on a host with a global IPv6 address, and the failure restores the snapshot whole, over unresolved effects, conflicts and manual edits, for a block the change did not cause. A direct panel client or an agent peer is a concrete source, so its flow is not compared with the rules before the change: while a wildcard panel or agent listener is allowed only on some of the host's addresses, a guarded change that protects such a flow is refused until overridden, even when the change does not touch those rules (`internal/modules/firewall/guard`).

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
- A proxy host that points at the panel or at NPM itself, by address or through a DNS name.
- A local user who creates Nginx Proxy Manager's first administrator before Darkpanel does.
- A compromised NPM exhausting an agent.
- A revoked exposure coming back.

#### Mitigations

- Runtime listeners accept only HTTPS from one allowed peer, read exactly one client header set by a proxy Darkpanel configured (`X-Darkpanel-Client` or `Cf-Connecting-IP`) and ignore `X-Forwarded-For` and `X-Real-IP`. They refuse a request whose forwarded protocol is not https, and refuse an unsafe request whose Origin is not the exposure's public host, compared with the exposure's public host, which is stored in lower case without a trailing dot (`internal/controller/listen`, `internal/controller/api`).
- Login over an exposure requires TOTP and every page shows a banner. Revocation is a durable `revoked` state that is checked on every request and stream and that nothing reverses; `sudo darkpanel panel listener revoke` is the break-glass (`internal/modules/panel`, `internal/controller/api`, `internal/controller/listen`, `web/src/modules/panel/banner.tsx`).
- NPM's admin port 81 is published on 127.0.0.1 only, and its HTTPS exposure is an opt-in action. The banner warns about it on every page, and, because it reads the list again, also about each host whose status could not be read (`unreadable`) and about a failed read (`web/src/modules/npm/banner.tsx`, `internal/modules/npm`).
- Outside the admin-exposure composite, a proxy host may not forward to any port of NPM's own container names or addresses, or to the panel gateway; the refusal is `npm.reserved`, and port 81 stays allowed inside that composite. A forward host that is a DNS name is resolved at plan and again at apply: a name that a container on one of NPM's networks answers to takes that container's addresses from Docker, any other name is resolved on the agent's host; every answer is checked as an address, a name with no answer is invalid input, and a set of answers that changed between the two is refused as `plan.state_changed`. The undo of a proxy host's update or removal checks the host's forward host that way and each custom location it can check (`internal/modules/npm/proxy`, `internal/modules/npm`).
- The undo of a proxy host restores a custom location it cannot check, and still refuses one it can show is reserved (`internal/modules/npm/proxyhost_edit.go`).
- The agent's NPM client limits the size of every answer and drains refusals. It keeps only the status and an allowlisted error code of a refusal, so NPM's message text never reaches an error, a log line or a change record (`internal/modules/npm/npmapi`).
- The public check honours the sources a Docker binding denies (`internal/modules/npm`).
- Each runtime listener keeps its own TLS session tickets, so a session resumed on one listener is not accepted by another (`internal/controller/listen`).
- When the creation of NPM's first administrator finds one that Darkpanel did not create, the deploy stops and removes the container, the data volumes and the network it created before it fails (`internal/modules/npm`).

#### Residual risk

- A forward host that is a DNS name and is re-pointed after it was applied is not checked again, and nginx in NPM's container may resolve a name differently from the agent's host. The accepted risk is listed in Known residual risks.
- A container on a network joined through the NPM attach action shares that network with NPM and reaches its admin login on port 81 and its API on port 3000 inside the container; the admin password is 32 random bytes.
- Let's Encrypt's duplicate-certificate limit can refuse a repeated add.

### DERP host

#### Threats

- A tampered derper binary, and a downgrade to an older artifact.
- A derper that relays for strangers.
- A version mismatch that makes client verification fail open or closed wrongly.
- A service account that Darkpanel did not create being removed.
- The probe used against internal targets.
- A compromised `darkpanel-derp` account that redirects or blocks a read the agent makes as root.

#### Mitigations

- Derper is built by the release workflow from upstream at a pinned version with a pinned module environment, and is listed in a manifest of per-architecture SHA-256 digests that is verified through the release's signature. The manifest parser refuses duplicate members (`internal/modules/derp/artifact`, `release/releasetool`).
- The agent installs derper as a systemd service under the system user `darkpanel-derp` with `NoNewPrivileges=yes` and only `CAP_NET_BIND_SERVICE`. It records that it created the user only after `useradd` succeeded, so uninstall never removes an account somebody else made (`internal/modules/derp`).
- Derper runs with `-verify-clients` unless the install input sets `verifyClients: false`, a supported opt-out whose plan warns that any node that knows the region can relay through the server. With the flag, derper checks every client's node key against the host's own `tailscaled` and refuses the client when `tailscaled` fails (`internal/modules/derp`).
- The host's `tailscaled` is pinned and held at the derper version, and a pin that fails rolls the package back, and so does a failed step that writes the repository file, on a context of its own, even when the forward step's context has been cancelled (`internal/modules/tailscale`, `internal/modules/tailscale/package_pin.go`).
- Derper's node key file lives in `/var/lib/darkpanel/derp`, a directory owned by `darkpanel-derp`. Darkpanel creates it at mode 0700 and gives it to the service user at that mode; a directory the service user already owns is accepted as it is, without a mode check (`internal/modules/derp/server/server.go`, `internal/platform/files_servicedir.go`). Darkpanel does not set the mode of the key file itself, which derper writes.
- The agent reads DERP certificate and key files through the platform's descriptor-based reads: no symlink beneath the service directory, a regular file only, and at most 1 MiB, so a compromised `darkpanel-derp` account cannot redirect a root read or block the agent with a FIFO or a huge file (`internal/platform`, `internal/modules/derp`).
- Certificate reads refuse symbolic links only beneath the service directory that holds the certificates, so a link the host owner placed at or above that directory keeps working (`internal/modules/derp/deps.go`).
- `derp.node.probe` needs the `operate` scope. The probe uses a throwaway node key (`internal/platform/tslocal/derp.go`). It accepts only an IP literal or a lower-case DNS name of at least two labels made of letters, digits and hyphens, and refuses loopback, unspecified, multicast, link-local, RFC 1918, CGNAT (`100.64.0.0/10`) and ULA (`fc00::/7`) addresses, as literals and among the addresses a name resolves to, after it unmaps IPv4-mapped IPv6 addresses. A failed probe reports one value of a fixed set (`timeout`, `connection_refused`, `unreachable`, `tls_handshake_failed`, `certificate_not_trusted`, `pin_mismatch`, `bad_answer`, `no_stun_answer`, `failed`), and the raw error appears only in the agent's debug log (`internal/modules/derp/node_probe.go`).

#### Residual risk

- Holding `tailscaled` at derper's version delays its security updates on that host.
- A failed probe reports only a fixed error value, so finding the cause of a failure needs the agent's debug log.
- The probe refuses the special-purpose forms that reach internal networks, among them `64:ff9b::/96`, `0.0.0.0/8`, `240.0.0.0/4` and `198.18.0.0/15` (`internal/modules/derp/node_probe.go`). Documentation ranges stay allowed and there is no allowlist. Only a caller with the `operate` scope can choose the target.
- Derper itself answers unauthenticated STUN and HTTPS probes from the internet.

## Known residual risks

These risks stay open. The owner accepted the first four for this release after the security review; the last is inherent in resolving a name at a point in time.

- `Origin`: a request without an Origin header is accepted for a cookie session on an unsafe method through a runtime listener. The SameSite=Strict cookie and the HMAC CSRF token that every unsafe request must carry still guard it, and requiring Origin would add no attacker that those checks let through. The owner accepted this.
- `port 81`: NPM serves its admin login on port 81 and its API on port 3000 inside its container, on every interface, so any container on a network joined through the attach action reaches both; only the host side of port 81 is bound to loopback. The admin password is 32 random bytes and the pinned image offers no way to bind the ports to loopback, so a joined container gains an authentication surface and nothing more. The owner accepted this.
- `openpgp`: the `openpgp` package of `golang.org/x/crypto` carries advisory GO-2026-5932 and no tagged release carries the fix. Darkpanel imports nothing from it. The pin stays until a tagged release carries the fix. The owner accepted this.
- `enrollment budget`: the global enrollment budget is 60 requests a minute across all sources, so a peer that reaches the agent listener, on the tailnet by design, can use it up and block further enrollment for the rest of the minute. It affects enrollment only, and an enrollment token lasts one hour, so a sustained flood forces a new token. The owner accepted this.
- `forward host`: a forward host that is a DNS name is checked when the plan is made and again when it is applied, and a name that is re-pointed after it was applied is not checked again, so a name the caller controls can later resolve to an address that was refused at apply. nginx in NPM's container resolves a name that no container answers to through the DNS servers configured on the host, which can answer differently from the agent's own resolver, for example for a name only the host's `/etc/hosts` holds.
