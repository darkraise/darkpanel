import type { AuthKeyRow, TailnetAccountView, TailnetDevice, TailnetPolicy } from "./types"

// Test fixtures of the tailnet pages: a connected tailnet with one
// authorized server, one live auth key and a small HuJSON policy, with the
// fields each test overrides.

export function tnAccount(over: Partial<TailnetAccountView> = {}): TailnetAccountView {
  return {
    tailnet: "example.com",
    clientId: "k7Zq3XexampleCNTRL",
    tagsAllowed: ["tag:server", "tag:ci"],
    capabilities: [
      { name: "devices:read", state: "ok" },
      { name: "policy:read", state: "ok" },
      { name: "devices:write", state: "unverified" },
      { name: "auth_keys:write", state: "unverified" },
    ],
    connectedAt: "2026-10-20T09:00:00Z",
    ...over,
  }
}

export function tnDevice(over: Partial<TailnetDevice> = {}): TailnetDevice {
  return {
    id: "nWeb1CNTRL",
    name: "web-1.tail1234.ts.net",
    hostname: "web-1",
    os: "linux",
    user: "admin@example.com",
    addresses: ["100.64.0.1", "fd7a:115c:a1e0::1"],
    tags: ["tag:server"],
    authorized: true,
    lastSeen: "2026-10-30T08:00:00Z",
    expires: "2027-04-28T08:00:00Z",
    keyExpiryDisabled: false,
    advertisedRoutes: [],
    enabledRoutes: [],
    exitNode: false,
    exitNodeEnabled: false,
    ...over,
  }
}

export function tnKey(over: Partial<AuthKeyRow> = {}): AuthKeyRow {
  return {
    description: "darkpanel-01JD0000000000000000000001",
    kind: "change",
    ownerId: "01JD0000000000000000000001",
    keyId: "kABC123CNTRL",
    state: "live",
    requestedAt: "2026-10-30T08:00:00Z",
    expires: "2026-10-31T08:00:00Z",
    ...over,
  }
}

export function tnPolicy(over: Partial<TailnetPolicy> = {}): TailnetPolicy {
  return {
    text: '// Example policy\n{\n  "tagOwners": {"tag:server": ["autogroup:admin"]},\n  "acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],\n}\n',
    etag: '"d41d8cd98f00b204"',
    ...over,
  }
}
