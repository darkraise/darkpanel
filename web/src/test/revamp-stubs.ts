import { cfConnector, cfTunnel } from "@/modules/cloudflare/fixtures"
import { serversFixture } from "@/modules/derp/fixtures"
import { dkContainer, dkImage } from "@/modules/docker/fixtures"
import { fwDocker, fwStatus } from "@/modules/firewall/fixtures"
import { npInstance } from "@/modules/npm/fixtures"
import { paExposure, paStatus } from "@/modules/panel/fixtures"
import { tnDevice } from "@/modules/tailnet/fixtures"
import { makeHost, type StubHandler } from "./utils"

export interface ScreenStubs {
  path: string
  props: Record<string, unknown>
  routes: Record<string, StubHandler>
}

const summary = {
  hostname: "web-1", os: { id: "ubuntu", versionId: "22.04", prettyName: "Ubuntu 22.04.5 LTS" }, kernel: "5.15.0",
  arch: "amd64", uptimeSeconds: 60, bootTime: "2026-10-19T08:00:00Z", cpu: { model: "QEMU", logicalCount: 2 },
  memory: { totalBytes: 2147483648, availableBytes: 1073741824 }, swap: { totalBytes: 0, freeBytes: 0 },
  load: { one: 0, five: 0, fifteen: 0 }, rootFs: { totalBytes: 10737418240, availableBytes: 5368709120 }, darkpanelVersion: "0.1.0",
}

const tailscaleStatus = {
  version: "1.104.1", backendState: "Running",
  self: { id: "n-self", hostName: "web", dnsName: "web.tail1234.ts.net", os: "linux", addresses: ["100.64.0.1"], tags: ["tag:server"] },
  tailnet: "example.com", magicDNSSuffix: "tail1234.ts.net", preferredDerp: "fra", runSSH: true,
  peers: [{
    id: "n-peer", hostName: "db", dnsName: "db.tail1234.ts.net", os: "linux", addresses: ["100.64.0.7"], tags: [],
    online: true, connection: "direct", endpoint: "203.0.113.9:41641", region: "", exitNode: false, exitNodeOption: false, lastHandshake: null,
  }],
  health: [],
}

const stubs: Record<string, ScreenStubs> = {
  SummaryPage: {
    path: "/hosts/h1/hostinfo",
    props: { hostId: "h1" },
    routes: { "GET /api/v1/hosts/h1/resources/hostinfo/summary": { body: summary } },
  },
  FirewallPage: {
    path: "/hosts/h1/firewall",
    props: { hostId: "h1" },
    routes: { "GET /api/v1/hosts/h1/resources/firewall/status": { body: fwStatus() } },
  },
  DockerPage: {
    path: "/hosts/h1/docker",
    props: { hostId: "h1" },
    routes: {
      "GET /api/v1/hosts/h1/resources/docker/containers": { body: { containers: [dkContainer({ id: "c1", name: "web" })] } },
      "GET /api/v1/hosts/h1/resources/docker/images": { body: { images: [dkImage({ tags: ["nginx:1.27"] })] } },
      "GET /api/v1/hosts/h1/resources/docker/networks": { body: { networks: [] } },
      "GET /api/v1/hosts/h1/resources/docker/volumes": { body: { volumes: [] } },
      "GET /api/v1/hosts/h1/resources/docker/projects": { body: { projects: [] } },
    },
  },
  TailscalePage: {
    path: "/hosts/h1/tailscale",
    props: { hostId: "h1" },
    routes: { "GET /api/v1/hosts/h1/resources/tailscale/status": { body: tailscaleStatus } },
  },
  DevicesPage: {
    path: "/tailnet/devices",
    props: {},
    routes: {
      "POST /api/v1/actions/tailnet.device.list/v1/read": {
        body: { output: { devices: [tnDevice({ id: "n1", name: "web-1.example.ts.net", authorized: true }), tnDevice({ id: "n2", name: "laptop.example.ts.net", authorized: false, advertisedRoutes: [], enabledRoutes: [] })] } },
      },
    },
  },
  DerpPage: {
    path: "/derp",
    props: {},
    routes: { "POST /api/v1/actions/derp.server.list/v1/read": { body: { output: serversFixture } } },
  },
  NpmPage: {
    path: "/hosts/h1/npm",
    props: { hostId: "h1" },
    routes: {
      "POST /api/v1/hosts/h1/actions/npm.instance.status/v1/read": {
        body: { output: npInstance({ deployed: true, containerId: "c0ffee", running: true, apiReady: true, public: "confirmed", adminEmail: "admin@example.com" }) },
      },
      "GET /api/v1/hosts/h1/resources/firewall/docker": { body: fwDocker() },
    },
  },
  ConnectorsPage: {
    path: "/cloudflare/connectors",
    props: {},
    routes: {
      "POST /api/v1/actions/core.host.list/v1/read": {
        body: { output: { hosts: [makeHost({ modules: [{ id: "cloudflare", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })] } },
      },
      "POST /api/v1/actions/cloudflare.tunnel.list/v1/read": { body: { output: { tunnels: [cfTunnel({ id: "t1", name: "darkpanel-c1" })] } } },
      "POST /api/v1/hosts/h1/actions/cloudflare.connector.list/v1/read": { body: { output: { connectors: [cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1" })] } } },
      "GET /api/v1/hosts/h1/modules/cloudflare/availability": { body: { module: "cloudflare", available: true, reason: null, actions: [] } },
    },
  },
  PanelPage: {
    path: "/hosts/h1/panel",
    props: { hostId: "h1" },
    routes: {
      "POST /api/v1/actions/panel.exposure.status/v1/read": { body: { output: paStatus(paExposure()) } },
      "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [makeHost({ isLocal: true })] } } },
    },
  },
  AuditPage: {
    path: "/audit",
    props: {},
    routes: {
      "POST /api/v1/actions/core.audit.list/v1/read": {
        body: {
          output: {
            entries: [{ id: "a1", at: "2026-10-19T08:00:00Z", actor: "user:u1", hostId: "h1", action: "core.token.create", version: 1, phase: "apply", changeId: "c1", input: {}, result: "ok", errorCode: null, source: "127.0.0.1" }],
            nextBefore: null,
          },
        },
      },
    },
  },
  RolloutsPage: {
    path: "/rollouts",
    props: {},
    routes: {
      "POST /api/v1/actions/core.update.rollouts/v1/read": {
        body: {
          output: {
            rollouts: [{
              id: "r1", version: "1.2.3", channel: "stable", state: "running", error: null, createdAt: "2026-11-11T08:00:00Z", updatedAt: "2026-11-11T08:01:00Z",
              hosts: [{ hostId: "h1", position: 0, state: "committed", planId: null, operationId: null, error: null }],
            }],
          },
        },
      },
      "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [makeHost()] } } },
    },
  },
}

export function screenStubs(name: string): ScreenStubs {
  const s = stubs[name]
  if (s === undefined) throw new Error(`no revamp stubs for ${name}`)
  return s
}
