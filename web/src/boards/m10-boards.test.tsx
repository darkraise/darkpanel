import { screen, within } from "@testing-library/react"
import { afterEach, describe, expect, test, vi } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { App } from "@/app"

const signedOut = {
  status: 401,
  body: { type: "about:blank", title: "Authentication required", status: 401, detail: "log in first", code: "auth.required" },
}

const STATES = [
  "UI-1 Users: default",
  "UI-1 Users: empty but me",
  "UI-1 Users: worst case",
  "UI-1 Users: invite dialog",
  "UI-1 Users: link shown once",
  "UI-2 Host groups",
  "UI-3 Sign-in",
  "UI-4 Notifications",
  "UI-5 Alerts",
  "UI-6 Host metrics",
  "UI-7 Login",
]

describe("M10 boards", () => {
  afterEach(() => {
    vi.unstubAllEnvs()
  })

  test("renders every board state", async () => {
    stubFetch({ "GET /api/v1/auth/session": signedOut })
    renderWithProviders(<App />, { path: "/__boards/m10" })
    const regions = await screen.findAllByRole("region")
    expect(regions.map((r) => r.getAttribute("aria-labelledby") && within(r).getByRole("heading", { level: 2 }).textContent)).toEqual(STATES)
    for (const name of STATES) {
      expect(screen.getByRole("region", { name })).toBeTruthy()
    }
    const users = screen.getByRole("region", { name: "UI-1 Users: worst case" })
    expect(within(users).getAllByRole("row")).toHaveLength(51)
    expect(within(users).getByRole("button", { name: "Invite user" })).toBeTruthy()
  })

  test("renders one board when ?board= names it", async () => {
    stubFetch({ "GET /api/v1/auth/session": signedOut })
    renderWithProviders(<App />, { path: "/__boards/m10?board=alerts" })
    expect(await screen.findByRole("region", { name: "UI-5 Alerts" })).toBeTruthy()
    expect(screen.getAllByRole("region")).toHaveLength(1)
  })

  test("is absent from production", async () => {
    vi.stubEnv("DEV", false)
    stubFetch({ "GET /api/v1/auth/session": signedOut })
    renderWithProviders(<App />, { path: "/__boards/m10" })
    expect(await screen.findByRole("heading", { name: /sign in/i })).toBeTruthy()
    expect(screen.queryByRole("region", { name: "UI-1 Users: default" })).toBeNull()
  })
})
