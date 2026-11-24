import { describe, expect, test } from "vitest"
import type { Scope } from "@/api/types"
import { adminSession, memberSession } from "@/test/utils"
import { canOnHost, isAdmin } from "./can"

describe("canOnHost", () => {
  const cases: { row: Scope | ""; need: Scope; want: boolean }[] = [
    { row: "read", need: "read", want: true },
    { row: "read", need: "operate", want: false },
    { row: "read", need: "admin", want: false },
    { row: "operate", need: "read", want: true },
    { row: "operate", need: "operate", want: true },
    { row: "operate", need: "admin", want: false },
    { row: "admin", need: "admin", want: true },
    { row: "", need: "read", want: false },
  ]
  test.each(cases)("a $row row allows $need: $want", ({ row, need, want }) => {
    expect(canOnHost({ scope: row }, need, memberSession())).toBe(want)
  })

  test("an admin session allows every scope on any row", () => {
    for (const need of ["read", "operate", "admin"] as const) expect(canOnHost({ scope: "" }, need, adminSession)).toBe(true)
  })
})

describe("isAdmin", () => {
  test("is admin access on everything, not the admin role alone", () => {
    expect(isAdmin(adminSession)).toBe(true)
    expect(isAdmin({ ...adminSession, user: { ...adminSession.user, allHosts: "read" } })).toBe(false)
    expect(isAdmin(memberSession("operate"))).toBe(false)
    expect(isAdmin(null)).toBe(false)
  })
})
