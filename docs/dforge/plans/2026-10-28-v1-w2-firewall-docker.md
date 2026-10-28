# v1 wave 2: firewall Docker integration, reconcile and exposure view Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build wave 2 of the v1 completion spec: the firewall module's Docker integration (detection, the marked `DOCKER-USER` block, per-binding container policy, private sources and forward exemptions, health and repair, the owned-chain cleanup and the preservation of foreign `DOCKER-USER` rules), the background task `firewall.reconcile`, the exposure view with per-source-partition verdicts, their web pages and the container action slot, the web confirmation for manual rules, and the M3 Docker harness that checks exposure against an external scan.

**Architecture:** Two new pure packages carry the logic: `internal/modules/firewall/dockerfw` (settings, bindings and container rules, the block's rendering and markers, realization of rules against container facts, the UFW rule operations, detection, health, and the `*-restore -n` inputs for `DOCKER-USER` and the cleanup) and `internal/modules/firewall/exposure` (source partitions and per-class verdicts over the existing evaluator, which gains a `Decided` field). The module root gains a Docker client seam, its own tables for the integration flag, settings, container rules, cleanup intents and foreign-rule records, journaled effects for that logical state, a `change` that can write the block files, change rows and settings and clean up the loaded chains, and the hooks into wave 1's actions, revert and restore paths and uninstall. The new actions, resources and the reconcile task are registered last, then the web pages and the harness consume them.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), standard library only; `internal/platform/{dockerapi,netfilter,netstat,tslocal}`; `database/sql` over `modernc.org/sqlite` through `internal/core/store`; UFW 0.36.1 and 0.36.2 and Docker CE 29.9.1 on Ubuntu 22.04 and 24.04 and Debian 12 and 13; Docker for the capture script; web: TypeScript, React 19, `@tanstack/react-query`, `darkraise-ui`, Vitest with jsdom and `vitest-axe`; harness: `test/harness` (build tag `harness`) on VMs in CI.

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1-3 | 4-6 | 7-8 | 9-11 | 12-14 | 15-17 | 18 | 19-21 | 22-24 | 25-27 | 28-30 | 31-33 | 34 | 35 | 36-37 | 38 | 39 | 40 | 41 | 42 | 43 | 44 | 45 | 46 | 47-48 | 49 | 50

**Integration check:** `mkdir -p /root/.cache/w2-tmp/integ && export TMPDIR=/root/.cache/w2-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 1200 make lint-web test-web`

**Worktree setup:** `timeout 600 make web-deps`

**Wave check:** `mkdir -p /root/.cache/w2-tmp/wave && export TMPDIR=/root/.cache/w2-tmp/wave GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 900 go test -count=1 ./internal/modules/firewall/... ./internal/platform/... ./internal/core/...`

## Global Constraints

- Scope is spec revision 8 as amended on `main`: sections 2.6, 2.7, 2.8, the Docker parts of 2.3, 2.5 and 2.9, the Docker and exposure parts of 2.10, the firewall Docker rows of section 5 (`docker.enable`, `docker.disable`, `docker.repair`, `docker.sources.set`, `container.allow`, `container.deny`, `container.remove`, `containers.apply`), the Docker and exposure exit criteria of the M3 row of Verification, and register rows 7–13 and 291 of `docs/dforge/registers/2026-10-21-v1-completion.md`. Not built here: the Docker module (wave 3a; its container view renders the container action slot this plan fills), Tailscale, exposure modules and hardening waves.
- Host safety (absolute). This host is live and runs an installed `darkpanel-agent` and `darkpanel-controller`. Never run `ufw`, `iptables`/`ip6tables` (any variant, any verb but `--version` and `-save`), `*-restore`, `nft` with anything but `list`, `sysctl -w`, `docker` against the host daemon with anything but `run`, `exec`, `cp`, `logs`, `inspect`, `ps`, `rm` and `pull` on containers named `dp-w2-*`, or write under `/proc/sys`, `/etc/ufw`, `/etc/default/ufw`, `/etc/docker`, `/var/lib/darkpanel`, `/etc/darkpanel` or `/run/darkpanel`; never start, stop, restart or reload any systemd unit; never talk to `/var/run/docker.sock` from a Go test. Real UFW and Docker work happens only inside privileged containers a task's script starts and removes in the same task (`docker ps -a --filter name=dp-w2-` prints only its header afterwards). Unit tests never touch the host's firewall, sysctls, units, sockets table or Docker daemon: every host access goes through an injected `deps` field, `core.Runner`, `core.FileWriter` or `core.Units` fake, or a root directory inside `t.TempDir()`.
- Every `go`, `golangci-lint`, `make` and `pnpm` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w2-tmp/<task number>` (`mkdir -p` it first; remove it and run `go clean -testcache` when the task ends), also where a step's command omits one of them: `/tmp` is an 8 GiB tmpfs and Unix socket paths over 108 bytes fail. When `df -h /` passes 90 %, trim Go build cache entries older than a few hours.
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`; only the standard library and modules already required are used, and after every task `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing.
- After every task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1 ./...` (every line `ok` or `?`); `go test -race -count=1` on the task's packages; `golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `go mod tidy -diff` (no output). A task that edits `test/harness/` also runs `go vet -tags harness ./test/...` and `go test -tags harness -count=1 ./test/harness/...`. A task that edits `web/` also runs `pnpm -C web lint`, `pnpm -C web typecheck` and `pnpm -C web test`.
- TDD: write the test, run it and see the expected RED (usually a build failure naming the first undefined identifier), implement, run GREEN.
- Frozen files (spec 1.12), which no task edits: `internal/core/contract.go`, `units.go`, `define.go`, `registry.go`, `internal/core/ext/**`, `internal/core/errcode/errcode.go`, `internal/core/lifecycle/**`, the core migrations, `internal/platform/exec.go`, `exec_rules.go`, `files.go`, `systemd.go`, `internal/modules/modules.go`, `internal/cli/modules.go`, `internal/controller/server/**`, `internal/controller/api/**`, `internal/controller/events/**`, `internal/controller/listen/sources.go`, `cmd/darkpanel/paths.go`, `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `web/src/modules/registry.ts`, `web/src/modules/types.ts`, `web/src/app.tsx`, `test/harness/pins.go`. A task that finds a missing code, allowlist rule or extension point in a frozen file stops and reports it; it does not edit the file. Every program and argument vector this plan runs is already allowlisted (Assumptions).
- A task edits only the files of its own **Files:** block. Each task of a wave runs in its own worktree from the wave's base (`main` plus every merged earlier wave), so a package-wide command sees only merged work plus the task's own files. "The base" in a task's text means the work its **Depends on** line names.
- Test isolation: `t.TempDir()` for files and databases; TCP only on `127.0.0.1` port 0; Unix sockets under `os.MkdirTemp("", "dp")`; `coretest.NewFakeClock` wherever a duration above one second is waited on; no fixed path outside a temporary directory; no global mutable state shared between tests of different tasks. Container names start with `dp-w2-cap-` (Task 1's script, the only one that starts containers). Tasks of one wave therefore share no port, path, container or global state.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new test identifier carries its task's prefix from the table in Contracts ("Test prefixes"), except the shared helpers that table names, which only their owning task declares.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, in full sentences, never naming a plan, task, register row or review; `//nolint` names the rule and a reason; gofmt clean. Error strings start with the package name (`firewall:`, `dockerfw:`, `exposure:`, `loaded:`, `eval:`, `harness:`). Refusals are `*errcode.Error` with client-safe details, using the codes already in `internal/core/errcode/errcode.go` (`firewall.unsupported_integration`, `firewall.docker_user_in_use`, `firewall.manual_required`, `firewall.unsupported_configuration`, `firewall.prediction_mismatch`, `plan.guard_failed`, `request.invalid_input`); anything else is a plain error. A problem's detail names the conflicting rule, network or range and never carries a secret.
- The firewall's module id is `firewall`; every `env.Files` call passes module `"firewall"`. `env.Exec` runs only these vectors: `/usr/sbin/ufw [--force|--dry-run] <verb> …`, `/usr/sbin/{iptables,ip6tables} --version`, `/usr/sbin/{iptables,ip6tables}-{nft,legacy}-save -t <filter|nat|raw|mangle|security>`, `/usr/sbin/{iptables,ip6tables}-{nft,legacy}-restore -n` (input on stdin), `nft list tables`, `nft list table <family> <name>`. Docker is reached only through `dockerapi` on its socket (spec R54), never the `docker` CLI. The block files `/etc/ufw/after.rules` and `/etc/ufw/after6.rules` are written whole through `env.Files.Restore` as `firewall.file:<path>` effects (they are `restore` owned paths already); no `OwnedBlock` path is declared.
- Values copied from the spec, used exactly: Docker 28.0 or later with `FirewallBackend.Driver` exactly `iptables` (`iptables` when the field is absent), Docker's chains in the same variant as UFW's and its `raw` protection loaded; default `privateSources` `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`, `fc00::/7`, `fe80::/10`; at most 32 entries, none shorter than /8 (IPv4) or /7 (IPv6); the block's text of spec 2.6 line for line, with log prefix `[DARKPANEL DOCKER DENY] ` and `-m limit --limit 3/min`; binding chain `dp-b-<first 12 hex digits of the binding's hash>`; reconcile wakes on Docker events (container `start`, `die`, `destroy`; network `connect`, `disconnect`, `create`, `destroy`) and every 5 minutes, as actor `system:firewall.reconcile`; Tailscale's ranges `100.64.0.0/10` and `fd7a:115c:a1e0::/48` on `tailscale0`; the exposure verdicts `open`, `closed`, `filtered`, `partial`, `unknown`; the revert timeout is the apply request's `confirmTimeoutSeconds`, 10–900 s, default 60.
- Every mutating Docker action is a `mutation[In]` declared with wave 1's `defineMutation` (revertible, agent side, lock `firewall`, `RepairedBy: "firewall.state.repair"`, `ApplyTimeout` 45 s, the overridable `firewall.lockout` guard, input embedding `core.Guarded`). Plan goes through `fw.plan`, Apply through `fw.prepare` and `fw.run`, Revert through wave 1's `revert`. A revert is never refused for foreign `DOCKER-USER` rules, and no automatic operation ever discards a foreign rule.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind. English only.
- The plan ends with the gates in `docs/dforge/gates.md`. Tasks 8, 48, 49 and 50 touch `test/harness/` or `.github/workflows/harness.yml`, so gate 7 (harness) applies to the branch once it is pushed, as well as on `main`; dispatch `harness.yml` on the branch per the owner's standing authorization. No task creates or pushes a tag.

## Contracts

Names are given with their package; everything not exported is used only inside its package. A task's Interfaces block cites these entries by their heading ("Contracts: dockerfw settings").

### Test prefixes

| Package | Task: prefix |
|---|---|
| `dockerfw` | 1: `cap` (shared helpers `capDir`, `capImages`, `capScenarios`, `capLoad`, `capFile`), 2: `set`, 9: `bd`, 11: `dt`, 15: `bk` (shared helper `bkRuleset`), 19: `rz` (shared helpers `rzFacts`, `rzContainer`, `rzNetwork`), 20: `hl`, 21: `ch`, 22: `ro`, 23: `dc` |
| `exposure` | 5: `pt`, 10: `ex` |
| `loaded` | 3: `vr` |
| `eval` | 4: `dcd` |
| `firewall` (module root) | 12: `fd` (shared helpers `fakeDocker`, `newFakeDocker`, `fdContainer`, `fdNetwork`, `fdDeps`), 16: `dtg`, 18: `ds` (shared helper `dsDB`), 24: `de`, 25: `di`, 26: `dsn`, 28: `drs`, 29: `df` (shared helper `dfState`), 30: `ddf`, 31: `dref`, 32: `dst`, 33: `xv`, 34: `kf` (shared helper `kfHost`), 35: `cl`, 36: `dru`, 37: `dun`, 38: `kfw`, 39: `den`, 40: `ctr`, 41: `dsh`, 42: `dsr`, 43: `drp`, 44: `dre`, 45: `rc`, 46: `reg`, 47: `dee` |
| `test/harness` | 8, 48, 49, 50: `m3d` (pure helpers `m3dx…` with unit tests) |
| web (`web/src/modules/firewall/`) | each test file uses its own file's prefix; shared fixtures only in `fixtures.ts` (Task 6) |


### Dependencies

Each task's **Depends on** line is exactly this: 1–8 none; 9: Task 2; 10: Task 4, Task 5; 11: Task 1, Task 2; 12: Task 3; 13, 14, 17: Task 6; 15, 16, 18: Task 9; 19, 21: Task 15; 20: Task 11, Task 15; 22: Task 19; 23: Task 1, Task 19; 24, 26: Task 18; 25: Task 24; 27: Task 6, Task 13; 28: Task 26; 29: Task 12, Task 19; 30: Task 16; 31: Task 15, Task 18; 32: Task 11, Task 18, Task 20, Task 29; 33: Task 10, Task 12, Task 29; 34: Task 18, Task 21; 35: Task 12, Task 18, Task 21, Task 24, Task 34; 36: Task 16, Task 24, Task 35; 37: Task 35; 38: Task 28, Task 34, Task 35; 39: Task 30, Task 31, Task 32, Task 36; 40: Task 22, Task 30, Task 32, Task 36; 41: Task 31, Task 35, Task 36; 42: Task 31, Task 36, Task 38; 43: Task 31, Task 36; 44: Task 39; 45: Task 40, Task 44; 46: Task 32, Task 33, Task 37, Task 39, Task 40, Task 41, Task 42, Task 43, Task 44, Task 45; 47: Task 46; 48: Task 8, Task 46; 49: Task 48; 50: Task 49.

### Capture tree (Task 1)

`internal/modules/firewall/dockerfw/testdata/capture/capture.sh <image> <outdir>` runs in a privileged container named `dp-w2-cap-<slug>-<scenario>` per image `ubuntu:22.04`, `ubuntu:24.04`, `debian:12`, `debian:13`, installs UFW from the image's archive and `docker-ce` 29.9.1 with `containerd.io` from Docker's repository (the versions in `test/harness/images.json`), and writes `<outdir>/<slug>/<scenario>/` for the scenarios `base`, `block`, `cleanup` and `routed`:

- `base`: UFW enabled with its defaults (incoming deny, routed deny) and `ufw allow 22/tcp`; dockerd running; container `web` (`busybox httpd`) on the default bridge with `-p 8080:80` and `-p 127.0.0.1:8081:80`; network `custom` with `-o com.docker.network.bridge.name=dpcustom` and subnet `172.30.0.0/24`, container `app` on it with `-p 9090:80`; IPv6 network `v6net` (`--ipv6 --subnet fd00:dead::/64`), container `six` on it with `-p 8443:80`.
- `block`: `base`, then the spec 2.6 block for both families written by hand between the markers into `after.rules` and `after6.rules` (bridges `docker0`, `br-+`, `dpcustom`; default `privateSources`; one binding `web` `8080/tcp` all addresses, allowed from any, with a deny of `203.0.113.0/24`), the matching `ufw route allow out on docker0 proto tcp to <web's address> port 80 comment 'darkpanel:01J00000000000000000000000 ctr 01J00000000000000000000001'` and `ufw route deny out on docker0 proto tcp from 203.0.113.0/24 to <web's address> port 80 comment 'darkpanel:01J00000000000000000000002 ctr 01J00000000000000000000003'`, then `ufw reload` (its exit status recorded).
- `cleanup`: `block`, then the files without the block and the cleanup input of Contracts "dockerfw restore inputs" loaded with `iptables-<variant>-restore -n` and `ip6tables-<variant>-restore -n` (exit statuses recorded), then `ufw reload`.
- `routed`: `base` plus network `routednet` with `-o com.docker.network.bridge.gateway_mode_ipv4=routed` and subnet `198.51.100.0/24`.

Each scenario directory holds `save/<variant>-<v4|v6>-<table>.txt` for the five tables of each variant whose tables exist (save output without the `# Generated by`/`# Completed on` lines, chain counters `[0:0]`), `files/etc/ufw/{after,after6,user,user6}.rules` and `files/etc/default/ufw`, `info.json` (the daemon's `/info` with `ServerVersion` and `FirewallBackend`), `version.json` (`/version`), `networks.json` (`/networks` with every network inspected), `containers.json` (every container inspected), `interfaces.txt` (`ip -o link` names, one per line), `commands.txt` (each command run and its exit status) and `versions.txt` (`iptables --version`, `ufw --version`, `dockerd --version`). Test-only helpers in `dockerfw/capture_test.go`: `capDir = "testdata/capture"`, `capImages` (the four slugs `ubuntu-22.04`, `ubuntu-24.04`, `debian-12`, `debian-13`), `capScenarios`, `capLoad(t *testing.T, image, scenario string) map[fwstate.Family]*netfilter.Ruleset` (the variant UFW uses, all five tables) and `capFile(t *testing.T, image, scenario, rel string) []byte`.

### dockerfw settings and facts (Task 2)

Package `internal/modules/firewall/dockerfw` (doc comment in `settings.go`), imports only the standard library, `internal/core/errcode`, `internal/modules/firewall/fwstate`, `internal/modules/firewall/model` and `internal/platform/netfilter`; it reads no host state.

```go
type Exemption struct {
	InInterface     string     `json:"inInterface,omitempty"`
	OrigDestination netip.Addr `json:"origDestination"`
	OrigPort        uint16     `json:"origPort,omitempty"`
	Destination     netip.Addr `json:"destination"`
	Proto           string     `json:"proto"` // tcp | udp
	Port            uint16     `json:"port,omitempty"`
}
type Settings struct {
	PrivateSources    []netip.Prefix `json:"privateSources"`
	ForwardExemptions []Exemption    `json:"forwardExemptions"`
}
const MaxPrivateSources = 32
func DefaultSettings() Settings                       // the six default ranges in spec order, no exemptions
func (s Settings) Validate() error                    // *errcode.Error request.invalid_input, pointers /privateSources/<i>, /forwardExemptions/<i>/<field>
func (s Settings) Private(a netip.Addr) (netip.Prefix, bool) // the first privateSources range holding a
func (e Exemption) Family() fwstate.Family

// the container facts every later dockerfw task reads (filled by the module root, Task 29)
type Endpoint struct {
	Network      string     // network name
	Bridge       string     // the network's bridge interface
	Addr4, Addr6 netip.Addr // zero when absent
}
type Published struct {
	HostAddr      netip.Addr // as Docker reports it; 0.0.0.0 or :: for every address
	HostPort      uint16
	ContainerPort uint16
	Proto         string
}
type Container struct {
	ID, Name, Project, Service string
	Ports     []Published
	Endpoints []Endpoint
}
type Network struct {
	ID, Name, Driver, Bridge     string
	Subnets                      []netip.Prefix
	IPv6                         bool
	GatewayModeV4, GatewayModeV6 string // com.docker.network.bridge.gateway_mode_ipv4|ipv6, "" when unset
}
type Facts struct {
	Containers []Container // running containers only, sorted by name
	Networks   []Network   // sorted by name
}
func Bridge(n Network) string // com.docker.network.bridge.name, "docker0" for the network named "bridge", else "br-" + ID[:12]
```

`Validate`: every prefix is valid and masked (`p == p.Masked()`), at most 32, IPv4 bits ≥ 8, IPv6 bits ≥ 7, no duplicate; an exemption's two addresses are valid, not unspecified and of one family, `proto` is `tcp` or `udp`, `inInterface` empty or matching `^[a-zA-Z0-9][a-zA-Z0-9_.+-]{0,14}$`, ports 0 (absent) or 1–65535, no duplicate exemption. Whether an exemption's destination lies in a Docker subnet is checked against facts by `ExemptionConflicts` (Contracts: dockerfw detection).

### dockerfw bindings and rules (Task 9)

```go
type Binding struct {
	Selector string     `json:"selector"`           // container name, or <compose project>/<service>
	HostAddr netip.Addr `json:"hostAddr,omitzero"`  // zero: every host address
	HostPort uint16     `json:"hostPort"`
	Proto    string     `json:"proto"`               // tcp | udp
}
func (b Binding) ID() string     // first 12 hex digits of sha256("<selector>|<hostAddr or all>|<hostPort>|<proto>")
func (b Binding) Chain() string  // "dp-b-" + ID()
func (b Binding) Validate() error // request.invalid_input, pointers /selector /hostAddr /hostPort /proto
func (b Binding) Selects(name, project, service string) bool
type Kind string
const ( Allow Kind = "allow"; Deny Kind = "deny" )
type ContainerRule struct {
	ID      string       `json:"id"`              // a ULID
	Binding Binding      `json:"binding"`
	Kind    Kind         `json:"kind"`
	From    netip.Prefix `json:"from,omitzero"`   // zero: any source (allow only)
}
func (r ContainerRule) Validate(s Settings) error // pointers prefixed /binding, then /kind, /from
const LabelPrefix = "ctr "                         // a realized UFW rule's label: "ctr <ContainerRule.ID>"
func RuleOf(label string) (id string, ok bool)
```

Selector patterns: a name `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`, a Compose service `^[a-z0-9][a-z0-9_-]{0,62}/[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`; `Selects` compares a name selector with the container name (without Docker's leading `/`) and a service selector with the Compose labels. `ContainerRule.Validate`: a deny needs `From`; `From` is masked; a deny whose `From` lies inside a `privateSources` range is refused at `/from` ("it could never match", spec 2.6); `HostAddr`, when set, is not unspecified.

### dockerfw block (Task 15)

```go
const (
	ChainDockerUser = "DOCKER-USER"
	ChainBindings   = "darkpanel-docker-bindings"
	ChainDeny       = "darkpanel-docker-deny"
	BlockID         = "docker"          // marker lines "# BEGIN DARKPANEL docker" / "# END DARKPANEL docker", as internal/platform/blocks.go writes them
	LogPrefix       = "[DARKPANEL DOCKER DENY] "
)
type BindingLine struct {
	Proto         string
	ContainerAddr netip.Addr
	ContainerPort uint16
	HostAddr      netip.Addr // zero: --ctorigdst omitted
	HostPort      uint16
	Chain         string     // the binding's dp-b-<id>
}
type BindingChain struct {
	Name  string
	Deny  []netip.Prefix // in order
	Allow []netip.Prefix // empty: 0.0.0.0/0 or ::/0
}
type BlockInput struct {
	CustomBridges []string // bridge names that are neither docker0 nor br-<id>, sorted
	Settings      Settings
	Lines         []BindingLine
	Chains        []BindingChain
}
func Render(in BlockInput, f fwstate.Family) []byte           // "*filter\n" … "COMMIT\n", only the family's entries
func WithBlock(file, block []byte) ([]byte, error)            // replaces the marked block or appends it, markers included
func WithoutBlock(file []byte) ([]byte, error)                // the file with no marked block
func BlockOf(file []byte) (block []byte, ok bool, err error)  // err for duplicated, nested or unterminated markers
func Owned(chain string) bool                                 // DOCKER-USER, darkpanel-*, dp-b-*
func BlockRules(block []byte) (*netfilter.Ruleset, error)     // the block parsed with netfilter.ParseRestore
func Foreign(chain *netfilter.Chain, known []netfilter.Rule, f fwstate.Family) []netfilter.Rule // in chain order: every rule whose netfilter.Key matches no known rule and that is not an unconditional -j RETURN
```

`Render` follows spec 2.6's listing line for line: the declarations (`ufw-user-forward` or `ufw6-user-forward`, `DOCKER-USER`, the two `darkpanel-docker-*` chains, then each binding chain of the family in `Chains` order), the `DOCKER-USER` lines (custom bridges in order, the family's `privateSources`, the family's exemptions rendered as spec 2.6 says), the binding lines of the family in `Lines` order, the binding chains' deny, allow and final deny lines, the deny chain, `COMMIT`. IPv6 uses `ufw6-user-forward` and destination `fc00::/7` in place of the three IPv4 private destinations.

### dockerfw realization (Task 19)

Uses Task 2's `Facts`, `Container`, `Endpoint`, `Network` and `Published`; declares none of them.

```go
type Realized struct {
	Block BlockInput   // CustomBridges and Settings filled
	Rules []model.Rule // the tagged UFW rules the rows need, in the order UFW must hold them; ID empty, Comment "ctr <rule id>"
}
func Realize(rows []ContainerRule, facts Facts, s Settings) Realized
```

`Realize` per allow row, per container the binding selects that publishes `HostPort`/`Proto` on `HostAddr` (or on every address), per network endpoint and family: with a container address in the family, one `route allow out on <bridge> proto <p> [from <From>] to <addr> port <container port>` rule, one `BindingLine` and the binding's `BindingChain` allow entry; for a family the binding is published on but the container has no address in (`docker-proxy`, spec 2.6 "Proxied families"), one `allow in proto <p> [from <From>] to <host addr or any> port <host port>` rule of that family. Per deny row on a binding that has an allow row: the chain's deny entry, one `route deny` rule placed after every Darkpanel route allow for the same container address and port, and for a proxied family one `deny in` rule placed before that family's `allow in` rule. A binding whose container is not running realizes nothing.

### dockerfw rule operations (Task 22)

```go
type RuleOp struct {
	Delete string      // a rule id to delete; empty for an add
	Add    *model.Rule // a rule to add, with its new ID
	Insert int         // 1-based position for Add in UFW's numbering; 0 appends
}
func RuleOps(current []model.Rule, desired []model.Rule, newID func() string) ([]RuleOp, error)
```

`current` is the configured rules (both families, as wave 1's status lists them); only Darkpanel rules whose label `RuleOf` reads are container rules. A desired rule equal to a current container rule (every field but `ID`) is kept; every other current container rule is deleted; every missing desired rule is added at the position that makes UFW's order match `desired`'s relative order; manual rules and other Darkpanel rules are never deleted or moved.

### dockerfw detection (Task 11)

```go
type Detection struct {
	Version       string // daemon ServerVersion
	Driver        string // FirewallBackend.Driver, "iptables" when absent
	DockerVariant string // "nft" | "legacy" | "" (neither holds DOCKER-USER and DOCKER-FORWARD) | "both"
	UFWVariant    string
	RawProtection bool
}
func Unsupported(d Detection) []string                                  // reasons; empty means supported
func DockerChains(filter *netfilter.Table) bool                         // DOCKER-USER and DOCKER-FORWARD both present
func RawProtected(raw *netfilter.Table, facts Facts) bool               // the rule Task 1's captures show
func ForeignDNAT(nat *netfilter.Table) []netfilter.Rule                 // DNAT rules reachable from nat PREROUTING outside Docker's chains
func Propose(r netfilter.Rule, f fwstate.Family) (Exemption, bool)
func Covered(r netfilter.Rule, ex []Exemption) bool
func ExemptionConflicts(ex []Exemption, nets []Network) []string        // "exemption <i>: destination <addr> lies in Docker network <name> (<subnet>)"
```

### dockerfw health (Task 20)

```go
type Level string
const ( Degraded Level = "degraded"; Broken Level = "broken" )
type Issue struct {
	Level  Level  `json:"level"`
	Reason string `json:"reason"`
}
type HealthInput struct {
	Want         bool                                 // the block belongs in the files: flag on and UFW enabled
	Files        map[fwstate.Family][]byte            // after.rules (V4) and after6.rules (V6) as on disk
	Expected     map[fwstate.Family][]byte            // Render of the realized block
	Loaded       map[fwstate.Family]*netfilter.Ruleset
	IPv6         bool                                 // IPV6 in /etc/default/ufw
	DefaultInput string                               // configured DEFAULT_INPUT_POLICY
	Facts        Facts
	Settings     Settings
	Interfaces   []string                             // every interface name present
}
func Health(in HealthInput) []Issue
func Unprotected(n Network, s Settings) (reason string, unprotected bool)
```

`Health` covers spec 2.6's list for both families in this order: block missing from a file (broken); the loaded `DOCKER-USER`, `darkpanel-docker-bindings`, `darkpanel-docker-deny` and `dp-b-*` chains not equal to the block's rules exactly once and in order, compared with `netfilter.Key` (broken); `FORWARD` not jumping to `DOCKER-USER` before Docker's own accept rules (broken); a foreign `DOCKER-USER` rule (broken, naming it); a foreign DNAT forward without an exemption (degraded, naming it); an exemption destination inside a Docker subnet (broken); a configured or loaded incoming policy that is not `DROP` or `REJECT` (broken); a `br-*` interface that is no Docker network's bridge (degraded, naming it); an unprotected network (degraded, with `Unprotected`'s reason: routed or `nat-unprotected` gateway mode, or a custom bridge name with a non-private subnet); `IPV6=no` with an IPv6-enabled Docker network (degraded). With `Want` false only the foreign-rule, incoming-policy and network checks apply.

### dockerfw restore inputs (Task 21)

Uses Task 15's `Foreign` to tell foreign rules apart.

```go
type Layout struct {
	Before      []string `json:"before"`      // foreign rules' save text, in order, placed before the block's first line
	After       []string `json:"after"`       // after its last line
	Interleaved bool     `json:"interleaved"` // some foreign rule sat between block lines; all are in Before
	Order       []string `json:"order"`       // the chain as it was, when Interleaved
}
func LayoutOf(chain *netfilter.Chain, known []netfilter.Rule, f fwstate.Family) Layout
func DockerUserInput(block []string, l Layout) []byte // "*filter", ":DOCKER-USER - [0:0]", l.Before, block (nil: "-A DOCKER-USER -j RETURN"), l.After, "COMMIT"
func CleanupInput(loaded *netfilter.Ruleset) []byte   // DOCKER-USER redeclared holding only -j RETURN, then -F and -X of every darkpanel-* and dp-b-* chain loaded, "COMMIT"
```

Rule texts are `netfilter.Rule.Text` (save syntax `-A <chain> …`).

### loaded variant read (Task 3)

```go
func (r Reader) ReadVariant(ctx context.Context, env core.Env, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error)
```

Same safety rules as `Read`: a legacy table is read only when `/proc/net/ip{,6}_tables_names` lists it, nothing nftables runs while `/sys/module/nf_tables` is absent; a variant that cannot be read safely returns `nil, nil`.

### eval decided (Task 4)

`fwstate.Result` gains `Decided string`: for `Accept`, the filter hook rule (`<table>/<chain>#<n>`) or policy (`<table>/<chain> policy ACCEPT`) that accepted the packet, or the last hook's when the filter table holds no rule on the path; for `Drop` and `Reject`, the rule or policy that dropped or rejected it; for `Unknown`, the rule whose match or target could not be decided, or empty for a hook-level reason. Nothing else in `Result` changes.

### exposure (Tasks 5 and 10)

Package `internal/modules/firewall/exposure`, imports only the standard library, `fwstate`, `eval` and `internal/platform/netfilter`; pure.

```go
// Task 5
type Iface struct {
	Name     string
	Up       bool
	Loopback bool
	Tailnet  bool // tailscale0
	Bridge   bool // a Docker bridge
	Addrs    []netip.Addr
}
type Class struct {
	Name  string       // internet | private | tailnet | localhost
	Range netip.Prefix // private only
}
type PortRange struct{ From, To uint16 }
type Range struct{ From, To netip.Addr }
func Classes(private []netip.Prefix, f fwstate.Family) []Class
func Cuts(rs *netfilter.Ruleset) (srcs []netip.Prefix, ifaces []string, sports []PortRange)
func Atoms(include, exclude, cuts []netip.Prefix) []Range
func (r Range) Prefixes() []netip.Prefix
func PortAtoms(cuts []PortRange) []PortRange // a partition of 1–65535

// Task 10
const DefaultMaxProbes = 4096
type Input struct {
	Rules     *netfilter.Ruleset
	Family    fwstate.Family
	Route     fwstate.Router
	Ifaces    []Iface
	Private   []netip.Prefix
	ShieldsUp bool
	Describe  func(ref string) string // nil: the ref itself
	MaxProbes int                     // 0: DefaultMaxProbes
}
type Target struct {
	Proto string
	Addr  netip.Addr // unspecified: every address
	Port  uint16
}
type Allowed struct {
	Sources   []string `json:"sources"`
	Ifaces    []string `json:"ifaces,omitempty"`
	SrcPorts  []string `json:"srcPorts,omitempty"`
	DecidedBy string   `json:"decidedBy"`
}
type Verdict struct {
	Class     string    `json:"class"`
	Range     string    `json:"range,omitempty"`
	Verdict   string    `json:"verdict"` // open | closed | filtered | partial | unknown
	DecidedBy string    `json:"decidedBy,omitempty"`
	Allowed   []Allowed `json:"allowed,omitempty"`
	Note      string    `json:"note,omitempty"`
}
func Evaluate(in Input, t Target) []Verdict
```

Partitions come from every source prefix, in-interface and source-port range any rule of the ruleset names (a superset of the row's path, which is always a correct partition). Per class: internet = addresses outside every `privateSources` range, Tailscale's ranges, loopback, link-local multicast and unspecified, on every up interface that is not loopback, `tailscale0` or a Docker bridge; private = the range's addresses on the same interfaces; tailnet = Tailscale's range of the family on `tailscale0` when it exists and is up (Note "host firewall; tailnet policy not evaluated"; with `ShieldsUp` `closed`, DecidedBy "Tailscale shields-up"); localhost = an `output` packet to `lo`. A probe carries a concrete source port (one per port atom). Verdict mapping: every probe `Accept` → `open`; every probe `Reject` → `closed`; no `Accept` and some `Drop` → `filtered`; `Accept` mixed with others → `partial` with `Allowed` grouping the accepted probes by `Decided`; any `Unknown` → `unknown` with the first reason in `Note`; more probes than `MaxProbes` → `unknown` with Note "too many partitions". A target bound to a loopback address is `closed` with DecidedBy "bound to loopback" for every class but localhost. A wildcard target's destination is the probe interface's first address of the family.

### Docker client seam (Task 12)

In package `firewall` (`deps.go`, `wire.go`):

```go
type dockerEvents interface {
	Next() (dockerapi.Event, error)
	Close() error
}
type dockerAPI interface {
	Info(ctx context.Context) (dockerapi.SystemInfo, error)
	Version(ctx context.Context) (dockerapi.VersionInfo, error)
	ListContainers(ctx context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error)
	InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
	ListNetworks(ctx context.Context, f dockerapi.Filters) ([]dockerapi.Network, error)
	Events(ctx context.Context, o dockerapi.EventsOptions) (dockerEvents, error)
}
// new deps fields
	docker      dockerAPI
	readVariant func(ctx context.Context, env core.Env, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error)
	sockets     func() ([]netstat.Socket, error)
	interfaces  func() ([]netstat.Interface, error)
	routeGet    func(q netstat.RouteQuery) (netstat.Route, error)
	shieldsUp   func(ctx context.Context) (on, known bool, err error)
```

`productionFW` sets them from `dockerapi.New(dockerapi.DefaultSocket)` (behind a small adapter whose `Events` returns the `*dockerapi.Stream[dockerapi.Event]`), `loaded.Reader.ReadVariant`, `netstat` under the module root, and `tslocal` prefs (`known` false when tailscaled is unavailable). Test helpers in `docker_fake_test.go`: `fakeDocker` (fields `info`, `version`, `containers []dockerapi.ContainerJSON`, `networks []dockerapi.Network`, `events chan dockerapi.Event`, `err error`, `calls []string`), `newFakeDocker() *fakeDocker` (Docker 29.9.1, driver `iptables`, no containers, the `bridge` network), `fdContainer(name string, opts ...func(*dockerapi.ContainerJSON)) dockerapi.ContainerJSON`, `fdNetwork(name, bridge string, subnets ...string)`, and `fdDeps(h *fakeHost, d *fakeDocker) deps`, which extends wave 1's `fakeHost` deps with the Docker fields (the variant reads, sockets, interfaces and routes served from fields of the fake host's `onRun`-style hooks).

### Docker store (Task 18)

Migration `internal/modules/firewall/migrations/agent/0003_firewall_docker.sql` (expand-only, `STRICT`):

```sql
CREATE TABLE firewall_settings (
    id       INTEGER PRIMARY KEY CHECK (id = 1),
    docker   INTEGER NOT NULL DEFAULT 0 CHECK (docker IN (0, 1)),
    settings TEXT NOT NULL
) STRICT;
CREATE TABLE firewall_container_rules (
    id         TEXT PRIMARY KEY,
    selector   TEXT NOT NULL,
    host_addr  TEXT NOT NULL,
    host_port  INTEGER NOT NULL CHECK (host_port BETWEEN 1 AND 65535),
    proto      TEXT NOT NULL CHECK (proto IN ('tcp', 'udp')),
    kind       TEXT NOT NULL CHECK (kind IN ('allow', 'deny')),
    source     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (selector, host_addr, host_port, proto, kind, source)
) STRICT;
CREATE TABLE firewall_docker_cleanups (
    owner      TEXT PRIMARY KEY,
    started_at TEXT NOT NULL
) STRICT;
CREATE TABLE firewall_docker_foreign (
    owner      TEXT PRIMARY KEY,
    record     TEXT NOT NULL,
    created_at TEXT NOT NULL
) STRICT;
```

`host_addr` and `source` are `""` for "all" and "any". In `docker_store.go`, each taking `*store.DB` or `*sql.Tx` as wave 1's store functions do:

```go
type dockerConfig struct {
	Enabled  bool              `json:"enabled"`
	Settings dockerfw.Settings `json:"settings"`
}
func readDockerConfig(ctx context.Context, db *store.DB) (dockerConfig, error)       // no row: {false, dockerfw.DefaultSettings()}
func writeDockerConfig(ctx context.Context, tx *sql.Tx, c dockerConfig) error
func listContainerRules(ctx context.Context, db *store.DB) ([]dockerfw.ContainerRule, error) // ordered by id
func containerRule(ctx context.Context, db *store.DB, id string) (dockerfw.ContainerRule, bool, error)
func insertContainerRule(ctx context.Context, tx *sql.Tx, r dockerfw.ContainerRule, at time.Time) error
func deleteContainerRule(ctx context.Context, tx *sql.Tx, id string) error
func replaceContainerRules(ctx context.Context, tx *sql.Tx, rows []dockerfw.ContainerRule, at time.Time) error
func beginCleanup(ctx context.Context, db *store.DB, owner string, at time.Time) error
func endCleanup(ctx context.Context, db *store.DB, owner string) error
func pendingCleanups(ctx context.Context, db *store.DB) ([]string, error)
```

Shared test helper `dsDB(t *testing.T) *store.DB` (wave 1's `fwTestDB` with migration 0003 applied).

### Docker effects (Tasks 24 and 25)

New effect resources, all `Local` and `InverseLocal` like wave 1's:

| Resource | Kind | State (`Before`/`After`/`Post`) | Version | Inverse (Task 25) |
|---|---|---|---|---|
| `firewall.setting:docker` | Modify | the flag (bool) | none | write the `Before` flag |
| `firewall.setting:sources` | Modify | `dockerfw.Settings` | none | write the `Before` settings |
| `firewall.container:<rule id>` | Create / Delete | the `dockerfw.ContainerRule` or `null` | none | delete the row / insert the `Before` row |
| `firewall.chains:docker` | Modify | `"loaded"` or `"absent"` (any `darkpanel-*` or `dp-b-*` chain loaded in either family) | none | `absent` → `d.cleanChains(ctx, env, env.ChangeID)` (an error when the field is nil); `loaded` → `ufw reload` when UFW is active |

Task 24 adds, in `docker_effects.go`: the op builders `flagOp(on bool) dockerOp`, `sourcesOp(s dockerfw.Settings) dockerOp`, `rowCreateOp(r dockerfw.ContainerRule) dockerOp`, `rowDeleteOp(r dockerfw.ContainerRule) dockerOp` and `chainsOp(state string, apply func(ctx context.Context, env core.Env) error) dockerOp`; `runDockerEffect` refuses an op whose `apply` is nil; `runDockerEffect(ctx context.Context, env core.Env, d deps, op dockerOp) error` (journaled like `runUfw`: Intend, call, observe, Made/Skipped); `dockerDoc(ctx context.Context, env core.Env, d deps, resource string) (json.RawMessage, error)` for `observe`; and the `observe` dispatch for the four nouns. `type dockerOp struct { Kind core.EffectKind; Resource string; After any; apply func(ctx context.Context, env core.Env) error }`. Row and setting writes happen in one SQL transaction each.

Task 25 adds the field `cleanChains func(ctx context.Context, env core.Env, owner string) error` to `deps` (`deps.go`); its tests set it to a fake. Task 35 sets it in `productionFW` (`wire.go`) to `cleanupChains` in `cleanupPreserve` mode, and `fdDeps` leaves it nil unless a test sets it.

### change and target (Tasks 16, 30, 36)

`change` (target.go) gains:

```go
	files    map[string][]byte          // whole after.rules / after6.rules contents to write before the ufw commands
	flag     *bool                      // the Docker integration flag to store; nil keeps it
	sources  *dockerfw.Settings         // settings to store; nil keeps them
	rowsAdd  []dockerfw.ContainerRule   // container rules to insert
	rowsDel  []string                   // container rule ids to delete
	cleanup  bool                       // run the owned-chain cleanup (Task 35) before the transition commands
	reload   bool                       // the change needs `ufw reload` when UFW stays active (block files changed)
```

`predictTarget` (Task 16) overlays `files` onto `cfgN.Files` before predicting, forces a `TransReload` when `reload` is set and UFW is active, and predicts the cleanup by removing every `darkpanel-*` and `dp-b-*` chain and setting `DOCKER-USER` to `-j RETURN` in both families. `fw.run` (Task 36) runs, in order: a restore's files; the logical effects (flag, sources, row deletes, row inserts); the `files` writes (`writeFileEffect`); the ufw ops; the cleanup's chain effect; the transition commands; a restore's sysctl values. The plan diff (Task 30) adds lines `docker integration on|off`, `private sources: …`, `forward exemptions: …`, `container rule + <selector> <hostAddr|all>:<hostPort>/<proto> <allow|deny> <from|any>`, `container rule - …`, `after.rules: Docker block written|removed`, `after6.rules: …`, `Docker chains removed`.

### Docker state (Task 29)

```go
type dockerState struct {
	available  bool
	reason     string                // why Docker is unavailable
	det        dockerfw.Detection
	facts      dockerfw.Facts
	cfg        dockerConfig
	rows       []dockerfw.ContainerRule
	realized   dockerfw.Realized
	interfaces []netstat.Interface
}
func (f fw) dockerState(ctx context.Context, env core.Env, cur fwstate.Loaded) (dockerState, error)
func blockFiles(cfg fwstate.Config, st dockerState, enabled bool) (map[string][]byte, error) // the after files with the realized block (enabled) or without it
```

Docker unavailable (`dockerapi.ErrUnavailable`) is not an error: `available` false, empty facts. Test helper `dfState(t *testing.T, opts ...) dockerState`.

### Refusals (Task 31)

In `docker_refuse.go`, called from `fw.prepare` after `hostUnsupported`:

```go
func dockerUserInUse(cur fwstate.Loaded, cfg fwstate.Config, flag bool) error // flag on and a foreign DOCKER-USER rule in either family: *errcode.Error firewall.docker_user_in_use listing the rules
func refuseOpenIncoming(flagAfter bool, final fwstate.Config) error             // flagAfter and DEFAULT_INPUT_POLICY ACCEPT: firewall.unsupported_integration
```

`prepare` calls `dockerUserInUse` for every mutation except `firewall.docker.repair` with `discardForeign: true` and `manual: true`; the builders of `defaults.set`, `state.enable`, `state.repair`, `snapshot.restore`, `docker.enable` and `docker.sources.set` call `refuseOpenIncoming`.

### Owned-chain cleanup (Task 35)

```go
type cleanupMode int
const ( cleanupRefuse cleanupMode = iota; cleanupPreserve )
func (f fw) cleanupChains(ctx context.Context, env core.Env, owner string, mode cleanupMode) error
func (f fw) finishCleanups(ctx context.Context, env core.Env) error // repeats every pending intent from step 2
```

Spec 2.6's five steps: `beginCleanup(owner)`; the files without the block (only when they hold one; as `firewall.file:` effects when `env.Effects` is set, else `env.Files.Restore`); re-read `DOCKER-USER` and, in `cleanupRefuse` mode, refuse with `firewall.docker_user_in_use` when it holds a foreign rule, while `cleanupPreserve` keeps the foreign rules through Task 34's `keepForeign`; `dockerfw.CleanupInput` through `<family><variant>-restore -n`; `ufw reload` when UFW is active and the caller asked for it; verify that no `darkpanel-*` or `dp-b-*` chain is loaded and `DOCKER-USER` holds only `-j RETURN` plus preserved foreign rules; `endCleanup(owner)`. `owner` is the change id, or `"uninstall"`. Inside a change, the chain step is journaled as `chainsOp("absent", <the restore-input load>)` through `runDockerEffect`.

### Foreign-rule preservation (Tasks 34 and 38)

Task 34 declares, in `docker_preserve.go`, the store functions of the `firewall_docker_foreign` table (created by Task 18's migration):

```go
type foreignRecord struct {
	Layouts map[fwstate.Family]dockerfw.Layout `json:"layouts"`
	Block   map[fwstate.Family][]string        `json:"block"` // the DOCKER-USER lines to reload; nil: -j RETURN
}
func putForeign(ctx context.Context, db *store.DB, owner string, rec foreignRecord, at time.Time) error
func foreignOf(ctx context.Context, db *store.DB, owner string) (foreignRecord, bool, error)
func deleteForeign(ctx context.Context, db *store.DB, owner string) error
```

```go
func (f fw) keepForeign(ctx context.Context, env core.Env, owner string, known [][]byte, next map[fwstate.Family][]string, transition func() error) (interleaved bool, err error)
func (f fw) finishForeign(ctx context.Context, env core.Env, owner string) (interleaved bool, err error)
```

`known` are the `after.rules`/`after6.rules` contents whose block lines count as Darkpanel's (the files before and after the transition). Before `transition` it reads `DOCKER-USER` in both families, computes `dockerfw.LayoutOf`, and stores `putForeign(owner, …)`; after it, it loads `dockerfw.DockerUserInput(next[f], layout)` with `*-restore -n`, then `deleteForeign`. `next[f]` is nil when the restored state has no block loaded (integration off or UFW stopped). `finishForeign` repeats the reload for a record a crash left. An interleaved layout makes the caller end the change `needs_repair` with the original order in its detail.

### Shapes the web and the schemas read (Tasks 32, 33, 39–44)

Exported from package `firewall`; `web/src/modules/firewall/types.ts` (Task 6) mirrors their JSON exactly.

```go
type DockerStatus struct {
	Available       bool                     `json:"available"`
	Reason          string                   `json:"reason,omitempty"`
	Version         string                   `json:"version,omitempty"`
	Driver          string                   `json:"driver,omitempty"`
	DockerVariant   string                   `json:"dockerVariant,omitempty"`
	UFWVariant      string                   `json:"ufwVariant,omitempty"`
	RawProtection   bool                     `json:"rawProtection"`
	Supported       bool                     `json:"supported"`
	Unsupported     []string                 `json:"unsupported,omitempty"`
	Enabled         bool                     `json:"enabled"`
	Health          string                   `json:"health"` // off | ok | degraded | broken | unsupported
	Issues          []dockerfw.Issue         `json:"issues,omitempty"`
	ForeignRules    []string                 `json:"foreignRules,omitempty"`
	ForeignForwards []dockerfw.Exemption     `json:"foreignForwards,omitempty"` // proposed exemptions for uncovered foreign DNAT rules
	ManualRoutes    []string                 `json:"manualRoutes,omitempty"`    // manual route rules to containers: no effect without a Darkpanel binding
	Networks        []DockerNetwork          `json:"networks"`
	Settings        dockerfw.Settings        `json:"settings"`
	Rules           []dockerfw.ContainerRule `json:"rules"`
	Bindings        []DockerBinding          `json:"bindings"`
}
type DockerNetwork struct {
	Name      string   `json:"name"`
	ID        string   `json:"id"`
	Bridge    string   `json:"bridge"`
	Subnets   []string `json:"subnets"`
	Protected bool     `json:"protected"`
	Reason    string   `json:"reason,omitempty"`
}
type DockerBinding struct {
	Binding       dockerfw.Binding `json:"binding"`
	ID            string           `json:"id"`
	Containers    []string         `json:"containers"`
	ContainerPort uint16           `json:"containerPort"`
	Allowed       []string         `json:"allowed"` // sources, "any" for every source
	Denied        []string         `json:"denied"`
	Proxied       []string         `json:"proxied,omitempty"`      // families served by docker-proxy
	PrivateReach  []string         `json:"privateReach,omitempty"` // privateSources ranges that reach it only through the exemption
}
type DockerEnableInput struct{ core.Guarded }
type DockerDisableInput struct{ core.Guarded }
type DockerRepairInput struct {
	core.Guarded
	DiscardForeign bool `json:"discardForeign,omitempty"`
	Manual         bool `json:"manual,omitempty"`
}
type DockerSourcesInput struct {
	core.Guarded
	PrivateSources    *[]netip.Prefix       `json:"privateSources,omitempty"`
	ForwardExemptions *[]dockerfw.Exemption `json:"forwardExemptions,omitempty"`
}
type ContainerAllowInput struct {
	core.Guarded
	Binding dockerfw.Binding `json:"binding"`
	From    netip.Prefix     `json:"from,omitzero"`
}
type ContainerDenyInput struct {
	core.Guarded
	Binding dockerfw.Binding `json:"binding"`
	From    netip.Prefix     `json:"from"`
}
type ContainerRemoveInput struct {
	core.Guarded
	ID string `json:"id"`
}
type ContainerOp struct {
	Op      string            `json:"op"` // allow | deny | remove
	Binding *dockerfw.Binding `json:"binding,omitempty"`
	From    netip.Prefix      `json:"from,omitzero"`
	ID      string            `json:"id,omitempty"`
}
type ContainersApplyInput struct {
	core.Guarded
	Ops []ContainerOp `json:"ops"` // empty: re-realize the stored rows against the current containers
}
type ExposureView struct {
	Rows  []ExposureRow `json:"rows"`
	Notes []string      `json:"notes,omitempty"` // out of sync, unsupported, Docker unavailable
}
type ExposureRow struct {
	Proto     string              `json:"proto"`
	Addr      string              `json:"addr"`
	Port      uint16              `json:"port"`
	Family    string              `json:"family"`
	Process   string              `json:"process,omitempty"`
	Container *ExposureContainer  `json:"container,omitempty"`
	Classes   []exposure.Verdict  `json:"classes"`
}
type ExposureContainer struct {
	Name          string           `json:"name"`
	Binding       dockerfw.Binding `json:"binding"`
	ContainerPort uint16           `json:"containerPort"`
	Proxied       bool             `json:"proxied"`
}
```

Action names, all version 1: `firewall.docker.enable`, `firewall.docker.disable`, `firewall.docker.repair`, `firewall.docker.sources.set`, `firewall.container.allow`, `firewall.container.deny`, `firewall.container.remove`, `firewall.containers.apply`. Resources: `firewall.docker` (`DockerStatus`), `firewall.exposure` (`ExposureView`). Background task id `firewall.reconcile` (`Every: 5 * time.Minute`, `LockKeys: ["firewall"]`). Constructors, each a method of `fw` returning `core.ActionDef`, `core.ResourceDef` or `core.BackgroundTask`, registered only by Task 46: `dockerEnable`, `dockerDisable` (Task 39), `containerAllow`, `containerDeny`, `containerRemove`, `containersApply` (Task 40), `dockerRepair`, `dockerSourcesSet` (Task 44), `reconcileTask` (Task 45), `dockerResource` (Task 32), `exposureResource` (Task 33).

### Harness (Task 8, used by Tasks 48–50)

In `test/harness/nmap.go` (package `harness`, beside `OpenPorts`, which stays unchanged):

```go
type ScanOpts struct {
	Family     string     // "-4" or "-6"
	UDP        bool       // -sU in place of -sS
	Ports      []uint16   // -p <list>; empty: every port (-p-)
	SourcePort uint16     // --source-port <n>; 0: nmap's choice
	Source     netip.Addr // -S <addr> -e <iface> when valid
	Iface      string     // the scanner interface for -e
	Target     string
}
type PortState struct {
	Port  uint16
	Proto string // tcp | udp
	State string // open | closed | filtered | open|filtered | unfiltered
}
func NmapArgs(o ScanOpts) []string               // -Pn -n, the scan type, -p, timing as checkScan's nmapArgs, -oG -
func ParsePorts(grepable string) ([]PortState, error) // every port entry of -oG output, TCP and UDP
```

In `test/harness/m3_docker_helpers_test.go` (build tag `harness`), declared only by Task 8:

```go
func m3dScan(ctx context.Context, t *testing.T, scanner *vm.VM, o ScanOpts) []PortState
func m3dAddSource(ctx context.Context, t *testing.T, scanner *vm.VM, p netip.Prefix, iface string) // ip addr add, removed in t.Cleanup
func m3dDocker(ctx context.Context, t *testing.T, host *vm.VM, args ...string) string      // sudo docker <args>; fails the test on error
func m3dWaitRunning(ctx context.Context, t *testing.T, host *vm.VM, name string)
func m3dRunTCP(ctx context.Context, t *testing.T, host *vm.VM, name, network string, publish ...string) // the pinned pebble image (TCP 14000)
func m3dRunUDP(ctx context.Context, t *testing.T, host *vm.VM, name, network string, publish ...string) // the pinned pebble-challtestsrv image (UDP 8053)
type m3dRow struct {
	Proto, Addr, Family string
	Port                uint16
	Verdict             string   // the internet class's verdict
	Allowed             []string // its allowed sources when partial
}
func m3dxRows(raw []byte) ([]m3dRow, error)                         // decodes firewall.exposure's JSON (ExposureView)
func m3dxMismatch(rows []m3dRow, scanned []PortState, proto string) []string // spec 2.7's rule: TCP exact; UDP open never scanned closed, closed or filtered never answered
```

Tasks 48–50 add only names that start with `m3dDock`, `m3dTraffic` and `m3dExpo` respectively (and their own `TestM3x…` unit tests), so no two declare one name.

### Web (Tasks 6, 7, 13, 14, 17, 27)

`types.ts` (Task 6) adds `DockerStatus`, `DockerNetwork`, `DockerBinding`, `DockerIssue`, `Exemption`, `DockerSettings`, `ContainerRule`, `Binding`, `ExposureView`, `ExposureRow`, `ExposureVerdict`, `ExposureAllowed`, and `dockerQueryKey(hostId)` = `["resource", hostId, "firewall.docker"]`, `exposureQueryKey(hostId)` = `["resource", hostId, "firewall.exposure"]`; `fixtures.ts` adds `fwDocker(over: Partial<DockerStatus> = {}): DockerStatus`, `fwExposure(over: Partial<ExposureView> = {}): ExposureView` and `fwBinding(over: Partial<DockerBinding> = {}): DockerBinding`, on the pattern of `fwStatus`. Components, each opening its change through `FirewallChange`: `DockerSources({hostId, docker}: {hostId: string; docker: DockerStatus})` (`docker-sources.tsx`, Task 13); `ExposurePage()` (`exposure-page.tsx`, route `exposure` in `routes.tsx`, reading `hostId` from the route params as `SnapshotsPage` does and fetching `exposureQueryKey`, Task 14); `AllowAction` and `DenyAction` (`container-actions.tsx`, props `{container: ContainerRef; onDone: () => void}`, Task 17; they fetch `dockerQueryKey(container.hostId)` for the binding list); `DockerPanel({hostId}: {hostId: string})` (`docker-panel.tsx`, fetching `dockerQueryKey` and rendering `DockerSources`, placed on `FirewallPage` with a link to `exposure`, Task 27). `FirewallChange` (Task 7) keeps its props; when a plan fails with `firewall.manual_required` it shows the detail and a confirmation that plans again with `manual: true` added to the input.

## Assumptions (evidence)

- Wave 1 left no Docker code in the firewall module: `rg -i docker internal/modules/firewall --glob '!*_test.go'` on 2026-10-28 finds only the package doc, `snapshotState.Docker` (`store.go`, never set) and `firewallChain` (`image.go`), which already counts `DOCKER-USER`, `darkpanel-*` and `dp-b-*` as Darkpanel's chains in `restoreKeep`, `verifyKeep`, `confined` and the repair before-image.
- The predictor already handles a block in `after.rules` that declares non-UFW chains: `predict/translate.go` `restoreText` concatenates the framework files and `startFamily` replaces a declared non-built-in chain's rules (flush semantics of `-n`) and keeps undeclared chains (read 2026-10-28). Unverified on real UFW with Docker — Task 1 verifies, and Task 23 compares the prediction with Task 1's `block` captures.
- The evaluator supports `--ctstate DNAT`, `--ctorigdst`, `--ctorigdstport`, nat `PREROUTING` DNAT with rerouting through the Router, Docker's raw-table drop and `-m limit … -j LOG` continuing (`eval/match.go` `ctstate`, `conntrackOption`; `eval/hooks.go` `natTarget`, `reroute`; `eval/eval_test.go` "a rate-limited LOG continues"; read 2026-10-28). It evaluates one packet at a time and gives no deciding rule for an accept, hence Tasks 4, 5 and 10.
- The exec allowlist already permits every vector this plan runs (`internal/platform/exec_rules.go` `ProductionRules`: `{iptables,ip6tables}{,-legacy,-nft}-save -t <table>`, `-restore -n`, the ufw verbs), read 2026-10-28; `iptables -S`/`-L` are not allowed and not used.
- The block's appended `*filter … COMMIT` section loads with UFW's `iptables-restore -n` of `after.rules`, and `CleanupInput`'s `-F`/`-X` lines load with `*-restore -n` on both variants: unverified — Task 1 verifies on all four images (the spec's own wave 2 spike, spec 2.6 step 3).
- Docker 29.9.1 (the harness pin in `test/harness/images.json`) reports `FirewallBackend.Driver` `iptables`, creates `DOCKER-USER` and `DOCKER-FORWARD` in the variant on its `PATH` and loads raw-table protection rules: unverified — Task 1 records `info.json` and the raw table, and Task 11 encodes `RawProtected` from them.
- `core.BackgroundTask{ID, Every, LockKeys, Run, Watch}` and `TaskContext.Apply` exist (`internal/core/background.go`); the agent runs a pass on start, every `Every`, and on a wake, skips a pass while a `LockKeys` entry is held, and applies as `system:<task id>` through `engine.ApplySystem` with `ConfirmNow` (`internal/agent/runtime/background.go`, `internal/core/lifecycle/system.go`; read 2026-10-28). No production module implements `Background` yet; Task 45 is the first.
- There is no generic module settings store (`core.Manifest.Settings` is read nowhere), so the firewall keeps the flag and settings in its own table (Task 18). `firewall_container_rules` does not exist yet.
- The module has no startup hook (`core.Module` is `Manifest()` plus optional `Uninstaller`, `Background`, `Streamer`), so an interrupted owned-chain cleanup outside a change is finished by `finishCleanups`, which the reconcile pass (first pass at agent start) and the next uninstall call; a cleanup inside a change is undone or repeated by the engine's revert of that change.
- Wave 1 has two restore paths: the `firewall.snapshot.restore` action and repair-with-restore go through `fw.run` → `writeRestored` as journaled effects, while revert, the emergency restore, a cut-off restore and uninstall's baseline go through `restoreOver` (`restore.go`). Both need the Docker hooks (Tasks 28, 38, 42).
- `module.Uninstall` returns before any work for `rules=keep` and when `/usr/sbin/ufw` is missing (`uninstall.go`, read 2026-10-28); spec 2.9 needs the cleanup in every case (Task 37).
- No page renders the container action slot yet (`web/src/modules/docker/routes.tsx` returns null); Task 17's components are tested on their own, and wave 3a renders them.
- The harness VMs install Docker CE 29.9.1 and the Compose plugin and pull the pinned `pebble` and `pebble-challtestsrv` images (`test/harness/images.json`, `provision.go`); those two serve TCP (Pebble's 14000) and UDP (challtestsrv's DNS on 8053), so the Docker harness needs no new image or `pins.go` change. `checkScan` scans TCP SYN only and has no fixed source port (`test/harness/m1_test.go`), hence Task 8.
- The `vm` job of `.github/workflows/harness.yml` has `timeout-minutes: 200` and an M3 firewall step with `-timeout 70m`; Task 50 adds the Docker step and raises the job's limit if the measured total needs it.

## Task index

1. Capture Docker and UFW on the four images
2. dockerfw: settings, private sources and forward exemptions
3. loaded: read one iptables variant on demand
4. eval: name the deciding rule of every verdict
5. exposure: source partitions
6. web: Docker and exposure types and fixtures
7. web: confirm manual rules in the firewall change dialog
8. harness: UDP and source-port scans and Docker helpers
9. dockerfw: bindings and container rules
10. exposure: per-class verdicts
11. dockerfw: detection and foreign forwards
12. Docker client seam and fakes
13. web: private sources and forward exemptions editor
14. web: exposure page
15. dockerfw: render the block and its markers
16. Predict block files, rows and the cleanup
17. web: container Allow and Deny actions
18. Docker store and migration
19. dockerfw: realize container rules against facts
20. dockerfw: integration health
21. dockerfw: DOCKER-USER and cleanup restore inputs
22. dockerfw: UFW rule operations
23. dockerfw: prove the block's decisions on the captures
24. Journal the Docker logical state as effects
25. Inverses of the Docker effects
26. Snapshots record the Docker logical state
27. web: Docker integration panel
28. Whole restore brings back the Docker rows and settings
29. Gather Docker state
30. Plan diff lines for Docker changes
31. Refuse foreign DOCKER-USER rules and an open incoming policy
32. firewall.docker resource
33. firewall.exposure resource
34. Preserve foreign DOCKER-USER rules around a transition
35. Owned-chain cleanup
36. Run Docker changes in order
37. Uninstall runs the cleanup in every case
38. Revert and restore keep foreign DOCKER-USER rules
39. firewall.docker.enable and firewall.docker.disable
40. Container allow, deny, remove and apply actions
41. state.enable, state.disable and defaults.set with the integration
42. snapshot.restore with the integration
43. state.repair with the integration
44. firewall.docker.repair and firewall.docker.sources.set
45. Background task firewall.reconcile
46. Register the Docker actions, resources and task
47. End-to-end Docker change through the engine
48. harness: Docker integration lifecycle and refusals
49. harness: Docker traffic with the integration on
50. harness: exposure against the scan and the workflow step

---
