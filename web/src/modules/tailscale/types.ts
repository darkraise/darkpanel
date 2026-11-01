export const TAILSCALE_MODULE = "tailscale"
export const TAILSCALE_ACTION_VERSION = 1

export type PeerConnection = "direct" | "derp" | "peer-relay" | "none"

export interface TailscaleNode {
  id: string
  hostName: string
  dnsName: string
  os: string
  addresses: string[]
  tags: string[]
}

export interface TailscalePeer extends TailscaleNode {
  online: boolean
  connection: PeerConnection
  endpoint: string
  region: string
  exitNode: boolean
  exitNodeOption: boolean
  lastHandshake: string | null
}

export interface PrefsFields {
  wantRunning?: boolean
  advertiseRoutes?: string[]
  exitNode?: string
  advertiseExitNode?: boolean
  acceptRoutes?: boolean
  shieldsUp?: boolean
  runSSH?: boolean
}

export interface TailscaleStatus {
  version: string
  backendState: string // tailscaled may add states, so this is not a union
  self: TailscaleNode | null
  tailnet: string
  magicDNSSuffix: string
  preferredDerp: string
  runSSH: boolean
  prefs?: PrefsFields // the agent always sends it, with all seven members
  peers: TailscalePeer[]
  health: string[]
}

export interface Measurement {
  available: boolean
  medianMs: number
  reason: string
}

export interface RegionLatency {
  regionId: number
  code: string
  name: string
  stun: Measurement
  https: Measurement
}

export interface TailscaleLatency {
  regions: RegionLatency[]
}

const CONNECTIONS: readonly string[] = ["direct", "derp", "peer-relay", "none"]

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

function isStrings(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((s) => typeof s === "string")
}

function isNode(v: unknown): v is TailscaleNode {
  return (
    isRecord(v) &&
    typeof v.id === "string" &&
    typeof v.hostName === "string" &&
    typeof v.dnsName === "string" &&
    typeof v.os === "string" &&
    isStrings(v.addresses) &&
    isStrings(v.tags)
  )
}

function isPeer(v: unknown): v is TailscalePeer {
  return (
    isNode(v) &&
    isRecord(v) &&
    typeof v.online === "boolean" &&
    typeof v.exitNode === "boolean" &&
    typeof v.exitNodeOption === "boolean" &&
    typeof v.connection === "string" &&
    CONNECTIONS.includes(v.connection) &&
    typeof v.endpoint === "string" &&
    typeof v.region === "string" &&
    (v.lastHandshake === null || typeof v.lastHandshake === "string")
  )
}

export function isTailscaleStatus(v: unknown): v is TailscaleStatus {
  return (
    isRecord(v) &&
    typeof v.version === "string" &&
    typeof v.backendState === "string" &&
    typeof v.tailnet === "string" &&
    typeof v.magicDNSSuffix === "string" &&
    typeof v.preferredDerp === "string" &&
    typeof v.runSSH === "boolean" &&
    (v.prefs === undefined || isRecord(v.prefs)) &&
    (v.self === null || isNode(v.self)) &&
    Array.isArray(v.peers) &&
    v.peers.every(isPeer) &&
    isStrings(v.health)
  )
}

function isMeasurement(v: unknown): v is Measurement {
  return isRecord(v) && typeof v.available === "boolean" && typeof v.medianMs === "number" && typeof v.reason === "string"
}

function isRegion(v: unknown): v is RegionLatency {
  return (
    isRecord(v) &&
    typeof v.regionId === "number" &&
    typeof v.code === "string" &&
    typeof v.name === "string" &&
    isMeasurement(v.stun) &&
    isMeasurement(v.https)
  )
}

export function isTailscaleLatency(v: unknown): v is TailscaleLatency {
  return isRecord(v) && Array.isArray(v.regions) && v.regions.every(isRegion)
}
