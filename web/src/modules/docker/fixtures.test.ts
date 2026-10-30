import { expect, test } from "vitest"
import { dkContainer, dkDetail, dkEngine, dkImage, dkNetwork, dkProject, dkVolume } from "./fixtures"

test("dkEngine is an available engine and takes overrides", () => {
  expect(dkEngine()).toEqual({ available: true, version: "29.9.1", apiVersion: "1.54", os: "linux", arch: "amd64", containers: 3, running: 2, images: 4 })
  expect(dkEngine({ available: false, reason: "the Docker socket is missing" })).toEqual({
    ...dkEngine(),
    available: false,
    reason: "the Docker socket is missing",
  })
})

test("dkContainer is the running web container", () => {
  const c = dkContainer()
  expect(Object.keys(c).sort()).toEqual(["created", "id", "image", "imageId", "name", "ports", "state", "status", "usage"])
  expect(c.id).toHaveLength(64)
  expect([c.name, c.image, c.state, c.status, c.created]).toEqual(["web", "nginx:1.27", "running", "Up 2 hours", "2026-10-01T00:00:00Z"])
  expect(c.ports).toEqual([{ ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" }])
  expect(c.usage).toEqual({ cpuPercent: 1.5, memUsage: 52428800, memLimit: 1073741824 })
})

test("dkContainer overrides replace a member, including removing one with undefined", () => {
  const c = dkContainer({ name: "db", state: "exited", usage: undefined, project: "shop", service: "db", owner: "npm" })
  expect([c.name, c.state, c.project, c.service, c.owner]).toEqual(["db", "exited", "shop", "db", "npm"])
  expect(c.usage).toBeUndefined()
  expect(c.image).toBe("nginx:1.27")
})

test("dkImage, dkNetwork, dkVolume and dkProject default to the objects the web container uses", () => {
  expect(dkImage()).toEqual({
    id: dkContainer().imageId,
    tags: ["nginx:1.27"],
    digests: ["nginx@sha256:" + "d".repeat(64)],
    size: 192000000,
    created: "2026-09-20T00:00:00Z",
    containers: ["web"],
    dangling: false,
  })
  expect(dkNetwork()).toEqual({
    id: "n".repeat(64),
    name: "bridge",
    driver: "bridge",
    scope: "local",
    internal: false,
    ipv6: false,
    subnets: ["172.17.0.0/16"],
    containers: ["web"],
    created: "2026-09-01T00:00:00Z",
  })
  expect(dkVolume()).toEqual({
    name: "data",
    driver: "local",
    mountpoint: "/var/lib/docker/volumes/data/_data",
    created: "2026-09-15T00:00:00Z",
    containers: ["web"],
  })
  expect(dkProject()).toEqual({
    name: "shop",
    directory: "/srv/shop",
    files: ["/srv/shop/compose.yaml"],
    envFiles: [],
    profiles: [],
    configHash: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
    hashChanged: false,
    containers: ["shop-web-1"],
    running: 1,
    firstSeen: "2026-10-02T00:00:00Z",
  })
})

test("dkDetail describes the same web container, with every environment value redacted", () => {
  const d = dkDetail()
  const c = dkContainer()
  expect([d.id, d.name, d.image, d.imageId, d.created, d.state]).toEqual([c.id, c.name, c.image, c.imageId, c.created, c.state])
  expect(Object.keys(d).sort()).toEqual([
    "cmd", "created", "entrypoint", "env", "exitCode", "id", "image", "imageId", "labels", "mounts", "name",
    "networks", "ports", "restartPolicy", "running", "startedAt", "state", "workingDir",
  ])
  expect(d.running).toBe(true)
  expect(d.ports).toEqual(c.ports)
  expect(d.env.map((e) => e.value)).toEqual(["[redacted]", "[redacted]"])
  expect(d.mounts).toEqual([{ type: "volume", name: dkVolume().name, source: dkVolume().mountpoint, destination: "/data", rw: true }])
  expect(d.networks).toEqual([
    { network: dkNetwork().name, networkId: dkNetwork().id, container: "web", endpointId: "e".repeat(64), ipv4: "172.17.0.2" },
  ])
  expect(dkDetail({ name: "db", running: false, finishedAt: "2026-10-01T01:00:00Z" })).toMatchObject({ name: "db", running: false, finishedAt: "2026-10-01T01:00:00Z" })
})

test("every fixture returns fresh arrays and objects", () => {
  dkContainer().ports.push({ privatePort: 1, proto: "udp" })
  dkImage().tags.push("x")
  dkNetwork().subnets.push("10.0.0.0/8")
  dkVolume().containers.push("x")
  dkProject().files.push("x")
  dkDetail().env.push({ name: "X", value: "[redacted]" })
  dkDetail().networks.push({ network: "x", networkId: "x", container: "web" })
  expect(dkContainer().ports).toHaveLength(1)
  expect(dkImage().tags).toEqual(["nginx:1.27"])
  expect(dkNetwork().subnets).toEqual(["172.17.0.0/16"])
  expect(dkVolume().containers).toEqual(["web"])
  expect(dkProject().files).toEqual(["/srv/shop/compose.yaml"])
  expect(dkDetail().env).toHaveLength(2)
  expect(dkDetail().networks).toHaveLength(1)
})
