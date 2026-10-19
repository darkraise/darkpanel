# ADR 0010: Web certificate authority

**Requirements:** R20

## Status

Accepted

## Context

ADR 0006 has the Tailscale web listener present a certificate from the
internal authority when `tailscale cert` is not available, and ADR 0003 makes
that authority Ed25519. The CA/Browser Forum Baseline Requirements (section
6.1.5) admit only RSA and ECDSA P-256, P-384 and P-521 keys in publicly
trusted TLS certificates, so browsers cannot be relied on to verify an Ed25519
chain, and the two decisions together could leave the Tailscale web listener
unusable in a browser.

M1 serves the web UI over HTTPS on the Tailscale addresses before the agent
authority exists (M2) and before `tailscale cert` can be obtained through the
agent (M5). Accepted ADRs are not edited, so the change is recorded here.

## Decision

- A separate root for web certificates only: ECDSA P-256, valid 10 years,
  created on the controller's first start. Its private key is sealed with the
  controller key and stored in `controller.db` (table `core_ca`, purpose
  `web`), as ADR 0005 stores the certificate authority key.
- The root issues one ECDSA P-256 leaf for the HTTPS listeners, valid 90 days.
  It is renewed after 60 days, on any change of the Tailscale addresses and on
  any change of the HTTPS bind addresses. Its SANs are the hostname,
  `localhost`, the Tailscale IP addresses and every explicit HTTPS bind
  address. The leaf lives in memory only; HTTPS listeners present the chain
  `[leaf, root]`.
- `darkpanel controller web-ca` prints the root certificate in PEM and its
  SHA-256 fingerprint through the controller socket, so the operator can trust
  it in a browser and pin it in the CLI.
- Agent identity stays on the Ed25519 authority of ADR 0003 from M2. ADR
  0006's `tailscale cert` path is unchanged; this root serves the case where
  that certificate is not available.

## Alternatives considered

- Ed25519 web certificates from the ADR 0003 authority: rejected; browsers
  are not required to accept them.
- One ECDSA authority for web and agent certificates: rejected. It would
  reopen ADR 0003, and a root that operators install in their browsers would
  also vouch for agents.
- A self-signed leaf without a root: rejected. Every renewal would need a new
  browser exception and a new CLI pin.

## Consequences

- An operator trusts one root per controller, once, in the browser and as
  `caFingerprint` in the CLI configuration; leaf renewals need no action.
- Losing `controller.key` loses the web root's key with the rest of the
  sealed data; a new root must then be trusted again.
- The MagicDNS name joins the SANs when M5 reads it from the Tailscale API.
