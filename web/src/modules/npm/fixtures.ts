import type { AccessListView, CertificateView, InstanceStatus, InstancesOutput, ProxyHostView } from "./types"

// Test fixtures of the NPM pages: one deployed instance and the proxy host,
// access list and certificate of one exposure, with the fields each test
// overrides. They carry no secret.

const IMAGE = "jc21/nginx-proxy-manager:2.13.5@sha256:" + "ab".repeat(32)

export function npInstance(over: Partial<InstanceStatus> = {}): InstanceStatus {
  return {
    deployed: true,
    containerId: "c0ffee00".repeat(8),
    image: IMAGE,
    pinnedImage: IMAGE,
    running: true,
    apiReady: true,
    public: "no",
    adminEmail: "ops@example.com",
    adminExposure: null,
    upgradeAvailable: false,
    ...over,
  }
}

export function npProxyHost(over: Partial<ProxyHostView> = {}): ProxyHostView {
  return {
    id: 1,
    domains: ["app.example.com"],
    forwardScheme: "http",
    forwardHost: "web",
    forwardPort: 8080,
    certificateId: 3,
    accessListId: 0,
    sslForced: true,
    hsts: true,
    websockets: false,
    reserved: "",
    unclaimed: false,
    ...over,
  }
}

export function npAccessList(over: Partial<AccessListView> = {}): AccessListView {
  return {
    id: 2,
    name: "office",
    satisfyAny: false,
    clients: [{ address: "203.0.113.0/24", directive: "allow" }],
    reserved: "",
    unclaimed: false,
    ...over,
  }
}

export function npCertificate(over: Partial<CertificateView> = {}): CertificateView {
  return {
    id: 3,
    name: "app.example.com",
    domains: ["app.example.com"],
    expiresOn: "2027-01-31T10:00:00.000Z",
    reserved: "",
    unclaimed: false,
    ...over,
  }
}

export function npInstances(over: Partial<InstancesOutput> = {}): InstancesOutput {
  return { instances: [{ hostId: "01JD0000000000000000000011", hostName: "edge-1", status: npInstance() }], ...over }
}
