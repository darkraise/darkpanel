import type { Page, Route } from "@playwright/test"
import type { Change, Host, Plan, Session } from "../src/api/types"
import type { ContainerDetail, DockerContainer, DockerNetwork } from "../src/modules/docker/types"
import type { DockerStatus, ExposureView, FirewallStatus } from "../src/modules/firewall/types"
import type { InstanceStatus } from "../src/modules/npm/types"

export const HOST_ID = "01JE00000000000000000000H1"
export const OFFLINE_HOST_ID = "01JE00000000000000000000H2"
export const CONTAINER_ID = "c0ffee0123456789abcdef"
export const FIREWALL_CHANGE_ID = "01JE000000000000000000C001"
export const EXPOSE_CHANGE_ID = "01JE000000000000000000C002"

const NOW = "2026-11-05T09:00:00Z"

const session: Session = {
  user: { id: "01JE000000000000000000U001", username: "admin", role: "admin", state: "active", createdAt: NOW },
  csrfToken: "csrf-fixture",
  expiresAt: null,
  token: null,
}

function moduleState(id: string, over: Partial<Host["modules"][number]> = {}): Host["modules"][number] {
  return { id, version: 1, enabled: true, available: true, reason: null, checkedAt: NOW, ...over }
}

const hosts: Host[] = [
  {
    id: HOST_ID,
    name: "web-1",
    isLocal: true,
    online: true,
    modules: [moduleState("hostinfo"), moduleState("firewall"), moduleState("docker"), moduleState("npm"), moduleState("tailscale", { enabled: false })],
    ingestion: { ok: true, since: NOW, error: null },
  },
  {
    id: OFFLINE_HOST_ID,
    name: "db-1",
    isLocal: false,
    online: false,
    modules: [
      moduleState("hostinfo"),
      moduleState("firewall", { available: false, reason: "UFW is not installed." }),
      moduleState("docker", { enabled: false }),
    ],
    ingestion: { ok: false, since: NOW, error: "The agent has not answered since 08:12." },
  },
]

const firewallStatus: FirewallStatus = {
  supported: true,
  unsupported: [],
  active: true,
  inSync: true,
  outOfSync: [],
  configured: { enabled: true, logLevel: "low", ipv6: true, defaults: { incoming: "DROP", outgoing: "ACCEPT", routed: "DROP" } },
  loaded: {
    variant: "nft",
    ipv6: true,
    sysctl: { "net/ipv4/ip_forward": "0" },
    chains: [{ family: "v4", table: "filter", chain: "INPUT", policy: "DROP", rules: 3 }],
  },
  rules: [
    { id: "01JE0000000000000000000R01", action: "allow", direction: "in", proto: "tcp", from: "any", to: "any", toPort: "22", family: "both", origin: "darkpanel", comment: "ssh" },
    { id: "manual-0123456789abcdef", action: "allow", direction: "in", proto: "tcp", from: "any", to: "any", toPort: "8080", family: "v4", origin: "manual" },
  ],
}

const firewallDocker: DockerStatus = {
  available: true,
  version: "29.9.1",
  driver: "iptables",
  dockerVariant: "nft",
  ufwVariant: "nft",
  rawProtection: true,
  supported: true,
  enabled: true,
  health: "ok",
  networks: [{ name: "bridge", id: "0123456789abcdef", bridge: "docker0", subnets: ["172.17.0.0/16"], protected: true }],
  settings: { privateSources: ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"], forwardExemptions: [] },
  rules: [],
  bindings: [
    { binding: { selector: "web", hostPort: 8080, proto: "tcp" }, id: "a1b2c3d4e5f6", containers: ["web"], containerPort: 80, allowed: [], denied: [] },
  ],
}

const exposure: ExposureView = {
  rows: [
    {
      proto: "tcp",
      addr: "0.0.0.0",
      port: 8080,
      family: "v4",
      container: { name: "web", binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containerPort: 80, proxied: false },
      classes: [{ class: "internet", verdict: "closed" }],
    },
  ],
}

const containers: DockerContainer[] = [
  {
    id: CONTAINER_ID,
    name: "web",
    image: "nginx:1.27",
    imageId: "sha256:0123456789abcdef",
    state: "running",
    status: "Up 3 hours",
    created: NOW,
    ports: [{ ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" }],
    usage: { cpuPercent: 0.4, memUsage: 31_457_280, memLimit: 2_147_483_648 },
  },
  {
    id: "deadbeef0123456789abcd",
    name: "worker",
    image: "example.com/worker:3",
    imageId: "sha256:fedcba9876543210",
    state: "exited",
    status: "Exited (0) 2 days ago",
    created: NOW,
    ports: [],
  },
]

const containerDetail: ContainerDetail = {
  id: CONTAINER_ID,
  name: "/web",
  image: "nginx:1.27",
  imageId: "sha256:0123456789abcdef",
  created: NOW,
  state: "running",
  running: true,
  startedAt: NOW,
  exitCode: 0,
  restartPolicy: "unless-stopped",
  env: [{ name: "NGINX_HOST", value: "<redacted>" }],
  ports: [{ ip: "0.0.0.0", privatePort: 80, publicPort: 8080, proto: "tcp" }],
  mounts: [{ type: "bind", source: "/srv/www", destination: "/usr/share/nginx/html", rw: false }],
  networks: [{ network: "apps", networkId: "feedface0123", container: "web", ipv4: "172.20.0.2" }],
}

const networks: DockerNetwork[] = [
  { id: "feedface0123", name: "apps", driver: "bridge", scope: "local", internal: false, ipv6: false, subnets: ["172.20.0.0/16"], containers: ["web"], created: NOW },
  { id: "0123456789ab", name: "bridge", driver: "bridge", scope: "local", internal: false, ipv6: false, subnets: ["172.17.0.0/16"], containers: [], created: NOW },
]

const npmStatus: InstanceStatus = {
  deployed: true,
  containerId: "npm0123456789",
  image: "jc21/nginx-proxy-manager:2.12.6",
  pinnedImage: "jc21/nginx-proxy-manager:2.12.6",
  running: true,
  apiReady: true,
  public: "no",
  adminEmail: "admin@example.com",
  upgradeAvailable: false,
}

function change(over: Partial<Change>): Change {
  return {
    id: FIREWALL_CHANGE_ID,
    hostId: HOST_ID,
    action: "firewall.rule.add",
    version: 1,
    class: "revertible",
    state: "pending",
    actor: "user:admin",
    createdAt: NOW,
    updatedAt: NOW,
    revertDeadline: null,
    output: null,
    error: null,
    compensatedBy: null,
    parentChangeId: null,
    owner: null,
    repairedBy: null,
    repairOutcome: null,
    override: null,
    ...over,
  }
}

function plan(over: Partial<Plan>): Plan {
  return {
    planId: "plan-fixture",
    hostId: HOST_ID,
    action: "firewall.rule.add",
    version: 1,
    inputHash: "sha256:fixture",
    diff: [],
    guards: [],
    expiresAt: "2099-01-01T00:00:00Z",
    confirmation: null,
    ...over,
  }
}

const LOCKOUT = "firewall.lockout"

function firewallPlan(overrides: unknown): Plan {
  const overridden = Array.isArray(overrides) && overrides.includes(LOCKOUT)
  return plan({
    planId: "plan-firewall-rule-add",
    action: "firewall.rule.add",
    diff: [
      { op: " ", text: "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in" },
      { op: "+", text: "### tuple ### deny tcp 22 0.0.0.0/0 any 198.51.100.0/24 in" },
      { op: "-", text: "### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in" },
    ],
    guards: [
      {
        id: LOCKOUT,
        ok: overridden,
        message: "The rule would block the address this session reaches the host from.",
        overridable: true,
        overridden,
      },
      { id: "firewall.persist", ok: true, message: "UFW's files and the loaded rules stay in sync.", overridable: false, overridden: false },
    ],
  })
}

type Reply = { status?: number; json?: unknown; sse?: string }

function problem(status: number, code: string, detail: string): Reply {
  return { status, json: { type: "about:blank", title: detail, status, detail, code } }
}

function sse(events: { event: string; data: unknown }[]): Reply {
  return { sse: events.map((e) => `event: ${e.event}\ndata: ${JSON.stringify(e.data)}\n\n`).join("") }
}

function bodyOf(route: Route): { input?: Record<string, unknown> } {
  const text = route.request().postData()
  return text === null ? {} : (JSON.parse(text) as { input?: Record<string, unknown> })
}

const ACTION = /^\/api\/v1\/(?:hosts\/([^/]+)\/)?actions\/([^/]+)\/v\d+\/(read|plan|apply)$/
const RESOURCE = /^\/api\/v1\/hosts\/[^/]+\/resources\/([^/]+)\/([^/]+)$/
const STREAM = /^\/api\/v1\/hosts\/[^/]+\/streams\/docker\/(logs|stats)$/
const AVAILABILITY = /^\/api\/v1\/hosts\/[^/]+\/modules\/([^/]+)\/availability$/
const CHANGE = /^\/api\/v1\/changes\/([^/]+)(?:\/(confirm|revert|steps))?$/

const READS: Record<string, () => unknown> = {
  "core.host.list": () => ({ hosts }),
  "npm.instance.list": () => ({ instances: [], unreadable: [] }),
  "panel.exposure.status": () => ({ exposed: false }),
  "npm.instance.status": () => npmStatus,
  "docker.container.inspect": () => containerDetail,
}

const RESOURCES: Record<string, unknown> = {
  "firewall.status": firewallStatus,
  "firewall.docker": firewallDocker,
  "firewall.exposure": exposure,
  "docker.containers": { containers },
  "docker.networks": { networks },
}

// plans and applies answer null for an action the fixtures do not cover, so
// mockApi records the request as unhandled.
function plans(name: string, input: Record<string, unknown>): Reply | null {
  switch (name) {
    case "firewall.rule.add":
      return { json: firewallPlan(input.overrides) }
    case "docker.container.stop":
      return {
        json: plan({
          planId: "plan-docker-stop",
          action: "docker.container.stop",
          diff: [{ op: "-", text: "container web: running" }, { op: "+", text: "container web: exited" }],
          guards: [{ id: "docker.not_owned", ok: true, message: "No other module owns the container.", overridable: false, overridden: false }],
          confirmation: "web",
        }),
      }
    case "npm.expose.container":
      return {
        json: plan({
          planId: "plan-npm-expose",
          hostId: null,
          action: "npm.expose.container",
          diff: [
            { op: "+", text: "proxy host app.example.com -> web:80 on network apps" },
            { op: "+", text: "certificate app.example.com (Let's Encrypt)" },
          ],
          guards: [{ id: "npm.network", ok: true, message: "Nginx Proxy Manager shares the network apps with web.", overridable: false, overridden: false }],
        }),
      }
    default:
      return null
  }
}

function applies(name: string, changes: Map<string, Change>): Reply | null {
  let applied: Change
  switch (name) {
    case "firewall.rule.add":
      applied = change({ state: "pending", revertDeadline: new Date(Date.now() + 60_000).toISOString() })
      break
    case "npm.expose.container":
      applied = change({ id: EXPOSE_CHANGE_ID, hostId: null, action: "npm.expose.container", class: "composite", state: "completed" })
      break
    default:
      return null
  }
  changes.set(applied.id, applied)
  return { json: applied }
}

// Fixtures is what a test reads back from mockApi.
export interface Fixtures {
  // The "<method> <path>" of every API request no handler answered; a test
  // fails on any, because a 404 would render an error alert and skew an audit.
  unhandled: string[]
}

// mockApi answers every /api/ request of page from the fixtures above, so a
// browser pass needs no controller. A signed-out page gets auth.required from
// the session probe and the login form answers auth.invalid.
export async function mockApi(page: Page, opts: { signedIn: boolean }): Promise<Fixtures> {
  const fixtures: Fixtures = { unhandled: [] }
  const changes = new Map<string, Change>()
  await page.route(
    (url) => url.pathname.startsWith("/api/"),
    async (route) => {
      const request = route.request()
      const method = request.method()
      const path = new URL(request.url()).pathname
      const reply = answer(method, path, route, opts.signedIn, changes)
      if (reply === null) {
        fixtures.unhandled.push(`${method} ${path}`)
        await route.fulfill({ status: 404, contentType: "application/json", body: JSON.stringify(problem(404, "not_found", "No fixture").json) })
        return
      }
      if (reply.sse !== undefined) {
        await route.fulfill({ status: 200, contentType: "text/event-stream", body: reply.sse })
        return
      }
      if (reply.json === undefined) {
        await route.fulfill({ status: reply.status ?? 204 })
        return
      }
      await route.fulfill({ status: reply.status ?? 200, contentType: "application/json", body: JSON.stringify(reply.json) })
    },
  )
  return fixtures
}

function answer(method: string, path: string, route: Route, signedIn: boolean, changes: Map<string, Change>): Reply | null {
  if (path === "/api/v1/auth/session") {
    return signedIn ? { json: session } : problem(401, "auth.required", "Sign in to continue.")
  }
  if (path === "/api/v1/auth/login") return problem(401, "auth.invalid", "Login failed.")
  if (path === "/api/v1/auth/logout") return { status: 204 }
  if (path === "/api/v1/events") return { status: 204 }
  if (path === "/api/v1/controller/listeners") {
    return { json: { tailscaleBound: false, message: "No Tailscale address: the controller listens on loopback only." } }
  }
  const action = ACTION.exec(path)
  if (action !== null && method === "POST") {
    const name = action[2] ?? ""
    switch (action[3]) {
      case "read": {
        const read = READS[name]
        return read === undefined ? null : { json: { output: read() } }
      }
      case "plan":
        return plans(name, bodyOf(route).input ?? {})
      default:
        return applies(name, changes)
    }
  }
  const resource = RESOURCE.exec(path)
  if (resource !== null && method === "GET") {
    const found = RESOURCES[`${resource[1]}.${resource[2]}`]
    return found === undefined ? null : { json: found }
  }
  const stream = STREAM.exec(path)
  if (stream !== null) {
    return stream[1] === "logs"
      ? sse([
          { event: "data", data: { stream: "stdout", text: "172.20.0.1 - - GET / 200\n" } },
          { event: "data", data: { stream: "stderr", text: "[warn] upstream timed out\n" } },
          { event: "end", data: { reason: "done" } },
        ])
      : sse([
          { event: "data", data: { read: NOW, cpuPercent: 0.4, memUsage: 31_457_280, memLimit: 2_147_483_648, netRx: 1024, netTx: 2048, pids: 4 } },
          { event: "end", data: { reason: "done" } },
        ])
  }
  const availability = AVAILABILITY.exec(path)
  if (availability !== null && method === "GET") {
    return { json: { module: availability[1], available: true, reason: null, actions: [] } }
  }
  const changeMatch = CHANGE.exec(path)
  if (changeMatch !== null) {
    const found = changes.get(decodeURIComponent(changeMatch[1] ?? ""))
    if (found === undefined) return null
    const verb = changeMatch[2]
    if (verb === "steps") return { json: { steps: [] } }
    if (verb === "confirm" || verb === "revert") {
      const next = { ...found, state: verb === "confirm" ? "confirmed" : "reverted", revertDeadline: null }
      changes.set(next.id, next)
      return { json: next }
    }
    return { json: found }
  }
  return null
}
