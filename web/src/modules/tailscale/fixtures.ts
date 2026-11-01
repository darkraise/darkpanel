// Test fixtures: one picture of a tailscale node shared by the tailscale pages' tests.
import type { PrefsFields, TailscaleLatency, TailscalePeer, TailscaleStatus } from "./types"

const DNS = "tail1234.ts.net"

function prefs(): PrefsFields {
  return { wantRunning: true, advertiseRoutes: [], exitNode: "", advertiseExitNode: false, acceptRoutes: false, shieldsUp: false, runSSH: false }
}

type PeerSpec = Partial<TailscalePeer> & Pick<TailscalePeer, "id" | "hostName" | "os" | "addresses" | "connection" | "exitNodeOption" | "lastHandshake">

function peer(spec: PeerSpec): TailscalePeer {
  return { dnsName: `${spec.hostName}.${DNS}`, tags: [], online: true, endpoint: "", region: "", exitNode: false, ...spec }
}

export const statusRunning: TailscaleStatus = {
  version: "1.104.1",
  backendState: "Running",
  self: { id: "n1", hostName: "web-1", dnsName: `web-1.${DNS}`, os: "linux", addresses: ["100.64.0.5", "fd7a:115c:a1e0::5"], tags: ["tag:server"] },
  tailnet: "example.com",
  magicDNSSuffix: DNS,
  preferredDerp: "fra",
  runSSH: false,
  prefs: prefs(),
  peers: [
    peer({ id: "n2", hostName: "db-1", os: "linux", addresses: ["100.64.0.7"], tags: ["tag:server"], connection: "derp", region: "fra", exitNodeOption: false, lastHandshake: "2026-10-30T11:59:00Z" }),
    peer({ id: "n3", hostName: "laptop", os: "macOS", addresses: ["100.64.0.8"], connection: "direct", endpoint: "203.0.113.7:41641", exitNodeOption: true, lastHandshake: "2026-10-30T11:59:30Z" }),
    peer({ id: "n4", hostName: "nas", os: "linux", addresses: ["100.64.0.9"], connection: "peer-relay", exitNodeOption: false, lastHandshake: "2026-10-30T11:58:00Z" }),
    peer({ id: "n5", hostName: "old-box", os: "linux", addresses: ["100.64.0.10"], online: false, connection: "none", exitNodeOption: false, lastHandshake: null }),
  ],
  health: [],
}

export const statusLoggedOut: TailscaleStatus = {
  version: "1.104.1",
  backendState: "NeedsLogin",
  self: null,
  tailnet: "",
  magicDNSSuffix: "",
  preferredDerp: "",
  runSSH: false,
  prefs: prefs(),
  peers: [],
  health: ["You are logged out."],
}

export const latencyFixture: TailscaleLatency = {
  regions: [
    { regionId: 1, code: "nyc", name: "New York City", stun: { available: true, medianMs: 18.4, reason: "" }, https: { available: true, medianMs: 21.7, reason: "" } },
    { regionId: 4, code: "fra", name: "Frankfurt", stun: { available: true, medianMs: 9.2, reason: "" }, https: { available: true, medianMs: 12.5, reason: "" } },
    { regionId: 17, code: "syd", name: "Sydney", stun: { available: false, medianMs: 0, reason: "no STUN endpoint" }, https: { available: true, medianMs: 231.8, reason: "" } },
  ],
}
