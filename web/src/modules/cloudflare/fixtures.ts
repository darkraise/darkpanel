import type { CloudflareAccountView, HostnameView, TunnelView } from "./types"

// Test fixtures of the Cloudflare pages: a connected account, one healthy
// tunnel with a single connection and one active hostname on it, with the
// fields each test overrides.

export function cfAccount(over: Partial<CloudflareAccountView> = {}): CloudflareAccountView {
  return {
    accountId: "0123456789abcdef0123456789abcdef",
    capabilities: [
      { name: "Cloudflare Tunnel Read", state: "ok" },
      { name: "Zone Read", state: "missing", detail: "cloud.permission_missing" },
      { name: "DNS Write", state: "unverified" },
    ],
    connectedAt: "2026-10-30T12:00:00Z",
    ...over,
  }
}

export function cfTunnel(over: Partial<TunnelView> = {}): TunnelView {
  return {
    id: "9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6f",
    name: "darkpanel-01JD0000000000000000000001",
    status: "healthy",
    connections: [
      { id: "c0a80101-0000-4000-8000-000000000001", colo: "FRA", originIp: "203.0.113.7", openedAt: "2026-10-30T12:05:00Z", version: "2026.10.0" },
    ],
    drift: false,
    hostnames: 1,
    createdAt: "2026-10-30T12:00:00Z",
    ...over,
  }
}

export function cfHostname(over: Partial<HostnameView> = {}): HostnameView {
  return {
    hostname: "app.example.com",
    tunnel: "9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6f",
    service: "http://172.18.0.2:8080",
    recordId: "r1a2b3c4d5e6f708192a3b4c5d6e7f801",
    phase: "active",
    health: "ok",
    checkedAt: "2026-10-30T12:10:00Z",
    ...over,
  }
}
