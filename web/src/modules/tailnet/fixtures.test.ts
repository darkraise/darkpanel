import { expect, test } from "vitest"
import { tnAccount, tnDevice, tnKey, tnPolicy } from "./fixtures"

test("tnAccount is a connected tailnet with read capabilities proven and writes unverified", () => {
  expect(tnAccount()).toEqual({
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
  })
  expect(tnAccount({ tailnet: "other.org" }).tailnet).toBe("other.org")
})

test("tnDevice is an authorized server with no routes and takes overrides", () => {
  expect(tnDevice()).toEqual({
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
  })
  expect(tnDevice({ authorized: false, advertisedRoutes: ["0.0.0.0/0", "::/0"], exitNode: true })).toMatchObject({
    authorized: false,
    advertisedRoutes: ["0.0.0.0/0", "::/0"],
    exitNode: true,
    exitNodeEnabled: false,
  })
})

test("tnKey is a live change key and takes overrides", () => {
  expect(tnKey()).toEqual({
    description: "darkpanel-01JD0000000000000000000001",
    kind: "change",
    ownerId: "01JD0000000000000000000001",
    keyId: "kABC123CNTRL",
    state: "live",
    requestedAt: "2026-10-30T08:00:00Z",
    expires: "2026-10-31T08:00:00Z",
  })
  expect(tnKey({ state: "uncertain", keyId: undefined }).keyId).toBeUndefined()
})

test("tnPolicy is HuJSON with a comment and a trailing comma, and an ETag", () => {
  const p = tnPolicy()
  expect(p.etag).toBe('"d41d8cd98f00b204"')
  expect(p.text).toBe(
    '// Example policy\n{\n  "tagOwners": {"tag:server": ["autogroup:admin"]},\n  "acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],\n}\n',
  )
  expect(tnPolicy({ text: "{}" }).text).toBe("{}")
})

test("every fixture returns fresh arrays", () => {
  tnAccount().tagsAllowed.push("tag:x")
  tnAccount().capabilities.pop()
  tnDevice().addresses.push("x")
  tnDevice().tags.push("x")
  expect(tnAccount().tagsAllowed).toEqual(["tag:server", "tag:ci"])
  expect(tnAccount().capabilities).toHaveLength(4)
  expect(tnDevice().addresses).toHaveLength(2)
  expect(tnDevice().tags).toEqual(["tag:server"])
})
