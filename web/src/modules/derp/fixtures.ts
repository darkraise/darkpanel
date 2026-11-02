// Test fixtures: one picture of the DERP servers and the probe of the first one, shared by the derp pages' tests.
import type { DerpServers, ServerProbe } from "./types"

const PIN = "sha256-raw:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

export const serversFixture: DerpServers = {
  servers: [
    {
      hostId: "01JD0000000000000000000011",
      host: "derp-1",
      mode: "ip",
      hostName: "203.0.113.7",
      regionId: 900,
      regionCode: "mydc",
      derperVersion: "v1.104.1",
      certName: PIN,
      certNotAfter: "2027-10-20T09:00:00Z",
      unitActive: true,
      health: "ok",
      reasons: [],
      checkedAt: "2026-11-01T08:00:00Z",
      error: "",
    },
    {
      hostId: "01JD0000000000000000000012",
      host: "derp-2",
      mode: "letsencrypt",
      hostName: "derp.example.com",
      regionId: 0,
      regionCode: "",
      derperVersion: "v1.102.0",
      certName: "",
      certNotAfter: null,
      unitActive: true,
      health: "degraded",
      reasons: ["the last check of this host failed: agent unreachable"],
      checkedAt: "2026-11-01T07:50:00Z",
      error: "agent unreachable",
    },
  ],
  availableVersion: "v1.104.1",
}

export const probeFixture: ServerProbe = {
  hostId: "01JD0000000000000000000011",
  regionId: 900,
  regionCode: "mydc",
  server: {
    installed: true,
    mode: "ip",
    hostName: "203.0.113.7",
    verifyClients: true,
    derperVersion: "v1.104.1",
    unitActive: true,
    certName: PIN,
    certNotAfter: "2027-10-20T09:00:00Z",
    availableVersion: "v1.104.1",
    linkViaTailnet: false,
    onTailnet: true,
    tags: ["tag:derp"],
    addresses: ["100.64.0.20"],
    visible: [
      { address: "100.64.0.1", visible: true, node: "web-1.tail1234.ts.net" },
      { address: "100.64.0.8", visible: false, node: "" },
    ],
  },
  sources: [
    {
      source: "controller",
      name: "controller",
      tls: { ok: true, latencyMs: 12.5, error: "" },
      stun: { ok: true, latencyMs: 11.2, error: "" },
      region: null,
      error: "",
    },
    {
      source: "01JD0000000000000000000013",
      name: "web-1",
      tls: { ok: true, latencyMs: 20.1, error: "" },
      stun: { ok: true, latencyMs: 19.4, error: "" },
      region: { info: ["DERP region 900 answered"], warnings: [], errors: [] },
      error: "",
    },
  ],
  nodes: [
    { name: "web-1.tail1234.ts.net", address: "100.64.0.1", hostId: "01JD0000000000000000000013", visible: true, preferred: "mydc", observed: true },
    { name: "laptop.tail1234.ts.net", address: "100.64.0.8", hostId: "", visible: false, preferred: "fra", observed: false },
  ],
  ready: true,
  warnings: [],
}
