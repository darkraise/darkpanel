import { expect, test } from "@playwright/test"
import { mockApi } from "./fixtures"

// The M10 boards (spec UI-1 to UI-7), screenshotted at 1440x900 in both
// themes to the untracked boards directory for review.
const BOARDS = {
  users: ["UI-1 Users: default", "UI-1 Users: empty but me", "UI-1 Users: worst case", "UI-1 Users: invite dialog", "UI-1 Users: link shown once"],
  groups: ["UI-2 Host groups"],
  "sign-in": ["UI-3 Sign-in"],
  notifications: ["UI-4 Notifications"],
  alerts: ["UI-5 Alerts"],
  metrics: ["UI-6 Host metrics"],
  login: ["UI-7 Login"],
}

for (const [board, states] of Object.entries(BOARDS)) {
  for (const mode of ["light", "dark"] as const) {
    test(`m10 board ${board} ${mode}`, async ({ page }) => {
      await mockApi(page, { signedIn: false })
      await page.addInitScript((m) => localStorage.setItem("mode", m), mode)
      await page.setViewportSize({ width: 1440, height: 900 })
      await page.goto(`http://127.0.0.1:${process.env.E2E_PORT}/__boards/m10?board=${board}`)
      for (const name of states) {
        await expect(page.getByRole("region", { name })).toBeVisible()
      }
      await expect(page.locator("html")).toHaveAttribute("data-mode", mode)
      if (board === "metrics") {
        // The kit's chart colours arrive in a style element after the first
        // paint; capture once every series has its colour.
        await expect
          .poll(() => page.evaluate(() => [...document.querySelectorAll(".recharts-line-curve, .recharts-area-curve")].map((c) => getComputedStyle(c).stroke)))
          .not.toContain("none")
      }
      const file = mode === "light" ? `m10-${board}.png` : `m10-${board}-dark.png`
      await page.screenshot({ path: `../docs/dforge/ui-ux/boards/${file}`, fullPage: true, animations: "disabled" })
    })
  }
}
