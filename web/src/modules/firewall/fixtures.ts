import type { FirewallStatus, Rule, SnapshotInfo } from "./types"

// Test fixtures of the firewall pages: a status, rules and snapshots with
// the fields each test overrides.

export function fwRule(over: Partial<Rule> = {}): Rule {
  return { id: "01JD0000000000000000000001", action: "allow", direction: "in", proto: "tcp", from: "any", to: "any", toPort: "443", family: "both", origin: "darkpanel", comment: "web", ...over }
}

export function fwStatus(over: Partial<FirewallStatus> = {}): FirewallStatus {
  return {
    configured: { enabled: true, logLevel: "low", ipv6: true, defaults: { incoming: "DROP", outgoing: "ACCEPT", routed: "DROP" } },
    loaded: {
      variant: "nft",
      ipv6: true,
      sysctl: { "net/ipv4/ip_forward": "0", "net/ipv4/conf/all/rp_filter": "2" },
      chains: [
        { family: "v4", table: "filter", chain: "INPUT", policy: "DROP", rules: 3 },
        { family: "v4", table: "filter", chain: "ufw-user-input", rules: 2 },
      ],
    },
    rules: [
      fwRule(),
      fwRule({ id: "manual-0123456789abcdef", toPort: "22", origin: "manual", comment: "" }),
      fwRule({ id: "manual-fedcba9876543210", proto: "any", toPort: undefined, origin: "manual", comment: "", raw: "allow any 22 0.0.0.0/0 any 0.0.0.0/0 in_eth0 -m owner" }),
    ],
    active: true,
    inSync: true,
    outOfSync: [],
    supported: true,
    unsupported: [],
    ...over,
  }
}

export function fwSnapshot(over: Partial<SnapshotInfo> = {}): SnapshotInfo {
  return { id: "01JD00000000000000000000S1", createdAt: "2026-10-24T08:00:00Z", changeId: "c1", actor: "user:u1", system: false, pinned: false, baseline: false, enabled: true, ...over }
}
