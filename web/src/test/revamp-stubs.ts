import { fwStatus } from "@/modules/firewall/fixtures"
import type { StubHandler } from "./utils"

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
}

export function screenStubs(name: string): ScreenStubs {
  const s = stubs[name]
  if (s === undefined) throw new Error(`no revamp stubs for ${name}`)
  return s
}
