import type { Capability } from "@/components/capability-list"

export const TAILNET_ACTION_VERSION = 1

export function tailnetKey(name: "account" | "devices" | "keys" | "policy"): string[] {
  return ["tailnet", name]
}

export interface TailnetAccountView {
  tailnet: string
  clientId: string
  tagsAllowed: string[]
  capabilities: Capability[]
  connectedAt: string
}

export interface TailnetAccountStatus {
  connected: boolean
  account?: TailnetAccountView
}

export interface TailnetDevice {
  id: string
  name: string
  hostname: string
  os: string
  user: string
  addresses: string[]
  tags: string[]
  authorized: boolean
  lastSeen?: string
  expires?: string
  keyExpiryDisabled: boolean
  advertisedRoutes: string[]
  enabledRoutes: string[]
  exitNode: boolean
  exitNodeEnabled: boolean
}

export interface TailnetDeviceRoutes {
  advertisedRoutes: string[]
  enabledRoutes: string[]
}

export interface AuthKeyRow {
  description: string
  kind: string
  ownerId: string
  keyId?: string
  state: string
  requestedAt: string
  expires: string
}

export interface CreatedKey {
  id: string
  key: string
  expires: string
  description: string
}

export interface TailnetPolicy {
  text: string
  etag: string
}

export interface PolicyEdit {
  op: "set" | "delete" | "addItem" | "removeItem"
  path: string
  value?: unknown
}
