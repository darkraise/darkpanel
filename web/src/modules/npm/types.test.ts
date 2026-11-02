import { expect, test } from "vitest"
import { npInstance, npInstances } from "./fixtures"
import {
  NPM_ACTION_VERSION,
  NPM_MODULE,
  isInstanceStatus,
  isInstancesOutput,
  npmFleetKey,
  npmKey,
  type AccessListList,
  type AdminExposure,
  type AdminPassword,
  type CertificateList,
  type HostInstance,
  type InstancesOutput,
  type ProxyHostList,
} from "./types"

type Json = Record<string, unknown>

function tyClone<T>(v: T): Json {
  return structuredClone(v) as Json
}

test("the module id and the action version are the npm module's", () => {
  expect(NPM_MODULE).toBe("npm")
  expect(NPM_ACTION_VERSION).toBe(1)
})

test("npmKey names one read of a host under its resources and npmFleetKey names the fleet read", () => {
  expect(npmKey("h1", "instance.status")).toEqual(["resource", "h1", "npm.instance.status"])
  expect(npmKey("h2", "proxyhost.list")).toEqual(["resource", "h2", "npm.proxyhost.list"])
  expect(npmFleetKey).toEqual(["npm", "instances"])
})

test("isInstanceStatus accepts the agent's JSON, with the admin exposure null, absent or set", () => {
  expect(isInstanceStatus(JSON.parse(JSON.stringify(npInstance())))).toBe(true)
  const absent = tyClone(npInstance())
  delete absent.adminExposure
  expect(isInstanceStatus(absent)).toBe(true)
  const exposed = npInstance({ public: "confirmed", adminExposure: { hostname: "npm.example.com", proxyHostId: 4, certificateId: 5 } })
  expect(isInstanceStatus(JSON.parse(JSON.stringify(exposed)))).toBe(true)
  expect(isInstanceStatus(npInstance({ deployed: false, containerId: "", image: "", running: false, apiReady: false, adminEmail: "" }))).toBe(true)
})

test("isInstanceStatus refuses what is not a complete status", () => {
  const status = (over: Json) => ({ ...tyClone(npInstance()), ...over })
  const rows: [string, unknown][] = [
    ["null", null],
    ["a string", "status"],
    ["an array", []],
    ["an empty object", {}],
    ["a deployed that is a string", status({ deployed: "yes" })],
    ["a missing containerId", status({ containerId: undefined })],
    ["a pinnedImage that is a number", status({ pinnedImage: 1 })],
    ["a running that is a string", status({ running: "yes" })],
    ["a missing apiReady", status({ apiReady: undefined })],
    ["an unknown public value", status({ public: "maybe" })],
    ["a missing adminEmail", status({ adminEmail: undefined })],
    ["an adminExposure that is a string", status({ adminExposure: "npm.example.com" })],
    ["an adminExposure without a hostname", status({ adminExposure: { proxyHostId: 4, certificateId: 5 } })],
    ["an adminExposure with a proxyHostId that is a string", status({ adminExposure: { hostname: "npm.example.com", proxyHostId: "4", certificateId: 5 } })],
    ["a missing upgradeAvailable", status({ upgradeAvailable: undefined })],
  ]
  for (const [name, value] of rows) expect(isInstanceStatus(value), name).toBe(false)
})

test("isInstancesOutput accepts the controller's JSON and an empty fleet", () => {
  expect(isInstancesOutput(JSON.parse(JSON.stringify(npInstances())))).toBe(true)
  expect(isInstancesOutput({ instances: [] })).toBe(true)
})

test("isInstancesOutput refuses what is not a complete answer", () => {
  const host = tyClone(npInstances().instances[0])
  const rows: [string, unknown][] = [
    ["null", null],
    ["an empty object", {}],
    ["instances that are null", { instances: null }],
    ["an instance that is a string", { instances: ["h1"] }],
    ["an instance without a hostId", { instances: [{ ...host, hostId: undefined }] }],
    ["a hostName that is a number", { instances: [{ ...host, hostName: 1 }] }],
    ["a status that is not a status", { instances: [{ ...host, status: { deployed: true } }] }],
  ]
  for (const [name, value] of rows) expect(isInstancesOutput(value), name).toBe(false)
})

// Vitest strips types, so this test is checked by `pnpm typecheck`: a
// missing, misspelled or mistyped member of the Go wire types fails the
// compilation of this file.
test("the wire types carry every member the agent and the controller send", () => {
  const exposure: AdminExposure = { hostname: "npm.example.com", proxyHostId: 4, certificateId: 5 }
  const host: HostInstance = { hostId: "h1", hostName: "edge-1", status: npInstance({ adminExposure: exposure }) }
  const fleet: InstancesOutput = { instances: [host] }
  const hosts: ProxyHostList = {
    items: [{
      id: 1, domains: ["app.example.com"], forwardScheme: "https", forwardHost: "web", forwardPort: 443, certificateId: 3, accessListId: 2,
      sslForced: true, hsts: true, websockets: true, reserved: "npm.admin.expose", unclaimed: false,
    }],
  }
  const lists: AccessListList = {
    items: [{ id: 2, name: "office", satisfyAny: true, clients: [{ address: "198.51.100.7", directive: "deny" }], reserved: "", unclaimed: true }],
  }
  const certs: CertificateList = {
    items: [{ id: 3, name: "app.example.com", domains: ["app.example.com"], expiresOn: "2027-01-31T10:00:00.000Z", reserved: "", unclaimed: false }],
  }
  const password: AdminPassword = { email: "ops@example.com", password: "s3cret" }
  expect(JSON.parse(JSON.stringify(fleet)) as InstancesOutput).toEqual(fleet)
  expect([hosts, lists, certs, password]).toHaveLength(4)
})
