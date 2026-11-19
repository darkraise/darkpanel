import { readFileSync } from "node:fs"
import { expect, test } from "@playwright/test"
import { mockApi } from "./fixtures"

test.use({ baseURL: `http://127.0.0.1:${process.env.E2E_PORT ?? "0"}` })

test("A2: first paint is glass/darkpanel before the app bundle runs", async ({ page }) => {
  await mockApi(page, { signedIn: true })
  await page.route(/\/src\/main\.tsx|\/assets\/index-.*\.js/, (r) => r.abort())
  await page.goto("/")
  const attrs = await page.evaluate(() => [document.documentElement.dataset.style, document.documentElement.dataset.preset])
  expect(attrs).toEqual(["glass", "darkpanel"])
})

test("A3: the open theme switcher shows only mode and accent controls", async ({ page }) => {
  await mockApi(page, { signedIn: true })
  await page.goto("/")
  await page.getByRole("button", { name: "Customize theme" }).click()
  await expect(page.getByText("Mode", { exact: true })).toBeVisible()
  await expect(page.getByText("Accent Color", { exact: true })).toBeVisible()
  for (const hidden of ["Surface Color", "Style", "Preset", "Density", "Radius", "Font Size", "Advanced"]) {
    await expect(page.getByText(hidden, { exact: true })).toHaveCount(0)
  }
})

test("A5: the built index.html carries no inline script", () => {
  const html = readFileSync("dist/index.html", "utf8")
  expect(html.match(/<script(?![^>]*\bsrc=)[^>]*>/g) ?? []).toEqual([])
  expect(readFileSync("dist/theme-init.js", "utf8")).toContain("darkpanel-theme-v2")
})
