# w0c plan: what remains before the plan review

The plan `docs/dforge/plans/2026-10-23-v1-w0c-extensions-and-web.md` holds
all 49 tasks (renumbered from the drafting labels by `assemble.py`; the
label-to-number map is `map.json`). Its task bodies, Depends-on lines,
waves and index are final. The header above `## Task index` still uses the
old 31-task numbering and needs these edits:

## 1. Task numbers in the header

Old → new: 1→1; 2→2 (listener types C3 + Env.Listeners), 19 (ext, C2), 3 (Proxied, C4);
3→4; 4→5; 5→6; 6→7, 20; 7→16; 8→8, 29 (go.mod); 9→9, 32; 10→10, 21, 33; 11→11;
12→12; 13→13; 14→14, 17; 15→15, 22, 34; 16→23, 35; 17→36, 42; 18→24; 19→25, 26;
20→27; 21→18 (migration), 28 (store); 22→30; 23→31; 24→39; 25→37; 26→43, 45;
27→40; 28→41, 44; 29→38, 46; 30→47, 48; 31→49.
Fix every "(Task N)" in Contracts, "only Task 7 edits web/package.json" (→16),
"only Task 8 edits go.mod" (→29), "Tasks 14 and 15 touch test/harness" (→14, 15,
17, 22, 34; gate 7 applies, harness.yml dispatch), Assumptions' "Task 7 verifies"
(→ verified: vitest-axe 1.0.0-pre.5 works under vitest 5.0.5 once setup.ts imports
`vitest-axe/extend-expect`, 2026-10-23) and "Task 29 adds the compile-time
assertions" (→38).

## 2. Contract amendments the drafting groups made

- C7: manifest has no Summary field: id, `Version: 1`, `DefaultEnabled: true`, nothing else; the hujson blank import lives in `internal/modules/tailnet/deps.go` (Task 29).
- C8: add `CapabilityOK/CapabilityMissing/CapabilityUnverified`; Retry-After over 60 s stops retrying, unreadable falls back to 1/2/4 s; 15 s per attempt via context; redirects refused; bodies over 8 MiB refused; Auth or request-build failure is Definite and sends nothing; unreadable 2xx JSON → CloudUnavailable; 3xx → CloudUnavailable. Split: client Task 23, sealing and probes Task 35.
- C9: `listenerTimeFormat`; `setListenerState` refuses an empty `from` and matches it through `json_each`; tables STRICT.
- C10: revoked listener → 404 not_found; unreadable state → 503 unavailable; forwarded protocol not https, or missing/malformed client header (several values, a port, a zone) → 403 auth.forbidden.
- C11: Source is `"<Client> via <Transport.Addr()>"` (no port); the Origin check applies to every unsafe request through a runtime listener, login included.
- C13 and C19: replace with the full texts in the listeners group's final report (copied below).
- C14: the gate also covers the composite runner's step Undo and Release; Lookup, Retire and AckPrivate stay admitted. auth mutates users/tokens only inside actions (login's totp_last_step and last-seen times excepted), so gating actions is complete.
- C15: Wake does nothing while suspended; runRecoverer returns errRecovererSuspended leaving the change recovering and owned by module; CompositesRecovered reports true while suspended.
- C17: a Run that returns nil or panics before shutdown restarts like an error; panics logged by type unless runtime errors; suspension re-checked before every restart.
- C18: OpenAPIRoutes = Routes() + SurfaceWeb extension routes only; extension paths must lie under /api/v1/ and not end in /; scope and path validated before clashes; Stream routes share the built-in stream lifecycle (passive authentication through a stream-pattern mux, SSERecheck re-authentication, SSEHardLimit, CloseStreams, Proxied.Revoked, X-Accel-Buffering behind a runtime listener) plus the per-write 30 s deadline; no shared stream cap exists. Split: web Task 41, socket and OpenAPI Task 44.
- C20: `server.Options.Contributors []ext.Controller` (tests only; production nil); Task 38 leaves Deps.Listeners nil, Task 47 sets it; contributions are gathered in startListeners before api.New; services start after the socket serves; shutdown order services, socket, stream responses, runtime listeners, network listeners; topologyOf gains a runtime-addresses parameter.
- C21: `changeInProgress(change)`; `changeError` null for applying, awaiting_confirmation, undoing, recovering, releasing; `Step.action` is `{ Name, Version }`; candidates/allowed/earlierChildChangeIds may be null; listEffects/listSteps return unwrapped arrays; `repairChange(id, body = {})`; new Change fields `string | null`, override `unknown`; ModuleAvailability.reason `string | null`.
- C22: `HostModuleEntry`; enabledModules returns it; hostCreateExtensions keeps enabled-but-unavailable entries; host pages firewall, docker, tailscale, npm, panel; global pages tailnet, derp, cloudflare in a "Network" sidebar group; container actions firewall.allow, firewall.deny, npm.expose, cloudflare.expose. Split: Tasks 10, 21, 33.
- C23: the dialog plans through a query keyed `["plan", name, version, hostId, input, overrides]`, which connectEvents never refetches.
- New C25 (harness, Tasks 14, 17): `Lab`, `NewLab`, `VMName`, `NIC`/`MAC`, `InetIPv4/IPv6/Addrs`, `PrivPrefix`, `PrivAddr`, `Boot`; the `-vms` flag (default 3) applied in bootVMs; labs use index octets 1-254 and multicast ports from 40100.
- New C26 (pins, Tasks 15, 22, 34): `RequiredPackages` gains docker-compose-plugin after containerd.io (5.6.0); `RequiredContainers`, `Container{Name, Ref}`, `Pins.Containers`, `Pins.Container(name)`; `PullScript(cs ...Container)` appended to TargetScript; images pulled on the VM by digest (pebble and challtestsrv 2.10.1, digests in Task 22). Unverified: that docker pull accepts `name:tag@digest` (first exercised by wave 6a).
- Assumptions to add: Task 32 adds `cli.Options.SelectPaths`; selectPaths may get a nil command (update rollback); socket-command tests use `--dev` with temporary XDG directories (an early draft run read the live controller socket's user list — read only).

## 3. Then

Self-review, `scripts/plan-lint` to 0 errors, register rows 50-53, 92, 93, 95 set `planned` with this plan and acceptances, plan review rounds (cap 3: highest total 6), the Plan review header line, then dforge:handoff for execution.

## C13 replacement (listeners group)

C13 — Web certificate sources (Task 36, Task 42), `internal/controller/listen`. `sources.go` exactly as spec 1.12 (`CertSource`, `SourceDeps`, `certSources`, `extraNames`). The stub `tailscale_cert.go` has `tailscaleCertSource(d SourceDeps) CertSource` (always falls through) and `tailscaleNames(ctx, d) ([]string, error)` (returns none). `Leaf.Ensure` becomes `Ensure(ctx context.Context, ips []netip.Addr) (bool, error)`; it reissues when the IPs, the sources' extra names or the renewal age change, and adds the extra names to the CA leaf's DNS SANs (sorted, deduplicated, without the hostname or localhost); when the extra names cannot be read the current names are kept and the error logged. `Leaf.GetCertificate` asks `certSources` in order; a source error is logged and skipped; the CA leaf is the fallback. Unexported `(*Leaf).useSources(d SourceDeps)`. `(*Leaf).EnsureIssued(ctx) error` issues a leaf without IP SANs when none exists and never replaces one. `listen.Config.Sources *SourceDeps` (nil: none). `(*Service).TLSConfig() (*tls.Config, error)` calls EnsureIssued, then returns the one shared tls.Config.

## C19 replacement (listeners group)

Adds `const RevokeGrace = WriteTimeout`; RuntimeConfig.Log nil discards; Start binds every open row (including rows Open recorded before Start), never revoked or closed, retries a failed bind every CheckInterval, fails only when rows cannot be read, and a second Start fails; Open validates the spec (address with port and no zone, allowed peer without zone, one of the two headers, a public host and a change id, else request.invalid_input; stored unmapped), records it open in one transaction and binds once Start has run (a failed bind moves the row to closed and returns the error), only records before Start, fails after Shutdown; Revoke commits revoked first, closes the revoked channel, stops accepting, closes connections after their current response (cut after RevokeGrace), succeeds on revoked or closed, not_found on a missing id; Close moves revoked to closed, succeeds on closed, request.invalid_input on open, not_found on missing; Addrs are the accepting listeners' addresses, unmapped and sorted; Shutdown keeps row states. Connections from any peer but AllowedPeer are closed before they are read; every bind re-reads its row under the runtime's lock, so a concurrent revocation wins; isRevoked re-reads the row.
