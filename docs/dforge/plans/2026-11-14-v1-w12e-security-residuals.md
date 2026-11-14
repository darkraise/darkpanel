# v1 w12e security residuals Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the residuals the 2026-11-14 panel re-review left on security rows 1 and 6 (rows 142, 176-179): nginx is reloaded around every join of NPM, every NPM start Darkpanel makes is verified, the undo of a finished upgrade is checked, the guard protects the IPv6 leg on a link-local-only host, the Tailscale link guard sees an exposure's tailnet client, and a restored location is cut at `/`.

**Architecture:** Task 1 gathers the evidence the riskiest assumptions need (route lookups on this IPv4-only host, the pinned NPM image's reload path) and gates Tasks 4 and 6. Tasks 2-4 are the small guard and restore fixes (D6, D5, D4). Tasks 5-6 are D1: the NPM client learns `enabled` and `meta.nginx_online`, then attach and the undo of a detach reload nginx before and after the join. Tasks 7-9 are D3 and D2: the upgrade's late check and recorded target list, the undo of a finished upgrade's check, and the verification after every start. Task 10 brings the threat model and the registers.

**Tech Stack:** Go 1.27.4, Markdown.

**Spec:** docs/dforge/specs/2026-11-14-v1-w12e-security-residuals-design.md

**Execution:** inline — `claude --model opus --effort high` — the hardest self-implemented task totals 8 at risk 3

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w12e-tmp/integ && export TMPDIR=/root/.cache/w12e-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -timeout 20m -count=1 ./... && timeout 600 go test -race -timeout 10m -count=1 ./internal/core/lifecycle/ && timeout 1500 go test -race -timeout 24m -count=1 ./internal/modules/npm/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

## Global Constraints

- Scope is the rows this plan cites: security register (`docs/dforge/registers/2026-11-06-v1-security-review.md`) rows 1, 6, 142, 176, 177, 178, 179 and the new hidden-tailnet-client row Task 10 records; completion register (`docs/dforge/registers/2026-10-21-v1-completion.md`) row 37. Out of scope (spec, Out of scope): the accepted `forward host` DNS risk (D7), the hidden tailnet client's own flow, NPM's admin hardening, the confirm window, a harness change (D8).
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd; runs `systemctl`, `docker`, `ufw`, `iptables`, `nft` or `nmap`; binds an address other than `127.0.0.1` (port 0); or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process. Task 1's route capture is read-only (`ip -6 ...` and the agent's own `netstat.RouteGet`, which only asks the kernel for a route), and its image reading fetches the registry over HTTPS into the scratchpad with `curl`, never through Docker.
- Every `go`, `golangci-lint` and `make` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w12e-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first and remove it when the task ends. A `go test -race` of `./internal/modules/npm/` runs as `timeout 1500 go test -race -timeout 24m -count=1` (550-600 s on this host).
- Refusal codes and detail strings are exactly as Contracts gives them; a test asserts them.
- Every new deadline is a named constant in the file that uses it (values in Contracts).
- Comments follow the repository's rule: none that say what the code does; a comment only for a non-obvious why. Fix the stale comments the spec names (`network_claims.go` "can reach NPM only after it is re-pointed" and the networkUndoDisconnect doc, `instance_upgrade.go` above `forwardCheck`, `linkguard.go` "The context's Client is then the proxy's address").
- No new dependency; `go.mod` does not change.

## Contracts

- **Evidence note (Task 1, read by Tasks 4, 6 and 10).** `docs/dforge/notes/w12e-evidence.md` with two sections. `## Route capture (<date>)`: the verbatim output of `ip -6 addr`, `ip -6 route`, and one line per `netstat.RouteGet` lookup, `<dst> from <src|-> -> <route or error text>`, for the lookups of the spec's Riskiest assumption. `## NPM 2.16.0 image`: for each question of Task 1 Step 3, the answer, the file path inside the image, and the quoted lines.
- **NPM client (Task 5, consumed by Tasks 6 and 9).** In `internal/modules/npm/npmapi/types.go`: `ProxyHost` gains `Enabled Flag \`json:"enabled"\`` and `Meta ProxyHostMeta \`json:"meta"\``; `type Flag bool` decodes a JSON `true`/`false` or the numbers `1`/`0` and encodes as a boolean; `type ProxyHostMeta struct { NginxOnline *bool \`json:"nginx_online,omitempty"\`; NginxErr *string \`json:"nginx_err,omitempty"\` }`. `ProxyHostBody` does not change. In `internal/modules/npm/fake`: `func (s *Server) SetNginxOnline(online bool, errText string)` — from then on every proxy-host create and update answers `meta.nginx_online` = online and, when false, `meta.nginx_err` = errText (default true, no error); `func (s *Server) DisableProxyHost(id int)` — the host answers `enabled: false`; every other created or seeded host answers `enabled: true`.
- **Reload (Task 6, consumed by Task 9's docs only).** New file `internal/modules/npm/nginx_reload.go`: `func (n nm) reloadNginx(ctx context.Context, env core.Env) (reloaded bool, err error)`. It lists the proxy hosts, takes the enabled host with the lowest id (none: `false, nil` with no write), and sends `UpdateProxyHost(id, proxyHostEditBody(host))` unchanged. A success whose `meta.nginx_online` is not false is `true, nil`. Otherwise the error's text is the reason: `meta.nginx_err`, or `nginx did not come online` when NPM gave none, or the client's error when the API did not answer. Constant `nginxReloadWait = 10 * time.Second` bounds each call.
- **Attach and the undo of a detach (Task 6).** `npm.network.attach` declares `ApplyTimeout: 45 * time.Second`. The order is today's check, then `reloadNginx`, connect, the held-address check, `reloadNginx`. Refusals carry code `npm.reserved`:
  - first reload not confirmed, Definite, no connect: `nginx could not be reloaded before darkpanel-npm joins network <net>: <reason>`;
  - held address: `<where> forwards to <host>, which resolves to <addr>, the address darkpanel-npm took on network <net>; it was disconnected again` for a name, and `<where> forwards to <addr>, the address darkpanel-npm took on network <net>; it was disconnected again` for an address;
  - second reload not confirmed: `nginx could not be reloaded after darkpanel-npm joined network <net>: <reason>; it was disconnected again`.
  The disconnect runs under `context.WithoutCancel` with `nginxDisconnectWait = 10 * time.Second`. A refusal returned after a failed disconnect carries no Definite mark and no 4xx cause (wrap it with `fmt.Errorf("...: %w", errors.New(text))`, never `errcode.New`).
- **Docker fake (Task 6, consumed by Tasks 7-9).** In `internal/modules/npm/testhost_test.go`: `func (d *thDocker) pin(network string, addr netip.Addr)`. Every later endpoint on that network without a static IPv4 address, made by `ConnectNetwork` or by `CreateContainer`'s networking config, gets `IPAddress = addr`. `pin(network, netip.Addr{})` clears the pin.
- **Recorded targets (Task 7, consumed by Tasks 8 and 9).** In `internal/modules/npm/store.go`: `type recordedTarget struct { Where, Host string; Name bool; Addr netip.Addr }` with JSON names `where`, `host`, `name`, `addr`; `containerConfig` gains `Targets *[]recordedTarget \`json:"targets,omitempty"\`` (nil = a row written before w12e; a pointer to an empty slice = no target). `func recordTargets(ts []forwardTarget) *[]recordedTarget` and `func (r recordedTarget) target() forwardTarget`. `containerConfig`'s other fields keep their encoding, so old rows still load.
- **Upgrade checks (Task 7, consumed by Tasks 8 and 9).** In `internal/modules/npm/instance_upgrade.go`: `func (h instanceUpgradeHandler) forwardCheckConfig(ctx context.Context, env core.Env, cfg containerConfig, targets []forwardTarget, freeID string) error` checks `darkpanel-npm` (whole pool) and every network of `cfg.Networks` with its static IPv4 address, counting container `freeID`'s addresses as free. A network of `cfg.Networks` that no longer exists is a Definite `errcode.NotFound` with detail `network <net> of the original container no longer exists`. `forwardCheck(ctx, env, c)` becomes `forwardCheck(ctx, env, c) ([]forwardTarget, error)`: it lists the targets and calls `forwardCheckConfig(instanceUpgradeConfig(c), targets, c.ID)`.
- **Start verification (Task 7, consumed by Task 9).** `func (h instanceUpgradeHandler) verifyHeld(ctx context.Context, env core.Env, containerID string, targets []forwardTarget) (dropped []string, err error)`. It inspects the container. Literal-address targets are compared first, then the forwarded names, re-resolved with `n.forwardAnswers`. Every network whose held IPv4 or IPv6 address is a target is disconnected, and its name is returned in `dropped` in name order. `err` is a non-Definite error with code `npm.reserved` (`withoutDefiniteMark` over `errcode.New`) and detail `<where> forwards to <addr>, which darkpanel-npm took on network <net> when it started; it was disconnected from <net>`, the first hit's. A hit on `darkpanel-npm` is not disconnected; its detail ends `; it stays on darkpanel-npm` and the caller treats it as a failure. A hit whose disconnect fails stops the container, and `err` joins the disconnect error. An inspection or lookup error is returned as it is, with no `dropped`. It runs under `context.WithoutCancel`.
- **Row result (Task 9).** When `restore`, `resume` or `recoverOriginal` drops networks, the upgrade row's result (`setUpgradePhase`'s result argument) is `the started container held a forwarded address and left network <net>[, <net>...]`, and `fail`'s error names the same networks.

## Assumptions (evidence)

- This host is an IPv4-only Docker host: only link-local IPv6 addresses (eth0 `fe80::be24:11ff:fedb:56d4/64`, docker0 `fe80::10ed:bdff:fe67:f4d1/64`, Docker bridges and veths), no IPv6 default route; `ip -6 route get 2001:db8::1` answers `RTNETLINK answers: Network is unreachable` and `ip -6 route get fe80::1` answers `fe80::1 from :: dev eth0 proto kernel src fe80::be24:11ff:fedb:56d4` (`ip -6 addr`, `ip -6 route`, run 2026-11-14 while planning). The agent's own `netstat.RouteGet` answers, from link-local and `::1` sources, are unverified — Task 1 verifies them and gates Task 4.
- NPM's proxy-host update regenerates that host's configuration, tests and reloads nginx, and answers success with `meta.nginx_online: false` and `meta.nginx_err` when the test fails; a disabled host is skipped (Fable round 2, upstream source v2.12.3 and v2.16.0) — unverified in the pinned image — Task 1 verifies it and gates Tasks 5-6.
- `enabled` in NPM's proxy-host answer may be a boolean or 0/1 — unverified — Task 1 reads it; `npmapi.Flag` accepts both either way.
- nginx keeps the workers of an old configuration until their accepted requests end (nginx.org/en/docs/control.html, read 2026-11-14 by Fable): the owner accepted that residual (spec D1, U4).
- `notDefinite` rebuilds a Definite error from its text and drops its code (`internal/modules/npm/instance_upgrade.go:283-288`); `withoutDefiniteMark` keeps it (`internal/modules/npm/effects.go:81-94`).
- `run` records an effect skipped when `do` returns a Definite error or a 4xx cause (`internal/modules/npm/effects.go:64-67`, `:117-126`); the connect and its follow-up run inside one `do` (spec D1, R23).
- The test Engine assigns an address only when it is static (`testhost_test.go:528-530`, `:728-730`), so Task 6 adds `pin`.
- The upgrade and every proxy-host write take the `npm` key (`instance_upgrade.go:60`, `proxyhost_add.go:50`, `proxyhost_edit.go:34,46`), so the target list of the late check holds until the start (R27).

## Task index

1. Evidence: route capture and the pinned NPM image
2. D6: cut a restored location at its first `/`
3. D5: the Tailscale link guard sees an exposure's tailnet client
4. D4: the IPv6 leg of a `[::]` binding on a link-local-only host
5. NPM client: `enabled` and `meta.nginx_online`
6. D1: reload nginx before and after NPM joins a network
7. D3: the upgrade's late check, recorded targets and the verification after its start
8. D2: check the undo of a finished upgrade
9. D3: verify every start of the original
10. Threat model and registers

---

### Task 1: Evidence: route capture and the pinned NPM image

**Files:**
- Create: `docs/dforge/notes/w12e-evidence.md`
- Create (temporary, deleted in Step 4, never committed): `internal/platform/netstat/w12e_capture_test.go`

**Interfaces:**
- Produces: the evidence note (Contracts, Evidence note).

**Items:** 2026-11-06-v1-security-review#6, 2026-11-06-v1-security-review#176

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 0 = 3

- [ ] **Step 1: No new test, because the task records evidence and changes no code that ships; the capture file below is deleted before the commit**

- [ ] **Step 2: Capture the routes**

Run `ip -6 addr`, `ip -6 route`, `ip -6 route get 2001:db8::1`, `ip -6 route get fe80::1` and paste the output verbatim into the note. Write the temporary capture test with exactly this content:

```go
//go:build w12ecapture

package netstat

import (
	"net/netip"
	"testing"
)

func TestW12eCapture(t *testing.T) {
	ifaces, err := Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	var srcs []netip.Addr
	for _, i := range ifaces {
		for _, p := range i.Addrs {
			if p.Addr().Is6() && (p.Addr().IsLinkLocalUnicast() || p.Addr().IsLoopback()) {
				srcs = append(srcs, p.Addr())
			}
		}
	}
	dsts := []string{"2001:db8::1", "2606:4700::1111", "fe80::1"}
	for _, d := range dsts {
		dst := netip.MustParseAddr(d)
		r, err := RouteGet(RouteQuery{Dst: dst})
		t.Logf("%s from - -> %+v %v", dst, r, err)
		for _, s := range srcs {
			r, err := RouteGet(RouteQuery{Dst: dst, Src: s})
			t.Logf("%s from %s -> %+v %v", dst, s, r, err)
		}
	}
	if r, err := RouteGet(RouteQuery{Dst: netip.MustParseAddr("fe80::1")}); err == nil && r.PrefSrc.IsValid() {
		r2, err2 := RouteGet(RouteQuery{Dst: netip.MustParseAddr("fe80::1"), Src: r.PrefSrc})
		t.Logf("fe80::1 from preferred %s -> %+v %v", r.PrefSrc, r2, err2)
	}
}
```

Run `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/1 timeout 120 go test -tags w12ecapture -run TestW12eCapture -v ./internal/platform/netstat/` and paste each logged line into the note. Classify each global lookup's error with `errors.Is` against `unix.ENETUNREACH`, `unix.EHOSTUNREACH`, `unix.EACCES` (as `netstat/routes.go` does).

**Gate (D4):** if any global lookup (`2001:db8::1`, `2606:4700::1111`, with or without a source) answers anything other than one of those three errnos or `netstat.ErrNoRoute`, stop the plan before Task 4, record the line `Gate D4: <answer>` in the ledger, and return D4 to the owner.

- [ ] **Step 3: Read the pinned image without Docker**

In the scratchpad, in a new empty directory: get an anonymous pull token (`curl -s "https://auth.docker.io/token?service=registry.docker.io&scope=repository:jc21/nginx-proxy-manager:pull"`), fetch the index of digest `sha256:4393e642e233e5efd5adeb7f10918d4aaea71c9ed875b94190273fc8e87f9594` from `https://registry-1.docker.io/v2/jc21/nginx-proxy-manager/manifests/<digest>` with the index and manifest media types in `Accept`, take the `linux/amd64` manifest, and download its layers. Each `curl` gets `--max-time 300`. List each layer with `tar -tzf` and extract only the paths under `app/` (the backend) and `etc/nginx/`, newest layer last. Answer in the note, each with file path and quoted lines:

1. Which code runs when a proxy host is updated (`PUT /api/nginx/proxy-hosts/:id`): does it regenerate the host's configuration, run `nginx -t`, and reload? What does it answer when the test fails (`meta.nginx_online`, `meta.nginx_err`)? Is a disabled host skipped?
2. Is `enabled` answered as a boolean or as 0/1?
3. Does the custom-location template render a static `proxy_pass` with the forward host, and is the proxy host's own forward host also static (no `resolver`/variable)?
4. Does NPM reload nginx on a timer (`ip_ranges.js` or similar), and on certificate requests?
5. Does nginx fail to start (not only to reload) when a static `proxy_pass` name has no answer (`nginx -t` behaviour with `host not found in upstream`)? Spec F13: if yes, write it in the note; Task 7's late check already refuses a name with no answer only when it can resolve, so the note says what the threat model states.
6. Does the proxy configuration buffer a request body before it connects upstream (`proxy_request_buffering`)?

**Gate (D1):** if question 1 shows that an unchanged update does not test and reload nginx, or that a failed test is not distinguishable from success in the answer, stop before Task 5, record `Gate D1: <finding>` in the ledger, and return D1 to the owner.

- [ ] **Step 4: Remove the capture test and commit**

```bash
rm internal/platform/netstat/w12e_capture_test.go
git add docs/dforge/notes/w12e-evidence.md
git commit -m "docs(notes): record the w12e route and NPM evidence"
```

---

### Task 2: D6: cut a restored location at its first `/`

**Files:**
- Modify: `internal/modules/npm/proxyhost_edit.go` (`checkRestoredLocation`)
- Test: `internal/modules/npm/proxyhost_locations_test.go`

**Interfaces:**
- Consumes: none.
- Produces: none.

**Items:** 2026-11-06-v1-security-review#179

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6

- [ ] **Step 1: Extend the test `internal/modules/npm/proxyhost_locations_test.go::TestW11c01UndoKeepsLocationsItCannotCheck`**

Add these rows to the table, after `{"a reserved upstream", "127.0.0.1", 81, errcode.NPMReserved},`:

```go
		{"a reserved upstream followed by a path", "127.0.0.1/x", 81, errcode.NPMReserved},
		{"a reserved upstream followed by an nginx variable", "127.0.0.1/$request_uri", 81, errcode.NPMReserved},
		{"a path alone", "/x", 9000, ""},
```

Rename the test function to `TestW12eRestoredLocationCutAtSlash` and keep its doc comment's first line naming both behaviours ("an undo keeps locations it cannot check and checks the host before the first /").

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/2 timeout 300 go test -count=1 -run TestW12eRestoredLocationCutAtSlash ./internal/modules/npm/`
Expected: FAIL in the two reserved-upstream rows (the undo restores them).

- [ ] **Step 3: Implement**

1. In `checkRestoredLocation`, cut `l.ForwardHost` at its first `/` (as `forwardTargets` does at `network_claims.go:854-862`, `strings.Cut`) before the empty and `$` tests, and check the part before the cut.
2. An empty host after the cut, or one holding `$`, is written back unchecked as today.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/2 timeout 300 go test -count=1 -run 'TestW12eRestoredLocationCutAtSlash|TestW11c01' ./internal/modules/npm/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/proxyhost_edit.go internal/modules/npm/proxyhost_locations_test.go
git commit -m "fix(npm): check a restored location's host before /"
```

---

### Task 3: D5: the Tailscale link guard sees an exposure's tailnet client

**Files:**
- Modify: `internal/modules/tailscale/linkguard.go` (the `case g.Via != nil:` branch and its comment)
- Test: `internal/modules/tailscale/linkguard_test.go`

**Interfaces:**
- Consumes: none.
- Produces: none.

**Items:** 2026-11-06-v1-security-review#178

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

Append to `internal/modules/tailscale/linkguard_test.go`:

```go
// TestW12eTailnetClientOfAnExposure: the client of an NPM exposure gets a flow to each public binding, so a change that
// takes the Tailscale link down is refused for a tailnet client; a host-address client and a tunnel's client add none.
func TestW12eTailnetClientOfAnExposure(t *testing.T) {
	via := func(kind string) *core.ProxyPath {
		return &core.ProxyPath{ExposureID: "e1", Kind: kind, ProxyAddr: netip.MustParseAddr("172.30.0.2"),
			ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"), Public: lgAddrs("0.0.0.0:443")}
	}
	guarded := func(client, kind string) core.Guarded {
		return core.Guarded{Guard: &core.GuardContext{Complete: true,
			Client: &core.Peer{Addr: netip.MustParseAddr(client), Port: 51000}, Via: via(kind)}}
	}
	for _, tc := range []struct {
		name   string
		routes map[string]string
		in     core.Guarded
		wantOK bool
		wantIn []string
	}{
		{"a tailnet client of an NPM exposure fails a logout", map[string]string{"100.64.0.9": "tailscale0", "172.30.0.2": "dp-panel"},
			guarded("100.64.0.9", "npm"), false, []string{"panel", "100.64.0.9"}},
		{"a host-address client of an NPM exposure adds no flow", map[string]string{"172.30.0.2": "dp-panel"},
			guarded("172.30.0.1", "npm"), true, nil},
		{"a tailnet client of a tunnel exposure adds no flow", map[string]string{"100.64.0.9": "tailscale0", "172.30.0.2": "dp-panel"},
			guarded("100.64.0.9", "tunnel"), true, nil},
		{"a client whose route leaves eth0 passes", map[string]string{"100.64.0.9": "eth0", "172.30.0.2": "dp-panel"},
			guarded("100.64.0.9", "npm"), true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, tsv := lgHost(t, lgWorld{controller: true, routes: tc.routes})
			g, err := tsv.linkGuard(context.Background(), h.env(""), tc.in, lgPrefs(nil), lgLogout)
			if err != nil {
				t.Fatal(err)
			}
			if g.OK != tc.wantOK {
				t.Fatalf("guard OK %v (%s), want %v", g.OK, g.Message, tc.wantOK)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(g.Message, want) {
					t.Errorf("message %q does not name %q", g.Message, want)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/3 timeout 300 go test -count=1 -run 'TestW12eTailnetClientOfAnExposure' ./internal/modules/tailscale/`
Expected: FAIL in "a tailnet client of an NPM exposure fails a logout" (the guard passes).

- [ ] **Step 3: Implement**

1. In the `case g.Via != nil:` branch, keep the proxy-to-listener flow and, when `g.Via.Kind == "npm"` and `g.Client` holds a valid address, add for each `g.Via.Public` binding the client's flow the way the `g.Client != nil` case does for `PanelListen` (`linkListenFor(client, binding.Addr(), f.host)`, label `panel`, `inbound: true`, `request: isRequest`). `add` already drops a client that is a host address (the hidden client).
2. Replace the comment "The context's Client is then the proxy's address, so it adds no flow of its own." with one that states why only an NPM exposure's client gets a flow (a tunnel's client never reaches this host directly).

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/3 timeout 300 go test -count=1 ./internal/modules/tailscale/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale/linkguard.go internal/modules/tailscale/linkguard_test.go
git commit -m "fix(tailscale): guard an NPM exposure's tailnet client"
```

---

### Task 4: D4: the IPv6 leg of a `[::]` binding on a link-local-only host

**Files:**
- Modify: `internal/modules/firewall/guard/flows.go` (`npmPublicFlows`)
- Test: `internal/modules/firewall/guard/w12e_linklocal_test.go` (create)

**Interfaces:**
- Consumes: the evidence note's route capture (Task 1).
- Produces: none.

**Items:** 2026-11-06-v1-security-review#6

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7

- [ ] **Step 1: Write the failing test**

Before writing it, compare the addresses below with the note's `## Route capture`; where the captured eth0 or docker0 link-local address differs, use the captured one. The fake `v1r21Fib` answers a global lookup with no matching route `netstat.ErrNoRoute`, which stands for the errnos the gate accepted.

```go
package guard

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	w12eEth0LL    = "fe80::be24:11ff:fedb:56d4"
	w12eDocker0LL = "fe80::10ed:bdff:fe67:f4d1"
	w12eDrop6443  = "-A INPUT -p tcp -m tcp --dport 443 -j DROP"
	w12eDropAll6  = "-A INPUT -j DROP"
)

// w12eLinkLocalHost is ckIfaces as captured on an IPv4-only Docker host: eth0 and the panel bridge hold only
// link-local IPv6 addresses, and no IPv6 route leaves the host unless defaultRoute adds one learned from router
// advertisements.
func w12eLinkLocalHost(defaultRoute bool) v1r21Fib {
	ifaces := ckIfaces()
	for i := range ifaces {
		switch ifaces[i].Name {
		case "eth0":
			ifaces[i].Addrs = []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix(w12eEth0LL + "/64")}
		case "dp-panel":
			ifaces[i].Addrs = append(ifaces[i].Addrs, netip.MustParsePrefix(w12eDocker0LL+"/64"))
		}
	}
	table := append(v1r21HostRoutes(),
		v1r21Route("unicast", "fe80::/64", "eth0", w12eEth0LL, 254),
		v1r21Route("unicast", "fe80::/64", "dp-panel", w12eDocker0LL, 254),
		v1r21Route("local", "::1/128", "lo", "::1", 255),
		v1r21Route("local", w12eEth0LL+"/128", "eth0", w12eEth0LL, 255),
		v1r21Route("local", w12eDocker0LL+"/128", "dp-panel", w12eDocker0LL, 255),
	)
	if defaultRoute {
		table = append(table, v1r21Route("unicast", "::/0", "eth0", w12eEth0LL, 254))
	}
	return v1r21Fib{&gHost{ifaces: ifaces, table: table}}
}

func TestW12eLinkLocalOnlyHostGuardsIPv6443(t *testing.T) {
	for _, tc := range []struct {
		name         string
		defaultRoute bool
		client       string
		v4, v6       []string
		want         string // "" when the change must pass
	}{
		{"a hidden client and a drop of IPv6 443", false, "172.30.0.1", nil, []string{w12eDrop6443}, "exposure exp1"},
		{"a hidden client and an IPv6 INPUT that drops everything", false, "172.30.0.1", nil, []string{w12eDropAll6}, "exposure exp1"},
		{"a hidden client and a change that touches no IPv6 rule", false, "172.30.0.1", []string{w12bOtherPort}, nil, ""},
		{"a visible client and a drop of IPv6 443", false, "10.0.0.50", nil, []string{w12eDrop6443}, ""},
		{"a visible client and a drop of IPv6 443 with a default route", true, "10.0.0.50", nil, []string{w12eDrop6443}, "exposure exp1"},
		{"a visible client and an IPv6 INPUT that drops everything with a default route", true, "10.0.0.50", nil, []string{w12eDropAll6}, "exposure exp1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := w12eLinkLocalHost(tc.defaultRoute)
			before := v1r21State(t, w11b09Nat+v1r21Table(), v1r21Table())
			st := v1r21State(t, w11b09Nat+v1r21Table(tc.v4...), v1r21Table(tc.v6...))
			g := w12bCheck(t, h, &before, st, "[::]:443", tc.client, "")
			switch {
			case tc.want == "" && !g.OK:
				t.Errorf("guard = %+v, want OK", g)
			case tc.want != "" && (g.OK || !strings.Contains(g.Message, tc.want)):
				t.Errorf("guard = %+v, want a refusal containing %q", g, tc.want)
			}
		})
	}
}

// TestW12eLinkLocalAddressesAddNoProbes: on a dual-stack host, link-local addresses on docker0 and two veths leave
// the number of route lookups of an unrelated change as it is without them.
func TestW12eLinkLocalAddressesAddNoProbes(t *testing.T) {
	lookups := func(extra bool) int {
		ifaces := ckIfaces()
		if extra {
			for i, name := range []string{"docker0", "veth1", "veth2"} {
				ifaces = append(ifaces, netstat.Interface{Index: 20 + i, Name: name, Up: true, Running: true,
					Addrs: []netip.Prefix{netip.MustParsePrefix("fe80::" + string(rune('a'+i)) + "/64")}})
			}
		}
		h := v1r21Fib{&gHost{ifaces: ifaces, table: w12bRoutes()}}
		before := v1r21State(t, w11b09Nat+v1r21Table(), v1r21Table())
		st := v1r21State(t, w11b09Nat+v1r21Table(w12bOtherPort), v1r21Table())
		if g := w12bCheck(t, h, &before, st, "[::]:443", "10.0.0.50", ""); !g.OK {
			t.Fatalf("guard = %+v, want OK", g)
		}
		return len(h.lookups)
	}
	if without, with := lookups(false), lookups(true); with != without {
		t.Errorf("%d route lookups with link-local veths, %d without; want the same", with, without)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/4 timeout 300 go test -count=1 -run 'TestW12eLinkLocal' ./internal/modules/firewall/guard/`
Expected: FAIL in the two hidden-client refusal rows and the two default-route rows (no IPv6 leg is built).

- [ ] **Step 3: Implement**

1. In `npmPublicFlows`, drop `!a.IsLinkLocalUnicast()` from the loop that decides whether the `[::]` binding gets its IPv6 leg; keep `!a.IsLoopback()`. Nothing else changes: `wildcardDsts` (`route.go`) is not touched.
2. Rewrite the comment above that loop so it says why a link-local address is enough (the leg is a stand-in: a cell whose reply has no route is skipped).

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/4 timeout 600 go test -count=1 ./internal/modules/firewall/...`
Expected: PASS, including every existing guard test (A8: a host with a global IPv6 address and a host with none).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/firewall/guard/flows.go internal/modules/firewall/guard/w12e_linklocal_test.go
git commit -m "fix(firewall): guard the IPv6 leg on link-local hosts"
```

---

### Task 5: NPM client: `enabled` and `meta.nginx_online`

**Files:**
- Modify: `internal/modules/npm/npmapi/types.go`
- Modify: `internal/modules/npm/fake/server.go`, `internal/modules/npm/fake/objects.go`
- Test: `internal/modules/npm/fake/objects_test.go`

**Interfaces:**
- Consumes: the evidence note's answers 1 and 2 (Task 1).
- Produces: Contracts, NPM client.

**Items:** 2026-11-06-v1-security-review#176

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 1 = 6

- [ ] **Step 1: Write the failing test**

Append to `internal/modules/npm/fake/objects_test.go` (add `encoding/json` to its imports):

```go
func TestW12eNginxOnlineAndEnabled(t *testing.T) {
	s := New(t)
	c, _ := fkLogin(t, s)
	ctx := t.Context()
	a, err := c.CreateProxyHost(ctx, fkHost("a.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if !bool(a.Enabled) || a.Meta.NginxOnline == nil || !*a.Meta.NginxOnline {
		t.Errorf("created %+v, want enabled and nginx online", a)
	}
	s.SetNginxOnline(false, `host not found in upstream "up.invalid"`)
	b, err := c.UpdateProxyHost(ctx, a.ID, fkHost("a.example.com"))
	if err != nil {
		t.Fatalf("an update whose nginx test fails is still a success: %v", err)
	}
	if b.Meta.NginxOnline == nil || *b.Meta.NginxOnline || b.Meta.NginxErr == nil || *b.Meta.NginxErr != `host not found in upstream "up.invalid"` {
		t.Errorf("updated %+v, want nginx offline with the reason", b.Meta)
	}
	s.DisableProxyHost(a.ID)
	hosts, err := c.ProxyHosts(ctx)
	if err != nil || len(hosts) != 1 || bool(hosts[0].Enabled) {
		t.Errorf("hosts %+v, error %v; want the one host disabled", hosts, err)
	}
	for _, tc := range []struct {
		raw  string
		want bool
	}{{`{"enabled":true}`, true}, {`{"enabled":false}`, false}, {`{"enabled":1}`, true}, {`{"enabled":0}`, false}} {
		var h npmapi.ProxyHost
		if err := json.Unmarshal([]byte(tc.raw), &h); err != nil || bool(h.Enabled) != tc.want {
			t.Errorf("%s decodes to %v (error %v), want %v", tc.raw, h.Enabled, err, tc.want)
		}
	}
	if raw, err := json.Marshal(npmapi.ProxyHost{Enabled: true}); err != nil || !strings.Contains(string(raw), `"enabled":true`) {
		t.Errorf("encoded %s (error %v), want enabled as a boolean", raw, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/5 timeout 300 go test -count=1 -run TestW12eNginxOnlineAndEnabled ./internal/modules/npm/fake/`
Expected: FAIL to compile (`Enabled`, `Meta`, `SetNginxOnline`, `DisableProxyHost` undefined).

- [ ] **Step 3: Implement**

1. In `npmapi/types.go`, add `Flag` with `UnmarshalJSON` accepting `true`, `false`, `1`, `0` (anything else is an error) and `MarshalJSON` writing a boolean; add `ProxyHostMeta` and the two `ProxyHost` fields exactly as Contracts names them.
2. In the fake, keep a server-wide `nginxOnline` (default true) and `nginxErr`; every proxy-host create and update answer carries `meta` from them; `SetNginxOnline` changes them under the server's lock. Store an enabled flag per host, true on create and seed, false after `DisableProxyHost`, answered in every proxy-host read.
3. Nothing else in `npmapi` changes; `ProxyHostBody` stays as it is, so a body never sends `enabled` or `meta`.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/5 timeout 600 go test -count=1 ./internal/modules/npm/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/npmapi/types.go internal/modules/npm/fake/
git commit -m "feat(npm): read enabled and nginx_online from NPM"
```

---

### Task 6: D1: reload nginx before and after NPM joins a network

**Files:**
- Create: `internal/modules/npm/nginx_reload.go`
- Modify: `internal/modules/npm/network_claims.go` (`networkAttach`, `networkAttachHandler.Apply`, `networkUndoDisconnect`, the comments the Global Constraints name)
- Modify: `internal/modules/npm/testhost_test.go` (`pin`)
- Test: `internal/modules/npm/w12e_attach_test.go` (create)

**Interfaces:**
- Consumes: Contracts, NPM client (Task 5).
- Produces: Contracts, Reload; Attach and the undo of a detach; Docker fake.

**Items:** 2026-11-06-v1-security-review#1, 2026-11-06-v1-security-review#176

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8

- [ ] **Step 1: Write the failing test**

```go
package npm

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12eConnectHook calls at before each ConnectNetwork reaches the test Engine.
type w12eConnectHook struct {
	*thDocker
	at func()
}

func (d w12eConnectHook) ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	d.at()
	return d.thDocker.ConnectNetwork(ctx, network, container, ep)
}

// w12eReloads counts the proxy-host updates NPM received: the module reloads nginx through them.
func w12eReloads(h *thHost) int {
	n := 0
	for _, r := range h.npm.Requests() {
		if r.Method == http.MethodPut && strings.HasPrefix(r.Path, "/api/nginx/proxy-hosts/") {
			n++
		}
	}
	return n
}

func w12eNoAnswer(_ context.Context, host string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func w12eAnswer(addr string) func(context.Context, string) ([]netip.Addr, error) {
	return func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{netip.MustParseAddr(addr)}, nil }
}

func w12eNoChange(t *testing.T, h *thHost, user dockerapi.Network) {
	t.Helper()
	if nwEndpoint(t, h, user.ID) != nil || len(nwClaims(t, h, nwNet)) != 0 {
		t.Error("a refused attach left NPM on the network or recorded a claim")
	}
	for _, e := range h.journal.effects() {
		if e.State == core.EffectMade {
			t.Errorf("effect %s was recorded made", e.Resource)
		}
	}
}

// TestW12eAttachReloadsBeforeJoin: nginx still forwards a name that lost its answer to the address it held; the
// reload before the join fails and nothing connects, for a static and for an Engine-chosen address.
func TestW12eAttachReloadsBeforeJoin(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   AttachInput
	}{
		{"static", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web", Address: netip.MustParseAddr("10.9.0.7")}},
		{"Engine-chosen", AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			h.docker.pin(nwNet, netip.MustParseAddr("10.9.0.7"))
			w12aSeed(h, "up.invalid", 81)
			h.npm.SetNginxOnline(false, `host not found in upstream "up.invalid"`)
			n := h.nm()
			n.d.resolve = w12eNoAnswer
			h.addChange("c1", "npm.network.attach")
			_, err := n.networkAttach().Handler.Apply(context.Background(), h.env("c1"), nwJSON(t, tc.in))
			coded := nwRefused(t, err, errcode.NPMReserved)
			if want := `nginx could not be reloaded before darkpanel-npm joins network appnet: host not found in upstream "up.invalid"`; coded.Detail != want {
				t.Errorf("detail %q, want %q", coded.Detail, want)
			}
			if nwCalls(h, "ConnectNetwork") != 0 {
				t.Error("the refused attach connected NPM")
			}
			w12eNoChange(t, h, user)
		})
	}
}

// TestW12eAttachReloadsAfterJoin: with the name answering another address the attach succeeds, with one reload before
// the connect and one after; with no enabled proxy host it reloads nothing; the undo of a detach does the same.
func TestW12eAttachReloadsAfterJoin(t *testing.T) {
	ctx := context.Background()
	t.Run("attach", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		atConnect := -1
		n.d.docker = w12eConnectHook{h.docker, func() { atConnect = w12eReloads(h) }}
		h.addChange("c1", "npm.network.attach")
		in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web", Address: netip.MustParseAddr("10.9.0.7")}
		if _, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in)); err != nil {
			t.Fatal(err)
		}
		if atConnect != 1 || w12eReloads(h) != 2 {
			t.Errorf("%d reloads before the connect and %d in all, want 1 and 2", atConnect, w12eReloads(h))
		}
		if nwEndpoint(t, h, user.ID) == nil {
			t.Error("NPM is not on the network")
		}
	})
	t.Run("no enabled proxy host", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		h.npm.DisableProxyHost(1)
		n := h.nm()
		n.d.resolve = w12eNoAnswer
		h.addChange("c1", "npm.network.attach")
		in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
		if _, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in)); err != nil {
			t.Fatal(err)
		}
		if w12eReloads(h) != 0 {
			t.Errorf("%d reloads, want none", w12eReloads(h))
		}
	})
	t.Run("undo of a detach", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		nwSeedAttached(t, h, user, nwClaimant, false)
		if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
			t.Fatal(err)
		}
		var endpoint core.Effect
		for _, e := range h.journal.effects() {
			if e.Kind == core.EffectModify {
				endpoint = e
			}
		}
		w12aSeed(h, "up.invalid", 81)
		h.docker.pin(nwNet, netip.MustParseAddr("10.9.0.7"))
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		atConnect := -1
		n.d.docker = w12eConnectHook{h.docker, func() { atConnect = w12eReloads(h) }}
		if _, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatal(err)
		}
		if atConnect != 1 || w12eReloads(h) != 2 {
			t.Errorf("%d reloads before the connect and %d in all, want 1 and 2", atConnect, w12eReloads(h))
		}
		// A repeat finds NPM on the network: it checks the held address and reloads once before it answers done.
		if _, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint); err != nil {
			t.Fatal(err)
		}
		if w12eReloads(h) != 3 {
			t.Errorf("%d reloads after the repeat, want 3", w12eReloads(h))
		}
		n.d.resolve = w12eAnswer("10.9.0.7")
		_, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
		nwRefused(t, err, errcode.NPMReserved)
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("a repeat whose held address is forwarded left NPM on the network")
		}
	})
}

// TestW12eAttachUnconfirmedReloadDisconnects: a second reload that fails, or a held address that a name now answers,
// disconnects NPM again; a disconnect that fails leaves the effect intended.
func TestW12eAttachUnconfirmedReloadDisconnects(t *testing.T) {
	ctx := context.Background()
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web", Address: netip.MustParseAddr("10.9.0.7")}
	t.Run("the second reload fails", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		n.d.docker = w12eConnectHook{h.docker, func() { h.npm.SetNginxOnline(false, "boom") }}
		h.addChange("c1", "npm.network.attach")
		_, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in))
		coded := nwRefused(t, err, errcode.NPMReserved)
		if want := "nginx could not be reloaded after darkpanel-npm joined network appnet: boom; it was disconnected again"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
		w12eNoChange(t, h, user)
	})
	t.Run("the held address is forwarded", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		n.d.docker = w12eConnectHook{h.docker, func() { n.d.resolve = w12eAnswer("10.9.0.7") }}
		h.addChange("c1", "npm.network.attach")
		_, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in))
		coded := nwRefused(t, err, errcode.NPMReserved)
		if want := "proxy host 1 forwards to up.invalid, which resolves to 10.9.0.7, the address darkpanel-npm took on network appnet; it was disconnected again"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
		w12eNoChange(t, h, user)
	})
	t.Run("the disconnect fails", func(t *testing.T) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		w12aSeed(h, "up.invalid", 81)
		h.docker.Fail("DisconnectNetwork", errors.New("engine down"))
		n := h.nm()
		n.d.resolve = w12eAnswer("203.0.113.9")
		n.d.docker = w12eConnectHook{h.docker, func() { h.npm.SetNginxOnline(false, "boom") }}
		h.addChange("c1", "npm.network.attach")
		_, err := n.networkAttach().Handler.Apply(ctx, h.env("c1"), nwJSON(t, in))
		if err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want a failure that is not definite", err)
		}
		intended := false
		for _, e := range h.journal.effects() {
			intended = intended || e.State == core.EffectIntended
		}
		if !intended || nwEndpoint(t, h, user.ID) == nil {
			t.Error("a connect that could not be taken back was not left intended")
		}
	})
}
```

Also add `pin` to `testhost_test.go` (Contracts, Docker fake): a `pinned map[string]netip.Addr` initialised in `newTHDocker`; in `ConnectNetwork` and in `CreateContainer`'s endpoint loop, an endpoint whose `IPAMConfig` holds no valid IPv4 address gets `e.IPAddress = d.pinned[n.Name]` when one is pinned. The test file needs `nwSeedAttached` (already in `network_claims_test.go`).

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/6 timeout 600 go test -count=1 -run 'TestW12eAttach' ./internal/modules/npm/`
Expected: FAIL (no reload is made; the before-join case connects).

- [ ] **Step 3: Implement**

1. `nginx_reload.go`: `reloadNginx` as Contracts states, each NPM call under `context.WithTimeout(ctx, nginxReloadWait)`; enabled is `bool(host.Enabled)`.
2. `networkAttachHandler.Apply`: after `h.check` and before `h.n.run` of the connect, when `ck.connect` is set, call `reloadNginx`; an error or `meta.nginx_online` false is the first refusal (Definite). Inside the connect's `do`, after `ConnectNetwork`: inspect NPM (`networkEndpointOf`), compare its address on the network with `forwardTargets` (re-listed), then call `reloadNginx` again; on a hit or an unconfirmed reload disconnect (`DisconnectNetwork`, "not connected" counts as done, under `context.WithoutCancel` and `nginxDisconnectWait`) and return the Definite `npm.reserved` refusal; when the disconnect fails, return the refusal's text joined with the disconnect error without the Definite mark and without an errcode cause (Contracts).
3. `networkAttach()`: `ApplyTimeout: 45 * time.Second`.
4. `networkUndoDisconnect`: the same sequence around its `ConnectNetwork`; the already-on-the-network answer (`on == true`, both places) first runs the held-address check and one `reloadNginx`, refusing and disconnecting as above.
5. Fix the comments the Global Constraints name: `forwardTargets` (a name with no answer still adds nothing here, because the reload around the join flushes what nginx holds) and `networkUndoDisconnect`'s doc.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/6 timeout 900 go test -count=1 ./internal/modules/npm/...`
Expected: PASS, including `TestW12aAttachResolvesForwardHostNames` and `TestW12aUndoOfADetachRefusesAForwardedAddress` unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/nginx_reload.go internal/modules/npm/network_claims.go internal/modules/npm/testhost_test.go internal/modules/npm/w12e_attach_test.go
git commit -m "fix(npm): reload nginx around every join of NPM"
```

---

### Task 7: D3: the upgrade's late check, recorded targets and the verification after its start

**Files:**
- Modify: `internal/modules/npm/instance_upgrade.go` (`forwardCheck`, `Apply`, `steps`, `fail`, the comment above `forwardCheck`)
- Modify: `internal/modules/npm/store.go` (`containerConfig`, `recordedTarget`)
- Test: `internal/modules/npm/w12e_upgrade_test.go` (create)

**Interfaces:**
- Consumes: Contracts, Docker fake (`pin`, Task 6).
- Produces: Contracts, Recorded targets; Upgrade checks; Start verification.

**Items:** 2026-11-06-v1-security-review#142

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8

- [ ] **Step 1: Write the failing test**

```go
package npm

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12eUpSetup is w12aUpSetup with NPM's address on app-net chosen by the Engine, and the container db holding
// 172.30.0.20 there, which a proxy host forwards to.
func w12eUpSetup(t *testing.T) *upSetup {
	t.Helper()
	s := w12aUpSetup(t)
	c, ok := s.h.docker.container(s.inst.ContainerID)
	if !ok {
		t.Fatal("the NPM container is missing")
	}
	c.NetworkSettings.Networks[upAppNet].IPAMConfig = nil
	s.h.docker.putContainer(c)
	s.orig = c
	s.h.docker.putContainer(thContainer("db", thEndpoint(upAppNet, "172.30.0.20")))
	w12aSeed(s.h, "172.30.0.20", 5432)
	return s
}

// w12eFreeAt removes db when the upgrade makes call, once, and pins its address for the next endpoints on app-net. For
// PullImage the pinned image is reported missing until the pull starts, so the upgrade pulls it.
func w12eFreeAt(t *testing.T, s *upSetup, call string) {
	t.Helper()
	if call == "PullImage" {
		s.dock.missing[Image] = true
	}
	done := false
	s.dock.trace = func(c string) {
		if c != call || done {
			return
		}
		done = true
		s.dock.missing[Image] = false
		if err := s.h.docker.RemoveContainer(context.Background(), "db", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Error(err)
		}
		s.h.docker.pin(upAppNet, netip.MustParseAddr("172.30.0.20"))
	}
}

// TestW12eUpgradeLateForwardCheck: a forwarded address freed during the pull refuses the upgrade at the late check,
// before anything is journaled or stopped.
func TestW12eUpgradeLateForwardCheck(t *testing.T) {
	s := w12eUpSetup(t)
	w12eFreeAt(t, s, "PullImage")
	_, err := s.apply()
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "proxy host 1 forwards to 172.30.0.20, an address darkpanel-npm could take on network app-net"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	if len(s.effects()) != 0 || s.phase() != "no row" || s.dock.counts["StopContainer"] != 0 {
		t.Error("the refused upgrade journaled an effect, recorded a phase or stopped NPM")
	}
}

// TestW12eStartVerifiesHeldAddresses: a forwarded address freed after the stop is taken by the new container at its
// start; the container leaves that network at once, the upgrade fails with npm.reserved and rolls back.
func TestW12eStartVerifiesHeldAddresses(t *testing.T) {
	t.Run("upgrade", func(t *testing.T) {
		s := w12eUpSetup(t)
		w12eFreeAt(t, s, "StopContainer")
		_, err := s.apply()
		if code := errcode.From(err).Code; code != errcode.NPMReserved {
			t.Fatalf("error %v with code %s, want %s", err, code, errcode.NPMReserved)
		}
		if !strings.Contains(err.Error(), "172.30.0.20") || !strings.Contains(err.Error(), "app-net") {
			t.Errorf("error %v does not name the address and the network", err)
		}
		if nwCalls(s.h, "DisconnectNetwork") == 0 {
			t.Error("the new container was not disconnected from app-net")
		}
		if got := s.phase(); got != "rolled_back" {
			t.Errorf("phase %q, want rolled_back", got)
		}
		u := s.upgrade()
		if u.Config.Targets == nil || len(*u.Config.Targets) == 0 {
			t.Errorf("the row recorded targets %v, want the late check's list", u.Config.Targets)
		}
	})
	t.Run("an upgrade whose new container holds no target", func(t *testing.T) {
		s := w12eUpSetup(t)
		if _, err := s.apply(); err != nil {
			t.Fatal(err)
		}
		if got := s.phase(); got != "done" || nwCalls(s.h, "DisconnectNetwork") != 0 {
			t.Errorf("phase %q after %d disconnects, want done and none", got, nwCalls(s.h, "DisconnectNetwork"))
		}
		if u := s.upgrade(); u.Config.Targets == nil {
			t.Error("the row recorded no target list; want an empty or full one, not none")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/7 timeout 600 go test -count=1 -run 'TestW12eUpgradeLateForwardCheck|TestW12eStartVerifiesHeldAddresses' ./internal/modules/npm/`
Expected: FAIL to compile (`Config.Targets` undefined), then, once it compiles, in both tests.

- [ ] **Step 3: Implement**

1. `store.go`: `recordedTarget`, `recordTargets`, `target()` and `containerConfig.Targets` as Contracts states.
2. `instance_upgrade.go`: split `forwardCheck` into `forwardCheckConfig` (Contracts) and the listing; `forwardCheck` returns the targets.
3. `Apply`: keep the early check; after `h.pull` and before `env.Effects.Intend`, run `forwardCheck` again (the late check) and return its Definite refusal as it is; put `recordTargets(targets)` of the late check into `config.Targets` before `insertUpgrade`.
4. `steps`: right after `StartContainer` of the new container and before `phase("waiting")`, run `verifyHeld(id, targets)` (Contracts, Start verification) as its own step `verify`; a hit or an error fails that step.
5. `fail`: keep the step error's code: replace `notDefinite(cause)` with `withoutDefiniteMark(cause)` where the cause carries an errcode, so `errcode.From` finds `npm.reserved` through `fail`'s `%w`; the `wait` branch is unchanged.
6. Rewrite the comment above `forwardCheck`: the late check narrows the window, the verification after the start closes it.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/7 timeout 900 go test -count=1 ./internal/modules/npm/...`
Expected: PASS, including every `TestW12aUpgrade*` and `TestUp*` test.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/instance_upgrade.go internal/modules/npm/store.go internal/modules/npm/w12e_upgrade_test.go
git commit -m "fix(npm): verify the upgraded NPM's addresses at start"
```

---

### Task 8: D2: check the undo of a finished upgrade

**Files:**
- Modify: `internal/modules/npm/upgrade_recover.go` (`Inverse`)
- Test: `internal/modules/npm/w12e_upgrade_test.go`

**Interfaces:**
- Consumes: Contracts, Recorded targets and Upgrade checks (Task 7).
- Produces: none.

**Items:** 2026-11-06-v1-security-review#177

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7

- [ ] **Step 1: Write the failing test**

Append to `internal/modules/npm/w12e_upgrade_test.go` (add `errors`, `fmt`, `github.com/darkraise/darkpanel/internal/core` and `github.com/darkraise/darkpanel/internal/modules/npm/npmapi` to its imports):

```go
// w12eDone records change urChange as a finished upgrade whose original container joined networks with the targets,
// and puts app-net with its subnet on the Engine.
func w12eDone(t *testing.T, f *urFixture, networks map[string]*dockerapi.EndpointSettings, targets *[]recordedTarget) {
	t.Helper()
	ctx := context.Background()
	f.h.docker.putNetwork(thNetwork(upAppNet, thSubnet("172.30.0.0/24", "172.30.0.1")))
	err := insertUpgrade(ctx, f.h.db, upgradeRow{ChangeID: urChange, FromImage: urFrom, ToImage: urTo,
		Config:    containerConfig{Create: instanceCreate(urFrom), Name: containerName, Networks: networks, Targets: targets},
		UpdatedAt: f.h.clock.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := setUpgradePhase(ctx, f.h.db, urChange, "done", "", f.archives(), f.h.clock.Now()); err != nil {
		t.Fatal(err)
	}
}

func w12eAppNet() map[string]*dockerapi.EndpointSettings {
	return map[string]*dockerapi.EndpointSettings{upAppNet: {}}
}

// w12eListing makes NPM's listing call list.
func w12eListing(f *urFixture, list func(ctx context.Context, live npmAPI) ([]npmapi.ProxyHost, error)) {
	inner := f.n.d.api
	f.n.d.api = func(ctx context.Context, env core.Env) (npmAPI, error) {
		live, err := inner(ctx, env)
		if err != nil {
			return nil, err
		}
		return w12dListingAPI{npmAPI: live, list: func(ctx context.Context) ([]npmapi.ProxyHost, error) { return list(ctx, live) }}, nil
	}
}

func TestW12eUpgradeUndoChecksForwards(t *testing.T) {
	t.Run("a free address of a network the upgraded container left", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{})
		w12aSeed(f.h, "172.30.0.20", 3000)
		sawPull := false
		w12eListing(f, func(ctx context.Context, live npmAPI) ([]npmapi.ProxyHost, error) {
			sawPull = urCalled(f.h.docker.calls(), "InspectImage")
			return live.ProxyHosts(ctx)
		})
		coded := nwRefused(t, f.inverse(urResource), errcode.NPMReserved)
		if want := "proxy host 1 forwards to 172.30.0.20, an address darkpanel-npm could take on network app-net"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
		if !sawPull {
			t.Error("the forwards were listed before the original image was made available")
		}
		if c, ok := f.h.docker.container(urNewID); !ok || !c.State.Running || f.phase() != "done" {
			t.Errorf("container %s found %v running %v in phase %q; want the upgraded one running and the change done", urNewID, ok, c.State.Running, f.phase())
		}
	})
	t.Run("a network of the original container no longer exists", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, map[string]*dockerapi.EndpointSettings{"gone-net": {}}, &[]recordedTarget{})
		mark := f.mark()
		err := f.inverse(urResource)
		coded := nwRefused(t, err, errcode.NotFound)
		if want := "network gone-net of the original container no longer exists"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
		if urCalled(f.callsSince(mark), "RemoveContainer") || f.phase() != "done" {
			t.Error("the refused undo removed a container or left phase done")
		}
	})
	t.Run("NPM's API does not answer", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{})
		w12eListing(f, func(context.Context, npmAPI) ([]npmapi.ProxyHost, error) {
			return nil, errors.New("npmapi: connection refused")
		})
		nwRefused(t, f.inverse(urResource), errcode.NPMUnhealthy)
		if f.phase() != "done" {
			t.Errorf("phase %q, want done", f.phase())
		}
	})
	t.Run("a retry after the container was removed uses the recorded list", func(t *testing.T) {
		f := urSetup(t)
		f.replace("")
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{{Where: "proxy host 1", Host: "172.30.0.20", Addr: netip.MustParseAddr("172.30.0.20")}})
		w12eListing(f, func(context.Context, npmAPI) ([]npmapi.ProxyHost, error) {
			return nil, fmt.Errorf("npmapi: the API is down")
		})
		coded := nwRefused(t, f.inverse(urResource), errcode.NPMReserved)
		if want := "proxy host 1 forwards to 172.30.0.20, an address darkpanel-npm could take on network app-net"; coded.Detail != want {
			t.Errorf("detail %q, want %q", coded.Detail, want)
		}
	})
	t.Run("a retry with an empty recorded list restores the original", func(t *testing.T) {
		f := urSetup(t)
		f.replace("")
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{})
		w12eListing(f, func(context.Context, npmAPI) ([]npmapi.ProxyHost, error) {
			return nil, fmt.Errorf("npmapi: the API is down")
		})
		if err := f.inverse(urResource); err != nil {
			t.Fatal(err)
		}
		f.restored()
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/8 timeout 600 go test -count=1 -run TestW12eUpgradeUndoChecksForwards ./internal/modules/npm/`
Expected: FAIL (the undo restores without a check).

- [ ] **Step 3: Implement**

1. In `Inverse`, phase `done`: after `checkArchives`, make the original image available (`h.pull(ctx, u.Config.Create.Image)`, Definite on failure), then the forward check, then `rollback`.
2. The forward check: when the instance's container (`containerName`) exists, list the targets with `forwardTargets` (an `npm.unhealthy` answer is refused Definite with the detail extended as `forwardCheck` does) and call `forwardCheckConfig(u.Config, targets, <the upgraded container's id>)`; when it is missing, use the row's recorded list (`target()` of each) when `u.Config.Targets` is not nil, and the API otherwise.
3. Every refusal is Definite and changes nothing; the rollback of a failed upgrade and recovery are not changed by this task.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/8 timeout 900 go test -count=1 ./internal/modules/npm/...`
Expected: PASS, including `TestUrInverse` (A5: an undo with no such forward restores as today).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/upgrade_recover.go internal/modules/npm/w12e_upgrade_test.go
git commit -m "fix(npm): check forwards before undoing an upgrade"
```

---

### Task 9: D3: verify every start of the original

**Files:**
- Modify: `internal/modules/npm/instance_upgrade.go` (`restore`, `resume`, `rollback`, `fail`)
- Modify: `internal/modules/npm/upgrade_recover.go` (`recoverOriginal`)
- Test: `internal/modules/npm/w12e_upgrade_test.go`

**Interfaces:**
- Consumes: Contracts, Start verification and Recorded targets (Task 7); Docker fake (Task 6).
- Produces: Contracts, Row result.

**Items:** 2026-11-06-v1-security-review#142

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7

- [ ] **Step 1: Extend the test `internal/modules/npm/w12e_upgrade_test.go::TestW12eStartVerifiesHeldAddresses`**

Add at the end of the `upgrade` subtest, after `u := s.upgrade()` and its check:

```go
		c, ok := s.h.docker.container(containerName)
		if !ok || !c.State.Running || c.Config.Image != upFromImage || c.NetworkSettings.Networks[upAppNet] != nil {
			t.Errorf("container %+v (found %v), want the original running without app-net", c, ok)
		}
		if want := "the started container held a forwarded address and left network app-net"; u.Result != want {
			t.Errorf("result %q, want %q", u.Result, want)
		}
```

Add these subtests to the same test:

```go
	t.Run("undo of a finished upgrade", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{{Where: "proxy host 1", Host: "172.30.0.20", Addr: netip.MustParseAddr("172.30.0.20")}})
		f.h.docker.pin(upAppNet, netip.MustParseAddr("172.30.0.20"))
		if err := f.inverse(urResource); err != nil {
			t.Fatal(err)
		}
		f.restored()
		c, _ := f.h.docker.container(containerName)
		if c.NetworkSettings.Networks[upAppNet] != nil {
			t.Error("the restored original stayed on app-net at a forwarded address")
		}
	})
	t.Run("recovery rollback from recreating", func(t *testing.T) {
		f := urSetup(t)
		f.replace(urTo)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{{Where: "proxy host 1", Host: "172.30.0.20", Addr: netip.MustParseAddr("172.30.0.20")}})
		if err := setUpgradePhase(context.Background(), f.h.db, urChange, "recreating", "", f.archives(), f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		f.h.docker.pin(upAppNet, netip.MustParseAddr("172.30.0.20"))
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		c, _ := f.h.docker.container(containerName)
		if !c.State.Running || c.NetworkSettings.Networks[upAppNet] != nil {
			t.Errorf("container %+v, want the original running without app-net", c)
		}
	})
	t.Run("restart of the original from a recorded list", func(t *testing.T) {
		f := urSetup(t)
		w12eDone(t, f, w12eAppNet(), &[]recordedTarget{{Where: "proxy host 1", Host: "172.30.0.20", Addr: netip.MustParseAddr("172.30.0.20")}})
		if err := setUpgradePhase(context.Background(), f.h.db, urChange, "stopped", "", nil, f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		if err := f.h.docker.ConnectNetwork(context.Background(), upAppNet, f.inst.ContainerID, nwStatic("172.30.0.20")); err != nil {
			t.Fatal(err)
		}
		f.setRunning(f.inst.ContainerID, false)
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		c, _ := f.h.docker.container(f.inst.ContainerID)
		if !c.State.Running || c.NetworkSettings.Networks[upAppNet] != nil {
			t.Errorf("container %+v, want the original running without app-net", c)
		}
	})
	t.Run("restart of the original from a row written before w12e", func(t *testing.T) {
		f := urSetup(t)
		w12eDone(t, f, w12eAppNet(), nil)
		if err := setUpgradePhase(context.Background(), f.h.db, urChange, "stopped", "", nil, f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		if err := f.h.docker.ConnectNetwork(context.Background(), upAppNet, f.inst.ContainerID, nwStatic("172.30.0.20")); err != nil {
			t.Fatal(err)
		}
		w12aSeed(f.h, "172.30.0.20", 3000)
		f.setRunning(f.inst.ContainerID, false)
		out, err := f.recover()
		if err != nil || out.State != core.RecoveryRolledBack {
			t.Fatalf("recovery %+v, error %v; want rolled back", out, err)
		}
		c, _ := f.h.docker.container(f.inst.ContainerID)
		if c.NetworkSettings.Networks[upAppNet] != nil {
			t.Error("the original stayed on app-net at an address NPM's API names as forwarded")
		}
	})
	t.Run("a row written before w12e whose API does not answer", func(t *testing.T) {
		f := urSetup(t)
		w12eDone(t, f, w12eAppNet(), nil)
		if err := setUpgradePhase(context.Background(), f.h.db, urChange, "stopped", "", nil, f.h.clock.Now()); err != nil {
			t.Fatal(err)
		}
		f.setRunning(f.inst.ContainerID, false)
		w12eListing(f, func(context.Context, npmAPI) ([]npmapi.ProxyHost, error) {
			return nil, errors.New("npmapi: connection refused")
		})
		var out core.RecoveryOutcome
		var err error
		upDrive(t, f.h, func() { out, err = f.recover() })
		if err != nil || out.State != core.RecoveryNeedsRepair || !strings.Contains(out.Reason, "unverified") {
			t.Fatalf("recovery %+v, error %v; want needs repair naming the unverified start", out, err)
		}
		if c, _ := f.h.docker.container(f.inst.ContainerID); !c.State.Running {
			t.Error("the original was not left running")
		}
	})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/9 timeout 600 go test -count=1 -run TestW12eStartVerifiesHeldAddresses ./internal/modules/npm/`
Expected: FAIL in the extended `upgrade` subtest and the five new subtests (the original keeps app-net).

- [ ] **Step 3: Implement**

1. `restore`: right after `StartContainer` of the original, run `verifyHeld(id, targets)` with the row's recorded list. A hit is not a failure: the function goes on to save the instance row and returns the dropped networks to `rollback`. A failed inspection or lookup is logged with `env.Log` and NPM stays up (W12a-R1). A hit on `darkpanel-npm`, or a hit that could not be disconnected, is returned as an error.
2. `resume` and `recoverOriginal`: the same after their `StartContainer` (in `recoverOriginal` also when the original already runs).
3. A row with `Config.Targets == nil`: after the start, wait for NPM's API (`await`, bounded by `instanceUpgradeAPIWait`), list `forwardTargets`, then verify; in `recoverOriginal` and the recovery rollback, an API that does not answer in time is `RecoveryNeedsRepair` with reason `the original Nginx Proxy Manager container was started but its addresses are unverified: <cause>`, and the container is left running.
4. `rollback` writes the Row result (Contracts) as the result of phase `rolled_back` when networks were dropped; `fail`'s message after a restored original names them (`...; the original instance was restored without network app-net`).

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/9 timeout 900 go test -count=1 ./internal/modules/npm/... && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12e-tmp/9 timeout 1500 go test -race -timeout 24m -count=1 ./internal/modules/npm/`
Expected: PASS, including every `TestUr*` test (A6b: a start that holds no target behaves as today).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/instance_upgrade.go internal/modules/npm/upgrade_recover.go internal/modules/npm/w12e_upgrade_test.go
git commit -m "fix(npm): verify the original NPM's addresses at start"
```

---

### Task 10: Threat model and registers

**Files:**
- Modify: `docs/security/threat-model.md`
- Modify: `docs/dforge/registers/2026-11-06-v1-security-review.md`

**Interfaces:**
- Consumes: the evidence note (Task 1); the behaviour of Tasks 2-9.
- Produces: none.

**Items:** 2026-11-06-v1-security-review#1, 2026-11-06-v1-security-review#6, 2026-11-06-v1-security-review#142, 2026-11-06-v1-security-review#176, 2026-11-06-v1-security-review#177, 2026-11-06-v1-security-review#178, 2026-11-06-v1-security-review#179, 2026-10-21-v1-completion#37

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 1 = 4

- [ ] **Step 1: No new test, because no script reads the threat model (spec A13 is a demo); Step 3's reading is the check**

- [ ] **Step 2: Edit the threat model**

Make `docs/security/threat-model.md` say only what the code now holds (spec A13):

1. Lines 215 and 229: the `[::]` binding's IPv6 leg is built on a host whose only IPv6 addresses are link-local, probed at the reply route's preferred address only.
2. Line 281: a join of NPM is preceded and followed by a confirmed nginx reload. Two residuals remain: workers of the old configuration finish requests they accepted, each once (owner-accepted, U4); and a confirmed reload may not be applied by the master if a name loses its answer between the test and the reload (a race).
3. Lines 292 and 295: every start of NPM that Darkpanel makes is verified against the forward targets, leaving the start-to-inspection time. Line 292 keeps its sentence for starts Darkpanel does not make (the restart policy, an Engine or host restart). The upgrade checks again after the pull.
4. Line 298: the undo of a finished upgrade is checked; it cannot run while NPM's API does not answer, unless the container is gone and the row's recorded list is used.
5. Known residual risks: the hidden tailnet client of an NPM exposure (every IPv6 tailnet client while NPM's networks are IPv4 only); D7's sentence next to the `forward host` risk; the start-to-inspection window; the old-worker requests; what the evidence note's answer 5 says about nginx failing to start on a name with no answer.

- [ ] **Step 3: Read it back against the code**

Read each edited paragraph next to the code it describes (`network_claims.go`, `nginx_reload.go`, `instance_upgrade.go`, `upgrade_recover.go`, `guard/flows.go`, `tailscale/linkguard.go`) and fix any sentence the code does not hold.

- [ ] **Step 4: Registers**

Add the hidden tailnet client as a new low row of the security register, using the spec's D5 wording, and set it `deferred` with the note `w12e D5: accepted residual; a stand-in tailnet flow needs its own design`. Then set rows 142, 176, 177, 178 and 179 to `verify` with `bash <plugin-root>/scripts/register set ... verify`, keeping each note: read the row's last cell and put `w12e <commit>: ` in front of it, since `--note` replaces the cell. Rows 1 and 6 stay as the panel re-review leaves them, and the w12e plan is named in their notes the same way.

- [ ] **Step 5: Commit**

```bash
git add docs/security/threat-model.md docs/dforge/registers/2026-11-06-v1-security-review.md
git commit -m "docs(security): record the w12e residual fixes"
```
