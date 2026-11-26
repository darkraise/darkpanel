import { expect, test } from "@playwright/test"
import { HOST_ID, mockApi } from "./fixtures"

const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

// UX-2: restart a service with the keyboard alone, through ChangeDialog.
test("restart a service by keyboard", async ({ page }) => {
  const fixtures = await mockApi(page, { signedIn: true, m11: true })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(url(`/hosts/${HOST_ID}/services`))
  const trigger = page.getByRole("button", { name: "Actions for nginx.service", exact: true })
  await expect(trigger).toBeVisible()
  // Tab from the State filter to the first row menus until nginx's has focus.
  await page.getByLabel("State").focus()
  for (let i = 0; i < 12 && !(await trigger.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press("Tab")
  await expect(trigger).toBeFocused()
  await page.keyboard.press("Enter")
  const item = page.getByRole("menuitem", { name: "Restart", exact: true })
  await expect(item).toBeVisible()
  for (let i = 0; i < 6 && !(await item.evaluate((el) => el === document.activeElement || el.hasAttribute("data-highlighted"))); i++) await page.keyboard.press("ArrowDown")
  await page.keyboard.press("Enter")
  const dialog = page.getByRole("dialog", { name: "Restart nginx.service" })
  await expect(dialog).toBeVisible()
  const apply = dialog.getByRole("button", { name: "Apply", exact: true })
  await expect(apply).toBeEnabled()
  await apply.focus()
  await page.keyboard.press("Enter")
  await expect(page.locator(".dr-toast-title", { hasText: "nginx.service restarted" })).toBeVisible()
  expect(fixtures.unhandled.filter((u) => !u.includes("/streams/"))).toEqual([])
})

for (const width of [375, 768, 1440]) {
  test(`the services tab has no sideways scroll at ${width} px`, async ({ page }) => {
    await mockApi(page, { signedIn: true, m11: true, worstCase: true })
    await page.setViewportSize({ width, height: 900 })
    await page.goto(url(`/hosts/${HOST_ID}/services`))
    await expect(page.getByRole("heading", { name: "Journal", exact: true })).toBeVisible()
    await expect(page.getByRole("row").nth(1)).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })
}
