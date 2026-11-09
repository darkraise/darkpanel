# Wave 11b fact sheet: lockout guard rows

Read-only investigation of `main` at 735d16a on 2026-11-08, for the wave 11b plan (security-review rows 27, 28, 46, 47, 48, 51, 53). Line numbers are from that commit; re-check them before citing.

## Owner decisions and planning rulings (2026-11-08)

- Owner: fix every open row before v1.0 (no deferral).
- Owner, row 27: a flow to a wildcard bind is checked on every non-link-local address of its family on every up, non-loopback interface. This replaces the W10b-R12 sentence "checked on every address of its family on the interface its route back leaves through" (spec around line 5450) and the same sentence in `docs/security/threat-model.md` (around line 229).
- Ruling, row 48 cost: fail closed. After `eval.InternetCells` in `cellFlows`, the flow is unknown when `len(cells) > 65536` or `len(cells) * (rules in the family's ruleset + 1) > 10_000_000`. Refusal text: `the rules and routes split the internet into %d ranges for this flow; checking them against %d rules is more than the guard evaluates (limit %d)`. Consequence to document: a host with more than about 1,400 bans that can match the flow's port, or a full BGP table, refuses guarded changes until overridden.
- Ruling, row 53 IPv6 leg: fix on the panel side. The NPM exposure records `[::]:443` as a public binding when Docker publishes IPv6 (`internal/modules/panel/listener.go` around 168-171), keeping W10b-R12's "bindings come from the exposure's own record".
- Ruling, row 53 policy routing: read `ip rule` (`RTM_GETRULE`, `unix.FRA_*`) in a new `internal/platform/netstat/rules.go`, and turn stand-in skipping off whenever a rule selects by a field the lookups cannot carry (destination port, or uid on forwarded replies). This also settles row 28's fail2ban-plus-MARK item: marks matter only when a rule has `FRA_FWMARK`.
- Ruling, row 53 special-purpose ranges: no code change; the threat model lists the full `holes4` set (`eval/cells.go` 21-36: TEST-NET-1/2/3, `2001:db8::/32`, private, CGNAT, loopback, link-local, multicast).

## Per item

- **27 wildcard destinations.** `wildcardDsts(pref, iface)` (`guard/route.go` 206-221) becomes `wildcardDsts(pref)`: `pref` first, then every address of its family on every `Up && !Loopback` interface (the filter `eligible` uses), skipping `IsLinkLocalUnicast()` in both families. Keep the route-back lookup and its failure paths (110-117). Also closes row 28's multipath and IPv4 link-local items. Hazard: bridge and tailscale0 addresses join the set; a hand-written allowance naming only uplinks refuses every revert. Tests: `route_test.go` `TestProbes` rows at 135 (2 probes become 4) and 193 (needs the mark query route); rewrite `TestV1r21WildcardDestinationsSkipIPv6LinkLocal` (`internet_cells_test.go` 517) as a table; add a dual-uplink test on the `v1r21TwoAddrs` pattern (379-410) expecting a refusal containing `to 10.1.0.10:443`.
- **28.2 interrupted dump.** `netstat.Routes` retries up to 3 attempts on `errDumpInterrupted` (`netstat/routes.go` 145, 442) through a testable `retryDump(dump func() ([][]byte, error)) ([][]byte, error)`.
- **28.5 `noReplyFrom` (route.go 186-199).** Untested branches: a non-`ErrNoRoute` error returns false; no local address of the family returns false. Table test with `newRouter(&gHost{routeErrs: …}, facts{ifaces: gIfaces()}, fwstate.Loaded{})`.
- **28.6 EINVAL rule (route.go 130-132).** `TestV1r21ThrowCellWhoseLookupFailsIsUnknown` (808) uses a plain error; add a `unix.EINVAL`-wrapped case in a throw cell expecting "cannot be resolved".
- **28.7 `flowSet.add` merge (flows.go 82-95).** Adjacent bug: a later `fwdReply` replaces an earlier one, which can skip more cells. Make it `fwdReplies []netip.Addr`, checked each at route.go 152-154.
- **28.8 mark scan (route.go 169).** Scan `raw` as well as `mangle`; not filter or nat (Tailscale's `ts-forward` MARK in filter would mark every flow).
- **28.9 mixed families.** In `noReply`, return false before any lookup when `fl.fwdReply` and the source differ in family.
- **28.10 local routes in main.** Add `"local"` to `cutRouteTypes` (cells.go 128), keeping the `RT_TABLE_LOCAL` exclusion (143). Test with `v1r21Fib` and a `local 45.0.0.0/8 table main` route, expecting `45.0.0.0-45.255.255.255`.
- **53 EINVAL in blackhole cells.** Adjacent bug: `cf.blackhole = within(routes.blackholes)` (cells.go 71) is containment, not longest match; a unicast /24 inside a blackhole /16 is marked blackhole. Mark blackhole (and `noPath`, 65) only when no more specific non-blackhole route contains the cell. Row in `TestV1r21SourceCellsOfRoutesWithoutAPathAreSkipped` (650).
- **53 selectors.** `RouteQuery` (`netstat/routes.go` 37-43) gains `Proto uint8`, `SrcPort uint16`, `DstPort uint16`, `UID uint32`, `HasUID bool`; `routeRequest` (114-136) encodes `RTA_IP_PROTO` (1 byte, TCP/UDP only), `RTA_SPORT`/`RTA_DPORT` (big-endian u16), `RTA_UID` (native u32); constants in `x/sys` v0.49.0. Reply lookups at route.go 146, 153, 193 set protocol and source port (`fl.pkt.DstPort`). Extend `TestRouteRequest` (`routes_test.go` 276-305).
- **46 panel binding end to end.** `exposure_test.go` has 42 lines; add a test with `v1r19Path()` plus `Public: 0.0.0.0:443`, host `v1r21Fib{&gHost{ifaces: ckIfaces(), table: v1r21HostRoutes()}}` through `v1r21CheckHost` (`internet_cells_test.go` 430), a ruleset dropping forwarded 443 to 172.30.0.2, expecting `exposure exp1 public from`; control case without the drop passes.
- **47 eval.** Shared `origDstPrefix(s string) (netip.Prefix, bool)` in `eval/match.go`, used by `conntrackOption` (193-200) and replacing `parsePrefixOrAddr` (`eval/cells.go` 109-117). Rows for `TestV1r20InternetCells` (`cells_test.go` 52): `::ffff:8.8.8.0/120` in an IPv6 call, `0.0.0.0/9`, a `ctorigdst` with no value. `TestOrigDstPrefix` in `match_test.go`.
- **48.1 wild source to unspecified destination.** Already covered (`TestV1r21WildcardBindIsCheckedOnEveryAddress` 388, `TestV1r21ThrowCellTheProxyAnswersIsChecked` 829, `TestV1r21ThrowCellWithoutARouteBackHasNothingToProtect` 750); close with pointers.
- **48.3 unmodelled rewrites.** In `cutters` (cells.go 206), keep every rule when any table holds a target outside {"", ACCEPT, DROP, REJECT, RETURN, LOG, TCPMSS, MARK, CONNMARK, DNAT, SNAT, MASQUERADE, chain jumps}. Test: nat `REDIRECT --to-ports 8443` plus a filter ban on 8443 keeps the ban.
- **48.4 skipped probes.** `checkFlow` (cells.go 168-193) returns a skipped count; `verdict` (`guard.go` 104) prints `every protected flow passes (%d checked, %d already blocked before the change)`. Rows in `TestVerdict` (`guard_test.go` 422).
- **48.5 before-router.** Evaluate the before-state with `&router{host: r.host, f: r.f, st: *before, local: r.local, routes: r.routes}` so `forwardingOff` reads the before-state's sysctl values.
- **51 harness.** Pass journalctl `--since @<agent seconds − 2>` from the agent's `date +%s%N` (already captured around `m3_docker_traffic_test.go` 575) through a pure `m3dxJournalSince(agentNs string) (string, error)` with a `Unit` test; parse the log once in `m3dxTrafficLeaks` (95-127), pinned by `TestM3xTrafficLeaks` and the `TestV1r24Unit…` tests.

## Suggested task order

1 netstat (retry, selectors) · 2 eval (`origDstPrefix`, rows) · 3 wildcard destinations, spec and threat-model sentence · 4 reply lookups (mixed family, raw marks, `fwdReplies`, tests) · 5 cells (cap, local routes, rewrites, blackhole longest match) · 6 before-router and skipped count · 7 `ip rule` dump and fail-closed stand-in skips · 8 panel IPv6 public binding · 9 row 46 test · 10 harness · 11 threat model.

## Plan design settled on 2026-11-09 (main at 091547c; the plan file is not written yet)

Owner ticked `agy` for this plan. Execution is inline, `claude --model opus --effort high`. Header lines follow `2026-11-08-v1-w11a-live-apply.md` with `TMPDIR=/root/.cache/w11b-tmp/<task>`; `**Task check:** none — full suite every 3 tasks`; branch `v1-w11b-lockout-guard` in the base repository. Gate 7 applies. Write `> **External executors:** agy`.

Changes to the fact sheet above, each with its reason:

- **IPv6 public leg.** `ListenerSpec.PublicBinding` holds one value and `firewall.DockerStatus` bindings carry no family, so the controller cannot tell "when Docker publishes IPv6". The panel records `[::]:443` for every NPM exposure (`listener.go`, and `lsSpec` in `listener_test.go`). `viaFlows` reads an unspecified IPv6 binding as dual-stack: the IPv4 flow always, and the IPv6 flow only when the host has an IPv6 address that is neither loopback nor link-local. That IPv6 flow has `standIn: true`, so a host without IPv6 routes skips its cells instead of refusing every change. No wire type or schema changes.
- **Row 28.9 mixed families.** `viaFlows` attaches the forwarded reply only to a flow of the proxy address's family, instead of returning false in `noReply`. Returning false would make every no-route IPv6 cell unknown.
- **`RouteQuery`.** Add `Proto uint8`, `SrcPort uint16`, `DstPort uint16`, encoded only when non-zero. No `UID`/`HasUID`: no lookup can know it, so any rule with a uid range turns stand-in skipping off, for local replies too.
- **Lookups carry selectors only when a policy rule of the family selects by protocol or port**, so hosts without such rules send the same queries as today and `rtQuery` test keys stay valid. Apply to reply lookups (`Proto`, `SrcPort` = the flow's `DstPort`) and to the output and forward lookups in `probes` and `route` (`Proto`, `DstPort`, `SrcPort` unless `unknownPort`).
- **Policy rule prefixes also cut cells.** `FRA_SRC` and `FRA_DST` prefixes join `routeCuts`, except a single-address prefix that is one of the host's own addresses (it would turn the flow into input). Unreadable rules set `routeCut.why` to `the policy rules cannot be read: %v`.
- **Stand-in off** (`noReply`) when a rule has a destination port or uid selector, or when a rule selects by mark (`FRA_FWMARK` or `FRA_FWMASK`) and `raw` or `mangle` holds a MARK/CONNMARK rule that may match. `netstat.Rule`: `Family, Priority, Table, Action uint8, Src, Dst netip.Prefix, Mark bool, Proto uint8, SrcPort, DstPort, UID bool`. `Rules(family)` dumps with `RTM_GETRULE`, `rtMsg(af,0,0,0)` and `retryDump`; `guard/routes.go` adds an optional `ruleDumper` and `LiveHost.Rules`; `gHost` gains `rules`.
- **Cell limit consequence.** About 2,200 port-matching bans reach the limit (2n cells times n rules), not 1,400. Helper `overCellLimit(cells, rules int) (limit int, over bool)`; print 65536 as the limit when the cell cap is the one hit.
- **Longest match.** `routeCutters` returns `routeCut` with a `paths` list (unicast and local routes outside the local table). A cell is no-path only when no longer path route contains it; blackhole only when no longer non-blackhole route contains it.
- **Row 48.5.** Add `func (r *router) forState(st fwstate.Loaded) *router` (a copy sharing the caches), so later caches need no edit.
- **Row 27 hazard is narrower than stated.** `revert.go` reaches `d.lockout` only after a revert with blocked effects, conflicts or a write error, so it affects troubled reverts, not every revert.

Trial on a scratch copy (2026-11-09): the row 27 change makes exactly four existing tests stale. `TestProbes` rows "an unspecified destination…" (4 probes) and "every lookup of an incoming flow…" (needs `gxQueryMark(rtQuery("203.0.113.7","10.0.0.1",""),0x10)`); `TestGxCheckControllerListeners` (adds the two `10.0.0.1:7443` lines, 4 results); `TestV1r21ThrowCellWithoutARouteBack…` (its rule becomes `-A INPUT -s 45.33.0.0/16 -d 10.0.0.1/32 -p tcp -j DROP`). Also verified: `netfilter.Parse` gives `Target.Name` REDIRECT and raw MARK; a route error in `reroute` gives `Unknown`, so the before-router test expects a refusal.

Tasks, with scores (files-spec-coupling-risk):

1. netstat lookup selectors and `retryDump` (1-2-2-1 = 6).
2. netstat `Rules` dump, `rules.go`; its live test asserts the main rule carries no selectors (1-2-2-0 = 5).
3. eval `origDstPrefix` and rows (1-2-0-1 = 4, agy gemini-4-argon low).
4. wildcard destinations and the four stale tests (1-2-0-3 = 6).
5. `fwdReplies`, `noReplyFrom` and EINVAL tests (2-2-0-2 = 6).
6. cells: cap, local routes, rewrites, longest match (1-2-1-3 = 7).
7. before-router and skipped count (2-2-0-2 = 6).
8. policy rules in the guard, raw mark scan, `TestV1r21ThrowCellWithMarkedRepliesIsUnknown` rewritten with a `policy` field on `v1r21Throw` (2-2-1-3 = 8).
9. dual-stack binding in `viaFlows`, panel record, row 46 test (2-2-1-3 = 8).
10. harness journal since and single parse (1-2-0-1 = 4, agy gemini-4-argon low).
11. spec rulings W11-R7 onward, the W10b-R12 sentence, threat-model bullets including removing the open-rows bullet, docs test (1-1-0-0 = 2, agy gemini-3.8-flash medium).

Risk-3 tasks carry `**Lenses:** security`. Register rows 27, 28, 46, 47, 48, 51, 53 of the security review are still `open`: set them `planned` when the plan is written.

The 20 wave 11a minor rows (v1-completion 447-466) are in no plan of W11-R1; wave 11c or a follow-up must take them.
