import { expect, test, type Locator, type Page } from "@playwright/test"
import { HOST_ID, mockApi } from "./fixtures"

const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

// tabTo presses Tab until target has focus.
async function tabTo(page: Page, target: Locator, most = 40) {
  for (let i = 0; i < most && !(await target.evaluate((el) => el === document.activeElement)); i++) await page.keyboard.press("Tab")
  await expect(target).toBeFocused()
}

// UI-7, UX-10, UX-11: by keyboard alone, add a job through the step-up and
// ChangeDialog, then set one kernel parameter and confirm it in ChangeDialog.
test("create a job and tune a key", async ({ page }) => {
  const fixtures = await mockApi(page, { signedIn: true, m11: true, jobs: true })
  const posts: string[] = []
  page.on("request", (r) => {
    if (r.method() === "POST") posts.push(new URL(r.url()).pathname)
  })
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto(url(`/hosts/${HOST_ID}/jobs`))
  const add = page.getByRole("button", { name: "Add job", exact: true })
  await expect(add).toBeVisible()
  await page.getByRole("link", { name: "Jobs", exact: true }).last().focus()
  await tabTo(page, add)
  await page.keyboard.press("Enter")

  const form = page.getByRole("dialog", { name: /^Add a job on / })
  await expect(form.getByLabel("Name")).toBeFocused()
  await page.keyboard.type("nightly-backup")
  await tabTo(page, form.getByLabel("Schedule"))
  await page.keyboard.type("daily")
  await expect(form.getByRole("list", { name: "Next runs" }).getByRole("listitem")).toHaveCount(3)
  await tabTo(page, form.getByLabel("Command"))
  await page.keyboard.type("/usr/local/bin/backup --all")
  await tabTo(page, form.getByRole("button", { name: "Review change" }))
  await page.keyboard.press("Enter")

  const stepUp = page.getByRole("dialog", { name: "Enter your authenticator code" })
  await expect(stepUp.getByLabel("Authenticator code")).toBeFocused()
  await page.keyboard.type("123456")
  await page.keyboard.press("Enter")

  const create = page.getByRole("dialog", { name: "Add nightly-backup" })
  const apply = create.getByRole("button", { name: "Apply", exact: true })
  await expect(apply).toBeEnabled()
  await tabTo(page, apply)
  await page.keyboard.press("Enter")
  await expect(page.locator(".dr-toast-title", { hasText: "nightly-backup added" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(create).toBeHidden()
  await expect(page.getByRole("table", { name: "Darkpanel jobs" }).getByText("nightly-backup", { exact: true })).toBeVisible()
  expect(posts.filter((p) => p === "/api/v1/auth/step-up")).toHaveLength(1)

  await page.goto(url(`/hosts/${HOST_ID}/tuning`))
  const somaxconn = page.getByLabel("net.core.somaxconn at boot")
  await expect(somaxconn).toBeVisible()
  await page.getByRole("link", { name: "Tuning", exact: true }).last().focus()
  await tabTo(page, somaxconn)
  await page.keyboard.type("8192")
  const review = page.getByRole("button", { name: "Review changes" })
  await tabTo(page, review)
  await page.keyboard.press("Enter")

  const set = page.getByRole("dialog", { name: "Set net.core.somaxconn" })
  const applySet = set.getByRole("button", { name: "Apply", exact: true })
  await expect(applySet).toBeEnabled()
  await tabTo(page, applySet)
  await page.keyboard.press("Enter")
  const confirm = set.getByRole("button", { name: "Confirm", exact: true })
  await expect(confirm).toBeVisible()
  await tabTo(page, confirm)
  await page.keyboard.press("Enter")
  await expect.poll(() => posts.some((p) => p.endsWith("/confirm"))).toBe(true)
  await expect(confirm).toBeHidden()
  const planned = posts.filter((p) => p.includes("/tuning.sysctl.apply/v1/plan"))
  expect(planned).toHaveLength(1)
  expect(fixtures.unhandled).toEqual([])
})
