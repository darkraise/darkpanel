import { expect, test } from "@playwright/test"
import { PENDING_HOST_ID, mockApi } from "./fixtures"

const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const EVIDENCE = "../.dforge/ui-ux/public-http-listener"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const tag = `${vp.width}x${vp.height}-${theme}`
    test(`UX-4: Copy works without the clipboard API ${tag}`, async ({ page }) => {
      await page.setViewportSize(vp)
      await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
      await page.addInitScript(() => {
        Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true })
        document.addEventListener("copy", () => {
          const el = document.activeElement
          if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
            document.documentElement.dataset.copied = el.value.slice(el.selectionStart ?? 0, el.selectionEnd ?? 0)
          }
        })
      })
      const fixtures = await mockApi(page, { signedIn: true, worstCase: true })
      await page.goto(url(`/hosts/${PENDING_HOST_ID}`))
      await page.getByRole("button", { name: "New enrollment token" }).click()
      await page.getByLabel("Controller address (optional)").fill("panel.example.com:7443")
      await page.keyboard.press("Enter")
      const change = page.getByRole("dialog")
      await change.getByLabel("Type new-1 to confirm").fill("new-1")
      await change.getByRole("button", { name: "Apply" }).click()
      await expect(page.getByLabel("Enrollment token for new-1")).toHaveValue("dpe_fixture_token")
      await expect(page.getByRole("dialog")).toHaveCount(1)
      const copy = page.getByRole("dialog").getByRole("button", { name: /^Copy/ })
      await copy.click()
      await expect(copy).toHaveAttribute("data-copied", "true")
      await expect(page.locator("html")).toHaveAttribute("data-copied", "dpe_fixture_token")
      await expect(copy).toBeFocused()
      await page.mouse.move(0, 0)
      await page.screenshot({ path: `${EVIDENCE}/UX-4-${tag}.png` })
      expect(fixtures.unhandled).toEqual([])
    })

    test(`UX-4: a failed copy says so ${tag}`, async ({ page }) => {
      await page.setViewportSize(vp)
      await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
      await page.addInitScript(() => {
        Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true })
        document.execCommand = () => false
      })
      const fixtures = await mockApi(page, { signedIn: true, worstCase: true })
      await page.goto(url(`/hosts/${PENDING_HOST_ID}`))
      await page.getByRole("button", { name: "New enrollment token" }).click()
      await page.getByLabel("Controller address (optional)").fill("panel.example.com:7443")
      await page.keyboard.press("Enter")
      const change = page.getByRole("dialog")
      await change.getByLabel("Type new-1 to confirm").fill("new-1")
      await change.getByRole("button", { name: "Apply" }).click()
      const field = page.getByLabel("Enrollment token for new-1")
      await expect(field).toHaveValue("dpe_fixture_token")
      await expect(page.getByRole("dialog")).toHaveCount(1)
      await page.getByRole("dialog").getByRole("button", { name: /^Copy/ }).click()
      await expect(page.getByRole("dialog").getByRole("alert")).toHaveText("Copy failed. Select the text and copy it yourself.")
      await expect(field).toBeEditable({ editable: false })
      await page.mouse.move(0, 0)
      await page.screenshot({ path: `${EVIDENCE}/UX-4-${tag}-failed.png` })
      expect(fixtures.unhandled).toEqual([])
    })
  }
}
