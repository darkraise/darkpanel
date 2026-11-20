import type { Status } from "@/components/visual/status"

export const CLOUDFLARE_ACTION_VERSION = 1

// ["cloudflare"] is the prefix CloudflareChange invalidates when its dialog closes.
export function cloudflareKey(name: "account" | "tunnels" | "hostnames") {
  return ["cloudflare", name] as const
}

export interface CloudflareAccountView {
  accountId: string
  capabilities: { name: string; state: "ok" | "missing" | "unverified"; detail?: string }[]
  connectedAt: string
}

export interface CloudflareAccountStatus {
  connected: boolean
  account?: CloudflareAccountView
}

export interface ConnectionView {
  id: string
  colo: string
  originIp: string
  openedAt: string
  version: string
}

export interface TunnelView {
  id: string
  name: string
  status: "inactive" | "degraded" | "healthy" | "down" | "unknown"
  connections: ConnectionView[] | null // null when Cloudflare could not be read
  drift: boolean
  hostnames: number
  createdAt: string
}

export interface HostnameView {
  hostname: string
  tunnel: string
  service: string
  recordId?: string
  phase: "adding_rule" | "adding_record" | "active" | "removing_record" | "removing_rule"
  health: "ok" | "rule_missing" | "record_missing" | "record_changed" | "unknown"
  checkedAt?: string
}

export interface StateView {
  status: Status
  label: string
}

const TUNNEL_STATUS: Record<TunnelView["status"], StateView> = {
  healthy: { status: "ok", label: "Healthy" },
  degraded: { status: "warn", label: "Degraded" },
  down: { status: "error", label: "Down" },
  inactive: { status: "off", label: "Inactive" },
  unknown: { status: "off", label: "Unknown" },
}

export function tunnelStatus(status: TunnelView["status"]): StateView {
  return TUNNEL_STATUS[status]
}

const PHASE: Record<HostnameView["phase"], StateView> = {
  active: { status: "ok", label: "Active" },
  adding_rule: { status: "info", label: "Adding rule" },
  adding_record: { status: "info", label: "Adding record" },
  removing_record: { status: "info", label: "Removing record" },
  removing_rule: { status: "info", label: "Removing rule" },
}

export function hostnamePhase(phase: HostnameView["phase"]): StateView {
  return PHASE[phase]
}

const HEALTH: Record<HostnameView["health"], StateView> = {
  ok: { status: "ok", label: "OK" },
  rule_missing: { status: "error", label: "Rule missing" },
  record_missing: { status: "error", label: "Record missing" },
  record_changed: { status: "error", label: "Record changed" },
  unknown: { status: "off", label: "Unknown" },
}

export function hostnameHealth(health: HostnameView["health"]): StateView {
  return HEALTH[health]
}

// connectorState maps the Engine's container state; "missing" means the container is gone.
export function connectorState(state: string): StateView {
  if (state === "running") return { status: "ok", label: "Running" }
  if (state === "missing") return { status: "error", label: "Missing" }
  return { status: "warn", label: state.charAt(0).toUpperCase() + state.slice(1) }
}
