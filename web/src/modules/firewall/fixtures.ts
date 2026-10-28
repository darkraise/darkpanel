import type { DockerBinding, DockerStatus, ExposureView, FirewallStatus, Rule, SnapshotInfo } from "./types"

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

export function fwBinding(over: Partial<DockerBinding> = {}): DockerBinding {
  return { binding: { selector: "web", hostPort: 8080, proto: "tcp" }, id: "a1b2c3d4e5f6", containers: ["web"], containerPort: 80, allowed: ["any"], denied: [], ...over }
}

export function fwDocker(over: Partial<DockerStatus> = {}): DockerStatus {
  return {
    available: true,
    version: "29.9.1",
    driver: "iptables",
    dockerVariant: "nft",
    ufwVariant: "nft",
    rawProtection: true,
    supported: true,
    enabled: true,
    health: "ok",
    networks: [{ name: "bridge", id: "0123456789abcdef", bridge: "docker0", subnets: ["172.17.0.0/16"], protected: true }],
    settings: { privateSources: ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"], forwardExemptions: [] },
    rules: [{ id: "01JD000000000000000000R001", binding: { selector: "web", hostPort: 8080, proto: "tcp" }, kind: "allow" }],
    bindings: [fwBinding()],
    ...over,
  }
}

export function fwExposure(over: Partial<ExposureView> = {}): ExposureView {
  return {
    rows: [
      {
        proto: "tcp",
        addr: "0.0.0.0",
        port: 22,
        family: "v4",
        process: "sshd",
        classes: [
          { class: "internet", verdict: "open", decidedBy: "filter/ufw-user-input#1" },
          { class: "private", range: "10.0.0.0/8", verdict: "open", decidedBy: "filter/ufw-user-input#1" },
        ],
      },
      {
        proto: "tcp",
        addr: "0.0.0.0",
        port: 8080,
        family: "v4",
        container: { name: "web", binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containerPort: 80, proxied: false },
        classes: [{ class: "internet", verdict: "partial", allowed: [{ sources: ["198.51.100.0/24"], decidedBy: "filter/darkpanel-docker-deny#1" }] }],
      },
    ],
    ...over,
  }
}
