import { expect, test } from "@playwright/test"
import { HOST_ID, mockApi } from "./fixtures"

const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

// UX-9: by keyboard alone, from the path bar to the editor and Save; the
// read asks the code once and the save rides the same step-up window.
test("edit and save a file", async ({ page }) => {
  const fixtures = await mockApi(page, { signedIn: true, m11: true, console: true })
  const stepUps: string[] = []
  const writes: string[] = []
  page.on("request", (r) => {
    if (r.url().endsWith("/api/v1/auth/step-up")) stepUps.push(r.url())
    if (r.url().includes("/files/write")) writes.push(r.postData() ?? "")
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(url(`/hosts/${HOST_ID}/console?path=/`))
  await expect(page.getByRole("tab", { name: "Files", selected: true })).toBeVisible()

  const path = page.getByLabel("Path", { exact: true })
  await path.focus()
  await page.keyboard.press("ControlOrMeta+A")
  await page.keyboard.type("/etc")
  await page.keyboard.press("Enter")
  await expect(page.getByRole("table", { name: "Files in /etc" })).toBeVisible()
  await expect(page).toHaveURL(/[?&]path=%2Fetc$/)

  const actions = page.getByRole("button", { name: "Actions for hosts" })
  for (let i = 0; i < 40 && !(await actions.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press("Tab")
  await expect(actions).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("menuitem", { name: "Download" })).toBeFocused()
  await page.keyboard.press("ArrowDown")
  await expect(page.getByRole("menuitem", { name: "Edit" })).toBeFocused()
  await page.keyboard.press("Enter")

  const code = page.getByRole("dialog", { name: "Enter your authenticator code" }).getByLabel("Authenticator code")
  await expect(code).toBeFocused()
  await page.keyboard.type("123456")
  await page.keyboard.press("Enter")

  const editor = page.getByRole("textbox", { name: "/etc/hosts", exact: true })
  await expect(editor).toBeFocused()
  await expect(editor).toHaveValue("127.0.0.1 localhost\n127.0.1.1 web-1\n")
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.type("10.0.0.12 db-1\n")
  await page.keyboard.press("Tab")
  await expect(page.getByRole("button", { name: "Save" })).toBeFocused()
  await page.keyboard.press("Enter")

  await expect(page.locator(".dr-toast-title", { hasText: "Saved /etc/hosts" })).toBeVisible()
  expect(writes).toEqual(["127.0.0.1 localhost\n127.0.1.1 web-1\n10.0.0.12 db-1\n"])
  expect(stepUps).toHaveLength(1)
  await expect(page.getByText("Unsaved changes")).toHaveCount(0)
  expect(fixtures.unhandled).toEqual([])
})
