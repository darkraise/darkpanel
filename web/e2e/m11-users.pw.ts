import { expect, test, type Locator, type Page } from "@playwright/test"
import { HOST_ID, mockApi } from "./fixtures"

const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

// ed25519Line is a well-formed ed25519 public key line.
function ed25519Line(comment: string): string {
  const type = Buffer.from("ssh-ed25519")
  const blob = Buffer.alloc(4 + type.length + 4 + 32, 7)
  blob.writeUInt32BE(type.length, 0)
  type.copy(blob, 4)
  blob.writeUInt32BE(32, 4 + type.length)
  return `ssh-ed25519 ${blob.toString("base64")} ${comment}`
}

// tabTo presses Tab until target has focus.
async function tabTo(page: Page, target: Locator, most = 30) {
  for (let i = 0; i < most && !(await target.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press("Tab")
  await expect(target).toBeFocused()
}

// UX-4: add a user with a key by keyboard alone, through the step-up dialog
// and ChangeDialog, then open the user sheet that shows the key.
test("add a user with a key", async ({ page }) => {
  const fixtures = await mockApi(page, { signedIn: true, m11: true })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(url(`/hosts/${HOST_ID}/users`))
  const add = page.getByRole("button", { name: "Add user", exact: true })
  await expect(add).toBeVisible()
  await page.getByRole("link", { name: "Users and SSH", exact: true }).last().focus()
  await tabTo(page, add)
  await page.keyboard.press("Enter")

  const form = page.getByRole("dialog", { name: "Add a user" })
  await expect(form.getByLabel("Name")).toBeFocused()
  await page.keyboard.type("carol")
  await tabTo(page, form.getByLabel("Public key (optional)"))
  await page.keyboard.type(ed25519Line("carol@laptop"))
  await tabTo(page, form.getByRole("button", { name: "Review change" }))
  await page.keyboard.press("Enter")

  const stepUp = page.getByRole("dialog", { name: "Enter your authenticator code" })
  await expect(stepUp.getByLabel("Authenticator code")).toBeFocused()
  await page.keyboard.type("123456")
  await page.keyboard.press("Enter")

  const change = page.getByRole("dialog", { name: "Add user carol" })
  const apply = change.getByRole("button", { name: "Apply", exact: true })
  await expect(apply).toBeEnabled()
  await tabTo(page, apply)
  await page.keyboard.press("Enter")
  await expect(page.locator(".dr-toast-title", { hasText: "carol added" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(change).toBeHidden()

  const row = page.getByRole("button", { name: "carol", exact: true })
  await expect(row).toBeVisible()
  await page.getByRole("link", { name: "Users and SSH", exact: true }).last().focus()
  await tabTo(page, row, 60)
  await page.keyboard.press("Enter")
  const sheet = page.getByRole("dialog", { name: "carol" })
  const keys = sheet.getByRole("table", { name: "Authorized keys" })
  await expect(keys.getByText("carol@laptop")).toBeVisible()
  await expect(keys.getByText(/^SHA256:[A-Za-z0-9+/]{43}$/)).toBeVisible()
  expect(fixtures.unhandled).toEqual([])
})

for (const width of [375, 768, 1440]) {
  test(`the users tab has no sideways scroll at ${width} px`, async ({ page }) => {
    await mockApi(page, { signedIn: true, m11: true, worstCase: true })
    await page.setViewportSize({ width, height: 900 })
    await page.goto(url(`/hosts/${HOST_ID}/users`))
    await expect(page.getByRole("heading", { name: "SSH daemon", exact: true })).toBeVisible()
    await expect(page.getByRole("row").nth(1)).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  })
}
