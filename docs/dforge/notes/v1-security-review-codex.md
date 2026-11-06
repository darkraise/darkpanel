# Security review — Codex gpt-6-astra

## Findings

R73 is not satisfied: confirmed high findings remain. No critical finding was established in this read-only review of `main` at `dc91aa2`.

### F1: Container inspection exposes the first CMD argument

- Severity: high
- Confidence: confirmed
- Area: Secret handling and delivery
- Location: `internal/modules/docker/inspect.go:119`, `internal/modules/docker/inspect.go:167`, `internal/modules/docker/wire.go:189`
- Prior row: none
- Description: `redactArgs` preserves element zero. Docker’s `Config.Cmd` can contain arguments to an entrypoint, so its first element need not be an executable name. The `read`-scope inspection action returns that element without further redaction.
- Failure scenario: An ordinary application container uses an entrypoint that accepts its credential as the first CMD argument. A holder of a `read` token calls `docker.container.inspect` and obtains the credential, bypassing admin-only secret delivery.
- Fix: Mask every CMD element. Preserve an executable name only where its position and meaning are established independently; do not infer that every argument array begins with a program.

### F2: NPM configuration snapshots disclose credentials through the effects API

- Severity: high
- Confidence: confirmed
- Area: Secret handling and delivery
- Location: `internal/modules/npm/proxyhost_edit.go:60`, `internal/modules/npm/proxyhost_edit.go:336`, `internal/modules/npm/proxyhost_edit.go:525`, `internal/modules/npm/effects.go:77`, `internal/core/lifecycle/effects.go:90`, `internal/controller/api/changes.go:201`
- Prior row: none
- Description: Updating or removing a proxy host records its complete advanced configuration, including custom-location configuration, in plaintext effect documents. The journal’s separate sealed-secret facility is unused. Effect documents are available to `read` tokens.
- Failure scenario: An administrator configures an upstream credential in NPM using `proxy_set_header Authorization "Bearer …"`. A subsequent Darkpanel update or removal saves that configuration in `before_json`. A `read` token retrieves the change’s effects and obtains the credential.
- Fix: Store configuration needed for restoration in the effect’s sealed secret. Public comparison documents should contain redacted fields and keyed fingerprints, including for custom locations. Apply the same treatment to observations and candidate documents.

### F3: Docker labels remain plaintext in plans and change records

- Severity: high
- Confidence: confirmed
- Area: Secret handling and delivery
- Location: `internal/modules/docker/network_manage.go:44`, `internal/modules/docker/volume_manage.go:74`, `internal/modules/docker/wire.go:276`, `internal/modules/docker/wire.go:283`, `internal/core/lifecycle/engine.go:445`, `internal/core/lifecycle/apply.go:245`, `internal/modules/docker/dstate/object.go:62`
- Prior row: 384 — open is correct; “minor” understates the plaintext disclosure and should become high.
- Description: Network and volume label values appear verbatim in plan diffs. Their input fields lack secret tags, so schema redaction also leaves them in `core_changes.input_json`. Separately, effect documents expose unsalted SHA-256 values suitable for offline guessing.
- Failure scenario: An administrator creates a network or volume with a credential-bearing label. Darkpanel persists the credential outside sealed storage. Readers of change records can obtain it; low-entropy values represented only by effect digests can also be guessed offline.
- Fix: Redact label values from diffs and recorded inputs. Use keyed fingerprints for comparison-only documents, and sealed storage wherever restoration requires the original value. Address existing plaintext records and rotate exposed credentials.

### F4: NPM response messages can carry secrets into agent logs and change errors

- Severity: high
- Confidence: confirmed
- Area: Secret handling and delivery
- Location: `internal/modules/npm/npmapi/client.go:77`, `internal/modules/npm/npmapi/client.go:243`, `internal/modules/npm/npmapi/errors.go:16`, `internal/modules/npm/deps.go:206`, `internal/modules/npm/proxyhost_add.go:245`, `internal/agent/api/handler.go:864`
- Prior row: none
- Description: The NPM client trusts `error.message`. Login failures retain it as an error cause, which the agent logs for 5xx responses. Other NPM 4xx messages are copied directly into client-facing details and can enter change records. A sanitized outer message does not sanitize its cause.
- Failure scenario: A compromised NPM API receives the stored administrator password in `/api/tokens`, then returns it inside `error.message`. A normal proxy-host list request causes the agent to log that password. NPM can similarly reflect its bearer token through subsequent errors.
- Fix: Discard upstream free-text messages at the client boundary. Retain only status and allowlisted categorical codes; do not attach the original response as a logged cause. Apply this to both login and mutation errors.

### F5: DNS forward hosts bypass NPM’s reserved-target restrictions

- Severity: high
- Confidence: confirmed
- Area: Exposure
- Location: `internal/modules/npm/proxy/upstream.go:38`, `internal/modules/npm/proxyhost_add.go:42`, `internal/modules/npm/proxyhost_add.go:258`, `internal/modules/npm/composite_admin.go:14`
- Prior row: 326 — open is correct; classify its “Important” finding as high and release-blocking.
- Description: The reserved-target check recognizes literals and selected names but resolves no DNS. Ordinary proxy-host mutations require `operate`, whereas the reserved admin-exposure composite requires `admin`.
- Failure scenario: An `operate` token creates a proxy host forwarding to an attacker-controlled DNS name resolving to `127.0.0.1`, port 81. NPM exposes its admin interface through the ordinary proxy-host path, bypassing the admin exposure’s authorization, reservation and forced-HTTPS controls. A name resolving to the panel gateway similarly bypasses that target check.
- Fix: Validate destinations using NPM’s actual resolution context and prevent subsequent rebinding. Pin validated destinations or enforce destination restrictions at connection time; a one-time controller-side DNS lookup is insufficient.

### F6: The firewall guard omits the public leg of NPM panel exposures

- Severity: high
- Confidence: confirmed
- Area: Firewall
- Location: `internal/controller/api/runtime.go:30`, `internal/controller/dispatch/guard.go:51`, `internal/modules/firewall/guard/flows.go:238`, `internal/modules/firewall/actions_container.go:75`
- Prior row: none
- Description: Runtime requests construct a `ProxyPath` without its `Public` bindings. Dispatch preserves that empty list. The guard therefore checks the proxy-to-controller connection but generates no browser-to-NPM public flow.
- Failure scenario: An operator connected through an NPM panel exposure removes the firewall allowance for NPM’s published HTTPS binding. With the other protected flows still permitted, the guard accepts the change without an override, although new connections through that exposure fail. Automatic revert remains a separate recovery mechanism; it does not make this guard decision correct.
- Fix: Populate public bindings from authoritative exposure and Docker state. Treat missing bindings for an NPM exposure as incomplete topology and fail closed. Protect the actual client’s path through the published binding.

### F7: Fixed sample addresses cannot establish that protected internet flows remain reachable

- Severity: high
- Confidence: confirmed
- Area: Firewall
- Location: `internal/modules/firewall/guard/flows.go:28`, `internal/modules/firewall/guard/flows.go:265`, `internal/modules/firewall/guard/flows.go:395`, `internal/modules/firewall/guard/route.go:48`, `internal/modules/firewall/eval/eval.go:279`
- Prior row: none
- Description: Tunnel and Tailscale flows substitute `198.51.100.1` or `2001:db8:ffff::1` for internet destinations. The evaluator treats these as concrete addresses, using ordinary prefix containment. The `standIn` flag changes routing-error handling, not address-match semantics.
- Failure scenario: An operator adds routed denies covering a connector’s real Cloudflare edge destinations while leaving the documentation address permitted. The synthetic tunnel probes pass, but connector reconnection fails. Address-specific outbound restrictions can similarly evade the intended Tailscale protection.
- Fix: Evaluate the relevant address ranges symbolically or partition them by the rules’ matching prefixes. Include actual endpoints and fail closed when an address-dependent rule prevents proving the required reachability.

### F8: DERP service-owned files can redirect root reads or block the agent

- Severity: high
- Confidence: confirmed
- Area: Command execution; DERP host
- Location: `internal/modules/derp/deps.go:120`, `internal/modules/derp/server_status.go:107`, `internal/modules/derp/server_install.go:315`, `internal/modules/derp/cert_renew.go:73`, `internal/modules/derp/cert_renew.go:107`
- Prior row: none
- Description: Production certificate and key-presence checks use `os.ReadFile` inside directories writable by `darkpanel-derp`. They bypass the platform’s descriptor-based symlink protections and do not bound reads or reject special files.
- Failure scenario: An attacker controlling the unprivileged DERP account replaces the certificate or certificate directory with a symlink. A status or renewal operation then reads the chosen target as root. A FIFO or an unbounded source can instead block the operation or exhaust memory. Arbitrary file-content disclosure was not established, but redirected root reads satisfy the supplied high-severity criterion.
- Fix: Read through securely opened directory descriptors, refusing symlinks throughout traversal. Require regular files and impose size limits. Replace key-presence reads with safe descriptor-based checks.

### F9: NPM’s own port 3000 is accepted as an ordinary upstream

- Severity: high
- Confidence: plausible
- Area: Exposure
- Location: `internal/modules/npm/proxy/upstream.go:47`, `internal/modules/npm/proxyhost_add.go:42`
- Prior row: 330 — open is correct; “minor” is inadequate if the backend is reachable on the container interface.
- Description: The self-target restriction is conditional on port 81. `darkpanel-npm:3000` passes. Whether the pinned image exposes its backend on that interface remains unverified.
- Failure scenario: If the backend listens beyond loopback, an `operate` token creates a public proxy to `darkpanel-npm:3000`, exposing an administrative service outside the admin-only exposure flow.
- Fix: Reserve all ports on NPM’s own names and addresses, with narrowly defined composite exceptions. Independently verify the pinned image’s bind addresses. Until that verification, treat the administrative-exposure impact as conditional.

### F10: Compose diagnostics are copied into errors without secret filtering

- Severity: high
- Confidence: plausible
- Area: Compose execution; Secret handling and delivery
- Location: `internal/modules/docker/compose_check.go:149`, `internal/modules/docker/compose_check.go:535`, `internal/modules/docker/compose_run.go:281`, `internal/core/lifecycle/apply.go:459`
- Prior row: none
- Description: Failed Compose configuration and execution copy the last stderr line into an error. The only transformation is truncation to 300 bytes. Execution failures can persist that detail in the change record.
- Failure scenario: An operator mistakenly interpolates a secret from an environment file into an invalid image, port or another validated value. If Compose quotes that value in its diagnostic, Darkpanel logs or journals the secret. The data path is confirmed; the installed Compose version’s diagnostic for these inputs was not inspected or executed.
- Fix: Return categorical failures and exit status without raw stderr. If detailed diagnostics are retained, use a separate protected delivery mechanism rather than ordinary errors, logs or change records.

### F11: Runtime login sources bypass IPv6 prefix normalization

- Severity: medium
- Confidence: confirmed
- Area: Authentication
- Location: `internal/controller/api/server.go:209`, `internal/controller/auth/ratelimit.go:68`
- Prior row: none
- Description: Runtime login sources are formatted as `<client> via <proxy>`. Neither address parser accepts this format, so the limiter uses it verbatim instead of grouping IPv6 clients by `/64`.
- Failure scenario: An internet attacker rotates addresses within one IPv6 `/64` and varies usernames through an exposure. Each address obtains a separate source bucket, permitting substantially more guessing and hash-work pressure than the intended prefix limit. Per-username locks and hash concurrency limits still apply.
- Fix: Separate the audit source description from the rate-limit key. Pass the parsed client address to the limiter and retain both addresses only in audit metadata.

### F12: The DERP probe exposes private-network reachability and TLS diagnostics

- Severity: medium
- Confidence: confirmed
- Area: DERP host
- Location: `internal/modules/derp/node_probe.go:27`, `internal/modules/derp/node_probe.go:106`, `internal/modules/derp/node_probe.go:275`, `internal/modules/derp/deps.go:181`
- Prior row: 306 — open is correct; classify as medium for the network and certificate-information disclosure.
- Description: A `read` action accepts private and CGNAT destinations and returns raw probe errors. Those errors can disclose internal certificate names and connection outcomes.
- Failure scenario: A `read` token submits private addresses with a chosen TLS hostname and uses the managed host to discover reachable services and certificate identities. This is a bounded TLS/STUN probe, not arbitrary HTTP response retrieval.
- Fix: Restrict private destinations to explicitly authorized DERP targets or ranges. Return categorical TLS failures instead of raw certificate-verification errors.

### F13: NPM bootstrap exposes an unauthenticated first-admin window

- Severity: medium
- Confidence: plausible
- Area: Exposure
- Location: `internal/modules/npm/deps.go:440`, `internal/modules/npm/instance_deploy.go:274`, `internal/modules/npm/instance_deploy.go:397`, `internal/modules/npm/instance_deploy.go:410`, `internal/modules/npm/npmapi/client.go:60`
- Prior row: none; row 29 acknowledges container-network access to port 81, not this bootstrap race.
- Description: The container starts with port 81 published before Darkpanel creates its first administrator. The client contract describes unauthenticated first-user creation, and the repository’s NPM fake implements it. Bootstrap polls at two-second intervals. The actual pinned NPM implementation was not inspected.
- Failure scenario: An unprivileged local user repeatedly submits first-admin creation to `127.0.0.1:81` during initial deployment. If it wins, Darkpanel detects an existing administrator and fails deployment, but the compensable failure path does not automatically remove the running container. The attacker controls that newly created NPM instance.
- Fix: Isolate the bootstrap API so only the agent can reach it until administrator creation and login verification finish. On bootstrap conflict, stop or isolate the container before reporting failure.

### F14: Existing DERP service directories are not restored to mode 0700

- Severity: low
- Confidence: confirmed
- Area: DERP host
- Location: `internal/platform/files_servicedir.go:341`, `internal/modules/derp/server/server.go:38`
- Prior row: none
- Description: `handOver` immediately succeeds when the directory already belongs to the service UID. It neither checks nor repairs its permissions, contrary to the claim that the directory is kept at mode 0700.
- Failure scenario: A service-owned directory drifts to mode 0755 or a writable mode. A later ensure operation accepts it unchanged. Exposure of actual key bytes depends on the files’ own permissions and was not established.
- Fix: Enforce mode 0700 through the opened descriptor for existing service-owned directories and declared subdirectories. Document the limits of maintaining permissions on directories writable by the service.

### F15: The threat model describes optional DERP client verification as unconditional

- Severity: low
- Confidence: confirmed
- Area: DERP host
- Location: `internal/modules/derp/server_install.go:49`, `internal/modules/derp/server_install.go:161`, `internal/modules/derp/server/server.go:164`
- Prior row: 26 — its default-on description is correct; the threat-model wording is inaccurate.
- Description: Explicit `verifyClients:false` omits `-verify-clients`. The plan warns about this, so this is an inaccurate mitigation claim rather than a bypass of the default.
- Failure scenario: An operator selects the supported opt-out and runs a relay that admits strangers, despite the threat model’s unconditional description.
- Fix: State that verification is enabled by default, can be explicitly disabled with a warning, and fails closed when enabled.

## Mitigation claims not held

- Firewall — “A lockout guard predicts the effect of a change on the protected flows and refuses it unless explicitly overridden” — public NPM bindings are absent, and concrete sample destinations do not represent all protected destinations — findings F6 and F7.
- Exposure — “Proxy hosts that forward to NPM's own port 81 or to the panel gateway are refused as reserved” — DNS names resolving to those destinations pass the check — finding F5.
- Authentication — “A login rate limit by user name and by source address applies a lock” — runtime source formatting defeats the limiter’s intended IPv6-prefix accounting; username locks remain effective — finding F11.
- DERP host — “created and kept at mode 0700” — existing service-owned directories are accepted without checking or repairing their mode — finding F14.
- DERP host — “Derper runs with `-verify-clients`” — this is the default, with an explicit supported opt-out — finding F15.
- Secret handling and delivery — “The CLI takes secrets only from a prompt or standard input, never a flag” — `internal/cli/input.go:263` also accepts secrets from a JSON input file. No impact on process-list secrecy: the flag contains the file path, not its secret contents.
- Authentication — “runtime listeners check the Origin” — absent Origin is accepted; supplied origins are checked and unsafe cookie requests still require CSRF validation. No demonstrated CSRF impact from this exception.

## Known residual risks

- `Origin`: **accept** — `internal/controller/api/server.go:315` checks supplied origins and requires the CSRF token for unsafe cookie requests; cookies are SameSite=Strict. No bypass was established from omission alone. Record acceptance of row 245 and preserve these controls — severity if unfixed: **low**.
- `RFC 1918`: **fix** — constrain private targets to explicitly authorized DERP destinations and replace raw TLS errors with categorical results. Preserve private diagnostics only through that explicit authorization. See F12 and row 306 — severity if unfixed: **medium**.
- `forward host`: **fix** — enforce reserved destinations using NPM’s resolution context and protect against rebinding. See F5 and row 326 — severity if unfixed: **high**.
- `port 81`: **fix** — isolate unauthenticated bootstrap as described in F13. After bootstrap, accepting authenticated admin-login reachability from deliberately attached networks is reasonable for v1 with the generated password and documented trust boundary; loopback publication alone does not provide bootstrap isolation — severity if unfixed: **medium**.
- `port 3000`: **fix** — reserve every port of NPM’s own names and addresses, and verify the pinned backend’s bind configuration. See F9 and row 330 — severity if unfixed: **high**, conditional on backend reachability.
- `digest`: **fix** — redact plaintext diffs and inputs, and replace public unsalted label hashes with keyed fingerprints. Adding a public salt alone would not prevent dictionary guessing. See F3 and row 384 — severity if unfixed: **high**.
- `openpgp`: **accept** — repository source searches found no production OpenPGP import or call. Retain row 429 as dependency tracking and adopt a suitable fixed tagged release when available. The advisory’s current upstream status was not checked because network commands were prohibited — severity if unfixed: **low** for the reviewed code path.

## Coverage

- Reviewed authentication, authorization, sessions, CSRF, login limiting, reveal routing and runtime request handling in `internal/controller/auth`, `internal/controller/api`, and relevant dispatch/core scope code.
- Reviewed TLS identities, enrollment, renewal, revocation, live-session registration and agent API trust boundaries in `internal/transport`, `internal/controller/fleet`, and `internal/agent`.
- Reviewed command allowlists, fixed environments, file-provider protections, service directories, Compose validation/execution, Docker inspection and label handling.
- Reviewed sealing/redaction, lifecycle journals and effects, secret delivery, module log masking, backup framing, staging, restore validation and quarantine paths.
- Reviewed firewall prediction/evaluation, protected-flow construction, mutation locking, snapshots and automatic-revert handling.
- Reviewed cloud credential/client handling, auth-key creation and sweeping, credential cleanup gates, and Cloudflare connector secret handling.
- Reviewed listener/exposure controls, NPM upstream reservations, bootstrap, API errors and configuration snapshots; DERP installation, service configuration, artifacts, certificates and probes.
- Reviewed update signature/checksum verification, extraction/staging, runner entry points and CLI restrictions. Reviewed web request/session handling, change-output caching, and searched for script-injection sinks.
- Read the threat model and parser inventory and checked findings against the register. Coverage was concentrated on these security paths, not every line of every module. Pinned container internals, external dependency implementations and deployment behavior were not verified. No builds, tests or network commands were run; no repository files were changed.