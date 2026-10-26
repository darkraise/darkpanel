import type { DiffLine } from "@/api/types"

// The firewall module's id and the version of every firewall action.
export const FIREWALL_MODULE = "firewall"
export const FIREWALL_ACTION_VERSION = 1

// A rule of the firewall's model, as model.Rule encodes it (spec 2.2).
export type RuleAction = "allow" | "deny" | "limit" | "reject"
export type RuleDirection = "in" | "out" | "route"
export type RuleProto = "tcp" | "udp" | "any" | "ah" | "esp" | "gre" | "ipv6" | "igmp"
export type RuleFamily = "v4" | "v6" | "both"
export type RuleLog = "off" | "log" | "log-all"
export type RuleOrigin = "darkpanel" | "manual"

export interface Rule {
  id?: string
  action: RuleAction
  direction: RuleDirection
  proto: RuleProto
  from: string
  fromPort?: string
  to: string
  toPort?: string
  inIface?: string
  outIface?: string
  family: RuleFamily
  log?: RuleLog
  comment?: string
  origin?: RuleOrigin // darkpanel when omitted
  raw?: string // the tuple of a manual rule the model cannot express
}

export type Policy = "ACCEPT" | "DROP" | "REJECT"

// UFW's configured state, read from its files (spec 2.1).
export interface FirewallConfigured {
  enabled: boolean
  logLevel: string
  ipv6: boolean
  defaults: { incoming: Policy; outgoing: Policy; routed: Policy }
  sysctlFile?: string
}

// One chain of the loaded state.
export interface LoadedChain {
  family: "v4" | "v6"
  table: string
  chain: string
  policy?: string // built-in chains only
  rules: number
}

// The loaded state the kernel holds, in summary (spec 2.1).
export interface FirewallLoaded {
  variant: string
  ipv6: boolean // ip6tables is present
  sysctl: Record<string, string>
  chains: LoadedChain[]
}

// One chain whose loaded rules differ from what UFW's files predict.
export interface ChainDifference {
  family: "v4" | "v6"
  table: string
  chain: string
  policyPredicted?: string
  policyLoaded?: string
  onlyPredicted?: string[]
  onlyLoaded?: string[]
  orderDiffers?: boolean
}

// The firewall.status resource (spec 2.2).
export interface FirewallStatus {
  supported: boolean
  unsupported: string[] // why the configuration is unsupported
  active: boolean
  inSync: boolean
  outOfSync: ChainDifference[]
  syncError?: string // why sync could not be checked
  configured: FirewallConfigured
  loaded: FirewallLoaded
  rules: Rule[]
}

// One row of the firewall.snapshots resource (spec 2.5).
export interface SnapshotInfo {
  id: string
  createdAt: string
  changeId?: string // the change it was taken before; absent until that change applied
  actor: string
  system: boolean
  pinned: boolean
  baseline: boolean
  enabled: boolean // UFW's ENABLED in the snapshot
}

export interface SnapshotList {
  snapshots: SnapshotInfo[]
}

// One state file whose content differs between two states.
export interface FileChange {
  path: string
  from: string // "sha256:<hex>" or "absent"
  to: string
}

// The output of firewall.snapshot.compare.
export interface SnapshotComparison {
  from: string
  to: string // a snapshot id, or "now"
  diff: DiffLine[]
  files: FileChange[]
}

// The query keys of the firewall's resources; a module event refetches
// every ["resource", hostId] query.
export const statusQueryKey = (hostId: string) => ["resource", hostId, "firewall.status"] as const
export const snapshotsQueryKey = (hostId: string) => ["resource", hostId, "firewall.snapshots"] as const
