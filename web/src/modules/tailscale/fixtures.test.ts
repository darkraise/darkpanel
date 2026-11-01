import { expect, test } from "vitest"
import { latencyFixture, statusLoggedOut, statusRunning } from "./fixtures"
import { isTailscaleLatency, isTailscaleStatus } from "./types"

test("statusRunning is a node on a tailnet with one peer of each connection kind", () => {
  expect(isTailscaleStatus(statusRunning)).toBe(true)
  expect(statusRunning.backendState).toBe("Running")
  expect(statusRunning.self?.dnsName).toBe("web-1.tail1234.ts.net")
  expect(statusRunning.self?.addresses).toEqual(["100.64.0.5", "fd7a:115c:a1e0::5"])
  expect(statusRunning.magicDNSSuffix).toBe("tail1234.ts.net")
  expect(statusRunning.preferredDerp).toBe("fra")
  expect(statusRunning.peers.map((p) => p.connection)).toEqual(["derp", "direct", "peer-relay", "none"])
  const [derp, direct, relay, offline] = statusRunning.peers
  expect([derp?.region, direct?.endpoint, relay?.region, offline?.online]).toEqual(["fra", "203.0.113.7:41641", "", false])
  expect(offline?.lastHandshake).toBeNull()
})

test("statusRunning lists its peers by DNS name, as the agent does", () => {
  const names = statusRunning.peers.map((p) => p.dnsName)
  expect(names).toEqual([...names].sort())
})

test("statusLoggedOut is a node that needs login", () => {
  expect(isTailscaleStatus(statusLoggedOut)).toBe(true)
  expect(statusLoggedOut.backendState).toBe("NeedsLogin")
  expect(statusLoggedOut.self).toBeNull()
  expect(statusLoggedOut.peers).toEqual([])
  expect([statusLoggedOut.tailnet, statusLoggedOut.magicDNSSuffix, statusLoggedOut.preferredDerp]).toEqual(["", "", ""])
})

test("latencyFixture has three regions by id and one without STUN", () => {
  expect(isTailscaleLatency(latencyFixture)).toBe(true)
  const ids = latencyFixture.regions.map((r) => r.regionId)
  expect(ids).toHaveLength(3)
  expect(ids).toEqual([...ids].sort((a, b) => a - b))
  const noStun = latencyFixture.regions.filter((r) => !r.stun.available)
  expect(noStun.map((r) => r.code)).toEqual(["syd"])
  expect(noStun[0]?.stun).toEqual({ available: false, medianMs: 0, reason: "no STUN endpoint" })
  for (const r of latencyFixture.regions) {
    expect(r.https.available, r.code).toBe(true)
    expect(r.https.reason, r.code).toBe("")
  }
})

test("the fixtures survive a JSON round trip unchanged", () => {
  for (const v of [statusRunning, statusLoggedOut, latencyFixture]) expect(JSON.parse(JSON.stringify(v))).toEqual(v)
})
