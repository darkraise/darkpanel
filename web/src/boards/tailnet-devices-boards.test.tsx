import { screen, within } from "@testing-library/react"
import { describe, expect, test } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { App } from "@/app"

const signedOut = {
  status: 401,
  body: { type: "about:blank", title: "Authentication required", status: 401, detail: "log in first", code: "auth.required" },
}

const STATES = [
  "Login: remembered",
  "Login: code-ready",
  "Login: neither",
  "Code step: remember line",
  "Devices page: loading",
  "Devices page: default",
  "Devices page: empty",
  "Devices page: error",
  "Devices page: worst case",
  "Devices page: 375 px",
  "Users: devices dialog",
  "Sign-in: Tailscale card",
]

describe("tailnet devices boards", () => {
  test("renders every board state", async () => {
    stubFetch({ "GET /api/v1/auth/session": signedOut })
    renderWithProviders(<App />, { path: "/__boards/tailnet-devices" })
    // The boards chunk loads lazily, which takes seconds on a slow CI runner.
    const regions = await screen.findAllByRole("region", { name: /^(Login|Code step|Devices page|Users|Sign-in):/ }, { timeout: 10_000 })
    expect(regions.map((r) => within(r).getAllByRole("heading", { level: 2 })[0]!.textContent)).toEqual(STATES)
    const worst = screen.getByRole("region", { name: "Devices page: worst case" })
    // 40 device rows and the header row.
    expect(within(worst).getAllByRole("row")).toHaveLength(41)
    expect(within(worst).getByText((_, el) => el?.textContent?.length === 253 && el.tagName === "SPAN")).toBeTruthy()
    expect(within(worst).getByRole("button", { name: "Forget device n2CNTRL2" })).toBeTruthy()
    expect(within(worst).getByText("Unnamed device")).toBeTruthy()
  }, 15_000)
})
