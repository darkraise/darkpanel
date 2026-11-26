import { expect, test } from "@playwright/test"
import { mockApi } from "./fixtures"

// The M11 boards (spec UI-1 to UI-8), screenshotted at 1440x900 in the light
// theme as m11-<board>.png to BOARDS_DIR, or to the Playwright results.
const BOARDS = ["services", "journal", "users", "fail2ban", "console", "jobs", "tuning"]
const OUT = process.env.BOARDS_DIR ?? "node_modules/.playwright-results/boards"

for (const board of BOARDS) {
  test(`m11 board ${board}`, async ({ page }) => {
    await mockApi(page, { signedIn: false })
    await page.addInitScript(() => localStorage.setItem("mode", "light"))
    await page.setViewportSize({ width: 1440, height: 900 })
    await page.goto(`http://127.0.0.1:${process.env.E2E_PORT}/__boards/m11?board=${board}`)
    await expect(page.getByTestId(`m11-board-${board}`)).toBeVisible()
    await expect(page.locator("html")).toHaveAttribute("data-mode", "light")
    await page.screenshot({ path: `${OUT}/m11-${board}.png`, fullPage: true, animations: "disabled" })
  })
}
