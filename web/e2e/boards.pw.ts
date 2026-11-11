import { expect, test } from "@playwright/test"

const BOARDS = [
  { id: "hosts", states: ["Loading", "Error", "Empty", "Mixed fleet", "Worst case"] },
  { id: "host-overview", states: ["Enrolled, online", "Pending", "Revoked", "Offline", "History empty", "History error"] },
  { id: "rollouts", states: ["Loading", "Empty", "Running", "Failed", "Cancelled", "Worst case"] },
] as const

for (const theme of ["light", "dark"] as const) {
  for (const board of BOARDS) {
    test(`board ${board.id} (${theme}) shows every state`, async ({ page }) => {
      await page.setViewportSize({ width: 1440, height: 900 })
      await page.goto(`http://127.0.0.1:${process.env.E2E_PORT}/boards.html?board=${board.id}&theme=${theme}`)
      const root = page.getByTestId(`board-${board.id}`)
      for (const state of board.states) {
        await expect(root.getByRole("heading", { level: 2, name: state })).toBeVisible()
      }
      if (theme === "light") await root.screenshot({ path: `../docs/dforge/ui-ux/boards/${board.id}.png` })
      else await root.screenshot({ path: `../docs/dforge/ui-ux/boards/${board.id}-dark.png` })
    })
  }
}
