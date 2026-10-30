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
  connections: ConnectionView[]
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
