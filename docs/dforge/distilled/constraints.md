# Constraints

Rulings and policy that bind future work.

## Transport

### A shared tls.Config needs a session-resumption test
Any plan that shares one `tls.Config` across connections, or enables TLS session tickets, must add a session-resumption test that uses shared ticket keys. `TestHandshakeNoResumption` (internal/transport/server_test.go) cannot fail while each handshake builds a fresh config with `SessionTicketsDisabled: true`, so it does not cover a shared config.
Set by: owner, 2026-10-21
Scope: internal/transport and any code that builds or reuses a tls.Config for agent or controller connections
Source: docs/superpowers/registers/2026-10-19-m2-fleet-lifecycle-core.md@9ad5d19 row 45
