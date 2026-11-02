export const DERP_MODULE = "derp"
export const DERP_ACTION_VERSION = 1

export function derpKey(name: "servers" | "probe", hostId?: string): string[] {
  return hostId === undefined ? [DERP_MODULE, name] : [DERP_MODULE, name, hostId]
}

export type DerpHealth = "ok" | "degraded" | "broken"

export interface DerpServerView {
  hostId: string
  host: string
  mode: string
  hostName: string
  regionId: number
  regionCode: string
  derperVersion: string
  certName: string
  certNotAfter: string | null
  unitActive: boolean
  health: DerpHealth
  reasons: string[]
  checkedAt: string
  error: string
}

export interface DerpServers {
  servers: DerpServerView[]
  availableVersion: string
}

export interface Visibility {
  address: string
  visible: boolean
  node: string
}

export interface ServerStatus {
  installed: boolean
  mode: string
  hostName: string
  verifyClients: boolean
  derperVersion: string
  unitActive: boolean
  certName: string
  certNotAfter: string | null
  availableVersion: string
  linkViaTailnet: boolean
  onTailnet: boolean
  tags: string[]
  addresses: string[]
  visible: Visibility[]
}

export interface ProbeResult {
  ok: boolean
  latencyMs: number
  error: string
}

export interface RegionDiagnostics {
  info: string[]
  warnings: string[]
  errors: string[]
}

export interface ProbeSource {
  source: string
  name: string
  tls: ProbeResult
  stun: ProbeResult
  region: RegionDiagnostics | null
  error: string
}

export interface NodeUse {
  name: string
  address: string
  hostId: string
  visible: boolean
  preferred: string
  observed: boolean
}

export interface ServerProbe {
  hostId: string
  regionId: number
  regionCode: string
  server: ServerStatus
  sources: ProbeSource[]
  nodes: NodeUse[]
  ready: boolean
  warnings: string[]
}

const HEALTHS: readonly string[] = ["ok", "degraded", "broken"]

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

function isStrings(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((s) => typeof s === "string")
}

function isServerView(v: unknown): v is DerpServerView {
  return (
    isRecord(v) &&
    typeof v.hostId === "string" &&
    typeof v.host === "string" &&
    typeof v.mode === "string" &&
    typeof v.hostName === "string" &&
    typeof v.regionId === "number" &&
    typeof v.regionCode === "string" &&
    typeof v.derperVersion === "string" &&
    typeof v.certName === "string" &&
    (v.certNotAfter === null || typeof v.certNotAfter === "string") &&
    typeof v.unitActive === "boolean" &&
    typeof v.health === "string" &&
    HEALTHS.includes(v.health) &&
    isStrings(v.reasons) &&
    typeof v.checkedAt === "string" &&
    typeof v.error === "string"
  )
}

export function isDerpServers(v: unknown): v is DerpServers {
  return isRecord(v) && Array.isArray(v.servers) && v.servers.every(isServerView) && typeof v.availableVersion === "string"
}

function isVisibility(v: unknown): v is Visibility {
  return isRecord(v) && typeof v.address === "string" && typeof v.visible === "boolean" && typeof v.node === "string"
}

function isServerStatus(v: unknown): v is ServerStatus {
  return (
    isRecord(v) &&
    typeof v.installed === "boolean" &&
    typeof v.mode === "string" &&
    typeof v.hostName === "string" &&
    typeof v.verifyClients === "boolean" &&
    typeof v.derperVersion === "string" &&
    typeof v.unitActive === "boolean" &&
    typeof v.certName === "string" &&
    (v.certNotAfter === null || typeof v.certNotAfter === "string") &&
    typeof v.availableVersion === "string" &&
    typeof v.linkViaTailnet === "boolean" &&
    typeof v.onTailnet === "boolean" &&
    isStrings(v.tags) &&
    isStrings(v.addresses) &&
    Array.isArray(v.visible) &&
    v.visible.every(isVisibility)
  )
}

function isProbeResult(v: unknown): v is ProbeResult {
  return isRecord(v) && typeof v.ok === "boolean" && typeof v.latencyMs === "number" && typeof v.error === "string"
}

function isRegionDiagnostics(v: unknown): v is RegionDiagnostics {
  return isRecord(v) && isStrings(v.info) && isStrings(v.warnings) && isStrings(v.errors)
}

function isProbeSource(v: unknown): v is ProbeSource {
  return (
    isRecord(v) &&
    typeof v.source === "string" &&
    typeof v.name === "string" &&
    isProbeResult(v.tls) &&
    isProbeResult(v.stun) &&
    (v.region === null || isRegionDiagnostics(v.region)) &&
    typeof v.error === "string"
  )
}

function isNodeUse(v: unknown): v is NodeUse {
  return (
    isRecord(v) &&
    typeof v.name === "string" &&
    typeof v.address === "string" &&
    typeof v.hostId === "string" &&
    typeof v.visible === "boolean" &&
    typeof v.preferred === "string" &&
    typeof v.observed === "boolean"
  )
}

export function isServerProbe(v: unknown): v is ServerProbe {
  return (
    isRecord(v) &&
    typeof v.hostId === "string" &&
    typeof v.regionId === "number" &&
    typeof v.regionCode === "string" &&
    isServerStatus(v.server) &&
    Array.isArray(v.sources) &&
    v.sources.every(isProbeSource) &&
    Array.isArray(v.nodes) &&
    v.nodes.every(isNodeUse) &&
    typeof v.ready === "boolean" &&
    isStrings(v.warnings)
  )
}
