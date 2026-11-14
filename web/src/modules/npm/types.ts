export const NPM_MODULE = "npm"
export const NPM_ACTION_VERSION = 1
export const NPM_DEPLOY_PUBLIC_VERSION = 2

export function npmKey(hostId: string, name: string): unknown[] {
  return ["resource", hostId, "npm." + name]
}

export const npmFleetKey = [NPM_MODULE, "instances"]

export interface AdminExposure {
  hostname: string
  proxyHostId: number
  certificateId: number
}

export interface InstanceStatus {
  deployed: boolean
  containerId: string
  image: string
  pinnedImage: string
  running: boolean
  apiReady: boolean
  public: "no" | "confirmed"
  adminEmail: string
  adminExposure?: AdminExposure | null
  upgradeAvailable: boolean
}

export interface HostInstance {
  hostId: string
  hostName: string
  status: InstanceStatus
}

export interface InstancesOutput {
  instances: HostInstance[]
  unreadable: string[]
}

export interface AccessClientInput {
  address: string
  directive: "allow" | "deny"
}

export interface ProxyHostView {
  id: number
  domains: string[]
  forwardScheme: "http" | "https"
  forwardHost: string
  forwardPort: number
  certificateId: number
  accessListId: number
  sslForced: boolean
  hsts: boolean
  websockets: boolean
  reserved: string
  unclaimed: boolean
}

export interface AccessListView {
  id: number
  name: string
  satisfyAny: boolean
  clients: AccessClientInput[]
  reserved: string
  unclaimed: boolean
}

export interface CertificateView {
  id: number
  name: string
  domains: string[]
  expiresOn: string
  reserved: string
  unclaimed: boolean
}

export interface ProxyHostList {
  items: ProxyHostView[]
}

export interface AccessListList {
  items: AccessListView[]
}

export interface CertificateList {
  items: CertificateView[]
}

export interface AdminPassword {
  email: string
  password: string
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

function isAdminExposure(v: unknown): v is AdminExposure {
  return isRecord(v) && typeof v.hostname === "string" && typeof v.proxyHostId === "number" && typeof v.certificateId === "number"
}

export function isInstanceStatus(v: unknown): v is InstanceStatus {
  return (
    isRecord(v) &&
    typeof v.deployed === "boolean" &&
    typeof v.containerId === "string" &&
    typeof v.image === "string" &&
    typeof v.pinnedImage === "string" &&
    typeof v.running === "boolean" &&
    typeof v.apiReady === "boolean" &&
    (v.public === "no" || v.public === "confirmed") &&
    typeof v.adminEmail === "string" &&
    (v.adminExposure === undefined || v.adminExposure === null || isAdminExposure(v.adminExposure)) &&
    typeof v.upgradeAvailable === "boolean"
  )
}

function isHostInstance(v: unknown): v is HostInstance {
  return isRecord(v) && typeof v.hostId === "string" && typeof v.hostName === "string" && isInstanceStatus(v.status)
}

export function isInstancesOutput(v: unknown): v is InstancesOutput {
  return (
    isRecord(v) &&
    Array.isArray(v.instances) &&
    v.instances.every(isHostInstance) &&
    Array.isArray(v.unreadable) &&
    v.unreadable.every((id) => typeof id === "string")
  )
}
