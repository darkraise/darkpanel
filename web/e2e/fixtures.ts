import type { Page, Route } from "@playwright/test"
import type { AuditEntry, Change, Host, Plan, Rollout, RolloutHost, Session, Step, UpdateTransaction } from "../src/api/types"
import type { ContainerDetail, DockerContainer, DockerNetwork } from "../src/modules/docker/types"
import type { DockerStatus, ExposureView, FirewallStatus } from "../src/modules/firewall/types"
import type { InstanceStatus } from "../src/modules/npm/types"
import type { Summary } from "../src/modules/hostinfo/summary-page"
import { installedFixture, rebootFixture, updatesFixture } from "../src/modules/system/fixtures"
import type { DockerState, InstalledPackage, Updates } from "../src/modules/system/types"

export const HOST_ID = "01JE00000000000000000000H1"
const OFFLINE_HOST_ID = "01JE00000000000000000000H2"
export const PENDING_HOST_ID = "01JE00000000000000000000P1"
export const REVOKED_HOST_ID = "01JE00000000000000000000R1"
export const LONG_HOST_ID = "01JE00000000000000000000L1"
export const CONTAINER_ID = "c0ffee0123456789abcdef"
const FIREWALL_CHANGE_ID = "01JE000000000000000000C001"
const EXPOSE_CHANGE_ID = "01JE000000000000000000C002"

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
    state: "enrolled",
    online: true,
    lastSeenAt: "2026-11-11T08:00:00Z",
    agentVersion: "1.0.0",
    protocolVersion: 1,
    os: "debian-12",
    arch: "amd64",
    supported: true,
    modules: [moduleState("hostinfo"), moduleState("firewall"), moduleState("docker"), moduleState("npm"), moduleState("tailscale", { enabled: false })],
    ingestion: { ok: true, since: NOW, error: null },
  },
  {
    id: OFFLINE_HOST_ID,
    name: "db-1",
    isLocal: false,
    state: "enrolled",
    online: false,
    lastSeenAt: null,
    agentVersion: "1.0.0",
    protocolVersion: 1,
    os: "debian-12",
    arch: "amd64",
    supported: true,
    modules: [
      moduleState("hostinfo"),
      moduleState("firewall", { available: false, reason: "UFW is not installed." }),
      moduleState("docker", { enabled: false }),
    ],
    ingestion: { ok: false, since: NOW, error: "The agent has not answered since 08:12." },
  },
]

function fleetHost(id: string, name: string, over: Partial<Host> = {}): Host {
  return {
    id,
    name,
    isLocal: false,
    state: "enrolled",
    online: true,
    lastSeenAt: NOW,
    agentVersion: "1.0.0",
    protocolVersion: 1,
    os: "debian-12",
    arch: "amd64",
    supported: true,
    modules: [moduleState("hostinfo")],
    ingestion: { ok: true, since: NOW, error: null },
    ...over,
  }
}

const worstHosts: Host[] = [
  ...hosts,
  fleetHost(LONG_HOST_ID, `web-${"x".repeat(59)}`, { agentVersion: `1.0.0-beta.1+${"a".repeat(27)}` }),
  fleetHost(PENDING_HOST_ID, "new-1", {
    state: "pending",
    online: false,
    lastSeenAt: null,
    agentVersion: null,
    protocolVersion: null,
    os: null,
    arch: null,
    modules: [],
    ingestion: null,
  }),
  fleetHost(REVOKED_HOST_ID, "old-1", { state: "revoked", online: false, supported: false, agentVersion: "0.9.0" }),
  ...Array.from({ length: 45 }, (_, i) => {
    const n = String(i + 1).padStart(2, "0")
    return fleetHost(`01JE000000000000000000N0${n}`, `node-${n}`)
  }),
]

function hoursBefore(hours: number): string {
  return new Date(Date.parse(NOW) - hours * 3_600_000).toISOString()
}

function transaction(n: number, fromVersion: string, toVersion: string, state: string, error: string | null): UpdateTransaction {
  return {
    id: `tx-${n}`,
    operationId: `op-${n}`,
    parentId: null,
    kind: "update",
    fromVersion,
    toVersion,
    targetVersion: toVersion,
    state,
    error: error === null ? null : { code: "update.health", message: error },
    startedAt: hoursBefore(24 * n),
    updatedAt: hoursBefore(24 * n - 1),
  }
}

const worstTransactions: UpdateTransaction[] = [
  transaction(1, "1.0.0", "1.1.0", "rolled-back", "the health check failed"),
  transaction(2, "0.9.0", "1.0.0", "committed", null),
  transaction(3, "0.8.0", "0.9.0", "committed", null),
]

function rolloutHosts(stateAt: (position: number) => string, error: (position: number) => string | null = () => null): RolloutHost[] {
  return worstHosts.map((h, position) => {
    const message = error(position)
    return {
      hostId: h.id,
      position,
      state: stateAt(position),
      planId: null,
      operationId: null,
      error: message === null ? null : { code: "update.health", message },
    }
  })
}

function rollout(n: number, over: Partial<Rollout>): Rollout {
  return {
    id: `rollout-${String(n).padStart(2, "0")}`,
    version: `1.2.${20 - n}`,
    channel: "stable",
    state: "succeeded",
    error: null,
    createdAt: hoursBefore(n),
    updatedAt: hoursBefore(n - 0.5),
    hosts: rolloutHosts(() => "committed"),
    ...over,
  }
}

const HEALTH = "web-1 failed its health check"

const worstRollouts: Rollout[] = [
  rollout(1, { version: "1.3.0", state: "running", updatedAt: NOW, hosts: rolloutHosts((p) => (p < 10 ? "committed" : p === 10 ? "updating" : "waiting")) }),
  rollout(2, {
    version: "1.2.9",
    channel: "beta",
    state: "failed",
    error: { code: "rollout.failed", message: HEALTH },
    hosts: rolloutHosts((p) => (p === 0 ? "failed" : "skipped"), (p) => (p === 0 ? HEALTH : null)),
  }),
  rollout(3, { state: "cancelled", hosts: rolloutHosts((p) => (p === 0 ? "committed" : "skipped")) }),
  ...Array.from({ length: 17 }, (_, i) => rollout(i + 4, {})),
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

// The named states of the System overview (UX-7) and the Docker install
// panel (UX-8). "default" is the dataset every other state starts from: web-1
// online with the system module, jq installed, three upgradable packages,
// docker-ce running and the firewall's Docker integration off.
export const SYSTEM_STATES = [
  "default",
  "loading",
  "offline",
  "disabled",
  "upToDate",
  "aptBusy",
  "failedSources",
  "dpkgInterrupted",
  "failedChange",
  "agentTooOld",
  "error",
  "dockerAbsent",
  "dockerDistro",
  "dockerStopped",
  "installing",
  "awaitingConfirm",
  "rolledBack",
  "installFailed",
] as const
export type SystemState = (typeof SYSTEM_STATES)[number]

// The install panel states reached by starting an install in the browser:
// the panel follows the change its own apply answered.
export const INSTALL_STATES: SystemState[] = ["installing", "awaitingConfirm", "rolledBack", "installFailed"]
const DOCKER_ABSENT: SystemState[] = ["dockerAbsent", "agentTooOld", ...INSTALL_STATES]

const summary: Summary = {
  hostname: "web-1",
  os: { id: "debian", versionId: "12", prettyName: "Debian GNU/Linux 12 (bookworm)" },
  kernel: "6.1.0-26-amd64",
  arch: "amd64",
  uptimeSeconds: 3_553_200,
  bootTime: "2026-09-25T10:00:00Z",
  cpu: { model: "AMD EPYC 7763", logicalCount: 4 },
  memory: { totalBytes: 8 * 2 ** 30, availableBytes: 1.8 * 2 ** 30 },
  swap: { totalBytes: 2 * 2 ** 30, freeBytes: 1.6 * 2 ** 30 },
  load: { one: 0.42, five: 0.37, fifteen: 0.31 },
  rootFs: { totalBytes: 100 * 2 ** 30, availableBytes: 59 * 2 ** 30 },
  darkpanelVersion: "1.0.0",
}

const defaultUpdates: Updates = {
  upgradable: [
    { name: "libssl3", from: "3.0.15-1~deb12u1", to: "3.0.17-1~deb12u2", origin: "bookworm-security", security: true },
    { name: "curl", from: "7.88.1-10+deb12u8", to: "7.88.1-10+deb12u12", origin: "bookworm-updates", security: false },
    { name: "tzdata", from: "2024b-0+deb12u1", to: "2025b-0+deb12u1", origin: "bookworm-updates", security: false },
  ],
  removals: [],
  heldBack: [],
  refreshedAt: "2026-11-05T08:40:00Z",
  failedSources: [],
  dpkgInterrupted: false,
}

function installedPackage(name: string, version: string, description: string, locked: string | null = null): InstalledPackage {
  return { name, version, arch: "amd64", description, protected: locked }
}

const defaultInstalled: InstalledPackage[] = [
  installedPackage("bash", "5.2.15-2+b7", "GNU Bourne Again SHell", "Removing it would break the system"),
  installedPackage("curl", "7.88.1-10+deb12u8", "command line tool for transferring data with URL syntax"),
  installedPackage("docker-ce", "5:27.3.1-1~debian.12~bookworm", "Docker: the open-source application container engine"),
  installedPackage("jq", "1.6-2.1", "lightweight and flexible command-line JSON processor"),
  installedPackage("sudo", "1.9.13p3-1+deb12u1", "Provide limited super user privileges", "Removing it would lock you out of administration"),
]

// Names a package search finds, beside the installed packages it matches.
const SEARCHABLE: Record<string, string> = { htop: "3.2.2-2", jq: "1.6-2.1", "jq-doc": "1.6-2.1" }

const dockerCe: DockerState = { state: "docker-ce", version: "27.3.1", updateAvailable: false, running: true, containers: 2, images: 3, volumes: 1, labelled: [] }

const APT_BUSY = "Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 4242 (apt-get)"

// SystemContext is one mockApi call's System dataset and what the test's
// changes did to it.
interface SystemContext {
  state: SystemState
  installed: Map<string, InstalledPackage>
  upgraded: boolean
  dockerInstalled: boolean
  // Whether the fixtures stand for the System tests: the firewall's Docker
  // integration is then off, so the uninstall flow needs no firewall step.
  integrationOff: boolean
}

function systemContext(state: SystemState | undefined, worstCase: boolean): SystemContext {
  const installed = worstCase ? installedFixture : defaultInstalled
  return {
    state: state ?? "default",
    installed: new Map(installed.map((p) => [p.name, p])),
    upgraded: false,
    dockerInstalled: false,
    integrationOff: state !== undefined,
  }
}

function dockerReason(sys: SystemContext): string | null {
  if (sys.dockerInstalled) return null
  if (DOCKER_ABSENT.includes(sys.state)) return "Docker Engine is not installed."
  if (sys.state === "dockerDistro") return "The distribution's docker.io package is installed."
  if (sys.state === "dockerStopped") return "The Docker daemon does not answer."
  return null
}

// systemHost applies the System state to web-1, the host the tests visit.
function systemHost(h: Host, sys: SystemContext): Host {
  if (h.id !== HOST_ID) return h
  const reason = dockerReason(sys)
  const modules = h.modules.map((m) => (m.id === "docker" && reason !== null ? { ...m, available: false, reason } : m))
  if (sys.state !== "agentTooOld") modules.splice(1, 0, moduleState("system", { enabled: sys.state !== "disabled" }))
  return { ...h, modules, online: sys.state !== "offline" }
}

function systemUpdates(sys: SystemContext, worstCase: boolean): Updates {
  if (sys.upgraded || sys.state === "upToDate") return { ...defaultUpdates, upgradable: [], refreshedAt: NOW }
  const base = worstCase ? updatesFixture : defaultUpdates
  if (sys.state === "failedSources") return { ...base, failedSources: ["http://deb.example.com/debian bookworm InRelease", "https://packages.example.org/apt stable Release"] }
  if (sys.state === "dpkgInterrupted") return { ...base, dpkgInterrupted: true }
  return base
}

function systemDocker(sys: SystemContext): DockerState {
  if (sys.dockerInstalled) return dockerCe
  if (DOCKER_ABSENT.includes(sys.state)) return { state: "absent" }
  if (sys.state === "dockerDistro") return { state: "distro", conflicts: ["docker.io", "containerd", "runc"] }
  if (sys.state === "dockerStopped") return { ...dockerCe, running: false }
  return dockerCe
}

// systemResource answers a System resource read: a value, "hang" for a read
// that never answers, or a problem.
function systemResource(name: string, sys: SystemContext, worstCase: boolean): Reply | "hang" | null {
  if (sys.state === "loading") return "hang"
  if (sys.state === "error") return problem(502, "agent.unreachable", "The agent did not answer within 10 seconds.")
  switch (name) {
    case "updates":
      return { json: systemUpdates(sys, worstCase) }
    case "packages":
      return { json: [...sys.installed.values()] }
    case "reboot":
      return { json: worstCase ? rebootFixture : { required: false, packages: [], kernel: null, services: [] } }
    case "autoupdates":
      return { json: { installed: true, enabled: true } }
    case "docker":
      return { json: systemDocker(sys) }
    default:
      return null
  }
}

function searchHits(term: string, sys: SystemContext): { name: string; candidate: string; installed: boolean }[] {
  return Object.entries(SEARCHABLE)
    .filter(([name]) => name.includes(term))
    .map(([name, candidate]) => ({ name, candidate, installed: sys.installed.has(name) }))
}

// The host's last package change, for the failed-change states of UX-7.
function seededChanges(sys: SystemContext): Change[] {
  const failed = (code: string, message: string) =>
    change({ id: "change-apt-failed", action: "system.packages.upgrade", class: "irreversible", state: "failed", error: { code, message } })
  if (sys.state === "aptBusy") return [failed("system.apt_busy", APT_BUSY)]
  if (sys.state === "failedChange") {
    return [failed("system.broken_packages", ["apt failed on libfoo1:", "The following packages have unmet dependencies:", " libfoo1 : Depends: libbar2 (>= 2.1) but 2.0-1 is to be installed", "E: Unmet dependencies."].join("\n"))]
  }
  return []
}

function names(input: Record<string, unknown>): string[] {
  return Array.isArray(input.names) ? input.names.filter((n): n is string => typeof n === "string") : []
}

function systemPlan(name: string, input: Record<string, unknown>, host: Host, sys: SystemContext, worstCase: boolean): Reply | null {
  const base = { planId: `plan-${name}`, hostId: HOST_ID, action: name }
  switch (name) {
    case "system.packages.upgrade": {
      const all = systemUpdates(sys, worstCase).upgradable
      const chosen = input.all === true ? all : all.filter((u) => names(input).includes(u.name))
      return { json: plan({ ...base, diff: chosen.map((u) => ({ op: "~", text: `${u.name} ${u.from} -> ${u.to}` })), confirmation: host.name }) }
    }
    case "system.packages.install":
      return { json: plan({ ...base, diff: names(input).map((n) => ({ op: "+", text: `${n} ${SEARCHABLE[n] ?? ""}`.trim() })) }) }
    case "system.packages.remove":
      return { json: plan({ ...base, diff: names(input).map((n) => ({ op: "-", text: n })), confirmation: host.name }) }
    case "system.host.reboot":
      return { json: plan({ ...base, diff: [{ op: "~", text: `reboot ${host.name} in 1 minute` }], confirmation: host.name }) }
    case "system.index.refresh":
    case "system.packages.repair":
    case "system.autoupdates.set":
    case "system.docker.engine-update":
      return { json: plan({ ...base, diff: [{ op: "~", text: name }] }) }
    case "system.docker.uninstall": {
      const purge = input.purge === true
      const diff = [{ op: "-", text: "docker-ce containerd.io docker-ce-cli docker-buildx-plugin docker-compose-plugin" }]
      if (purge) diff.push({ op: "-", text: "/var/lib/docker and /var/lib/containerd (2 containers, 3 images, 1 volume)" })
      return { json: plan({ ...base, diff, confirmation: purge ? `purge docker on ${host.name}` : host.name }) }
    }
    case "system.docker.install":
      return {
        json: plan({
          ...base,
          hostId: null,
          diff: [
            { op: "+", text: "Docker's apt repository and signing key" },
            { op: "+", text: "docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin" },
            ...(input.firewallIntegration === true ? [{ op: "+", text: "firewall Docker integration" }] : []),
          ],
        }),
      }
    default:
      return null
  }
}

const INSTALL_CHANGE_ID = "01JE000000000000000000C003"

function installStep(ordinal: number, action: string, state: string, childDeadline: string | null = null): Step {
  return { id: `step-${ordinal}`, ordinal, host: HOST_ID, action, state, attempt: 1, childChangeId: `child-${ordinal}`, earlierChildChangeIds: null, childDeadline, error: null }
}

// The install composite as the System state leaves it: running, waiting for
// the firewall step's confirmation, rolled back or failed.
function installChange(sys: SystemContext, firewall: boolean): { change: Change; steps: Step[] } {
  const deadline = new Date(Date.now() + 900_000).toISOString()
  const base = { id: INSTALL_CHANGE_ID, hostId: null, action: "system.docker.install", class: "composite" }
  switch (sys.state) {
    case "installing":
      return { change: change({ ...base, state: "applying" }), steps: [installStep(0, "system.docker.check", "completed"), installStep(1, "system.docker.engine-install", "applying")] }
    case "rolledBack":
      return { change: change({ ...base, state: "reverted" }), steps: [] }
    case "installFailed":
      return {
        change: change({
          ...base,
          state: "failed",
          error: { code: "system.firewall_flag_mismatch", message: "The firewall refused the Docker integration: UFW manages the FORWARD chain on this host." },
        }),
        steps: [],
      }
    default: {
      const steps = [installStep(0, "system.docker.check", "completed"), installStep(1, "system.docker.engine-install", "completed")]
      if (!firewall) return { change: change({ ...base, state: "completed" }), steps }
      return { change: change({ ...base, state: "awaiting_confirmation" }), steps: [...steps, installStep(2, "firewall.docker.enable", "applied", deadline)] }
    }
  }
}

// systemApply applies a System plan: the change, and its effect on the
// dataset the next reads answer.
function systemApply(name: string, input: Record<string, unknown>, sys: SystemContext, steps: Map<string, Step[]>): Change | null {
  const done = (output: unknown = null) => change({ id: `change-${name}`, hostId: HOST_ID, action: name, class: "irreversible", state: "completed", output })
  switch (name) {
    case "system.packages.upgrade":
      sys.upgraded = true
      return done()
    case "system.packages.install":
      for (const n of names(input)) sys.installed.set(n, installedPackage(n, SEARCHABLE[n] ?? "1.0-1", `the ${n} package`))
      return done()
    case "system.packages.remove":
      for (const n of names(input)) sys.installed.delete(n)
      return done()
    case "system.host.reboot":
      return done({ scheduledAt: new Date(Date.now() + 60_000).toISOString(), bootTimeBefore: summary.bootTime })
    case "system.index.refresh":
    case "system.packages.repair":
    case "system.autoupdates.set":
    case "system.docker.engine-update":
    case "system.docker.uninstall":
      return done()
    case "system.docker.install": {
      const { change: c, steps: s } = installChange(sys, input.firewallIntegration === true)
      steps.set(c.id, s)
      if (c.state === "completed") sys.dockerInstalled = true
      return c
    }
    default:
      return null
  }
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

// Changes applied during a test, so the reads after them show their effect.
interface Applied {
  revoked: Set<string>
  deleted: Set<string>
  rollouts: Rollout[]
}

const READS: Record<string, (worstCase: boolean, applied: Applied, sys: SystemContext) => unknown> = {
  "core.host.list": (worstCase, applied, sys) => ({
    hosts: (worstCase ? worstHosts : hosts)
      .filter((h) => !applied.deleted.has(h.id))
      .map((h) => systemHost(applied.revoked.has(h.id) ? { ...h, state: "revoked" } : h, sys)),
  }),
  "core.audit.list": (worstCase) => ({ entries: worstCase ? worstAudit : worstAudit.slice(0, 3), nextBefore: null }),
  "core.token.list": () => ({ tokens }),
  "core.update.status": (worstCase) => ({ transactions: worstCase ? worstTransactions : [] }),
  "core.update.rollouts": (worstCase, applied) => ({ rollouts: [...applied.rollouts, ...(worstCase ? worstRollouts : [])] }),
  "npm.instance.list": () => ({ instances: [], unreadable: [] }),
  "panel.exposure.status": () => ({ exposed: false }),
  "npm.instance.status": () => npmStatus,
  "docker.container.inspect": () => containerDetail,
}

const ACTOR_LONG = `token:${"deploy-pipeline-".repeat(3)}eu`

const worstAudit: AuditEntry[] = Array.from({ length: 25 }, (_, i) => ({
  id: `audit-${String(i).padStart(2, "0")}`,
  at: hoursBefore(i),
  actor: i % 5 === 4 ? ACTOR_LONG : "user:admin",
  hostId: i % 3 === 0 ? null : HOST_ID,
  action: ["system.packages.upgrade", "system.docker.install", "firewall.rule.add", "core.token.create", "system.host.reboot"][i % 5]!,
  version: 1,
  phase: "apply",
  changeId: `change-${i}`,
  input: {},
  result: i % 7 === 3 ? "error" : "ok",
  errorCode: i % 7 === 3 ? "system.apt_busy" : null,
  source: "127.0.0.1",
}))

const tokens = [
  { id: "tok-ci", name: "ci", scope: "read", createdAt: hoursBefore(400), expiresAt: "2099-01-01T00:00:00Z", lastUsedAt: hoursBefore(2), revokedAt: null },
  { id: "tok-deploy", name: `deploy-${"x".repeat(56)}`, scope: "operate", createdAt: hoursBefore(300), expiresAt: null, lastUsedAt: null, revokedAt: null },
  { id: "tok-old", name: "old", scope: "admin", createdAt: hoursBefore(900), expiresAt: hoursBefore(100), lastUsedAt: null, revokedAt: hoursBefore(120) },
]

const RESOURCES: Record<string, unknown> = {
  "hostinfo.summary": summary,
  "firewall.status": firewallStatus,
  "firewall.docker": firewallDocker,
  "firewall.exposure": exposure,
  "docker.containers": { containers },
  "docker.networks": { networks },
}

// plans and applies answer null for an action the fixtures do not cover, so
// mockApi records the request as unhandled.
function hostNamed(fleet: Host[], id: unknown): Host {
  return fleet.find((h) => h.id === id) ?? fleet[0]!
}

function scheduleText(schedule: unknown): string {
  if (schedule === null || typeof schedule !== "object") return "on demand"
  const { days, time } = schedule as { days: string[]; time: string }
  return `on ${days.join(", ")} at ${time}`
}

function plans(name: string, input: Record<string, unknown>, fleet: Host[]): Reply | null {
  switch (name) {
    case "core.host.revoke":
    case "core.host.delete":
    case "core.host.token": {
      const host = hostNamed(fleet, input.hostId)
      return { json: plan({ planId: `plan-${name}`, hostId: null, action: name, diff: [{ op: "-", text: `host ${host.name}: ${host.state}` }], confirmation: host.name }) }
    }
    case "core.host.update-policy":
      return {
        json: plan({
          planId: "plan-core.host.update-policy",
          hostId: null,
          action: name,
          diff: [
            { op: "-", text: "web-1 follows stable, on demand" },
            { op: "+", text: `web-1 follows ${String(input.channel)}, ${scheduleText(input.schedule)}` },
          ],
        }),
      }
    case "core.update.rollout":
      return { json: plan({ planId: "plan-core.update.rollout", hostId: null, action: name, diff: [{ op: "+", text: `rollout of ${String(input.version)}` }] }) }
    case "core.update.rollout-cancel":
      return { json: plan({ planId: "plan-core.update.rollout-cancel", hostId: null, action: name, diff: [{ op: "-", text: `rollout ${String(input.rolloutId)}` }] }) }
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

function coreOutput(name: string, input: Record<string, unknown>, fleet: Host[]): unknown {
  switch (name) {
    case "core.host.revoke":
      return { ...hostNamed(fleet, input.hostId), state: "revoked" }
    case "core.host.delete":
      return hostNamed(fleet, input.hostId)
    case "core.host.token":
      return { host: hostNamed(worstHosts, PENDING_HOST_ID), token: "dpe_fixture_token", expiresAt: NOW }
    case "core.host.update-policy":
      return { hostId: input.hostId, channel: input.channel, schedule: input.schedule }
    case "core.update.rollout": {
      const ids = (input.hosts as string[] | undefined) ?? []
      return {
        id: "rollout-new",
        version: input.version,
        channel: input.channel,
        state: "running",
        error: null,
        createdAt: NOW,
        updatedAt: NOW,
        hosts: ids.map((hostId, position) => ({ hostId, position, state: "waiting", planId: null, operationId: null, error: null })),
      }
    }
    case "core.update.rollout-cancel":
      return { ...worstRollouts[0]!, state: "cancelled" }
    default:
      return undefined
  }
}

function applies(name: string, changes: Map<string, Change>, input: Record<string, unknown>, fleet: Host[]): Reply | null {
  let applied: Change
  const output = coreOutput(name, input, fleet)
  if (output !== undefined) {
    applied = change({ id: `change-${name}`, hostId: null, action: name, class: "irreversible", state: "completed", output })
    changes.set(applied.id, applied)
    return { json: applied }
  }
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
// worstCase answers with the longest lists and names the host and rollout
// pages must hold: 50 hosts, three update transactions and 20 rollouts, and
// on the System pages 400 upgradable and 3000 installed packages with
// 70-character names. systemState picks a named System or Docker install
// state (SYSTEM_STATES) for web-1.
export async function mockApi(page: Page, opts: { signedIn: boolean; worstCase?: boolean; systemState?: SystemState }): Promise<Fixtures> {
  const fixtures: Fixtures = { unhandled: [] }
  const changes = new Map<string, Change>()
  const planned = new Map<string, Record<string, unknown>>()
  const applied: Applied = { revoked: new Set(), deleted: new Set(), rollouts: [] }
  const worstCase = opts.worstCase ?? false
  const sys = systemContext(opts.systemState, worstCase)
  const steps = new Map<string, Step[]>()
  await page.route(
    (url) => url.pathname.startsWith("/api/"),
    async (route) => {
      const request = route.request()
      const method = request.method()
      const path = new URL(request.url()).pathname
      const reply = answer(method, path, route, opts.signedIn, { changes, planned, applied, worstCase, sys, steps })
      if (reply === "hang") return
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

// The state one mockApi call keeps across requests. planned keeps each
// action's last planned input, because an apply carries only the plan id.
interface MockState {
  changes: Map<string, Change>
  planned: Map<string, Record<string, unknown>>
  applied: Applied
  worstCase: boolean
  sys: SystemContext
  steps: Map<string, Step[]>
}

function answer(method: string, path: string, route: Route, signedIn: boolean, state: MockState): Reply | "hang" | null {
  const { changes, planned, applied, worstCase, sys, steps } = state
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
    const fleet = (worstCase ? worstHosts : hosts).map((h) => systemHost(h, sys))
    switch (action[3]) {
      case "read": {
        if (name === "system.packages.search") {
          const term = bodyOf(route).input?.term
          return { json: { output: searchHits(typeof term === "string" ? term : "", sys) } }
        }
        const read = READS[name]
        return read === undefined ? null : { json: { output: read(worstCase, applied, sys) } }
      }
      case "plan": {
        const input = bodyOf(route).input ?? {}
        planned.set(name, input)
        return systemPlan(name, input, hostNamed(fleet, HOST_ID), sys, worstCase) ?? plans(name, input, fleet)
      }
      default: {
        const input = planned.get(name) ?? {}
        const system = systemApply(name, input, sys, steps)
        if (system !== null) {
          changes.set(system.id, system)
          return { json: system }
        }
        const reply = applies(name, changes, input, fleet)
        if (name === "core.host.revoke" && typeof input.hostId === "string") applied.revoked.add(input.hostId)
        if (name === "core.host.delete" && typeof input.hostId === "string") applied.deleted.add(input.hostId)
        if (name === "core.update.rollout") applied.rollouts.unshift(coreOutput(name, input, fleet) as Rollout)
        return reply
      }
    }
  }
  const resource = RESOURCE.exec(path)
  if (resource !== null && method === "GET") {
    if (resource[1] === "system") return systemResource(resource[2] ?? "", sys, worstCase)
    if (resource[1] === "firewall" && resource[2] === "docker" && sys.integrationOff) return { json: { ...firewallDocker, enabled: false } }
    const found = RESOURCES[`${resource[1]}.${resource[2]}`]
    return found === undefined ? null : { json: found }
  }
  if (path === "/api/v1/changes" && method === "GET") {
    return { json: { changes: [...[...changes.values()].filter((c) => c.hostId === HOST_ID).reverse(), ...seededChanges(sys)] } }
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
    if (verb === "steps") return { json: { steps: steps.get(found.id) ?? [] } }
    if (verb === "confirm" || verb === "revert") {
      const next = { ...found, state: verb === "confirm" ? "confirmed" : "reverted", revertDeadline: null }
      changes.set(next.id, next)
      if (found.action === "system.docker.install") {
        steps.set(found.id, (steps.get(found.id) ?? []).map((s) => ({ ...s, state: verb === "confirm" ? "confirmed" : "reverted", childDeadline: null })))
        sys.dockerInstalled = verb === "confirm"
      }
      return { json: next }
    }
    return { json: found }
  }
  return null
}
