import { expect, test } from "vitest"
import { fwBinding, fwDocker, fwExposure } from "./fixtures"
import {
  dockerQueryKey,
  exposureQueryKey,
  type ContainerRule,
  type DockerSettings,
  type ExposureAllowed,
  type ExposureRow,
  type ExposureVerdict,
} from "./types"

test("fxQueryKeys name the docker and exposure resources of one host", () => {
  expect(dockerQueryKey("h1")).toEqual(["resource", "h1", "firewall.docker"])
  expect(exposureQueryKey("h2")).toEqual(["resource", "h2", "firewall.exposure"])
})

test("fxDocker defaults to a supported, enabled integration with the default private sources", () => {
  const d = fwDocker()
  expect(d.available).toBe(true)
  expect(d.supported).toBe(true)
  expect(d.enabled).toBe(true)
  expect(d.health).toBe("ok")
  expect(d.settings).toEqual({
    privateSources: ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10"],
    forwardExemptions: [],
  })
  expect(d.networks.map((n) => [n.name, n.bridge, n.protected])).toEqual([["bridge", "docker0", true]])
  expect(d.bindings).toEqual([fwBinding()])
  expect(d.rules.map((r) => [r.kind, r.binding.selector, r.binding.hostPort])).toEqual([["allow", "web", 8080]])
})

test("fxDocker lets a test override any field and leaves the rest", () => {
  const d = fwDocker({ enabled: false, health: "off", issues: [{ level: "broken", reason: "block missing from after.rules" }] })
  expect(d.enabled).toBe(false)
  expect(d.health).toBe("off")
  expect(d.issues).toEqual([{ level: "broken", reason: "block missing from after.rules" }])
  expect(d.version).toBe("29.9.1")
})

test("fxDocker returns a fresh object each call", () => {
  const a = fwDocker()
  a.settings.privateSources.push("203.0.113.0/24")
  a.networks.push({ name: "x", id: "x", bridge: "br-x", subnets: [], protected: false })
  expect(fwDocker().settings.privateSources).toHaveLength(6)
  expect(fwDocker().networks).toHaveLength(1)
})

test("fxBinding is the web container's allowed binding and takes overrides", () => {
  expect(fwBinding()).toEqual({
    binding: { selector: "web", hostPort: 8080, proto: "tcp" },
    id: "a1b2c3d4e5f6",
    containers: ["web"],
    containerPort: 80,
    allowed: ["any"],
    denied: [],
  })
  expect(fwBinding({ denied: ["203.0.113.0/24"], proxied: ["v6"] })).toMatchObject({ denied: ["203.0.113.0/24"], proxied: ["v6"], containerPort: 80 })
})

test("fxExposure holds an open host service row and a partial container row", () => {
  const e = fwExposure()
  expect(e.rows.map((r) => [r.proto, r.addr, r.port, r.family, r.process ?? null])).toEqual([
    ["tcp", "0.0.0.0", 22, "v4", "sshd"],
    ["tcp", "0.0.0.0", 8080, "v4", null],
  ])
  expect(e.rows[0]?.classes.map((c) => [c.class, c.verdict])).toEqual([["internet", "open"], ["private", "open"]])
  const container = e.rows[1]
  expect(container?.container).toEqual({ name: "web", binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containerPort: 80, proxied: false })
  expect(container?.classes[0]).toMatchObject({ class: "internet", verdict: "partial" })
  expect(fwExposure({ notes: ["Docker is unavailable"] }).notes).toEqual(["Docker is unavailable"])
})

// These literals pin the field names of the Go shapes (DockerSettings,
// ContainerRule, ExposureVerdict, ExposureRow) at typecheck time.
test("fxTypes accept every optional field the server can send", () => {
  const settings: DockerSettings = {
    privateSources: ["10.0.0.0/8"],
    forwardExemptions: [{ inInterface: "eth0", origDestination: "192.0.2.10", origPort: 8443, destination: "172.17.0.2", proto: "tcp", port: 443 }],
  }
  const rule: ContainerRule = { id: "01JD000000000000000000R002", binding: { selector: "proj/web", hostAddr: "127.0.0.1", hostPort: 8081, proto: "udp" }, kind: "deny", from: "203.0.113.0/24" }
  const allowed: ExposureAllowed = { sources: ["198.51.100.0/24"], ifaces: ["eth0"], srcPorts: ["1024-2048"], decidedBy: "filter/ufw-user-input#4" }
  const verdict: ExposureVerdict = { class: "private", range: "10.0.0.0/8", verdict: "unknown", decidedBy: "filter/INPUT#2", allowed: [allowed], note: "too many partitions" }
  const row: ExposureRow = { proto: "udp", addr: "::", port: 53, family: "v6", process: "named", classes: [verdict] }
  expect([settings, rule, row].map((x) => typeof x)).toEqual(["object", "object", "object"])
})
