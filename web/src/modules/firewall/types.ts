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
export const dockerQueryKey = (hostId: string) => ["resource", hostId, "firewall.docker"] as const
export const exposureQueryKey = (hostId: string) => ["resource", hostId, "firewall.exposure"] as const

// The transport protocol of a Docker binding.
export type DockerProto = "tcp" | "udp"

// One finding of the Docker integration's health check.
export interface DockerIssue {
  level: "degraded" | "broken"
  reason: string
}

// A DNAT forward that stays open to private sources; an absent port is any port.
export interface Exemption {
  inInterface?: string
  origDestination: string
  origPort?: number
  destination: string
  proto: DockerProto
  port?: number
}

// The Docker integration's settings.
export interface DockerSettings {
  privateSources: string[]
  forwardExemptions: Exemption[]
}

// A published port of a container selection; an absent hostAddr is every host address.
export interface Binding {
  selector: string // a container name, or <compose project>/<service>
  hostAddr?: string
  hostPort: number
  proto: DockerProto
}

// A stored allow or deny of a binding; an absent from is any source and only an allow has none.
export interface ContainerRule {
  id: string
  binding: Binding
  kind: "allow" | "deny"
  from?: string
}

// A Docker network and whether the block protects its bridge.
export interface DockerNetwork {
  name: string
  id: string
  bridge: string
  subnets: string[]
  protected: boolean
  reason?: string // why the network is unprotected
}

// A binding as realized against the running containers.
export interface DockerBinding {
  binding: Binding
  id: string
  containers: string[]
  containerPort: number
  allowed: string[] // sources, "any" for every source
  denied: string[]
  proxied?: string[] // families served by docker-proxy
  privateReach?: string[] // privateSources ranges that reach it only through the exemption
}

// The firewall.docker resource.
export interface DockerStatus {
  available: boolean
  reason?: string
  version?: string
  driver?: string
  dockerVariant?: string
  ufwVariant?: string
  rawProtection: boolean
  supported: boolean
  unsupported?: string[]
  enabled: boolean
  health: "off" | "ok" | "degraded" | "broken" | "unsupported"
  issues?: DockerIssue[]
  foreignRules?: string[]
  foreignForwards?: Exemption[] // proposed exemptions for uncovered foreign DNAT rules
  manualRoutes?: string[] // manual route rules to containers: no effect without a Darkpanel binding
  networks: DockerNetwork[]
  settings: DockerSettings
  rules: ContainerRule[]
  bindings: DockerBinding[]
}

// The sources class an exposure verdict speaks for.
export type ExposureClass = "internet" | "private" | "tailnet" | "localhost"

// The accepted sources of a partly reachable target and the rule that accepts them.
export interface ExposureAllowed {
  sources: string[]
  ifaces?: string[]
  srcPorts?: string[]
  decidedBy: string
}

// Whether one class of sources reaches a listening port.
export interface ExposureVerdict {
  class: ExposureClass
  range?: string // private only
  verdict: "open" | "closed" | "filtered" | "partial" | "unknown"
  decidedBy?: string
  allowed?: ExposureAllowed[]
  note?: string
}

// The container behind a published port.
export interface ExposureContainer {
  name: string
  binding: Binding
  containerPort: number
  proxied: boolean
}

// One listening port with a verdict per source class.
export interface ExposureRow {
  proto: string
  addr: string
  port: number
  family: string
  process?: string
  container?: ExposureContainer
  classes: ExposureVerdict[]
}

// The firewall.exposure resource.
export interface ExposureView {
  rows: ExposureRow[]
  notes?: string[] // out of sync, unsupported, Docker unavailable
}
