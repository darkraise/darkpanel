import { expect, test } from "vitest"
import {
  DOCKER_ACTION_VERSION,
  DOCKER_MODULE,
  dockerKey,
  type ContainerState,
  type DockerMount,
  type DockerPort,
  type DockerUsage,
  type EndpointView,
  type EnvVar,
  type LogEvent,
  type LogLine,
  type Pruned,
  type PullEvent,
  type StatsEvent,
} from "./types"

test("the module id and the action version are the docker module's", () => {
  expect(DOCKER_MODULE).toBe("docker")
  expect(DOCKER_ACTION_VERSION).toBe(1)
})

test("dockerKey names the resource of one host", () => {
  expect(dockerKey("h1", "engine")).toEqual(["resource", "h1", "docker.engine"])
  expect(dockerKey("h2", "containers")).toEqual(["resource", "h2", "docker.containers"])
  expect(dockerKey("h2", "images")).toEqual(["resource", "h2", "docker.images"])
  expect(dockerKey("h3", "networks")).toEqual(["resource", "h3", "docker.networks"])
  expect(dockerKey("h3", "volumes")).toEqual(["resource", "h3", "docker.volumes"])
  expect(dockerKey("h4", "projects")).toEqual(["resource", "h4", "docker.projects"])
})

// The types below have no fixture. Vitest strips types, so this test is
// checked by `pnpm typecheck`: a missing, misspelled or mistyped member of
// the agent's JSON fails the compilation of this file.
test("the smaller wire types carry every member the agent sends", () => {
  const port: DockerPort = { ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" }
  const usage: DockerUsage = { cpuPercent: 1.5, memUsage: 2, memLimit: 3 }
  const state: ContainerState = { id: "a1", name: "web", running: true }
  const env: EnvVar = { name: "TOKEN", value: "[redacted]" }
  const mount: DockerMount = { type: "volume", name: "data", source: "/var/lib/docker/volumes/data/_data", destination: "/data", rw: true }
  const endpoint: EndpointView = { network: "bridge", networkId: "n1", container: "web", endpointId: "e1", aliases: ["web"], ipv4: "172.17.0.2", ipv6: "fd00::2" }
  const line: LogLine = { stream: "stdout", text: "ready" }
  const log: LogEvent = { stream: "stderr", text: "failed" }
  const stats: StatsEvent = { read: "2026-10-30T00:00:00Z", cpuPercent: 1, memUsage: 2, memLimit: 3, netRx: 4, netTx: 5, pids: 6 }
  const pull: PullEvent = { status: "Downloading", id: "layer", progress: "[=>  ]", current: 1, total: 2 }
  const pullFailed: PullEvent = { status: "error", error: "denied" }
  const pruned: Pruned = { images: [{ id: "sha256:aa", tags: ["old:1"], size: 10 }], reclaimed: 10 }
  expect([port, usage, state, env, mount, endpoint, line, log, stats, pull, pullFailed, pruned]).toHaveLength(12)
  expect(JSON.parse(JSON.stringify(stats)) as StatsEvent).toEqual(stats)
})
