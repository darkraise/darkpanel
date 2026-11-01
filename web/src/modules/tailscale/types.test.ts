import { expect, test } from "vitest"
import { latencyFixture, statusLoggedOut, statusRunning } from "./fixtures"
import {
  TAILSCALE_ACTION_VERSION,
  TAILSCALE_MODULE,
  isTailscaleLatency,
  isTailscaleStatus,
  type Measurement,
  type PrefsFields,
  type RegionLatency,
  type TailscaleNode,
  type TailscalePeer,
} from "./types"

type Json = Record<string, unknown>

function typesClone<T>(v: T): Json {
  return structuredClone(v) as Json
}

test("the module id and the action version are the tailscale module's", () => {
  expect(TAILSCALE_MODULE).toBe("tailscale")
  expect(TAILSCALE_ACTION_VERSION).toBe(1)
})

test("isTailscaleStatus accepts the agent's JSON for a running and a logged-out node", () => {
  expect(isTailscaleStatus(JSON.parse(JSON.stringify(statusRunning)))).toBe(true)
  expect(isTailscaleStatus(JSON.parse(JSON.stringify(statusLoggedOut)))).toBe(true)
  expect(isTailscaleStatus({ ...typesClone(statusRunning), prefs: undefined })).toBe(true)
})

test("isTailscaleStatus refuses what is not a complete status", () => {
  const peer = (over: Json) => ({ ...typesClone(statusRunning.peers[0]), ...over })
  const rows: [string, unknown][] = [
    ["null", null],
    ["a string", "Running"],
    ["an array", []],
    ["an empty object", {}],
    ["a missing version", { ...typesClone(statusRunning), version: undefined }],
    ["a backend state that is not a string", { ...typesClone(statusRunning), backendState: 3 }],
    ["a self that is not a node", { ...typesClone(statusRunning), self: "web-1" }],
    ["a self without addresses", { ...typesClone(statusRunning), self: { ...typesClone(statusRunning.self), addresses: undefined } }],
    ["a missing runSSH", { ...typesClone(statusRunning), runSSH: undefined }],
    ["a runSSH that is not a boolean", { ...typesClone(statusRunning), runSSH: "no" }],
    ["prefs that are an array", { ...typesClone(statusRunning), prefs: [] }],
    ["prefs that are null", { ...typesClone(statusRunning), prefs: null }],
    ["peers that are null", { ...typesClone(statusRunning), peers: null }],
    ["a peer that is a string", { ...typesClone(statusRunning), peers: ["db-1"] }],
    ["a peer with an unknown connection", { ...typesClone(statusRunning), peers: [peer({ connection: "wireguard" })] }],
    ["a peer with a lastHandshake that is a number", { ...typesClone(statusRunning), peers: [peer({ lastHandshake: 1 })] }],
    ["a peer without online", { ...typesClone(statusRunning), peers: [peer({ online: undefined })] }],
    ["health that is not strings", { ...typesClone(statusRunning), health: [1] }],
  ]
  for (const [name, value] of rows) expect(isTailscaleStatus(value), name).toBe(false)
})

test("isTailscaleLatency accepts the agent's JSON and refuses an incomplete answer", () => {
  expect(isTailscaleLatency(JSON.parse(JSON.stringify(latencyFixture)))).toBe(true)
  expect(isTailscaleLatency({ regions: [] })).toBe(true)
  const region = typesClone(latencyFixture.regions[0])
  const rows: [string, unknown][] = [
    ["null", null],
    ["an empty object", {}],
    ["regions that are null", { regions: null }],
    ["a region that is a number", { regions: [1] }],
    ["a region without a code", { regions: [{ ...region, code: undefined }] }],
    ["a regionId that is a string", { regions: [{ ...region, regionId: "1" }] }],
    ["a missing stun measurement", { regions: [{ ...region, stun: undefined }] }],
    ["an https measurement without available", { regions: [{ ...region, https: { medianMs: 1, reason: "" } }] }],
    ["a medianMs that is a string", { regions: [{ ...region, https: { available: true, medianMs: "9", reason: "" } }] }],
  ]
  for (const [name, value] of rows) expect(isTailscaleLatency(value), name).toBe(false)
})

// The types below are checked by `pnpm typecheck`: Vitest strips types, so a
// missing, misspelled or mistyped member of the agent's JSON fails the
// compilation of this file.
test("the wire types carry every member the agent sends", () => {
  const node: TailscaleNode = { id: "n1", hostName: "web-1", dnsName: "web-1.tail1234.ts.net", os: "linux", addresses: ["100.64.0.5"], tags: [] }
  const peer: TailscalePeer = {
    ...node, online: true, connection: "peer-relay", endpoint: "", region: "", exitNode: false, exitNodeOption: true, lastHandshake: null,
  }
  const measurement: Measurement = { available: false, medianMs: 0, reason: "no STUN endpoint" }
  const region: RegionLatency = { regionId: 1, code: "nyc", name: "New York City", stun: measurement, https: { available: true, medianMs: 20, reason: "" } }
  const prefs: PrefsFields = {
    wantRunning: true, advertiseRoutes: ["10.0.0.0/24"], exitNode: "", advertiseExitNode: false, acceptRoutes: true, shieldsUp: false, runSSH: true,
  }
  const unset: PrefsFields = {}
  expect([node, peer, measurement, region, prefs, unset]).toHaveLength(6)
  expect(JSON.parse(JSON.stringify(peer)) as TailscalePeer).toEqual(peer)
  expect(JSON.parse(JSON.stringify(prefs)) as PrefsFields).toEqual(prefs)
  expect(JSON.stringify(unset)).toBe("{}")
})
