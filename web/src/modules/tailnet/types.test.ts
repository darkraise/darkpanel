import { expect, test } from "vitest"
import {
  TAILNET_ACTION_VERSION,
  tailnetKey,
  type AuthKeyRow,
  type CreatedKey,
  type PolicyEdit,
  type TailnetAccountStatus,
  type TailnetDeviceRoutes,
} from "./types"
import { tnAccount } from "./fixtures"

test("the action version is 1", () => {
  expect(TAILNET_ACTION_VERSION).toBe(1)
})

test("tailnetKey names one group of queries", () => {
  expect(tailnetKey("account")).toEqual(["tailnet", "account"])
  expect(tailnetKey("devices")).toEqual(["tailnet", "devices"])
  expect(tailnetKey("keys")).toEqual(["tailnet", "keys"])
  expect(tailnetKey("policy")).toEqual(["tailnet", "policy"])
})

// Vitest strips types, so this test is checked by `pnpm typecheck`: a
// missing, misspelled or mistyped member of the controller's JSON fails the
// compilation of this file.
test("the wire types carry every member the controller sends", () => {
  const status: TailnetAccountStatus = { connected: true, account: tnAccount() }
  const none: TailnetAccountStatus = { connected: false }
  const routes: TailnetDeviceRoutes = { advertisedRoutes: ["10.0.0.0/24"], enabledRoutes: [] }
  const row: AuthKeyRow = {
    description: "darkpanel-01JD0000000000000000000001",
    kind: "change",
    ownerId: "01JD0000000000000000000001",
    keyId: "k1",
    state: "live",
    requestedAt: "2026-10-30T08:00:00Z",
    expires: "2026-10-31T08:00:00Z",
  }
  const created: CreatedKey = { id: "k1", key: "tskey-auth-secret", expires: "2026-10-31T08:00:00Z", description: row.description }
  const edits: PolicyEdit[] = [
    { op: "set", path: "/derpMap/OmitDefaultRegions", value: true },
    { op: "delete", path: "/derpMap/Regions/900" },
    { op: "addItem", path: "/grants", value: { src: ["*"], dst: ["*"], ip: ["*"] } },
    { op: "removeItem", path: "/grants", value: "x" },
  ]
  expect(status.account?.tailnet).toBe("example.com")
  expect(none.account).toBeUndefined()
  expect([routes.enabledRoutes.length, created.id, edits.map((e) => e.op)]).toEqual([0, "k1", ["set", "delete", "addItem", "removeItem"]])
})
