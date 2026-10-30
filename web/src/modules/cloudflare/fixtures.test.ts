import { expect, test } from "vitest"
import { CLOUDFLARE_ACTION_VERSION, cloudflareKey } from "./types"
import { cfAccount, cfHostname, cfTunnel } from "./fixtures"

test("fxKeys name the account, tunnels and hostnames queries", () => {
  expect(CLOUDFLARE_ACTION_VERSION).toBe(1)
  expect(cloudflareKey("account")).toEqual(["cloudflare", "account"])
  expect(cloudflareKey("tunnels")).toEqual(["cloudflare", "tunnels"])
  expect(cloudflareKey("hostnames")).toEqual(["cloudflare", "hostnames"])
})

test("fxAccount defaults to a connected account with one capability of each state", () => {
  const a = cfAccount()
  expect(a.accountId).toBe("0123456789abcdef0123456789abcdef")
  expect(a.capabilities.map((c) => c.state)).toEqual(["ok", "missing", "unverified"])
  expect(a.capabilities.find((c) => c.state === "missing")?.detail).toBe("cloud.permission_missing")
  expect(cfAccount({ accountId: "other" }).accountId).toBe("other")
  expect(cfAccount({ accountId: "other" }).connectedAt).toBe(a.connectedAt)
})

test("fxTunnel defaults to a healthy tunnel with one connection and no drift", () => {
  const t = cfTunnel()
  expect(t.name).toMatch(/^darkpanel-[0-9A-Z]{26}$/)
  expect(t.status).toBe("healthy")
  expect(t.connections).toHaveLength(1)
  expect(t.connections[0]).toMatchObject({ colo: "FRA", originIp: "203.0.113.7" })
  expect(t.drift).toBe(false)
  expect(t.hostnames).toBe(1)
  const down = cfTunnel({ status: "down", connections: [], drift: true })
  expect([down.status, down.connections, down.drift, down.name]).toEqual(["down", [], true, t.name])
})

test("fxHostname defaults to an active, healthy hostname of the default tunnel", () => {
  const h = cfHostname()
  expect(h.hostname).toBe("app.example.com")
  expect(h.tunnel).toBe(cfTunnel().id)
  expect([h.phase, h.health]).toEqual(["active", "ok"])
  expect(h.recordId).toBeTruthy()
  const broken = cfHostname({ health: "record_missing", checkedAt: undefined })
  expect([broken.health, broken.checkedAt, broken.service]).toEqual(["record_missing", undefined, h.service])
})
