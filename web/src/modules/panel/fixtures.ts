import type { ExposureView, StatusOutput } from "./types"

// paExposure is a live exposure of the panel through NPM; the overrides change it.
export function paExposure(over: Partial<ExposureView> = {}): ExposureView {
  return {
    id: "01JC0000000000000000000001",
    via: "npm",
    hostname: "panel.example.org",
    hostId: "h1",
    subnet: "172.31.255.248/29",
    gateway: "172.31.255.249",
    proxy: "172.31.255.250",
    port: 9443,
    state: "open",
    listenerId: "l1",
    createdAt: "2026-11-04T12:00:00Z",
    ...over,
  }
}

// paStatus is the answer of panel.exposure.status for the exposure, or for none.
export function paStatus(exposure?: ExposureView): StatusOutput {
  return exposure === undefined ? { exposed: false } : { exposed: true, exposure }
}
