import { readAction } from "@/api/client"
import {
  NPM_ACTION_VERSION,
  type AccessClientInput,
  type AccessListList,
  type AccessListView,
  type CertificateList,
  type CertificateView,
  type ProxyHostList,
  type ProxyHostView,
} from "./types"

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

function isStringArray(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((s) => typeof s === "string")
}

function isListOf<T>(v: unknown, isItem: (item: unknown) => item is T): v is { items: T[] } {
  return isRecord(v) && Array.isArray(v.items) && v.items.every(isItem)
}

function isProxyHostView(v: unknown): v is ProxyHostView {
  return (
    isRecord(v) &&
    typeof v.id === "number" &&
    isStringArray(v.domains) &&
    (v.forwardScheme === "http" || v.forwardScheme === "https") &&
    typeof v.forwardHost === "string" &&
    typeof v.forwardPort === "number" &&
    typeof v.certificateId === "number" &&
    typeof v.accessListId === "number" &&
    typeof v.sslForced === "boolean" &&
    typeof v.hsts === "boolean" &&
    typeof v.websockets === "boolean" &&
    typeof v.reserved === "string" &&
    typeof v.unclaimed === "boolean"
  )
}

function isAccessClient(v: unknown): v is AccessClientInput {
  return isRecord(v) && typeof v.address === "string" && (v.directive === "allow" || v.directive === "deny")
}

function isAccessListView(v: unknown): v is AccessListView {
  return (
    isRecord(v) &&
    typeof v.id === "number" &&
    typeof v.name === "string" &&
    typeof v.satisfyAny === "boolean" &&
    Array.isArray(v.clients) &&
    v.clients.every(isAccessClient) &&
    typeof v.reserved === "string" &&
    typeof v.unclaimed === "boolean"
  )
}

function isCertificateView(v: unknown): v is CertificateView {
  return (
    isRecord(v) &&
    typeof v.id === "number" &&
    typeof v.name === "string" &&
    isStringArray(v.domains) &&
    typeof v.expiresOn === "string" &&
    typeof v.reserved === "string" &&
    typeof v.unclaimed === "boolean"
  )
}

// isProxyHostList reports whether a reply of npm.proxyhost.list has the
// shape the Proxy hosts tab renders.
export function isProxyHostList(v: unknown): v is ProxyHostList {
  return isListOf(v, isProxyHostView)
}

// isAccessListList reports whether a reply of npm.accesslist.list has the
// shape the Access lists tab renders.
export function isAccessListList(v: unknown): v is AccessListList {
  return isListOf(v, isAccessListView)
}

// isCertificateList reports whether a reply of npm.certificate.list has the
// shape the Certificates tab renders.
export function isCertificateList(v: unknown): v is CertificateList {
  return isListOf(v, isCertificateView)
}

// readProxyHostList reads npm.proxyhost.list on a host and refuses a reply
// of another shape.
export function readProxyHostList(hostId: string): Promise<ProxyHostList> {
  return readAction<unknown>("npm.proxyhost.list", NPM_ACTION_VERSION, {}, hostId).then((body) => {
    if (!isProxyHostList(body)) throw new Error("The host answered with an unexpected list of NPM proxy hosts.")
    return body
  })
}

// readAccessListList reads npm.accesslist.list on a host and refuses a reply
// of another shape.
export function readAccessListList(hostId: string): Promise<AccessListList> {
  return readAction<unknown>("npm.accesslist.list", NPM_ACTION_VERSION, {}, hostId).then((body) => {
    if (!isAccessListList(body)) throw new Error("The host answered with an unexpected list of NPM access lists.")
    return body
  })
}

// readCertificateList reads npm.certificate.list on a host and refuses a
// reply of another shape.
export function readCertificateList(hostId: string): Promise<CertificateList> {
  return readAction<unknown>("npm.certificate.list", NPM_ACTION_VERSION, {}, hostId).then((body) => {
    if (!isCertificateList(body)) throw new Error("The host answered with an unexpected list of NPM certificates.")
    return body
  })
}
