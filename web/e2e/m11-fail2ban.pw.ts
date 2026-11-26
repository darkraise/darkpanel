import { expect, test, type Locator, type Page } from "@playwright/test"
import { HOST_ID, mockApi } from "./fixtures"

const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

async function tabTo(page: Page, target: Locator, most = 40) {
  for (let i = 0; i < most && !(await target.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press("Tab")
  await expect(target).toBeFocused()
}

// UX-6: an operator unbans an address with the keyboard alone, through
// ChangeDialog with no typed phrase, and its row goes.
test("unban by keyboard", async ({ page }) => {
  const fixtures = await mockApi(page, { signedIn: true, m11: "operate" })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(url(`/hosts/${HOST_ID}/fail2ban`))
  const unban = page.getByRole("button", { name: "Unban 198.51.100.201 from sshd", exact: true })
  await expect(unban).toBeVisible()
  await expect(page.getByRole("heading", { name: "Settings" })).toHaveCount(0)
  await page.getByRole("link", { name: "Intrusion prevention", exact: true }).last().focus()
  await tabTo(page, unban)
  await page.keyboard.press("Enter")
  const dialog = page.getByRole("dialog", { name: "Unban 198.51.100.201" })
  const apply = dialog.getByRole("button", { name: "Apply", exact: true })
  await expect(apply).toBeEnabled()
  await expect(dialog.getByLabel(/to confirm$/)).toHaveCount(0)
  await tabTo(page, apply)
  await page.keyboard.press("Enter")
  await expect(page.locator(".dr-toast-title", { hasText: "198.51.100.201 unbanned" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(dialog).toBeHidden()
  const table = page.getByRole("table", { name: "Banned addresses" })
  await expect(table.getByText("198.51.100.201", { exact: true })).toHaveCount(0)
  await expect(table.getByText("192.0.2.44", { exact: true }).first()).toBeVisible()
  expect(fixtures.unhandled).toEqual([])
})

for (const width of [375, 768, 1440]) {
  test(`the intrusion prevention tab has no sideways scroll at ${width} px`, async ({ page }) => {
    await mockApi(page, { signedIn: true, m11: true, worstCase: true })
    await page.setViewportSize({ width, height: 900 })
    await page.goto(url(`/hosts/${HOST_ID}/fail2ban`))
    await expect(page.getByRole("heading", { name: "Settings", exact: true })).toBeVisible()
    await expect(page.getByRole("table", { name: "Banned addresses" }).getByRole("row").nth(1)).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })
}
