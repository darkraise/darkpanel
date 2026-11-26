import { screen, within } from "@testing-library/react"
import { describe, expect, test } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { App } from "@/app"

const signedOut = {
  status: 401,
  body: { type: "about:blank", title: "Authentication required", status: 401, detail: "log in first", code: "auth.required" },
}

const STATES = [
  "UI-1 Services: default",
  "UI-1 Services: failed units",
  "UI-1 Services: loading, error, filtered empty",
  "UI-1 Services: worst case",
  "UI-1 Services: protected unit menu",
  "UI-2 Journal: following",
  "UI-2 Journal: paused",
  "UI-2 Journal: reconnecting",
  "UI-2 Journal: empty",
  "UI-3 Users and SSH: default",
  "UI-3 Users and SSH: add user dialog",
  "UI-3 Users and SSH: user sheet",
  "UI-3 Users and SSH: hardening form with guard error",
  "UI-4 Intrusion prevention: not installed",
  "UI-4 Intrusion prevention: installed",
  "UI-4 Intrusion prevention: CrowdSec present",
  "UI-4 Intrusion prevention: worst case",
  "UI-5 Console: off",
  "UI-5 Console: terminal connected",
  "UI-5 Console: terminal closed",
  "UI-6 Console: step-up dialog",
  "UI-5 Console: files",
  "UI-5 Console: editor",
  "UI-5 Console: conflict",
  "UI-5 Console: upload progress",
  "UI-7 Jobs: off",
  "UI-7 Jobs: empty",
  "UI-7 Jobs: default",
  "UI-7 Jobs: form with invalid schedule",
  "UI-7 Jobs: worst case",
  "UI-8 Tuning: default",
  "UI-8 Tuning: locked key",
  "UI-8 Tuning: override warning",
  "UI-8 Tuning: unsupported filesystem",
]

describe("M11 boards", () => {
  test("renders every board state", async () => {
    stubFetch({ "GET /api/v1/auth/session": signedOut })
    renderWithProviders(<App />, { path: "/__boards/m11" })
    // The boards chunk loads lazily, which takes seconds on a slow CI runner.
    const regions = await screen.findAllByRole("region", {}, { timeout: 10_000 })
    expect(regions.map((r) => within(r).getByRole("heading", { level: 2 }).textContent)).toEqual(STATES)
    const worst = screen.getByRole("region", { name: "UI-1 Services: worst case" })
    expect(within(worst).getByText("Showing 6 of 400 units")).toBeTruthy()
    const stepUp = screen.getByRole("region", { name: "UI-6 Console: step-up dialog" })
    expect(within(stepUp).getByText("Opening a terminal needs your authenticator code")).toBeTruthy()
    expect(within(stepUp).getByRole("button", { name: "Verify" })).toBeTruthy()
  }, 15_000)
})
