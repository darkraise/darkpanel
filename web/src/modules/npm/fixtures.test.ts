import { expect, test } from "vitest"
import { npAccessList, npCertificate, npInstance, npInstances, npProxyHost } from "./fixtures"
import { isInstanceStatus, isInstancesOutput } from "./types"

const FX_IMAGE = "jc21/nginx-proxy-manager:2.13.5@sha256:" + "ab".repeat(32)

test("npInstance is a running, ready, deployed instance on the pinned image that is not public", () => {
  expect(isInstanceStatus(npInstance())).toBe(true)
  expect(npInstance()).toEqual({
    deployed: true,
    containerId: "c0ffee00".repeat(8),
    image: FX_IMAGE,
    pinnedImage: FX_IMAGE,
    running: true,
    apiReady: true,
    public: "no",
    adminEmail: "ops@example.com",
    adminExposure: null,
    upgradeAvailable: false,
  })
})

test("npInstance overrides replace a member and each call returns a new object", () => {
  const exposed = npInstance({ public: "confirmed", adminExposure: { hostname: "npm.example.com", proxyHostId: 4, certificateId: 5 } })
  expect([exposed.public, exposed.adminExposure?.hostname, exposed.running]).toEqual(["confirmed", "npm.example.com", true])
  const none = npInstance({ deployed: false, containerId: "", running: false, apiReady: false })
  expect([none.deployed, none.containerId, none.image]).toEqual([false, "", FX_IMAGE])
  const first = npInstance()
  first.running = false
  expect(npInstance().running).toBe(true)
})

test("npProxyHost, npAccessList and npCertificate describe one exposure that is not reserved", () => {
  expect(npProxyHost()).toEqual({
    id: 1,
    domains: ["app.example.com"],
    forwardScheme: "http",
    forwardHost: "web",
    forwardPort: 8080,
    certificateId: 3,
    accessListId: 0,
    sslForced: true,
    hsts: true,
    websockets: false,
    reserved: "",
    unclaimed: false,
  })
  expect(npAccessList()).toEqual({
    id: 2,
    name: "office",
    satisfyAny: false,
    clients: [{ address: "203.0.113.0/24", directive: "allow" }],
    reserved: "",
    unclaimed: false,
  })
  expect(npCertificate()).toEqual({
    id: 3,
    name: "app.example.com",
    domains: ["app.example.com"],
    expiresOn: "2027-01-31T10:00:00.000Z",
    reserved: "",
    unclaimed: false,
  })
  expect(npProxyHost({ reserved: "npm.admin.expose", unclaimed: true })).toEqual({ ...npProxyHost(), reserved: "npm.admin.expose", unclaimed: true })
  expect(npAccessList({ name: "darkpanel-c1-4" }).name).toBe("darkpanel-c1-4")
  expect(npCertificate({ domains: ["a.example.com", "b.example.com"] }).domains).toEqual(["a.example.com", "b.example.com"])
})

test("npInstances lists one host with the default instance and takes overrides", () => {
  expect(isInstancesOutput(npInstances())).toBe(true)
  expect(npInstances()).toEqual({ instances: [{ hostId: "01JD0000000000000000000011", hostName: "edge-1", status: npInstance() }] })
  expect(npInstances({ instances: [] })).toEqual({ instances: [] })
})
