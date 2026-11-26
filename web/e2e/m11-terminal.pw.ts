import { expect, test } from "@playwright/test"
import { HOST_ID, mockApi, mockTerminal } from "./fixtures"

const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

// UX-8: a code opens the terminal through the bridge; typed text reaches the
// shell and its output comes back; a narrower panel sends a resize.
test("echo through the bridge", async ({ page }) => {
  const fixtures = await mockApi(page, { signedIn: true, m11: true, console: true })
  const terminal = await mockTerminal(page)
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(url(`/hosts/${HOST_ID}/console`))
  const code = page.getByRole("dialog", { name: "Enter your authenticator code" }).getByLabel("Authenticator code")
  await expect(code).toBeFocused()
  await page.keyboard.type("123456")
  await page.keyboard.press("Enter")
  await expect(page.getByRole("toolbar", { name: "Terminal" }).getByText("Connected")).toBeVisible()
  expect(terminal.urls).toHaveLength(1)
  const opened = new URL(terminal.urls[0] ?? "")
  expect(opened.searchParams.get("ticket")).toBe("ticket-fixture")
  expect(terminal.urls[0]).not.toContain("123456")

  await page.keyboard.type("echo hi")
  await page.keyboard.press("Enter")
  const rows = page.locator(".xterm-rows > div")
  await expect(rows.filter({ hasText: /^\$ echo hi\s*$/ })).toHaveCount(1)
  await expect(rows.filter({ hasText: /^hi\s*$/ })).toHaveCount(1)

  const before = terminal.resizes.length
  await page.setViewportSize({ width: 900, height: 700 })
  await expect.poll(() => terminal.resizes.length).toBeGreaterThan(before)
  const last = terminal.resizes[terminal.resizes.length - 1]
  expect(last?.cols).toBeGreaterThan(0)
  expect(last?.rows).toBeGreaterThan(0)

  await page.keyboard.press("Control+Shift+L")
  await expect(page.getByRole("toolbar", { name: "Terminal" })).toBeFocused()
  expect(fixtures.unhandled).toEqual([])
})
