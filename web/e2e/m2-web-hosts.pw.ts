import { expect, test, type Locator, type Page } from "@playwright/test"
import { HOST_ID, LONG_HOST_ID, PENDING_HOST_ID, REVOKED_HOST_ID, mockApi } from "./fixtures"

const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const EVIDENCE = "../.dforge/ui-ux/m2-web-hosts"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`
const LONG_NAME = `web-${"x".repeat(59)}`

async function open(page: Page, theme: (typeof THEMES)[number], path: string) {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  const fixtures = await mockApi(page, { signedIn: true, worstCase: true })
  await page.goto(url(path))
  await expect(page.locator("html")).toHaveAttribute("data-mode", theme)
  return fixtures
}

async function noPageScroll(page: Page) {
  const { scrollWidth, clientWidth } = await page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, clientWidth: document.documentElement.clientWidth }))
  expect(scrollWidth, `scrollWidth ${scrollWidth} against clientWidth ${clientWidth}`).toBeLessThanOrEqual(clientWidth)
}

// Keyboard only: focus moves by Tab from the element focused before, never by a click.
// From the page body it first takes "Skip to content", as a keyboard user does,
// because at 768 px and wider the sidebar lists all 50 worst-case hosts.
async function tabTo(page: Page, target: Locator) {
  if (await page.evaluate(() => document.activeElement === document.body)) {
    await page.keyboard.press("Tab")
    await expect(page.getByRole("link", { name: "Skip to content" })).toBeFocused()
    await page.keyboard.press("Enter")
  }
  for (let i = 0; i < 80; i++) {
    if (await target.evaluate((el) => el === document.activeElement)) return
    await page.keyboard.press("Tab")
  }
  throw new Error("Tab never reached the control")
}

async function applyByKeyboard(page: Page, confirmation: string | null) {
  const dialog = page.getByRole("dialog")
  if (confirmation !== null) {
    await tabTo(page, dialog.getByLabel(`Type ${confirmation} to confirm`))
    await page.keyboard.type(confirmation)
  }
  await tabTo(page, dialog.getByRole("button", { name: "Apply" }))
  await page.keyboard.press("Enter")
  await expect(dialog.getByText("completed", { exact: true })).toBeVisible()
}

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const tag = `${vp.width}x${vp.height}-${theme}`
    test.describe(tag, () => {
      test.use({ viewport: vp })

      test(`UX-1, UX-2, UX-8, UX-12: worst-case pages ${tag}`, async ({ page }) => {
        const fixtures = await open(page, theme, "/")
        await expect(page.getByRole("table").getByRole("row")).toHaveCount(51)
        await expect(page.getByRole("table").getByRole("link", { name: LONG_NAME })).toBeVisible()
        await noPageScroll(page)
        await page.screenshot({ path: `${EVIDENCE}/UI-1-${tag}.png`, fullPage: true })
        await page.goto(url(`/hosts/${HOST_ID}`))
        await expect(page.getByRole("table", { name: "Update history" }).getByRole("row")).toHaveCount(4)
        await noPageScroll(page)
        await page.screenshot({ path: `${EVIDENCE}/UI-2-${tag}.png`, fullPage: true })
        await page.goto(url(`/hosts/${PENDING_HOST_ID}`))
        await expect(page.getByText("The update history is unavailable while the host is offline.")).toBeVisible()
        await page.screenshot({ path: `${EVIDENCE}/UI-2-${tag}-pending.png`, fullPage: true })
        await page.goto(url("/rollouts"))
        await expect(page.getByRole("region", { name: /\((stable|beta)\)$/ })).toHaveCount(20)
        await noPageScroll(page)
        await page.screenshot({ path: `${EVIDENCE}/UI-3-${tag}.png`, fullPage: true })
        expect(fixtures.unhandled, "API requests without a fixture").toEqual([])
      })

      test(`UX-3, UX-11: revoke by keyboard ${tag}`, async ({ page }) => {
        const fixtures = await open(page, theme, `/hosts/${LONG_HOST_ID}`)
        const button = page.getByRole("button", { name: "Revoke host" })
        await tabTo(page, button)
        await page.keyboard.press("Enter")
        await applyByKeyboard(page, LONG_NAME)
        await page.keyboard.press("Escape")
        await expect(button).toBeFocused()
        expect(fixtures.unhandled).toEqual([])
      })

      test(`UX-4: delete by keyboard ${tag}`, async ({ page }) => {
        const fixtures = await open(page, theme, `/hosts/${REVOKED_HOST_ID}`)
        await tabTo(page, page.getByRole("button", { name: "Delete host" }))
        await page.keyboard.press("Enter")
        const dialog = page.getByRole("dialog")
        await tabTo(page, dialog.getByLabel("Type old-1 to confirm"))
        await page.keyboard.type("old-1")
        await tabTo(page, dialog.getByRole("button", { name: "Apply" }))
        await page.keyboard.press("Enter")
        await expect(page).toHaveURL(url("/"))
        expect(fixtures.unhandled).toEqual([])
      })

      test(`UX-5, UX-11: new enrollment token by keyboard ${tag}`, async ({ page }) => {
        const fixtures = await open(page, theme, `/hosts/${PENDING_HOST_ID}`)
        await tabTo(page, page.getByRole("button", { name: "New enrollment token" }))
        await page.keyboard.press("Enter")
        await expect(page.getByLabel("Controller address (optional)")).toBeFocused()
        await page.keyboard.type("panel.example.com:7443")
        await page.keyboard.press("Enter")
        const dialog = page.getByRole("dialog")
        await tabTo(page, dialog.getByLabel("Type new-1 to confirm"))
        await page.keyboard.type("new-1")
        await tabTo(page, dialog.getByRole("button", { name: "Apply" }))
        await page.keyboard.press("Enter")
        // The change dialog closes as the token dialog opens (Task 6), so no "completed" is awaited here.
        await expect(page.getByLabel("Enrollment token for new-1")).toHaveValue("dpe_fixture_token")
        // Wait for the change dialog's exit animation, so the screenshot shows the grant alone.
        await expect(page.getByRole("dialog")).toHaveCount(1)
        const grant = page.getByRole("dialog")
        await expect(grant).toHaveAttribute("data-state", "open")
        await expect(grant.getByRole("button", { name: "Copy" })).toBeVisible()
        await expect(grant.getByText(/run sudo darkpanel agent enroll and paste the token before /)).toBeVisible()
        await expect(grant).toBeInViewport({ ratio: 1 })
        await page.waitForFunction(() => document.getAnimations().length === 0)
        await page.screenshot({ path: `${EVIDENCE}/UX-5-${tag}.png` })
        await page.keyboard.press("Escape")
        await expect(page.getByRole("dialog")).toHaveCount(0)
        await expect(page.getByText("dpe_fixture_token")).toHaveCount(0)
        await expect(page.getByLabel("Enrollment token for new-1")).toHaveCount(0)
        expect(fixtures.unhandled).toEqual([])
      })

      test(`UX-6, UX-11: update policy by keyboard ${tag}`, async ({ page }) => {
        const fixtures = await open(page, theme, `/hosts/${HOST_ID}`)
        await tabTo(page, page.getByRole("button", { name: "Change update policy" }))
        await page.keyboard.press("Enter")
        await expect(page.getByRole("radio", { name: "Stable" })).toBeFocused()
        await page.keyboard.press("ArrowDown")
        await expect(page.getByRole("radio", { name: "Beta" })).toBeChecked()
        await tabTo(page, page.getByRole("checkbox", { name: "Update on a schedule" }))
        await page.keyboard.press("Space")
        await tabTo(page, page.getByRole("checkbox", { name: "Monday" }))
        await page.keyboard.press("Space")
        await tabTo(page, page.getByLabel("Time (UTC)"))
        await page.keyboard.type("03:00")
        await tabTo(page, page.getByRole("button", { name: "Continue" }))
        await page.keyboard.press("Enter")
        await expect(page.getByRole("dialog").getByLabel("Planned changes")).toBeVisible()
        await page.screenshot({ path: `${EVIDENCE}/UX-6-${tag}.png` })
        await applyByKeyboard(page, null)
        expect(fixtures.unhandled).toEqual([])
      })

      test(`UX-7, UX-11: update now by keyboard ${tag}`, async ({ page }) => {
        const fixtures = await open(page, theme, `/hosts/${HOST_ID}`)
        await tabTo(page, page.getByRole("button", { name: "Update now" }))
        await page.keyboard.press("Enter")
        await expect(page.getByLabel("Version")).toBeFocused()
        await page.keyboard.type("v2")
        await page.keyboard.press("Enter")
        await expect(page.getByText("Enter a version such as 1.2.3.")).toBeVisible()
        await expect(page.getByLabel("Version")).toBeFocused()
        await page.keyboard.press("ControlOrMeta+a")
        await page.keyboard.type("1.2.3")
        await page.keyboard.press("Enter")
        await applyByKeyboard(page, null)
        await page.keyboard.press("Escape")
        await expect(page.getByRole("link", { name: "Follow the rollout" })).toBeVisible()
        expect(fixtures.unhandled).toEqual([])
      })

      test(`UX-9, UX-11: start a rollout by keyboard ${tag}`, async ({ page }) => {
        const fixtures = await open(page, theme, "/rollouts")
        const start = page.getByRole("button", { name: "Start rollout" }).first()
        await expect(start).toBeEnabled()
        await tabTo(page, start)
        await page.keyboard.press("Enter")
        await expect(page.getByRole("group", { name: "Hosts" }).getByRole("checkbox").first()).toBeFocused()
        await expect(page.getByRole("dialog").getByRole("button", { name: "Continue" })).toBeInViewport()
        await page.keyboard.press("Space")
        await tabTo(page, page.getByLabel("Version"))
        await page.keyboard.type("1.3.0")
        await tabTo(page, page.getByRole("button", { name: "Continue" }))
        await page.keyboard.press("Enter")
        await applyByKeyboard(page, null)
        expect(fixtures.unhandled).toEqual([])
      })

      test(`UX-10, UX-11: cancel a rollout by keyboard ${tag}`, async ({ page }) => {
        const fixtures = await open(page, theme, "/rollouts")
        await tabTo(page, page.getByRole("button", { name: "Cancel rollout" }))
        await page.keyboard.press("Enter")
        await expect(page.getByRole("checkbox", { name: "Cancel without waiting for the host that is updating" })).toBeFocused()
        await page.keyboard.press("Space")
        await tabTo(page, page.getByRole("button", { name: "Continue" }))
        await page.keyboard.press("Enter")
        await applyByKeyboard(page, null)
        expect(fixtures.unhandled).toEqual([])
      })
    })
  }
}
