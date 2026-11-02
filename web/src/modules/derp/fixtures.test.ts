import { expect, test } from "vitest"
import { probeFixture, serversFixture } from "./fixtures"
import { isDerpServers, isServerProbe } from "./types"

const FX_PIN = "sha256-raw:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

test("serversFixture lists an ip-mode server in region 900 that is ok and a letsencrypt server without a region that is degraded", () => {
  expect(isDerpServers(serversFixture)).toBe(true)
  expect(serversFixture).toEqual({
    servers: [
      {
        hostId: "01JD0000000000000000000011",
        host: "derp-1",
        mode: "ip",
        hostName: "203.0.113.7",
        regionId: 900,
        regionCode: "mydc",
        derperVersion: "v1.104.1",
        certName: FX_PIN,
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
  })
})

test("probeFixture is the probe of the first server: the controller and one agent are OK, one node is visible and observed, one is not visible", () => {
  expect(isServerProbe(probeFixture)).toBe(true)
  expect(probeFixture.hostId).toBe(serversFixture.servers[0]?.hostId)
  expect([probeFixture.regionId, probeFixture.regionCode, probeFixture.ready, probeFixture.warnings]).toEqual([900, "mydc", true, []])
  expect(probeFixture.server).toEqual({
    installed: true,
    mode: "ip",
    hostName: "203.0.113.7",
    verifyClients: true,
    derperVersion: "v1.104.1",
    unitActive: true,
    certName: FX_PIN,
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
  })
  expect(probeFixture.sources).toEqual([
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
  ])
  expect(probeFixture.nodes).toEqual([
    { name: "web-1.tail1234.ts.net", address: "100.64.0.1", hostId: "01JD0000000000000000000013", visible: true, preferred: "mydc", observed: true },
    { name: "laptop.tail1234.ts.net", address: "100.64.0.8", hostId: "", visible: false, preferred: "fra", observed: false },
  ])
})

test("the nodes of the probe agree with the server's visibility answers", () => {
  const visible = new Map(probeFixture.server.visible.map((v) => [v.address, v.visible]))
  for (const n of probeFixture.nodes) expect(n.visible, n.name).toBe(visible.get(n.address))
})

test("the fixtures survive a JSON round trip unchanged", () => {
  for (const v of [serversFixture, probeFixture]) expect(JSON.parse(JSON.stringify(v))).toEqual(v)
})
