import { expect, test } from "vitest"
import { probeFixture, serversFixture } from "./fixtures"
import {
  DERP_ACTION_VERSION,
  DERP_MODULE,
  derpKey,
  isDerpServers,
  isServerProbe,
  type DerpServers,
  type NodeUse,
  type ProbeResult,
  type ProbeSource,
  type RegionDiagnostics,
  type ServerProbe,
  type ServerStatus,
  type Visibility,
} from "./types"

type Json = Record<string, unknown>

function tyClone<T>(v: T): Json {
  return structuredClone(v) as Json
}

test("the module id and the action version are the derp module's", () => {
  expect(DERP_MODULE).toBe("derp")
  expect(DERP_ACTION_VERSION).toBe(1)
})

test("derpKey names one group of queries, a probe by its host", () => {
  expect(derpKey("servers")).toEqual(["derp", "servers"])
  expect(derpKey("probe")).toEqual(["derp", "probe"])
  expect(derpKey("probe", "01JD0000000000000000000011")).toEqual(["derp", "probe", "01JD0000000000000000000011"])
})

test("isDerpServers accepts the controller's JSON", () => {
  expect(isDerpServers(JSON.parse(JSON.stringify(serversFixture)))).toBe(true)
  expect(isDerpServers({ servers: [], availableVersion: "v1.104.1" })).toBe(true)
})

test("isDerpServers refuses what is not a complete answer", () => {
  const server = (over: Json) => ({ ...tyClone(serversFixture.servers[0]), ...over })
  const answer = (servers: unknown[]) => ({ servers, availableVersion: "v1.104.1" })
  const rows: [string, unknown][] = [
    ["null", null],
    ["a string", "servers"],
    ["an array", []],
    ["an empty object", {}],
    ["servers that are null", { servers: null, availableVersion: "v1.104.1" }],
    ["a missing availableVersion", { servers: [] }],
    ["a server that is a string", answer(["derp-1"])],
    ["a server without a hostId", answer([server({ hostId: undefined })])],
    ["a regionId that is a string", answer([server({ regionId: "900" })])],
    ["an unknown health", answer([server({ health: "fine" })])],
    ["a certNotAfter that is a number", answer([server({ certNotAfter: 5 })])],
    ["reasons that are not strings", answer([server({ reasons: [1] })])],
    ["a unitActive that is a string", answer([server({ unitActive: "yes" })])],
    ["a missing error", answer([server({ error: undefined })])],
  ]
  for (const [name, value] of rows) expect(isDerpServers(value), name).toBe(false)
})

test("isServerProbe accepts the controller's JSON, a source without region diagnostics and an uninstalled status", () => {
  expect(isServerProbe(JSON.parse(JSON.stringify(probeFixture)))).toBe(true)
  const none = { ...tyClone(probeFixture.server), installed: false, mode: "", hostName: "", certName: "", certNotAfter: null, visible: [] }
  expect(isServerProbe({ ...tyClone(probeFixture), server: none })).toBe(true)
  expect(probeFixture.sources[0]?.region).toBeNull()
})

test("isServerProbe refuses what is not a complete answer", () => {
  const source = tyClone(probeFixture.sources[1])
  const node = tyClone(probeFixture.nodes[0])
  const withSources = (sources: unknown) => ({ ...tyClone(probeFixture), sources })
  const rows: [string, unknown][] = [
    ["null", null],
    ["an empty object", {}],
    ["a hostId that is a number", { ...tyClone(probeFixture), hostId: 1 }],
    ["a missing regionCode", { ...tyClone(probeFixture), regionCode: undefined }],
    ["a server that is a string", { ...tyClone(probeFixture), server: "derp-1" }],
    ["a server visibility without an address", { ...tyClone(probeFixture), server: { ...tyClone(probeFixture.server), visible: [{ visible: true, node: "" }] } }],
    ["a server with tags that are null", { ...tyClone(probeFixture), server: { ...tyClone(probeFixture.server), tags: null } }],
    ["sources that are null", withSources(null)],
    ["a source without tls", withSources([{ ...source, tls: undefined }])],
    ["a tls latencyMs that is a string", withSources([{ ...source, tls: { ok: true, latencyMs: "9", error: "" } }])],
    ["a region without errors", withSources([{ ...source, region: { info: [], warnings: [] } }])],
    ["a region that is an array", withSources([{ ...source, region: [] }])],
    ["nodes that are null", { ...tyClone(probeFixture), nodes: null }],
    ["a node with observed as a string", { ...tyClone(probeFixture), nodes: [{ ...node, observed: "yes" }] }],
    ["a ready that is a string", { ...tyClone(probeFixture), ready: "yes" }],
    ["warnings that are not strings", { ...tyClone(probeFixture), warnings: [1] }],
  ]
  for (const [name, value] of rows) expect(isServerProbe(value), name).toBe(false)
})

// Vitest strips types, so this test is checked by `pnpm typecheck`: a
// missing, misspelled or mistyped member of the controller's JSON fails the
// compilation of this file.
test("the wire types carry every member the controller sends", () => {
  const result: ProbeResult = { ok: false, latencyMs: 0, error: "connection refused" }
  const region: RegionDiagnostics = { info: ["a"], warnings: ["b"], errors: ["c"] }
  const source: ProbeSource = { source: "controller", name: "controller", tls: result, stun: result, region, error: "" }
  const seen: Visibility = { address: "100.64.0.1", visible: false, node: "" }
  const status: ServerStatus = {
    installed: true, mode: "letsencrypt", hostName: "derp.example.com", verifyClients: false, derperVersion: "v1.104.1", unitActive: false,
    certName: "", certNotAfter: null, availableVersion: "v1.104.1", linkViaTailnet: true, onTailnet: false, tags: [], addresses: [], visible: [seen],
  }
  const node: NodeUse = { name: "n", address: "100.64.0.1", hostId: "", visible: false, preferred: "", observed: false }
  const probe: ServerProbe = { hostId: "h", regionId: 0, regionCode: "", server: status, sources: [source], nodes: [node], ready: false, warnings: ["w"] }
  const servers: DerpServers = {
    servers: [{
      hostId: "h", host: "derp-1", mode: "ip", hostName: "203.0.113.7", regionId: 900, regionCode: "mydc", derperVersion: "v1.104.1",
      certName: "sha256-raw:00", certNotAfter: "2027-10-20T09:00:00Z", unitActive: true, health: "broken", reasons: ["r"], checkedAt: "2026-11-01T08:00:00Z", error: "",
    }],
    availableVersion: "v1.104.1",
  }
  expect(JSON.parse(JSON.stringify(probe)) as ServerProbe).toEqual(probe)
  expect(JSON.parse(JSON.stringify(servers)) as DerpServers).toEqual(servers)
})
