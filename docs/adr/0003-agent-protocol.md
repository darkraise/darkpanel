# ADR 0003: Controller–agent protocol, identity and enrollment

**Requirements:** R1, R18, R22, R23, R24, R25, R27, R28

## Status

Proposed

## Context

Agents open no listening port and dial out (product spec Constraints). The same
agent handler must also serve the local CLI. The review found four problems
this ADR must close:

- Finding 1: one authority signing both roles let an agent impersonate the
  controller, the class of flaw behind 1Panel CVE-2025-54424.
- Findings 5 and 6: enrollment and revocation were underspecified.
- Finding 21: offline detection was undefined.
- Finding 23: 90-day certificates locked out hosts that stay offline for a
  month.

For names, action classes, errors, actors and version skew, see the shared
contracts in the index.

## Decision

- **Identity.** The controller runs an internal Ed25519 certificate authority
  (10-year lifetime, key custody in ADR 0005). Controller certificates carry
  `serverAuth` and the URI SAN `darkpanel://controller/<controller id>`; agent
  certificates carry only `clientAuth` and `darkpanel://host/<host id>`. Agents
  verify the chain and require the controller SAN they pinned at enrollment;
  the controller requires a host SAN, a known host and an unrevoked serial.
- **Transport.** The agent dials the controller's agent listener (default port
  7443, bound to the Tailscale interface by default; any other bind is
  explicit). TLS 1.3, ALPN `darkpanel-agent/1` requires a client certificate;
  ALPN `darkpanel-enroll/1` accepts none and serves only enrollment. The
  connection carries a yamux session with stream limits and deadlines; the
  controller sends HTTP/1.1 requests to the agent handler over streams it
  opens, and the agent pushes events over streams it opens.
- **Agent endpoints.** `GET /agent/v1/hello`; `POST /agent/v1/actions/{name}/{version}/{plan|apply|confirm|revert}`;
  `GET /agent/v1/resources/{module}/{resource}`; `POST /agent/v1/journal/ack`.
- **Hello** is exchanged both ways: protocol version, binary version, OS, and
  for the agent its modules, action versions and schema hashes. A protocol or
  version outside the supported window is shown on the host (R27) and only
  updates are dispatched.
- **Liveness.** Heartbeat every 15 s; a host is offline after 45 s without one,
  and actions to it fail immediately.
- **Enrollment.** The admin creates a host record; the controller issues a
  single-use token bound to that host id: 128-bit random, stored hashed,
  valid one hour, compared in constant time, consumed atomically, rate
  limited. The token carries the authority fingerprint and controller SAN. The
  agent verifies the controller against them before sending the token, then
  sends a certificate signing request; the controller ignores any identity in
  the request and issues a certificate for the bound host id.
- **Certificates and revocation.** Agent certificates last one year and are
  renewed after two thirds of their life; renewal revokes the previous serial.
  Revocation is by serial, checked at every handshake, and closes the host's
  live session. An agent with an expired certificate re-enrolls with a new
  token.
- **Journal delivery.** The agent journal is forwarded in sequence order; the
  controller acknowledges by sequence and deduplicates; acknowledged entries
  are pruned, unacknowledged ones are kept up to a size cap.

## Alternatives considered

- Controller connects in to agents: rejected by the owner on 2026-10-17; it
  needs an inbound port per host.
- WebSocket with JSON-RPC: rejected; it needs a custom RPC layer and a
  separate API for the local CLI.
- gRPC bidirectional streams: rejected; it adds a second schema system and
  larger binaries.
- Reverse HTTP/2 over the dialed connection: rejected; only the client opens
  streams, so agent-pushed events would need long-polling.
- 90-day certificates: rejected; see Context.

## Consequences

- The controller keeps a host table, revoked serials and per-agent schema
  hashes.
- The agent listener has no web routes.
- Admins create a host record before enrolling.
- Enrollment tests must cover token reuse, a CSR claiming another host id, a
  revoked serial and a closed live session.
